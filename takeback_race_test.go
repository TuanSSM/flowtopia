package main

// The second review of FWR 4's taking back: a Create that may have arrived is never treated as unsent, and a claim made
// by another worker while a take-back or a hold is being decided is seen as it is now. The two-transaction tests hold
// the claim's transaction open until the other statement is waiting for that row's lock (asked of pg_locks), then
// commit it.

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// A Create answered 504 (the other server stored it, its proxy answered late) and waiting for its next attempt: the
// member deletes the post, the Create is given up, and the Delete goes to that inbox.
func TestDBDeleteAfterAmbiguousFailure(t *testing.T) {
	e := newNotesEnv(t)
	id, _ := e.remote("f", "slow.fan.example", true)
	e.follows(id)
	var mu sync.Mutex
	var got []string
	e.out.fetch = deliveryStub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		kind := "Create"
		if strings.Contains(string(b), `"type":"Delete"`) {
			kind = "Delete"
		}
		mu.Lock()
		got = append(got, kind)
		mu.Unlock()
		if kind == "Create" {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	e.out.fetch.origin = e.a.fed.origin
	sent := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), got...) }
	ctx := context.Background()
	step := func() {
		t.Helper()
		for i := 0; i < 10; i++ {
			if w, err := e.out.step(ctx); err != nil {
				t.Fatal(err)
			} else if !w {
				return
			}
		}
	}
	p := e.postAs(e.ana, map[string]any{"body": "regret", "audience": "public"})
	e.produce()
	step()
	if s := sent(); len(s) != 1 || s[0] != "Create" {
		t.Fatalf("setup: sent %v", s)
	}
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", p), nil)
	e.produce()
	step()
	if s := sent(); len(s) != 2 || s[1] != "Delete" {
		t.Errorf("after a Create answered 504, the Delete: sent %v", s)
	}
	if st, _, _, why := e.rowOf(fmt.Sprintf("https://here.example/users/ana/posts/%d/activity", p), "https://slow.fan.example/inbox"); st != "failed" || why != "taken back after an attempt that may have arrived" {
		t.Errorf("the Create: %q (%s)", st, why)
	}
}

// claimOpen claims the row of activity apID for inbox in a transaction it leaves open, as another worker's claim not yet
// committed, and returns the transaction and the row's id.
func (e *notesEnv) claimOpen(apID, inbox string) (*sql.Tx, int64) {
	e.t.Helper()
	var did int64
	if err := e.a.db.QueryRow(`SELECT d.id FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.ap_id = $1 AND d.inbox = $2`, apID, inbox).Scan(&did); err != nil {
		e.t.Fatal(err)
	}
	tx, err := e.a.db.Begin()
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE ap_deliveries SET locked_until = $2, attempts = attempts + 1 WHERE id = $1`, did, e.out.now().Add(deliveryLease)); err != nil {
		e.t.Fatal(err)
	}
	return tx, did
}

// waitLock waits until some statement is waiting for a row lock (a transaction lock on a tuple not granted).
func (e *notesEnv) waitLock() {
	e.t.Helper()
	waitFor(e.t, "a statement waiting for the claimed row's lock", func() bool {
		// Only this test's schema: the server is shared, and a waiter holds a lock on this schema's ap_deliveries.
		return e.count(`SELECT count(*) FROM pg_locks w WHERE NOT w.granted AND w.locktype IN ('transactionid', 'tuple')
			AND w.pid IN (SELECT pid FROM pg_locks WHERE relation = 'ap_deliveries'::regclass)`) > 0
	})
}

// heldBackSQL while another worker's claim of the Create is not yet committed: it waits for the row, and then holds the
// Delete back until the claim's lease ends, never marking the claimed Create failed.
func TestDBHeldBackSeesLateClaim(t *testing.T) {
	e := newNotesEnv(t)
	id, _ := e.remote("f", "race.fan.example", true)
	e.follows(id)
	p := e.postAs(e.ana, map[string]any{"body": "x", "audience": "public"})
	e.produce()
	create := fmt.Sprintf("https://here.example/users/ana/posts/%d/activity", p)
	var oid int64
	if err := e.a.db.QueryRow(`SELECT id FROM ap_outbox WHERE ap_id = $1`, create).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	tx, did := e.claimOpen(create, "https://race.fan.example/inbox")
	type res struct {
		until  sql.NullTime
		gaveUp int
		err    error
	}
	ch := make(chan res, 1)
	go func() {
		var r res
		r.err = e.a.db.QueryRow(heldBackSQL, oid, "https://race.fan.example/inbox", e.out.now()).Scan(&r.until, &r.gaveUp)
		ch <- r
	}()
	e.waitLock()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	r := <-ch
	if r.err != nil {
		t.Fatal(r.err)
	}
	var st string
	e.a.db.QueryRow(`SELECT state FROM ap_deliveries WHERE id = $1`, did).Scan(&st)
	if !r.until.Valid || r.gaveUp != 0 || st != "queued" {
		t.Errorf("a Create claimed meanwhile: hold %v, gave up %d, the Create %q", r.until, r.gaveUp, st)
	}
}

// The take-back while another worker's claim of the Create is not yet committed: it waits for the row and leaves the
// claimed Create alone; its Delete is not held by that statement (it could not see the claim), and the send-time check
// then holds it.
func TestDBTakeBackSeesLateClaim(t *testing.T) {
	e := newNotesEnv(t)
	id, _ := e.remote("f", "race.fan.example", true)
	e.follows(id)
	p := e.postAs(e.ana, map[string]any{"body": "x", "audience": "public"})
	e.produce()
	create := fmt.Sprintf("https://here.example/users/ana/posts/%d/activity", p)
	del := fmt.Sprintf("https://here.example/users/ana/posts/%d#delete", p)
	tx, did := e.claimOpen(create, "https://race.fan.example/inbox")
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", p), nil)
	done := make(chan struct{})
	go func() { e.produce(); close(done) }()
	e.waitLock()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	<-done
	var st string
	e.a.db.QueryRow(`SELECT state FROM ap_deliveries WHERE id = $1`, did).Scan(&st)
	if st != "queued" {
		t.Fatalf("the claimed Create after the take-back: %q", st)
	}
	ds, _, _, _ := e.rowOf(del, "https://race.fan.example/inbox")
	if ds != "queued" {
		t.Fatalf("the Delete's row: %q", ds)
	}
	// At its send the Delete is held: nothing goes out while the Create is claimed.
	var mu sync.Mutex
	var hits int
	e.out.fetch = deliveryStub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mu.Lock(); hits++; mu.Unlock(); w.WriteHeader(202) }))
	e.out.fetch.origin = e.a.fed.origin
	if _, err := e.out.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	n := hits
	mu.Unlock()
	if n != 0 {
		t.Errorf("%d requests while the Create is claimed", n)
	}
}
