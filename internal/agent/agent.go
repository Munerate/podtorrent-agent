// Package agent connects an operator's OpenAI-compatible engine to the
// PodTorrent network over one outbound WebSocket (spec/protocol-v0.md).
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/Munerate/podtorrent-agent/internal/protocol"
)

const Name = "podtorrent-agent/0.1.0"

type Config struct {
	NetworkURL string // ws(s)://host/v0/node/connect
	Token      string
	EngineURL  string // e.g. http://127.0.0.1:8000
	Manifest   protocol.Manifest
	Log        *slog.Logger
	HTTPClient *http.Client
}

type Agent struct {
	cfg      Config
	Metrics  *Metrics
	receipts *receipts

	mu       sync.Mutex
	inFlight map[string]context.CancelFunc
	manifest protocol.Manifest      // current; guarded by mu
	busy     map[string]int         // requests in flight per model; guarded by mu
	notify   map[chan struct{}]bool // sessions to tell about manifest changes; guarded by mu
}

func New(cfg Config) *Agent {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	cfg.EngineURL = strings.TrimRight(cfg.EngineURL, "/")
	return &Agent{cfg: cfg, Metrics: NewMetrics(), receipts: newReceipts(), inFlight: map[string]context.CancelFunc{},
		manifest: cfg.Manifest, busy: map[string]int{}, notify: map[chan struct{}]bool{}}
}

// SetManifest replaces the manifest. Connected sessions send it to the
// network at once (a "manifest" message); requests in flight are unaffected.
func (a *Agent) SetManifest(m protocol.Manifest) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.manifest = m
	for ch := range a.notify {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (a *Agent) Manifest() protocol.Manifest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.manifest
}

// acquire takes a slot for model, or says why not.
func (a *Agent) acquire(model string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, mod := range a.manifest.Models {
		if mod.ID == model {
			if a.busy[model] >= mod.Slots {
				return errors.New("no free slots")
			}
			a.busy[model]++
			return nil
		}
	}
	return errors.New("model not served by this node")
}

func (a *Agent) release(model string) {
	a.mu.Lock()
	a.busy[model]--
	a.mu.Unlock()
}

// free reports free slots per model and in total.
func (a *Agent) free() (int, map[string]int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	total, by := 0, map[string]int{}
	for _, mod := range a.manifest.Models {
		by[mod.ID] = max(0, mod.Slots-a.busy[mod.ID])
		total += by[mod.ID]
	}
	return total, by
}

// Run keeps the link up until ctx ends, reconnecting with backoff.
func (a *Agent) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		start := time.Now()
		err := a.session(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		a.cfg.Log.Warn("link down, reconnecting", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

func (a *Agent) session(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, a.cfg.NetworkURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + a.cfg.Token}},
	})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	return a.Serve(ctx, conn, a.cfg.NetworkURL)
}

// Serve runs the node side of the protocol on an open connection, whichever
// side opened it: the agent (outbound mode) or the network (inbound mode).
func (a *Agent) Serve(ctx context.Context, conn *websocket.Conn, peer string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(32 << 20)

	send := func(m protocol.Message) error { return wsjson.Write(ctx, conn, m) }

	manifest := a.Manifest()
	if err := send(protocol.Message{Type: "hello", Protocol: protocol.Int(protocol.Version), Agent: Name, Manifest: &manifest,
		ReceiptKey: a.receipts.publicKey()}); err != nil {
		return err
	}
	var welcome protocol.Message
	if err := wsjson.Read(ctx, conn, &welcome); err != nil {
		return fmt.Errorf("waiting for welcome: %w", err)
	}
	if welcome.Type != "welcome" {
		return fmt.Errorf("expected welcome, got %q: %s", welcome.Type, welcome.Message)
	}
	a.receipts.setNetworkKey(welcome.ReceiptKey)
	a.cfg.Log.Info("connected", "node", welcome.Node, "network", peer)
	a.Metrics.connected(true)
	defer a.Metrics.connected(false)

	var statusMu sync.Mutex
	sendStatus := func() {
		statusMu.Lock()
		defer statusMu.Unlock()
		total, byModel := a.free()
		_ = send(protocol.Message{Type: "status", FreeSlots: protocol.Int(total), FreeByModel: byModel})
	}
	sendStatus()

	changed := make(chan struct{}, 1)
	a.mu.Lock()
	a.notify[changed] = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.notify, changed)
		a.mu.Unlock()
	}()

	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sendStatus()
			case <-changed:
				m := a.Manifest()
				_ = send(protocol.Message{Type: "manifest", Manifest: &m})
				sendStatus()
				a.cfg.Log.Info("manifest sent", "models", len(m.Models))
			}
		}
	}()

	for {
		var m protocol.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			return err
		}
		switch m.Type {
		case "request":
			model := requestModel(m.Body)
			if err := a.acquire(model); err != nil {
				a.Metrics.request(model, "refused")
				_ = send(protocol.Message{Type: "response_error", ID: m.ID, Message: err.Error()})
				continue
			}
			sendStatus()
			go func() {
				a.Metrics.request(model, a.forward(ctx, m, welcome.Node, model, send))
				a.release(model)
				sendStatus()
			}()
		case "cancel":
			a.mu.Lock()
			if c := a.inFlight[m.ID]; c != nil {
				c()
			}
			a.mu.Unlock()
		case "receipt":
			result := a.receipts.check(m)
			a.Metrics.receipt(result)
			if result != "matched" {
				a.cfg.Log.Warn("network receipt", "id", m.ID, "result", result)
			}
		case "error":
			return fmt.Errorf("network: %s", m.Message)
		}
	}
}

func requestModel(body []byte) string {
	var head struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &head)
	return head.Model
}

// CheckEngine asks the engine which models it serves (GET /v1/models) and
// returns the manifest models it does not list.
func CheckEngine(ctx context.Context, client *http.Client, engineURL string, m protocol.Manifest) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(engineURL, "/")+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /v1/models: %s", resp.Status)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("GET /v1/models: %w", err)
	}
	have := map[string]bool{}
	for _, d := range list.Data {
		have[d.ID] = true
	}
	var missing []string
	for _, mod := range m.Models {
		if !have[mod.ID] {
			missing = append(missing, mod.ID)
		}
	}
	return missing, nil
}

// forward sends one request to the engine and streams the answer back.
// It returns the outcome for metrics: "ok", "engine_error" or "failed".
// After the response ends it sends this node's signed receipt.
func (a *Agent) forward(ctx context.Context, m protocol.Message, node, model string, send func(protocol.Message) error) (outcome string) {
	ctx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.inFlight[m.ID] = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		delete(a.inFlight, m.ID)
		a.mu.Unlock()
	}()

	outcome = "failed"
	fail := func(err error) {
		a.cfg.Log.Warn("request failed", "id", m.ID, "err", err)
		_ = send(protocol.Message{Type: "response_error", ID: m.ID, Message: err.Error()})
	}
	if m.Method != http.MethodPost || !protocol.AllowedPaths[m.Path] {
		fail(fmt.Errorf("path not allowed: %s %s", m.Method, m.Path))
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.EngineURL+m.Path, bytes.NewReader(m.Body))
	if err != nil {
		fail(err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.cfg.HTTPClient.Do(req)
	if err != nil {
		fail(errors.New("engine unreachable"))
		return
	}
	defer resp.Body.Close()

	if err := send(protocol.Message{Type: "response_start", ID: m.ID, Status: resp.StatusCode,
		Headers: map[string]string{"content-type": resp.Header.Get("Content-Type")}}); err != nil {
		return
	}
	buf := make([]byte, 16<<10)
	var tail usageTail
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			tail.Write(buf[:n])
			if serr := send(protocol.Message{Type: "response_chunk", ID: m.ID, Data: string(buf[:n])}); serr != nil {
				return
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			fail(err)
			return
		}
	}
	if send(protocol.Message{Type: "response_end", ID: m.ID}) == nil {
		p, c, cached := tail.counts()
		_ = send(a.receipts.sign(protocol.Receipt{ID: m.ID, Node: node, Model: model, Status: resp.StatusCode,
			PromptTokens: p, CompletionTokens: c, CachedTokens: cached, TS: time.Now().UnixMilli()}))
	}
	if resp.StatusCode >= 400 {
		return "engine_error"
	}
	return "ok"
}
