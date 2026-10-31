package engine

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Munerate/podtorrent-agent/pkg/manifest"
)

func TestCheckAndTestChat(t *testing.T) {
	srv := httptest.NewServer(MockHandler([]string{"mock/echo-1"}))
	defer srv.Close()
	ctx := context.Background()

	m := manifest.Manifest{Node: "example-node-01", Models: []manifest.Model{{ID: "mock/echo-1", Slots: 1}, {ID: "other", Slots: 1}}}
	missing, err := Check(ctx, srv.URL, m)
	if err != nil || !reflect.DeepEqual(missing, []string{"other"}) {
		t.Fatalf("Check = %v, %v", missing, err)
	}

	res, err := TestChat(ctx, srv.URL, "mock/echo-1", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if res.Chunks == 0 || res.Reply == "" || res.FirstToken <= 0 || res.Total < res.FirstToken {
		t.Fatalf("TestChat = %+v", res)
	}
	srv.Close()
	if _, err := TestChat(ctx, srv.URL, "mock/echo-1", "hello"); err == nil {
		t.Fatal("TestChat succeeded with the engine down")
	}
}
