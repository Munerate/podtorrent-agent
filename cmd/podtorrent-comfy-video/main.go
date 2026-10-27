// Command podtorrent-comfy-video serves the PodTorrent video job API in front
// of a ComfyUI server running a video model workflow. See
// docs/comfyui-video.md.
package main

import (
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
	workflow := flag.String("workflow", "workflow.json", "workflow in ComfyUI API format, with {{prompt}} and other placeholders")
	model := flag.String("model", "", "model id to serve, as in your manifest (required)")
	sizes := flag.String("sizes", "832x480,480x832,640x640", "allowed sizes, comma-separated; the first is the default")
	minSecs := flag.Float64("min-seconds", 5, "shortest clip")
	maxSecs := flag.Float64("max-seconds", 15, "longest clip")
	defSecs := flag.Float64("default-seconds", 5, "clip length when the request has none")
	fps := flag.Int("fps", 24, "frames per second; sets {{frames}}")
	frameMultiple := flag.Int("frame-multiple", 0, "if set, {{frames}} = n*multiple+1 (e.g. 8 for models that need 8n+1 frames)")
	keep := flag.Duration("keep", 24*time.Hour, "forget finished jobs after this (ComfyUI keeps its files)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	wf, err := os.ReadFile(*workflow)
	if err != nil {
		log.Error("read workflow", "err", err)
		os.Exit(1)
	}
	if *model == "" {
		log.Error("-model is required")
		os.Exit(1)
	}
	a, err := comfyvideo.New(comfyvideo.Config{ComfyURL: *comfy, Workflow: wf, Model: *model, Sizes: strings.Split(*sizes, ","),
		MinSecs: *minSecs, MaxSecs: *maxSecs, DefSecs: *defSecs, FPS: *fps, FrameMultiple: *frameMultiple, Keep: *keep, Log: log})
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	log.Info("serving video job API", "listen", *listen, "comfy", *comfy, "model", *model)
	srv := &http.Server{Addr: *listen, Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("listen", "err", err)
		os.Exit(1)
	}
}
