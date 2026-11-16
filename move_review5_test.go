package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A member who follows the most accounts there can be is told so when they follow someone on another server, even
// when the Follow could not be queued now (its host unavailable): the limit is read before the queue is asked, as it
// was before the follow and its Follow became one transaction (PRT 2's fifth review). Nothing is written.
func TestDBFollowAtTheLimitSaysSo(t *testing.T) {
	e := newMoveEnv(t)
	e.target(novaID, anaActor)
	e.must(200, e.ana, "PUT", "/api/me/aliases", map[string]any{"account": "@nova@social.example"}) // caches nova
	var novaUID int64
	var inbox string
	if err := e.a.db.QueryRow(`SELECT user_id, COALESCE(NULLIF(shared_inbox, ''), inbox) FROM ap_actors WHERE ap_id = $1`, novaID).Scan(&novaUID, &inbox); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(inbox)
	if err != nil {
		t.Fatal(err)
	}
	e.mustExec(`INSERT INTO ap_hosts (host, state, until, created_at) VALUES ($1, 'unavailable', now() + interval '1 hour', now())
		ON CONFLICT (host) DO UPDATE SET state = 'unavailable', until = EXCLUDED.until`, u.Hostname())
	kimID, kim := e.member("kim", false)
	e.mustExec(`INSERT INTO users (username, pw_hash) SELECT 'lim' || g, 'x' FROM generate_series(1, $1::int) g`, maxFollows)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id) SELECT $1, id FROM users WHERE username LIKE 'lim%'`, kimID)
	code, out := e.do(kim, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", novaUID), nil)
	if code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["error"]), "the most there can be") {
		t.Errorf("a follow at the limit, its host unavailable: %d %v", code, out)
	}
	if n := e.count(`SELECT count(*) FROM follows WHERE followee_id = $1`, novaUID); n != 0 {
		t.Errorf("%d follows written", n)
	}
}
