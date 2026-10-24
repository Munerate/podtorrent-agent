package agent

import (
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

// InboundHandler serves inbound mode: instead of dialing out, the agent
// listens and the network connects to it. The network must present the
// node's token; in production the listener also requires the network's
// client certificate (see TLSConfig). One network connection is active at a
// time; a new one replaces the old.
func (a *Agent) InboundHandler() http.Handler {
	var mu sync.Mutex
	var current *websocket.Conn
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v0/node/connect", func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(a.cfg.Token)) != 1 {
			http.Error(w, "bad node token", http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		if current != nil {
			_ = current.Close(websocket.StatusNormalClosure, "replaced by a newer connection")
		}
		current = conn
		mu.Unlock()
		err = a.Serve(r.Context(), conn, r.RemoteAddr)
		a.cfg.Log.Warn("network connection ended", "from", r.RemoteAddr, "err", err)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	return mux
}

// TLSConfig builds the listener's TLS settings. With clientCA set, only
// clients whose certificate chains to it (the PodTorrent network) may connect.
func TLSConfig(certFile, keyFile, clientCA string) (*tls.Config, error) {
	if certFile == "" || keyFile == "" {
		return nil, errors.New("inbound TLS needs both -tls-cert and -tls-key")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	if clientCA != "" {
		pem, err := os.ReadFile(clientCA)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates in %s", clientCA)
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}
