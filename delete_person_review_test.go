package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A key request the key maker took before a member left, and handles after, makes no key (A of the review of FWR 7):
// the key maker is held, by a hook, from the moment it takes ana's request until she has left; then it finds she no
// longer shares, makes nothing, and her second leaving is refused, so no second Delete(Person) goes.
func TestDBLeaveDropsWaitingKeyRequest(t *testing.T) {
	e := newNotesEnv(t)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	f := e.a.fed
	ana := keyOwner(e.anaID)
	settleKeys(t, f) // the request ana's sharing asked for is handled already: the one below is the only one
	entered, release := make(chan struct{}, 1), make(chan struct{})
	handled := make(chan error, 4)
	f.mu.Lock()
	f.beforeKey = func(o keyOwner) {
		if o != ana {
			return
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release // closed once: a second call goes straight on
	}
	f.handled = func(o keyOwner, _ bool, err error) {
		if o == ana {
			select {
			case handled <- err:
			default:
			}
		}
	}
	f.mu.Unlock()
	if !f.requestKey(ana) {
		t.Fatal("the request was not taken")
	}
	<-entered
	e.must(200, e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true})
	close(release)
	if err := <-handled; !errors.Is(err, errNotSharing) {
		t.Errorf("the key maker after ana left: %v, want errNotSharing", err)
	}
	if n := e.count(`SELECT count(*) FROM ap_keys WHERE user_id = $1`, e.anaID); n != 0 {
		t.Errorf("ana has %d keys after leaving", n)
	}
	if code, _ := e.do(e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true}); code != 400 {
		t.Errorf("leaving twice: %d", code)
	}
	if n := len(e.personDeletes()); n != 1 {
		t.Errorf("%d Delete(Person) activities, want 1", n)
	}
	// The other ways a key is asked for: ensureKey itself, and a delivery of the member's that has no key.
	if made, err := f.ensureKey(context.Background(), ana); made || !errors.Is(err, errNotSharing) {
		t.Errorf("ensureKey for a member who left: %v %v", made, err)
	}
}

// A delivery of a member who does not share and has no key is given up at its send, and no key is asked for (B).
func TestDBDeliverAsksNoKeyForNonSharer(t *testing.T) {
	e := newNotesEnv(t)
	bobID, _ := e.user("bob", "user")
	asked := make(chan keyOwner, 4)
	e.a.fed.mu.Lock()
	e.a.fed.beforeKey = func(o keyOwner) {
		select {
		case asked <- o:
		default:
		}
	}
	e.a.fed.mu.Unlock()
	var ob int64
	if err := e.a.db.QueryRow(`INSERT INTO ap_outbox (ap_id, actor_id, body, created_at, fanned_out) VALUES ('https://here.example/users/bob/activities/x', $1, '{}', $2, true) RETURNING id`, bobID, e.clock.now()).Scan(&ob); err != nil {
		t.Fatal(err)
	}
	e.mustExec(`INSERT INTO ap_deliveries (outbox_id, inbox, host, next_at, created_at) VALUES ($1, 'https://h1.fan.example/inbox', 'h1.fan.example', $2, $2)`, ob, e.clock.now())
	e.sendAll()
	if e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'failed' AND last_error = 'the member does not share their profile and has no key'`) != 1 {
		t.Error("the delivery of a member who does not share was not given up")
	}
	select {
	case o := <-asked:
		t.Errorf("a key was asked for %v", o)
	default:
	}
	if e.count(`SELECT count(*) FROM ap_keys WHERE user_id = $1`, bobID) != 0 {
		t.Error("a key was made for a member who does not share")
	}
}

// After "Remove me from other servers", nothing the member does enqueues an activity or makes a key (B): one case per
// way out, each checked on its own. After an admin's deletion the member has no session, so every one of them is 401.
func TestDBNothingGoesOutAfterLeaving(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		name := "after leaving"
		if deleted {
			name = "after the account's deletion"
		}
		t.Run(name, func(t *testing.T) { nothingGoesOut(t, deleted) })
	}
}

func nothingGoesOut(t *testing.T, deleted bool) {
	e := newNotesEnv(t)
	e.setFlag("community", true)
	ctx := context.Background()
	bo, boAP := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	var remotePost, liked, own, ownComment, remoteComment int64
	e.a.db.QueryRow(`INSERT INTO posts (user_id, body, audience, ap_id) VALUES ($1, 'remote', 'public', 'https://h1.fan.example/notes/1') RETURNING id`, bo).Scan(&remotePost)
	e.a.db.QueryRow(`INSERT INTO posts (user_id, body, audience, ap_id) VALUES ($1, 'remote liked', 'public', 'https://h1.fan.example/notes/2') RETURNING id`, bo).Scan(&liked)
	e.mustExec(`INSERT INTO likes (post_id, user_id, ap_id) VALUES ($1, $2, 'https://here.example/users/ana/activities/like1')`, liked, e.anaID)
	own = e.postAs(e.ana, map[string]any{"body": "mine, gone out", "audience": "public"})
	e.produce()
	e.a.db.QueryRow(`INSERT INTO comments (post_id, user_id, body) VALUES ($1, $2, 'my comment') RETURNING id`, own, e.anaID).Scan(&ownComment)
	e.mustExec(`INSERT INTO ap_comment_out (comment_id, post_id, user_id, state, targets) VALUES ($1, $2, $3, 'sent', '[]')`, ownComment, own, e.anaID)
	e.a.db.QueryRow(`INSERT INTO comments (post_id, user_id, body) VALUES ($1, $2, 'on theirs') RETURNING id`, remotePost, e.anaID).Scan(&remoteComment)
	e.mustExec(`INSERT INTO ap_comment_out (comment_id, post_id, user_id, state, targets) VALUES ($1, $2, $3, 'sent', '[]')`, remoteComment, remotePost, e.anaID)
	if deleted {
		e.must(200, e.rootSession(), "DELETE", fmt.Sprintf("/api/admin/users/%d", e.anaID), nil)
	} else {
		e.must(200, e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true})
		if e.count(`SELECT count(*) FROM likes WHERE post_id = $1 AND user_id = $2 AND ap_id IS NULL`, liked, e.anaID) != 1 {
			t.Error("ana's like of a remote post is not kept here as a like of this server")
		}
	}
	e.mustExec(`DELETE FROM ap_outbox`) // what the leaving or the deletion sent: what follows must add nothing
	var boActor *remoteActor
	if ra, err := e.a.fed.remoteActor(ctx, boAP); err == nil {
		boActor = ra
	}
	_ = boActor
	// State a stale page or another server could still leave behind: a follow of bo, bo's follow and request.
	stale := func() {
		if !deleted {
			e.mustExec(`INSERT INTO follows (follower_id, followee_id, state, ap_id) VALUES ($1, $2, 'accepted', 'https://here.example/users/ana/activities/f1') ON CONFLICT DO NOTHING`, e.anaID, bo)
			e.mustExec(`INSERT INTO follows (follower_id, followee_id, state, ap_id) VALUES ($1, $2, 'pending', 'https://h1.fan.example/follows/1') ON CONFLICT DO NOTHING`, bo, e.anaID)
		}
	}
	type step struct {
		name, method, path string
		body               map[string]any
	}
	for _, s := range []step{
		{"like", "POST", fmt.Sprintf("/api/posts/%d/like", remotePost), nil},
		{"unlike", "POST", fmt.Sprintf("/api/posts/%d/like", liked), nil},
		{"comment", "POST", fmt.Sprintf("/api/posts/%d/comments", remotePost), map[string]any{"body": "again"}},
		{"edit a comment", "PUT", fmt.Sprintf("/api/comments/%d", ownComment), map[string]any{"body": "edited"}},
		{"edit a comment on a remote post", "PUT", fmt.Sprintf("/api/comments/%d", remoteComment), map[string]any{"body": "edited"}},
		{"delete a comment", "DELETE", fmt.Sprintf("/api/comments/%d", ownComment), nil},
		{"post", "POST", "/api/posts", map[string]any{"body": "public again", "audience": "public"}},
		{"post here", "POST", "/api/posts", map[string]any{"body": "here only", "audience": "local"}},
		{"delete a post", "DELETE", fmt.Sprintf("/api/posts/%d", own), nil},
		{"follow", "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil},
		{"unfollow", "DELETE", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil},
		{"accept", "PUT", fmt.Sprintf("/api/fed/requests/%d", bo), nil},
		{"reject", "DELETE", fmt.Sprintf("/api/fed/requests/%d", bo), nil},
		{"block", "PUT", fmt.Sprintf("/api/fed/accounts/%d/block", bo), nil},
		{"profile change", "PUT", "/api/me", map[string]any{"bio": "new bio", "displayName": "Ana B"}},
	} {
		stale()
		code, out := e.do(e.ana, s.method, s.path, s.body)
		if deleted && code != 401 {
			t.Errorf("%s with the session of a deleted account: %d %v", s.name, code, out["_raw"])
		}
		e.produce()
		if n := e.count(`SELECT count(*) FROM ap_outbox`); n != 0 {
			t.Errorf("%s (%d): %d activities enqueued", s.name, code, n)
			e.mustExec(`DELETE FROM ap_outbox`)
		}
		if n := e.count(`SELECT count(*) FROM ap_keys WHERE user_id = $1`, e.anaID); n != 0 {
			t.Fatalf("%s: a key was made for ana", s.name)
		}
	}
	// -rotate-keys makes no key for a member without one.
	if _, _, err := e.a.fed.rotateKeys(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM ap_keys WHERE user_id = $1`, e.anaID); n != 0 {
		t.Error("-rotate-keys made a key for ana")
	}
}

// Leaving is all or nothing (D): federation refused between the handler's check and the transaction leaves everything
// as it was, with 503, so the member can leave once federation works again.
func TestDBLeaveAllOrNothing(t *testing.T) {
	e := newNotesEnv(t)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	e.a.fed.mu.Lock()
	e.a.fed.beforeGone = func(int64) { e.a.fed.refuse("federation refused: a test") }
	e.a.fed.mu.Unlock()
	if code, out := e.do(e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true}); code != 503 {
		t.Errorf("refused meanwhile: %d %v", code, out["_raw"])
	}
	for what, ok := range map[string]bool{
		"the key":        e.count(`SELECT count(*) FROM ap_keys WHERE user_id = $1`, e.anaID) == 1,
		"sharing":        e.count(`SELECT count(*) FROM users WHERE id = $1 AND federates`, e.anaID) == 1,
		"the name free":  e.count(`SELECT count(*) FROM retired_usernames WHERE username = 'ana'`) == 0,
		"the follow":     e.count(`SELECT count(*) FROM follows WHERE follower_id = $1`, bo) == 1,
		"nothing queued": e.count(`SELECT count(*) FROM ap_outbox`) == 0,
	} {
		if !ok {
			t.Errorf("a leaving refused meanwhile changed %s", what)
		}
	}
}

// Two leavings at once (G): the second, made while the first is between its checks and its transaction, sends the
// Delete(Person); the first then finds no key, changes nothing and says so, and never "N of them".
func TestDBTwoLeavingsAtOnce(t *testing.T) {
	e := newNotesEnv(t)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	var nested atomic.Bool // the hook runs again inside the second leaving, and then does nothing
	var second int
	var secondSaid string
	e.a.fed.mu.Lock()
	e.a.fed.beforeGone = func(int64) {
		if nested.CompareAndSwap(false, true) {
			code, out := e.do(e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true})
			second, secondSaid = code, fmt.Sprint(out["told"])
		}
	}
	e.a.fed.mu.Unlock()
	code, out := e.do(e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true})
	if second != 200 || !strings.Contains(secondSaid, "1 of them") {
		t.Errorf("the leaving made in between: %d %s", second, secondSaid)
	}
	if code != 400 || strings.Contains(fmt.Sprint(out["_raw"]), "of them") {
		t.Errorf("the leaving that found the key gone: %d %v", code, out["_raw"])
	}
	if n := len(e.personDeletes()); n != 1 {
		t.Errorf("%d Delete(Person) activities, want 1", n)
	}
}

// The browser's page of a member who left, and of a deleted account, says the profile is no longer shared, not that
// the account was deleted (G); servers still get the Tombstone.
func TestDBLeftPageWords(t *testing.T) {
	e := newNotesEnv(t)
	e.must(200, e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true})
	w := pubAsk(e.tenv, "GET", "/users/ana", "text/html")
	if w.Code != 410 || !strings.Contains(w.Body.String(), "Profile no longer shared") || strings.Contains(w.Body.String(), "Account deleted") {
		t.Errorf("the page of a member who left: %d %s", w.Code, w.Body)
	}
	if w := pubAsk(e.tenv, "GET", "/users/ana", apAccept); w.Code != 410 || !strings.Contains(w.Body.String(), `"type":"Tombstone"`) {
		t.Errorf("the actor of a member who left: %d %s", w.Code, w.Body)
	}
}

// The domain policy is applied before the cut at 2,000 servers (F): with the allowlist on and 2,005 servers known, of
// which the two allowed sort last, both are told.
func TestDBDeletePersonPolicyBeforeCut(t *testing.T) {
	e := newNotesEnv(t)
	e.mustExec(`WITH u AS (INSERT INTO users (username, pw_hash, domain, display_name)
			SELECT 'f@h' || lpad(g::text, 5, '0') || '.example', '!', 'h' || lpad(g::text, 5, '0') || '.example', 'f' FROM generate_series(1, $1) g RETURNING id, domain),
		a AS (INSERT INTO ap_actors (user_id, ap_id, kind, inbox, shared_inbox, fetched_at, expires_at)
			SELECT id, 'https://' || domain || '/users/f', 'Person', 'https://' || domain || '/users/f/inbox', 'https://' || domain || '/inbox', now(), now() + interval '1 day' FROM u)
		INSERT INTO follows (follower_id, followee_id, state) SELECT id, $2, 'accepted' FROM u`, personDeleteHostsMax+3, e.anaID)
	for _, h := range []string{"zz1.example", "zz2.example"} {
		id, _ := e.remote("z", h, true)
		e.follows(id)
		e.mustExec(`INSERT INTO domain_policies (domain, policy) VALUES ($1, 'allow')`, h)
	}
	e.mustExec(`UPDATE instance_settings SET value = '"allowlist"' WHERE key = 'federation_policy'`)
	if err := e.a.fed.loadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	out := e.must(200, e.rootSession(), "DELETE", fmt.Sprintf("/api/admin/users/%d", e.anaID), nil)
	pd := e.personDeletes()
	if len(pd) != 1 || strings.Join(pd[0].inboxes, " ") != "https://zz1.example/inbox https://zz2.example/inbox" {
		t.Errorf("addressed to %+v (%v)", pd, out["_raw"])
	}
	if s, _ := out["federation"].(string); strings.Contains(s, "left out") || !strings.Contains(s, "2 of them") {
		t.Errorf("the admin is told: %v", out["_raw"])
	}
}

// How long a member's 10,000 queued deliveries take to go with them, while the queue's lock is held (H): measured and
// logged, not held to a time (nothing here may depend on the machine's speed); the review's numbers are in the todo.
func TestDBDeletePersonLockTime(t *testing.T) {
	e := newNotesEnv(t)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	var ob int64
	if err := e.a.db.QueryRow(`INSERT INTO ap_outbox (ap_id, actor_id, body, created_at, fanned_out) VALUES ('https://here.example/users/ana/activities/many', $1, '{}', $2, true) RETURNING id`, e.anaID, e.clock.now()).Scan(&ob); err != nil {
		t.Fatal(err)
	}
	e.mustExec(`INSERT INTO ap_deliveries (outbox_id, inbox, host, next_at, created_at)
		SELECT $1, 'https://m' || g || '.example/inbox', 'm' || g || '.example', $2, $2 FROM generate_series(1, 10000) g`, ob, e.clock.now())
	var held time.Duration
	var began time.Time
	e.a.fed.out.enqueueHook = func(stage, _ string) {
		if stage == "lock" {
			began = time.Now()
		}
	}
	start := time.Now()
	e.must(200, e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true})
	if !began.IsZero() {
		held = time.Since(began)
	}
	t.Logf("leaving with 10,000 queued deliveries: %v in all, about %v from the queue's lock to the answer", time.Since(start), held)
	if n := e.count(`SELECT count(*) FROM ap_deliveries WHERE outbox_id = $1`, ob); n != 0 {
		t.Errorf("%d of the member's deliveries are left", n)
	}
}

// An unlike that would send an Undo asks first that the member shares, as a like does (B): a member who stopped sharing
// (and keeps their key) is refused with the reason, and nothing is enqueued.
func TestDBUnlikeNeedsSharing(t *testing.T) {
	e := newNotesEnv(t)
	e.setFlag("community", true)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	var pid int64
	if err := e.a.db.QueryRow(`INSERT INTO posts (user_id, body, audience, ap_id) VALUES ($1, 'remote', 'public', 'https://h1.fan.example/notes/1') RETURNING id`, bo).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	e.mustExec(`INSERT INTO likes (post_id, user_id, ap_id) VALUES ($1, $2, 'https://here.example/users/ana/activities/like1')`, pid, e.anaID)
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"federates": false})
	if code, out := e.do(e.ana, "POST", fmt.Sprintf("/api/posts/%d/like", pid), nil); code != 403 || !strings.Contains(fmt.Sprint(out["error"]), "share their profile") {
		t.Errorf("an unlike by a member who stopped sharing: %d %v", code, out["_raw"])
	}
	if n := e.count(`SELECT count(*) FROM ap_outbox`); n != 0 {
		t.Errorf("%d activities enqueued", n)
	}
}
