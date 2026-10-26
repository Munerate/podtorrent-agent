// Package conformance checks that a node implements protocol v0
// (spec/protocol-v0.md). It plays the network's side on one connection,
// whichever side opened it, and reports each check.
package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/Munerate/podtorrent-agent/internal/protocol"
)

type Result struct {
	Name string
	Err  error
}

type Report struct {
	Node    string
	Results []Result
}

func (r *Report) OK() bool {
	for _, x := range r.Results {
		if x.Err != nil {
			return false
		}
	}
	return len(r.Results) > 0
}

func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "node %q\n", r.Node)
	for _, x := range r.Results {
		if x.Err == nil {
			fmt.Fprintf(&b, "  PASS  %s\n", x.Name)
		} else {
			fmt.Fprintf(&b, "  FAIL  %s: %v\n", x.Name, x.Err)
		}
	}
	return b.String()
}

type session struct {
	conn  *websocket.Conn
	in    chan protocol.Message
	errc  chan error
	slots map[string]int
}

func (s *session) send(ctx context.Context, m protocol.Message) error {
	return wsjson.Write(ctx, s.conn, m)
}

// next returns the next message matching keep, or an error after d.
func (s *session) next(d time.Duration, keep func(protocol.Message) bool) (protocol.Message, error) {
	t := time.NewTimer(d)
	defer t.Stop()
	for {
		select {
		case m := <-s.in:
			if keep(m) {
				return m, nil
			}
		case err := <-s.errc:
			return protocol.Message{}, fmt.Errorf("connection: %w", err)
		case <-t.C:
			return protocol.Message{}, fmt.Errorf("nothing within %s", d)
		}
	}
}

// response collects one request's response until end or error.
func (s *session) response(id string, d time.Duration) (status int, body string, err error) {
	deadline := time.Now().Add(d)
	started := false
	for {
		m, err := s.next(time.Until(deadline), func(m protocol.Message) bool { return m.ID == id })
		if err != nil {
			return status, body, err
		}
		switch m.Type {
		case "response_start":
			if started {
				return status, body, fmt.Errorf("second response_start")
			}
			started, status = true, m.Status
		case "response_chunk":
			if !started {
				return status, body, fmt.Errorf("response_chunk before response_start")
			}
			body += m.Data
		case "response_end":
			if !started {
				return status, body, fmt.Errorf("response_end before response_start")
			}
			return status, body, nil
		case "response_error":
			return status, body, fmt.Errorf("response_error: %s", m.Message)
		}
	}
}

// Run checks a node on conn. prompt is the user message sent to the model.
func Run(ctx context.Context, conn *websocket.Conn, prompt string, timeout time.Duration) *Report {
	r := &Report{}
	check := func(name string, err error) bool {
		r.Results = append(r.Results, Result{name, err})
		return err == nil
	}
	conn.SetReadLimit(32 << 20)
	s := &session{conn: conn, in: make(chan protocol.Message, 256), errc: make(chan error, 1), slots: map[string]int{}}
	go func() {
		for {
			var m protocol.Message
			if err := wsjson.Read(ctx, conn, &m); err != nil {
				s.errc <- err
				return
			}
			s.in <- m
		}
	}()

	// 1. hello first, with a usable manifest.
	hello, err := s.next(10*time.Second, func(protocol.Message) bool { return true })
	if err == nil && hello.Type != "hello" {
		err = fmt.Errorf("first message is %q, want hello", hello.Type)
	}
	if err == nil && (hello.Protocol == nil || *hello.Protocol != protocol.Version) {
		err = fmt.Errorf("protocol must be %d", protocol.Version)
	}
	if err == nil && (hello.Manifest == nil || hello.Manifest.Node == "" || hello.Manifest.TotalSlots() == 0) {
		err = fmt.Errorf("manifest needs node and models with slots > 0")
	}
	if !check("hello with protocol 0 and a manifest", err) {
		return r
	}
	man := *hello.Manifest
	r.Node = man.Node
	for _, m := range man.Models {
		s.slots[m.ID] = m.Slots
	}
	model := man.Models[0].ID
	_ = s.send(ctx, protocol.Message{Type: "welcome", Protocol: protocol.Int(protocol.Version), Node: man.Node})

	// 2. status with every model's free slots.
	st, err := s.next(10*time.Second, func(m protocol.Message) bool { return m.Type == "status" })
	if err == nil {
		for id, n := range s.slots {
			if st.FreeByModel[id] != n {
				err = fmt.Errorf("free_by_model[%q] = %d, want %d (all free)", id, st.FreeByModel[id], n)
				break
			}
		}
	}
	check("status after welcome reports all slots free", err)

	body := func(stream bool, m string) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"model": m, "stream": stream, "max_tokens": 32,
			"stream_options": map[string]bool{"include_usage": stream},
			"messages":       []map[string]string{{"role": "user", "content": prompt}}})
		return b
	}

	// 3. non-streaming request.
	_ = s.send(ctx, protocol.Message{Type: "request", ID: "c-plain", Method: "POST", Path: "/v1/chat/completions", Body: body(false, model)})
	status, out, err := s.response("c-plain", timeout)
	if err == nil && status != 200 {
		err = fmt.Errorf("status %d: %.200s", status, out)
	}
	if err == nil && !strings.Contains(out, `"choices"`) {
		err = fmt.Errorf("no choices in body: %.200s", out)
	}
	check("non-streaming chat completion is forwarded and answered", err)

	// 4. streaming request, with usage in the final chunk.
	_ = s.send(ctx, protocol.Message{Type: "request", ID: "c-stream", Method: "POST", Path: "/v1/chat/completions", Body: body(true, model)})
	status, out, err = s.response("c-stream", timeout)
	if err == nil && (status != 200 || !strings.Contains(out, "data: [DONE]")) {
		err = fmt.Errorf("status %d, want a stream ending in data: [DONE]: %.200s", status, out)
	}
	check("streaming chat completion ends with [DONE]", err)
	check("stream reports usage when include_usage is set", func() error {
		if err != nil {
			return fmt.Errorf("no stream")
		}
		if !strings.Contains(out, `"usage"`) {
			return fmt.Errorf("no usage chunk; the network meters with it")
		}
		return nil
	}())

	// 5. unknown model and disallowed path are refused.
	_ = s.send(ctx, protocol.Message{Type: "request", ID: "c-unknown", Method: "POST", Path: "/v1/chat/completions", Body: body(false, "conformance/no-such-model")})
	m, err := s.next(timeout, func(m protocol.Message) bool { return m.ID == "c-unknown" })
	if err == nil && m.Type != "response_error" {
		err = fmt.Errorf("got %q, want response_error", m.Type)
	}
	check("request for a model not in the manifest gets response_error", err)

	_ = s.send(ctx, protocol.Message{Type: "request", ID: "c-path", Method: "POST", Path: "/v1/files", Body: body(false, model)})
	m, err = s.next(timeout, func(m protocol.Message) bool { return m.ID == "c-path" })
	if err == nil && m.Type != "response_error" {
		err = fmt.Errorf("got %q, want response_error", m.Type)
	}
	check("request for a path not in the spec gets response_error", err)

	// 6. cancel: the node stops and frees the slot.
	_ = s.send(ctx, protocol.Message{Type: "request", ID: "c-cancel", Method: "POST", Path: "/v1/chat/completions", Body: body(true, model)})
	_ = s.send(ctx, protocol.Message{Type: "cancel", ID: "c-cancel"})
	_, err = s.next(timeout+35*time.Second, func(m protocol.Message) bool {
		return m.Type == "status" && m.FreeByModel[model] == s.slots[model]
	})
	check("slots are free again after requests finish or are cancelled", err)

	// 7. unknown message types are ignored.
	_ = s.send(ctx, protocol.Message{Type: "conformance_unknown_type"})
	_ = s.send(ctx, protocol.Message{Type: "request", ID: "c-after", Method: "POST", Path: "/v1/chat/completions", Body: body(false, model)})
	_, _, err = s.response("c-after", timeout)
	check("unknown message types are ignored", err)
	return r
}
