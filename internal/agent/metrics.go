package agent

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
)

// Metrics are the agent's own counters, served in Prometheus text format by
// MetricsHandler. They never include prompt or response content.
type Metrics struct {
	up       atomic.Int64
	connects atomic.Int64
	mu       sync.Mutex
	requests map[[2]string]int64 // {model, outcome} -> count
}

func NewMetrics() *Metrics { return &Metrics{requests: map[[2]string]int64{}} }

func (m *Metrics) connected(up bool) {
	if up {
		m.up.Store(1)
		m.connects.Add(1)
	} else {
		m.up.Store(0)
	}
}

func (m *Metrics) request(model, outcome string) {
	m.mu.Lock()
	m.requests[[2]string{model, outcome}]++
	m.mu.Unlock()
}

// MetricsHandler serves /metrics. Bind it to localhost.
func (a *Agent) MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		m := a.Metrics
		fmt.Fprintf(w, "# HELP podtorrent_agent_connected 1 if the link to the network is up.\n# TYPE podtorrent_agent_connected gauge\npodtorrent_agent_connected %d\n", m.up.Load())
		fmt.Fprintf(w, "# HELP podtorrent_agent_connects_total Links established since start.\n# TYPE podtorrent_agent_connects_total counter\npodtorrent_agent_connects_total %d\n", m.connects.Load())

		man := a.Manifest()
		_, free := a.free()
		fmt.Fprint(w, "# HELP podtorrent_agent_slots Slots per model, from the manifest.\n# TYPE podtorrent_agent_slots gauge\n")
		for _, mod := range man.Models {
			fmt.Fprintf(w, "podtorrent_agent_slots{model=%q} %d\n", mod.ID, mod.Slots)
		}
		fmt.Fprint(w, "# HELP podtorrent_agent_busy_slots Requests in flight per model.\n# TYPE podtorrent_agent_busy_slots gauge\n")
		for _, mod := range man.Models {
			fmt.Fprintf(w, "podtorrent_agent_busy_slots{model=%q} %d\n", mod.ID, mod.Slots-free[mod.ID])
		}

		m.mu.Lock()
		keys := make([][2]string, 0, len(m.requests))
		for k := range m.requests {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
		fmt.Fprint(w, "# HELP podtorrent_agent_requests_total Requests by model and outcome (ok, engine_error, failed, refused).\n# TYPE podtorrent_agent_requests_total counter\n")
		for _, k := range keys {
			fmt.Fprintf(w, "podtorrent_agent_requests_total{model=%q,outcome=%q} %d\n", k[0], k[1], m.requests[k])
		}
		m.mu.Unlock()
	})
}
