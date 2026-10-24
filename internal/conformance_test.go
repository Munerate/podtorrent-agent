package internal_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/Munerate/podtorrent-agent/internal/agent"
	"github.com/Munerate/podtorrent-agent/internal/conformance"
	"github.com/Munerate/podtorrent-agent/internal/mockengine"
	"github.com/Munerate/podtorrent-agent/internal/protocol"
)

// The reference agent must pass its own conformance suite (inbound mode, so
// the test can dial it).
func TestAgentIsConformant(t *testing.T) {
	engine := httptest.NewServer(mockengine.Handler([]string{model}, time.Millisecond))
	defer engine.Close()
	a := agent.New(agent.Config{Token: "tok", EngineURL: engine.URL,
		Manifest: protocol.Manifest{Node: "conf-node", Models: []protocol.Model{{ID: model, Slots: 2}}}})
	srv := httptest.NewServer(a.InboundHandler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v0/node/connect",
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer tok"}}})
	if err != nil {
		t.Fatal(err)
	}
	r := conformance.Run(ctx, conn, "hello", 5*time.Second)
	if !r.OK() {
		t.Fatal(r)
	}
}

// Manifest changes reach the network without dropping the link.
func TestManifestReload(t *testing.T) {
	engine := httptest.NewServer(mockengine.Handler([]string{model, "mock/echo-2"}, 0))
	defer engine.Close()
	a := agent.New(agent.Config{Token: "tok", EngineURL: engine.URL,
		Manifest: protocol.Manifest{Node: "n", Models: []protocol.Model{{ID: model, Slots: 1}}}})
	srv := httptest.NewServer(a.InboundHandler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v0/node/connect",
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer tok"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	read := func(typ string) protocol.Message {
		for {
			var m protocol.Message
			if err := wsjson.Read(ctx, conn, &m); err != nil {
				t.Fatal(err)
			}
			if m.Type == typ {
				return m
			}
		}
	}
	read("hello")
	_ = wsjson.Write(ctx, conn, protocol.Message{Type: "welcome", Protocol: protocol.Int(0), Node: "n"})
	read("status")
	a.SetManifest(protocol.Manifest{Node: "n", Models: []protocol.Model{{ID: model, Slots: 1}, {ID: "mock/echo-2", Slots: 3}}})
	m := read("manifest")
	if len(m.Manifest.Models) != 2 {
		t.Fatalf("manifest = %+v", m.Manifest)
	}
	if st := read("status"); st.FreeByModel["mock/echo-2"] != 3 {
		t.Fatalf("status after reload = %+v", st.FreeByModel)
	}
}

