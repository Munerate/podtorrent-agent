// Package manifest reads, checks and writes node manifests
// (spec/protocol-v0.md, "Manifest"). It is part of the public API for
// programs built on the agent, such as a desktop app.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/Munerate/podtorrent-agent/internal/protocol"
)

type (
	Manifest     = protocol.Manifest
	Model        = protocol.Model
	Price        = protocol.Price
	Speed        = protocol.Speed
	CachedPrompt = protocol.CachedPrompt
)

// Load reads a manifest file. It fails if the file is not valid JSON or has
// no node name or no slots; Validate finds the other problems.
func Load(path string) (Manifest, error) {
	var m Manifest
	raw, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("manifest: %w", err)
	}
	if m.Node == "" || m.TotalSlots() == 0 {
		return m, fmt.Errorf("manifest needs a node name and at least one model with slots > 0")
	}
	return m, nil
}

// Save writes m to path atomically, so an agent watching the file never reads
// half of it.
func Save(path string, m Manifest) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".manifest-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Problem is one thing wrong with a manifest. Field is a JSON path such as
// "models[0].slots".
type Problem struct {
	Field   string
	Message string
}

func (p Problem) String() string { return p.Field + ": " + p.Message }

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Validate returns every problem it finds in m; none means the network will
// accept it.
func Validate(m Manifest) []Problem {
	var ps []Problem
	add := func(field, format string, args ...any) {
		ps = append(ps, Problem{field, fmt.Sprintf(format, args...)})
	}
	if m.Node == "" {
		add("node", "is required")
	}
	for i, t := range m.Tiers {
		if t != "interactive" && t != "batch" {
			add(fmt.Sprintf("tiers[%d]", i), "must be interactive or batch, not %q", t)
		}
	}
	if m.PowerWatts < 0 {
		add("power_watts", "must not be negative")
	}
	checkPrice(add, "price", m.Price)
	if len(m.Models) == 0 {
		add("models", "needs at least one model")
	}
	seen := map[string]bool{}
	for i, mod := range m.Models {
		f := fmt.Sprintf("models[%d]", i)
		switch {
		case mod.ID == "":
			add(f+".id", "is required")
		case seen[mod.ID]:
			add(f+".id", "%q is listed twice", mod.ID)
		}
		seen[mod.ID] = true
		if mod.Slots <= 0 {
			add(f+".slots", "must be at least 1")
		}
		if mod.InteractiveReserve < 0 || mod.InteractiveReserve > mod.Slots {
			add(f+".interactive_reserve", "must be between 0 and slots (%d)", mod.Slots)
		}
		if mod.ContextMax < 0 {
			add(f+".context_max", "must not be negative")
		}
		if mod.PowerWatts < 0 {
			add(f+".power_watts", "must not be negative")
		}
		if mod.Modality != "" && mod.Modality != "text" && mod.Modality != "video" {
			add(f+".modality", "must be text or video, not %q", mod.Modality)
		}
		if mod.Speed != nil && (mod.Speed.SingleTPS < 0 || mod.Speed.BatchTPS < 0) {
			add(f+".speed", "must not be negative")
		}
		checkPrice(add, f+".price", mod.Price)
		for j, cp := range mod.CachedPrompts {
			g := fmt.Sprintf("%s.cached_prompts[%d]", f, j)
			if cp.Name == "" {
				add(g+".name", "is required")
			}
			if !sha256Hex.MatchString(cp.SHA256) {
				add(g+".sha256", "must be 64 lowercase hex digits")
			}
		}
	}
	return ps
}

func checkPrice(add func(string, string, ...any), field string, p *Price) {
	if p == nil {
		return
	}
	if p.PerMtokIn < 0 || p.PerMtokOut < 0 || p.PerMtokInCached < 0 || p.PerVideoSecond < 0 {
		add(field, "prices must not be negative")
	}
	checkPrice(add, field+".batch", p.Batch)
}
