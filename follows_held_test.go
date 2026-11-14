package main

// Follows across servers, what the review of FWR 3 asked to hold: the follows switch, what a Follow and its Undo leave
// behind, how often a repeat is answered, the lists, a real 410 and a suspended domain. On followEnv (follows_test.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

// With the follows flag off (its default), the inbox takes nothing that would add or accept a follow: a Follow and an
// Accept are counted and dropped (202, no row, nothing sent, nobody told), and the worker drops one stored before the
// flag went off. What only removes a follow (a Reject, an Undo, a Block) is still acted on: removing is always safe and
// keeps this server in step with the other. The rows there were stay as they were, hidden as local follows are, and
// turning the flag on again replays nothing. The actor says manuallyApprovesFollowers while off.
func TestDBFollowsFlagOff(t *testing.T) {
	e := newFollowEnv(t)
	anaID, ana := e.member("ana", false)
	cydID, cyd := e.member("cyd", true)
	e.member("eve", false)
	bo := e.remoteID(boID)
	// What there was before the flag went off: bo follows ana and waits for cyd; ana's Follow of bo waits.
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state, ap_id) VALUES ($1, $2, 'accepted', 'https://social.example/f/a'), ($1, $3, 'pending', 'https://social.example/f/c'),
		($2, $1, 'pending', 'https://here.example/users/ana/activities/x')`, bo, anaID, cydID)
	e.setFlag("follows", false)
	approves := func() any {
		var d map[string]any
		json.Unmarshal([]byte(e.visitBody("/users/ana")), &d)
		return d["manuallyApprovesFollowers"]
	}
	if approves() != true {
		t.Errorf("ana's actor while follows are off: manuallyApprovesFollowers %v", approves())
	}
	// A Follow of an unlocked and of a locked member, and an Accept of ana's Follow: dropped, nothing written or sent.
	for _, c := range []struct {
		name string
		body []byte
	}{
		{"a Follow of eve", activity(boID, "Follow", "https://here.example/users/eve", nil)},
		{"a Follow of cyd", activity(boID, "Follow", "https://here.example/users/cyd", nil)},
		{"an Accept of ana's Follow", activity(boID, "Accept", "https://here.example/users/ana/activities/x", nil)},
	} {
		if why := e.deliver(c.body, e.bo()); why != inDropped {
			t.Errorf("%s with follows off: %v", c.name, why)
		}
	}
	if e.state(anaID, bo) != "pending" || e.count(`SELECT count(*) FROM follows`) != 3 || len(e.sent()) != 0 || e.count(`SELECT count(*) FROM notifications`) != 0 {
		t.Errorf("with follows off: %d rows, ana's follow %q", e.count(`SELECT count(*) FROM follows`), e.state(anaID, bo))
	}
	// A Follow stored while on and handled after the flag went off is dropped by the worker.
	e.setFlag("follows", true)
	e.clock.add(time.Second)
	e.post("/inbox", activity(boID, "Follow", "https://here.example/users/eve", nil), e.bo(), nil)
	e.setFlag("follows", false)
	for worked := true; worked; {
		var err error
		if worked, err = e.q.step(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(e.outcome(), "follows are off") || e.count(`SELECT count(*) FROM follows WHERE follower_id = $1`, bo) != 2 {
		t.Errorf("a Follow handled after the flag went off: %s", e.outcome())
	}
	// The member's routes are not there, and no Follow goes out.
	e.must(404, ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil)
	e.must(404, cyd, "GET", "/api/fed/requests", nil)
	e.must(404, cyd, "PUT", fmt.Sprintf("/api/fed/requests/%d", bo), nil)
	if len(e.sent()) != 0 {
		t.Error("something was sent while follows are off")
	}
	// Turning the flag on replays nothing: the dropped Follows stay dropped, the rows there were are as they were, and
	// cyd can answer the request that waited.
	e.setFlag("follows", true)
	if approves() != false {
		t.Errorf("ana's actor with follows on: manuallyApprovesFollowers %v", approves())
	}
	if e.count(`SELECT count(*) FROM follows`) != 3 || e.state(bo, cydID) != "pending" || len(e.sent()) != 0 {
		t.Errorf("after turning follows on: %d rows", e.count(`SELECT count(*) FROM follows`))
	}
	e.must(200, cyd, "PUT", fmt.Sprintf("/api/fed/requests/%d", bo), nil)
	e.sent()
	e.setFlag("follows", false)
	// Off again: a Reject of ana's Follow, an Undo of bo's follow of cyd and a Block remove rows, as removing is safe.
	e.deliver(activity(boID, "Reject", "https://here.example/users/ana/activities/x", nil), e.bo())
	if e.state(anaID, bo) != "" {
		t.Errorf("a Reject with follows off: %q", e.state(anaID, bo))
	}
	e.deliver(activity(boID, "Undo", "https://social.example/f/c", nil), e.bo())
	if e.state(bo, cydID) != "" {
		t.Errorf("an Undo with follows off: %q", e.state(bo, cydID))
	}
	e.deliver(activity(boID, "Block", "https://here.example/users/ana", nil), e.bo())
	if e.state(bo, anaID) != "" || len(e.sent()) != 0 {
		t.Errorf("a Block with follows off: %q", e.state(bo, anaID))
	}
}

// A server cycling Follow and Undo through new accounts leaves nothing behind: no follow, no notification, and no
// account the cache's bound cannot remove once the inbox's 7 days of de-duplication are over. And what accounts of
// other servers leave in one member's notifications is bounded, per server and in all.
func TestDBFollowUndoLeavesNothing(t *testing.T) {
	e := newFollowEnv(t)
	anaID, _ := e.member("ana", false)
	var ids []int64
	for i := range 50 {
		id := fmt.Sprintf("https://social.example/users/cycle%d", i)
		e.w.put(id, worldDoc{body: actorDoc(id, fmt.Sprintf("cycle%d", i), "", &e.other.PublicKey, nil)})
		sign := e.signer(e.other, id+"#main-key", sigCavage)
		follow := activity(id, "Follow", "https://here.example/users/ana", nil)
		var f struct{ ID string }
		json.Unmarshal(follow, &f)
		e.deliver(follow, sign)
		e.deliver(activity(id, "Undo", map[string]any{"id": f.ID, "type": "Follow", "actor": id, "object": "https://here.example/users/ana"}, nil), sign)
		ids = append(ids, e.remoteID(id))
	}
	if n := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1`, anaID); n != 0 {
		t.Errorf("50 Follow and Undo cycles left %d notifications", n)
	}
	if n := e.count(`SELECT count(*) FROM follows`); n != 0 {
		t.Errorf("they left %d follows", n)
	}
	e.mustExec(`DELETE FROM ap_inbox`) // the inbox keeps each activity 7 days for de-duplication (FWR 1), then housekeeping removes it
	if _, err := e.a.db.Exec(evictActorsSQL, remoteActorsMax, 0); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM ap_actors WHERE user_id = ANY($1)`, pq.Array(ids)); n != 0 {
		t.Errorf("%d of the 50 accounts are pinned in the cache", n)
	}
	e.sent()

	// The caps: 60 accounts of social.example follow ana; she keeps the newest 50 of their notifications.
	for i := range 60 {
		id := fmt.Sprintf("https://social.example/users/many%d", i)
		e.w.put(id, worldDoc{body: actorDoc(id, fmt.Sprintf("many%d", i), "", &e.other.PublicKey, nil)})
		e.from = fmt.Sprintf("198.51.100.%d:4000", 100+i) // another address each: an address pays for 60 key fetches an hour
		e.deliver(activity(id, "Follow", "https://here.example/users/ana", nil), e.signer(e.other, id+"#main-key", sigCavage))
	}
	e.from = "198.51.100.200:4000"
	if n := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1`, anaID); n != remoteNotesPerHost {
		t.Errorf("ana keeps %d notifications from one server, want %d", n, remoteNotesPerHost)
	}
	// And from all servers together at most remoteNotesMax: 600 more from accounts of other hosts, then one Follow.
	e.mustExec(`INSERT INTO users (username, display_name, pw_hash, role, domain) SELECT 'n' || g || '@h' || (g % 30) || '.example', '', '!', 'user', 'h' || (g % 30) || '.example' FROM generate_series(1, 600) g`)
	e.mustExec(`INSERT INTO notifications (user_id, actor_id, kind) SELECT $1, id, 'follow' FROM users WHERE username LIKE 'n%@h%.example'`, anaID)
	id := "https://social.example/users/last"
	e.w.put(id, worldDoc{body: actorDoc(id, "last", "", &e.other.PublicKey, nil)})
	e.deliver(activity(id, "Follow", "https://here.example/users/ana", nil), e.signer(e.other, id+"#main-key", sigCavage))
	if n := e.count(`SELECT count(*) FROM notifications n JOIN users u ON u.id = n.actor_id WHERE n.user_id = $1 AND u.domain <> ''`, anaID); n != remoteNotesMax {
		t.Errorf("ana keeps %d notifications from other servers, want %d", n, remoteNotesMax)
	}
	if e.count(`SELECT count(*) FROM notifications n JOIN ap_actors a ON a.user_id = n.actor_id WHERE a.ap_id = $1`, id) != 1 {
		t.Error("the newest notification was the one dropped")
	}
}

// What the review's mutations showed no test held (each must fail one of these): Decline only a request, the request
// list only requests, no notification across a block written at the same moment, and the allowance of follows across
// servers. And a real 410 through the delivery queue reaching goneFollower, and a suspended domain's follows.
func TestDBFollowsHeld(t *testing.T) {
	e := newFollowEnv(t)
	anaID, ana := e.member("ana", false)
	e.deliver(activity(boID, "Follow", "https://here.example/users/ana", nil), e.bo())
	bo := e.remoteID(boID)
	if e.state(bo, anaID) != "accepted" {
		t.Fatal("bo does not follow ana")
	}
	e.sent()
	// (a) Declining what is no request is the 404, and the accepted follower stays.
	e.must(404, ana, "DELETE", fmt.Sprintf("/api/fed/requests/%d", bo), nil)
	if e.state(bo, anaID) != "accepted" || len(e.sent()) != 0 {
		t.Errorf("declining an accepted follower: %q", e.state(bo, anaID))
	}
	// (c) An accepted follower is no request.
	if l := e.must(200, ana, "GET", "/api/fed/requests", nil); l["total"] != 0.0 {
		t.Errorf("ana's requests: %v", l["_raw"])
	}
	// (b) A block written at the same moment as a follow (the race the block's statement cannot close): bo's Reject of
	// ana's follow tells ana nothing across it.
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state, ap_id) VALUES ($1, $2, 'pending', 'https://here.example/users/ana/activities/r')`, anaID, bo)
	e.mustExec(`INSERT INTO blocks (user_id, target_id) VALUES ($1, $2)`, anaID, bo)
	before := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1`, anaID)
	e.deliver(activity(boID, "Reject", "https://here.example/users/ana/activities/r", nil), e.bo())
	if e.state(anaID, bo) != "" || e.count(`SELECT count(*) FROM notifications WHERE user_id = $1`, anaID) != before {
		t.Errorf("a Reject across a block: %q, notifications %v", e.state(anaID, bo), e.kinds(anaID))
	}
	e.mustExec(`DELETE FROM blocks`)
	// (d) The allowance of follows across servers (limits.fedFollows): one more is 429.
	e.a.lim.fedFollows = newLimiter(1, time.Hour)
	e.must(200, ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil)
	if code, out := e.do(ana, "DELETE", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil); code != 429 {
		t.Errorf("past the allowance: %d %v", code, out["_raw"])
	}
	e.a.lim.fedFollows = newLimiter(100, time.Hour)
	e.sent()

	// A real 410: the queue sends the next answer to bo's own inbox, the stub answers 410 Gone, and goneFollower
	// removes bo's follows of members here and their notifications.
	stub := newInboxStub(e.clock.now, e.pub)
	stub.answer = func(host, path, _ string) (int, string) {
		if path == "/users/bo/inbox" {
			return 410, ""
		}
		return 202, ""
	}
	e.out.fetch = deliveryStub(t, stub)
	e.out.fetch.origin = e.a.fed.origin
	e.out.pace.wait = func(ctx context.Context, d time.Duration) error { e.clock.add(d); return ctx.Err() }
	e.clock.add(followAnswerEvery)
	e.deliver(activity(boID, "Follow", "https://here.example/users/ana", nil), e.bo())
	for worked := true; worked; {
		var err error
		if worked, err = e.out.step(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if e.state(bo, anaID) != "" || e.count(`SELECT count(*) FROM notifications WHERE actor_id = $1 AND kind = 'follow'`, bo) != 0 || len(stub.requests()) == 0 {
		t.Errorf("after bo's inbox answered 410: %q, %d requests made", e.state(bo, anaID), len(stub.requests()))
	}

	// The queue names an inbox in one spelling (host in lower case, no :443); a stored inbox in another spelling is
	// still that account's.
	e.deliver(activity(boID, "Follow", "https://here.example/users/ana", nil), e.bo())
	e.mustExec(`UPDATE ap_actors SET inbox = 'https://SOCIAL.example:443/users/bo/inbox' WHERE user_id = $1`, bo)
	e.a.fed.goneFollower(context.Background(), "https://social.example/users/bo/inbox")
	if e.state(bo, anaID) != "" {
		t.Error("a 410 for bo's inbox in the queue's spelling left bo's follow")
	}
	e.mustExec(`UPDATE ap_actors SET inbox = 'https://social.example/users/bo/inbox' WHERE user_id = $1`, bo)
	e.clock.add(followAnswerEvery)

	// Suspending a domain ends the follows with its accounts both ways, and their notifications, and sends nothing.
	e.deliver(activity(boID, "Follow", "https://here.example/users/ana", nil), e.bo())
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted') ON CONFLICT DO NOTHING`, anaID, bo)
	e.sent()
	_, admin := e.user("boss", "admin")
	e.must(200, admin, "PUT", "/api/admin/federation/domains/social.example", map[string]any{"policy": "suspend"})
	if e.count(`SELECT count(*) FROM follows`) != 0 || e.count(`SELECT count(*) FROM notifications WHERE actor_id = $1`, bo) != 0 || len(e.sent()) != 0 {
		t.Errorf("after suspending social.example: %d follows", e.count(`SELECT count(*) FROM follows`))
	}
}
