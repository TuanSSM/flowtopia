package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Nothing of a member reaches an inbox after their Delete(Person), whatever state their activities were in when the
// account was deleted or the member asked to be removed: one never tried, one tried and waiting for another attempt,
// one held behind another of the member's activities (ap_outbox.comes_after), and one in flight. The first three go
// with the account (or with the member's activities, when they leave), in the transaction of the Delete(Person); the one
// in flight finishes first, since the queue sends one request at a time per host, and the Delete(Person) for that
// server waits for it. Each server then gets the Delete(Person) once, and the activities that were waiting never.
func TestDBDeletePersonOrder(t *testing.T) {
	for _, how := range []string{"deleted by an admin", "removed from other servers"} {
		t.Run(how, func(t *testing.T) { deletePersonOrder(t, how == "deleted by an admin") })
	}
}

func deletePersonOrder(t *testing.T, deleted bool) {
	e := newNotesEnv(t)
	ctx := context.Background()
	anaKey, anaKeyID, err := e.a.fed.privateKey(ctx, keyOwner(e.anaID))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ name, host string }{{"bo", "h1.fan.example"}, {"dee", "h2.fan.example"}, {"eve", "h3.fan.example"}} {
		id, _ := e.remote(f.name, f.host, false)
		e.follows(id)
	}
	now := e.clock.now()
	activity := func(n string, comesAfter any) int64 {
		var id int64
		apID := fmt.Sprintf("https://here.example/users/ana/activities/%s", n)
		if err := e.a.db.QueryRow(`INSERT INTO ap_outbox (ap_id, actor_id, body, created_at, fanned_out, comes_after) VALUES ($1, $2, $3, $4, true, $5) RETURNING id`,
			apID, e.anaID, fmt.Sprintf(`{"@context":"https://www.w3.org/ns/activitystreams","id":%q,"type":"Create","actor":"https://here.example/users/ana","object":"x"}`, apID), now, comesAfter).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	row := func(outbox int64, host, inbox string, attempts int, next time.Time) {
		e.mustExec(`INSERT INTO ap_deliveries (outbox_id, inbox, host, attempts, next_at, created_at) VALUES ($1, $2, $3, $4, $5, $6)`, outbox, inbox, host, attempts, next, now)
	}
	// In flight: due first, so the worker below claims it, and held at h3's inbox by the stub.
	flight := activity("flight", nil)
	row(flight, "h3.fan.example", "https://h3.fan.example/users/eve/inbox", 0, now.Add(-time.Hour))
	// Never tried, tried and waiting for its next attempt, and held behind the one tried: all at dee's inbox.
	untried := activity("untried", nil)
	row(untried, "h2.fan.example", "https://h2.fan.example/users/dee/inbox", 0, now)
	tried := activity("tried", nil)
	row(tried, "h2.fan.example", "https://h2.fan.example/users/dee/inbox", 1, now.Add(10*time.Minute))
	held := activity("held", "https://here.example/users/ana/activities/tried")
	row(held, "h2.fan.example", "https://h2.fan.example/users/dee/inbox", 0, now)

	release := make(chan struct{})
	stub := &personStub{pub: &anaKey.PublicKey, keyID: e.a.fed.origin.url + anaKeyID, now: e.clock.now,
		hold: map[string]chan struct{}{"h3.fan.example": release}, arrived: make(chan string, 4)}
	e.out.fetch = deliveryStub(t, stub)
	e.out.fetch.origin = e.a.fed.origin
	e.out.pace.wait = func(ctx context.Context, d time.Duration) error { e.clock.add(d); return ctx.Err() }
	inFlight := make(chan error, 1)
	go func() {
		_, err := e.out.step(context.Background())
		inFlight <- err
	}()
	if host := <-stub.arrived; host != "h3.fan.example" {
		t.Fatalf("the request in flight went to %s", host)
	}

	if deleted {
		e.must(200, e.rootSession(), "DELETE", fmt.Sprintf("/api/admin/users/%d", e.anaID), nil)
	} else {
		e.must(200, e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true})
	}
	if n := e.count(`SELECT count(*) FROM ap_outbox WHERE actor_id = $1`, e.anaID); n != 0 {
		t.Errorf("%d of ana's activities are left to go after her Delete(Person)", n)
	}
	if n := e.count(`SELECT count(*) FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.signer IS NULL`); n != 0 {
		t.Errorf("%d deliveries of anything but the Delete(Person) are left", n)
	}
	pd := e.personDeletes()
	if len(pd) != 1 || len(pd[0].inboxes) != 3 {
		t.Fatalf("the Delete(Person): %+v", pd)
	}

	// While h3's request is in flight the Delete(Person) goes everywhere else, and not to h3.
	e.sendAll()
	sentTo := func() map[string][]string {
		out := map[string][]string{}
		for _, g := range stub.taken() {
			kind := fmt.Sprint(g.body["type"])
			if kind == "Delete" && g.body["object"] == "https://here.example/users/ana" {
				kind = "Delete(Person)"
			} else {
				kind = fmt.Sprint(g.body["id"])
			}
			out[g.host] = append(out[g.host], kind)
		}
		return out
	}
	got := sentTo()
	if len(got["h1.fan.example"]) != 1 || len(got["h2.fan.example"]) != 1 || got["h2.fan.example"][0] != "Delete(Person)" || len(got["h3.fan.example"]) != 1 {
		t.Errorf("while h3's request is in flight: %v", got)
	}
	close(release)
	if err := <-inFlight; err != nil {
		t.Fatal(err)
	}
	e.sendAll()
	got = sentTo()
	want := []string{"https://here.example/users/ana/activities/flight", "Delete(Person)"}
	if fmt.Sprint(got["h3.fan.example"]) != fmt.Sprint(want) || fmt.Sprint(got["h2.fan.example"]) != "[Delete(Person)]" || fmt.Sprint(got["h1.fan.example"]) != "[Delete(Person)]" {
		t.Errorf("what each server got, in order: %v", got)
	}
	for _, g := range stub.taken() {
		if g.body["object"] == "https://here.example/users/ana" && g.sig != "" {
			t.Errorf("the Delete(Person) to %s does not verify with ana's key: %s", g.host, g.sig)
		}
	}
	if n := e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'queued'`); n != 0 {
		t.Errorf("%d deliveries still queued", n)
	}
}
