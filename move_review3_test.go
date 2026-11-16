package main

// What the third review of the move found (PRT 2): a follower written "asked" whose Follow was never sent is tried
// again, and one whose Follow did go is not sent a second.

import (
	"context"
	"testing"
	"time"
)

// restart is the process started again after a stop: nothing in memory says a move waits, so only the start
// (movesAtStart) and the wake-up a step sets from the rows (moveWakeSQL) bring it back. A first step comes at once,
// within any lease a stopped step left; then the clock passes the lease and the steps go on by themselves.
func (e *moveEnv) restart() {
	e.t.Helper()
	e.a.fed.movesDue.Store(false)
	e.a.fed.movesAt.Store(0)
	if _, err := e.a.movesAtStart(); err != nil {
		e.t.Fatal(err)
	}
	e.produce()
	e.clock.add(moveLease + time.Minute)
	e.produce()
}

// moveFollows counts the Follows of member id in the queue.
func (e *moveEnv) moveFollows(id int64) int {
	e.t.Helper()
	return e.count(`SELECT count(*) FROM ap_outbox WHERE actor_id = $1 AND body LIKE '%"type":"Follow"%'`, id)
}

// Four stops: the workers' context ended in the Follow's transaction (a deploy: nothing of it is written, the row goes
// back and is tried again, so no follow is left without its Follow); a hard stop between "asked" and that transaction
// (a kill, a power cut: the row stays asked with its lease, and the start and the wake-up bring it back once the lease
// has passed, with no step woken by hand); a hard stop just after it (the lease was let go in it, so a follower who then
// unfollows by hand is not sent the Follow again); and a member who stops sharing while a row is left asked and held
// (the step that stops drops it, so no later wake-up takes it again).
func TestDBMoveOutAskedButNotSent(t *testing.T) {
	// The workers' context ends in the Follow's transaction.
	e := newMoveEnv(t)
	ids := e.followers("c", 1, true)
	e.target(novaID, anaActor)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	ctx, cancel := context.WithCancel(context.Background())
	e.out.enqueueHook = func(step, _ string) {
		if step == "lock" {
			e.out.enqueueHook = nil
			cancel()
		}
	}
	e.a.fed.movesDue.Store(true)
	e.a.movesOut(ctx)
	if n := e.followsTo(novaID); n != 0 || e.moveFollows(ids[0]) != 0 || e.progress() != "todo 1, asked 0, moved 0, kept 0 []" {
		t.Errorf("after the context ended: %d follows, %d Follows, %s", n, e.moveFollows(ids[0]), e.progress())
	}
	e.produce()
	e.clock.add(moveWait + time.Second)
	e.produce()
	if n := e.moveFollows(ids[0]); n != 1 || e.followsTo(novaID) != 1 || e.progress() != "todo 0, asked 1, moved 0, kept 0 []" {
		t.Errorf("after the context ended and the wait: %d Follows, %d follows, %s", n, e.followsTo(novaID), e.progress())
	}

	// A hard stop between "asked" and the Follow's transaction, the process started again within the lease.
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
	if n := g.moveFollows(gids[0]); n != 0 || g.progress() != "todo 0, asked 1, moved 0, kept 0 []" {
		t.Fatalf("after the hard stop: %d Follows, %s", n, g.progress())
	}
	g.restart()
	if n := g.moveFollows(gids[0]); n != 1 || g.count(`SELECT count(*) FROM move_followers WHERE claimed_until IS NOT NULL`) != 0 {
		t.Errorf("after the hard stop, the start and the lease: %d Follows (want 1), %d rows still held", n, g.count(`SELECT count(*) FROM move_followers WHERE claimed_until IS NOT NULL`))
	}

	// A hard stop just after the Follow's transaction, and the follower unfollows the new account by hand.
	h := newMoveEnv(t)
	hids := h.followers("s", 1, true)
	h.target(novaID, anaActor)
	h.must(200, h.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	h.a.fed.moveSentHook = func(int64) { h.a.fed.moveSentHook = nil; panic("a hard stop") }
	h.a.fed.movesDue.Store(true)
	func() {
		defer func() { recover() }()
		h.a.movesOut(context.Background())
	}()
	h.mustExec(`DELETE FROM follows f USING ap_actors a WHERE f.follower_id = $1 AND f.followee_id = a.user_id AND a.ap_id = $2`, hids[0], novaID)
	claimed := 0
	h.a.fed.movesHook = func(n int) { claimed += n }
	h.restart()
	if n := h.moveFollows(hids[0]); n != 1 || claimed != 0 {
		t.Errorf("a follower whose Follow went and who unfollowed was sent %d Follows (want 1) and claimed %d times (want 0)", n, claimed)
	}

	// A member who stops sharing while a row is left asked and held.
	k := newMoveEnv(t)
	k.followers("t", 1, true)
	k.target(novaID, anaActor)
	k.must(200, k.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	k.a.fed.moveSendHook = func(int64) { k.a.fed.moveSendHook = nil; panic("a hard stop") }
	k.a.fed.movesDue.Store(true)
	func() {
		defer func() { recover() }()
		k.a.movesOut(context.Background())
	}()
	k.must(200, k.ana, "PUT", "/api/me", map[string]any{"federates": false})
	k.restart()
	if n := k.count(`SELECT count(*) FROM move_followers WHERE user_id = $1`, k.anaID); n != 0 {
		t.Errorf("after the move stopped, %d rows of it are left (%s)", n, k.progress())
	}
}

// A suspension here of the new account after its followers here were asked keeps them, with that reason, in the
// suspension's own statement: no answer can come, and Settings counts them kept, not asked.
func TestDBMoveOutSuspendedAfterAsked(t *testing.T) {
	e := newMoveEnv(t)
	e.followers("v", 2, true)
	e.target(novaID, anaActor)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	e.produce()
	if p := e.progress(); p != "todo 0, asked 2, moved 0, kept 0 []" || e.followsTo(novaID) != 2 {
		t.Fatalf("before the suspension: %s, %d follows", p, e.followsTo(novaID))
	}
	var nova int64
	if err := e.a.db.QueryRow(`SELECT user_id FROM ap_actors WHERE ap_id = $1`, novaID).Scan(&nova); err != nil {
		t.Fatal(err)
	}
	_, mod := e.user("mo", "moderator")
	e.must(200, mod, "POST", "/api/mod/actions", map[string]any{"action": "suspend", "kind": "user", "id": nova, "reason": "spam",
		"until": time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)})
	if p := e.progress(); p != "todo 0, asked 0, moved 0, kept 2 [the moderators here suspended the new account]" || e.followsTo(novaID) != 0 {
		t.Errorf("after the suspension: %s, %d follows", p, e.followsTo(novaID))
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
