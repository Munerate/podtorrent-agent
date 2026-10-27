// Command podtorrent-comfy-video serves the PodTorrent video job API in front
// of a ComfyUI server running a video model workflow. See
// docs/comfyui-video.md.
package main

import (
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Munerate/podtorrent-agent/internal/comfyvideo"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8100", "address to serve the job API on (keep it on loopback); use it as the agent's -engine")
	comfy := flag.String("comfy", "http://127.0.0.1:8188", "ComfyUI base URL")
	workflow := flag.String("workflow", "workflow.json", "workflow in ComfyUI API format with placeholders; or quality modes: best=a.json,fast=b.json")
	defMode := flag.String("default-quality", "", "quality mode when the request has none (default: the first in -workflow)")
	model := flag.String("model", "", "model id to serve, as in your manifest (required)")
	sizes := flag.String("sizes", "832x480,480x832,640x640", "allowed sizes, comma-separated; the first is the default")
	minSecs := flag.Float64("min-seconds", 5, "shortest clip")
	maxSecs := flag.Float64("max-seconds", 15, "longest clip")
	defSecs := flag.Float64("default-seconds", 5, "clip length when the request has none")
	fps := flag.Int("fps", 24, "frames per second; sets {{frames}}")
	frameMultiple := flag.Int("frame-multiple", 0, "if set, {{frames}} = n*multiple+offset, nearest to seconds*fps (e.g. 8 for 8n+1, 17 for 17n+5)")
	frameOffset := flag.Int("frame-offset", 1, "offset for -frame-multiple (1 for 8n+1, 5 for 17n+5)")
	keep := flag.Duration("keep", 24*time.Hour, "forget finished jobs after this")
	maxQueued := flag.Int("max-queued", 4, "unfinished jobs allowed beyond the one rendering; more get 429")
	state := flag.String("state", "comfy-video-jobs.json", "file that keeps jobs across restarts (no prompts); empty to keep them in memory only")
	outputDir := flag.String("output-dir", "", "ComfyUI's output folder; if set, clips are deleted from it after -keep")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	workflows, first := map[string]json.RawMessage{}, ""
	for _, item := range strings.Split(*workflow, ",") {
		mode, file, ok := strings.Cut(item, "=")
		if !ok {
			mode, file = "default", item
		}
		wf, err := os.ReadFile(file)
		if err != nil {
			log.Error("read workflow", "err", err)
			os.Exit(1)
		}
		workflows[mode] = wf
		if first == "" {
			first = mode
		}
	}
	if *defMode == "" {
		*defMode = first
	}
	if *model == "" {
		log.Error("-model is required")
		os.Exit(1)
	}
	a, err := comfyvideo.New(comfyvideo.Config{ComfyURL: *comfy, Workflows: workflows, DefaultMode: *defMode, Model: *model,
		Sizes: strings.Split(*sizes, ","), MinSecs: *minSecs, MaxSecs: *maxSecs, DefSecs: *defSecs, FPS: *fps,
		FrameMultiple: *frameMultiple, FrameOffset: *frameOffset, Keep: *keep, MaxQueued: *maxQueued, StatePath: *state, OutputDir: *outputDir, Log: log})
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	go func() {
		for range time.Tick(10 * time.Minute) {
			a.Prune()
		}
	}()
	log.Info("serving video job API", "listen", *listen, "comfy", *comfy, "model", *model, "quality", a.Modes())
	srv := &http.Server{Addr: *listen, Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("listen", "err", err)
		os.Exit(1)
	}
}
