// Command podtorrent-conformance checks that a node (this agent or your own
// implementation) follows the PodTorrent node protocol v0. It plays the
// network: either it listens for an outbound node, or it dials an inbound one.
//
//	podtorrent-conformance -listen 127.0.0.1:8787           # then start the node with -network ws://127.0.0.1:8787/v0/node/connect
//	podtorrent-conformance -dial http://127.0.0.1:8443/v0/node/connect -token <node token>
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/coder/websocket"

	"github.com/Munerate/podtorrent-agent/internal/conformance"
)

func main() {
	listen := flag.String("listen", "", "listen here for an outbound node")
	dial := flag.String("dial", "", "dial an inbound node at this URL")
	token := flag.String("token", os.Getenv("PODTORRENT_NODE_TOKEN"), "node token the node expects/presents")
	prompt := flag.String("prompt", "Reply with the word OK.", "user message sent to the node's model")
	timeout := flag.Duration("timeout", 2*time.Minute, "time allowed per request")
	flag.Parse()

	ctx := context.Background()
	var report *conformance.Report
	switch {
	case *dial != "":
		conn, _, err := websocket.Dial(ctx, *dial, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + *token}}})
		if err != nil {
			fmt.Fprintln(os.Stderr, "dial:", err)
			os.Exit(2)
		}
		report = conformance.Run(ctx, conn, *prompt, *timeout)
	case *listen != "":
		done := make(chan *conformance.Report, 1)
		srv := &http.Server{Addr: *listen, ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v0/node/connect" {
				http.NotFound(w, r)
				return
			}
			if *token != "" && r.Header.Get("Authorization") != "Bearer "+*token {
				http.Error(w, "bad node token", http.StatusUnauthorized)
				return
			}
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			done <- conformance.Run(r.Context(), conn, *prompt, *timeout)
			_ = conn.Close(websocket.StatusNormalClosure, "conformance done")
		})}
		go func() { _ = srv.ListenAndServe() }()
		fmt.Printf("waiting for a node on ws://%s/v0/node/connect\n", *listen)
		report = <-done
	default:
		fmt.Fprintln(os.Stderr, "give -listen or -dial")
		os.Exit(2)
	}
	fmt.Print(report)
	if !report.OK() {
		os.Exit(1)
	}
	fmt.Println("conformant")
}
