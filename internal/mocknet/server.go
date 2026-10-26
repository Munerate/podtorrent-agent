// Package mocknet is a minimal in-memory PodTorrent network: it accepts node
// links on /v0/node/connect and serves an OpenAI-style /v1/chat/completions
// and video job API (/v1/videos) that route to a connected node. Operators can run it to test their node
// (or their own agent implementation) without touching the real network.
package mocknet

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/Munerate/podtorrent-agent/internal/protocol"
)

type Server struct {
	NodeToken string // token nodes must present
	APIKey    string // key clients must present
	Log       *slog.Logger

	// Receipts counts node receipts whose signature checked out.
	Receipts atomic.Int64

	key   ed25519.PrivateKey
	mu    sync.Mutex
	nodes map[*node]bool
	jobs  map[string]*node // video job id -> node; guarded by mu
	seq   atomic.Int64
}

type node struct {
	conn     *websocket.Conn
	manifest protocol.Manifest
	free     map[string]int // free slots per model, guarded by Server.mu
	mu       sync.Mutex
	pending  map[string]chan protocol.Message
}

func New(nodeToken, apiKey string) *Server {
	_, key, _ := ed25519.GenerateKey(nil)
	return &Server{NodeToken: nodeToken, APIKey: apiKey, Log: slog.Default(), key: key, nodes: map[*node]bool{}, jobs: map[string]*node{}}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v0/node/connect", s.connect)
	mux.HandleFunc("POST /v1/chat/completions", s.chat)
	mux.HandleFunc("GET /v1/models", s.models)
	mux.HandleFunc("POST /v1/videos", s.chat)
	mux.HandleFunc("GET /v1/videos/{id}", s.video)
	mux.HandleFunc("GET /v1/videos/{id}/content", s.video)
	return mux
}

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	if bearer(r) != s.NodeToken {
		http.Error(w, "bad node token", http.StatusUnauthorized)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(32 << 20)
	ctx := r.Context()

	var hello protocol.Message
	if err := wsjson.Read(ctx, conn, &hello); err != nil || hello.Type != "hello" || hello.Manifest == nil {
		_ = wsjson.Write(ctx, conn, protocol.Message{Type: "error", Message: "expected hello with manifest"})
		return
	}
	n := &node{conn: conn, manifest: *hello.Manifest, pending: map[string]chan protocol.Message{}}
	if err := wsjson.Write(ctx, conn, protocol.Message{Type: "welcome", Protocol: protocol.Int(protocol.Version), Node: n.manifest.Node,
		ReceiptKey: base64.StdEncoding.EncodeToString(s.key.Public().(ed25519.PublicKey))}); err != nil {
		return
	}
	s.mu.Lock()
	s.nodes[n] = true
	s.mu.Unlock()
	s.Log.Info("node connected", "node", n.manifest.Node, "agent", hello.Agent)
	defer func() {
		s.mu.Lock()
		delete(s.nodes, n)
		s.mu.Unlock()
		n.mu.Lock()
		for id, ch := range n.pending {
			ch <- protocol.Message{Type: "response_error", ID: id, Message: "node disconnected"}
		}
		n.mu.Unlock()
		s.Log.Info("node disconnected", "node", n.manifest.Node)
	}()

	for {
		var m protocol.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			return
		}
		switch m.Type {
		case "manifest":
			if m.Manifest != nil {
				s.mu.Lock()
				n.manifest = *m.Manifest
				s.mu.Unlock()
			}
		case "status":
			s.mu.Lock()
			n.free = m.FreeByModel
			s.mu.Unlock()
		case "receipt":
			if reply, ok := s.receipt(hello.ReceiptKey, m); ok {
				_ = wsjson.Write(ctx, conn, reply)
			}
		case "response_start", "response_chunk", "response_end", "response_error":
			n.mu.Lock()
			ch := n.pending[m.ID]
			n.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
}

// receipt checks a node's receipt and countersigns it. The mock network
// doesn't meter responses, so it takes the node's counts as its own.
func (s *Server) receipt(nodeKey string, m protocol.Message) (protocol.Message, bool) {
	k, err1 := base64.StdEncoding.DecodeString(nodeKey)
	sig, err2 := base64.StdEncoding.DecodeString(m.Sig)
	if err1 != nil || err2 != nil || len(k) != ed25519.PublicKeySize || !ed25519.Verify(k, []byte(m.Receipt), sig) {
		s.Log.Warn("bad node receipt", "id", m.ID)
		return protocol.Message{}, false
	}
	var rc protocol.Receipt
	if json.Unmarshal([]byte(m.Receipt), &rc) != nil {
		return protocol.Message{}, false
	}
	s.Receipts.Add(1)
	rc.Tier, rc.NodeSig = "interactive", &m.Sig
	b, _ := json.Marshal(rc)
	return protocol.Message{Type: "receipt", ID: m.ID, Receipt: string(b),
		Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(s.key, b))}, true
}

// pick returns a node serving model with a free slot, and reserves the slot.
func (s *Server) pick(model string) *node {
	s.mu.Lock()
	defer s.mu.Unlock()
	for n := range s.nodes {
		if n.free[model] > 0 {
			n.free[model]--
			return n
		}
	}
	return nil
}

func openAIError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": msg}})
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	seen := map[string]bool{}
	data := []map[string]string{}
	s.mu.Lock()
	for n := range s.nodes {
		for _, m := range n.manifest.Models {
			if !seen[m.ID] {
				seen[m.ID] = true
				data = append(data, map[string]string{"id": m.ID, "object": "model"})
			}
		}
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	if bearer(r) != s.APIKey {
		openAIError(w, http.StatusUnauthorized, "invalid API key")
		return
	}
	var body json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		openAIError(w, http.StatusBadRequest, "bad json")
		return
	}
	var head struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &head)
	n := s.pick(head.Model)
	if n == nil {
		openAIError(w, http.StatusServiceUnavailable, fmt.Sprintf("no node with a free slot serves %q", head.Model))
		return
	}
	var job *bytes.Buffer
	if r.URL.Path == "/v1/videos" {
		job = &bytes.Buffer{}
		defer func() {
			var v struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(job.Bytes(), &v) == nil && v.ID != "" {
				s.mu.Lock()
				s.jobs[v.ID] = n
				s.mu.Unlock()
			}
		}()
	}
	s.relay(w, r, n, protocol.Message{Method: http.MethodPost, Path: r.URL.Path, Body: body}, job)
}

// video serves a video job's status or content from the node that runs it.
func (s *Server) video(w http.ResponseWriter, r *http.Request) {
	if bearer(r) != s.APIKey {
		openAIError(w, http.StatusUnauthorized, "invalid API key")
		return
	}
	s.mu.Lock()
	n := s.jobs[r.PathValue("id")]
	if n != nil && !s.nodes[n] {
		n = nil
	}
	s.mu.Unlock()
	if n == nil {
		openAIError(w, http.StatusNotFound, "no such video")
		return
	}
	s.relay(w, r, n, protocol.Message{Method: http.MethodGet, Path: r.URL.Path}, nil)
}

// relay sends req to node n and writes the response to w (and to capture, if set).
func (s *Server) relay(w http.ResponseWriter, r *http.Request, n *node, req protocol.Message, capture *bytes.Buffer) {
	id := fmt.Sprintf("req_%d", s.seq.Add(1))
	ch := make(chan protocol.Message, 64)
	n.mu.Lock()
	n.pending[id] = ch
	n.mu.Unlock()
	defer func() {
		n.mu.Lock()
		delete(n.pending, id)
		n.mu.Unlock()
	}()

	ctx := r.Context()
	req.Type, req.ID = "request", id
	if err := wsjson.Write(ctx, n.conn, req); err != nil {
		openAIError(w, http.StatusBadGateway, "node link failed")
		return
	}
	flusher, _ := w.(http.Flusher)
	started := false
	for {
		select {
		case <-ctx.Done():
			_ = wsjson.Write(context.Background(), n.conn, protocol.Message{Type: "cancel", ID: id})
			return
		case m := <-ch:
			switch m.Type {
			case "response_start":
				started = true
				if ct := m.Headers["content-type"]; ct != "" {
					w.Header().Set("Content-Type", ct)
				}
				w.WriteHeader(m.Status)
			case "response_chunk":
				data := []byte(m.Data)
				if m.Encoding == "base64" {
					data, _ = base64.StdEncoding.DecodeString(m.Data)
				}
				if capture != nil {
					capture.Write(data)
				}
				_, _ = w.Write(data)
				if flusher != nil {
					flusher.Flush()
				}
			case "response_end":
				return
			case "response_error":
				if !started {
					openAIError(w, http.StatusBadGateway, m.Message)
				}
				return
			}
		}
	}
}
