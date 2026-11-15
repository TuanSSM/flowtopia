package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// produced runs the producer and then the workers' steps until neither does anything, failing the test on an error from
// either: what a worker's loop does, minus its waits.
func (e *notesEnv) produced() {
	e.t.Helper()
	for i := 0; i < 400; i++ {
		did, err := e.a.sendOut(context.Background())
		if err != nil {
			e.t.Fatalf("the producer: %v", err)
		}
		worked, err := e.out.step(context.Background())
		if err != nil {
			e.t.Fatalf("a worker's step: %v", err)
		}
		if !did && !worked {
			return
		}
	}
	e.t.Fatal("the producer and the workers do not settle")
}

// createOf says whether member name's post id has its Create in the queue.
func (e *notesEnv) createOf(name string, post int64) bool {
	return e.count(`SELECT count(*) FROM ap_outbox WHERE ap_id = $1`, fmt.Sprintf("https://here.example/users/%s/posts/%d/activity", name, post)) == 1
}

// One record the queue refuses for good does not stop the producer (1 of the second review of FWR 7): bob shares, his
// key is not made yet, he posts Public to a follower on another server, stops sharing, and deletes the post. The queue
// refuses the Delete (he has no key and does not share): that record is done, nothing of his having gone out, and
// counted; ana's post after it goes out.
func TestDBRefusedDeleteHoldsNobody(t *testing.T) {
	for _, kind := range []string{"post", "comment"} {
		t.Run(kind, func(t *testing.T) { refusedDeleteHoldsNobody(t, kind) })
	}
}

func refusedDeleteHoldsNobody(t *testing.T, kind string) {
	e := newNotesEnv(t)
	e.setFlag("community", true)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	bobID, bob := e.user("bob", "user")
	e.mustExec(`UPDATE users SET federates = true WHERE id = $1`, bobID) // shares; his key is not made yet
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, bo, bobID)
	var path string
	if kind == "post" {
		p := e.postAs(bob, map[string]any{"body": "bob's", "audience": "public"})
		path = fmt.Sprintf("/api/posts/%d", p)
	} else {
		p := e.postAs(e.ana, map[string]any{"body": "ana's, to comment on", "audience": "public"})
		out := e.must(200, bob, "POST", fmt.Sprintf("/api/posts/%d/comments", p), map[string]any{"body": "bob's comment"})
		path = fmt.Sprintf("/api/comments/%d", num(out["comment"].(map[string]any)["id"]))
	}
	e.produced()
	if n := e.count(`SELECT count(*) FROM ap_outbox WHERE actor_id = $1`, bobID); n != 1 {
		t.Fatalf("bob's Create: %d activities", n)
	}
	e.must(200, bob, "PUT", "/api/me", map[string]any{"federates": false})
	e.must(200, bob, "DELETE", path, nil)
	before := e.a.fed.outNotMade.Load()
	later := e.postAs(e.ana, map[string]any{"body": "ana's, after", "audience": "public"})
	e.produced()
	if !e.createOf("ana", later) {
		t.Error("ana's post after bob's refused Delete did not go out")
	}
	if n := e.count(`SELECT count(*) FROM ap_post_out WHERE user_id = $1`, bobID) + e.count(`SELECT count(*) FROM ap_comment_out WHERE user_id = $1`, bobID); n != 0 {
		t.Errorf("bob's record is left: %d", n)
	}
	if e.a.fed.outNotMade.Load() != before+1 {
		t.Errorf("the refused Delete is not counted: %d", e.a.fed.outNotMade.Load()-before)
	}
	if n := e.count(`SELECT count(*) FROM ap_keys WHERE user_id = $1`, bobID); n != 0 {
		t.Error("a key was made for bob")
	}
}

// A record whose making fails every time is put back on the delivery schedule while the one behind it is made and
// delivered, and is given up, counted, after the schedule's last wait (1 of the second review): no single record holds
// the producer or the workers. The failure is a hook's, so it fails for no reason the code could fix.
func TestDBFailingRecordHoldsNobody(t *testing.T) {
	for _, kind := range []string{"post", "comment"} {
		t.Run(kind, func(t *testing.T) { failingRecordHoldsNobody(t, kind) })
	}
}

func failingRecordHoldsNobody(t *testing.T, kind string) {
	e := newNotesEnv(t)
	e.setFlag("community", true)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	stub := &personStub{now: e.clock.now}
	e.out.fetch = deliveryStub(t, stub)
	e.out.fetch.origin = e.a.fed.origin
	var stuck int64
	if kind == "post" {
		stuck = e.postAs(e.ana, map[string]any{"body": "fails every time", "audience": "public"})
	} else {
		p := e.postAs(e.ana, map[string]any{"body": "the post", "audience": "public"})
		e.produced()
		out := e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", p), map[string]any{"body": "fails every time"})
		stuck = num(out["comment"].(map[string]any)["id"])
	}
	bad := errors.New("a record that cannot be made")
	e.a.fed.outHook = func(k string, id int64) error {
		if k == kind && id == stuck {
			return bad
		}
		return nil
	}
	defer func() { e.a.fed.outHook = nil }()
	before, sent := e.a.fed.outNotMade.Load(), len(stub.taken())
	behind := e.postAs(e.ana, map[string]any{"body": "behind it", "audience": "public"})
	e.produced()
	if !e.createOf("ana", behind) {
		t.Fatal("the post behind the failing record was not made")
	}
	if len(stub.taken()) <= sent {
		t.Error("nothing was delivered while a record failed")
	}
	table, col := "ap_post_out", "post_id"
	if kind == "comment" {
		table, col = "ap_comment_out", "comment_id"
	}
	if n := e.count(`SELECT count(*) FROM `+table+` WHERE `+col+` = $1 AND tries = 1 AND due_at IS NOT NULL`, stuck); n != 1 {
		t.Errorf("the failing record is not put back on the schedule")
	}
	// Through the schedule: each wait passed on the test's clock, the record fails again; after the last it is given up.
	for i := 0; i < len(deliveryRetries)+1; i++ {
		e.clock.add(deliveryRetries[min(i, len(deliveryRetries)-1)])
		e.a.fed.postsDue.Store(true)
		e.a.fed.commentsDue.Store(true)
		e.produced()
	}
	if n := e.count(`SELECT count(*) FROM `+table+` WHERE `+col+` = $1`, stuck); n != 0 {
		t.Errorf("the failing record is not given up after the schedule")
	}
	if e.a.fed.outNotMade.Load() != before+1 {
		t.Errorf("given up %d times, want once", e.a.fed.outNotMade.Load()-before)
	}
}

// A producer that fails, for a reason no record carries (its switches unread, the database), does not stop the workers:
// the step says the error and still claims and sends a row that is due (1 of the second review).
func TestDBProducerErrorStillSends(t *testing.T) {
	e := newNotesEnv(t)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	p := e.postAs(e.ana, map[string]any{"body": "due", "audience": "public"})
	e.produce() // made, not sent
	if !e.createOf("ana", p) {
		t.Fatal("the setup: ana's Create is not queued")
	}
	stub := &personStub{now: e.clock.now}
	e.out.fetch = deliveryStub(t, stub)
	e.out.fetch.origin = e.a.fed.origin
	was := e.out.produce
	e.out.produce = func(context.Context) (bool, error) { return false, errors.New("the producer cannot work") }
	defer func() { e.out.produce = was }()
	worked, _ := e.out.step(context.Background()) // the row's own outcome is not what is asked here
	if !worked || len(stub.taken()) != 1 {
		t.Errorf("a step with its producer failing: worked %v, %d sent", worked, len(stub.taken()))
	}
	if worked, err := e.out.step(context.Background()); worked || err == nil || !strings.Contains(err.Error(), "cannot work") {
		t.Errorf("with nothing to claim, the step says the producer's error: %v %v", worked, err)
	}
}

// Sharing again waits for the member's Delete(Person) at most the queue's 48 hours, whatever the queue's state (2 of
// the second review): with sending paused past them on the test's clock, the member may share again and the page names
// no time; when sending resumes the old Delete is given up unsigned, before its kept key is opened (the copy is made
// unopenable first, so opening it would be the failure the row says), and one still waiting for its fan-out is never
// fanned out.
func TestDBSharingAgainAfterHorizon(t *testing.T) {
	for _, waiting := range []bool{false, true} {
		name := "fanned out"
		if waiting {
			name = "waiting for its fan-out"
		}
		t.Run(name, func(t *testing.T) { sharingAgainAfterHorizon(t, waiting) })
	}
}

func sharingAgainAfterHorizon(t *testing.T, waiting bool) {
	e := newNotesEnv(t)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	e.must(200, e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true})
	if waiting {
		e.mustExec(`DELETE FROM ap_deliveries`)
		e.mustExec(`UPDATE ap_outbox SET fanned_out = false, targets = '{https://h1.fan.example/inbox}' WHERE signer IS NOT NULL`)
		e.out.waiting.Store(true)
	}
	e.setFlag(fedFlag, false)
	fed := func() map[string]any { return e.must(200, e.ana, "GET", "/api/me", nil)["federation"].(map[string]any) }
	if code, _ := e.do(e.ana, "PUT", "/api/me", map[string]any{"federates": true}); code != 409 || fed()["leaving"] == nil {
		t.Fatalf("sharing again at once: %d, %v", code, fed())
	}
	e.clock.add(47 * time.Hour)
	if code, _ := e.do(e.ana, "PUT", "/api/me", map[string]any{"federates": true}); code != 409 || fed()["leaving"] == nil {
		t.Errorf("sharing again after 47 hours: %d", code)
	}
	e.clock.add(2 * time.Hour)
	if f := fed(); f["leaving"] != nil {
		t.Errorf("after 49 hours paused the page still names a time: %v", f["leaving"])
	}
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"federates": true})
	e.mustExec(`UPDATE ap_outbox SET signer_sealed = 'v1.not-a-key' WHERE signer IS NOT NULL`)
	stub := &personStub{now: e.clock.now}
	e.out.fetch = deliveryStub(t, stub)
	e.out.fetch.origin = e.a.fed.origin
	e.out.pace.wait = func(ctx context.Context, d time.Duration) error { e.clock.add(d); return ctx.Err() }
	e.setFlag(fedFlag, true)
	e.out.mu.Lock()
	e.out.checked = time.Time{}
	e.out.mu.Unlock()
	e.sendAll()
	if n := len(stub.taken()); n != 0 {
		t.Errorf("the old Delete(Person) went out after 48 hours: %d requests", n)
	}
	if waiting {
		if n := e.count(`SELECT count(*) FROM ap_deliveries`); n != 0 || e.count(`SELECT count(*) FROM ap_outbox WHERE signer IS NOT NULL AND fanned_out`) != 1 {
			t.Errorf("the Delete waiting for its fan-out was fanned out: %d rows", n)
		}
	} else if n := e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'failed' AND last_error = 'not delivered within 48 hours'`); n != 1 {
		t.Errorf("the old Delete is not given up for its age before its key is opened: %d", n)
	}
}

// A key made for a member who stops sharing while it is being made is not written (4 of the second review): the check
// under the member's locked row in ensureKey's transaction sees the change; the one before the key is made did not.
// The member stops sharing in that gap, by a hook, every time.
func TestDBKeyNotWrittenAfterStopping(t *testing.T) {
	e := newNotesEnv(t)
	bobID, bob := e.user("bob", "user")
	e.mustExec(`UPDATE users SET federates = true WHERE id = $1`, bobID)
	f := e.a.fed
	f.mu.Lock()
	f.keyMade = func(o keyOwner) {
		if o == keyOwner(bobID) {
			e.must(200, bob, "PUT", "/api/me", map[string]any{"federates": false})
		}
	}
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.keyMade = nil; f.mu.Unlock() }()
	made, err := f.ensureKey(context.Background(), keyOwner(bobID))
	if made || !errors.Is(err, errNotSharing) || e.count(`SELECT count(*) FROM ap_keys WHERE user_id = $1`, bobID) != 0 {
		t.Errorf("a key made while bob stopped sharing: made %v, %v, %d keys", made, err, e.count(`SELECT count(*) FROM ap_keys WHERE user_id = $1`, bobID))
	}
}
