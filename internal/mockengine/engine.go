// Package mockengine is a tiny OpenAI-compatible engine for tests. It echoes
// the last user message back, word by word, with or without streaming.
package mockengine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type chatRequest struct {
	Model         string `json:"model"`
	Stream        bool   `json:"stream"`
	StreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

// Handler serves POST /v1/chat/completions and GET /v1/models.
// delay is the pause between streamed tokens.
func Handler(models []string, delay time.Duration) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		data := []map[string]string{}
		for _, m := range models {
			data = append(data, map[string]string{"id": m, "object": "model"})
		}
		writeJSON(w, map[string]any{"object": "list", "data": data})
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":{"message":"bad json"}}`, http.StatusBadRequest)
			return
		}
		prompt := ""
		if n := len(req.Messages); n > 0 {
			prompt = req.Messages[n-1].Content
		}
		words := strings.Fields("echo: " + prompt)
		id := fmt.Sprintf("chatcmpl-mock-%d", time.Now().UnixNano())
		usage := map[string]int{"prompt_tokens": len(strings.Fields(prompt)), "completion_tokens": len(words),
			"total_tokens": len(strings.Fields(prompt)) + len(words)}

		if !req.Stream {
			writeJSON(w, map[string]any{
				"id": id, "object": "chat.completion", "model": req.Model,
				"choices": []any{map[string]any{"index": 0, "finish_reason": "stop",
					"message": map[string]string{"role": "assistant", "content": strings.Join(words, " ")}}},
				"usage": usage,
			})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		send := func(v any) {
			b, _ := json.Marshal(v)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if flusher != nil {
				flusher.Flush()
			}
		}
		chunk := func(delta map[string]string, finish any) {
			send(map[string]any{"id": id, "object": "chat.completion.chunk", "model": req.Model,
				"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		}
		for i, word := range words {
			if i > 0 {
				word = " " + word
			}
			chunk(map[string]string{"content": word}, nil)
			select {
			case <-r.Context().Done():
				return
			case <-time.After(delay):
			}
		}
		chunk(map[string]string{}, "stop")
		if req.StreamOptions.IncludeUsage {
			send(map[string]any{"id": id, "object": "chat.completion.chunk", "model": req.Model, "choices": []any{}, "usage": usage})
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
