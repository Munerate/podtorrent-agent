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
