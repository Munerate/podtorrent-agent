// Package selftest checks a node setup end to end before it goes live: it
// plays the network on loopback, connects an agent with the given manifest
// and engine to it, and runs the conformance checks. It is part of the
// public API for programs built on the agent, such as a desktop app.
package selftest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/Munerate/podtorrent-agent/internal/agent"
	"github.com/Munerate/podtorrent-agent/internal/conformance"
	"github.com/Munerate/podtorrent-agent/pkg/manifest"
)

type Options struct {
	EngineURL string
	Manifest  manifest.Manifest
	Prompt    string        // user message sent to the model; default "Reply with the word OK."
	Timeout   time.Duration // per request; default 2 minutes
	Log       *slog.Logger  // the agent's log; default discards
}

type Check struct {
	Name string
	Err  error // nil if passed
}

type Report struct {
	Node   string
	Checks []Check
}

func (r *Report) OK() bool {
	for _, c := range r.Checks {
		if c.Err != nil {
			return false
		}
	}
	return len(r.Checks) > 0
}

// Run runs the checks. It listens only on 127.0.0.1 and stops when done.
func Run(ctx context.Context, o Options) (*Report, error) {
	if o.Prompt == "" {
		o.Prompt = "Reply with the word OK."
	}
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Minute
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	b := make([]byte, 16)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	done := make(chan *conformance.Report, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/node/connect" || r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		rep := conformance.Run(ctx, conn, o.Prompt, o.Timeout)
		_ = conn.Close(websocket.StatusNormalClosure, "self-test done")
		select {
		case done <- rep:
		default:
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	a := agent.New(agent.Config{NetworkURL: "ws://" + ln.Addr().String() + "/v0/node/connect", Token: token,
		EngineURL: o.EngineURL, Manifest: o.Manifest, Log: o.Log})
	go func() { _ = a.Run(ctx) }()

	select {
	case rep := <-done:
		out := &Report{Node: rep.Node}
		for _, r := range rep.Results {
			out.Checks = append(out.Checks, Check{r.Name, r.Err})
		}
		return out, nil
	case <-ctx.Done():
		return nil, errors.New("self-test: " + ctx.Err().Error())
	}
}
