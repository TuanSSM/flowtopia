package main

// What the second review of the move found (PRT 2): a member whose new account's server cannot take the Follows waits
// with a back-off and is passed over, then kept with the reason, while other members' moves go on; this server's own
// trouble (the switches unread, federation refused just now) never keeps anyone; a batch stops at once when the
// redirect is taken back or its rows are no longer its own; a follower is "asked" before their Follow is sent; a Follow
// that waited and was written for no inbox leaves no follow behind; taking a redirect back takes the queue's lock first.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const vegaID = "https://other.example/users/vega"

// bea is a second member who shares, with her password and her own followers here.
func (e *moveEnv) bea() (int64, string) {
	e.t.Helper()
	id, tok := e.member("bea", false)
	e.mustExec(`UPDATE users SET pw_hash = $2 WHERE id = $1`, id, hashPassword(movePW))
	return id, tok
}

// fill puts n rows in the queue for host, due tomorrow, so the host's room is taken.
func (e *moveEnv) fill(host string, n int) {
	e.t.Helper()
	e.mustExec(`INSERT INTO ap_outbox (ap_id, body, created_at, fanned_out) VALUES ('https://here.example/activities/fill-' || $1, '{}', now(), true)`, host)
	e.mustExec(`INSERT INTO ap_deliveries (outbox_id, inbox, host, next_at, created_at) SELECT o.id, 'https://' || $1 || '/u/' || g || '/inbox', $1, now() + interval '1 day', now()
		FROM ap_outbox o, generate_series(1, $2) g WHERE o.ap_id = 'https://here.example/activities/fill-' || $1`, host, n)
}

// followsTo counts the follows, pending or accepted, of the account target.
func (e *moveEnv) followsTo(target string) int {
	e.t.Helper()
	return e.count(`SELECT count(*) FROM follows f JOIN ap_actors a ON a.user_id = f.followee_id WHERE a.ap_id = $1`, target)
}

// One member's move to a server that cannot take it does not hold anyone else's: ana's new account's server stays full,
// so her followers wait (passed over, with a back-off, and no follow written and removed again), while bea's followers,
// moving to a healthy server, are asked at once, and bea's move leaves ana's wait as it was. After moveTriesMax batches
// that could not go, ana's followers are kept with the reason.
func TestDBMoveOutStuckMemberWaits(t *testing.T) {
	e := newMoveEnv(t)
	e.followers("a", 3, true)
	e.target(novaID, anaActor)
	e.fill("social.example", moveHostRoom)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	e.produce()
	var until time.Time
	if err := e.a.db.QueryRow(`SELECT min(claimed_until) FROM move_followers WHERE user_id = $1 AND state = 'todo'`, e.anaID).Scan(&until); err != nil {
		t.Fatal(err)
	}
	if p := e.progress(); p != "todo 3, asked 0, moved 0, kept 0 []" || !until.After(e.clock.now()) || e.followsTo(novaID) != 0 {
		t.Fatalf("ana's followers with the host full: %s, wait until %v, %d follows", p, until, e.followsTo(novaID))
	}
	// bea moves to vega on other.example: her followers go now, and ana's wait is untouched.
	beaID, bea := e.bea()
	for i := 0; i < 2; i++ {
		id, _ := e.member(fmt.Sprintf("b%d", i), false)
		e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, id, beaID)
	}
	person(e.w, vegaID, "vega", map[string]any{"alsoKnownAs": []string{"https://here.example/users/bea"}})
	e.must(200, bea, "POST", "/api/me/move", map[string]any{"target": "@vega@other.example", "password": movePW})
	e.produce()
	var after time.Time
	if err := e.a.db.QueryRow(`SELECT min(claimed_until) FROM move_followers WHERE user_id = $1 AND state = 'todo'`, e.anaID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM move_followers WHERE user_id = $1 AND state = 'asked'`, beaID); n != 2 || !after.Equal(until) {
		t.Errorf("bea's followers asked: %d of 2; ana's wait %v, was %v", n, after, until)
	}
	// The host stays full: batch after batch waits, longer each time, and then ana's followers are kept with the reason,
	// never having written a follow.
	for i := 0; i < 3*moveTriesMax && e.count(`SELECT count(*) FROM move_followers WHERE user_id = $1 AND state = 'todo'`, e.anaID) > 0; i++ {
		e.clock.add(time.Hour + time.Minute)
		e.a.fed.movesDue.Store(true)
		e.produce()
		if e.followsTo(novaID) != 0 {
			t.Fatal("a follow of nova was written while its server was full")
		}
	}
	if p := e.progress(); p != "todo 0, asked 0, moved 0, kept 3 [the new account's server had too much waiting from this server for too long]" {
		t.Errorf("after the tries: %s", p)
	}
	if moveBackOff(0) != moveWait || moveBackOff(3) != 8*moveWait || moveBackOff(20) != time.Hour {
		t.Errorf("the back-off: %v %v %v", moveBackOff(0), moveBackOff(3), moveBackOff(20))
	}
}

// This server's own trouble keeps nobody: with the switches unreadable, or federation refused just now, the batch waits
// and the followers are moved once it passes.
func TestDBMoveOutOwnTroubleKeepsNobody(t *testing.T) {
	e := newMoveEnv(t)
	e.followers("o", 2, true)
	e.target(novaID, anaActor)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	// The switches cannot be read.
	e.mustExec(`ALTER TABLE flags RENAME TO flags_away`)
	e.a.flagMu.Lock()
	e.a.flagsAt = time.Time{}
	e.a.flagMu.Unlock()
	e.produce()
	e.mustExec(`ALTER TABLE flags_away RENAME TO flags`)
	if p := e.progress(); p != "todo 2, asked 0, moved 0, kept 0 []" {
		t.Errorf("with the switches unread: %s", p)
	}
	// Federation refused for a moment.
	e.a.fed.mu.Lock()
	e.a.fed.refused = "a test's refusal"
	e.a.fed.mu.Unlock()
	e.clock.add(moveBackOff(0) + time.Second)
	e.a.fed.movesDue.Store(true)
	e.produce()
	e.a.fed.mu.Lock()
	e.a.fed.refused = ""
	e.a.fed.mu.Unlock()
	if p := e.progress(); p != "todo 2, asked 0, moved 0, kept 0 []" {
		t.Errorf("with federation refused: %s", p)
	}
	e.clock.add(moveBackOff(0) + time.Second)
	e.a.fed.movesDue.Store(true)
	e.produce()
	if p := e.progress(); p != "todo 0, asked 2, moved 0, kept 0 []" {
		t.Errorf("once it passed: %s", p)
	}
}

// A batch stops at once when the redirect is taken back between two followers: the rest get no Follow, and a follower
// who already followed nova keeps following ana. And when another step took the batch's rows (its lease ran out), it
// stops too.
func TestDBMoveOutBatchStops(t *testing.T) {
	e := newMoveEnv(t)
	ids := e.followers("s", 4, true)
	e.target(novaID, anaActor)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	var novaUID int64
	if err := e.a.db.QueryRow(`SELECT user_id FROM ap_actors WHERE ap_id = $1`, novaID).Scan(&novaUID); err != nil {
		t.Fatal(err)
	}
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, ids[3], novaUID)
	e.a.fed.moveRowHook = func(follower int64) {
		if follower == ids[1] {
			e.a.fed.moveRowHook = nil
			e.must(200, e.ana, "DELETE", "/api/me/move", map[string]any{"password": movePW})
		}
	}
	e.produce()
	if n := len(e.rows(ids[0])); n != 1 {
		t.Errorf("the follower before the take-back sent %d Follows, want 1", n)
	}
	for _, id := range ids[1:] {
		if n := len(e.rows(id)); n != 0 {
			t.Errorf("member %d sent %d Follows after the take-back", id, n)
		}
	}
	if e.count(`SELECT count(*) FROM follows WHERE follower_id = $1 AND followee_id = $2`, ids[3], e.anaID) != 1 {
		t.Error("a follower who already followed nova lost their follow of ana after the take-back")
	}

	// Another step took the rows: the first stops at the next follower.
	f := newMoveEnv(t)
	fids := f.followers("l", 3, true)
	f.target(novaID, anaActor)
	f.must(200, f.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	f.a.fed.moveRowHook = func(follower int64) {
		if follower == fids[1] {
			f.a.fed.moveRowHook = nil
			f.mustExec(`UPDATE move_followers SET claimed_until = claimed_until + interval '1 hour' WHERE user_id = $1 AND state = 'todo'`, f.anaID)
		}
	}
	f.a.fed.movesDue.Store(true)
	if _, err := f.a.movesOut(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(f.rows(fids[1])) + len(f.rows(fids[2])); n != 0 {
		t.Errorf("%d Follows sent for rows another step holds", n)
	}

	// The redirect changed with the rows left in place (a write from outside the take-back, an older build after a
	// rollback): each row's write asks it again, and the batch stops.
	g := newMoveEnv(t)
	gids := g.followers("r", 3, true)
	g.target(novaID, anaActor)
	g.must(200, g.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	g.a.fed.moveRowHook = func(follower int64) {
		if follower == gids[1] {
			g.a.fed.moveRowHook = nil
			g.mustExec(`UPDATE users SET moved_to = '' WHERE id = $1`, g.anaID)
		}
	}
	g.a.fed.movesDue.Store(true)
	if _, err := g.a.movesOut(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(g.rows(gids[1])) + len(g.rows(gids[2])); n != 0 {
		t.Errorf("%d Follows sent after the redirect changed", n)
	}
}

// A follower is "asked" before their Follow is enqueued, so an Accept that comes at once finds them asked.
func TestDBMoveOutAskedBeforeFollow(t *testing.T) {
	e := newMoveEnv(t)
	ids := e.followers("k", 1, true)
	e.target(novaID, anaActor)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	var state string
	e.out.enqueueHook = func(step, apID string) {
		if step == "lock" && strings.Contains(apID, "/users/k0/") {
			e.a.db.QueryRow(`SELECT state FROM move_followers WHERE follower_id = $1`, ids[0]).Scan(&state)
		}
	}
	e.produce()
	e.out.enqueueHook = nil
	if state != "asked" {
		t.Errorf("when the Follow was enqueued the follower was %q, want asked", state)
	}
}

// A Follow that waited for room in the queue and was then written for no inbox (its host unavailable by then) leaves no
// pending follow, and the member moved with it is kept with that reason.
func TestDBMoveOutDroppedFollow(t *testing.T) {
	e := newMoveEnv(t)
	ids := e.followers("d", 1, true)
	e.target(novaID, anaActor)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	e.mustExec(`INSERT INTO ap_outbox (ap_id, body, created_at, fanned_out) VALUES ('https://here.example/activities/waits', '{}', now(), false)`)
	e.produce()
	if p := e.progress(); p != "todo 0, asked 1, moved 0, kept 0 []" || e.followsTo(novaID) != 1 {
		t.Fatalf("the Follow that waits: %s", p)
	}
	e.mustExec(`INSERT INTO ap_hosts (host, created_at, state) VALUES ('social.example', now(), 'unavailable') ON CONFLICT (host) DO UPDATE SET state = 'unavailable'`)
	e.sendAll()
	if p := e.progress(); p != "todo 0, asked 0, moved 0, kept 1 [the new account's server could not be reached]" || e.followsTo(novaID) != 0 {
		t.Errorf("after the fan-out to no inbox: %s, %d follows of nova", p, e.followsTo(novaID))
	}
	if e.count(`SELECT count(*) FROM follows WHERE follower_id = $1 AND followee_id = $2`, ids[0], e.anaID) != 1 {
		t.Error("the follower lost their follow of ana")
	}
}

// Taking a redirect back takes the queue's lock before the member's row, as every other path takes them. What it guards:
// only the order of the two statements inside handleMoveBack's transaction (the lock written before the update of the
// member's row), read from main.go. It does not prove that no deadlock can happen; other paths that take both are held
// by the queue's own rule (enqueueOrdered takes the lock first, and its then runs after) and by TestDBDeliveryLockOrder.
func TestMoveBackLockOrder(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "func (a *app) handleMoveBack(")
	if i < 0 {
		t.Fatal("handleMoveBack not found")
	}
	body := s[i:]
	body = body[:strings.Index(body, "\n}\n")]
	lock, row := strings.Index(body, "pg_advisory_xact_lock"), strings.Index(body, "UPDATE users SET moved_to = ''")
	if lock < 0 || row < 0 || lock > row {
		t.Errorf("handleMoveBack takes the member's row before the queue's lock (lock at %d, row at %d)", lock, row)
	}
}

// A new account's server that is unavailable (or whose circuit is open) gets no Follow at all: the batch waits, no
// follow is written and removed again, and once the server is back the followers are asked.
func TestDBMoveOutHostDown(t *testing.T) {
	e := newMoveEnv(t)
	e.followers("h", 2, true)
	e.target(novaID, anaActor)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	e.mustExec(`INSERT INTO ap_hosts (host, created_at, state) VALUES ('social.example', now(), 'unavailable') ON CONFLICT (host) DO UPDATE SET state = 'unavailable'`)
	e.produce()
	if p := e.progress(); p != "todo 2, asked 0, moved 0, kept 0 []" || e.followsTo(novaID) != 0 || e.count(`SELECT count(*) FROM ap_outbox WHERE body LIKE '%"type":"Follow"%'`) != 0 {
		t.Errorf("with the host unavailable: %s, %d follows", p, e.followsTo(novaID))
	}
	e.mustExec(`UPDATE ap_hosts SET state = 'ok' WHERE host = 'social.example'`)
	e.clock.add(moveBackOff(1) + time.Second)
	e.a.fed.movesDue.Store(true)
	e.produce()
	if p := e.progress(); p != "todo 0, asked 2, moved 0, kept 0 []" {
		t.Errorf("once the host is back: %s", p)
	}
}
