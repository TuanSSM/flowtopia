package main

import (
	"encoding/json"
	"slices"
	"testing"
)

// An account named only inside a link is no mention (the review of FWR 4): the content shows a link, so no Mention tag,
// no address in cc and no delivery; one named beside it is mentioned. One reading (bodyMentions) for both.
func TestDBMentionInsideLinkNotAddressed(t *testing.T) {
	e := newNotesEnv(t)
	f1, _ := e.remote("a", "one.example", true)
	e.follows(f1)
	_, menAP := e.remote("men", "mentioned.example", false)
	_, boAP := e.remote("bo", "social.example", false)
	e.postAs(e.ana, map[string]any{"body": "see https://mentioned.example/@men@mentioned.example for it, thanks @bo@social.example", "audience": "public"})
	e.produce()
	out := e.outRows()
	if len(out) != 1 {
		t.Fatalf("%d activities", len(out))
	}
	var c struct {
		Cc     []string
		Object struct{ Tag []map[string]string }
	}
	json.Unmarshal([]byte(out[0].body), &c)
	if slices.Contains(c.Cc, menAP) || len(c.Object.Tag) != 1 || c.Object.Tag[0]["href"] != boAP || !slices.Contains(c.Cc, boAP) {
		t.Errorf("cc %v, tags %v", c.Cc, c.Object.Tag)
	}
	if !slices.Equal(out[0].inboxes, []string{"https://one.example/inbox", boAP + "/inbox"}) {
		t.Errorf("delivered to %v", out[0].inboxes)
	}
}
