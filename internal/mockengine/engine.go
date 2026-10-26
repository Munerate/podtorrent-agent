// Package mockengine is a tiny OpenAI-compatible engine for tests. It echoes
// the last user message back, word by word, with or without streaming, and
// runs fake video jobs (POST /v1/videos) that finish after ten token delays
// and whose content is VideoBytesPerSecond bytes of filler per second.
package mockengine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// VideoBytesPerSecond is the size of a mock clip per second of video.
const VideoBytesPerSecond = 64 << 10

type videoJob struct {
	id, model, seconds, size string
	created                  time.Time
}

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

// Handler serves POST /v1/chat/completions, GET /v1/models and the video job
// API. delay is the pause between streamed tokens.
func Handler(models []string, delay time.Duration) http.Handler {
	mux := http.NewServeMux()
	var (
		jobsMu sync.Mutex
		jobs   = map[string]*videoJob{}
		seq    atomic.Int64
	)
	jobTime := max(10*delay, 10*time.Millisecond)
	view := func(j *videoJob) map[string]any {
		status, progress := "in_progress", 50
		if time.Since(j.created) >= jobTime {
			status, progress = "completed", 100
		}
		return map[string]any{"id": j.id, "object": "video", "model": j.model, "status": status, "progress": progress,
			"seconds": j.seconds, "size": j.size, "created_at": j.created.Unix()}
	}
	job := func(w http.ResponseWriter, r *http.Request) *videoJob {
		jobsMu.Lock()
		j := jobs[r.PathValue("id")]
		jobsMu.Unlock()
		if j == nil {
			http.Error(w, `{"error":{"message":"no such video"}}`, http.StatusNotFound)
		}
		return j
	}
	mux.HandleFunc("POST /v1/videos", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model   string          `json:"model"`
			Seconds json.RawMessage `json:"seconds"`
			Size    string          `json:"size"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":{"message":"bad json"}}`, http.StatusBadRequest)
			return
		}
		seconds := strings.Trim(string(req.Seconds), `"`)
		if _, err := strconv.ParseFloat(seconds, 64); err != nil {
			seconds = "4"
		}
		if req.Size == "" {
			req.Size = "1280x720"
		}
		j := &videoJob{id: fmt.Sprintf("video_mock_%d", seq.Add(1)), model: req.Model, seconds: seconds, size: req.Size, created: time.Now()}
		jobsMu.Lock()
		jobs[j.id] = j
		jobsMu.Unlock()
		v := view(j)
		v["status"], v["progress"] = "queued", 0
		writeJSON(w, v)
	})
	mux.HandleFunc("GET /v1/videos/{id}", func(w http.ResponseWriter, r *http.Request) {
		if j := job(w, r); j != nil {
			writeJSON(w, view(j))
		}
	})
	mux.HandleFunc("GET /v1/videos/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		j := job(w, r)
		if j == nil {
			return
		}
		if view(j)["status"] != "completed" {
			http.Error(w, `{"error":{"message":"video not ready"}}`, http.StatusConflict)
			return
		}
		secs, _ := strconv.ParseFloat(j.seconds, 64)
		w.Header().Set("Content-Type", "video/mp4")
		clip := make([]byte, int(secs*VideoBytesPerSecond))
		for i := range clip {
			clip[i] = byte(i % 251)
		}
		_, _ = w.Write(clip)
	})
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
