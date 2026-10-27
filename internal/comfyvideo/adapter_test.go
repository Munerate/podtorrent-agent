package comfyvideo_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Munerate/podtorrent-agent/internal/comfyvideo"
)

const workflow = `{
  "3": {"class_type": "TextEncode", "inputs": {"text": "{{prompt}}"}},
  "5": {"class_type": "EmptyVideoLatent", "inputs": {"width": "{{width}}", "height": "{{height}}", "length": "{{frames}}"}},
  "7": {"class_type": "Sampler", "inputs": {"seed": "{{seed}}", "note": "style: {{prompt}}"}}
}`

func TestFill(t *testing.T) {
	v, err := comfyvideo.Fill(json.RawMessage(workflow), map[string]any{"prompt": "a lighthouse", "width": 832, "height": 480, "frames": 121, "seed": int64(7)})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	for _, want := range []string{`"text":"a lighthouse"`, `"width":832`, `"height":480`, `"length":121`, `"seed":7`, `"note":"style: a lighthouse"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("filled workflow lacks %s: %s", want, b)
		}
	}
}

func cfg(c comfyvideo.Config) comfyvideo.Config {
	c.Workflows, c.Model, c.Sizes, c.MinSecs, c.MaxSecs, c.DefSecs, c.FPS = map[string]json.RawMessage{"best": json.RawMessage(workflow)}, "example/video-1", []string{"832x480", "640x640"}, 5, 15, 5, 24
	return c
}

func TestFrames(t *testing.T) {
	a, _ := comfyvideo.New(cfg(comfyvideo.Config{}))
	if got := a.Frames(5); got != 120 {
		t.Errorf("frames = %d", got)
	}
	a, _ = comfyvideo.New(cfg(comfyvideo.Config{FrameMultiple: 8}))
	if got := a.Frames(5); got != 121 {
		t.Errorf("frames 8n+1 = %d", got)
	}
}

func TestConfigChecks(t *testing.T) {
	c := cfg(comfyvideo.Config{})
	c.Workflows = map[string]json.RawMessage{"best": json.RawMessage(`{"nodes": [], "links": []}`)}
	if _, err := comfyvideo.New(c); err == nil {
		t.Error("UI-format workflow without placeholders accepted")
	}
	c.Workflows = map[string]json.RawMessage{"best": json.RawMessage(`{"nodes": [], "x": "{{prompt}}"}`)}
	if _, err := comfyvideo.New(c); err == nil || !strings.Contains(err.Error(), "Export (API)") {
		t.Errorf("UI-format workflow: %v", err)
	}
}

func TestRequestChecks(t *testing.T) {
	a, _ := comfyvideo.New(cfg(comfyvideo.Config{ComfyURL: "http://127.0.0.1:1"}))
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	for body, want := range map[string]int{
		`{"model":"example/video-1","prompt":"x","seconds":"4"}`:     400,
		`{"model":"example/video-1","prompt":"x","size":"1280x720"}`: 400,
		`{"model":"other/model","prompt":"x"}`:                       400,
		`{"model":"example/video-1","prompt":""}`:                    400,
		`{"model":"example/video-1","prompt":"x","quality":"turbo"}`: 400,
		`{"model":"example/video-1","prompt":"x","seconds":"6"}`:     502, // valid, but ComfyUI is down
	} {
		resp, err := http.Post(srv.URL+"/v1/videos", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s: status %d, want %d", body, resp.StatusCode, want)
		}
	}
	resp, _ := http.Get(srv.URL + "/v1/models")
	var m struct{ Data []struct{ ID string } }
	_ = json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	if len(m.Data) != 1 || m.Data[0].ID != "example/video-1" {
		t.Errorf("models without ComfyUI: %+v", m)
	}
}

// Jobs survive a restart through the state file, and ended jobs past Keep
// are forgotten with their clip deleted from the output folder.
func TestStateAndPrune(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "jobs.json")
	out := filepath.Join(dir, "output")
	_ = os.MkdirAll(filepath.Join(out, "video"), 0o755)
	clip := filepath.Join(out, "video", "H3_00001.mp4")
	_ = os.WriteFile(clip, []byte("clip"), 0o644)
	old := time.Now().Add(-48 * time.Hour).UTC()
	saved := []map[string]any{
		{"id": "video_old", "prompt_id": "p0", "model": "example/video-1", "seconds": 5, "size": "832x480", "mode": "best",
			"created": old, "status": "completed", "finished": old,
			"file": map[string]string{"filename": "H3_00001.mp4", "subfolder": "video", "type": "output"}},
		{"id": "video_new", "prompt_id": "p1", "model": "example/video-1", "seconds": 5, "size": "832x480", "mode": "best",
			"created": time.Now().UTC(), "status": "failed", "finished": time.Now().UTC(), "error": "ComfyUI reported an error"},
	}
	b, _ := json.Marshal(saved)
	_ = os.WriteFile(state, b, 0o600)

	a, err := comfyvideo.New(cfg(comfyvideo.Config{StatePath: state, OutputDir: out, Keep: time.Hour}))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/v1/videos/video_new")
	var v struct {
		Status string
		Error  struct{ Message string }
	}
	_ = json.NewDecoder(resp.Body).Decode(&v)
	resp.Body.Close()
	if v.Status != "failed" || v.Error.Message == "" {
		t.Errorf("restored job: %+v", v)
	}
	resp, _ = http.Get(srv.URL + "/v1/videos/video_old")
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("expired job: %d", resp.StatusCode)
	}
	if _, err := os.Stat(clip); !os.IsNotExist(err) {
		t.Errorf("expired clip not deleted: %v", err)
	}
	if b, _ := os.ReadFile(state); strings.Contains(string(b), "video_old") || !strings.Contains(string(b), "video_new") {
		t.Errorf("state after prune: %s", b)
	}
}
