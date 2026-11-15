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
// test's own schema, as a full disk would), 20 steps report no work and the producer logs nothing. And the same for the
// full queue's branches (the fourth review), posts and comments, a Delete at its schedule's end and before it, a Create
// and a comment's Create and Update (commentOutFull's Update branch, the last review): the producer reports no work,
// counts nothing and logs nothing while its write fails. And the same for the too large Create, an Update dropped for a
// comment no longer shown, and the profile's full queue (the last review).
func TestDBFailingWriteDoesNotSpin(t *testing.T) {
	t.Run("a failing record", failingWriteRecord)
	for _, c := range []struct {
		kind, state string
		final       bool
	}{{"post", "delete", true}, {"post", "delete", false}, {"post", "create", false}, {"comment", "delete", true}, {"comment", "delete", false},
		{"comment", "create", true}, {"comment", "create", false}, {"comment", "update", true}, {"comment", "update", false}} {
		t.Run(fmt.Sprintf("full queue, %s %s, final %v", c.kind, c.state, c.final), func(t *testing.T) { failingWriteFullQueue(t, c.kind, c.state, c.final) })
	}
	t.Run("full queue, a write failing every other time", failingWriteIntermittent)
	t.Run("a Create too large to send", failingWriteTooLarge)
	t.Run("an Update dropped for a comment no longer shown", failingWriteDroppedUpdate)
	t.Run("full queue, the profile", failingWriteProfileFull)
}

// refuseWrites makes every update and deletion of table fail, as a full disk would.
func (e *notesEnv) refuseWrites(table string) {
	e.t.Helper()
	e.mustExec(`CREATE OR REPLACE FUNCTION refuse_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'could not extend file'; END $$`)
	e.mustExec(`CREATE TRIGGER refuse_write BEFORE UPDATE OR DELETE ON ` + table + ` FOR EACH ROW EXECUTE FUNCTION refuse_write()`)
}

// fillQueue fills the delivery queue to the number of activities that may wait, so the next enqueue is refused.
func (e *notesEnv) fillQueue() {
	e.t.Helper()
	e.mustExec(`INSERT INTO ap_outbox (ap_id, body, targets, created_at) SELECT 'https://here.example/actor/activities/full' || g, '{}', '{https://h1.fan.example/inbox}', now()
		FROM generate_series(1, $1) g`, deliveryWaitingMax)
}

// noSpin runs the producer ten times with a record whose write fails, and fails unless each pass reports no work with an
// error, nothing is counted, and nothing is logged about it (lines holding about).
func (e *notesEnv) noSpin(run func() (bool, error), about string) {
	e.t.Helper()
	logs := logged(e.t)
	f := e.a.fed
	notTaken, notSent, notMade := f.notTakenBack.Load(), f.commentsNotSent.Load(), f.outNotMade.Load()
	for i := 0; i < 10; i++ {
		f.postsDue.Store(true)
		f.commentsDue.Store(true)
		f.personAt.Store(0)
		if did, err := run(); did || err == nil {
			e.t.Fatalf("pass %d with the record's write failing: did %v, %v", i, did, err)
		}
	}
	if f.notTakenBack.Load() != notTaken || f.commentsNotSent.Load() != notSent || f.outNotMade.Load() != notMade {
		e.t.Errorf("counted while the write failed: notTakenBack +%d, commentsNotSent +%d, outNotMade +%d", f.notTakenBack.Load()-notTaken,
			f.commentsNotSent.Load()-notSent, f.outNotMade.Load()-notMade)
	}
	if strings.Contains(logs.String(), about) {
		e.t.Errorf("the producer logged while the write failed: %s", logs.String())
	}
}

func failingWriteTooLarge(t *testing.T) {
	e := newNotesEnv(t)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	p := e.postAs(e.ana, map[string]any{"body": "x", "audience": "public"})
	e.mustExec(`UPDATE posts SET body = repeat('x', $2) WHERE id = $1`, p, inboxBodyMax) // the Note alone is over a delivery's size
	e.refuseWrites("ap_post_out")
	e.noSpin(func() (bool, error) { return e.a.postOut(context.Background()) }, fmt.Sprintf("post %d", p))
	if n := e.count(`SELECT count(*) FROM ap_post_out WHERE post_id = $1 AND state = 'create' AND tries = 0`, p); n != 1 {
		t.Error("the record changed though its write failed")
	}
}

func failingWriteDroppedUpdate(t *testing.T) {
	e := newNotesEnv(t)
	e.setFlag("community", true)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	p := e.postAs(e.ana, map[string]any{"body": "x", "audience": "public"})
	out := e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", p), map[string]any{"body": "a comment"})
	cid := num(out["comment"].(map[string]any)["id"])
	e.produced()
	e.must(200, e.ana, "PUT", fmt.Sprintf("/api/comments/%d", cid), map[string]any{"body": "edited"})
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"federates": false})
	e.refuseWrites("ap_comment_out")
	e.noSpin(func() (bool, error) { return e.a.commentOut(context.Background()) }, fmt.Sprintf("comment %d", cid))
	if n := e.count(`SELECT count(*) FROM ap_comment_out WHERE comment_id = $1 AND state = 'update' AND tries = 0`, cid); n != 1 {
		t.Error("the record changed though its write failed")
	}
}

func failingWriteProfileFull(t *testing.T) {
	e := newNotesEnv(t)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	e.produce()
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"displayName": "Ana Again"})
	e.mustExec(`UPDATE ap_person_out SET due_at = $2 WHERE user_id = $1`, e.anaID, e.clock.now().Add(-time.Second))
	e.fillQueue()
	e.refuseWrites("ap_person_out")
	e.noSpin(func() (bool, error) { return e.a.personOut(context.Background()) }, fmt.Sprintf("member %d", e.anaID))
	if n := e.count(`SELECT count(*) FROM ap_person_out WHERE user_id = $1 AND due_at IS NOT NULL`, e.anaID); n != 1 {
		t.Error("the record changed though its write failed")
	}
}

// A write that fails once is not a failure of the record (the fourth review): the record is left as it was, and the
// retry path does not run on it (it would count a try and log "failed", or give a Delete up). With every other write
// refused (a sequence in the test's own schema), a post's Delete in a full queue is held one wait over two passes: one
// "waits" line, no "failed" line, one try.
func failingWriteIntermittent(t *testing.T) {
	e := newNotesEnv(t)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	p := e.postAs(e.ana, map[string]any{"body": "x", "audience": "public"})
	e.produce()
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", p), nil)
	e.mustExec(`INSERT INTO ap_outbox (ap_id, body, targets, created_at) SELECT 'https://here.example/actor/activities/full' || g, '{}', '{https://h1.fan.example/inbox}', now()
		FROM generate_series(1, $1) g`, deliveryWaitingMax)
	e.mustExec(`CREATE SEQUENCE every_other`)
	e.mustExec(`CREATE FUNCTION every_other_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		IF nextval('every_other') % 2 = 1 THEN RAISE EXCEPTION 'could not extend file'; END IF; RETURN COALESCE(NEW, OLD); END $$`)
	e.mustExec(`CREATE TRIGGER every_other_write BEFORE UPDATE OR DELETE ON ap_post_out FOR EACH ROW EXECUTE FUNCTION every_other_write()`)
	logs := logged(t)
	for i := 0; i < 2; i++ {
		e.a.fed.postsDue.Store(true)
		e.a.sendOut(context.Background())
	}
	l := logs.String()
	if w, f := strings.Count(l, fmt.Sprintf("Delete of post %d waits", p)), strings.Count(l, fmt.Sprintf("delete of post %d failed", p)); w != 1 || f != 0 {
		t.Errorf("two passes, every other write refused: %d waits lines, %d failed lines; want 1 and 0", w, f)
	}
	if _, tries, _ := e.postRecord(p); tries != 1 {
		t.Errorf("two passes, every other write refused: %d tries, want 1", tries)
	}
}

func failingWriteFullQueue(t *testing.T, kind, state string, final bool) {
	e := newNotesEnv(t)
	e.setFlag("community", true)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	p := e.postAs(e.ana, map[string]any{"body": "x", "audience": "public"})
	table, col, id := "ap_post_out", "post_id", p
	if kind == "comment" {
		e.produce()
		out := e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", p), map[string]any{"body": "a comment"})
		table, col, id = "ap_comment_out", "comment_id", num(out["comment"].(map[string]any)["id"])
	}
	if state == "update" {
		// The comment's Create sent, then an edit a minute past the gap between two Updates, so the Update is made.
		e.produce()
		e.must(200, e.ana, "PUT", fmt.Sprintf("/api/comments/%d", id), map[string]any{"body": "edited"})
		e.mustExec(`UPDATE ap_comment_out SET sent_at = $2 WHERE comment_id = $1`, id, e.clock.now().Add(-commentUpdateGap-time.Minute))
	}
	if state == "delete" {
		e.produce()
		path := fmt.Sprintf("/api/posts/%d", p)
		if kind == "comment" {
			path = fmt.Sprintf("/api/comments/%d", id)
		}
		e.must(200, e.ana, "DELETE", path, nil)
	}
	tries := 0
	if final {
		tries = len(deliveryRetries)
	}
	e.mustExec(`UPDATE `+table+` SET tries = $2 WHERE `+col+` = $1`, id, tries)
	if n := e.count(`SELECT count(*) FROM `+table+` WHERE `+col+` = $1 AND state = $2`, id, state); n != 1 {
		t.Fatalf("the setup: no %s record in state %s", kind, state)
	}
	e.mustExec(`INSERT INTO ap_outbox (ap_id, body, targets, created_at) SELECT 'https://here.example/actor/activities/full' || g, '{}', '{https://h1.fan.example/inbox}', now()
		FROM generate_series(1, $1) g`, deliveryWaitingMax)
	e.mustExec(`CREATE FUNCTION refuse_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'could not extend file'; END $$`)
	e.mustExec(`CREATE TRIGGER refuse_write BEFORE UPDATE OR DELETE ON ` + table + ` FOR EACH ROW EXECUTE FUNCTION refuse_write()`)
	logs := logged(t)
	notTaken, notSent, notMade := e.a.fed.notTakenBack.Load(), e.a.fed.commentsNotSent.Load(), e.a.fed.outNotMade.Load()
	for i := 0; i < 10; i++ {
		e.a.fed.postsDue.Store(true)
		e.a.fed.commentsDue.Store(true)
		did, err := e.a.sendOut(context.Background())
		if did || err == nil {
			t.Fatalf("pass %d with the record's write failing: did %v, %v", i, did, err)
		}
	}
	if e.a.fed.notTakenBack.Load() != notTaken || e.a.fed.commentsNotSent.Load() != notSent || e.a.fed.outNotMade.Load() != notMade {
		t.Errorf("counted while the write failed: notTakenBack +%d, commentsNotSent +%d, outNotMade +%d", e.a.fed.notTakenBack.Load()-notTaken,
			e.a.fed.commentsNotSent.Load()-notSent, e.a.fed.outNotMade.Load()-notMade)
	}
	if n := strings.Count(logs.String(), fmt.Sprintf("of %s %d", kind, id)) + strings.Count(logs.String(), fmt.Sprintf("%s %d is not sent", kind, id)); n != 0 {
		t.Errorf("the producer logged %d lines while the write failed: %s", n, logs.String())
	}
	if n := e.count(`SELECT count(*) FROM `+table+` WHERE `+col+` = $1 AND tries = $2`, id, tries); n != 1 {
		t.Error("the record changed though its write failed")
	}
}

func failingWriteRecord(t *testing.T) {
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
