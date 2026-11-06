package manifest

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	m := Manifest{Node: "example-node-01", Models: []Model{{ID: "mock/echo-1", Slots: 2}}}
	if err := Save(path, m); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, m) {
		t.Fatalf("got %+v, want %+v", got, m)
	}
	if err := Save(path, Manifest{Node: "example-node-01"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted a manifest with no slots")
	}
}

func TestValidate(t *testing.T) {
	ok := Manifest{Node: "example-node-01", Tiers: []string{"interactive"},
		Models: []Model{{ID: "mock/echo-1", Slots: 4, InteractiveReserve: 1}}}
	if ps := Validate(ok); len(ps) != 0 {
		t.Fatalf("valid manifest: %v", ps)
	}
	bad := Manifest{Tiers: []string{"fast"}, Price: &Price{PerMtokIn: -1},
		Models: []Model{
			{ID: "a", Slots: 1, InteractiveReserve: 2, Modality: "audio"},
			{ID: "a", CachedPrompts: []CachedPrompt{{Name: "p", SHA256: "xyz"}}},
		}}
	var fields []string
	for _, p := range Validate(bad) {
		fields = append(fields, p.Field)
	}
	want := []string{"node", "tiers[0]", "price", "models[0].interactive_reserve", "models[0].modality",
		"models[1].id", "models[1].slots", "models[1].cached_prompts[0].sha256"}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("problems in %v, want %v", fields, want)
	}
}

func TestValidatePipeline(t *testing.T) {
	m := func(p *Pipeline) Manifest {
		return Manifest{Node: "example-node-01", Models: []Model{{ID: "mock/echo-1", Slots: 1, Pipeline: p}}}
	}
	for _, p := range []*Pipeline{
		{Layers: 48},
		{Layers: 48, Hops: []Hop{{Peer: "mac-a", Lo: 0, Hi: 24}, {Peer: "mac-b", Lo: 24, Hi: 48}}},
	} {
		if ps := Validate(m(p)); len(ps) != 0 {
			t.Fatalf("%+v: %v", p, ps)
		}
	}
	for p, field := range map[*Pipeline]string{
		{}: "models[0].pipeline.layers",
		{Layers: 48, Hops: []Hop{{Lo: 0, Hi: 24}, {Lo: 20, Hi: 48}}}: "models[0].pipeline.hops[1]",
		{Layers: 48, Hops: []Hop{{Lo: 0, Hi: 50}}}:                   "models[0].pipeline.hops[0]",
		{Layers: 48, Hops: []Hop{{Lo: 0, Hi: 24}}}:                   "models[0].pipeline.hops",
	} {
		ps := Validate(m(p))
		if len(ps) != 1 || ps[0].Field != field {
			t.Fatalf("%+v: problems %v, want one in %s", p, ps, field)
		}
	}
}
