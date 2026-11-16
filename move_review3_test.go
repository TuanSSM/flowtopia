package main

// What the third review of the move found (PRT 2): a follower written "asked" whose Follow was never sent is tried
// again, and one whose Follow did go is not sent a second.

import (
	"context"
	"testing"
	"time"
)

// Three stops: the workers' context ended between "asked" and the send (a deploy: what follows the send runs on a
// context of its own and puts the row back); a hard stop there (a kill, a power cut: the row stays asked with its
// lease, and the claim takes it again once the lease has passed, since no follow of the new account exists); and a stop
// after the Follow was made but before the lease was let go (the follow exists, so the row is left to the Accept).
func TestDBMoveOutAskedButNotSent(t *testing.T) {
	follows := func(e *moveEnv, id int64) int {
		return e.count(`SELECT count(*) FROM ap_outbox WHERE actor_id = $1 AND body LIKE '%"type":"Follow"%'`, id)
	}
	again := func(e *moveEnv) {
		e.clock.add(moveLease + time.Minute)
		e.a.fed.movesDue.Store(true)
		e.produce()
	}
	// The workers' context ends between "asked" and the send.
	e := newMoveEnv(t)
	ids := e.followers("c", 1, true)
	e.target(novaID, anaActor)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	ctx, cancel := context.WithCancel(context.Background())
	e.a.fed.moveSendHook = func(int64) { e.a.fed.moveSendHook = nil; cancel() }
	e.a.fed.movesDue.Store(true)
	e.a.movesOut(ctx)
	if p := e.progress(); p != "todo 1, asked 0, moved 0, kept 0 []" {
		t.Errorf("after the context ended: %s", p)
	}
	again(e)
	if n := follows(e, ids[0]); n != 1 || e.progress() != "todo 0, asked 1, moved 0, kept 0 []" {
		t.Errorf("after the context ended and again: %d Follows, %s", n, e.progress())
	}

	// A hard stop between "asked" and the send.
	g := newMoveEnv(t)
	gids := g.followers("p", 1, true)
	g.target(novaID, anaActor)
	g.must(200, g.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	g.a.fed.moveSendHook = func(int64) { g.a.fed.moveSendHook = nil; panic("a hard stop") }
	g.a.fed.movesDue.Store(true)
	func() {
		defer func() { recover() }()
		g.a.movesOut(context.Background())
	}()
	if n := follows(g, gids[0]); n != 0 || g.progress() != "todo 0, asked 1, moved 0, kept 0 []" {
		t.Fatalf("after the hard stop: %d Follows, %s", n, g.progress())
	}
	again(g)
	if n := follows(g, gids[0]); n != 1 {
		t.Errorf("after the hard stop and the lease: %d Follows, want 1", n)
	}
	// Its Follow was made: a later claim leaves it alone, even with the lease still on (the stop came before it was let go).
	g.mustExec(`UPDATE move_followers SET claimed_until = $2 WHERE follower_id = $1`, gids[0], g.clock.now())
	claimed := 0
	g.a.fed.movesHook = func(n int) { claimed += n }
	again(g)
	if n := follows(g, gids[0]); n != 1 || claimed != 0 {
		t.Errorf("a follower whose Follow went was sent %d (want 1) and claimed %d times (want 0)", n, claimed)
	}
}

// The count of batches that could not go is the highest of the batch's rows, not the first's: with one row at its last
// try and the host still full, the batch gives up.
func TestDBMoveOutTriesOfTheBatch(t *testing.T) {
	e := newMoveEnv(t)
	ids := e.followers("q", 2, true)
	e.target(novaID, anaActor)
	e.fill("social.example", moveHostRoom)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	e.mustExec(`UPDATE move_followers SET tries = $2 WHERE follower_id = $1`, ids[1], moveTriesMax-1)
	e.produce()
	if p := e.progress(); p != "todo 0, asked 0, moved 0, kept 2 [the new account's server had too much waiting from this server for too long]" {
		t.Errorf("a batch with a row at its last try: %s", p)
	}
}

// A batch that cannot go makes the member's rows wait, but not a row another step holds on a live lease.
func TestDBMoveOutWaitLeavesOthersLease(t *testing.T) {
	e := newMoveEnv(t)
	ids := e.followers("w", 2, true)
	e.target(novaID, anaActor)
	e.fill("social.example", moveHostRoom)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	held := e.clock.now().Add(time.Minute).Truncate(time.Microsecond)
	e.mustExec(`UPDATE move_followers SET claimed_until = $2 WHERE follower_id = $1`, ids[0], held)
	e.a.fed.movesDue.Store(true)
	if _, err := e.a.movesOut(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got time.Time
	if err := e.a.db.QueryRow(`SELECT claimed_until FROM move_followers WHERE follower_id = $1`, ids[0]).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(held) {
		t.Errorf("the row another step holds was moved from %v to %v", held, got)
	}
	var waits time.Time
	if err := e.a.db.QueryRow(`SELECT claimed_until FROM move_followers WHERE follower_id = $1`, ids[1]).Scan(&waits); err != nil || !waits.After(held) {
		t.Errorf("the batch's own row waits until %v (%v), want after %v", waits, err, held)
	}
}

// An account the moderators here suspended (FMD 2) can be neither moved to nor followed by the members moved with a
// member: the move is refused with words, and a suspension after the move keeps the followers, with that reason.
func TestDBMoveOutSuspendedHere(t *testing.T) {
	e := newMoveEnv(t)
	e.target(novaID)
	if code, _ := e.move("@nova@social.example", movePW); code != 422 { // caches nova: it names nobody yet
		t.Fatalf("the first try: %d", code)
	}
	e.target(novaID, anaActor)
	e.mustExec(`UPDATE users SET suspended_until = now() + interval '1 day' FROM ap_actors a WHERE a.user_id = users.id AND a.ap_id = $1`, novaID)
	if code, out := e.move("@nova@social.example", movePW); code != 403 || out["reason"] != "suspendedHere" || out["error"] != moveTargetSuspended {
		t.Errorf("a move to an account suspended here: %d %v", code, out["_raw"])
	}

	f := newMoveEnv(t)
	f.followers("z", 2, true)
	f.target(novaID, anaActor)
	f.must(200, f.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	f.mustExec(`UPDATE users SET suspended_until = now() + interval '1 day' FROM ap_actors a WHERE a.user_id = users.id AND a.ap_id = $1`, novaID)
	f.produce()
	if p := f.progress(); p != "todo 0, asked 0, moved 0, kept 2 [the moderators here suspended the new account]" || f.followsTo(novaID) != 0 {
		t.Errorf("followers of a member whose new account was suspended here: %s, %d follows", p, f.followsTo(novaID))
	}
}
