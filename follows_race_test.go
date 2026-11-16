package main

// Two Follows of the same member from two accounts of one server, both new here, taken at the same moment (the
// federation suite's start: bob and cyd of two.flowa.test following ana of one.flowa.test): each becomes its own
// request, whichever inbox worker handles it, however their statements interleave, and also when one actor's
// document answers 503 once (its server not ready yet), which the sender retries.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestDBTwoNewFollowersAtOnce(t *testing.T) {
	e := newFollowEnv(t)
	anaID, _ := e.member("ana", true) // approves by hand: each Follow is a request
	const rounds = 25
	for round := range rounds {
		// Two accounts of social.example never seen here, each with its own key document.
		ids := [2]string{fmt.Sprintf("https://social.example/users/r%da", round), fmt.Sprintf("https://social.example/users/r%db", round)}
		for _, id := range ids {
			e.w.put(id, worldDoc{body: actorDoc(id, "r", "R", &e.other.PublicKey, nil)})
		}
		if round%5 == 4 {
			// The second actor's server is not ready the first time: its document answers 503, so the delivery is
			// refused and sent again, as the delivery queue on the other side does.
			e.w.put(ids[1], worldDoc{status: 503})
		}
		posted := make([]int, 2)
		var wg sync.WaitGroup
		for i, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				posted[i] = e.post("/inbox", activity(id, "Follow", "https://here.example/users/ana", nil), e.signer(e.other, id+"#main-key", sigCavage), nil).Code
			}()
		}
		wg.Wait()
		if posted[1] != 202 {
			e.w.put(ids[1], worldDoc{body: actorDoc(ids[1], "r", "R", &e.other.PublicKey, nil)})
			e.clock.add(actorFailFor) // the refused fetch is remembered that long (7.4); the sender's retries come back after it
			posted[1] = e.post("/inbox", activity(ids[1], "Follow", "https://here.example/users/ana", nil), e.signer(e.other, ids[1]+"#main-key", sigCavage), nil).Code
		}
		if posted[0] != 202 || posted[1] != 202 {
			t.Fatalf("round %d: the deliveries were answered %v", round, posted)
		}
		// Both inbox workers at once, until nothing waits.
		for {
			var worked [2]bool
			var wg sync.WaitGroup
			for w := range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					ok, err := e.q.step(context.Background())
					if err != nil {
						t.Error(err)
					}
					worked[w] = ok
				}()
			}
			wg.Wait()
			if !worked[0] && !worked[1] {
				break
			}
		}
		for _, id := range ids {
			var state, outcome string
			e.a.db.QueryRow(`SELECT COALESCE((SELECT f.state FROM follows f JOIN ap_actors a ON a.user_id = f.follower_id WHERE a.ap_id = $1 AND f.followee_id = $2), ''),
				COALESCE((SELECT i.state || ':' || i.outcome FROM ap_inbox i JOIN ap_actors a ON a.user_id = i.actor_id WHERE a.ap_id = $1 ORDER BY i.id DESC LIMIT 1), '')`,
				id, anaID).Scan(&state, &outcome)
			if state != "pending" {
				t.Errorf("round %d: the Follow of %s is %q (the inbox row %s)", round, id, state, outcome)
			}
		}
	}
	if n := e.count(`SELECT count(*) FROM follows WHERE followee_id = $1 AND state = 'pending'`, anaID); n != 2*rounds {
		t.Errorf("%d requests wait for ana, want %d", n, 2*rounds)
	}
}

// A member who shares and follows at once, before the key worker has made their key (it makes one at a time, and an
// RSA key takes from milliseconds to seconds): the Follow's send finds no key and gives the row back for a minute
// (deliveryOwnWait). Once the key is made the row is due at once and goes, not a minute later. This was the federation
// suite's "both requests reach ana" failing in CI: cyd's Follow waited that minute behind bob's key.
func TestDBFirstActivityWaitsOnlyForItsKey(t *testing.T) {
	e := newFollowEnv(t)
	f := e.a.fed
	settleKeys(t, f)
	cydID, cyd := e.user("cyd", "user")
	cy := keyOwner(cydID)
	release := make(chan struct{})
	made := make(chan bool, 4)
	f.mu.Lock()
	f.beforeKey = func(o keyOwner) {
		if o == cy {
			<-release // closed once: later requests go straight on
		}
	}
	f.handled = func(o keyOwner, ok bool, _ error) {
		if o == cy && ok {
			made <- true
		}
	}
	f.mu.Unlock()
	t.Cleanup(func() { f.mu.Lock(); f.beforeKey, f.handled = nil, nil; f.mu.Unlock() })
	e.must(200, cyd, "PUT", "/api/me", map[string]any{"federates": true}) // asks the key worker, which waits above
	bo := e.remoteID(boID)
	if v := e.must(200, cyd, "PUT", fmt.Sprintf("/api/fed/accounts/%d", bo)+"/follow", nil)["view"].(map[string]any); v["follow"] != "requested" {
		t.Fatalf("cyd's follow: %v", v)
	}
	// The send finds no key: the row is given back for deliveryOwnWait, not counted as an attempt.
	e.out.step(context.Background())
	due := func() time.Time {
		t.Helper()
		var at time.Time
		if err := e.a.db.QueryRow(`SELECT d.next_at FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.actor_id = $1 AND d.state = 'queued'`, cydID).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	if at := due(); at.Sub(e.clock.now()) < deliveryOwnWait/2 {
		t.Fatalf("the Follow with no key yet is due in %v, want about %v", at.Sub(e.clock.now()), deliveryOwnWait)
	}
	close(release)
	select {
	case <-made:
	case <-time.After(30 * time.Second):
		t.Fatal("cyd's key was not made")
	}
	// keyMade runs right after the worker says the key is made, on its goroutine, before it tells the test.
	if at := due(); at.After(e.clock.now()) {
		t.Errorf("with cyd's key made, the Follow is still due in %v", at.Sub(e.clock.now()))
	}
	if s := e.sent(); len(s) != 1 || s[0].Type != "Follow" || s[0].Owner != "cyd" {
		t.Fatalf("queued: %+v", s)
	}
	// The next step sends it, with no clock moved: delivered (its row removed) or tried (an attempt counted).
	if worked, err := e.out.step(context.Background()); !worked || err != nil {
		t.Fatalf("the send: %v %v", worked, err)
	}
	if n := e.count(`SELECT count(*) FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.actor_id = $1 AND d.attempts = 0`, cydID); n != 0 {
		t.Errorf("cyd's Follow was not sent once its key was made: %d rows untried", n)
	}
}
