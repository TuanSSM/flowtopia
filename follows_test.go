package main

// Follows across servers (docs/federation.md 6.3, 6.4, 9.1 and 11; FWR item 3): a federated instance at
// https://here.example whose inbox takes deliveries signed as other servers sign them (inbox_test.go's world),
// whose delivery queue is driven by the test and never sends on its own, and, in TestDBTwoFlowaFollow, a second
// Flowa at https://b.example reached through the real fetcher, signer, inbox and delivery queue. No test waits
// on a goroutine or the wall clock: the workers are stepped by hand and the clocks are the test's.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// followEnv is inboxEnv with follows on and the delivery queue held: what is enqueued is read from its tables.
type followEnv struct {
	*inboxEnv
	out  *deliveryQueue
	seen int64 // the last ap_outbox row sent() returned
}

func newFollowEnv(t *testing.T) *followEnv {
	t.Helper()
	e := newInboxEnv(t)
	e.setFlag("follows", true)
	q := e.a.fed.out
	q.manual, q.now, q.built, q.jitter = true, e.clock.now, time.Time{}, func() float64 { return 0 }
	q.mu.Lock()
	q.floor = time.Time{}
	q.mu.Unlock()
	return &followEnv{inboxEnv: e, out: q}
}

// member makes a member who shares their profile beyond this server, with their key made.
func (e *followEnv) member(name string, locked bool) (int64, string) {
	e.t.Helper()
	id, tok := e.user(name, "user")
	e.must(200, tok, "PUT", "/api/me", map[string]any{"federates": true, "locked": locked})
	if _, err := e.a.fed.ensureKey(context.Background(), keyOwner(id)); err != nil {
		e.t.Fatal(err)
	}
	return id, tok
}

// deliver sends body signed by sign to the shared inbox, and lets the inbox's worker handle every row stored.
func (e *followEnv) deliver(body []byte, sign signer) inboxReason {
	e.t.Helper()
	e.clock.add(time.Second) // a new Date, so a repeat of the same body is not a replayed signature
	e.post("/inbox", body, sign, nil)
	why := e.last()
	for {
		worked, err := e.q.step(context.Background())
		if err != nil {
			e.t.Fatal(err)
		}
		if !worked {
			return why
		}
	}
}

// outcome is how the inbox's worker last finished a row.
func (e *followEnv) outcome() string {
	var s string
	if err := e.a.db.QueryRow(`SELECT state || ':' || COALESCE(outcome, '') FROM ap_inbox ORDER BY id DESC LIMIT 1`).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

// sentAct is one activity enqueued: its type, actor and id, its object (an id, or the embedded activity's type,
// actor, object and id), and the inbox it was enqueued for.
type sentAct struct {
	Type, Actor, ID, Inbox, Owner string
	Object                        string // the object's id when it is one
	Inner                         map[string]any
}

// sent is what was enqueued since the last call, oldest first.
func (e *followEnv) sent() []sentAct {
	e.t.Helper()
	rows, err := e.a.db.Query(`SELECT o.id, o.body, d.inbox, COALESCE(u.username, '') FROM ap_outbox o JOIN ap_deliveries d ON d.outbox_id = o.id
		LEFT JOIN users u ON u.id = o.actor_id WHERE o.id > $1 ORDER BY o.id`, e.seen)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []sentAct
	for rows.Next() {
		var body string
		var s sentAct
		if err := rows.Scan(&e.seen, &body, &s.Inbox, &s.Owner); err != nil {
			e.t.Fatal(err)
		}
		var d map[string]any
		if err := json.Unmarshal([]byte(body), &d); err != nil {
			e.t.Fatal(err)
		}
		s.Type, _ = d["type"].(string)
		s.Actor, _ = d["actor"].(string)
		s.ID, _ = d["id"].(string)
		s.Object, _ = d["object"].(string)
		s.Inner, _ = d["object"].(map[string]any)
		if !followKinds[s.Type] {
			continue // a member's profile or post going out (FWR 4, notes_test.go), not a follow's activity
		}
		if d["@context"] != asContext {
			e.t.Errorf("an activity without its context: %s", body)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}

// state is the follow from follower to followee: "", pending or accepted.
func (e *followEnv) state(follower, followee int64) string {
	e.t.Helper()
	var s string
	err := e.a.db.QueryRow(`SELECT state FROM follows WHERE follower_id = $1 AND followee_id = $2`, follower, followee).Scan(&s)
	if err != nil && !strings.Contains(err.Error(), "no rows") {
		e.t.Fatal(err)
	}
	return s
}

func (e *followEnv) remoteID(apID string) int64 {
	e.t.Helper()
	if _, err := e.a.fed.remoteActor(context.Background(), apID); err != nil {
		e.t.Fatal(err)
	}
	var id int64
	if err := e.a.db.QueryRow(`SELECT user_id FROM ap_actors WHERE ap_id = $1`, apID).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// fixture reads a document of testdata/federation/follows, written from documentation (its README).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "federation", "follows", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const evID = "https://social.example/users/eve"

// Every path in and out (FWR 3's DoD): a Follow in, accepted at once or held as a request and approved or
// declined; repeats; an Undo, an Accept and a Reject from the wrong actor or of a Follow that is not ours; our
// Follow out, its Accept in each shape the design names, its Reject and the Undo of its Accept; a block both
// ways; members who do not federate; the caps; 410; the cache's bound; and what the pages show.
func TestDBFollowsAcross(t *testing.T) {
	e := newFollowEnv(t)
	ctx := context.Background()
	anaID, ana := e.member("ana", false)
	cydID, cyd := e.member("cyd", true)
	e.w.put(evID, worldDoc{body: actorDoc(evID, "eve", "Eve", &e.other.PublicKey, nil)})
	eve := e.signer(e.other, evID+"#main-key", sigCavage)
	boInbox := boID + "/inbox"

	// A Follow of ana (the documentation-derived fixture): accepted at once, answered with Accept from ana's key to
	// bo's own inbox (never the shared one), the Follow embedded with the id it came with; ana is told.
	if why := e.deliver(fixture(t, "follow.json"), e.bo()); why != inAccepted {
		t.Fatalf("the Follow: %v", why)
	}
	bo := e.remoteID(boID)
	if e.state(bo, anaID) != "accepted" || e.count(`SELECT count(*) FROM follows WHERE ap_id = 'https://social.example/0b5b2a4e-6c3f-4d2a-9b1e-7d8c1f2a3b4c'`) != 1 {
		t.Fatalf("bo's follow of ana: %q (%s)", e.state(bo, anaID), e.outcome())
	}
	s := e.sent()
	if len(s) != 1 || s[0].Type != "Accept" || s[0].Actor != "https://here.example/users/ana" || s[0].Inbox != boInbox || s[0].Owner != "ana" ||
		s[0].Inner["type"] != "Follow" || s[0].Inner["id"] != "https://social.example/0b5b2a4e-6c3f-4d2a-9b1e-7d8c1f2a3b4c" || s[0].Inner["actor"] != boID ||
		!strings.HasPrefix(s[0].ID, "https://here.example/users/ana/activities/") {
		t.Errorf("the Accept: %+v", s)
	}
	if k := e.kinds(anaID); len(k) != 1 || k[0] != "follow" {
		t.Errorf("ana's notifications: %v", k)
	}
	// The same delivery again: a repeat by id, stored once, answered once.
	if why := e.deliver(fixture(t, "follow.json"), e.bo()); why != inDuplicate || len(e.sent()) != 0 {
		t.Errorf("the same Follow again: %v", why)
	}
	// Another Follow while accepted, within the hour: one row still, taken and not answered again (each answer is a
	// row to send: at most one an hour for a pair, followAnswerEvery). An hour later the Accept is sent again (6.3:
	// the remote may have lost it), with no second notification.
	e.deliver(activity(boID, "Follow", "https://here.example/users/ana", nil), e.bo())
	if e.count(`SELECT count(*) FROM follows WHERE follower_id = $1`, bo) != 1 || len(e.sent()) != 0 || !strings.Contains(e.outcome(), "within the hour") {
		t.Errorf("a repeat Follow within the hour: %d rows (%s)", e.count(`SELECT count(*) FROM follows WHERE follower_id = $1`, bo), e.outcome())
	}
	e.clock.add(followAnswerEvery)
	e.deliver(activity(boID, "Follow", "https://here.example/users/ana", nil), e.bo())
	if e.count(`SELECT count(*) FROM follows WHERE follower_id = $1`, bo) != 1 || len(e.sent()) != 1 || len(e.kinds(anaID)) != 1 {
		t.Errorf("a repeat Follow an hour later: %d rows, notifications %v (%s)", e.count(`SELECT count(*) FROM follows WHERE follower_id = $1`, bo), e.kinds(anaID), e.outcome())
	}

	// The wrong actor: eve takes back bo's Follow, embedded under bo's name (dropped at the inbox: the Undo and
	// what it undoes have one actor), and by id only (stored, and matched against eve's own follows: none).
	if why := e.deliver(activity(evID, "Undo", map[string]any{"id": "https://social.example/0b5b2a4e-6c3f-4d2a-9b1e-7d8c1f2a3b4c", "type": "Follow", "actor": boID, "object": "https://here.example/users/ana"}, nil), eve); why != inDropped {
		t.Errorf("eve's Undo of bo's Follow, embedded: %v", why)
	}
	var boFollow string
	if err := e.a.db.QueryRow(`SELECT ap_id FROM follows WHERE follower_id = $1`, bo).Scan(&boFollow); err != nil {
		t.Fatal(err)
	}
	e.deliver(activity(evID, "Undo", boFollow, nil), eve)
	// A forged Undo signed by eve that names bo as its actor is a forward (7.3), never acted on.
	if why := e.deliver(fixture(t, "undo-follow.json"), eve); why != inForward {
		t.Errorf("bo's Undo signed by eve: %v", why)
	}
	if e.state(bo, anaID) != "accepted" {
		t.Fatal("someone other than bo ended bo's follow")
	}
	// bo's own Undo (the fixture) ends it, and a repeat changes nothing.
	e.deliver(fixture(t, "undo-follow.json"), e.bo())
	if e.state(bo, anaID) != "" {
		t.Errorf("bo's Undo: %q (%s)", e.state(bo, anaID), e.outcome())
	}

	// Approval by hand: cyd's actor says so, bo's Follow waits, cyd is told, nothing is answered yet.
	var doc map[string]any
	json.Unmarshal([]byte(e.visitBody("/users/cyd")), &doc)
	if doc["manuallyApprovesFollowers"] != true {
		t.Errorf("cyd's actor: manuallyApprovesFollowers %v", doc["manuallyApprovesFollowers"])
	}
	e.deliver(activity(boID, "Follow", "https://here.example/users/cyd", nil), e.bo())
	if e.state(bo, cydID) != "pending" || len(e.sent()) != 0 {
		t.Fatalf("a Follow of cyd: %q (%s)", e.state(bo, cydID), e.outcome())
	}
	if k := e.kinds(cydID); len(k) != 1 || k[0] != "follow_request" {
		t.Errorf("cyd's notifications: %v", k)
	}
	reqs := e.must(200, cyd, "GET", "/api/fed/requests", nil)["requests"].([]any)
	if len(reqs) != 1 || reqs[0].(map[string]any)["user"].(map[string]any)["remote"].(map[string]any)["handle"] != "bo@social.example" {
		t.Errorf("cyd's requests: %v", reqs)
	}
	// A pending request is in no count and no list (F10's counts, #108's rule).
	if l := e.must(200, cyd, "GET", "/api/followers", nil); l["total"] != 0.0 || len(l["followers"].([]any)) != 0 {
		t.Errorf("cyd's followers while bo waits: %v", l["_raw"])
	}
	// Only cyd answers cyd's requests: ana gets the 404 of a request that does not wait for her.
	e.must(404, ana, "PUT", fmt.Sprintf("/api/fed/requests/%d", bo), nil)
	e.must(200, cyd, "PUT", fmt.Sprintf("/api/fed/requests/%d", bo), nil)
	s = e.sent()
	if e.state(bo, cydID) != "accepted" || len(s) != 1 || s[0].Type != "Accept" || s[0].Owner != "cyd" || s[0].Inbox != boInbox || len(e.kinds(cydID)) != 0 {
		t.Errorf("approving: %q %+v %v", e.state(bo, cydID), s, e.kinds(cydID))
	}
	e.must(404, cyd, "PUT", fmt.Sprintf("/api/fed/requests/%d", bo), nil) // no longer waits
	// Declining: the request goes, with a Reject.
	e.mustExec(`DELETE FROM follows WHERE follower_id = $1`, bo)
	e.deliver(activity(boID, "Follow", "https://here.example/users/cyd", nil), e.bo())
	e.must(200, cyd, "DELETE", fmt.Sprintf("/api/fed/requests/%d", bo), nil)
	if s = e.sent(); e.state(bo, cydID) != "" || len(s) != 1 || s[0].Type != "Reject" || s[0].Inner["actor"] != boID {
		t.Errorf("declining: %q %+v", e.state(bo, cydID), s)
	}
	// A request taken back by bo's Undo takes its notification with it.
	e.deliver(activity(boID, "Follow", "https://here.example/users/cyd", nil), e.bo())
	e.deliver(activity(boID, "Undo", map[string]any{"id": boID + "/f/9", "type": "Follow", "actor": boID, "object": "https://here.example/users/cyd"}, nil), e.bo())
	if e.state(bo, cydID) != "" || len(e.kinds(cydID)) != 0 {
		t.Errorf("an Undo of a request: %q %v", e.state(bo, cydID), e.kinds(cydID))
	}

	// Members who do not federate: one who never shared (no key) is not answered; one who shared and stopped is
	// answered with Reject from their key; a name nobody has is not answered; the instance actor answers Reject.
	eliID, _ := e.user("eli", "user")
	fayID, fay := e.member("fay", false)
	e.must(200, fay, "PUT", "/api/me", map[string]any{"federates": false})
	for _, c := range []struct {
		object, want, owner string
		follower            int64
	}{
		{"https://here.example/users/eli", "", "", eliID},
		{"https://here.example/users/fay", "Reject", "fay", fayID},
		{"https://here.example/users/nobody", "", "", 0},
		{"https://here.example/actor", "Reject", "", 0},
	} {
		e.deliver(activity(boID, "Follow", c.object, nil), e.bo())
		s := e.sent()
		if c.want == "" && len(s) != 0 || c.want != "" && (len(s) != 1 || s[0].Type != c.want || s[0].Owner != c.owner) {
			t.Errorf("a Follow of %s: %+v (%s)", c.object, s, e.outcome())
		}
		if c.follower != 0 && e.state(bo, c.follower) != "" {
			t.Errorf("a Follow of %s was written", c.object)
		}
	}
	if s := e.count(`SELECT count(*) FROM ap_outbox WHERE body LIKE '%"actor":"https://here.example/actor"%'`); s != 1 {
		t.Errorf("the instance actor's Reject: %d", s)
	}

	// Our Follow out: ana follows bo, found by handle (the cache holds bo). It waits as "requested", and the
	// Follow goes to bo's own inbox, signed as ana.
	out := e.must(200, ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil)
	s = e.sent()
	if out["view"].(map[string]any)["follow"] != "requested" || e.state(anaID, bo) != "pending" || len(s) != 1 || s[0].Type != "Follow" ||
		s[0].Object != boID || s[0].Inbox != boInbox || s[0].Owner != "ana" {
		t.Fatalf("ana's Follow: %v %+v", out["_raw"], s)
	}
	followID := s[0].ID
	if v := e.must(200, ana, "GET", fmt.Sprintf("/api/fed/accounts/%d", bo), nil)["view"].(map[string]any); v["follow"] != "requested" {
		t.Errorf("bo's profile here while ana's request waits: %v", v)
	}
	// Pressing Follow again sends nothing more.
	e.must(200, ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil)
	if len(e.sent()) != 0 {
		t.Error("a second Follow was sent")
	}
	// Accepts that are not bo's answer to ana's Follow change nothing: from eve, for a Follow that names bo; an
	// embedded Follow by an actor that is not ours (dropped at the inbox); and bo's, by an id we never sent.
	e.deliver(activity(evID, "Accept", map[string]any{"id": followID, "type": "Follow", "actor": "https://here.example/users/ana", "object": boID}, nil), eve)
	e.deliver(activity(evID, "Accept", followID, nil), eve)
	if why := e.deliver(activity(boID, "Accept", map[string]any{"id": "https://elsewhere.example/f/1", "type": "Follow", "actor": "https://elsewhere.example/users/x", "object": boID}, nil), e.bo()); why != inDropped {
		t.Errorf("an Accept of a Follow that is not ours: %v", why)
	}
	e.deliver(activity(boID, "Accept", "https://here.example/users/ana/activities/nothing", nil), e.bo())
	// bo's Accept of a Follow by ana that names someone else (eve): refused, as the Follow is not of bo.
	e.deliver(activity(boID, "Accept", map[string]any{"type": "Follow", "actor": "https://here.example/users/ana", "object": evID}, nil), e.bo())
	if e.state(anaID, bo) != "pending" {
		t.Fatal("an Accept from someone other than bo, or of another Follow, was taken")
	}
	// bo's Accept in each shape: with an id of its server's own (Mastodon may send one), and with none.
	for _, name := range []string{"accept-unknown-id.json", "accept-no-id.json"} {
		e.mustExec(`UPDATE follows SET state = 'pending' WHERE follower_id = $1`, anaID)
		if why := e.deliver(fixture(t, name), e.bo()); why != inAccepted || e.state(anaID, bo) != "accepted" {
			t.Errorf("%s: %v %q (%s)", name, why, e.state(anaID, bo), e.outcome())
		}
	}
	// By the id we sent alone, and embedded with it (the row takes the fixtures' id).
	e.mustExec(`UPDATE follows SET ap_id = 'https://here.example/users/ana/activities/follow1' WHERE follower_id = $1`, anaID)
	for _, name := range []string{"accept-by-id.json", "accept-embedded.json"} {
		e.mustExec(`UPDATE follows SET state = 'pending' WHERE follower_id = $1`, anaID)
		if why := e.deliver(fixture(t, name), e.bo()); why != inAccepted || e.state(anaID, bo) != "accepted" {
			t.Errorf("%s: %v %q (%s)", name, why, e.state(anaID, bo), e.outcome())
		}
	}
	if v := e.must(200, ana, "GET", fmt.Sprintf("/api/fed/accounts/%d", bo), nil)["view"].(map[string]any); v["follow"] != "following" {
		t.Errorf("after the Accept: %v", v)
	}
	// An Accept of a follow already accepted is ignored.
	e.deliver(activity(boID, "Accept", "https://here.example/users/ana/activities/follow1", nil), e.bo())
	if !strings.Contains(e.outcome(), "no follow of ours waits") {
		t.Errorf("a repeat Accept: %s", e.outcome())
	}
	// bo takes its Accept back (Undo of Accept, the fixture): read as a Reject; ana is told once.
	e.deliver(fixture(t, "undo-accept.json"), e.bo())
	if e.state(anaID, bo) != "" || e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'follow_rejected'`, anaID) != 1 {
		t.Errorf("the Undo of bo's Accept: %q %v", e.state(anaID, bo), e.kinds(anaID))
	}
	// A Reject of our Follow, while it waits, removes it (the fixture, embedded).
	e.must(200, ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil)
	e.sent()
	e.mustExec(`UPDATE follows SET ap_id = 'https://here.example/users/ana/activities/follow1' WHERE follower_id = $1`, anaID)
	e.deliver(fixture(t, "reject-embedded.json"), e.bo())
	if e.state(anaID, bo) != "" {
		t.Errorf("bo's Reject: %q", e.state(anaID, bo))
	}
	// Unfollow sends the Undo of the Follow, embedded with its id.
	e.must(200, ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil)
	followID = e.sent()[0].ID
	e.must(200, ana, "DELETE", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil)
	if s = e.sent(); e.state(anaID, bo) != "" || len(s) != 1 || s[0].Type != "Undo" || s[0].Inner["id"] != followID || s[0].Inner["type"] != "Follow" || s[0].Inbox != boInbox {
		t.Errorf("the Unfollow: %+v", s)
	}

	// A block: ana blocks bo while each follows the other. Both follows go in the block's statement; bo's is
	// answered with Reject, ana's with Undo; no Block is sent. bo's next Follow is refused with Reject.
	e.deliver(activity(boID, "Follow", "https://here.example/users/ana", nil), e.bo())
	e.must(200, ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil)
	e.sent()
	e.must(200, ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/block", bo), nil)
	types := map[string]int{}
	for _, a := range e.sent() {
		types[a.Type]++
	}
	if e.state(bo, anaID) != "" || e.state(anaID, bo) != "" || types["Reject"] != 1 || types["Undo"] != 1 || types["Block"] != 0 || len(types) != 2 {
		t.Errorf("the block: %q %q %v", e.state(bo, anaID), e.state(anaID, bo), types)
	}
	e.clock.add(followAnswerEvery) // the Accept just above answered this pair within the hour
	e.deliver(activity(boID, "Follow", "https://here.example/users/ana", nil), e.bo())
	if s = e.sent(); e.state(bo, anaID) != "" || len(s) != 1 || s[0].Type != "Reject" {
		t.Errorf("a blocked account's Follow: %q %+v", e.state(bo, anaID), s)
	}
	// Again within the hour: refused, and not answered again.
	e.deliver(activity(boID, "Follow", "https://here.example/users/ana", nil), e.bo())
	if s = e.sent(); e.state(bo, anaID) != "" || len(s) != 0 {
		t.Errorf("a blocked account's Follow again within the hour: %+v", s)
	}
	if code, out := e.do(ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil); code != 403 || !strings.Contains(fmt.Sprint(out["error"]), fedFollowBlocked) {
		t.Errorf("following an account one blocked: %d %v", code, out["_raw"])
	}
	e.must(200, ana, "DELETE", fmt.Sprintf("/api/fed/accounts/%d/block", bo), nil)
	// A Block from bo removes the follows both ways, and tells nobody.
	e.deliver(activity(boID, "Follow", "https://here.example/users/ana", nil), e.bo())
	e.must(200, ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil)
	before := len(e.kinds(anaID))
	e.deliver(activity(boID, "Block", "https://here.example/users/ana", nil), e.bo())
	if e.state(bo, anaID) != "" || e.state(anaID, bo) != "" || len(e.kinds(anaID)) > before {
		t.Errorf("bo's Block: %q %q", e.state(bo, anaID), e.state(anaID, bo))
	}
	e.sent()

	// A member who does not share their profile cannot follow out, and is told why; a server not on the allowlist
	// cannot be followed, and the member is told why, before anything is written or queued.
	_, gil := e.user("gil", "user")
	if code, out := e.do(gil, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil); code != 403 || out["error"] != fedFollowOptIn {
		t.Errorf("a member who does not federate: %d %v", code, out["_raw"])
	}
	e.mustExec(`DELETE FROM domain_policies`)
	e.allow("other.example")
	if code, out := e.do(ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", bo), nil); code != 403 || out["error"] != lookupReasons[lookupNotAllowed].words {
		t.Errorf("a server not allowed: %d %v", code, out["_raw"])
	}
	e.allow("social.example")
	// An actor whose inbox is on another server cannot be followed, and nothing is queued (the queue's rule that a
	// delivery meets only the actor's own server).
	odd := "https://social.example/users/odd"
	e.w.put(odd, worldDoc{body: actorDoc(odd, "odd", "Odd", &e.other.PublicKey, map[string]any{"inbox": "https://other.example/inbox"})})
	oddID := e.remoteID(odd)
	if code, out := e.do(ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", oddID), nil); code != 403 || out["error"] != fedFollowForeign || len(e.sent()) != 0 || e.state(anaID, oddID) != "" {
		t.Errorf("an actor whose inbox is elsewhere: %d %v", code, out["_raw"])
	}
	if e.count(`SELECT count(*) FROM ap_deliveries WHERE host = 'other.example'`) != 0 {
		t.Error("a delivery was queued for another server than the actor's")
	}
	// A local id is not a remote account: the one 404.
	e.must(404, ana, "GET", fmt.Sprintf("/api/fed/accounts/%d", cydID), nil)
	e.must(404, ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", cydID), nil)

	// The caps: a member's requests, a host's requests, a host's follows. Past each a Follow is dropped and not
	// answered.
	mkRemote := func(host string, n int) {
		e.mustExec(`INSERT INTO users (username, display_name, pw_hash, role, domain) SELECT 'r' || g || '@' || $1, '', '!', 'user', $1 FROM generate_series(1, $2) g`, host, n)
	}
	mkRemote("other.example", followRequestsMax)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) SELECT id, $1, 'pending' FROM users WHERE domain = 'other.example'`, cydID)
	e.deliver(activity(boID, "Follow", "https://here.example/users/cyd", nil), e.bo())
	if e.state(bo, cydID) != "" || len(e.sent()) != 0 || !strings.Contains(e.outcome(), "the most requests waiting") {
		t.Errorf("past a member's %d requests: %q (%s)", followRequestsMax, e.state(bo, cydID), e.outcome())
	}
	e.mustExec(`DELETE FROM follows WHERE followee_id = $1`, cydID)
	hal, _ := e.member("hal", true)
	mkRemote("social.example", followRequestsPerHost)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) SELECT id, $1, 'pending' FROM users WHERE domain = 'social.example' AND username LIKE 'r%'`, hal)
	e.deliver(activity(boID, "Follow", "https://here.example/users/cyd", nil), e.bo())
	if e.state(bo, cydID) != "" || !strings.Contains(e.outcome(), "its server has the most requests") {
		t.Errorf("past a host's %d requests: %q (%s)", followRequestsPerHost, e.state(bo, cydID), e.outcome())
	}
	e.mustExec(`DELETE FROM follows WHERE followee_id = $1`, hal)
	e.mustExec(`INSERT INTO users (username, display_name, pw_hash, role, domain) SELECT 's' || g || '@social.example', '', '!', 'user', 'social.example' FROM generate_series(1, $1) g`, followsPerHost)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id) SELECT id, $1 FROM users WHERE domain = 'social.example' AND username LIKE 's%'`, hal)
	e.deliver(activity(boID, "Follow", "https://here.example/users/ana", nil), e.bo())
	if e.state(bo, anaID) != "" || len(e.sent()) != 0 || !strings.Contains(e.outcome(), "the most follows") {
		t.Errorf("past a host's %d follows: %q (%s)", followsPerHost, e.state(bo, anaID), e.outcome())
	}
	e.mustExec(`DELETE FROM follows WHERE followee_id = $1`, hal)
	e.mustExec(`DELETE FROM users WHERE domain <> '' AND username ~ '^[rs][0-9]+@'`)

	// The lists and counts: bo follows ana. Ana's followers list shows bo with the handle the server proved it
	// could show; the public count counts bo (an account on another server is public on its side, F10 and #108's
	// rule); a pending follow is in neither.
	e.deliver(activity(boID, "Follow", "https://here.example/users/ana", nil), e.bo())
	list := e.must(200, ana, "GET", "/api/followers", nil)
	f0 := list["followers"].([]any)[0].(map[string]any)["user"].(map[string]any)
	if list["total"] != 1.0 || f0["remote"].(map[string]any)["handle"] != "bo@social.example" || f0["remote"].(map[string]any)["host"] != "social.example" {
		t.Errorf("ana's followers: %v", list["_raw"])
	}
	var followers, following int
	if err := e.a.db.QueryRow(publicFollowCountsSQL, anaID, false).Scan(&followers, &following); err != nil || followers != 1 {
		t.Errorf("ana's public counts: %d %d %v", followers, following, err)
	}
	// The handle is shown only when its name may be: a preferredUsername that turns the text's direction gives
	// no handle, and the page shows the host alone.
	rlo := "https://social.example/users/rlo"
	e.w.put(rlo, worldDoc{body: actorDoc(rlo, "evil\u202eexe", "", &e.other.PublicKey, nil)})
	rloID := e.remoteID(rlo)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id) VALUES ($1, $2)`, rloID, anaID)
	for _, it := range e.must(200, ana, "GET", "/api/followers", nil)["followers"].([]any) {
		u := it.(map[string]any)["user"].(map[string]any)
		if r, _ := u["remote"].(map[string]any); r != nil && r["id"] == float64(rloID) && (r["handle"] != nil || strings.ContainsRune(fmt.Sprint(u["displayName"]), '\u202e')) {
			t.Errorf("a name that turns the text's direction is shown: %v", u)
		}
	}
	acc := e.must(200, ana, "GET", fmt.Sprintf("/api/fed/accounts/%d", rloID), nil)["account"].(map[string]any)
	if acc["handle"] != nil || acc["host"] != "social.example" {
		t.Errorf("the remote profile of that account: %v", acc)
	}

	// The cache's bound never removes an account a follow points at, either way.
	n, err := e.a.db.ExecContext(ctx, evictActorsSQL, remoteActorsMax, 0)
	if err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM ap_actors WHERE user_id IN ($1, $2)`, bo, rloID) != 2 {
		gone, _ := n.RowsAffected()
		t.Errorf("the bound removed a followed actor (%d removed)", gone)
	}

	// 410 at bo's own inbox: bo's follows of local members go, and its requests with them.
	e.deliver(activity(boID, "Follow", "https://here.example/users/cyd", nil), e.bo())
	e.a.fed.goneFollower(ctx, boInbox)
	if e.count(`SELECT count(*) FROM follows WHERE follower_id = $1`, bo) != 0 || e.count(`SELECT count(*) FROM notifications WHERE actor_id = $1 AND kind = 'follow_request'`, bo) != 0 {
		t.Error("a 410 at bo's inbox left bo's follows")
	}
	// The delivery queue calls it on a 410 (startFederation sets the hook).
	if e.out.goneInbox == nil {
		t.Error("the delivery queue's goneInbox is not set")
	}
}

// visitBody is the actor document a server would read at path.
func (e *followEnv) visitBody(path string) string {
	_, body, _ := e.visit("GET", path, "", 1, map[string]string{"Accept": "application/activity+json"})
	return body
}

// /api/me and PUT /api/me say and take locked only in a mode that federates (as federates since #106); in local
// mode the field is ignored and the key set is as before (TestDBLocalModeUnchanged pins the set).
func TestDBLockedOnlyFederating(t *testing.T) {
	e := newEnv(t)
	id, tok := e.user("ana", "user")
	out := e.must(200, tok, "PUT", "/api/me", map[string]any{"locked": true, "bio": "hi"})
	if _, ok := out["federation"]; ok || e.count(`SELECT count(*) FROM users WHERE id = $1 AND locked`, id) != 0 {
		t.Errorf("local mode took locked: %v", out["_raw"])
	}
	if _, ok := e.must(200, tok, "GET", "/api/me", nil)["federation"]; ok {
		t.Error("local mode's /api/me names federation")
	}
	f := newFollowEnv(t)
	_, fa := f.member("ana", false)
	if v := f.must(200, fa, "PUT", "/api/me", map[string]any{"locked": true})["federation"].(map[string]any); v["locked"] != true {
		t.Errorf("federated mode's PUT /api/me: %v", v)
	}
	if v := f.must(200, fa, "GET", "/api/me", nil)["federation"].(map[string]any); v["locked"] != true {
		t.Errorf("federated mode's /api/me: %v", v)
	}
	f.must(400, fa, "PUT", "/api/me", map[string]any{"locked": "yes"})
}

// Two Flowa instances over the real fetcher, signer, inbox and delivery queue (FWR 3; FWR 8's container suite,
// TestTwoInstances of the DoD, is item 8's): cyd on here.example follows ana on b.example, accepted at once,
// then unfollows; ana approves by hand and cyd's next Follow waits for her Approve; and ana follows cyd back and
// unfollows. Each step is one worker step on each side, on the test's clocks.
func TestDBTwoFlowaFollow(t *testing.T) {
	e := newFollowEnv(t)
	be := newEnv(t)
	if err := be.fedStart(map[string]string{"FLOWA_ORIGIN": "https://b.example"}); err != nil {
		t.Fatal(err)
	}
	be.setFlag(fedFlag, true)
	be.setFlag("follows", true)
	be.mustExec(`INSERT INTO instance_settings (key, value) VALUES ('federation_policy', '"open"') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	bf := be.a.fed
	if err := bf.loadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	anaID, ana := be.user("ana", "user")
	be.must(200, ana, "PUT", "/api/me", map[string]any{"federates": true})
	for _, o := range []keyOwner{keyOwner(anaID), instanceActor} {
		if _, err := bf.ensureKey(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}
	settleKeys(t, bf)
	e.allow("b.example")
	cydID, cyd := e.member("cyd", false)
	// Each side fetches the other's actors and sends to the other's inboxes through its own real fetcher.
	e.c.fetch = actorStub(t, be.h)
	e.c.fetch.origin = e.a.fed.origin
	e.out.fetch = actorStub(t, be.h)
	e.out.fetch.origin = e.a.fed.origin
	bf.actors.fetch = actorStub(t, e.h)
	bf.actors.fetch.origin = bf.origin
	bf.actors.now, bf.actors.built = e.clock.now, time.Time{}
	bf.verifier.now, bf.verifier.built = e.clock.now, time.Time{}
	bf.inbox.now, bf.inbox.manual = e.clock.now, true
	bq := bf.out
	bq.manual, bq.now, bq.built, bq.jitter = true, e.clock.now, time.Time{}, func() float64 { return 0 }
	bq.mu.Lock()
	bq.floor = time.Time{}
	bq.mu.Unlock()
	bq.fetch = actorStub(t, e.h)
	bq.fetch.origin = bf.origin
	ctx := context.Background()
	// pump steps every worker on both sides until none has work.
	pump := func() {
		t.Helper()
		for i := 0; i < 50; i++ {
			worked := false
			for _, step := range []func(context.Context) (bool, error){e.out.step, e.q.step, bq.step, bf.inbox.step} {
				w, err := step(ctx)
				if err != nil {
					t.Fatal(err)
				}
				worked = worked || w
			}
			if !worked {
				return
			}
		}
		t.Fatal("the workers did not settle")
	}
	anaURL, cydURL := "https://b.example/users/ana", "https://here.example/users/cyd"
	anaHere := e.remoteID(anaURL) // as a lookup by handle leaves it in the cache
	view := func() string {
		return e.must(200, cyd, "GET", fmt.Sprintf("/api/fed/accounts/%d", anaHere), nil)["view"].(map[string]any)["follow"].(string)
	}
	var cydThere int64
	cydAt := func() int64 {
		be.a.db.QueryRow(`SELECT user_id FROM ap_actors WHERE ap_id = $1`, cydURL).Scan(&cydThere)
		return cydThere
	}

	// cyd follows ana: requested until b.example's Accept arrives, then following; ana has cyd as a follower.
	e.must(200, cyd, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", anaHere), nil)
	if view() != "requested" {
		t.Fatalf("before the Accept: %s", view())
	}
	pump()
	if view() != "following" || be.count(`SELECT count(*) FROM follows WHERE follower_id = $1 AND followee_id = $2 AND state = 'accepted'`, cydAt(), anaID) != 1 {
		t.Fatalf("after the Accept: %s; at b.example %d", view(), be.count(`SELECT count(*) FROM follows`))
	}
	// cyd unfollows: the Undo ends the follow at b.example.
	e.must(200, cyd, "DELETE", fmt.Sprintf("/api/fed/accounts/%d/follow", anaHere), nil)
	pump()
	if be.count(`SELECT count(*) FROM follows`) != 0 || view() != "none" {
		t.Errorf("after the unfollow: %d rows at b.example, %s", be.count(`SELECT count(*) FROM follows`), view())
	}

	// Approval by hand: ana turns it on; here.example learns it from ana's actor, which says so.
	be.must(200, ana, "PUT", "/api/me", map[string]any{"locked": true})
	e.must(200, cyd, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", anaHere), nil)
	pump()
	if view() != "requested" || be.count(`SELECT count(*) FROM follows WHERE state = 'pending'`) != 1 || len(be.kinds(anaID)) == 0 {
		t.Fatalf("a request to ana: %s, %v", view(), be.kinds(anaID))
	}
	be.must(200, ana, "PUT", fmt.Sprintf("/api/fed/requests/%d", cydAt()), nil)
	pump()
	if view() != "following" {
		t.Errorf("after ana's Approve: %s", view())
	}

	// The other way: ana follows cyd (who approves at once), and unfollows.
	cydThereID := cydAt()
	be.must(200, ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", cydThereID), nil)
	pump()
	anaFollows := func() string {
		return be.must(200, ana, "GET", fmt.Sprintf("/api/fed/accounts/%d", cydThereID), nil)["view"].(map[string]any)["follow"].(string)
	}
	if anaFollows() != "following" || e.count(`SELECT count(*) FROM follows WHERE follower_id = $1 AND followee_id = $2 AND state = 'accepted'`, anaHere, cydID) != 1 {
		t.Errorf("ana's follow of cyd: %s", anaFollows())
	}
	be.must(200, ana, "DELETE", fmt.Sprintf("/api/fed/accounts/%d/follow", cydThereID), nil)
	pump()
	if e.count(`SELECT count(*) FROM follows WHERE follower_id = $1`, anaHere) != 0 {
		t.Error("ana's unfollow did not reach here.example")
	}
	// Every delivery was sent and verified: nothing was given up on either side.
	if n := e.count(`SELECT count(*) FROM ap_deliveries`) + be.count(`SELECT count(*) FROM ap_deliveries`); n != 0 {
		t.Errorf("%d deliveries left on the two queues", n)
	}
	if n := e.count(`SELECT count(*) FROM ap_inbox WHERE state <> 'done'`) + be.count(`SELECT count(*) FROM ap_inbox WHERE state <> 'done'`); n != 0 {
		t.Errorf("%d inbox rows not handled", n)
	}
}

// The new pages and endpoints on a pool of one connection (TestDBOneConnection's rule): no handler holds a
// connection while it waits for another, the enqueue's included.
func TestDBFollowsOneConnection(t *testing.T) {
	e := newFollowEnv(t)
	_, ana := e.member("ana", true)
	e.deliver(activity(boID, "Follow", "https://here.example/users/ana", nil), e.bo())
	bo := e.remoteID(boID)
	e.a.db.SetMaxOpenConns(1)
	defer e.a.db.SetMaxOpenConns(8)
	within := func(what string, f func()) {
		t.Helper()
		done := make(chan struct{})
		go func() { f(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s did not finish in 10 s on one connection", what)
		}
	}
	for _, c := range []struct{ method, path string }{
		{"GET", fmt.Sprintf("/api/fed/accounts/%d", bo)},
		{"GET", "/api/fed/requests"},
		{"GET", "/api/followers"},
		{"GET", "/api/notifications"},
		{"GET", "/api/blocks"},
		{"GET", "/api/me"},
		{"PUT", fmt.Sprintf("/api/fed/requests/%d", bo)},
		{"PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", bo)},
		{"DELETE", fmt.Sprintf("/api/fed/accounts/%d/follow", bo)},
		{"PUT", fmt.Sprintf("/api/fed/accounts/%d/block", bo)},
		{"DELETE", fmt.Sprintf("/api/fed/accounts/%d/block", bo)},
	} {
		within(c.method+" "+c.path, func() {
			if code, out := e.do(ana, c.method, c.path, map[string]any{}); code != 200 {
				t.Errorf("%s %s: %d %v", c.method, c.path, code, out["_raw"])
			}
		})
	}
	within("a Follow in and its worker", func() {
		e.deliver(activity(boID, "Follow", "https://here.example/users/ana", nil), e.bo())
	})
}
