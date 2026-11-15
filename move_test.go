package main

// Moving to an account on another server (PRT 2, docs/federation.md "Moving to another server, as built (PRT 2)"):
// the alias check both ways, the Update before the Move at every inbox, the queue's rules for both, the domain
// policy, a member who stops sharing, leaves or is deleted while a move is made, the 30 days, a second move, two at
// once, a target that redirects, is too large or does not answer, the target on this server, and what a moved
// profile can still do. The other server is the stub world of the actor cache (remote_test.go) for the target, and a
// stub of inboxes for the followers' servers.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
)

const (
	movePW    = "flowa-test-only"
	anaActor  = "https://here.example/users/ana"
	novaID    = "https://social.example/users/nova"
	moveNoway = "a password that is not hers"
)

// moveStub stands for the followers' servers: it records each delivery (host, path, the activity's type and id, and
// the status it answered) in the order they came, and answers with answer (202 when nil). n is how many deliveries
// of that type that host had before this one.
type moveStub struct {
	mu     sync.Mutex
	got    []moveGot
	answer func(host, typ string, n int) int
}

type moveGot struct {
	host, path, typ, id string
	status              int
}

func (s *moveStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var act struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	json.Unmarshal(body, &act)
	host := strings.ToLower(r.Host)
	s.mu.Lock()
	n := 0
	for _, g := range s.got {
		if g.host == host && g.typ == act.Type {
			n++
		}
	}
	status := 202
	if s.answer != nil {
		status = s.answer(host, act.Type, n)
	}
	s.got = append(s.got, moveGot{host: host, path: r.URL.Path, typ: act.Type, id: act.ID, status: status})
	s.mu.Unlock()
	w.WriteHeader(status)
}

func (s *moveStub) taken() []moveGot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.got)
}

// sequence is the types each host was sent, in order, each with its status: "Update:503 Update:202 Move:202".
func (s *moveStub) sequence(host string) string {
	var out []string
	for _, g := range s.taken() {
		if g.host == host {
			out = append(out, fmt.Sprintf("%s:%d", g.typ, g.status))
		}
	}
	return strings.Join(out, " ")
}

type moveEnv struct {
	*notesEnv
	stub *moveStub
}

// newMoveEnv is a federated instance with the moves flag on, ana sharing with her password set, followers on two
// servers (bo and cy on h1.fan.example, with its shared inbox; dee on h2.fan.example), and the deliveries sent to
// the stub of inboxes on the test's clock.
func newMoveEnv(t *testing.T) *moveEnv {
	t.Helper()
	e := &moveEnv{notesEnv: newNotesEnv(t), stub: &moveStub{}}
	e.setFlag("moves", true)
	e.a.fed.lookupPause = func(context.Context, time.Duration) {}
	e.mustExec(`UPDATE users SET pw_hash = $2 WHERE id = $1`, e.anaID, hashPassword(movePW))
	for _, f := range []struct{ name, host string }{{"bo", "h1.fan.example"}, {"cy", "h1.fan.example"}, {"dee", "h2.fan.example"}} {
		id, _ := e.remote(f.name, f.host, f.host == "h1.fan.example")
		e.follows(id)
	}
	e.out.fetch = deliveryStub(t, e.stub)
	e.out.fetch.origin = e.a.fed.origin
	e.out.pace.wait = func(ctx context.Context, d time.Duration) error { e.clock.add(d); return ctx.Err() }
	return e
}

// target serves the account nova at social.example (its actor and its WebFinger), naming aka in alsoKnownAs.
func (e *moveEnv) target(id string, aka ...string) {
	e.t.Helper()
	if aka == nil {
		aka = []string{}
	}
	pref := id[strings.LastIndexByte(id, '/')+1:]
	person(e.w, id, pref, map[string]any{"alsoKnownAs": aka})
}

// move asks for ana's move to q with password pw.
func (e *moveEnv) move(q, pw string) (int, map[string]any) {
	e.t.Helper()
	return e.do(e.ana, "POST", "/api/me/move", map[string]any{"target": q, "password": pw})
}

// moveRow is an activity of ana's in the queue: its type, id, the one it comes after, its body and its inboxes.
type moveRow struct {
	typ, id, after string
	body           map[string]any
	inboxes        []string
}

func (e *moveEnv) rows(actor int64) []moveRow {
	e.t.Helper()
	rows, err := e.a.db.Query(`SELECT o.ap_id, COALESCE(o.comes_after, ''), o.body, COALESCE(array_agg(d.inbox ORDER BY d.inbox) FILTER (WHERE d.inbox IS NOT NULL), '{}')
		FROM ap_outbox o LEFT JOIN ap_deliveries d ON d.outbox_id = o.id WHERE o.actor_id = $1 GROUP BY o.id ORDER BY o.id`, actor)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []moveRow
	for rows.Next() {
		var r moveRow
		var body string
		if err := rows.Scan(&r.id, &r.after, &body, pq.Array(&r.inboxes)); err != nil {
			e.t.Fatal(err)
		}
		json.Unmarshal([]byte(body), &r.body)
		r.typ, _ = r.body["type"].(string)
		out = append(out, r)
	}
	return out
}

func (e *moveEnv) movedTo() string {
	e.t.Helper()
	var s string
	if err := e.a.db.QueryRow(`SELECT moved_to FROM users WHERE id = $1`, e.anaID).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

// actorJSON is ana's actor document as another server reads it.
func (e *moveEnv) actorJSON() map[string]any {
	e.t.Helper()
	e.a.fed.dropActor(keyOwner(e.anaID))
	w := pubAsk(e.tenv, "GET", "/users/ana", apAccept)
	if w.Code != 200 {
		e.t.Fatalf("GET /users/ana: %d %s", w.Code, w.Body)
	}
	var d map[string]any
	json.Unmarshal(w.Body.Bytes(), &d)
	return d
}

// contextNames is the terms the third entry of a document's @context defines.
func contextNames(d map[string]any) map[string]any {
	c, _ := d["@context"].([]any)
	if len(c) < 3 {
		return nil
	}
	m, _ := c[2].(map[string]any)
	return m
}

// TestDBMoveOut is PRT 2's definition of done on this side: ana moves to nova on another server.
//
// The move is refused while nova does not name ana in alsoKnownAs, with words that say what to do, and nothing is
// written; with a wrong password, and with nothing typed. Once nova names her, one move writes, in one transaction,
// movedTo and the time on ana's row, an Update(Person) whose actor names movedTo (with its term in the context) and
// a Move (actor and object ana, target nova) to her followers' inboxes, one per server, the Move held behind the
// Update. Members here who follow her and share their profile follow nova and stop following her; one who does not
// share keeps the follow. Her actor, her public page and her profile here say where she went. A moved profile makes
// no Public post, takes no new follower (here or from another server) and follows nobody new; another move waits
// 30 days; taking the redirect back sends an Update without movedTo and keeps the 30 days; after them she may move
// again.
func TestDBMoveOut(t *testing.T) {
	e := newMoveEnv(t)
	moID, mo := e.member("mo", false)
	luID, _ := e.user("lu", "user")
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $3, 'accepted'), ($2, $3, 'accepted')`, moID, luID, e.anaID)
	e.target(novaID)

	// The alias check: nova does not name ana yet. Nothing is written.
	code, out := e.move("@nova@social.example", movePW)
	if code != 422 || out["reason"] != "noAlias" || !strings.Contains(fmt.Sprint(out["error"]), "@ana@here.example") {
		t.Fatalf("a move to an account that does not name ana: %d %v", code, out["_raw"])
	}
	if e.movedTo() != "" || len(e.rows(e.anaID)) != 0 {
		t.Fatal("a refused move wrote something")
	}
	// Nor with a wrong password, or with nothing typed.
	if code, _ := e.move("@nova@social.example", moveNoway); code != 403 {
		t.Errorf("a wrong password: %d", code)
	}
	if code, _ := e.move("", movePW); code != 400 {
		t.Errorf("no account typed: %d", code)
	}

	// nova names ana (and another account, first): the move goes, fetched again although the cache kept nova.
	e.target(novaID, "https://else.example/users/x", anaActor)
	out = e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "nova@social.example", "password": movePW})
	told := fmt.Sprint(out["told"])
	if !strings.Contains(told, "2 servers") || !strings.Contains(told, "1 member here now follows your new account") || !strings.Contains(told, "1 member here could not") {
		t.Errorf("ana is told: %s", told)
	}
	if mv, _ := out["federation"].(map[string]any)["move"].(map[string]any); mv == nil || fmt.Sprint(mv["moved"].(map[string]any)["handle"]) != "@nova@social.example" || mv["next"] == nil {
		t.Errorf("the card after the move: %v", out["federation"])
	}
	if e.movedTo() != novaID {
		t.Errorf("moved_to is %q", e.movedTo())
	}
	rs := e.rows(e.anaID)
	if len(rs) != 2 || rs[0].typ != "Update" || rs[1].typ != "Move" {
		t.Fatalf("ana's activities: %+v", rs)
	}
	want := []string{"https://h1.fan.example/inbox", "https://h2.fan.example/users/dee/inbox"}
	upd, mv := rs[0], rs[1]
	if !slices.Equal(upd.inboxes, want) || !slices.Equal(mv.inboxes, want) {
		t.Errorf("addressed to %v and %v, want %v for both", upd.inboxes, mv.inboxes, want)
	}
	if mv.after != upd.id || upd.after != "" {
		t.Errorf("the Move comes after %q, the Update after %q; want the Update's id %q and none", mv.after, upd.after, upd.id)
	}
	if obj, _ := upd.body["object"].(map[string]any); obj == nil || obj["movedTo"] != novaID || obj["id"] != anaActor || obj["@context"] != nil {
		t.Errorf("the Update's object: %v", upd.body["object"])
	}
	if term, _ := contextNames(upd.body)["movedTo"].(map[string]any); term["@id"] != "as:movedTo" || term["@type"] != "@id" {
		t.Errorf("the Update's context: %v", upd.body["@context"])
	}
	if mv.body["actor"] != anaActor || mv.body["object"] != anaActor || mv.body["target"] != novaID || mv.body["@context"] != asContext ||
		fmt.Sprint(mv.body["to"]) != "["+anaActor+"/followers]" || !strings.HasPrefix(mv.id, anaActor+"/activities/") {
		t.Errorf("the Move: %v", mv.body)
	}

	// The members here: mo (who shares) asked to follow nova, and no longer follows ana; lu keeps her follow.
	var novaUID int64
	if err := e.a.db.QueryRow(`SELECT user_id FROM ap_actors WHERE ap_id = $1`, novaID).Scan(&novaUID); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM follows WHERE follower_id = $1 AND followee_id = $2 AND state = 'pending'`, moID, novaUID) != 1 ||
		e.count(`SELECT count(*) FROM follows WHERE follower_id = $1 AND followee_id = $2`, moID, e.anaID) != 0 ||
		e.count(`SELECT count(*) FROM follows WHERE follower_id = $1 AND followee_id = $2`, luID, e.anaID) != 1 {
		t.Error("the members here who follow ana were not moved as they should be")
	}
	if fs := e.rows(moID); len(fs) != 1 || fs[0].typ != "Follow" || fs[0].body["object"] != novaID || !slices.Equal(fs[0].inboxes, []string{"https://social.example/users/nova/inbox"}) {
		t.Errorf("mo's Follow of nova: %+v", fs)
	}

	// Sent: each server gets the Update and then the Move.
	e.sendAll()
	for _, h := range []string{"h1.fan.example", "h2.fan.example"} {
		if got := e.stub.sequence(h); got != "Update:202 Move:202" {
			t.Errorf("%s was sent %q", h, got)
		}
	}

	// Her actor names movedTo (and no alsoKnownAs: she names no account she moved from), with the term; her public
	// page and her profile here say where she went, to everyone.
	d := e.actorJSON()
	if d["movedTo"] != novaID || d["alsoKnownAs"] != nil || contextNames(d)["movedTo"] == nil {
		t.Errorf("the actor: movedTo %v, alsoKnownAs %v, context %v", d["movedTo"], d["alsoKnownAs"], d["@context"])
	}
	if w := pubAsk(e.tenv, "GET", "/users/ana", "text/html"); !strings.Contains(w.Body.String(), `This profile has moved to <a href="https://social.example/users/nova" rel="nofollow noopener ugc">@nova@social.example</a>.`) {
		t.Errorf("the public page does not say where she went: %s", w.Body)
	}
	if p := e.must(200, mo, "GET", "/api/users/ana", nil); fmt.Sprint(p["moved"].(map[string]any)["handle"]) != "@nova@social.example" {
		t.Errorf("her profile here: %v", p["_raw"])
	}

	// What a moved profile cannot do: a Public post, a new follower here, following someone on another server.
	if code, out := e.do(e.ana, "POST", "/api/posts", map[string]any{"body": "still here", "audience": "public"}); code != 400 || out["error"] != audMoved {
		t.Errorf("a Public post after moving: %d %v", code, out["_raw"])
	}
	e.must(200, e.ana, "POST", "/api/posts", map[string]any{"body": "to this server", "audience": "local"})
	_, kim := e.member("kim", false)
	if code, out := e.do(kim, "PUT", "/api/users/ana/follow", nil); code != 403 || out["error"] != moveTakesNoFollowers {
		t.Errorf("a new follower here: %d %v", code, out["_raw"])
	}
	if code, out := e.do(e.ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", novaUID), nil); code != 403 || out["error"] != moveFollowsNobody {
		t.Errorf("following after moving: %d %v", code, out["_raw"])
	}

	// Another move within 30 days, to nova or anyone, is refused with the date it is possible from.
	if code, out := e.move("@nova@social.example", movePW); code != 409 || !strings.Contains(fmt.Sprint(out["error"]), "30 days after the last one") {
		t.Errorf("a second move: %d %v", code, out["_raw"])
	}

	// Taking the redirect back: the password again, movedTo gone from the actor, an Update(Person) marked due, the 30
	// days still counted.
	if code, _ := e.do(e.ana, "DELETE", "/api/me/move", map[string]any{"password": moveNoway}); code != 403 {
		t.Errorf("taking it back with a wrong password: %d", code)
	}
	e.must(200, e.ana, "DELETE", "/api/me/move", map[string]any{"password": movePW})
	if e.movedTo() != "" || e.count(`SELECT count(*) FROM ap_person_out WHERE user_id = $1 AND due_at IS NOT NULL`, e.anaID) != 1 {
		t.Error("taking the redirect back did not clear it or mark the profile update")
	}
	if d := e.actorJSON(); d["movedTo"] != nil || contextNames(d)["movedTo"] != nil {
		t.Errorf("the actor after taking it back: %v", d)
	}
	if code, _ := e.do(e.ana, "DELETE", "/api/me/move", map[string]any{"password": movePW}); code != 400 {
		t.Errorf("taking back a redirect there is not: %d", code)
	}
	if code, _ := e.move("@nova@social.example", movePW); code != 409 {
		t.Errorf("a move after taking it back, within the 30 days: %d", code)
	}
	e.produce()
	e.sendAll()
	// After 30 days she may move again.
	e.clock.add(moveCooldown + time.Minute)
	if code, out := e.move("@nova@social.example", movePW); code != 200 {
		t.Errorf("a move after 30 days: %d %v", code, out["_raw"])
	}
}

// The alias check, the other way: the accounts a member moves here from. Named by handle (resolved as a search by
// handle), at most aliasesMax, in the actor's alsoKnownAs with its term, sent as an Update(Person); this server's own
// accounts refused; removed again. A remote actor's alsoKnownAs is kept as fetched, bounded.
func TestDBMoveOutAliases(t *testing.T) {
	e := newMoveEnv(t)
	e.target("https://social.example/users/old")
	e.produce()
	out := e.must(200, e.ana, "PUT", "/api/me/aliases", map[string]any{"account": "@old@social.example"})
	if mv := out["federation"].(map[string]any)["move"].(map[string]any); fmt.Sprint(mv["aliases"]) != "[map[handle:@old@social.example id:https://social.example/users/old]]" {
		t.Errorf("the card lists %v", mv["aliases"])
	}
	d := e.actorJSON()
	if fmt.Sprint(d["alsoKnownAs"]) != "[https://social.example/users/old]" || d["movedTo"] != nil || contextNames(d)["alsoKnownAs"] == nil {
		t.Errorf("the actor: %v %v", d["alsoKnownAs"], d["@context"])
	}
	if e.count(`SELECT count(*) FROM ap_person_out WHERE user_id = $1 AND due_at IS NOT NULL`, e.anaID) != 1 {
		t.Error("naming an alias marks no profile update")
	}
	e.produce()
	if rs := e.rows(e.anaID); len(rs) != 1 || rs[0].typ != "Update" || fmt.Sprint(rs[0].body["object"].(map[string]any)["alsoKnownAs"]) != "[https://social.example/users/old]" ||
		contextNames(rs[0].body)["alsoKnownAs"] == nil {
		t.Errorf("the Update(Person): %+v", rs)
	}
	// Twice is once; this server's own account is refused; the bound.
	if out := e.must(200, e.ana, "PUT", "/api/me/aliases", map[string]any{"account": "@old@social.example"}); out["changed"] != false {
		t.Errorf("the same alias twice: %v", out["_raw"])
	}
	e.user("bob", "user")
	if code, out := e.do(e.ana, "PUT", "/api/me/aliases", map[string]any{"account": "@bob@here.example"}); code != 400 || out["error"] != aliasSame {
		t.Errorf("an account of this server: %d %v", code, out["_raw"])
	}
	for i := 1; i < aliasesMax; i++ {
		e.target(fmt.Sprintf("https://social.example/users/old%d", i))
		e.must(200, e.ana, "PUT", "/api/me/aliases", map[string]any{"account": fmt.Sprintf("old%d@social.example", i)})
	}
	if code, out := e.do(e.ana, "PUT", "/api/me/aliases", map[string]any{"account": "@nova@social.example"}); code != 400 || out["error"] != aliasesFull {
		t.Errorf("one alias too many: %d %v", code, out["_raw"])
	}
	e.must(200, e.ana, "DELETE", "/api/me/aliases", map[string]any{"id": "https://social.example/users/old"})
	if code, _ := e.do(e.ana, "DELETE", "/api/me/aliases", map[string]any{"id": "https://social.example/users/old"}); code != 404 {
		t.Errorf("removing one not named: %d", code)
	}
	if n := len(e.actorJSON()["alsoKnownAs"].([]any)); n != aliasesMax-1 {
		t.Errorf("%d aliases left, want %d", n, aliasesMax-1)
	}

	// A remote actor's alsoKnownAs as kept: the first 16, no repeats, none over 2,048 bytes.
	var many []string
	many = append(many, "https://x.example/"+strings.Repeat("a", actorAliasMax), anaActor, anaActor)
	for i := 0; i < 30; i++ {
		many = append(many, fmt.Sprintf("https://x.example/u/%d", i))
	}
	if got := keptAliases(many); len(got) != actorAliasesKept || got[0] != anaActor || slices.Contains(got[1:], anaActor) {
		t.Errorf("kept %d: %v", len(got), got[:3])
	}
	if got := keptAliases(nil); got == nil || len(got) != 0 {
		t.Error("no alias is not an empty list")
	}
}

// The order at every inbox and the queue's rules for both activities. h1 answers the Update 503 twice: the Move is
// not tried while the Update waits, and goes after it. h2 refuses the Update for good (400 in both forms): the Move
// goes after it all the same, as a comment's Update does after its Create given up. h3 is down for the whole
// horizon: both are tried on the schedule and given up, and the Move is never sent before the Update is settled.
func TestDBMoveOutOrder(t *testing.T) {
	e := newMoveEnv(t)
	e3, _ := e.remote("eve", "h3.fan.example", false)
	e.follows(e3)
	e.stub.answer = func(host, typ string, n int) int {
		switch {
		case host == "h1.fan.example" && typ == "Update" && n < 2:
			return 503
		case host == "h2.fan.example" && typ == "Update":
			return 400
		case host == "h3.fan.example":
			return 503
		}
		return 202
	}
	e.target(novaID, anaActor)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	e.sendAll()
	if got := e.stub.sequence("h1.fan.example"); got != "Update:503" {
		t.Errorf("h1 after the first pass: %q", got)
	}
	if got := e.stub.sequence("h2.fan.example"); got != "Update:400 Update:400 Move:202" {
		t.Errorf("h2: %q", got)
	}
	var moveTries int
	if err := e.a.db.QueryRow(`SELECT d.attempts FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.body LIKE '%"type":"Move"%' AND d.host = 'h1.fan.example'`).Scan(&moveTries); err != nil || moveTries != 0 {
		t.Errorf("the Move to h1 was tried %d times while the Update waits (%v)", moveTries, err)
	}
	// The clock moves to each next attempt in turn, as the schedule has it.
	for i := 0; i < 60 && e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'queued'`) > 0; i++ {
		var next time.Time
		if err := e.a.db.QueryRow(`SELECT min(next_at) FROM ap_deliveries WHERE state = 'queued'`).Scan(&next); err != nil {
			t.Fatal(err)
		}
		if d := next.Sub(e.clock.now()); d > 0 {
			e.clock.add(d + time.Second)
		}
		e.sendAll()
	}
	if got := e.stub.sequence("h1.fan.example"); got != "Update:503 Update:503 Update:202 Move:202" {
		t.Errorf("h1: %q", got)
	}
	seq := e.stub.sequence("h3.fan.example")
	if !strings.HasPrefix(seq, "Update:503") || strings.Count(seq, "Update") != len(deliveryRetries)+1 || strings.Index(seq, "Move") < strings.LastIndex(seq, "Update") {
		t.Errorf("h3, down throughout: %q", seq)
	}
	if n := e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'failed' AND host = 'h3.fan.example'`); n != 2 {
		t.Errorf("%d rows given up at h3, want the Update's and the Move's", n)
	}
}

// The domain policy, before anything is asked: a suspended server is refused with no request sent; in allowlist
// mode a server not allowed is refused; a server under "limit" may be moved to (the member's choice: limit hides a
// server's content here, 10, and is not a refusal).
func TestDBMoveOutPolicy(t *testing.T) {
	e := newMoveEnv(t)
	e.target(novaID, anaActor)
	ctx := context.Background()
	e.mustExec(`INSERT INTO domain_policies (domain, policy) VALUES ('social.example', 'suspend') ON CONFLICT (domain) DO UPDATE SET policy = 'suspend'`)
	if err := e.a.fed.loadPolicy(ctx); err != nil {
		t.Fatal(err)
	}
	before := e.w.total()
	if code, out := e.move("@nova@social.example", movePW); code != 403 || out["reason"] != "notAllowed" || e.w.total() != before {
		t.Errorf("a suspended server: %d %v, %d requests", code, out["_raw"], e.w.total()-before)
	}
	e.mustExec(`UPDATE instance_settings SET value = '"allowlist"' WHERE key = 'federation_policy'`)
	e.mustExec(`DELETE FROM domain_policies WHERE domain = 'social.example'`)
	if err := e.a.fed.loadPolicy(ctx); err != nil {
		t.Fatal(err)
	}
	if code, out := e.move("@nova@social.example", movePW); code != 403 || out["reason"] != "notAllowed" {
		t.Errorf("a server not allowed: %d %v", code, out["_raw"])
	}
	e.mustExec(`INSERT INTO domain_policies (domain, policy) VALUES ('social.example', 'limit'), ('h1.fan.example', 'allow'), ('h2.fan.example', 'allow')`)
	if err := e.a.fed.loadPolicy(ctx); err != nil {
		t.Fatal(err)
	}
	if code, out := e.move("@nova@social.example", movePW); code != 200 || e.movedTo() != novaID {
		t.Errorf("a limited server: %d %v", code, out["_raw"])
	}
}

// A member who stops sharing, leaves or is deleted while the move is being made (between the check of nova and the
// transaction): nothing of the move is written, the answer says so, and after a deletion or a leaving only the
// Delete(Person) goes. A move made and then followed by a deletion: the Update and the Move go with the account, and
// the Delete(Person) is sent. Stopping sharing after a move does not call the Move back: it was the member's.
func TestDBMoveOutMidway(t *testing.T) {
	for _, c := range []struct{ name, how string }{{"stops sharing", "share"}, {"leaves", "leave"}, {"is deleted", "delete"}} {
		t.Run(c.name, func(t *testing.T) {
			e := newMoveEnv(t)
			e.target(novaID, anaActor)
			root := e.rootSession()
			e.a.fed.moveHook = func(string) {
				e.a.fed.moveHook = nil
				switch c.how {
				case "share":
					e.must(200, e.ana, "PUT", "/api/me", map[string]any{"federates": false})
				case "leave":
					e.must(200, e.ana, "POST", "/api/me/federation/leave", map[string]any{"confirm": true})
				case "delete":
					e.must(200, root, "DELETE", fmt.Sprintf("/api/admin/users/%d", e.anaID), nil)
				}
			}
			code, out := e.move("@nova@social.example", movePW)
			want := 409
			if c.how == "delete" {
				want = 409 // the session went with the account only after the handler took it
			}
			if code != want {
				t.Errorf("the move: %d %v", code, out["_raw"])
			}
			if n := e.count(`SELECT count(*) FROM ap_outbox WHERE body LIKE '%"type":"Move"%' OR (body LIKE '%"type":"Update"%' AND body LIKE '%movedTo%')`); n != 0 {
				t.Errorf("%d activities of the move written", n)
			}
			if c.how != "delete" && e.movedTo() != "" {
				t.Error("moved_to was written")
			}
			if c.how != "share" && len(e.personDeletes()) != 1 {
				t.Errorf("%d Delete(Person) activities, want 1", len(e.personDeletes()))
			}
		})
	}
	t.Run("deleted after the move", func(t *testing.T) {
		e := newMoveEnv(t)
		e.target(novaID, anaActor)
		e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
		e.must(200, e.rootSession(), "DELETE", fmt.Sprintf("/api/admin/users/%d", e.anaID), nil)
		e.sendAll()
		for _, h := range []string{"h1.fan.example", "h2.fan.example"} {
			if got := e.stub.sequence(h); got != "Delete:202" {
				t.Errorf("%s was sent %q", h, got)
			}
		}
	})
	t.Run("stops sharing after the move", func(t *testing.T) {
		e := newMoveEnv(t)
		e.target(novaID, anaActor)
		e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
		e.must(200, e.ana, "PUT", "/api/me", map[string]any{"federates": false})
		e.sendAll()
		if got := e.stub.sequence("h1.fan.example"); got != "Update:202 Move:202" {
			t.Errorf("h1 was sent %q", got)
		}
		// Not sharing, she cannot move again (nor could she have moved): sharing is what other servers know her by.
		e.clock.add(moveCooldown + time.Minute)
		if code, out := e.move("@nova@social.example", movePW); code != 400 || out["error"] != moveNeedsShare {
			t.Errorf("a move while not sharing: %d %v", code, out["_raw"])
		}
	})
}

// Two moves at once: the first is held just before its transaction while the second is made whole. The first then
// finds the move made and changes nothing: one Update, one Move, one moved_to. A full queue refuses a move with 503
// and changes nothing, so it can be tried again.
func TestDBMoveOutAtOnce(t *testing.T) {
	e := newMoveEnv(t)
	e.target(novaID, anaActor)
	other := "https://social.example/users/vega"
	e.target(other, anaActor)
	var second int
	e.a.fed.moveHook = func(string) {
		e.a.fed.moveHook = nil
		second, _ = e.move("@vega@social.example", movePW)
	}
	if code, out := e.move("@nova@social.example", movePW); code != 409 || out["error"] != moveChanged {
		t.Errorf("the first, after the second was made: %d %v", code, out["_raw"])
	}
	if second != 200 || e.movedTo() != other {
		t.Errorf("the second: %d, moved to %q", second, e.movedTo())
	}
	if n := e.count(`SELECT count(*) FROM ap_outbox WHERE body LIKE '%"type":"Move"%'`); n != 1 {
		t.Errorf("%d Moves", n)
	}

	f := newMoveEnv(t)
	f.target(novaID, anaActor)
	f.mustExec(`INSERT INTO ap_outbox (ap_id, body, created_at, fanned_out) SELECT 'https://here.example/activities/w' || g, '{}', now(), false FROM generate_series(1, $1) g`, deliveryWaitingMax)
	code, out, hdr := f.doH(f.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW}, nil)
	if code != 503 || out["error"] != moveFull || hdr.Get("Retry-After") != "300" || f.movedTo() != "" {
		t.Errorf("a full queue: %d %v %q, moved to %q", code, out["_raw"], hdr.Get("Retry-After"), f.movedTo())
	}
}

// The target: an address that redirects to the actor is moved to under the actor's own id; a document over the
// fetcher's 1 MB is no account; a server that does not answer before the member's request ends is not found; the
// target on this server is refused with its own words, with no request; a gone account is refused. Nothing is
// written by any of them.
func TestDBMoveOutTarget(t *testing.T) {
	e := newMoveEnv(t)
	e.target(novaID, anaActor)
	e.w.put("https://social.example/@nova", worldDoc{status: 301, location: novaID})
	e.user("bob", "user")
	before := e.w.total()
	if code, out := e.move("@bob@here.example", movePW); code != 400 || out["error"] != moveSameServer || e.w.total() != before {
		t.Errorf("an account of this server: %d %v", code, out["_raw"])
	}
	if code, out := e.move("https://here.example/users/bob", movePW); code != 400 || out["error"] != moveSameServer {
		t.Errorf("the address of an account of this server: %d %v", code, out["_raw"])
	}
	big := "https://social.example/users/big"
	e.w.put(big, worldDoc{body: []byte(`{"id":"` + big + `","type":"Person","inbox":"` + big + `/inbox","summary":"` + strings.Repeat("a", actorDocMax) + `"}`)})
	putWF(e.w, "social.example", "acct:big@social.example", "acct:big@social.example", big)
	if code, out := e.move("@big@social.example", movePW); code != 422 || out["reason"] != "notAccount" {
		t.Errorf("a document over 1 MB: %d %v", code, out["_raw"])
	}
	// An account that names ana but whose inbox is on another origin: nothing could reach it (sendFollow's rule).
	person(e.w, "https://social.example/users/far", "far", map[string]any{"alsoKnownAs": []string{anaActor}, "inbox": "https://other.example/inbox"})
	if code, out := e.move("@far@social.example", movePW); code != 422 || out["reason"] != "notAccount" {
		t.Errorf("an account whose inbox is on another origin: %d %v", code, out["_raw"])
	}
	gone := "https://social.example/users/gone"
	putWF(e.w, "social.example", "acct:gone@social.example", "acct:gone@social.example", gone)
	e.w.put(gone, worldDoc{status: 410})
	if code, out := e.move("@gone@social.example", movePW); code != 410 || out["reason"] != "gone" {
		t.Errorf("a gone account: %d %v", code, out["_raw"])
	}
	// No answer: the member's request ends while the actor is held at the stub.
	slow := "https://social.example/users/slow"
	putWF(e.w, "social.example", "acct:slow@social.example", "acct:slow@social.example", slow)
	release := e.w.holdAt(slow)
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "/api/me/move", strings.NewReader(`{"target":"@slow@social.example","password":"`+movePW+`"}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: cookieName, Value: e.ana})
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); e.h.ServeHTTP(w, r) }()
	select { // the request may end before it asks (a refusal), which the check below then reports
	case <-e.w.arrived:
	case <-done:
	}
	cancel()
	<-done
	if w.Code != 404 || !strings.Contains(w.Body.String(), `"notFound"`) {
		t.Errorf("a server that does not answer: %d %s", w.Code, w.Body)
	}
	if e.movedTo() != "" || len(e.rows(e.anaID)) != 0 {
		t.Fatal("a refused move wrote something")
	}
	// The address that redirects: the move goes, to the actor's own id.
	if code, out := e.move("https://social.example/@nova", movePW); code != 200 || e.movedTo() != novaID {
		t.Errorf("an address that redirects to the actor: %d %v, moved to %q", code, out["_raw"], e.movedTo())
	}
}

// A Follow from another server of a member who moved is refused with a Reject, while a follower who was already
// following keeps its follow (a repeat of its Follow is answered as before).
func TestDBMoveOutFollowIn(t *testing.T) {
	e := newMoveEnv(t)
	e.target(novaID, anaActor)
	e.must(200, e.ana, "POST", "/api/me/move", map[string]any{"target": "@nova@social.example", "password": movePW})
	e.deliver(activity(boID, "Follow", anaActor, nil), e.bo())
	if got := e.outcome(); !strings.Contains(got, "refused (the account moved to another server)") {
		t.Errorf("a new Follow: %s", got)
	}
	if e.count(`SELECT count(*) FROM follows f JOIN ap_actors a ON a.user_id = f.follower_id WHERE a.ap_id = $1`, boID) != 0 {
		t.Error("a follow was written")
	}
}

// Moving and the aliases are only in a mode that federates, behind the moves flag: off, the routes answer as the flag
// says and /api/me says nothing of moving; local mode has none of the routes (TestDBLocalModeUnchanged holds its
// table, TestDBFederatedRoutes the federated one).
func TestDBMoveOutFlagAndModes(t *testing.T) {
	e := newMoveEnv(t)
	e.setFlag("moves", false)
	for _, c := range []struct{ method, path string }{{"POST", "/api/me/move"}, {"DELETE", "/api/me/move"}, {"PUT", "/api/me/aliases"}, {"DELETE", "/api/me/aliases"}} {
		if code, _ := e.do(e.ana, c.method, c.path, map[string]any{}); code != 404 {
			t.Errorf("%s %s with the flag off: %d", c.method, c.path, code)
		}
	}
	if me := e.must(200, e.ana, "GET", "/api/me", nil); me["federation"].(map[string]any)["move"] != nil {
		t.Error("/api/me speaks of moving with the flag off")
	}
	l := newEnv(t)
	if err := l.a.start(func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	l.h = l.a.routes(staticOf(t, map[string]string{"index.html": "<!doctype html><title>Flowa</title>"}))
	_, tok := l.user("ana", "user")
	for _, c := range []struct{ method, path string }{{"POST", "/api/me/move"}, {"PUT", "/api/me/aliases"}} {
		if code, _ := l.do(tok, c.method, c.path, map[string]any{}); code != 404 && code != 405 {
			t.Errorf("local mode answers %s %s with %d", c.method, c.path, code)
		}
	}
	if me := l.must(200, tok, "GET", "/api/me", nil); me["federation"] != nil {
		t.Error("local mode's /api/me speaks of federation")
	}
}
