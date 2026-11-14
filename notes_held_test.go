package main

// What waits to go out when it cannot go yet (the review of FWR 4): a Delete the full queue refuses stays recorded and is
// tried again on the delivery schedule, with no busy loop, while a Create it refuses is dropped (nothing left, so no
// Delete is needed); a switch turned off drops the Creates and Updates that wait, and the worker asks the switches again
// at the send; a post deleted or hidden while the server ran in local mode sends its Delete at the next start that
// federates.

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// fill makes the delivery queue full: deliveryWaitingMax activities wait for their fan-out, so enqueue answers
// errDeliveryFull.
func (e *notesEnv) fill() {
	e.t.Helper()
	e.mustExec(`INSERT INTO ap_outbox (ap_id, body, fanned_out, created_at) SELECT 'https://here.example/a/full' || g, '{}', false, now() FROM generate_series(1, $1) g`, deliveryWaitingMax)
}

func (e *notesEnv) unfill() {
	e.t.Helper()
	e.mustExec(`DELETE FROM ap_outbox WHERE ap_id LIKE 'https://here.example/a/full%'`)
}

func TestDBOutWhenQueueFull(t *testing.T) {
	e := newNotesEnv(t)
	f1, _ := e.remote("a", "one.example", true)
	e.follows(f1)
	sent := e.postAs(e.ana, map[string]any{"body": "went out", "audience": "public"})
	e.produce()
	e.mustExec(`DELETE FROM ap_deliveries`) // taken
	e.fill()
	// A Create the full queue refuses is dropped, and a Delete of that post later needs nothing.
	lost := e.postAs(e.ana, map[string]any{"body": "too late", "audience": "public"})
	e.produce()
	if n := e.count(`SELECT count(*) FROM ap_post_out WHERE post_id = $1`, lost); n != 0 {
		t.Errorf("a Create the full queue refused left %d records", n)
	}
	// A Delete it refuses stays, held for the schedule's first wait; asking again meanwhile does nothing.
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", sent), nil)
	e.produce()
	var tries int
	var due time.Time
	if err := e.a.db.QueryRow(`SELECT tries, due_at FROM ap_post_out WHERE post_id = $1 AND state = 'delete'`, sent).Scan(&tries, &due); err != nil {
		t.Fatalf("the Delete's record: %v", err)
	}
	if tries != 1 || !due.Equal(e.out.now().Add(deliveryRetries[0])) {
		t.Errorf("held: %d tries, due %v", tries, due)
	}
	if did, err := e.a.sendOut(context.Background()); did || err != nil {
		t.Errorf("a step before the record is due did something: %v %v", did, err)
	}
	// Room again, and the wait over: the Delete goes, and its record with it.
	e.unfill()
	e.clock.add(deliveryRetries[0])
	e.produce()
	if n := e.count(`SELECT count(*) FROM ap_post_out`); n != 0 {
		t.Errorf("%d records after the Delete went", n)
	}
	if n := e.count(`SELECT count(*) FROM ap_outbox WHERE ap_id = $1`, fmt.Sprintf("https://here.example/users/ana/posts/%d#delete", sent)); n != 1 {
		t.Errorf("the Delete was enqueued %d times", n)
	}
	// Past the schedule's end it is given up, said in the log.
	again := e.postAs(e.ana, map[string]any{"body": "again", "audience": "public"})
	e.produce()
	e.mustExec(`DELETE FROM ap_deliveries`)
	e.fill()
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", again), nil)
	e.mustExec(`UPDATE ap_post_out SET tries = $1`, len(deliveryRetries))
	e.produce()
	if n := e.count(`SELECT count(*) FROM ap_post_out`); n != 0 {
		t.Errorf("a Delete past the schedule's end is still recorded")
	}
}

func TestDBOutSwitchesRechecked(t *testing.T) {
	e := newNotesEnv(t)
	f1, _ := e.remote("a", "one.example", true)
	e.follows(f1)
	_, admin := e.user("boss", "admin")
	// Turning follows off drops the post and the profile change that wait, and keeps a Delete.
	gone := e.postAs(e.ana, map[string]any{"body": "sent", "audience": "public"})
	e.produce()
	e.mustExec(`DELETE FROM ap_deliveries`)
	e.postAs(e.ana, map[string]any{"body": "waits", "audience": "public"})
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"bio": "waits too"})
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", gone), nil)
	e.must(200, admin, "PUT", "/api/flags", map[string]any{"flags": map[string]bool{"follows": false}})
	if c, p, d := e.count(`SELECT count(*) FROM ap_post_out WHERE state = 'create'`), e.count(`SELECT count(*) FROM ap_person_out WHERE due_at IS NOT NULL`),
		e.count(`SELECT count(*) FROM ap_post_out WHERE state = 'delete'`); c != 0 || p != 0 || d != 1 {
		t.Errorf("after follows went off: %d Creates, %d Updates, %d Deletes wait", c, p, d)
	}
	e.produce()
	if n := e.count(`SELECT count(*) FROM ap_outbox WHERE ap_id LIKE '%#delete'`); n != 1 {
		t.Errorf("the Delete with follows off: %d", n)
	}
	// A switch set off behind the handler's back (another process): the worker asks at the send and drops.
	e.must(200, admin, "PUT", "/api/flags", map[string]any{"flags": map[string]bool{"follows": true}})
	e.postAs(e.ana, map[string]any{"body": "made while on", "audience": "public"})
	e.clock.add(10 * time.Minute)
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"bio": "changed while on"})
	n := e.count(`SELECT count(*) FROM ap_outbox`)
	e.setFlag("follows", false)
	e.produce()
	if got := e.count(`SELECT count(*) FROM ap_outbox`); got != n || e.count(`SELECT count(*) FROM ap_post_out WHERE state = 'create'`) != 0 || e.count(`SELECT count(*) FROM ap_person_out WHERE due_at IS NOT NULL`) != 0 {
		t.Errorf("with follows off at the send: %d activities made, records left", got-n)
	}
}

func TestDBDeleteAfterLocalMode(t *testing.T) {
	e := newNotesEnv(t)
	f1, _ := e.remote("a", "one.example", true)
	e.follows(f1)
	del := e.postAs(e.ana, map[string]any{"body": "deleted in local mode", "audience": "public"})
	hid := e.postAs(e.ana, map[string]any{"body": "hidden in local mode", "audience": "public"})
	kept := e.postAs(e.ana, map[string]any{"body": "kept", "audience": "public"})
	e.produce()
	// What local mode does: the post goes, or is hidden, and nothing marks the record.
	e.mustExec(`DELETE FROM posts WHERE id = $1`, del)
	e.mustExec(`UPDATE posts SET hidden_at = now() WHERE id = $1`, hid)
	b := e.again()
	if err := b.fedStart(map[string]string{"FLOWA_ORIGIN": "https://here.example"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		post  int64
		state string
	}{{del, "delete"}, {hid, "delete"}, {kept, "sent"}} {
		var s string
		e.a.db.QueryRow(`SELECT state FROM ap_post_out WHERE post_id = $1`, c.post).Scan(&s)
		if s != c.state {
			t.Errorf("post %d after a start that federates: %q, want %q", c.post, s, c.state)
		}
	}
}
