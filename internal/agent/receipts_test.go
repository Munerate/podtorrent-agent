package agent

import "testing"

func TestUsageTail(t *testing.T) {
	var u usageTail
	u.Write([]byte(`data: {"choices":[]}` + "\n\n"))
	u.Write([]byte(`data: {"usage":{"prompt_tokens":12,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":8}}}` + "\n\ndata: [DONE]\n\n"))
	p, c, cached := u.counts()
	if p == nil || *p != 12 || c == nil || *c != 3 || cached == nil || *cached != 8 {
		t.Fatalf("counts = %v %v %v", p, c, cached)
	}
	var none usageTail
	none.Write([]byte(`{"choices":[]}`))
	if p, c, _ := none.counts(); p != nil || c != nil {
		t.Fatal("expected no counts without usage")
	}
}
