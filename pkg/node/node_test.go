package node

import (
	"testing"

	"github.com/Munerate/podtorrent-agent/pkg/manifest"
)

func TestStatusFollowsManifest(t *testing.T) {
	n := New(Config{Token: "t", Manifest: manifest.Manifest{Node: "example-node-01",
		Models: []manifest.Model{{ID: "b", Slots: 2}, {ID: "a", Slots: 1}}}})
	st := n.Status()
	if st.Connected || len(st.Models) != 2 || st.Models[0].ID != "a" || st.Models[1].Slots != 2 || st.Models[1].Busy != 0 {
		t.Fatalf("Status = %+v", st)
	}
	n.SetManifest(manifest.Manifest{Node: "example-node-01", Models: []manifest.Model{{ID: "c", Slots: 3}}})
	if st := n.Status(); len(st.Models) != 1 || st.Models[0].ID != "c" {
		t.Fatalf("after SetManifest: %+v", st)
	}
}
