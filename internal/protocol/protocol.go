// Package protocol holds the message types of the PodTorrent node protocol v0.
// See spec/protocol-v0.md.
package protocol

import "encoding/json"

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
	Message     string            `json:"message,omitempty"`
}

type Manifest struct {
	Node   string   `json:"node"`
	Models []Model  `json:"models"`
	Tiers  []string `json:"tiers,omitempty"` // "interactive", "batch"; default both
	Price  *Price   `json:"price,omitempty"`
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
	Batch           *Price  `json:"batch,omitempty"` // batch-tier price
}

// TotalSlots is the node-wide concurrency limit.
func (m *Manifest) TotalSlots() int {
	n := 0
	for _, mod := range m.Models {
		n += mod.Slots
	}
	return n
}

// AllowedPaths are the engine paths the network may ask a node to call.
var AllowedPaths = map[string]bool{"/v1/chat/completions": true}

func Int(v int) *int { return &v }
