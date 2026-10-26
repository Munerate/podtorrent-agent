// Package protocol holds the message types of the PodTorrent node protocol v0.
// See spec/protocol-v0.md.
package protocol

import (
	"encoding/json"
	"strings"
)

const Version = 0

// Message is the envelope for every frame. Only the fields for its Type are set.
type Message struct {
	Type        string            `json:"type"`
	Protocol    *int              `json:"protocol,omitempty"`
	Agent       string            `json:"agent,omitempty"`
	Node        string            `json:"node,omitempty"`
	Manifest    *Manifest         `json:"manifest,omitempty"`
	FreeSlots   *int              `json:"free_slots,omitempty"`
	FreeByModel map[string]int    `json:"free_by_model,omitempty"`
	ID          string            `json:"id,omitempty"`
	Method      string            `json:"method,omitempty"`
	Path        string            `json:"path,omitempty"`
	Body        json.RawMessage   `json:"body,omitempty"`
	Status      int               `json:"status,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Data        string            `json:"data,omitempty"`
	Encoding    string            `json:"encoding,omitempty"` // "base64" for binary response_chunk data
	Message     string            `json:"message,omitempty"`
	ReceiptKey  string            `json:"receipt_key,omitempty"` // base64 Ed25519 public key (hello, welcome)
	Receipt     string            `json:"receipt,omitempty"`     // serialised Receipt; Sig signs these bytes
	Sig         string            `json:"sig,omitempty"`
}

// Receipt is one side's account of a finished request (spec: "Receipts").
// The network's receipt adds Tier, CostUSD and NodeSig.
type Receipt struct {
	ID               string   `json:"id"`
	Node             string   `json:"node"`
	Model            string   `json:"model"`
	Status           int      `json:"status"`
	PromptTokens     *int     `json:"prompt_tokens"`
	CompletionTokens *int     `json:"completion_tokens"`
	CachedTokens     *int     `json:"cached_tokens"`
	VideoSeconds     *float64 `json:"video_seconds,omitempty"` // video jobs only
	Size             string   `json:"size,omitempty"`          // video jobs only
	TS               int64    `json:"ts"`
	Tier             string   `json:"tier,omitempty"`
	CostUSD          *float64 `json:"cost_usd,omitempty"`
	NodeSig          *string  `json:"node_sig,omitempty"`
}

// SameCounts reports whether two receipts agree on the fields that are compared.
func (r Receipt) SameCounts(o Receipt) bool {
	eq := func(a, b *int) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	eqf := func(a, b *float64) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	return r.ID == o.ID && r.Node == o.Node && r.Model == o.Model && r.Status == o.Status &&
		eq(r.PromptTokens, o.PromptTokens) && eq(r.CompletionTokens, o.CompletionTokens) && eq(r.CachedTokens, o.CachedTokens) &&
		eqf(r.VideoSeconds, o.VideoSeconds)
}

type Manifest struct {
	Node   string   `json:"node"`
	Models []Model  `json:"models"`
	Tiers  []string `json:"tiers,omitempty"` // "interactive", "batch"; default both
	Price  *Price   `json:"price,omitempty"`
	// Declared full-load power draw, for tokens per watt on the dashboard.
	PowerWatts float64 `json:"power_watts,omitempty"`
}

type Model struct {
	ID         string `json:"id"`
	Quant      string `json:"quant,omitempty"`
	ContextMax int    `json:"context_max,omitempty"`
	Slots      int    `json:"slots"`
	Speed      *Speed `json:"speed,omitempty"`
	// Slots batch work may not take, kept for interactive requests.
	InteractiveReserve int            `json:"interactive_reserve,omitempty"`
	CachedPrompts      []CachedPrompt `json:"cached_prompts,omitempty"`
	Price              *Price         `json:"price,omitempty"` // overrides the node-wide price
	PowerWatts         float64        `json:"power_watts,omitempty"`
	Modality           string         `json:"modality,omitempty"` // "text" (default) or "video"
}

// CachedPrompt is an opening of the conversation the engine keeps processed.
// SHA256 is computed as in spec/protocol-v0.md ("Cached prompts").
type CachedPrompt struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Tokens int    `json:"tokens,omitempty"`
}

type Speed struct {
	SingleTPS float64 `json:"single_tps,omitempty"`
	BatchTPS  float64 `json:"batch_tps,omitempty"`
}

type Price struct {
	PerMtokIn       float64 `json:"per_mtok_in"`
	PerMtokOut      float64 `json:"per_mtok_out"`
	PerMtokInCached float64 `json:"per_mtok_in_cached,omitempty"`
	PerVideoSecond  float64 `json:"per_video_second,omitempty"` // video models
	Batch           *Price  `json:"batch,omitempty"`            // batch-tier price
}

// TotalSlots is the node-wide concurrency limit.
func (m *Manifest) TotalSlots() int {
	n := 0
	for _, mod := range m.Models {
		n += mod.Slots
	}
	return n
}

// Allowed reports whether the network may ask a node to call method path on
// its engine (spec: the request table).
func Allowed(method, path string) bool {
	switch method {
	case "POST":
		return path == "/v1/chat/completions" || path == "/v1/videos"
	case "GET":
		id, rest, _ := strings.Cut(strings.TrimPrefix(path, "/v1/videos/"), "/")
		return strings.HasPrefix(path, "/v1/videos/") && validJobID(id) && (rest == "" || rest == "content")
	}
	return false
}

// VideoJobID returns the job id in a video status or content path, or "".
func VideoJobID(path string) string {
	id, _, _ := strings.Cut(strings.TrimPrefix(path, "/v1/videos/"), "/")
	return id
}

func validJobID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	if id == "." || id == ".." {
		return false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

func Int(v int) *int { return &v }
