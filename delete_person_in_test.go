package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// In: an actor's own Delete, with its id as the object or a Person or Tombstone object, removes everything of that
// account here in one statement: its posts and their pictures, its follows both ways and its requests, its replies
// (a placeholder where a member answered) and likes, every notification it left, the deletions of its posts
// remembered, its name, picture and bio, and this server's deliveries waiting for its own inbox (not those for its
// server's shared inbox, nor the server's record); its key verifies nothing more. Kept: a member's block of it and
// the reports about it. A Delete naming another actor, one by another account, and one signed on another origin
// change nothing.
func TestDBDeletePersonIn(t *testing.T) {
	e := newNotesInEnv(t)
	e.send(e.bo(), boID, "Create", noteObj(1, "<p>hello</p>", nil))
	post := e.remotePostID(boID + "/statuses/1")
	file := e.picture(post, strings.Repeat("d", 32)+".jpg")
	pub := num(e.must(200, e.ana, "POST", "/api/posts", map[string]any{"body": "mine", "audience": "public"})["posts"].([]any)[0].(map[string]any)["id"])
	var lc, rid, answer int64
	e.a.db.QueryRow(`INSERT INTO comments (post_id, user_id, body) VALUES ($1, $2, 'a comment of ana') RETURNING id`, pub, e.anaID).Scan(&lc)
	e.a.db.QueryRow(`INSERT INTO comments (post_id, user_id, body, ap_id) VALUES ($1, $2, 'bo replies', $3) RETURNING id`, pub, e.boUser, boID+"/statuses/9").Scan(&rid)
	e.a.db.QueryRow(`INSERT INTO comments (post_id, user_id, body, parent_id) VALUES ($1, $2, 'ana answers', $3) RETURNING id`, pub, e.anaID, rid).Scan(&answer)
	e.mustExec(`INSERT INTO likes (post_id, user_id, ap_id) VALUES ($1, $2, $3)`, pub, e.boUser, boID+"/likes/1")
	e.mustExec(`INSERT INTO comment_likes (comment_id, user_id, ap_id) VALUES ($1, $2, $3)`, lc, e.boUser, boID+"/likes/2")
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted'), ($1, $3, 'pending')`, e.boUser, e.anaID, e.cyID)
	for _, k := range []string{"follow", "follow_request", "follow_rejected", "like", "comment", "reply", "share", "mention", "comment_like"} {
		e.mustExec(`INSERT INTO notifications (user_id, actor_id, kind, fed) VALUES ($1, $2, $3, true)`, e.anaID, e.boUser, k)
	}
	e.mustExec(`INSERT INTO ap_deleted (ap_id, user_id, at) VALUES ($1, $2, now())`, boID+"/statuses/77", e.boUser)
	e.mustExec(`INSERT INTO reports (reporter_id, target_kind, target_id, reason) VALUES ($1, 'post', $2, 'spam')`, e.anaID, post)
	e.mustExec(`INSERT INTO blocks (user_id, target_id) VALUES ($1, $2)`, e.cyID, e.boUser)
	var ob int64
	e.a.db.QueryRow(`INSERT INTO ap_outbox (ap_id, body, created_at, fanned_out) VALUES ('https://here.example/actor/activities/t1', '{}', now(), true) RETURNING id`).Scan(&ob)
	e.mustExec(`INSERT INTO ap_deliveries (outbox_id, inbox, host, next_at, created_at) VALUES ($1, 'https://social.example/users/bo/inbox', 'social.example', now(), now()),
		($1, 'https://social.example/inbox', 'social.example', now(), now())`, ob)
	e.mustExec(`INSERT INTO ap_hosts (host, created_at) VALUES ('social.example', now()) ON CONFLICT DO NOTHING`)
	state := func(id string) string {
		var s string
		e.a.db.QueryRow(`SELECT state FROM ap_actors WHERE ap_id = $1`, id).Scan(&s)
		return s
	}
	e.remoteID(catID)

	// Refused: a Delete of another actor by bo (not cat's own: cat stays), a Delete of bo by cat, and bo's own Delete
	// signed by an account of another server (the origin rule: refused at the inbox).
	e.send(e.bo(), boID, "Delete", catID)
	e.deliver(activity(catID, "Delete", boID, nil), e.cat())
	if why := e.deliver(activity(boID, "Delete", boID, nil), e.eve()); why == inAccepted {
		t.Error("a Delete of bo signed on another origin was taken")
	}
	if state(catID) != "ok" || state(boID) != "ok" || e.count(`SELECT count(*) FROM posts WHERE user_id = $1`, e.boUser) != 1 {
		t.Fatalf("a Delete not the actor's own changed something: cat %s, bo %s", state(catID), state(boID))
	}

	// bo's own Delete, with a Person object.
	e.send(e.bo(), boID, "Delete", map[string]any{"id": boID, "type": "Person"})
	if state(boID) != "gone" || e.outcome() != "done:" {
		t.Fatalf("bo is %q after its own Delete (%s)", state(boID), e.outcome())
	}
	for what, n := range map[string]int{
		"posts":                        e.count(`SELECT count(*) FROM posts WHERE user_id = $1`, e.boUser),
		"pictures' rows":               e.count(`SELECT count(*) FROM ap_media`),
		"follows and requests":         e.count(`SELECT count(*) FROM follows WHERE follower_id = $1 OR followee_id = $1`, e.boUser),
		"replies with text":            e.count(`SELECT count(*) FROM comments WHERE user_id = $1 AND body <> ''`, e.boUser),
		"likes":                        e.count(`SELECT count(*) FROM likes WHERE user_id = $1`, e.boUser) + e.count(`SELECT count(*) FROM comment_likes WHERE user_id = $1`, e.boUser),
		"notifications":                e.count(`SELECT count(*) FROM notifications WHERE actor_id = $1`, e.boUser),
		"remembered deletions":         e.count(`SELECT count(*) FROM ap_deleted WHERE user_id = $1`, e.boUser),
		"profile":                      e.count(`SELECT count(*) FROM users WHERE id = $1 AND (display_name <> '' OR bio <> '' OR avatar <> '')`, e.boUser),
		"deliveries to bo's own inbox": e.count(`SELECT count(*) FROM ap_deliveries WHERE inbox = 'https://social.example/users/bo/inbox' AND state = 'queued'`),
		"actor's summary and picture":  e.count(`SELECT count(*) FROM ap_actors WHERE user_id = $1 AND (summary <> '' OR icon <> '')`, e.boUser),
	} {
		if n != 0 {
			t.Errorf("left after bo's own Delete: %s (%d)", what, n)
		}
	}
	if !gone(file) {
		t.Error("the picture's file is still on disk")
	}
	for what, n := range map[string]int{
		"ana's answer":                         e.count(`SELECT count(*) FROM comments WHERE id = $1 AND body = 'ana answers'`, answer),
		"the placeholder it answers":           e.count(`SELECT count(*) FROM comments WHERE id = $1 AND deleted`, rid),
		"cyd's block":                          e.count(`SELECT count(*) FROM blocks WHERE target_id = $1`, e.boUser),
		"the report":                           e.count(`SELECT count(*) FROM reports WHERE target_id = $1`, post),
		"the delivery to the shared inbox":     e.count(`SELECT count(*) FROM ap_deliveries WHERE inbox = 'https://social.example/inbox' AND state = 'queued'`),
		"the server's record":                  e.count(`SELECT count(*) FROM ap_hosts WHERE host = 'social.example'`),
		"the account's row (shown as deleted)": e.count(`SELECT count(*) FROM users WHERE id = $1`, e.boUser),
	} {
		if n != 1 {
			t.Errorf("after bo's own Delete, %s: %d, want 1", what, n)
		}
	}
	// Its key verifies nothing more, and asking for it again fetches nothing: gone is for good.
	if w := e.post("/inbox", activity(boID, "Like", fmt.Sprintf("https://here.example/users/ana/posts/%d", pub), nil), e.bo(), nil); w.Code != 401 {
		t.Errorf("signed by a gone actor: %d", w.Code)
	}
	before := e.w.total()
	if ra, err := e.a.fed.remoteActor(context.Background(), boID); err != nil || ra.State != "gone" || e.w.total() != before {
		t.Errorf("bo asked for again: %v %v, %d fetches", ra, err, e.w.total()-before)
	}

	// cat's own Delete with a Tombstone object.
	e.send(e.cat(), catID, "Delete", map[string]any{"id": catID, "type": "Tombstone", "formerType": "Person"})
	if state(catID) != "gone" {
		t.Errorf("cat is %q after its own Delete with a Tombstone", state(catID))
	}
}

// A Delete from an actor never seen (Mastodon sends one for each deleted account to every server it knows) is the
// cheapest path through the inbox: 202, no fetch, no row, and one statement (actorKnown's indexed lookup), for the
// bare id and a Tombstone alike; the address's allowance is spent first, so past it the answer is 429 with no
// statement at all.
func TestDBUnknownDeleteCheap(t *testing.T) {
	e := newInboxEnv(t)
	db, c := stmtCountingDB(t, e.tenv)
	wasA, wasF := e.a.db, e.a.fed.db
	swap := func(on bool) {
		if on {
			e.a.db, e.a.fed.db = db, db
		} else {
			e.a.db, e.a.fed.db = wasA, wasF
		}
	}
	defer swap(false)
	ghost := "https://social.example/users/ghost"
	before := e.w.total()
	// The switches are read at most every 2 s for every request (flagsCached), not for this one: held as read, so the count
	// does not depend on how fast the test runs.
	e.a.flagsCached()
	e.a.flagMu.Lock()
	e.a.flagsAt = time.Now().Add(time.Hour)
	e.a.flagMu.Unlock()
	swap(true)
	for _, object := range []any{ghost, map[string]any{"id": ghost, "type": "Tombstone"}} {
		e.clock.add(time.Second)
		c.stmts.Store(0)
		w := e.post("/inbox", activity(ghost, "Delete", object, nil), e.signer(e.other, ghost+"#main-key", sigCavage), nil)
		if w.Code != 202 || e.last() != inUnknownDelete {
			t.Errorf("a Delete from an actor never seen: %d (%v)", w.Code, e.last())
		}
		if n := c.stmts.Load(); n != 1 {
			t.Errorf("a Delete from an actor never seen (%v) sent %d statements, want 1", object, n)
		}
	}
	swap(false)
	if e.w.total() != before || e.count(`SELECT count(*) FROM ap_inbox`) != 0 || e.count(`SELECT count(*) FROM ap_actors WHERE ap_id = $1`, ghost) != 0 {
		t.Error("a Delete from an actor never seen fetched or stored something")
	}
	swap(true)
	e.a.lim.inboxAddr = newLimiter(1, time.Hour)
	e.clock.add(time.Second)
	e.post("/inbox", activity(ghost, "Delete", ghost, nil), e.signer(e.other, ghost+"#main-key", sigCavage), nil)
	c.stmts.Store(0)
	e.clock.add(time.Second)
	if w := e.post("/inbox", activity(ghost, "Delete", ghost, nil), e.signer(e.other, ghost+"#main-key", sigCavage), nil); w.Code != 429 || c.stmts.Load() != 0 {
		t.Errorf("past the address's allowance: %d, %d statements", w.Code, c.stmts.Load())
	}
}

// "Remove me from other servers": confirmed, held to its allowance, refused in single mode for the owner and while
// federation is refused. It sends the member's Delete(Person) signed with the key other servers hold, turns sharing
// off, retires the name (the actor answers 410), removes the key and the follows with other servers, gives up what of
// the member still waited in the queue, and the page then says the member left. Sharing again makes a new key and
// the actor answers again.
func TestDBLeaveOtherServers(t *testing.T) {
	e := newNotesEnv(t)
	ctx := context.Background()
	bo, _ := e.remote("bo", "h1.fan.example", true)
	dee, _ := e.remote("dee", "h2.fan.example", false)
	eve, _ := e.remote("eve", "h3.fan.example", false)
	e.follows(bo)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'pending'), ($2, $3, 'accepted')`, dee, e.anaID, eve)
	e.mustExec(`INSERT INTO notifications (user_id, actor_id, kind, fed) VALUES ($1, $2, 'follow_request', true)`, e.anaID, dee)
	moID, _ := e.user("mo", "user")
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, moID, e.anaID)
	e.postAs(e.ana, map[string]any{"body": "on its way", "audience": "public"})
	e.produce()
	anaKey, anaKeyID, err := e.a.fed.privateKey(ctx, keyOwner(e.anaID))
	if err != nil {
		t.Fatal(err)
	}
	fed := func() map[string]any {
		return e.must(200, e.ana, "GET", "/api/me", nil)["federation"].(map[string]any)
	}
	if f := fed(); !strings.Contains(fmt.Sprint(f["leave"]), "may refuse you") || f["left"] != nil {
		t.Errorf("before: the page is told %v", f)
	}
	if code, out := e.do(e.ana, "POST", "/api/me/federation/leave", map[string]any{}); code != 400 || !strings.Contains(fmt.Sprint(out["error"]), "Confirm") {
		t.Errorf("not confirmed: %d %v", code, out["_raw"])
	}
	out := e.must(200, e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true})
	if s, _ := out["told"].(string); !strings.Contains(s, "3 of them") {
		t.Errorf("the member is told: %v", out["_raw"])
	}
	pd := e.personDeletes()
	want := []string{"https://h1.fan.example/inbox", "https://h2.fan.example/users/dee/inbox", "https://h3.fan.example/users/eve/inbox"}
	if len(pd) != 1 || pd[0].signer.Int64 != e.anaID || pd[0].keyID != anaKeyID || !slices.Equal(pd[0].inboxes, want) {
		t.Fatalf("the Delete(Person): %+v", pd)
	}
	for what, n := range map[string]int{
		"sharing on":                 e.count(`SELECT count(*) FROM users WHERE id = $1 AND federates`, e.anaID),
		"keys":                       e.count(`SELECT count(*) FROM ap_keys WHERE user_id = $1`, e.anaID),
		"follows with other servers": e.count(`SELECT count(*) FROM follows WHERE follower_id IN ($1, $2, $3) OR followee_id IN ($1, $2, $3)`, bo, dee, eve),
		"request notifications":      e.count(`SELECT count(*) FROM notifications WHERE kind = 'follow_request'`),
		"records of posts":           e.count(`SELECT count(*) FROM ap_post_out WHERE user_id = $1`, e.anaID),
		"her Create still queued":    e.count(`SELECT count(*) FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.actor_id = $1 AND d.state = 'queued'`, e.anaID),
	} {
		if n != 0 {
			t.Errorf("after leaving: %s (%d)", what, n)
		}
	}
	if e.count(`SELECT count(*) FROM follows WHERE follower_id = $1 AND followee_id = $2`, moID, e.anaID) != 1 || e.count(`SELECT count(*) FROM retired_usernames WHERE username = 'ana'`) != 1 {
		t.Error("a follow here went, or the name is not retired")
	}
	if w := pubAsk(e.tenv, "GET", "/users/ana", apAccept); w.Code != 410 {
		t.Errorf("the actor after leaving: %d", w.Code)
	}
	if f := fed(); f["leave"] != nil || f["left"] == nil || f["optedIn"] != false {
		t.Errorf("after: the page is told %v", f)
	}
	if code, _ := e.do(e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true}); code != 400 {
		t.Errorf("leaving twice: %d", code)
	}
	// The servers verify it with the key they hold.
	stub := &personStub{pub: &anaKey.PublicKey, keyID: e.a.fed.origin.url + anaKeyID, now: e.clock.now}
	e.out.fetch = deliveryStub(t, stub)
	e.out.fetch.origin = e.a.fed.origin
	e.out.pace.wait = func(ctx context.Context, d time.Duration) error { e.clock.add(d); return ctx.Err() }
	e.sendAll()
	if got := stub.taken(); len(got) != 3 || got[0].sig != "" || got[1].sig != "" || got[2].sig != "" {
		t.Errorf("delivered: %+v", got)
	}

	// Sharing again: a new key, and the actor answers again.
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"federates": true})
	if _, err := e.a.fed.ensureKey(ctx, keyOwner(e.anaID)); err != nil {
		t.Fatal(err)
	}
	newKey, _, err := e.a.fed.privateKey(ctx, keyOwner(e.anaID))
	if err != nil || newKey.Equal(anaKey) {
		t.Errorf("sharing again: %v, or the same key", err)
	}
	if w := pubAsk(e.tenv, "GET", "/users/ana", apAccept); w.Code != 200 {
		t.Errorf("the actor after sharing again: %d", w.Code)
	}
	if f := fed(); f["left"] == nil || f["leave"] == nil {
		t.Errorf("sharing again: the page is told %v", f)
	}

	// The allowance, single mode's owner, and federation refused.
	e.a.lim.fedLeave = newLimiter(0, time.Hour)
	if code, _ := e.do(e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true}); code != 429 {
		t.Errorf("past the allowance: %d", code)
	}
	e.a.lim.fedLeave = newLimiter(3, time.Hour)
	e.mustExec(`UPDATE users SET owner = (id = $1), role = CASE WHEN id = $1 THEN 'admin' ELSE role END`, e.anaID)
	e.a.mode = modeSingle
	if code, out := e.do(e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true}); code != 400 || !strings.Contains(fmt.Sprint(out["error"]), "FLOWA_FEDERATE") {
		t.Errorf("single mode's owner: %d %v", code, out["_raw"])
	}
	e.a.mode = modeFederated
	e.a.fed.refuse("federation refused: a test")
	if code, _ := e.do(e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true}); code != 503 {
		t.Errorf("federation refused: %d", code)
	}
}
