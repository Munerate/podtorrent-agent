// Command mock-engine is a fake OpenAI-compatible engine that echoes prompts.
package main

import (
	"flag"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Munerate/podtorrent-agent/internal/mockengine"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8000", "listen address")
	models := flag.String("models", "mock/echo-1", "comma-separated model ids")
	delay := flag.Duration("delay", 50*time.Millisecond, "pause between streamed tokens")
	flag.Parse()
	log.Printf("mock engine on http://%s serving %s", *addr, *models)
	log.Fatal(http.ListenAndServe(*addr, mockengine.Handler(strings.Split(*models, ","), *delay)))
}
