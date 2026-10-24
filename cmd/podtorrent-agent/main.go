// Command podtorrent-agent connects a local OpenAI-compatible engine to the
// PodTorrent network.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Munerate/podtorrent-agent/internal/agent"
	"github.com/Munerate/podtorrent-agent/internal/protocol"
)

func main() {
	network := flag.String("network", env("PODTORRENT_NETWORK", "wss://network.podtorrent.com/v0/node/connect"), "network link URL")
	engine := flag.String("engine", env("PODTORRENT_ENGINE", "http://127.0.0.1:8000"), "local OpenAI-compatible engine base URL")
	showVersion := flag.Bool("version", false, "print version and exit")
	skipCheck := flag.Bool("skip-engine-check", false, "don't check the engine's /v1/models at start")
	listen := flag.String("listen", env("PODTORRENT_LISTEN", ""), "inbound mode: listen here for the network instead of dialing out (e.g. :8443)")
	tlsCert := flag.String("tls-cert", env("PODTORRENT_TLS_CERT", ""), "inbound mode: server certificate (PEM)")
	tlsKey := flag.String("tls-key", env("PODTORRENT_TLS_KEY", ""), "inbound mode: server key (PEM)")
	clientCA := flag.String("client-ca", env("PODTORRENT_CLIENT_CA", ""), "inbound mode: require network client certificates signed by this CA (PEM)")
	insecure := flag.Bool("insecure-plaintext", false, "inbound mode: serve without TLS (only behind a tunnel or for local tests)")
	manifestPath := flag.String("manifest", env("PODTORRENT_MANIFEST", "manifest.json"), "node manifest file")
	flag.Parse()

	if *showVersion {
		fmt.Println(agent.Name)
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	token := os.Getenv("PODTORRENT_NODE_TOKEN")
	if token == "" {
		fatal(log, "PODTORRENT_NODE_TOKEN is not set")
	}
	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		fatal(log, err.Error())
	}
	var m protocol.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		fatal(log, fmt.Sprintf("manifest: %v", err))
	}
	if m.Node == "" || m.TotalSlots() == 0 {
		fatal(log, "manifest needs a node name and at least one model with slots > 0")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if !*skipCheck {
		checkEngine(ctx, log, *engine, m)
	}
	a := agent.New(agent.Config{NetworkURL: *network, Token: token, EngineURL: *engine, Manifest: m, Log: log})
	if *listen == "" {
		_ = a.Run(ctx)
		return
	}

	srv := &http.Server{Addr: *listen, Handler: a.InboundHandler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	if *insecure {
		log.Warn("inbound mode without TLS: use only behind a tunnel or for local tests", "listen", *listen)
		err = srv.ListenAndServe()
	} else {
		if srv.TLSConfig, err = agent.TLSConfig(*tlsCert, *tlsKey, *clientCA); err != nil {
			fatal(log, err.Error())
		}
		if *clientCA == "" {
			log.Warn("inbound mode without -client-ca: any client with the node token can connect")
		}
		log.Info("inbound mode", "listen", *listen, "mtls", *clientCA != "")
		err = srv.ListenAndServeTLS("", "")
	}
	if err != nil && err != http.ErrServerClosed {
		fatal(log, err.Error())
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fatal(log *slog.Logger, msg string) {
	log.Error(msg)
	os.Exit(1)
}

// checkEngine waits up to a minute for the engine, then refuses to start if it
// doesn't serve every model in the manifest.
func checkEngine(ctx context.Context, log *slog.Logger, engine string, m protocol.Manifest) {
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(time.Minute)
	for {
		missing, err := agent.CheckEngine(ctx, client, engine, m)
		if err == nil {
			if len(missing) > 0 {
				fatal(log, fmt.Sprintf("engine at %s does not list these manifest models: %v", engine, missing))
			}
			log.Info("engine ok", "engine", engine, "models", len(m.Models))
			return
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			fatal(log, fmt.Sprintf("engine at %s not reachable: %v", engine, err))
		}
		log.Warn("waiting for engine", "err", err)
		time.Sleep(2 * time.Second)
	}
}
