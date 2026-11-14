package main

import (
	"context"
	"testing"
)

// Switches that cannot be read are not "off" (as follows across servers reads them, FWR 3's second review): a post and
// a profile change that wait are neither sent nor dropped, and go once the switches read on again.
func TestDBOutSwitchesUnread(t *testing.T) {
	e := newNotesEnv(t)
	f1, _ := e.remote("a", "one.example", true)
	e.follows(f1)
	e.postAs(e.ana, map[string]any{"body": "waits", "audience": "public"})
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"bio": "waits too"})
	real := e.a.fed.switches
	e.a.fed.switches = func() (bool, bool) { return false, false }
	for range 3 {
		if did, err := e.a.sendOut(context.Background()); did || err != errSwitchesUnread {
			t.Fatalf("a pass with the switches unread: %v %v", did, err)
		}
	}
	if e.count(`SELECT count(*) FROM ap_post_out WHERE state = 'create'`) != 1 || e.count(`SELECT count(*) FROM ap_person_out WHERE due_at IS NOT NULL`) != 1 || e.count(`SELECT count(*) FROM ap_outbox`) != 0 {
		t.Fatal("with the switches unread something was sent or dropped")
	}
	e.a.fed.switches = real
	e.produce()
	if n := e.count(`SELECT count(*) FROM ap_outbox`); n != 2 {
		t.Errorf("%d activities once the switches read on, want the Create and the Update", n)
	}
}
