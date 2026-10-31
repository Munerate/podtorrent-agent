// Command podtorrent-agent connects a local OpenAI-compatible engine to the
// PodTorrent network.
package main

import (
	"context"
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
	"github.com/Munerate/podtorrent-agent/pkg/manifest"
)

func main() {
	network := flag.String("network", env("PODTORRENT_NETWORK", "wss://api.podtorrent.com/v0/node/connect"), "network link URL")
	engine := flag.String("engine", env("PODTORRENT_ENGINE", "http://127.0.0.1:8000"), "local OpenAI-compatible engine base URL")
	showVersion := flag.Bool("version", false, "print version and exit")
	skipCheck := flag.Bool("skip-engine-check", false, "don't check the engine's /v1/models at start")
	listen := flag.String("listen", env("PODTORRENT_LISTEN", ""), "inbound mode: listen here for the network instead of dialing out (e.g. :8443)")
	tlsCert := flag.String("tls-cert", env("PODTORRENT_TLS_CERT", ""), "inbound mode: server certificate (PEM)")
	tlsKey := flag.String("tls-key", env("PODTORRENT_TLS_KEY", ""), "inbound mode: server key (PEM)")
	clientCA := flag.String("client-ca", env("PODTORRENT_CLIENT_CA", ""), "inbound mode: require network client certificates signed by this CA (PEM)")
	insecure := flag.Bool("insecure-plaintext", false, "inbound mode: serve without TLS (only behind a tunnel or for local tests)")
	metricsListen := flag.String("metrics-listen", env("PODTORRENT_METRICS_LISTEN", ""), "serve Prometheus metrics at /metrics on this address (e.g. 127.0.0.1:9100)")
	jobPoll := flag.Duration("job-poll", 2*time.Second, "how often to check a running video job on the engine")
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
	m, err := manifest.Load(*manifestPath)
	if err != nil {
		fatal(log, err.Error())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if !*skipCheck {
		checkEngine(ctx, log, *engine, m)
	}
	a := agent.New(agent.Config{NetworkURL: *network, Token: token, EngineURL: *engine, Manifest: m, Log: log, JobPoll: *jobPoll})
	go watchManifest(ctx, log, a, *manifestPath, *engine, *skipCheck)
	if *metricsListen != "" {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", a.MetricsHandler())
		ms := &http.Server{Addr: *metricsListen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go func() { <-ctx.Done(); _ = ms.Close() }()
		go func() {
			if err := ms.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Error("metrics listener", "err", err)
			}
		}()
		log.Info("metrics", "url", "http://"+*metricsListen+"/metrics")
	}
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

// watchManifest reloads the manifest when the file changes (checked every
// 5 s) or on SIGHUP. An invalid file, or one naming models the engine
// doesn't serve, is logged and ignored; the running manifest stays.
func watchManifest(ctx context.Context, log *slog.Logger, a *agent.Agent, path, engine string, skipCheck bool) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	modTime := func() time.Time {
		if fi, err := os.Stat(path); err == nil {
			return fi.ModTime()
		}
		return time.Time{}
	}
	last := modTime()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
		case <-t.C:
			if mt := modTime(); mt.Equal(last) {
				continue
			} else {
				last = mt
			}
		}
		m, err := manifest.Load(path)
		if err != nil {
			log.Error("manifest not reloaded", "err", err)
			continue
		}
		if !skipCheck {
			c := &http.Client{Timeout: 5 * time.Second}
			if missing, err := agent.CheckEngine(ctx, c, engine, m); err != nil || len(missing) > 0 {
				log.Error("manifest not reloaded: engine check failed", "missing", missing, "err", err)
				continue
			}
		}
		a.SetManifest(m)
		log.Info("manifest reloaded", "models", len(m.Models))
	}
}
