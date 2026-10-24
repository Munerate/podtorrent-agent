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
	"github.com/Munerate/podtorrent-agent/internal/mockengine"
	"github.com/Munerate/podtorrent-agent/internal/protocol"
)

// In inbound mode the network dials the agent; the protocol on top is the same.
func TestInboundMode(t *testing.T) {
	engine := httptest.NewServer(mockengine.Handler([]string{model}, 0))
	defer engine.Close()
	a := agent.New(agent.Config{Token: "node-token", EngineURL: engine.URL,
		Manifest: protocol.Manifest{Node: "inbound-node", Models: []protocol.Model{{ID: model, Slots: 1}}}})
	srv := httptest.NewServer(a.InboundHandler())
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v0/node/connect"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer wrong"}}}); err == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: err=%v", err)
	}

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer node-token"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()

	var hello protocol.Message
	if err := wsjson.Read(ctx, conn, &hello); err != nil || hello.Type != "hello" || hello.Manifest.Node != "inbound-node" {
		t.Fatalf("hello = %+v, err = %v", hello, err)
	}
	_ = wsjson.Write(ctx, conn, protocol.Message{Type: "welcome", Protocol: protocol.Int(0), Node: "inbound-node"})
	_ = wsjson.Write(ctx, conn, protocol.Message{Type: "request", ID: "r1", Method: "POST", Path: "/v1/chat/completions",
		Body: []byte(`{"model":"` + model + `","messages":[{"role":"user","content":"hi inbound"}]}`)})

	var body strings.Builder
	for {
		var m protocol.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			t.Fatal(err)
		}
		if m.ID != "r1" {
			continue
		}
		if m.Type == "response_chunk" {
			body.WriteString(m.Data)
		}
		if m.Type == "response_error" {
			t.Fatal(m.Message)
		}
		if m.Type == "response_end" {
			break
		}
	}
	if !strings.Contains(body.String(), "echo: hi inbound") {
		t.Fatalf("body = %s", body.String())
	}
}
