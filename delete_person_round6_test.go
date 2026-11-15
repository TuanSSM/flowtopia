package main

import (
	"context"
	"testing"
	"time"
)

// Sharing again after "Remove me from other servers" ends when the sending ends (the review of FWR 8), not at
// housekeeping's next pass: while a delivery of the Delete(Person) is queued, or the activity still waits for its
// fan-out, it is refused; once every delivery is done the member may share again at once, though the copy of the key is
// still kept (housekeeping has not run), since a fanned-out activity with nothing queued is never signed again.
func TestDBSharingAgainWhenSent(t *testing.T) {
	t.Run("a delivery queued, then sent", func(t *testing.T) {
		e := newNotesEnv(t)
		bo, _ := e.remote("bo", "h1.fan.example", true)
		e.follows(bo)
		e.must(200, e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true})
		if code, _ := e.do(e.ana, "PUT", "/api/me", map[string]any{"federates": true}); code != 409 {
			t.Fatalf("sharing again with the Delete(Person) queued: %d, want 409", code)
		}
		stub := &personStub{now: e.clock.now}
		e.out.fetch = deliveryStub(t, stub)
		e.out.fetch.origin = e.a.fed.origin
		e.out.pace.wait = func(ctx context.Context, d time.Duration) error { e.clock.add(d); return ctx.Err() }
		e.sendAll()
		if n := len(stub.taken()); n != 1 || e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'queued'`) != 0 {
			t.Fatalf("the setup: %d requests, %d rows queued", n, e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'queued'`))
		}
		if n := e.count(`SELECT count(*) FROM ap_outbox WHERE signer = $1 AND signer_sealed IS NOT NULL`, e.anaID); n != 1 {
			t.Fatalf("the setup: the copy of the key is gone already (%d), so this shows nothing", n)
		}
		fed := e.must(200, e.ana, "GET", "/api/me", nil)["federation"].(map[string]any)
		if fed["leaving"] != nil {
			t.Errorf("with nothing left to send the card still says: %v", fed["leaving"])
		}
		if code, out := e.do(e.ana, "PUT", "/api/me", map[string]any{"federates": true}); code != 200 {
			t.Errorf("sharing again once the Delete(Person) is sent, before housekeeping: %d %v", code, out)
		}
	})
	t.Run("waiting for its fan-out", func(t *testing.T) {
		e := newNotesEnv(t)
		bo, _ := e.remote("bo", "h1.fan.example", true)
		e.follows(bo)
		e.must(200, e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true})
		e.mustExec(`DELETE FROM ap_deliveries`)
		e.mustExec(`UPDATE ap_outbox SET fanned_out = false, targets = '{https://h1.fan.example/inbox}' WHERE signer IS NOT NULL`)
		if code, _ := e.do(e.ana, "PUT", "/api/me", map[string]any{"federates": true}); code != 409 {
			t.Errorf("sharing again with the Delete(Person) waiting for its fan-out: %d, want 409", code)
		}
		// Fanned out with nothing queued (every delivery done or given up): nothing will be signed with the copy again.
		e.mustExec(`UPDATE ap_outbox SET fanned_out = true, targets = NULL WHERE signer IS NOT NULL`)
		if code, out := e.do(e.ana, "PUT", "/api/me", map[string]any{"federates": true}); code != 200 {
			t.Errorf("sharing again with the Delete(Person) fanned out and nothing queued: %d %v", code, out)
		}
	})
}
