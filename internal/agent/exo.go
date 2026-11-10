package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"
)

// EngineExo is Config.Engine for an exo cluster (github.com/exo-explore/exo).
// exo lists every model it knows at GET /v1/models, loaded or not, so the
// agent reads which models have a running instance from exo's state and
// offers only those. It also refuses requests that carry remote URLs (exo
// downloads image URLs itself, from the operator's network) and fails a
// stream that sends no event in time, so the network can retry it elsewhere.
const EngineExo = "exo"

// The only parts of exo's state the agent reads: instances and runner
// statuses. Never /state itself, which holds the prompts of running tasks.
const (
	exoInstancesPath = "/state/instances"
	exoRunnersPath   = "/state/runners"
)

// WatchEngine keeps track of which models the engine can serve, until ctx
// ends. Only exo needs it; for other engines it returns at once.
func (a *Agent) WatchEngine(ctx context.Context) {
	if a.cfg.Engine != EngineExo {
		return
	}
	t := time.NewTicker(a.cfg.EnginePoll)
	defer t.Stop()
	for {
		ready, err := a.exoReady(ctx)
		if err != nil {
			a.cfg.Log.Warn("exo state", "err", err)
			ready = nil // serve nothing while exo can't say what it runs
		}
		a.setReady(ready)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// setReady records the models the engine can serve now and tells the
// network when that changes.
func (a *Agent) setReady(ready []string) {
	slices.Sort(ready)
	a.mu.Lock()
	changed := !reflect.DeepEqual(a.ready, ready) || !a.readyKnown
	a.ready, a.readyKnown = ready, true
	a.mu.Unlock()
	if changed {
		a.cfg.Log.Info("engine models ready", "models", ready)
		a.sendCurrent(a.statusMessage())
	}
}

// engineReady says whether the engine can serve model now. Guarded by mu.
func (a *Agent) engineReady(model string) bool {
	return a.cfg.Engine != EngineExo || slices.Contains(a.ready, model)
}

// exoReady returns the models with at least one instance whose runners are
// all ready or running.
func (a *Agent) exoReady(ctx context.Context) ([]string, error) {
	var instances map[string]map[string]struct {
		ShardAssignments struct {
			ModelID       string                     `json:"modelId"`
			RunnerToShard map[string]json.RawMessage `json:"runnerToShard"`
		} `json:"shardAssignments"`
	}
	if err := a.exoGet(ctx, exoInstancesPath, &instances); err != nil {
		return nil, err
	}
	var runners map[string]map[string]json.RawMessage
	if err := a.exoGet(ctx, exoRunnersPath, &runners); err != nil {
		return nil, err
	}
	var ready []string
	for _, tagged := range instances {
		for _, inst := range tagged { // {"MlxRingInstance": {...}}
			sa := inst.ShardAssignments
			ok := sa.ModelID != "" && len(sa.RunnerToShard) > 0
			for runner := range sa.RunnerToShard {
				st := runners[runner]
				_, r := st["RunnerReady"]
				_, busy := st["RunnerRunning"]
				ok = ok && (r || busy)
			}
			if ok && !slices.Contains(ready, sa.ModelID) {
				ready = append(ready, sa.ModelID)
			}
		}
	}
	return ready, nil
}

func (a *Agent) exoGet(ctx context.Context, path string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.EngineURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := a.cfg.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v); err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	return nil
}

// hasRemoteURL reports whether a request body names a URL the engine would
// fetch: any "url" or "image_url" string that isn't inline data.
func hasRemoteURL(body []byte) bool {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return false
	}
	var walk func(any) bool
	walk = func(v any) bool {
		switch v := v.(type) {
		case map[string]any:
			for k, x := range v {
				if s, ok := x.(string); ok && (k == "url" || k == "image_url") && !strings.HasPrefix(strings.TrimSpace(s), "data:") {
					return true
				}
				if walk(x) {
					return true
				}
			}
		case []any:
			for _, x := range v {
				if walk(x) {
					return true
				}
			}
		}
		return false
	}
	return walk(v)
}

var errNoEvent = errors.New("engine sent no output in time")

// firstEvent reads an event stream until its first "data:" line, skipping
// keep-alive comments, and returns what it read. exo answers 200 and sends
// comments while a model isn't ready yet; waiting for real data lets the
// agent fail the request before anything reaches the client.
func firstEvent(body io.Reader, timeout time.Duration, cancel context.CancelFunc) ([]byte, error) {
	timer := time.AfterFunc(timeout, cancel)
	defer timer.Stop()
	var got []byte
	buf := make([]byte, 16<<10)
	for {
		n, err := body.Read(buf)
		got = append(got, buf[:n]...)
		if bytes.HasPrefix(got, []byte("data:")) || bytes.Contains(got, []byte("\ndata:")) {
			return got, nil
		}
		if err == io.EOF {
			return got, nil
		}
		if err != nil {
			if !timer.Stop() {
				return got, errNoEvent
			}
			return got, err
		}
	}
}
