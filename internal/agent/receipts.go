package agent

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/Munerate/podtorrent-agent/internal/protocol"
)

// usageTail keeps the end of a response body and reads the token counts from
// its last "usage" object (the whole JSON body, or the final SSE chunk).
// Only the counts are kept; the body is never stored or logged.
type usageTail struct{ b []byte }

const tailBytes = 8192

func (u *usageTail) Write(p []byte) {
	u.b = append(u.b, p...)
	if len(u.b) > tailBytes {
		u.b = append(u.b[:0:0], u.b[len(u.b)-tailBytes:]...)
	}
}

var countRe = map[string]*regexp.Regexp{}

func init() {
	for _, k := range []string{"prompt_tokens", "completion_tokens", "cached_tokens"} {
		countRe[k] = regexp.MustCompile(`"` + k + `"\s*:\s*(\d+)`)
	}
}

func (u *usageTail) counts() (prompt, completion, cached *int) {
	s := string(u.b)
	i := strings.LastIndex(s, `"usage"`)
	if i < 0 {
		return nil, nil, nil
	}
	s = s[i:]
	num := func(k string) *int {
		m := countRe[k].FindStringSubmatch(s)
		if m == nil {
			return nil
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return nil
		}
		return &n
	}
	return num("prompt_tokens"), num("completion_tokens"), num("cached_tokens")
}

// receipts signs this node's receipts and checks the network's.
type receipts struct {
	key ed25519.PrivateKey

	mu      sync.Mutex
	network ed25519.PublicKey           // from welcome; nil if the network sent none
	sent    map[string]protocol.Receipt // our receipts awaiting the network's, by id
}

func newReceipts() *receipts {
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		panic(err)
	}
	return &receipts{key: key, sent: map[string]protocol.Receipt{}}
}

func (r *receipts) publicKey() string {
	return base64.StdEncoding.EncodeToString(r.key.Public().(ed25519.PublicKey))
}

func (r *receipts) setNetworkKey(b64 string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.network = nil
	if k, err := base64.StdEncoding.DecodeString(b64); err == nil && len(k) == ed25519.PublicKeySize {
		r.network = k
	}
}

// sign returns the receipt message for rc and remembers rc to compare with
// the network's reply.
func (r *receipts) sign(rc protocol.Receipt) protocol.Message {
	b, _ := json.Marshal(rc)
	r.mu.Lock()
	if len(r.sent) > 10000 { // the network stopped answering; don't grow without bound
		clear(r.sent)
	}
	r.sent[rc.ID] = rc
	r.mu.Unlock()
	return protocol.Message{Type: "receipt", ID: rc.ID, Receipt: string(b),
		Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(r.key, b))}
}

// check verifies the network's receipt and compares it with ours. It returns
// "matched", "mismatch", "bad_signature" or "unknown".
func (r *receipts) check(m protocol.Message) string {
	r.mu.Lock()
	ours, ok := r.sent[m.ID]
	delete(r.sent, m.ID)
	netKey := r.network
	r.mu.Unlock()
	if !ok {
		return "unknown"
	}
	sig, err := base64.StdEncoding.DecodeString(m.Sig)
	if netKey == nil || err != nil || !ed25519.Verify(netKey, []byte(m.Receipt), sig) {
		return "bad_signature"
	}
	var theirs protocol.Receipt
	if json.Unmarshal([]byte(m.Receipt), &theirs) != nil || !ours.SameCounts(theirs) {
		return "mismatch"
	}
	return "matched"
}
