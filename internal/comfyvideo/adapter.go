// Package comfyvideo serves the video job API in spec/protocol-v0.md
// ("Video") in front of a ComfyUI server, so a video model that runs as a
// ComfyUI workflow can join the network without changes to ComfyUI.
//
// The operator exports the model's workflow in ComfyUI's API format and
// marks the inputs with placeholders (see Fill). Each POST /v1/videos queues
// the filled workflow with POST /prompt; status comes from /history and
// /queue, and the clip from /view. Prompts and clips are never logged or
// stored here; ComfyUI keeps its outputs as it always does.
package comfyvideo

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	ComfyURL string          // e.g. http://127.0.0.1:8188
	Workflow json.RawMessage // API-format workflow with placeholders
	Model    string          // model id served at /v1/models
	Sizes    []string        // allowed "WxH"; the first is the default
	MinSecs  float64
	MaxSecs  float64
	DefSecs  float64 // when the request has no seconds
	FPS      int
	// FrameMultiple > 0 makes frames = n*FrameMultiple + 1, the nearest count
	// to seconds*FPS (many video models need 4n+1 or 8n+1 frames).
	FrameMultiple int
	Keep          time.Duration // forget jobs (their content then 404s) after this
	HTTPClient    *http.Client
	Log           *slog.Logger
}

type job struct {
	id, prompt string // prompt: ComfyUI's prompt_id, not the user's text
	model      string
	seconds    float64
	size       string
	created    time.Time

	// Set once the job ended.
	status string // "", "completed" or "failed"
	file   *output
}

type output struct{ Filename, Subfolder, Type string }

type Adapter struct {
	cfg  Config
	mu   sync.Mutex
	jobs map[string]*job
}

// New checks the workflow and returns an adapter.
func New(cfg Config) (*Adapter, error) {
	if !bytes.Contains(cfg.Workflow, []byte("{{prompt}}")) {
		return nil, errors.New(`workflow has no "{{prompt}}" placeholder`)
	}
	var w map[string]any
	if err := json.Unmarshal(cfg.Workflow, &w); err != nil {
		return nil, fmt.Errorf("workflow: %w", err)
	}
	if _, ok := w["nodes"]; ok {
		return nil, errors.New("workflow is in ComfyUI's UI format; export it with \"Export (API)\"")
	}
	if len(cfg.Sizes) == 0 {
		return nil, errors.New("no sizes")
	}
	for _, s := range cfg.Sizes {
		if _, _, err := parseSize(s); err != nil {
			return nil, err
		}
	}
	if cfg.FPS <= 0 || cfg.MinSecs <= 0 || cfg.MaxSecs < cfg.MinSecs || cfg.DefSecs < cfg.MinSecs || cfg.DefSecs > cfg.MaxSecs {
		return nil, errors.New("need fps > 0 and 0 < min seconds <= default <= max")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Keep <= 0 {
		cfg.Keep = 24 * time.Hour
	}
	cfg.ComfyURL = strings.TrimRight(cfg.ComfyURL, "/")
	return &Adapter{cfg: cfg, jobs: map[string]*job{}}, nil
}

func (a *Adapter) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"object": "list",
			"data": []map[string]string{{"id": a.cfg.Model, "object": "model", "owned_by": "comfyui"}}})
	})
	mux.HandleFunc("POST /v1/videos", a.submit)
	mux.HandleFunc("GET /v1/videos/{id}", func(w http.ResponseWriter, r *http.Request) {
		j := a.job(w, r)
		if j == nil {
			return
		}
		a.refresh(r.Context(), j)
		writeJSON(w, http.StatusOK, a.view(j))
	})
	mux.HandleFunc("GET /v1/videos/{id}/content", a.content)
	return mux
}

func apiError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func parseSize(s string) (int, int, error) {
	ws, hs, ok := strings.Cut(s, "x")
	wi, err1 := strconv.Atoi(ws)
	hi, err2 := strconv.Atoi(hs)
	if !ok || err1 != nil || err2 != nil || wi <= 0 || hi <= 0 {
		return 0, 0, fmt.Errorf("size %q is not WIDTHxHEIGHT", s)
	}
	return wi, hi, nil
}

// Frames is the frame count for a clip of secs seconds.
func (a *Adapter) Frames(secs float64) int {
	n := secs * float64(a.cfg.FPS)
	if m := a.cfg.FrameMultiple; m > 0 {
		return int(math.Round(n/float64(m)))*m + 1
	}
	return int(math.Round(n))
}

func (a *Adapter) submit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model   string          `json:"model"`
		Prompt  string          `json:"prompt"`
		Seconds json.RawMessage `json:"seconds"`
		Size    string          `json:"size"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "body must be JSON")
		return
	}
	if req.Model != "" && req.Model != a.cfg.Model {
		apiError(w, http.StatusBadRequest, fmt.Sprintf("model %q is not served here", req.Model))
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		apiError(w, http.StatusBadRequest, "prompt is required")
		return
	}
	secs := a.cfg.DefSecs
	if s := strings.Trim(string(req.Seconds), `"`); s != "" && s != "null" {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || v < a.cfg.MinSecs || v > a.cfg.MaxSecs {
			apiError(w, http.StatusBadRequest, fmt.Sprintf("seconds must be from %g to %g", a.cfg.MinSecs, a.cfg.MaxSecs))
			return
		}
		secs = v
	}
	size := req.Size
	if size == "" {
		size = a.cfg.Sizes[0]
	}
	if !slices.Contains(a.cfg.Sizes, size) {
		apiError(w, http.StatusBadRequest, "size must be one of "+strings.Join(a.cfg.Sizes, ", "))
		return
	}
	width, height, _ := parseSize(size)
	seed, _ := rand.Int(rand.Reader, big.NewInt(1<<48))
	wf, err := Fill(a.cfg.Workflow, map[string]any{
		"prompt": req.Prompt, "width": width, "height": height, "seconds": secs, "fps": a.cfg.FPS,
		"frames": a.Frames(secs), "seed": seed.Int64(),
	})
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body, _ := json.Marshal(map[string]any{"prompt": wf, "client_id": "podtorrent-comfy-video"})
	var queued struct {
		PromptID string `json:"prompt_id"`
	}
	if status, err := a.comfy(r.Context(), http.MethodPost, "/prompt", body, &queued); err != nil || queued.PromptID == "" {
		a.cfg.Log.Warn("ComfyUI refused the workflow", "status", status, "err", err)
		apiError(w, http.StatusBadGateway, "ComfyUI refused the job; check the workflow and that ComfyUI is running")
		return
	}
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	j := &job{id: "video_" + hex.EncodeToString(b), prompt: queued.PromptID, model: a.cfg.Model, seconds: secs, size: size, created: time.Now()}
	a.mu.Lock()
	a.prune()
	a.jobs[j.id] = j
	a.mu.Unlock()
	a.cfg.Log.Info("job queued", "id", j.id, "seconds", secs, "size", size)
	v := a.view(j)
	v["status"] = "queued"
	writeJSON(w, http.StatusOK, v)
}

// Fill returns the workflow with placeholders replaced. A string value that
// is exactly "{{name}}" becomes the value itself (a number for width,
// height, seconds, fps, frames and seed); "{{prompt}}" inside a longer string
// is replaced in place.
func Fill(workflow json.RawMessage, vals map[string]any) (any, error) {
	var w any
	if err := json.Unmarshal(workflow, &w); err != nil {
		return nil, err
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case map[string]any:
			for k, x := range t {
				t[k] = walk(x)
			}
		case []any:
			for i, x := range t {
				t[i] = walk(x)
			}
		case string:
			if strings.HasPrefix(t, "{{") && strings.HasSuffix(t, "}}") {
				if x, ok := vals[strings.TrimSpace(t[2:len(t)-2])]; ok {
					return x
				}
			}
			if p, ok := vals["prompt"].(string); ok {
				return strings.ReplaceAll(t, "{{prompt}}", p)
			}
		}
		return v
	}
	return walk(w), nil
}

func (a *Adapter) job(w http.ResponseWriter, r *http.Request) *job {
	a.mu.Lock()
	a.prune()
	j := a.jobs[r.PathValue("id")]
	a.mu.Unlock()
	if j == nil {
		apiError(w, http.StatusNotFound, "no such video")
	}
	return j
}

// prune forgets jobs older than Keep. Call with mu held.
func (a *Adapter) prune() {
	for id, j := range a.jobs {
		if time.Since(j.created) > a.cfg.Keep {
			delete(a.jobs, id)
		}
	}
}

func (a *Adapter) view(j *job) map[string]any {
	a.mu.Lock()
	status := j.status
	a.mu.Unlock()
	progress := 0
	switch status {
	case "":
		status = "queued"
	case "in_progress":
		progress = 50
	case "completed":
		progress = 100
	}
	return map[string]any{"id": j.id, "object": "video", "model": j.model, "status": status, "progress": progress,
		"seconds": strconv.FormatFloat(j.seconds, 'f', -1, 64), "size": j.size, "created_at": j.created.Unix()}
}

var videoExt = map[string]string{".mp4": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime",
	".mkv": "video/x-matroska", ".gif": "image/gif"}

// refresh asks ComfyUI where the job is, unless it already ended.
func (a *Adapter) refresh(ctx context.Context, j *job) {
	a.mu.Lock()
	done := j.status == "completed" || j.status == "failed"
	a.mu.Unlock()
	if done {
		return
	}
	var hist map[string]struct {
		Outputs map[string]map[string]json.RawMessage `json:"outputs"`
		Status  struct {
			StatusStr string `json:"status_str"`
			Completed bool   `json:"completed"`
		} `json:"status"`
	}
	if _, err := a.comfy(ctx, http.MethodGet, "/history/"+url.PathEscape(j.prompt), nil, &hist); err != nil {
		return // ComfyUI busy or restarting; report the last known status
	}
	status, file := "", (*output)(nil)
	if h, ok := hist[j.prompt]; ok {
		status = "failed"
		if h.Status.StatusStr != "error" {
			if file = findVideo(h.Outputs); file != nil {
				status = "completed"
			} else if !h.Status.Completed {
				status = "in_progress"
			}
		}
	} else {
		var q struct {
			Running []json.RawMessage `json:"queue_running"`
			Pending []json.RawMessage `json:"queue_pending"`
		}
		if _, err := a.comfy(ctx, http.MethodGet, "/queue", nil, &q); err != nil {
			return
		}
		switch {
		case inQueue(q.Running, j.prompt):
			status = "in_progress"
		case inQueue(q.Pending, j.prompt):
			status = "queued"
		case time.Since(j.created) > time.Minute:
			status = "failed" // ComfyUI no longer knows it (restarted or cleared its queue)
		}
	}
	a.mu.Lock()
	j.status, j.file = status, file
	a.mu.Unlock()
	if status == "completed" || status == "failed" {
		a.cfg.Log.Info("job ended", "id", j.id, "status", status)
	}
}

// findVideo returns the first video file among a prompt's outputs.
func findVideo(outputs map[string]map[string]json.RawMessage) *output {
	keys := make([]string, 0, len(outputs))
	for k := range outputs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		for _, list := range outputs[k] {
			var files []output
			if json.Unmarshal(list, &files) != nil {
				continue
			}
			for _, f := range files {
				if videoExt[strings.ToLower(path.Ext(f.Filename))] != "" {
					f := f
					return &f
				}
			}
		}
	}
	return nil
}

func inQueue(items []json.RawMessage, promptID string) bool {
	for _, it := range items {
		var fields []json.RawMessage
		var id string
		if json.Unmarshal(it, &fields) == nil && len(fields) > 1 && json.Unmarshal(fields[1], &id) == nil && id == promptID {
			return true
		}
	}
	return false
}

func (a *Adapter) content(w http.ResponseWriter, r *http.Request) {
	j := a.job(w, r)
	if j == nil {
		return
	}
	a.refresh(r.Context(), j)
	a.mu.Lock()
	status, file := j.status, j.file
	a.mu.Unlock()
	if status != "completed" || file == nil {
		apiError(w, http.StatusConflict, "video not ready")
		return
	}
	q := url.Values{"filename": {file.Filename}, "subfolder": {file.Subfolder}, "type": {file.Type}}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, a.cfg.ComfyURL+"/view?"+q.Encode(), nil)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// No overall timeout: clips can be large.
	resp, err := (&http.Client{Transport: a.cfg.HTTPClient.Transport}).Do(req)
	if err != nil {
		apiError(w, http.StatusBadGateway, "ComfyUI unreachable")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		apiError(w, http.StatusBadGateway, "ComfyUI no longer has this video")
		return
	}
	w.Header().Set("Content-Type", videoExt[strings.ToLower(path.Ext(file.Filename))])
	if n := resp.Header.Get("Content-Length"); n != "" {
		w.Header().Set("Content-Length", n)
	}
	_, _ = io.Copy(w, resp.Body)
}

// comfy calls ComfyUI and decodes its JSON answer into out.
func (a *Adapter) comfy(ctx context.Context, method, p string, body []byte, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.cfg.ComfyURL+p, rd)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.cfg.HTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("%s %s: %s", method, p, resp.Status)
	}
	return resp.StatusCode, json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}
