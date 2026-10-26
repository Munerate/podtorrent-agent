package internal_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Munerate/podtorrent-agent/internal/agent"
	"github.com/Munerate/podtorrent-agent/internal/mockengine"
	"github.com/Munerate/podtorrent-agent/internal/mocknet"
	"github.com/Munerate/podtorrent-agent/internal/protocol"
)

const model = "mock/echo-1"

// A client request goes gateway (mock network) -> agent link -> mock engine and back.
func TestHelloPath(t *testing.T) {
	engine := httptest.NewServer(mockengine.Handler([]string{model}, time.Millisecond))
	defer engine.Close()
	mn := mocknet.New("node-token", "client-key")
	net := httptest.NewServer(mn.Handler())
	defer net.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := agent.New(agent.Config{
		NetworkURL: "ws" + strings.TrimPrefix(net.URL, "http") + "/v0/node/connect",
		Token:      "node-token",
		EngineURL:  engine.URL,
		Manifest:   protocol.Manifest{Node: "test-node", Models: []protocol.Model{{ID: model, Slots: 2}}},
	})
	go func() { _ = a.Run(ctx) }()

	// Wait for the node to register.
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := post(net.URL, "client-key", false)
		if err == nil && resp.StatusCode == 200 {
			var out struct {
				Choices []struct{ Message struct{ Content string } }
			}
			_ = json.NewDecoder(resp.Body).Decode(&out)
			resp.Body.Close()
			if got := out.Choices[0].Message.Content; got != "echo: hello there" {
				t.Fatalf("content = %q", got)
			}
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("node never became routable: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	resp, err := post(net.URL, "client-key", true)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"content":" there"`) || !strings.HasSuffix(string(body), "data: [DONE]\n\n") {
		t.Fatalf("unexpected stream: %s", body)
	}

	// Each finished request produced a signed node receipt, and the agent
	// checked the network's countersigned reply.
	for deadline := time.Now().Add(2 * time.Second); mn.Receipts.Load() < 2 || !strings.Contains(metrics(a), `podtorrent_agent_network_receipts_total{result="matched"} 2`); {
		if time.Now().After(deadline) {
			t.Fatalf("receipts: network verified %d; agent metrics:\n%s", mn.Receipts.Load(), metrics(a))
		}
		time.Sleep(20 * time.Millisecond)
	}

	resp, _ = post(net.URL, "wrong", false)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad key status = %d", resp.StatusCode)
	}
}

func metrics(a *agent.Agent) string {
	w := httptest.NewRecorder()
	a.MetricsHandler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	return w.Body.String()
}

func post(base, key string, stream bool) (*http.Response, error) {
	b, _ := json.Marshal(map[string]any{"model": model, "stream": stream,
		"messages": []map[string]string{{"role": "user", "content": "hello there"}}})
	req, _ := http.NewRequest("POST", base+"/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

func TestCheckEngine(t *testing.T) {
	engine := httptest.NewServer(mockengine.Handler([]string{model}, 0))
	defer engine.Close()
	m := protocol.Manifest{Node: "n", Models: []protocol.Model{{ID: model, Slots: 1}, {ID: "other/model", Slots: 1}}}
	missing, err := agent.CheckEngine(context.Background(), http.DefaultClient, engine.URL, m)
	if err != nil || len(missing) != 1 || missing[0] != "other/model" {
		t.Fatalf("missing = %v, err = %v", missing, err)
	}
}

// A video job: submit, poll until completed, download the clip. The job holds
// its slot until it ends, then the node sends a receipt with its seconds.
func TestVideoJob(t *testing.T) {
	const video = "mock/video-1"
	engine := httptest.NewServer(mockengine.Handler([]string{video}, 5*time.Millisecond))
	defer engine.Close()
	mn := mocknet.New("node-token", "client-key")
	net := httptest.NewServer(mn.Handler())
	defer net.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := agent.New(agent.Config{
		NetworkURL: "ws" + strings.TrimPrefix(net.URL, "http") + "/v0/node/connect",
		Token:      "node-token",
		EngineURL:  engine.URL,
		JobPoll:    10 * time.Millisecond,
		Manifest: protocol.Manifest{Node: "video-node", Models: []protocol.Model{{ID: video, Slots: 1, Modality: "video",
			Price: &protocol.Price{PerVideoSecond: 0.05}}}},
	})
	go func() { _ = a.Run(ctx) }()

	call := func(method, path, body string) (int, []byte) {
		req, _ := http.NewRequest(method, net.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer client-key")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	submit := `{"model":"` + video + `","prompt":"a lighthouse at dusk","seconds":"4","size":"1280x720"}`
	var job struct{ ID, Status string }
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		code, b := call("POST", "/v1/videos", submit)
		if code == 200 {
			_ = json.Unmarshal(b, &job)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("submit: %d %s", code, b)
		}
	}
	if job.ID == "" || job.Status != "queued" {
		t.Fatalf("job = %+v", job)
	}
	for deadline := time.Now().Add(5 * time.Second); job.Status != "completed"; time.Sleep(20 * time.Millisecond) {
		_, b := call("GET", "/v1/videos/"+job.ID, "")
		_ = json.Unmarshal(b, &job)
		if time.Now().After(deadline) {
			t.Fatalf("job never completed: %+v", job)
		}
	}
	code, clip := call("GET", "/v1/videos/"+job.ID+"/content", "")
	if code != 200 || len(clip) != 4*mockengine.VideoBytesPerSecond || clip[300] != byte(300%251) {
		t.Fatalf("content: %d, %d bytes", code, len(clip))
	}
	if code, _ := call("GET", "/v1/videos/..%2Fsecret/content", ""); code == 200 {
		t.Fatal("path traversal reached the engine")
	}

	// The receipt arrives when the job ends, and the slot is free again.
	for deadline := time.Now().Add(2 * time.Second); !strings.Contains(metrics(a), `podtorrent_agent_network_receipts_total{result="matched"} 1`); {
		if time.Now().After(deadline) {
			t.Fatalf("no video receipt; metrics:\n%s", metrics(a))
		}
		time.Sleep(20 * time.Millisecond)
	}
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if code, _ := call("POST", "/v1/videos", submit); code == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slot not freed after the job ended")
		}
	}
}

func TestAllowed(t *testing.T) {
	for _, c := range []struct {
		method, path string
		ok           bool
	}{
		{"POST", "/v1/chat/completions", true}, {"POST", "/v1/videos", true}, {"GET", "/v1/videos/video_1", true},
		{"GET", "/v1/videos/video_1/content", true}, {"GET", "/v1/videos/../models", false}, {"GET", "/v1/videos/a/b", false},
		{"GET", "/v1/models", false}, {"POST", "/v1/files", false}, {"GET", "/v1/videos/", false},
	} {
		if got := protocol.Allowed(c.method, c.path); got != c.ok {
			t.Errorf("Allowed(%s %s) = %v", c.method, c.path, got)
		}
	}
}
