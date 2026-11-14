package internal_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/podtorrent/podtorrent-agent/internal/agent"
	"github.com/podtorrent/podtorrent-agent/internal/mockengine"
	"github.com/podtorrent/podtorrent-agent/internal/mocknet"
	"github.com/podtorrent/podtorrent-agent/internal/protocol"
)

// fakeExo answers like an exo cluster: state paths say whether the model has
// a ready instance, and streams open with a keep-alive comment. With stall
// set, a stream sends only keep-alives, as exo does for a model still loading.
type fakeExo struct {
	loaded, stall atomic.Bool
	chats, urls   atomic.Int64
	fullState     atomic.Bool // set if anyone read /state itself (it holds prompts)
}

func (f *fakeExo) handler(t *testing.T) http.Handler {
	engine := mockengine.Handler([]string{model}, time.Millisecond)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /state", func(w http.ResponseWriter, r *http.Request) { f.fullState.Store(true) })
	mux.HandleFunc("GET /state/instances", func(w http.ResponseWriter, r *http.Request) {
		if !f.loaded.Load() {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`{"i1":{"MlxRingInstance":{"instanceId":"i1","shardAssignments":{"modelId":"` + model +
			`","runnerToShard":{"r1":{},"r2":{}},"nodeToRunner":{"n1":"r1","n2":"r2"}}}}}`))
	})
	mux.HandleFunc("GET /state/runners", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"r1":{"RunnerReady":{}},"r2":{"RunnerRunning":{}}}`))
	})
	mux.HandleFunc("GET /v1/models", engine.ServeHTTP)
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		f.chats.Add(1)
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte("http://")) {
			f.urls.Add(1)
		}
		var head struct{ Stream bool }
		_ = json.Unmarshal(body, &head)
		if head.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(": keep-alive\n\n"))
			w.(http.Flusher).Flush()
			if f.stall.Load() {
				<-r.Context().Done()
				return
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		engine.ServeHTTP(w, r)
	})
	return mux
}

// An exo node offers a model only while exo has it loaded, refuses remote
// URLs, and fails a stream that sends no event in time.
func TestExoEngine(t *testing.T) {
	exo := &fakeExo{}
	engine := httptest.NewServer(exo.handler(t))
	defer engine.Close()
	net := httptest.NewServer(mocknet.New("node-token", "client-key").Handler())
	defer net.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := agent.New(agent.Config{
		NetworkURL: "ws" + strings.TrimPrefix(net.URL, "http") + "/v0/node/connect",
		Token:      "node-token",
		EngineURL:  engine.URL,
		Manifest:   protocol.Manifest{Node: "test-node", Models: []protocol.Model{{ID: model, Slots: 2}}},
		Engine:     agent.EngineExo,
		EnginePoll: 20 * time.Millisecond,
		FirstEvent: 300 * time.Millisecond,
	})
	go a.WatchEngine(ctx)
	go func() { _ = a.Run(ctx) }()

	// Not loaded: the network has no node with a free slot, and exo gets nothing.
	time.Sleep(300 * time.Millisecond)
	if resp, err := post(net.URL, "client-key", false); err != nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("before load: %v %v", status(resp), err)
	}
	if n := exo.chats.Load(); n != 0 {
		t.Fatalf("exo got %d chats before the model was loaded", n)
	}

	// Loaded: the node is offered and serves, streams included.
	exo.loaded.Store(true)
	for deadline := time.Now().Add(5 * time.Second); ; {
		resp, err := post(net.URL, "client-key", true)
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if !strings.Contains(string(body), `"content":" there"`) || !strings.HasSuffix(string(body), "data: [DONE]\n\n") {
				t.Fatalf("unexpected stream: %s", body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node never served after load: %v %v", status(resp), err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// A remote image URL is refused before it reaches exo; inline data goes through
	// (the mock engine then answers 400, as it takes only text content).
	for _, tc := range []struct {
		url     string
		reaches bool
	}{{"http://192.168.1.1/admin", false}, {"data:image/png;base64,AAAA", true}} {
		b, _ := json.Marshal(map[string]any{"model": model, "messages": []any{map[string]any{"role": "user",
			"content": []any{map[string]any{"type": "text", "text": "hello there"},
				map[string]any{"type": "image_url", "image_url": map[string]string{"url": tc.url}}}}}})
		before := exo.chats.Load()
		resp, err := postBody(net.URL, b)
		if reached := exo.chats.Load() > before; err != nil || reached != tc.reaches || (!reached && resp.StatusCode != http.StatusBadGateway) {
			t.Fatalf("image %s: status %v, reached exo %v, err %v", tc.url, status(resp), reached, err)
		}
	}
	if n := exo.urls.Load(); n != 0 {
		t.Fatalf("exo got %d requests with a remote URL", n)
	}

	// A stream that sends only keep-alives fails before anything reaches the client.
	exo.stall.Store(true)
	start := time.Now()
	resp, err := post(net.URL, "client-key", true)
	if err != nil || resp.StatusCode != http.StatusBadGateway || time.Since(start) > 3*time.Second {
		t.Fatalf("stalled stream: %v %v after %v", status(resp), err, time.Since(start))
	}
	exo.stall.Store(false)

	// Unloaded again: the node stops being offered.
	exo.loaded.Store(false)
	for deadline := time.Now().Add(5 * time.Second); ; {
		resp, err := post(net.URL, "client-key", false)
		if err == nil && resp.StatusCode == http.StatusServiceUnavailable {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node still offered after unload: %v %v", status(resp), err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if exo.fullState.Load() {
		t.Fatal("the agent read exo's full /state, which holds prompts")
	}
}

func postBody(base string, b []byte) (*http.Response, error) {
	req, _ := http.NewRequest("POST", base+"/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer client-key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	return resp, err
}

func status(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}
