// Package engine checks an operator's local OpenAI-compatible engine before
// it is put on the network. It is part of the public API for programs built
// on the agent, such as a desktop app.
package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Munerate/podtorrent-agent/internal/agent"
	"github.com/Munerate/podtorrent-agent/internal/mockengine"
	"github.com/Munerate/podtorrent-agent/pkg/manifest"
)

// Check asks the engine for its models (GET /v1/models) and returns the
// manifest models it does not serve. The agent refuses to start while any
// are missing.
func Check(ctx context.Context, engineURL string, m manifest.Manifest) (missing []string, err error) {
	return agent.CheckEngine(ctx, &http.Client{Timeout: 5 * time.Second}, engineURL, m)
}

// ChatResult is the outcome of TestChat.
type ChatResult struct {
	FirstToken time.Duration // time to the first streamed token
	Total      time.Duration
	Chunks     int    // streamed chunks with content
	Reply      string // the model's answer
}

// TestChat sends one short streamed chat request for model, the way the
// network will, and reports how the engine answered.
func TestChat(ctx context.Context, engineURL, model, prompt string) (ChatResult, error) {
	var res ChatResult
	body, _ := json.Marshal(map[string]any{
		"model": model, "stream": true, "max_tokens": 32,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(engineURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return res, err
	}
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return res, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("POST /v1/chat/completions: %s", resp.Status)
	}
	var reply strings.Builder
	done := false
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		if data == "[DONE]" {
			done = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return res, fmt.Errorf("stream: %w", err)
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content == "" {
				continue
			}
			if res.Chunks == 0 {
				res.FirstToken = time.Since(start)
			}
			res.Chunks++
			reply.WriteString(c.Delta.Content)
		}
	}
	if err := sc.Err(); err != nil {
		return res, err
	}
	res.Total, res.Reply = time.Since(start), reply.String()
	if !done {
		return res, fmt.Errorf("stream ended without data: [DONE]")
	}
	if res.Chunks == 0 {
		return res, fmt.Errorf("stream had no content")
	}
	return res, nil
}

// MockHandler is a stand-in engine serving models, for trying the setup
// without a real engine. Serve it on loopback only.
func MockHandler(models []string) http.Handler {
	return mockengine.Handler(models, 50*time.Millisecond)
}
