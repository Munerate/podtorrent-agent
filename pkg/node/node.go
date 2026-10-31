// Package node runs the agent in-process: the outbound link from a local
// engine to the PodTorrent network. It is part of the public API for programs
// built on the agent, such as a desktop app. Inbound mode is CLI-only.
package node

import (
	"context"
	"log/slog"
	"sort"

	"github.com/Munerate/podtorrent-agent/internal/agent"
	"github.com/Munerate/podtorrent-agent/pkg/manifest"
)

const (
	DefaultNetworkURL = "wss://api.podtorrent.com/v0/node/connect"
	DefaultEngineURL  = "http://127.0.0.1:8000"
	Version           = agent.Name
)

type Config struct {
	NetworkURL string // default DefaultNetworkURL
	Token      string
	EngineURL  string // default DefaultEngineURL
	Manifest   manifest.Manifest
	Log        *slog.Logger // never given prompt or response content
}

type Node struct{ a *agent.Agent }

func New(cfg Config) *Node {
	if cfg.NetworkURL == "" {
		cfg.NetworkURL = DefaultNetworkURL
	}
	if cfg.EngineURL == "" {
		cfg.EngineURL = DefaultEngineURL
	}
	return &Node{agent.New(agent.Config{NetworkURL: cfg.NetworkURL, Token: cfg.Token, EngineURL: cfg.EngineURL, Manifest: cfg.Manifest, Log: cfg.Log})}
}

// Run keeps the link up, reconnecting with backoff, until ctx ends.
func (n *Node) Run(ctx context.Context) error { return n.a.Run(ctx) }

// SetManifest replaces the manifest; a connected node sends it to the network
// at once.
func (n *Node) SetManifest(m manifest.Manifest) { n.a.SetManifest(m) }

func (n *Node) Manifest() manifest.Manifest { return n.a.Manifest() }

type Status struct {
	Connected bool
	Connects  int64 // links established since start
	Models    []ModelStatus
	Receipts  map[string]int64 // network receipts by result: matched, mismatch, bad_signature, unknown
}

type ModelStatus struct {
	ID       string
	Slots    int
	Busy     int
	Requests map[string]int64 // by outcome: ok, engine_error, failed, refused
}

// Status reports the link and per-model counters. It holds no prompt or
// response content.
func (n *Node) Status() Status {
	s := n.a.Snapshot()
	st := Status{Connected: s.Connected, Connects: s.Connects, Receipts: s.Receipts}
	byID := map[string]*ModelStatus{}
	for _, mod := range n.a.Manifest().Models {
		st.Models = append(st.Models, ModelStatus{ID: mod.ID, Slots: s.Slots[mod.ID], Busy: s.Busy[mod.ID], Requests: map[string]int64{}})
	}
	for i := range st.Models {
		byID[st.Models[i].ID] = &st.Models[i]
	}
	for k, v := range s.Requests {
		if ms := byID[k[0]]; ms != nil {
			ms.Requests[k[1]] = v
		}
	}
	sort.Slice(st.Models, func(i, j int) bool { return st.Models[i].ID < st.Models[j].ID })
	return st
}
