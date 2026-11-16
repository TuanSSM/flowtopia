package main

// What the review of the move found (PRT 2): the members here moved after the answer, in batches, resumable, with
// room left at the target's host and true reasons; a Follow that waits for room is sent, not refused; taking a
// redirect back gives up the Move where it was not sent and comes after it where it is on its way; the move's Update
// comes after the member's Update before it; a redirect stays visible and can be stopped with the flag off; a target
// that moved itself is refused; a moved member follows nobody new here either; the old follow is kept until the new
// one is accepted; the key not made yet has its own words; the move's allowance counts only moves that passed.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// progress is where the move of ana's followers here stands, as Settings has it.
func (e *moveEnv) progress() string {
	e.t.Helper()
	p, err := e.a.moveProgressOf(context.Background(), e.anaID)
	if err != nil {
		e.t.Fatal(err)
	}
	if p == nil {
		return "none"
	}
	return fmt.Sprintf("todo %d, asked %d, moved %d, kept %d %v", p.Todo, p.Asked, p.Moved, p.Kept, p.Reasons)
}

// novaKeyed serves nova with a key of its own (the inbox environment's second key) and still naming ana, so it can
// sign an Accept or a Reject.
func (e *moveEnv) novaKeyed() {
	e.t.Helper()
	e.w.put(novaID, worldDoc{body: actorDoc(novaID, "nova", "Nova", &e.other.PublicKey, map[string]any{"alsoKnownAs": []string{anaActor}})})
	e.clock.add(actorTTL + time.Minute) // the cached copy has no key: fetched again on its next use
}

func (e *moveEnv) nova() signer { return e.signer(e.other, novaID+"#main-key", sigCavage) }

// followers makes n members here who follow ana; sharing ones share their profile (and have a key).
func (e *moveEnv) followers(prefix string, n int, sharing bool) []int64 {
	e.t.Helper()
	var ids []int64
	for i := 0; i < n; i++ {
		var id int64
		if sharing {
			id, _ = e.member(fmt.Sprintf("%s%d", prefix, i), false)
		} else {
			id, _ = e.user(fmt.Sprintf("%s%d", prefix, i), "user")
		}
		e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, id, e.anaID)
		ids = append(ids, id)
	}
	return ids
}

// The request's work does not grow with the members here who follow the member: a move with 3 followers here and
// one with 30 send Postgres the same statements (within the few a cached setting adds); nothing of theirs is enqueued inside it, and the background step
// moves them all afterwards, in batches.
func TestDBMoveOutFollowersAfterAnswer(t *testing.T) {
	count := func(n int) (int64, *moveEnv) {
		e := newMoveEnv(t)
		e.followers("f", n, false)
		e.target(novaID, anaActor)
		e.produce()
		db, c := stmtCountingDB(t, e.tenv)
		was, wasFed := e.a.db, e.a.fed.db
		e.a.db, e.a.fed.db = db, db
		c.stmts.Store(0)
		e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
		e.a.db, e.a.fed.db = was, wasFed
		return c.stmts.Load(), e
	}
	few, _ := count(3)
	many, e := count(30)
	t.Logf("a move sent %d statements with 3 followers here and %d with 30", few, many)
	// A cached setting read again on its own clock (the flags, kept 2 s) can add a few statements to either run; work per
	// follower here would add 27 or more.
	if many-few > 5 || few-many > 5 {
		t.Errorf("the move's request sent %d statements with 3 followers here and %d with 30", few, many)
	}
	if n := e.count(`SELECT count(*) FROM move_followers WHERE user_id = $1 AND state = 'todo'`, e.anaID); n != 30 {
		t.Errorf("%d followers written down, want 30", n)
	}
	was := moveBatch
	moveBatch = 7
	defer func() { moveBatch = was }()
	var batches []int
	e.a.fed.movesHook = func(n int) { batches = append(batches, n) }
	e.produce()
	if fmt.Sprint(batches) != "[7 7 7 7 2]" || e.progress() != "todo 0, asked 0, moved 0, kept 30 [they do not share their profile beyond this server]" {
		t.Errorf("batches %v, progress %s", batches, e.progress())
	}
}

// A stop midway: a step claimed a batch and died holding it. After the start (movesAtStart) and the end of its lease
// the move goes on and finishes, each follower moved once.
func TestDBMoveOutResumes(t *testing.T) {
	e := newMoveEnv(t)
	ids := e.followers("s", 5, true)
	e.target(novaID, anaActor)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	// The step that died: it claimed two rows and wrote nothing.
	if _, err := e.a.db.Exec(moveClaimSQL, e.clock.now(), 2, e.clock.now().Add(moveLease)); err != nil {
		t.Fatal(err)
	}
	e.a.fed.movesDue.Store(false)
	e.a.fed.movesAt.Store(0)
	if moving, err := e.a.movesAtStart(); err != nil || !moving {
		t.Fatalf("the start does not see the followers still to move: %v %v", moving, err)
	}
	e.produce()
	if p := e.progress(); p != "todo 2, asked 3, moved 0, kept 0 []" {
		t.Errorf("before the lease ends: %s", p)
	}
	e.clock.add(moveLease + time.Second)
	e.a.fed.movesDue.Store(true)
	e.produce()
	if p := e.progress(); p != "todo 0, asked 5, moved 0, kept 0 []" {
		t.Errorf("after the lease: %s", p)
	}
	for _, id := range ids {
		if n := len(e.rows(id)); n != 1 {
			t.Errorf("member %d sent %d Follows, want 1", id, n)
		}
	}
}

// Two steps at once never move the same follower twice: the first holds its batch while the second claims, and each
// follower sends one Follow.
func TestDBMoveOutTwoSteps(t *testing.T) {
	e := newMoveEnv(t)
	ids := e.followers("t", 6, true)
	e.target(novaID, anaActor)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	was := moveBatch
	moveBatch = 3
	defer func() { moveBatch = was }()
	ctx := context.Background()
	second := make(chan struct{})
	var mu sync.Mutex
	first := true
	e.a.fed.movesHook = func(int) {
		mu.Lock()
		was := first
		first = false
		mu.Unlock()
		if !was {
			return
		}
		// The first step has claimed its batch: a second step runs whole meanwhile.
		e.a.fed.movesDue.Store(true)
		go func() { defer close(second); e.a.movesOut(ctx) }()
		<-second
	}
	e.a.fed.movesDue.Store(true)
	if _, err := e.a.movesOut(ctx); err != nil {
		t.Fatal(err)
	}
	e.a.fed.movesHook = nil
	e.produce()
	for _, id := range ids {
		if n := len(e.rows(id)); n != 1 {
			t.Errorf("member %d sent %d Follows, want 1", id, n)
		}
	}
	if p := e.progress(); p != "todo 0, asked 6, moved 0, kept 0 []" {
		t.Errorf("progress %s", p)
	}
}

// Taking the redirect back midway stops the move of the followers here: what was still to do is dropped, nothing
// more is sent for it.
func TestDBMoveOutStopsWhenTakenBack(t *testing.T) {
	e := newMoveEnv(t)
	ids := e.followers("u", 5, true)
	e.target(novaID, anaActor)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	was := moveBatch
	moveBatch = 2
	defer func() { moveBatch = was }()
	e.a.fed.movesDue.Store(true)
	if _, err := e.a.movesOut(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.must(200, e.ana, "DELETE", "/api/me/move", map[string]any{"password": movePW})
	if n := e.count(`SELECT count(*) FROM move_followers WHERE user_id = $1`, e.anaID); n != 0 {
		t.Errorf("taking the redirect back left %d rows of the move", n)
	}
	e.produce()
	sent := 0
	for _, id := range ids {
		sent += len(e.rows(id))
	}
	if sent != 2 || e.count(`SELECT count(*) FROM move_followers WHERE user_id = $1`, e.anaID) != 0 {
		t.Errorf("after taking it back: %d Follows sent, %d rows left", sent, e.count(`SELECT count(*) FROM move_followers WHERE user_id = $1`, e.anaID))
	}
	// A member who stops sharing midway stops it too.
	f := newMoveEnv(t)
	f.followers("v", 3, true)
	f.target(novaID, anaActor)
	f.must(200, f.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	f.must(200, f.ana, "PUT", "/api/me", map[string]any{"federates": false})
	f.produce()
	if n := f.count(`SELECT count(*) FROM move_followers WHERE user_id = $1`, f.anaID); n != 0 {
		t.Errorf("after ana stopped sharing: %d rows left", n)
	}
}

// Room at the target's host: while it has moveHostRoom rows queued, no batch is made; once it drains the move goes on.
// Every follower is counted, whatever their number.
func TestDBMoveOutHostRoom(t *testing.T) {
	e := newMoveEnv(t)
	e.followers("w", 2, true)
	e.target(novaID, anaActor)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	e.mustExec(`INSERT INTO ap_outbox (ap_id, body, created_at, fanned_out) VALUES ('https://here.example/activities/full', '{}', now(), true)`)
	e.mustExec(`INSERT INTO ap_deliveries (outbox_id, inbox, host, next_at, created_at) SELECT o.id, 'https://social.example/u/' || g || '/inbox', 'social.example', now() + interval '1 day', now()
		FROM ap_outbox o, generate_series(1, $1) g WHERE o.ap_id = 'https://here.example/activities/full'`, moveHostRoom)
	e.a.fed.movesDue.Store(true)
	if did, err := e.a.movesOut(context.Background()); err != nil || did {
		t.Errorf("a batch with the host full: %v %v", did, err)
	}
	if p := e.progress(); p != "todo 2, asked 0, moved 0, kept 0 []" {
		t.Errorf("with the host full: %s", p)
	}
	e.mustExec(`DELETE FROM ap_deliveries WHERE outbox_id = (SELECT id FROM ap_outbox WHERE ap_id = 'https://here.example/activities/full')`)
	e.clock.add(moveWait + time.Second)
	e.produce()
	if p := e.progress(); p != "todo 0, asked 2, moved 0, kept 0 []" {
		t.Errorf("once the host drained: %s", p)
	}
}

// A Follow that waits for room in the queue is sent, not refused: the follow stays requested (handleFedFollow, before
// the move: the same fault was there), and a member moved in a move is asked, not kept.
func TestDBFollowWhileQueueWaits(t *testing.T) {
	e := newMoveEnv(t)
	e.target(novaID, anaActor)
	e.must(200, e.ana, "PUT", "/api/me/aliases", map[string]any{"account": "@nova@social.example"}) // caches nova
	var novaUID int64
	if err := e.a.db.QueryRow(`SELECT user_id FROM ap_actors WHERE ap_id = $1`, novaID).Scan(&novaUID); err != nil {
		t.Fatal(err)
	}
	// One activity waits for its fan-out: everything enqueued after it waits too.
	e.mustExec(`INSERT INTO ap_outbox (ap_id, body, created_at, fanned_out) VALUES ('https://here.example/activities/waits', '{}', now(), false)`)
	_, kim := e.member("kim", false)
	out := e.must(200, kim, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", novaUID), nil)
	if out["view"].(map[string]any)["follow"] != "requested" || e.count(`SELECT count(*) FROM follows WHERE followee_id = $1`, novaUID) != 1 {
		t.Errorf("a Follow that waits: %v", out["_raw"])
	}
}

// The order at an inbox whose host is down and back. Before the move ana's profile changed, and its Update(Person)
// failed at h1: the move's Update comes after it there. Then the move, which h1 cannot take either; then the redirect
// is taken back while h1 is down: the Move, never tried, is given up, and the Update that takes it back comes after
// the move's Update. When h1 is back it gets the profile's Update, the move's Update and the take-back Update, in that
// order, and never the Move.
func TestDBMoveOutOrdersUpdates(t *testing.T) {
	e := newMoveEnv(t)
	down := true
	e.stub.answer = func(host, typ string, n int) int {
		if host == "h1.fan.example" && down {
			return 503
		}
		return 202
	}
	e.target(novaID, anaActor)
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"bio": "before the move"})
	e.produce()
	e.sendAll()
	before := e.rows(e.anaID)
	if len(before) != 1 || before[0].typ != "Update" {
		t.Fatalf("the profile's Update: %+v", before)
	}
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	rs := e.rows(e.anaID)
	upd, mv := rs[1], rs[2]
	if upd.after != before[0].id || mv.after != upd.id {
		t.Errorf("the move's Update comes after %q (want the profile's %q), the Move after %q", upd.after, before[0].id, mv.after)
	}
	e.clock.add(2 * time.Minute)
	e.sendAll()
	e.must(200, e.ana, "DELETE", "/api/me/move", map[string]any{"password": movePW})
	rs = e.rows(e.anaID)
	back := rs[len(rs)-1]
	if back.after != mv.id {
		t.Errorf("the take-back Update comes after %q, want the Move %q", back.after, mv.id)
	}
	if n := e.count(`SELECT count(*) FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.ap_id = $1 AND d.host = 'h1.fan.example' AND d.state = 'failed' AND d.last_error = 'taken back before it was sent'`, mv.id); n != 1 {
		t.Errorf("the Move to h1 is not given up: %d", n)
	}
	down = false
	for i := 0; i < 20 && e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'queued'`) > 0; i++ {
		e.clock.add(time.Hour)
		e.sendAll()
	}
	var got []string
	for _, g := range e.stub.taken() {
		if g.host == "h1.fan.example" && g.status == 202 {
			got = append(got, g.id)
		}
	}
	if !slices.Equal(got, []string{before[0].id, upd.id, back.id}) {
		t.Errorf("h1 took %v, want the profile's Update, the move's and the take-back's: %v", got, []string{before[0].id, upd.id, back.id})
	}
	if seq := e.stub.sequence("h2.fan.example"); seq != "Update:202 Update:202 Move:202 Update:202" {
		t.Errorf("h2, up throughout: %q", seq)
	}
}

// With the flag turned off after a move, the member still sees the redirect and can stop it, and the composer offers
// no Public audience; starting a move and the aliases are off.
func TestDBMoveOutFlagOffAfterMove(t *testing.T) {
	e := newMoveEnv(t)
	e.target(novaID, anaActor)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	e.setFlag("moves", false)
	me := e.must(200, e.ana, "GET", "/api/me", nil)
	fed := me["federation"].(map[string]any)
	mv, _ := fed["move"].(map[string]any)
	if mv == nil || mv["flag"] != false || mv["moved"] == nil || fed["audiences"] != nil {
		t.Errorf("the card with the flag off: %v", fed)
	}
	if code, _ := e.do(e.ana, "PUT", "/api/me/aliases", map[string]any{"account": "@nova@social.example"}); code != 404 {
		t.Errorf("an alias with the flag off: %d", code)
	}
	e.must(200, e.ana, "DELETE", "/api/me/move", map[string]any{"password": movePW})
	if e.movedTo() != "" {
		t.Error("the redirect was not stopped with the flag off")
	}
	if fed := e.must(200, e.ana, "GET", "/api/me", nil)["federation"].(map[string]any); fed["move"] != nil || fed["audiences"] == nil {
		t.Errorf("after stopping it with the flag off: %v", fed)
	}
}

// Small points of the review: a target that moved itself is refused (the A to B, B to A loop); a moved member follows
// nobody new here either; the old follow stays when the new account refuses; the key not made yet has its own words;
// answers that change nothing do not spend the move's allowance; with the community flag off members here are kept
// with that reason.
func TestDBMoveOutReviewPoints(t *testing.T) {
	e := newMoveEnv(t)
	// The loop: nova points back to ana.
	person(e.w, novaID, "nova", map[string]any{"alsoKnownAs": []string{anaActor}, "movedTo": anaActor})
	if code, out := e.move("@nova@social.example", movePW); code != 422 || out["reason"] != "targetMoved" || out["error"] != moveTargetMoved {
		t.Errorf("a target that moved itself: %d %v", code, out["_raw"])
	}
	// Six answers that change nothing (no alias), then a move that passes: the allowance of 5 counts only that one.
	e.target(novaID)
	for i := 0; i < 6; i++ {
		if code, _ := e.move("@nova@social.example", movePW); code != 422 {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	moID, _ := e.member("mo", false)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, moID, e.anaID)
	e.target(novaID, anaActor)
	if code, out := e.move("@nova@social.example", movePW); code != 200 {
		t.Fatalf("a move after six refused: %d %v", code, out["_raw"])
	}
	// A moved member follows nobody new here.
	e.user("bob", "user")
	if code, out := e.do(e.ana, "PUT", "/api/users/bob/follow", nil); code != 403 || out["error"] != moveFollowsNobody {
		t.Errorf("a moved member following someone here: %d %v", code, out["_raw"])
	}
	// nova refuses mo's Follow: mo keeps following ana, kept with the reason.
	e.produce()
	fs := e.rows(moID)
	if len(fs) != 1 {
		t.Fatalf("mo's Follow: %+v", fs)
	}
	e.novaKeyed()
	e.deliver(activity(novaID, "Reject", fs[0].id, nil), e.nova())
	if e.count(`SELECT count(*) FROM follows WHERE follower_id = $1 AND followee_id = $2`, moID, e.anaID) != 1 ||
		e.progress() != "todo 0, asked 0, moved 0, kept 1 [the new account refused the follow]" {
		t.Errorf("after nova's Reject: %s; %s", e.progress(), e.outcome())
	}

	// The key not made yet.
	g := newMoveEnv(t)
	g.target(novaID, anaActor)
	settleKeys(t, g.a.fed) // the key request sharing made is handled first, or it would make the key again
	g.mustExec(`DELETE FROM ap_keys WHERE user_id = $1`, g.anaID)
	if code, out := g.move("@nova@social.example", movePW); code != 400 || out["error"] != moveKeyNotYet {
		t.Errorf("sharing with no key yet: %d %v", code, out["_raw"])
	}

	// The community flag off: members here are kept with that reason, as following anyone elsewhere is off.
	h := newMoveEnv(t)
	h.followers("c", 1, true)
	h.target(novaID, anaActor)
	h.must(200, h.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	h.setFlag("community", false)
	h.produce()
	if p := h.progress(); p != "todo 0, asked 0, moved 0, kept 1 [following people on other servers is off on this server]" || !strings.HasPrefix(p, "todo 0") {
		t.Errorf("with the community flag off: %s", p)
	}
}
