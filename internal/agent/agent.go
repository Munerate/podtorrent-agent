// Package agent connects an operator's OpenAI-compatible engine to the
// PodTorrent network over one outbound WebSocket (spec/protocol-v0.md).
package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/Munerate/podtorrent-agent/internal/protocol"
)

const Name = "podtorrent-agent/0.4.0"

type Config struct {
	NetworkURL string // ws(s)://host/v0/node/connect
	Token      string
	EngineURL  string // e.g. http://127.0.0.1:8000
	Manifest   protocol.Manifest
	Log        *slog.Logger
	HTTPClient *http.Client
	JobPoll    time.Duration // how often to check a video job on the engine; default 2 s
}

type Agent struct {
	cfg      Config
	Metrics  *Metrics
	receipts *receipts

	mu       sync.Mutex
	inFlight map[string]context.CancelFunc
	manifest protocol.Manifest            // current; guarded by mu
	busy     map[string]int               // requests in flight per model; guarded by mu
	notify   map[chan struct{}]bool       // sessions to tell about manifest changes; guarded by mu
	send     func(protocol.Message) error // current session's sender, for video jobs; guarded by mu
	node     string                       // node id from the current welcome; guarded by mu
	gen      int                          // session count; guarded by mu
}

func New(cfg Config) *Agent {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	cfg.EngineURL = strings.TrimRight(cfg.EngineURL, "/")
	if cfg.JobPoll <= 0 {
		cfg.JobPoll = 2 * time.Second
	}
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
	a.send, a.node = func(m protocol.Message) error {
		if m.Type == "status" {
			statusMu.Lock()
			defer statusMu.Unlock()
		}
		return send(m)
	}, welcome.Node
	a.gen++
	gen := a.gen
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.notify, changed)
		if a.gen == gen {
			a.send = nil
		}
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
			if m.Method == http.MethodGet {
				// Video job status or content: no slot.
				go a.forward(ctx, m, welcome.Node, "", send)
				continue
			}
			model := requestModel(m.Body)
			if err := a.acquire(model); err != nil {
				a.Metrics.request(model, "refused")
				_ = send(protocol.Message{Type: "response_error", ID: m.ID, Message: err.Error()})
				continue
			}
			sendStatus()
			go func() {
				outcome, job := a.forward(ctx, m, welcome.Node, model, send)
				a.Metrics.request(model, outcome)
				if job != "" {
					// The video job keeps its slot until the engine finishes it.
					go a.watchJob(m.ID, job, model)
					return
				}
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
// It returns the outcome for metrics: "ok", "engine_error" or "failed", and
// for an accepted video job (POST /v1/videos) the engine's job id. After a
// chat response ends it sends this node's signed receipt; video jobs get
// theirs from watchJob.
func (a *Agent) forward(ctx context.Context, m protocol.Message, node, model string, send func(protocol.Message) error) (outcome, job string) {
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
	if !protocol.Allowed(m.Method, m.Path) {
		fail(fmt.Errorf("path not allowed: %s %s", m.Method, m.Path))
		return
	}
	var body io.Reader
	if m.Method == http.MethodPost {
		body = bytes.NewReader(m.Body)
	}
	req, err := http.NewRequestWithContext(ctx, m.Method, a.cfg.EngineURL+m.Path, body)
	if err != nil {
		fail(err)
		return
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	binary := strings.HasSuffix(m.Path, "/content")
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
			chunk := protocol.Message{Type: "response_chunk", ID: m.ID, Data: string(buf[:n])}
			if binary {
				chunk.Data, chunk.Encoding = base64.StdEncoding.EncodeToString(buf[:n]), "base64"
			} else {
				tail.Write(buf[:n])
			}
			if serr := send(chunk); serr != nil {
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
	video := m.Method == http.MethodPost && m.Path == "/v1/videos"
	if video && resp.StatusCode < 400 {
		job = parseJob(tail.b).ID
	}
	if send(protocol.Message{Type: "response_end", ID: m.ID}) == nil && m.Path == "/v1/chat/completions" {
		p, c, cached := tail.counts()
		_ = send(a.receipts.sign(protocol.Receipt{ID: m.ID, Node: node, Model: model, Status: resp.StatusCode,
			PromptTokens: p, CompletionTokens: c, CachedTokens: cached, TS: time.Now().UnixMilli()}))
	}
	if resp.StatusCode >= 400 {
		return "engine_error", ""
	}
	if video && job == "" {
		a.cfg.Log.Warn("video job without id", "id", m.ID)
	}
	return "ok", job
}

// watchJob checks a video job on the engine until it ends, then frees its
// slot and sends the receipt for request id on the current session.
func (a *Agent) watchJob(id, job, model string) {
	defer func() {
		a.release(model)
		a.sendCurrent(a.statusMessage())
	}()
	ctx, cancel := context.WithTimeout(context.Background(), maxJobTime)
	defer cancel()
	t := time.NewTicker(a.cfg.JobPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			a.cfg.Log.Warn("video job timed out", "id", id)
			return
		case <-t.C:
		}
		j, err := a.jobStatus(ctx, job)
		if err != nil || (j.Status != "completed" && j.Status != "failed") {
			continue
		}
		status := http.StatusOK
		if j.Status == "failed" {
			status = http.StatusInternalServerError
		}
		a.mu.Lock()
		node := a.node
		a.mu.Unlock()
		a.sendCurrent(a.receipts.sign(protocol.Receipt{ID: id, Node: node, Model: model, Status: status,
			VideoSeconds: j.Seconds, Size: j.Size, TS: time.Now().UnixMilli()}))
		return
	}
}

const maxJobTime = 6 * time.Hour

func (a *Agent) jobStatus(ctx context.Context, job string) (videoJob, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.EngineURL+"/v1/videos/"+job, nil)
	if err != nil {
		return videoJob{}, err
	}
	resp, err := a.cfg.HTTPClient.Do(req)
	if err != nil {
		return videoJob{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return videoJob{}, fmt.Errorf("job status: %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return videoJob{}, err
	}
	return parseJob(b), nil
}

// videoJob is what the agent reads from an engine's video job object: no prompt.
type videoJob struct {
	ID      string
	Status  string
	Seconds *float64
	Size    string
}

func parseJob(b []byte) videoJob {
	var raw struct {
		ID      string          `json:"id"`
		Status  string          `json:"status"`
		Seconds json.RawMessage `json:"seconds"`
		Size    string          `json:"size"`
	}
	if json.Unmarshal(b, &raw) != nil || !protocol.Allowed(http.MethodGet, "/v1/videos/"+raw.ID) {
		return videoJob{}
	}
	j := videoJob{ID: raw.ID, Status: raw.Status, Size: raw.Size}
	// OpenAI sends seconds as a string ("8"); some engines send a number.
	if s, err := strconv.ParseFloat(strings.Trim(string(raw.Seconds), `"`), 64); err == nil {
		j.Seconds = &s
	}
	return j
}

func (a *Agent) statusMessage() protocol.Message {
	total, byModel := a.free()
	return protocol.Message{Type: "status", FreeSlots: protocol.Int(total), FreeByModel: byModel}
}

// sendCurrent sends m on the current session, if there is one.
func (a *Agent) sendCurrent(m protocol.Message) {
	a.mu.Lock()
	send := a.send
	a.mu.Unlock()
	if send != nil {
		_ = send(m)
	}
}
