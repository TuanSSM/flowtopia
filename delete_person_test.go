package main

import (
	"context"
	"crypto/rsa"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
)

/* ---------- account deletion across servers (FWR 7) ---------- */

// deleteUserStatements is what DELETE /api/admin/users/{id} sends to Postgres in local mode: measured with
// TestDBDeleteUserLocalStatements on the commit before FWR 7 (bf4c205) and pinned here, so a deployment that sets
// nothing new deletes an account with the statements it always did (TestDBLocalModeUnchanged's rule).
const deleteUserStatements = 7

// stmtCountingDB is a pool over a driver that counts the statements sent (stmtCounter), on the test's schema.
func stmtCountingDB(t *testing.T, e *tenv) (*sql.DB, *stmtCounter) {
	t.Helper()
	var path string
	if err := e.a.db.QueryRow(`SELECT current_setting('search_path')`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	u := os.Getenv("TEST_DATABASE_URL")
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	c := &stmtCounter{dsn: u + sep + "search_path=" + path}
	db := sql.OpenDB(c)
	t.Cleanup(func() { db.Close() })
	return db, c
}

// Deleting an account in local mode sends Postgres the statements it did before FWR 7, in one transaction, and the
// answer is the one it always was.
func TestDBDeleteUserLocalStatements(t *testing.T) {
	e := newEnv(t)
	_, admin := e.user("root", "admin")
	db, c := stmtCountingDB(t, e)
	was := e.a.db
	e.a.db = db
	defer func() { e.a.db = was }()
	for _, name := range []string{"ana", "bob"} {
		id, _ := e.user(name, "user")
		c.stmts.Store(0)
		c.tx.Store(0)
		out := e.must(200, admin, "DELETE", fmt.Sprintf("/api/admin/users/%d", id), nil)
		if raw := out["_raw"].(string); strings.TrimSpace(raw) != `{"ok":true}` {
			t.Errorf("the answer in local mode: %s", raw)
		}
		t.Logf("DELETE /api/admin/users in local mode: %d statements, %d transactions", c.stmts.Load(), c.tx.Load())
		if n := c.stmts.Load(); n != deleteUserStatements || c.tx.Load() != 1 {
			t.Errorf("deleting an account in local mode sent %d statements in %d transactions, want %d in 1 as before FWR 7", n, c.tx.Load(), deleteUserStatements)
		}
	}
	if e.count(`SELECT count(*) FROM ap_outbox`) != 0 || e.a.fed != nil {
		t.Error("local mode wrote an activity, or has a federation")
	}
}

// personStub stands for the servers a Delete(Person) goes to: it verifies each delivery with the deleted member's
// public key, as a server that kept that key does, and records what it took.
type personStub struct {
	mu    sync.Mutex
	pub   *rsa.PublicKey
	keyID string
	now   func() time.Time
	got   []personGot
	// hold, when set for a host, keeps each request to it until the channel is closed; arrived hears of each such
	// request as it is held, so a test knows a request is in flight.
	hold    map[string]chan struct{}
	arrived chan string
}

type personGot struct {
	host, path, sig string // sig "" when the signature verified with the member's key
	body            map[string]any
}

func (s *personStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	sig := "not verified"
	v, err := newSigVerifier("https://"+r.Host, func(_ context.Context, id string) (*rsa.PublicKey, error) {
		if id == s.keyID {
			return s.pub, nil
		}
		return nil, errors.New("not the member's key")
	})
	if err == nil {
		v.built, v.now = time.Time{}, s.now
		if _, err = v.verifyRequest(r.Context(), r, body); err == nil {
			sig = ""
		} else {
			sig = err.Error()
		}
	}
	var d map[string]any
	json.Unmarshal(body, &d)
	host := strings.ToLower(r.Host)
	s.mu.Lock()
	s.got = append(s.got, personGot{host: host, path: r.URL.Path, sig: sig, body: d})
	ch := s.hold[host]
	s.mu.Unlock()
	if ch != nil {
		s.arrived <- host
		select {
		case <-ch:
		case <-r.Context().Done():
			return
		}
	}
	w.WriteHeader(202)
}

func (s *personStub) taken() []personGot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.got)
}

// sendAll runs the delivery queue's steps until nothing is left to claim.
func (e *notesEnv) sendAll() {
	e.t.Helper()
	for i := 0; ; i++ {
		worked, err := e.out.step(context.Background())
		if err != nil {
			e.t.Fatal(err)
		}
		if !worked {
			return
		}
		if i > 200 {
			e.t.Fatal("the queue does not settle")
		}
	}
}

// personDeletes are the Delete(Person) rows of the queue: their activity, the id they are signed as, and the inboxes
// of their deliveries still queued.
type personDelete struct {
	body    map[string]any
	signer  sql.NullInt64
	keyID   string
	sealed  bool
	actor   sql.NullInt64
	inboxes []string
}

func (e *notesEnv) personDeletes() []personDelete {
	e.t.Helper()
	rows, err := e.a.db.Query(`SELECT o.body, o.signer, COALESCE(o.signer_key, ''), o.signer_sealed IS NOT NULL, o.actor_id,
		COALESCE(array_agg(d.inbox ORDER BY d.inbox) FILTER (WHERE d.state = 'queued'), '{}')
		FROM ap_outbox o LEFT JOIN ap_deliveries d ON d.outbox_id = o.id WHERE o.body LIKE '%"type":"Delete"%' AND o.signer IS NOT NULL GROUP BY o.id ORDER BY o.id`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []personDelete
	for rows.Next() {
		var p personDelete
		var body string
		if err := rows.Scan(&body, &p.signer, &p.keyID, &p.sealed, &p.actor, pq.Array(&p.inboxes)); err != nil {
			e.t.Fatal(err)
		}
		json.Unmarshal([]byte(body), &p.body)
		out = append(out, p)
	}
	return out
}

// TestDBDeletePerson is FWR 7's definition of done, both directions.
//
// Out: an admin deletes a member who shares their profile. In the deletion's own transaction one Delete(Person) is
// written, signed with the member's key after the account and its key are gone (the row's copy), addressed once to
// each server that may know the account (followers, a pending follower, an account the member follows, an account
// that liked the member's post, an inbox a post went to; not a gone account, not a suspended domain, not a member
// here), and nothing the member had waiting goes out after it. The servers verify it with the member's key; the
// copy is cleared once no delivery waits; and every public address answers for the deleted name as the design says.
// A member who never shared sends nothing; a full queue refuses the deletion with 503 and loses nothing; a pause
// keeps it waiting; a suspended member's deletion goes too; with federation refused nothing can be sent and the
// answer says so.
//
// In: TestDBDeletePersonIn (an actor's own Delete removes everything of it) and TestDBUnknownDeleteCheap (one from an
// actor never seen opens no connection to anyone and costs one statement).
func TestDBDeletePerson(t *testing.T) {
	e := newNotesEnv(t)
	ctx := context.Background()
	root := e.rootSession()
	anaKey, anaKeyID, err := e.a.fed.privateKey(ctx, keyOwner(e.anaID))
	if err != nil {
		t.Fatal(err)
	}
	bo, _ := e.remote("bo", "h1.fan.example", true)
	cy, _ := e.remote("cy", "h1.fan.example", true)
	dee, _ := e.remote("dee", "h2.fan.example", false)
	eve, _ := e.remote("eve", "h3.fan.example", false)
	fay, _ := e.remote("fay", "h4.fan.example", false)
	gone, _ := e.remote("gone", "h6.fan.example", false)
	bad, _ := e.remote("x", "h7.fan.example", true)
	e.follows(bo)
	e.follows(cy)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'pending')`, dee, e.anaID)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, e.anaID, eve)
	e.follows(gone)
	e.mustExec(`UPDATE ap_actors SET state = 'gone' WHERE user_id = $1`, gone)
	e.follows(bad)
	e.mustExec(`INSERT INTO domain_policies (domain, policy) VALUES ('h7.fan.example', 'suspend')`)
	if err := e.a.fed.loadPolicy(ctx); err != nil {
		t.Fatal(err)
	}
	moID, _ := e.user("mo", "user")
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, moID, e.anaID)
	// A post of ana's that went out (its record names h5's inbox), a like of it by fay, and a Public post whose Create
	// is still queued, and a profile change waiting: none of those goes after the deletion.
	p0 := e.postAs(e.ana, map[string]any{"body": "an older post", "audience": "local"})
	e.mustExec(`INSERT INTO likes (post_id, user_id, ap_id) VALUES ($1, $2, 'https://h4.fan.example/likes/1')`, p0, fay)
	e.mustExec(`INSERT INTO ap_post_out (post_id, user_id, state, targets) VALUES (999999, $1, 'sent', $2)`, e.anaID,
		`[{"Actor":"https://h5.fan.example/users/gus","Inbox":"https://h5.fan.example/users/gus/inbox","SharedInbox":"https://h5.fan.example/inbox"}]`)
	e.postAs(e.ana, map[string]any{"body": "still on its way", "audience": "public"})
	e.produce()
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"bio": "a change that waits"})
	if e.count(`SELECT count(*) FROM ap_outbox WHERE actor_id = $1`, e.anaID) != 1 || e.count(`SELECT count(*) FROM ap_person_out WHERE user_id = $1 AND due_at IS NOT NULL`, e.anaID) != 1 {
		t.Fatal("the setup: ana's Create and her profile change do not wait")
	}

	out := e.must(200, root, "DELETE", fmt.Sprintf("/api/admin/users/%d", e.anaID), nil)
	if s, _ := out["federation"].(string); !strings.Contains(s, "5 of them") {
		t.Errorf("the admin is told: %v", out["_raw"])
	}
	want := []string{"https://h1.fan.example/inbox", "https://h2.fan.example/users/dee/inbox", "https://h3.fan.example/users/eve/inbox",
		"https://h4.fan.example/users/fay/inbox", "https://h5.fan.example/inbox"}
	pd := e.personDeletes()
	if len(pd) != 1 {
		t.Fatalf("%d Delete(Person) activities, want 1", len(pd))
	}
	d := pd[0]
	actor := "https://here.example/users/ana"
	if d.body["type"] != "Delete" || d.body["actor"] != actor || d.body["object"] != actor || !strings.HasPrefix(d.body["id"].(string), actor+"/activities/") ||
		fmt.Sprint(d.body["to"]) != "["+publicCollection+"]" || d.body["@context"] != asContext {
		t.Errorf("the activity: %v", d.body)
	}
	if d.actor.Valid || d.signer.Int64 != e.anaID || d.keyID != anaKeyID || !d.sealed {
		t.Errorf("signed as: actor %v, signer %v, key %q, sealed %v", d.actor, d.signer, d.keyID, d.sealed)
	}
	if !slices.Equal(d.inboxes, want) {
		t.Errorf("addressed to %v, want %v", d.inboxes, want)
	}
	for what, n := range map[string]int{
		"activities of ana's left":   e.count(`SELECT count(*) FROM ap_outbox WHERE actor_id = $1`, e.anaID),
		"records of posts left":      e.count(`SELECT count(*) FROM ap_post_out WHERE user_id = $1`, e.anaID),
		"profile updates left":       e.count(`SELECT count(*) FROM ap_person_out WHERE user_id = $1`, e.anaID),
		"keys left":                  e.count(`SELECT count(*) FROM ap_keys WHERE user_id = $1`, e.anaID),
		"deliveries besides the one": e.count(`SELECT count(*) FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.signer IS NULL`),
	} {
		if n != 0 {
			t.Errorf("%s: %d", what, n)
		}
	}
	if e.count(`SELECT count(*) FROM retired_usernames WHERE username = 'ana'`) != 1 {
		t.Error("ana's name is not retired")
	}
	if _, _, err := e.a.fed.privateKey(ctx, keyOwner(e.anaID)); !errors.Is(err, errNoKey) {
		t.Errorf("ana's key is still held in memory: %v", err)
	}

	// Sent: every server verifies the Delete with ana's key as it kept it, once each; then the copy is cleared.
	stub := &personStub{pub: &anaKey.PublicKey, keyID: e.a.fed.origin.url + anaKeyID, now: e.clock.now}
	e.out.fetch = deliveryStub(t, stub)
	e.out.fetch.origin = e.a.fed.origin
	e.out.pace.wait = func(ctx context.Context, d time.Duration) error { e.clock.add(d); return ctx.Err() }
	e.sendAll()
	got := stub.taken()
	var where []string
	for _, g := range got {
		where = append(where, "https://"+g.host+g.path)
		if g.sig != "" || g.body["type"] != "Delete" || g.body["object"] != actor {
			t.Errorf("a delivery to %s%s: signature %q, %v", g.host, g.path, g.sig, g.body)
		}
	}
	slices.Sort(where)
	if !slices.Equal(where, want) {
		t.Errorf("delivered to %v, want %v", where, want)
	}
	if _, _, err := e.out.housekeep(ctx); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM ap_outbox WHERE signer_sealed IS NOT NULL`) != 0 {
		t.Error("the copy of ana's key is kept after every delivery ended")
	}

	// What every public address answers for the deleted name: the actor 410 with a Tombstone (and a page that says so),
	// everything else the one 404 of a name nobody shows.
	for _, c := range []struct {
		path, accept string
		code         int
	}{
		{"/users/ana", apAccept, 410}, {"/users/ana", "text/html", 410},
		{wf("acct:ana@here.example"), "", 404},
		{"/users/ana/outbox", apAccept, 404}, {"/users/ana/followers", apAccept, 404}, {"/users/ana/following", apAccept, 404},
		{"/users/ana/featured", apAccept, 404}, {fmt.Sprintf("/users/ana/posts/%d", p0), apAccept, 404},
		{fmt.Sprintf("/users/ana/posts/%d", p0), "text/html", 404}, {fmt.Sprintf("/users/ana/posts/%d/activity", p0), apAccept, 404},
		{"/users/ana/avatar.jpg", "", 404}, {fmt.Sprintf("/users/ana/posts/%d/picture.jpg", p0), "", 404},
	} {
		w := pubAsk(e.tenv, "GET", c.path, c.accept)
		if w.Code != c.code {
			t.Errorf("GET %s (%s): %d, want %d", c.path, c.accept, w.Code, c.code)
		}
		if c.code == 410 && c.accept == apAccept && w.Body.String() != `{"@context":"https://www.w3.org/ns/activitystreams","id":"https://here.example/users/ana","type":"Tombstone","formerType":"Person"}` {
			t.Errorf("the Tombstone: %s", w.Body)
		}
		if strings.Contains(w.Body.String(), "a change that waits") || strings.Contains(w.Body.String(), "an older post") {
			t.Errorf("GET %s shows what the deleted member wrote", c.path)
		}
	}

	// A member who never shared their profile: nothing written, the name free again, the answer says why.
	bobID, _ := e.user("bob", "user")
	out = e.must(200, root, "DELETE", fmt.Sprintf("/api/admin/users/%d", bobID), nil)
	if s, _ := out["federation"].(string); !strings.Contains(s, "never shared") || len(e.personDeletes()) != 1 || e.count(`SELECT count(*) FROM retired_usernames WHERE username = 'bob'`) != 0 {
		t.Errorf("a member who never shared: %v", out["_raw"])
	}

	// A full queue: the deletion is refused with 503 and the reason, and nothing is lost: the account is still there.
	cyFollowsCyd := func(id int64) {
		e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, cy, id)
	}
	cydID, _ := e.member("cyd", false)
	cyFollowsCyd(cydID)
	e.mustExec(`INSERT INTO ap_outbox (ap_id, body, targets, created_at) SELECT 'https://here.example/actor/activities/full' || g, '{}', '{https://h1.fan.example/inbox}', now()
		FROM generate_series(1, $1) g`, deliveryWaitingMax)
	if code, o := e.do(root, "DELETE", fmt.Sprintf("/api/admin/users/%d", cydID), nil); code != 503 || !strings.Contains(o["error"].(string), "not deleted") {
		t.Errorf("a full queue: %d %v", code, o["_raw"])
	}
	if e.count(`SELECT count(*) FROM users WHERE id = $1`, cydID) != 1 || e.count(`SELECT count(*) FROM ap_keys WHERE user_id = $1`, cydID) != 1 {
		t.Error("a deletion the queue refused deleted the account or its key")
	}
	e.mustExec(`DELETE FROM ap_outbox WHERE ap_id LIKE 'https://here.example/actor/activities/full%'`)
	e.out.waiting.Store(false)

	// Paused: written in the deletion's transaction, kept while the switch is off, sent once it is on again.
	e.setFlag(fedFlag, false)
	out = e.must(200, root, "DELETE", fmt.Sprintf("/api/admin/users/%d", cydID), nil)
	if s, _ := out["federation"].(string); !strings.Contains(s, "paused") {
		t.Errorf("deleted while paused: %v", out["_raw"])
	}
	before := len(stub.taken())
	e.sendAll()
	if len(stub.taken()) != before {
		t.Error("the queue sent while paused")
	}
	e.setFlag(fedFlag, true)
	e.sendAll()
	if after := stub.taken(); len(after) != before+1 || after[before].body["actor"] != "https://here.example/users/cyd" {
		t.Errorf("after the pause: %d deliveries more", len(after)-before)
	}

	// A suspended member who shared: their deletion goes too (servers that hold the account must still learn it is gone).
	susID, _ := e.member("sus", false)
	cyFollowsCyd(susID)
	e.mustExec(`UPDATE users SET suspended_until = now() + interval '7 days' WHERE id = $1`, susID)
	out = e.must(200, root, "DELETE", fmt.Sprintf("/api/admin/users/%d", susID), nil)
	if s, _ := out["federation"].(string); !strings.Contains(s, "1 of them") {
		t.Errorf("a suspended member: %v", out["_raw"])
	}

	// -rotate-keys while a deletion waits: the kept copy cannot be renewed, so it is cleared, and the delivery is given up
	// at its send rather than signed with anything else.
	e.sendAll()
	rotID, _ := e.member("rot", false)
	cyFollowsCyd(rotID)
	e.must(200, root, "DELETE", fmt.Sprintf("/api/admin/users/%d", rotID), nil)
	if _, _, err := e.a.fed.rotateKeys(ctx); err != nil || e.a.fed.keptCleared < 1 || e.count(`SELECT count(*) FROM ap_outbox WHERE signer_sealed IS NOT NULL`) != 0 {
		t.Errorf("-rotate-keys: %v, %d copies cleared", err, e.a.fed.keptCleared)
	}
	before = len(stub.taken())
	e.sendAll()
	if len(stub.taken()) != before || e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'failed' AND last_error = 'the key of the deleted account is no longer kept'`) != 1 {
		t.Errorf("a deletion whose key was cleared: %d sent", len(stub.taken())-before)
	}

	// Federation refused (a moved origin, a secret that opens no key): nothing can be sent; the account still goes, its
	// name is retired, and the answer and the log say no other server was told.
	logs := logged(t)
	refID, _ := e.member("ref", false)
	cyFollowsCyd(refID)
	n := len(e.personDeletes())
	e.a.fed.refuse("federation refused: a test")
	out = e.must(200, root, "DELETE", fmt.Sprintf("/api/admin/users/%d", refID), nil)
	if s, _ := out["federation"].(string); !strings.Contains(s, "could not be told") || len(e.personDeletes()) != n ||
		e.count(`SELECT count(*) FROM retired_usernames WHERE username = 'ref'`) != 1 || e.count(`SELECT count(*) FROM users WHERE id = $1`, refID) != 0 {
		t.Errorf("deleted while federation is refused: %v", out["_raw"])
	}
	if !strings.Contains(logs.String(), "no other server was told") {
		t.Error("the log does not say the deletion was not sent")
	}
}

// The servers one Delete(Person) goes to are bounded: one inbox per server (its shared inbox first), at most
// personDeleteHostsMax servers, the rest left out and logged.
func TestDBDeletePersonBound(t *testing.T) {
	e := newNotesEnv(t)
	root := e.rootSession()
	logs := logged(t)
	e.mustExec(`WITH u AS (INSERT INTO users (username, pw_hash, domain, display_name)
			SELECT 'f@h' || g || '.example', '!', 'h' || g || '.example', 'f' FROM generate_series(1, $1) g RETURNING id, domain),
		a AS (INSERT INTO ap_actors (user_id, ap_id, kind, inbox, shared_inbox, fetched_at, expires_at)
			SELECT id, 'https://' || domain || '/users/f', 'Person', 'https://' || domain || '/users/f/inbox', 'https://' || domain || '/inbox', now(), now() + interval '1 day' FROM u)
		INSERT INTO follows (follower_id, followee_id, state) SELECT id, $2, 'accepted' FROM u`, personDeleteHostsMax+5, e.anaID)
	// A second account on one server: still one delivery there.
	two, _ := e.remote("g", "h1.example", false)
	e.follows(two)
	out := e.must(200, root, "DELETE", fmt.Sprintf("/api/admin/users/%d", e.anaID), nil)
	pd := e.personDeletes()
	if len(pd) != 1 || len(pd[0].inboxes) != personDeleteHostsMax {
		t.Fatalf("%d activities, %d deliveries; want one to %d servers", len(pd), len(pd[0].inboxes), personDeleteHostsMax)
	}
	hosts := map[string]bool{}
	for _, in := range pd[0].inboxes {
		h := actorHost(in)
		if hosts[h] || !strings.HasSuffix(in, ".example/inbox") {
			t.Errorf("a second delivery to %s, or not its shared inbox: %s", h, in)
		}
		hosts[h] = true
	}
	if s, _ := out["federation"].(string); !strings.Contains(s, "left out") || !strings.Contains(logs.String(), "known to more than 2000 servers") {
		t.Errorf("the cut is not said: %v", out["_raw"])
	}
}

// rootSession signs in the admin a federated start made (ADMIN_USER root).
func (e *tenv) rootSession() string {
	e.t.Helper()
	var id int64
	if err := e.a.db.QueryRow(`SELECT id FROM users WHERE username = 'root'`).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return e.session(id)
}
