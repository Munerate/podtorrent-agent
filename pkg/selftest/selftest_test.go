package selftest

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Munerate/podtorrent-agent/pkg/engine"
	"github.com/Munerate/podtorrent-agent/pkg/manifest"
)

func TestRun(t *testing.T) {
	srv := httptest.NewServer(engine.MockHandler([]string{"mock/echo-1"}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rep, err := Run(ctx, Options{EngineURL: srv.URL, Timeout: 10 * time.Second,
		Manifest: manifest.Manifest{Node: "example-node-01", Models: []manifest.Model{{ID: "mock/echo-1", Slots: 2}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Fatalf("self-test failed: %+v", rep.Checks)
	}
}
