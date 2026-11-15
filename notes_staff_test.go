package main

import (
	"context"
	"strings"
	"testing"
)

// What staff see of FWR 4 (the review): the moderation queue shows a reported post's picture with its description as
// written, and the Federation tab counts the activities that reached only the first 10,000 addresses.
func TestDBNotesForStaff(t *testing.T) {
	e := newNotesEnv(t)
	e.setFlag("moderation", true)
	_, mod := e.user("mo", "moderator")
	_, bob := e.user("bob", "user")
	p := e.postAs(e.ana, map[string]any{"body": "x", "image": jpegData(t, 8, 8), "alt": "Ana mid-throw", "audience": "local"})
	e.must(200, bob, "POST", "/api/reports", map[string]any{"kind": "post", "id": p, "reason": "spam"})
	items := e.must(200, mod, "GET", "/api/mod/queue", nil)["items"].([]any)
	var alt any
	for _, it := range items {
		m := it.(map[string]any)
		if num(m["id"]) == p {
			alt = m["target"].(map[string]any)["alt"]
		}
	}
	if alt != "Ana mid-throw" {
		t.Errorf("the queue's post: alt %v", alt)
	}

	// 10,001 followers at as many addresses: the Create goes to the first 10,000, and the tab says so.
	e.mustExec(`INSERT INTO users (id, username, pw_hash, domain) SELECT 5000000 + g, 'c' || g || '@c' || g || '.cut.example', '!', 'c' || g || '.cut.example' FROM generate_series(1, $1) g`, deliveryTargetsMax+1)
	e.mustExec(`INSERT INTO ap_actors (user_id, ap_id, kind, inbox, key_id, public_key, fetched_at, expires_at)
		SELECT id, 'https://' || domain || '/u', 'Person', 'https://' || domain || '/u/inbox', 'https://' || domain || '/u#k', 'pem', now(), now() + interval '1 day' FROM users WHERE id > 5000000`)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) SELECT id, $1, 'accepted' FROM users WHERE id > 5000000`, e.anaID)
	e.postAs(e.ana, map[string]any{"body": "to everyone", "audience": "public"})
	e.produce()
	if n := e.count(`SELECT count(*) FROM ap_deliveries`); n != deliveryTargetsMax {
		t.Errorf("%d deliveries, want the first %d", n, deliveryTargetsMax)
	}
	n, err := e.a.fed.out.numbers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n.Cut != 1 {
		t.Errorf("the tab's count of cut activities: %d", n.Cut)
	}
}

// The picture's description is counted as the composer counts it: the characters sent, trimmed, before inner spaces
// are joined for keeping (the second review of FWR 4).
func TestDBAltCountedAsSent(t *testing.T) {
	e := newEnv(t)
	_, ana := e.user("ana", "user")
	pic := jpegData(t, 8, 8)
	over := strings.Repeat("a", 1498) + "   b" // 1502 characters as typed, 1500 once the spaces are joined
	e.must(400, ana, "POST", "/api/posts", map[string]any{"body": "x", "image": pic, "alt": over})
	full := strings.Repeat("a", 1496) + "   b" // 1500 as typed
	out := e.must(200, ana, "POST", "/api/posts", map[string]any{"body": "x", "image": pic, "alt": " " + full + " "})
	if got := out["posts"].([]any)[0].(map[string]any)["alt"]; got != strings.Repeat("a", 1496)+" b" {
		t.Errorf("kept as %v", got)
	}
}
