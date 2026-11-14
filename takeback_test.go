package main

// Taking back (docs/federation.md 8.8, "Taking back"; the review of FWR 4): an activity that takes back another (a post's
// Delete its Create, an Undo its Follow) never reaches an inbox before the first's outcome there, and is never lost
// where the first may have arrived. Inboxes, each in another state when the post is deleted: the Create never tried
// (attempts 0: no request left, so it is given up and no Delete goes), the Create failed once and waits for its next
// attempt (that request may have arrived, as a 504 from a proxy after the post was stored: the Create is given up and
// the Delete goes), claimed with a request in flight (the Delete waits for the end of its lease, and at its send gives
// the Create up if that failed meanwhile), already taken (the Delete goes at once), and the Create still waiting for
// its own fan-out because the queue was full (neither goes anywhere).

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// rowOf is the state, due time and lease of the row of activity apID for inbox, or "" when there is none.
func (e *notesEnv) rowOf(apID, inbox string) (state string, next time.Time, lease *time.Time, why string) {
	e.t.Helper()
	err := e.a.db.QueryRow(`SELECT d.state, d.next_at, d.locked_until, d.last_error FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.ap_id = $1 AND d.inbox = $2`,
		apID, inbox).Scan(&state, &next, &lease, &why)
	if err != nil && !strings.Contains(err.Error(), "no rows") {
		e.t.Fatal(err)
	}
	return state, next, lease, why
}

func TestDBDeleteNeverOvertakesCreate(t *testing.T) {
	e := newNotesEnv(t)
	for _, h := range []string{"never", "failed", "flight", "taken"} {
		id, _ := e.remote("f", h+".fan.example", true)
		e.follows(id)
	}
	in := func(h string) string { return "https://" + h + ".fan.example/inbox" }
	p := e.postAs(e.ana, map[string]any{"body": "soon gone", "audience": "public"})
	e.produce()
	create := fmt.Sprintf("https://here.example/users/ana/posts/%d/activity", p)
	del := fmt.Sprintf("https://here.example/users/ana/posts/%d#delete", p)
	now := e.out.now()
	lease := now.Add(deliveryLease)
	// never.fan.example: not tried yet (its host busy). failed: one 5xx, the next attempt in ten minutes. flight: claimed
	// now. taken: answered 202, gone.
	e.mustExec(`UPDATE ap_deliveries SET next_at = $2 WHERE inbox = $1`, in("never"), now.Add(time.Minute))
	e.mustExec(`UPDATE ap_deliveries SET next_at = $2, attempts = 1 WHERE inbox = $1`, in("failed"), now.Add(10*time.Minute))
	e.mustExec(`UPDATE ap_deliveries SET locked_until = $2, attempts = 1 WHERE inbox = $1`, in("flight"), lease)
	e.mustExec(`DELETE FROM ap_deliveries WHERE inbox = $1`, in("taken"))
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", p), nil)
	e.produce()

	if s, _, _, why := e.rowOf(create, in("never")); s != "failed" || why != "taken back before it was sent" {
		t.Errorf("never.fan.example: the Create is %q (%s), want given up as taken back before it was sent", s, why)
	}
	if s, _, _, _ := e.rowOf(del, in("never")); s != "" {
		t.Errorf("never.fan.example: a Delete is queued (%s) for a server no request ever reached", s)
	}
	if s, _, _, why := e.rowOf(create, in("failed")); s != "failed" || why != "taken back after an attempt that may have arrived" {
		t.Errorf("failed.fan.example: the Create is %q (%s), want given up after an attempt that may have arrived", s, why)
	}
	if s, next, _, _ := e.rowOf(del, in("failed")); s != "queued" || next.After(now) {
		t.Errorf("failed.fan.example: the Delete is %q, due %v: the Create may have arrived, so it must go now", s, next)
	}
	if s, next, _, _ := e.rowOf(del, in("flight")); s != "queued" || next.Before(lease) {
		t.Errorf("flight.fan.example: the Delete is %q, due %v, before the Create's lease ends at %v", s, next, lease)
	}
	if s, next, _, _ := e.rowOf(del, in("taken")); s != "queued" || next.After(now) {
		t.Errorf("taken.fan.example: the Delete is %q, due %v, want due now", s, next)
	}

	// The claim order holds it: stepping the queue sends the Deletes to failed.fan.example and taken.fan.example, not to
	// flight.fan.example while its Create is still claimed.
	var mu sync.Mutex
	var got []string
	e.out.fetch = deliveryStub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Host)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	e.out.fetch.origin = e.a.fed.origin
	ctx := context.Background()
	step := func() {
		t.Helper()
		for i := 0; i < 20; i++ {
			if w, err := e.out.step(ctx); err != nil {
				t.Fatal(err)
			} else if !w {
				return
			}
		}
	}
	step()
	slices.Sort(got)
	if !slices.Equal(got, []string{"failed.fan.example", "taken.fan.example"}) {
		t.Fatalf("sent to %v while the Create to flight.fan.example is in flight", got)
	}
	// The Create's request in flight failed (its row is given back for a later attempt) and the lease is over: at its
	// send the Delete gives that Create up and goes, since the Create may have arrived before its answer was lost.
	e.mustExec(`UPDATE ap_deliveries d SET locked_until = NULL, next_at = $2 FROM ap_outbox o WHERE o.id = d.outbox_id AND o.ap_id = $1 AND d.inbox = $3`,
		create, now.Add(30*time.Minute), in("flight"))
	e.clock.add(deliveryLease + time.Second)
	step()
	if !slices.Equal(got, []string{"failed.fan.example", "taken.fan.example", "flight.fan.example"}) {
		t.Fatalf("sent to %v, want the Delete to flight.fan.example after the Create's lease", got)
	}
	if s, _, _, why := e.rowOf(create, in("flight")); s != "failed" || !strings.HasPrefix(why, "taken back") {
		t.Errorf("flight.fan.example: the Create after its Delete went is %q (%s)", s, why)
	}
	// The same, with the Create still claimed when the Delete's row comes up: the Delete is given back, not sent, and
	// no attempt is counted.
	e2 := newNotesEnv(t)
	id, _ := e2.remote("g", "late.fan.example", true)
	e2.follows(id)
	p2 := e2.postAs(e2.ana, map[string]any{"body": "x", "audience": "public"})
	e2.produce()
	e2.must(200, e2.ana, "DELETE", fmt.Sprintf("/api/posts/%d", p2), nil)
	e2.produce() // the Create was not claimed: taken back, nothing to send
	if n := e2.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'queued'`); n != 0 {
		t.Errorf("late.fan.example: %d rows queued after a Create not yet sent was taken back", n)
	}
	p3 := e2.postAs(e2.ana, map[string]any{"body": "y", "audience": "public"})
	e2.produce()
	c3 := fmt.Sprintf("https://here.example/users/ana/posts/%d/activity", p3)
	d3 := fmt.Sprintf("https://here.example/users/ana/posts/%d#delete", p3)
	now2 := e2.out.now()
	e2.mustExec(`UPDATE ap_deliveries SET locked_until = $1`, now2.Add(deliveryLease))
	e2.must(200, e2.ana, "DELETE", fmt.Sprintf("/api/posts/%d", p3), nil)
	e2.produce()
	// Its due time is moved back to now, as if the lease had been renewed past it: deliver asks again and holds it.
	e2.mustExec(`UPDATE ap_deliveries d SET next_at = $2 FROM ap_outbox o WHERE o.id = d.outbox_id AND o.ap_id = $1`, d3, now2)
	e2.mustExec(`UPDATE ap_deliveries d SET locked_until = $2 FROM ap_outbox o WHERE o.id = d.outbox_id AND o.ap_id = $1`, c3, now2.Add(2*deliveryLease))
	var sent2 []string // the stub's handler runs on the server's goroutines: under mu, and safe to call any number of times
	e2.out.fetch = deliveryStub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sent2 = append(sent2, r.Host)
		mu.Unlock()
		w.WriteHeader(202)
	}))
	e2.out.fetch.origin = e2.a.fed.origin
	if _, err := e2.out.step(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	n2 := len(sent2)
	mu.Unlock()
	if s, next, _, _ := e2.rowOf(d3, "https://late.fan.example/inbox"); n2 != 0 || s != "queued" || next.Before(now2.Add(2*deliveryLease)) {
		t.Errorf("a Delete claimed while its Create is in flight: sent %v, %q due %v", sent2, s, next)
	}
	var attempts int
	e2.a.db.QueryRow(`SELECT d.attempts FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.ap_id = $1`, d3).Scan(&attempts)
	if attempts != 0 {
		t.Errorf("the Delete held back counted %d attempts", attempts)
	}
}

// A Create still waiting for its own fan-out (the queue was full) when its Delete comes goes nowhere, and neither does
// the Delete.
func TestDBDeleteOfWaitingCreate(t *testing.T) {
	e := newNotesEnv(t)
	id, _ := e.remote("f", "wait.fan.example", true)
	e.follows(id)
	p := e.postAs(e.ana, map[string]any{"body": "held", "audience": "public"})
	e.produce()
	// As enqueue leaves an activity past the queue's cap: its rows not written, its inboxes in targets.
	e.mustExec(`DELETE FROM ap_deliveries`)
	e.mustExec(`UPDATE ap_outbox SET fanned_out = false, targets = ARRAY['https://wait.fan.example/inbox']`)
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", p), nil)
	e.produce()
	if n := e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'queued'`); n != 0 {
		t.Errorf("%d rows queued for a post whose Create never left", n)
	}
	if n := e.count(`SELECT count(*) FROM ap_outbox WHERE NOT fanned_out`); n != 0 {
		t.Errorf("%d activities still wait for their fan-out", n)
	}
}
