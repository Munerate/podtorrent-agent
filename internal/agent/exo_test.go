package agent

import "testing"

func TestHasRemoteURL(t *testing.T) {
	for body, want := range map[string]bool{
		`{"messages":[{"role":"user","content":"see http://example.com"}]}`:                                               false,
		`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://x/y.png"}}]}]}`:           true,
		`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":" data:image/png;base64,AA"}}]}]}`: false,
		`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":"http://10.0.0.1/"}]}]}`:                  true,
		`{"messages":[{"role":"user","content":[{"type":"input_file","file":{"url":"file:///etc/hosts"}}]}]}`:             true,
		`{"tools":[{"type":"function","function":{"parameters":{"properties":{"url":{"type":"string"}}}}}]}`:              false,
		`not json`: false,
	} {
		if got := hasRemoteURL([]byte(body)); got != want {
			t.Errorf("hasRemoteURL(%s) = %v, want %v", body, got, want)
		}
	}
}
