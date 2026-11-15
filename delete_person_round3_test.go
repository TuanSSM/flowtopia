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

// failPost makes the producer fail post p's record n times (the hook counts them down), then lets it through.
func (e *notesEnv) failPost(p int64, n *atomic.Int64, err error) {
	e.a.fed.outHook = func(k string, id int64) error {
		if k == "post" && id == p && n.Add(-1) >= 0 {
			return err
		}
		return nil
	}
	e.t.Cleanup(func() { e.a.fed.outHook = nil })
}

// throughSchedule runs the producer and the workers once per wait of the delivery schedule, the test's clock moved past
// each wait.
func (e *notesEnv) throughSchedule(waits int) {
	for i := 0; i < waits; i++ {
		e.produced()
		e.clock.add(deliveryRetries[min(i, len(deliveryRetries)-1)])
		e.a.fed.postsDue.Store(true)
	}
	e.produced()
}

func (e *notesEnv) postRecord(p int64) (state string, tries int, ok bool) {
	err := e.a.db.QueryRow(`SELECT state, tries FROM ap_post_out WHERE post_id = $1`, p).Scan(&state, &tries)
	return state, tries, err == nil
}

// A post's Delete starts its tries afresh (S1 of the third review): its Create failed through the whole schedule but
// for its last wait and then went out, the member deletes the post, and one failure of the Delete puts it back on the
// schedule instead of giving it up on the Create's count. And a Delete that fails every time gets its own full schedule,
// then is given up and counted as not taken back (S4), not as not sent.
func TestDBDeleteTriesAfresh(t *testing.T) {
	e := newNotesEnv(t)
	e.setFlag("community", true)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	stub := &personStub{now: e.clock.now}
	e.out.fetch = deliveryStub(t, stub)
	e.out.fetch.origin = e.a.fed.origin
	p := e.postAs(e.ana, map[string]any{"body": "flaky", "audience": "public"})
	var fails atomic.Int64
	fails.Store(int64(len(deliveryRetries)))
	e.failPost(p, &fails, errors.New("transient"))
	e.throughSchedule(len(deliveryRetries) + 1)
	if state, tries, _ := e.postRecord(p); !e.createOf("ana", p) || state != "sent" || tries != 0 {
		t.Fatalf("after the Create went at its last try: created %v, record %s with %d tries", e.createOf("ana", p), state, tries)
	}
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", p), nil)
	if state, tries, _ := e.postRecord(p); state != "delete" || tries != 0 {
		t.Errorf("the deletion's mark: %s with %d tries", state, tries)
	}
	fails.Store(1)
	e.a.fed.postsDue.Store(true)
	e.produced()
	if state, tries, ok := e.postRecord(p); !ok || state != "delete" || tries != 1 {
		t.Errorf("one failure of the Delete: record %v %s with %d tries, want it put back", ok, state, tries)
	}
	e.throughSchedule(2)
	if n := e.count(`SELECT count(*) FROM ap_outbox WHERE ap_id = $1`, fmt.Sprintf("https://here.example/users/ana/posts/%d#delete", p)); n != 1 {
		t.Error("the Delete did not go after its one failure")
	}

	// A post deleted while its Create still waits on the schedule: the deletion's mark starts the tries afresh.
	w := e.postAs(e.ana, map[string]any{"body": "waits", "audience": "public"})
	var some atomic.Int64
	some.Store(3)
	e.failPost(w, &some, errors.New("transient"))
	e.throughSchedule(2) // three attempts, each failing
	if state, tries, _ := e.postRecord(w); state != "create" || tries != 3 {
		t.Fatalf("the setup: the Create waits with %d tries (%s)", tries, state)
	}
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", w), nil)
	if state, tries, _ := e.postRecord(w); state != "delete" || tries != 0 {
		t.Errorf("a deletion of a post whose Create waits: %s with %d tries, want 0", state, tries)
	}
	// A profile change starts its update's tries afresh (they are kept in memory, personTries).
	e.a.fed.personTries.Store(e.anaID, len(deliveryRetries))
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"bio": "a change"})
	if _, ok := e.a.fed.personTries.Load(e.anaID); ok {
		t.Error("a new change of the profile keeps the tries of the update before")
	}

	// A Delete that fails every time: its whole schedule, then given up, counted in notTakenBack.
	q := e.postAs(e.ana, map[string]any{"body": "to delete", "audience": "public"})
	e.produced()
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", q), nil)
	var always atomic.Int64
	always.Store(1 << 30)
	e.failPost(q, &always, errors.New("always"))
	notTaken, notMade := e.a.fed.notTakenBack.Load(), e.a.fed.outNotMade.Load()
	e.a.fed.postsDue.Store(true)
	e.produced() // its first failure
	for i := 0; i < len(deliveryRetries); i++ {
		if _, tries, ok := e.postRecord(q); !ok || tries != i+1 {
			t.Fatalf("the Delete after %d failures: kept %v with %d tries", i+1, ok, tries)
		}
		e.clock.add(deliveryRetries[i])
		e.a.fed.postsDue.Store(true)
		e.produced()
	}
	if _, _, ok := e.postRecord(q); ok {
		t.Error("the Delete is not given up after its schedule")
	}
	if e.a.fed.notTakenBack.Load() != notTaken+1 || e.a.fed.outNotMade.Load() != notMade {
		t.Errorf("a Delete given up: notTakenBack +%d, outNotMade +%d; want 1 and 0", e.a.fed.notTakenBack.Load()-notTaken, e.a.fed.outNotMade.Load()-notMade)
	}
}

// A record whose own write fails reports no work, so the worker's loop waits and logs once a minute instead of going
// round at once (S2): with the producer failing a post and the record's table refusing every write (a trigger in the
// test's own schema, as a full disk would), 20 steps report no work and the producer logs nothing.
func TestDBFailingWriteDoesNotSpin(t *testing.T) {
	e := newNotesEnv(t)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	p := e.postAs(e.ana, map[string]any{"body": "x", "audience": "public"})
	var fails atomic.Int64
	fails.Store(1 << 30)
	e.failPost(p, &fails, errors.New("enqueue write failed"))
	e.mustExec(`CREATE FUNCTION refuse_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'could not extend file'; END $$`)
	e.mustExec(`CREATE TRIGGER refuse_write BEFORE UPDATE OR DELETE ON ap_post_out FOR EACH ROW EXECUTE FUNCTION refuse_write()`)
	logs := logged(t)
	worked := 0
	var last error
	for i := 0; i < 20; i++ {
		e.a.fed.postsDue.Store(true)
		w, err := e.out.step(context.Background())
		if w {
			worked++
		}
		last = err
	}
	if worked != 0 || last == nil {
		t.Errorf("20 steps whose record cannot be written: %d reported work, last error %v", worked, last)
	}
	if n := strings.Count(logs.String(), fmt.Sprintf("of post %d", p)); n != 0 {
		t.Errorf("the producer logged %d lines for a record it could not write", n)
	}
}

// This server's own state is not the record's failure (S3): a stop (the step's context ended) and federation refused
// meanwhile leave the record as it was, no try counted, nothing given up.
func TestDBStopIsNoTry(t *testing.T) {
	for _, how := range []string{"stopping", "refused"} {
		t.Run(how, func(t *testing.T) {
			e := newNotesEnv(t)
			bo, _ := e.remote("bo", "h1.fan.example", true)
			e.follows(bo)
			p := e.postAs(e.ana, map[string]any{"body": "x", "audience": "public"})
			e.produced()
			e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", p), nil) // a Delete: no switch drops it
			e.a.fed.outHook = func(k string, id int64) error {
				if k != "post" || id != p {
					return nil
				}
				if how == "refused" {
					e.a.fed.refuse("federation refused: a test") // enqueueOrdered answers errRefused
					return nil
				}
				return fmt.Errorf("a turn at the database: %w", context.Canceled)
			}
			t.Cleanup(func() { e.a.fed.outHook = nil })
			notMade := e.a.fed.outNotMade.Load()
			e.a.sendOut(context.Background())
			var tries int
			var due *time.Time
			if err := e.a.db.QueryRow(`SELECT tries, due_at FROM ap_post_out WHERE post_id = $1`, p).Scan(&tries, &due); err != nil {
				t.Fatalf("the record: %v", err)
			}
			if tries != 0 || due != nil || e.a.fed.outNotMade.Load() != notMade {
				t.Errorf("%s counted as a try: %d tries, due %v, outNotMade +%d", how, tries, due, e.a.fed.outNotMade.Load()-notMade)
			}
		})
	}
}

// The queue's doubt about its clock holds a member's Delete(Person) and their sharing again (S5): after a jump forward of
// more than 4 days while the Delete waits for its fan-out, the fan-out does not drop it (the clock may be wrong), and
// while the clock is doubted the member may not share again, and the page names no time. And when the Delete's rows
// were queued before the jump (so older than 48 hours by the jumped clock), the member still waits.
func TestDBLeavingUnderClockDoubt(t *testing.T) {
	t.Run("waiting for its fan-out", leavingUnderDoubtWaiting)
	t.Run("queued before the jump", leavingUnderDoubtQueued)
}

func leavingUnderDoubtQueued(t *testing.T) {
	e := newNotesEnv(t)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	e.must(200, e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true})
	stub := &personStub{now: e.clock.now}
	e.out.fetch = deliveryStub(t, stub)
	e.out.fetch.origin = e.a.fed.origin
	e.clock.add(deliveryClockFault + time.Hour)
	if _, err := e.out.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !e.out.doubting() || len(stub.taken()) != 0 {
		t.Fatalf("the setup: doubted %v, %d sent", e.out.doubting(), len(stub.taken()))
	}
	fed := e.must(200, e.ana, "GET", "/api/me", nil)["federation"].(map[string]any)
	if s := fmt.Sprint(fed["leaving"]); !strings.Contains(s, "waiting for its clock") {
		t.Errorf("rows queued before the jump, the clock doubted: the page is told %q", s)
	}
	if code, _ := e.do(e.ana, "PUT", "/api/me", map[string]any{"federates": true}); code != 409 {
		t.Errorf("sharing again while rows queued before the jump are held: %d", code)
	}
}

func leavingUnderDoubtWaiting(t *testing.T) {
	e := newNotesEnv(t)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	e.must(200, e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true})
	e.mustExec(`DELETE FROM ap_deliveries`)
	e.mustExec(`UPDATE ap_outbox SET fanned_out = false, targets = '{https://h1.fan.example/inbox}' WHERE signer IS NOT NULL`)
	e.mustExec(`INSERT INTO ap_outbox (ap_id, body, fanned_out, created_at) SELECT 'https://here.example/actor/activities/full' || g, '{}', true, now() FROM generate_series(1, 1) g`)
	e.out.waiting.Store(true)
	e.clock.add(deliveryClockFault + time.Hour)
	e.out.mu.Lock()
	e.out.checked = time.Time{}
	e.out.mu.Unlock()
	if _, err := e.out.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Not dropped: fanned out as any activity (its rows, written now, keep the member waiting: leavingSQL).
	if n := e.count(`SELECT count(*) FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.signer IS NOT NULL AND d.state = 'queued'`); n != 1 {
		t.Error("the Delete(Person) waiting for its fan-out was dropped while the clock is doubted")
	}
	if !e.out.doubting() {
		t.Fatal("the setup: the clock is not doubted")
	}
	fed := e.must(200, e.ana, "GET", "/api/me", nil)["federation"].(map[string]any)
	if s := fmt.Sprint(fed["leaving"]); !strings.Contains(s, "waiting for its clock") || strings.Contains(s, "UTC") {
		t.Errorf("while the clock is doubted the page is told %q", s)
	}
	if code, _ := e.do(e.ana, "PUT", "/api/me", map[string]any{"federates": true}); code != 409 {
		t.Errorf("sharing again while the clock is doubted: %d", code)
	}
}

// An unlike whose member leaves between its question and the queue's (6 of the third review) is answered as a member
// with no key is: the like removed here, the notice, nothing enqueued, no 500.
func TestDBUnlikeWhileLeaving(t *testing.T) {
	e := newNotesEnv(t)
	e.setFlag("community", true)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	var pid int64
	if err := e.a.db.QueryRow(`INSERT INTO posts (user_id, body, audience, ap_id) VALUES ($1, 'remote', 'public', 'https://h1.fan.example/notes/1') RETURNING id`, bo).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	e.mustExec(`INSERT INTO likes (post_id, user_id, ap_id) VALUES ($1, $2, 'https://here.example/users/ana/activities/like1')`, pid, e.anaID)
	var once atomic.Bool
	e.out.enqueueHook = func(stage, _ string) {
		if stage == "lock" && once.CompareAndSwap(false, true) {
			// What leaving changes that the queue asks about, as if it committed here.
			e.mustExec(`DELETE FROM ap_keys WHERE user_id = $1`, e.anaID)
			e.mustExec(`UPDATE users SET federates = false WHERE id = $1`, e.anaID)
		}
	}
	t.Cleanup(func() { e.out.enqueueHook = nil })
	code, out := e.do(e.ana, "POST", fmt.Sprintf("/api/posts/%d/like", pid), nil)
	if code != 200 || !strings.Contains(fmt.Sprint(out["notice"]), "Nothing is sent") || e.count(`SELECT count(*) FROM likes WHERE post_id = $1`, pid) != 0 {
		t.Errorf("an unlike while leaving: %d %v", code, out["_raw"])
	}
	if n := e.count(`SELECT count(*) FROM ap_outbox`); n != 0 {
		t.Errorf("%d activities enqueued", n)
	}
}
