package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Follow then Undo(Follow) to one inbox (FWR 3, found in the review of FWR 4): the Undo takes back the Follow, so a
// Follow never tried is given up and no Undo goes; one that met one server error (it may have arrived) is given up and
// the Undo goes, so no follow stays there; one in flight holds the Undo until its lease ends (docs/federation.md 8.8,
// "Taking back").
func TestDBUndoNeverOvertakesFollow(t *testing.T) {
	e := newFollowEnv(t)
	_, cyd := e.member("cyd", false)
	bo := e.remoteID(boID)
	follow := func() string {
		t.Helper()
		e.must(200, cyd, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil)
		var id string
		if err := e.a.db.QueryRow(`SELECT ap_id FROM ap_outbox ORDER BY id DESC LIMIT 1`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	inbox := "https://social.example/users/bo/inbox"
	undoRow := func() (state string, next time.Time) {
		t.Helper()
		err := e.a.db.QueryRow(`SELECT d.state, d.next_at FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id
			WHERE o.body LIKE '%"type":"Undo"%' AND d.inbox = $1 ORDER BY d.id DESC LIMIT 1`, inbox).Scan(&state, &next)
		if err != nil && !strings.Contains(err.Error(), "no rows") {
			t.Fatal(err)
		}
		return
	}
	// The Follow never tried: the unfollow takes it back, and no Undo goes.
	f0 := follow()
	e.must(200, cyd, "DELETE", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil)
	var st, why string
	e.a.db.QueryRow(`SELECT d.state, d.last_error FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.ap_id = $1`, f0).Scan(&st, &why)
	if s, _ := undoRow(); st != "failed" || why != "taken back before it was sent" || s != "" {
		t.Errorf("a Follow never sent and its Undo: the Follow is %q (%s), an Undo row %q", st, why, s)
	}
	// The Follow failed once and waits ten minutes: it may have arrived, so it is given up and the Undo goes now.
	f1 := follow()
	now := e.out.now()
	e.mustExec(`UPDATE ap_deliveries d SET next_at = $2, attempts = 1 FROM ap_outbox o WHERE o.id = d.outbox_id AND o.ap_id = $1`, f1, now.Add(10*time.Minute))
	e.must(200, cyd, "DELETE", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil)
	e.a.db.QueryRow(`SELECT d.state, d.last_error FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.ap_id = $1`, f1).Scan(&st, &why)
	if s, next := undoRow(); st != "failed" || s != "queued" || next.After(now) {
		t.Errorf("a Follow that may have arrived and its Undo: the Follow is %q (%s), the Undo %q due %v", st, why, s, next)
	}
	// The Follow in flight: the Undo waits for the end of its lease.
	f2 := follow()
	lease := e.out.now().Add(deliveryLease)
	e.mustExec(`UPDATE ap_deliveries d SET locked_until = $2 FROM ap_outbox o WHERE o.id = d.outbox_id AND o.ap_id = $1`, f2, lease)
	e.must(200, cyd, "DELETE", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil)
	if s, next := undoRow(); s != "queued" || next.Before(lease) {
		t.Errorf("a Follow in flight and its Undo: %q due %v, before the lease ends at %v", s, next, lease)
	}
}
