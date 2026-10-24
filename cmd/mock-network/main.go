// Command mock-network is a local stand-in for the PodTorrent network, for
// testing a node end to end.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/Munerate/podtorrent-agent/internal/mocknet"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8787", "listen address")
	nodeToken := flag.String("node-token", "dev-node-token", "token nodes must present")
	apiKey := flag.String("api-key", "dev-api-key", "API key clients must present")
	flag.Parse()
	log.Printf("mock network on http://%s (nodes: ws://%s/v0/node/connect)", *addr, *addr)
	log.Fatal(http.ListenAndServe(*addr, mocknet.New(*nodeToken, *apiKey).Handler()))
}
