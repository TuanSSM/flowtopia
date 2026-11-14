package main

// What becomes public with federation, and what never does (docs/federation.md, F2, F16, 4.3, 5.2 and 6.7):
// the audience of a post, the names that are never given out again, and (further down) the actor
// documents, the Notes and the small pages that show them. The DB tests ask through the whole server.

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// mustExec runs a statement on the test's database.
func (e *tenv) mustExec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.a.db.Exec(q, args...); err != nil {
		e.t.Fatal(err)
	}
}

// A retired name is refused at every door an account is made by, in the words of a taken name; the name of
// a deleted account is retired only when it had opted in (or ever had a key); the instance's own host is
// taken in a mode that federates; and no request renames an account.
func TestDBRetiredNames(t *testing.T) {
	e := newEnv(t)
	if err := e.fedStart(nil); err != nil {
		t.Fatal(err)
	}
	var root int64
	if err := e.a.db.QueryRow(`SELECT id FROM users WHERE username = 'root'`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	rootTok := e.session(root)
	anaID, _ := e.user("ana", "user")                      // opted in
	bobID, _ := e.user("bob", "user")                      // never opted in
	cydID, _ := e.user("cyd", "user")                      // opted in once, then out: her key stays
	e.user("dee", "user")                                  // holds her name: the words of a taken name
	hostID, hostTok := e.user("flowa.example.org", "user") // named like the instance's host
	e.mustExec(`UPDATE users SET federates = true WHERE id = $1`, anaID)
	e.mustExec(`INSERT INTO ap_keys (user_id, key_id, public_key, private_sealed) VALUES ($1, '/users/cyd#main-key', 'x', 'x')`, cydID)
	for _, id := range []int64{anaID, bobID, cydID} {
		e.must(200, rootTok, "DELETE", fmt.Sprintf("/api/admin/users/%d", id), nil)
	}
	if got := e.count(`SELECT count(*) FROM retired_usernames WHERE username IN ('ana', 'cyd')`); got != 2 || e.count(`SELECT count(*) FROM retired_usernames`) != 2 {
		t.Fatalf("retired: %d of ana and cyd, %d in all; want those two only", got, e.count(`SELECT count(*) FROM retired_usernames`))
	}

	signup := func(name string) (int, string) {
		code, out := e.do("", "POST", "/api/signup", map[string]any{"username": name, "password": "flowa-test-only"})
		return code, out["_raw"].(string)
	}
	takenCode, taken := signup("dee")
	if takenCode != 409 {
		t.Fatalf("a taken name: %d %s", takenCode, taken)
	}
	for _, name := range []string{"ana", "cyd", "flowa.example.org", "ANA"} {
		if code, body := signup(name); code != takenCode || body != taken {
			t.Errorf("sign-up as %s: %d %s; a taken name: %d %s", name, code, body, takenCode, taken)
		}
	}
	if code, body := signup("bob"); code != 200 {
		t.Errorf("bob never opted in, so his name is free again: %d %s", code, body)
	}

	// A request to join, while sign-up is by approval: refused in the same words.
	e.must(200, rootTok, "GET", "/api/admin/instance", nil)
	e.mustExec(`INSERT INTO instance_settings (key, value) VALUES ('registration', '"approval"')`)
	e.a.instMu.Lock()
	e.a.instCache = nil
	e.a.instMu.Unlock()
	if code, body := signup("eve"); code != 202 {
		t.Fatalf("a request to join: %d %s", code, body)
	}
	for _, name := range []string{"ana", "flowa.example.org"} {
		if code, body := signup(name); code != takenCode || body != taken {
			t.Errorf("a request to join as %s: %d %s", name, code, body)
		}
	}
	// A request that waited under a name before it was retired (put there behind sign-up's back) cannot
	// become an account; the admin is told the name is taken, and nothing of who had it.
	e.mustExec(`INSERT INTO join_requests (username, display_name, pw_hash) VALUES ('ana', 'ana', 'x')`)
	var req int64
	if err := e.a.db.QueryRow(`SELECT id FROM join_requests WHERE username = 'ana'`).Scan(&req); err != nil {
		t.Fatal(err)
	}
	if code, out := e.do(rootTok, "POST", fmt.Sprintf("/api/admin/requests/%d/approve", req), nil); code != 409 || !strings.Contains(out["error"].(string), "is taken") || e.count(`SELECT count(*) FROM users WHERE username = 'ana'`) != 0 {
		t.Errorf("approving a request under a retired name: %d %v", code, out["_raw"])
	}

	// ADMIN_USER: no account under a retired name; the start goes on while accounts exist, and stops with
	// the reason when there would be none.
	if id, err := e.a.seedAdmin("ana", "flowa-test-only"); id != 0 || err != nil || e.count(`SELECT count(*) FROM users WHERE username = 'ana'`) != 0 {
		t.Errorf("ADMIN_USER=ana: %d %v", id, err)
	}
	hostAdmin := newEnv(t)
	if err := hostAdmin.fedStart(map[string]string{"ADMIN_USER": "flowa.example.org"}); err == nil || !strings.Contains(err.Error(), "taken") {
		t.Errorf("the first account named like the host: %v", err)
	}
	empty := newEnv(t)
	empty.mustExec(`INSERT INTO retired_usernames (username) VALUES ('root')`)
	if err := empty.a.start(func(k string) string {
		return map[string]string{"ADMIN_USER": "root", "ADMIN_PASSWORD": "flowa-test-only", "FLOWA_PREVIEW_FETCH": "off"}[k]
	}); err == nil || !strings.Contains(err.Error(), "taken") {
		t.Errorf("the first account under a retired name: %v", err)
	}

	// A member named like the host keeps the account and cannot opt in: other servers would find the
	// instance actor under that handle.
	if code, out := e.do(hostTok, "PUT", "/api/me", map[string]any{"federates": true}); code != 400 || out["error"] != hostNameRefusal || e.count(`SELECT count(*) FROM users WHERE id = $1 AND federates`, hostID) != 0 {
		t.Errorf("opting in as the host's name: %d %v", code, out["_raw"])
	}

	// No request renames an account: the name is not a field anything takes.
	_, deeTok := e.user("fay", "user")
	if code, _ := e.do(deeTok, "PUT", "/api/me", map[string]any{"username": "zed", "displayName": "Fay"}); code != 200 || e.count(`SELECT count(*) FROM users WHERE username = 'fay'`) != 1 || e.count(`SELECT count(*) FROM users WHERE username = 'zed'`) != 0 {
		t.Errorf("a rename through PUT /api/me: %d", code)
	}

	// Local mode: retired names stay retired (4.3), and an opted-in member deleted there is retired too; a
	// name like a host is nobody's business there (there is no origin), so sign-up is as before.
	l := newEnv(t)
	if err := l.a.start(func(k string) string { return map[string]string{"FLOWA_PREVIEW_FETCH": "off"}[k] }); err != nil {
		t.Fatal(err)
	}
	_, ltok := l.user("lead", "admin")
	gid, _ := l.user("gil", "user")
	l.mustExec(`UPDATE users SET federates = true WHERE id = $1`, gid)
	l.mustExec(`INSERT INTO retired_usernames (username) VALUES ('old')`)
	l.must(200, ltok, "DELETE", fmt.Sprintf("/api/admin/users/%d", gid), nil)
	for _, name := range []string{"old", "gil"} {
		code, out := l.do("", "POST", "/api/signup", map[string]any{"username": name, "password": "flowa-test-only"})
		if code != 409 || out["error"] != "That username is taken." {
			t.Errorf("local mode, sign-up as %s: %d %v", name, code, out["_raw"])
		}
	}
	if code, _ := l.do("", "POST", "/api/signup", map[string]any{"username": "flowa.example.org", "password": "flowa-test-only"}); code != 200 {
		t.Errorf("local mode, a name like a host: %d", code)
	}
}

// The audience of a post: This server for every post outside a mode that federates, whatever the page
// sends, and with no new key in any answer; in a mode that federates, Public only for a member who opted in
// and asked for it, while federation is not refused; told to the author alone.
func TestDBPostAudience(t *testing.T) {
	l := newEnv(t)
	if err := l.a.start(func(k string) string { return map[string]string{"FLOWA_PREVIEW_FETCH": "off"}[k] }); err != nil {
		t.Fatal(err)
	}
	l.h = l.a.routes(staticOf(t, nil))
	lid, ltok := l.user("ana", "user")
	l.mustExec(`UPDATE users SET federates = true WHERE id = $1`, lid)
	for _, aud := range []any{"public", "local", "nonsense", nil} {
		out := l.must(200, ltok, "POST", "/api/posts", map[string]any{"body": "hello", "audience": aud})
		if raw := out["_raw"].(string); strings.Contains(raw, "audience") {
			t.Errorf("local mode, audience %v: the answer names an audience: %s", aud, raw)
		}
	}
	if n := l.count(`SELECT count(*) FROM posts WHERE audience <> 'local'`); n != 0 {
		t.Errorf("local mode: %d posts are not This server", n)
	}
	if _, me := l.do(ltok, "GET", "/api/me", nil); strings.Contains(me["_raw"].(string), "audiences") {
		t.Error("local mode: /api/me offers audiences")
	}

	e := newEnv(t)
	if err := e.fedStart(nil); err != nil {
		t.Fatal(err)
	}
	aID, aTok := e.user("ana", "user")
	_, bTok := e.user("bob", "user")
	post := func(tok string, aud any) (int, map[string]any) {
		return e.do(tok, "POST", "/api/posts", map[string]any{"body": "a figure-8", "audience": aud})
	}
	if code, out := post(aTok, "public"); code != 400 || out["error"] != audNotOptedIn {
		t.Errorf("Public before opting in: %d %v", code, out["_raw"])
	}
	if code, out := post(aTok, "everyone"); code != 400 || out["error"] != audUnknown {
		t.Errorf("an unknown audience: %d %v", code, out["_raw"])
	}
	if _, me := e.do(aTok, "GET", "/api/me", nil); me["federation"].(map[string]any)["audiences"] != nil {
		t.Error("the composer is offered audiences before opting in")
	}
	e.must(200, aTok, "PUT", "/api/me", map[string]any{"federates": true})
	_, me := e.do(aTok, "GET", "/api/me", nil)
	if ch, _ := me["federation"].(map[string]any)["audiences"].([]any); len(ch) != 2 || ch[0].(map[string]any)["value"] != "public" {
		t.Errorf("the choices offered once opted in: %v", me["federation"])
	}
	code, out := post(aTok, "public")
	if code != 200 {
		t.Fatalf("Public once opted in: %d %v", code, out["_raw"])
	}
	pub := out["posts"].([]any)[0].(map[string]any)
	if pub["audience"] != "public" {
		t.Errorf("the author is not told the audience: %v", pub)
	}
	// Nothing asked is This server: no page from before audiences publishes by leaving the field out.
	_, out = post(aTok, nil)
	quiet := out["posts"].([]any)[0].(map[string]any)
	if quiet["audience"] != "local" {
		t.Errorf("no audience asked: %v", quiet)
	}
	if e.count(`SELECT count(*) FROM posts WHERE user_id = $1 AND audience = 'public'`, aID) != 1 {
		t.Error("the Public post is not stored as public")
	}
	// Another member reads the same posts with no audience in them.
	if _, feed := e.do(bTok, "GET", "/api/posts", nil); strings.Contains(feed["_raw"].(string), "audience") {
		t.Errorf("another member is told the audience: %s", feed["_raw"])
	}
	if _, feed := e.do(aTok, "GET", "/api/posts", nil); !strings.Contains(feed["_raw"].(string), `"audience":"public"`) {
		t.Errorf("the author's timeline has no audience: %s", feed["_raw"])
	}
	// While federation is refused nothing is offered and Public is refused.
	e.a.fed.refuse("federation refused: a test")
	if code, out := post(aTok, "public"); code != 400 || out["error"] != audOff {
		t.Errorf("Public while refused: %d %v", code, out["_raw"])
	}
	if _, me := e.do(aTok, "GET", "/api/me", nil); me["federation"].(map[string]any)["audiences"] != nil {
		t.Error("audiences are offered while federation is refused")
	}

	// The owner of a single-profile instance with FLOWA_FEDERATE=on has opted in by the setting.
	s := newEnv(t)
	if err := s.fedSingleStart(nil); err != nil {
		t.Fatal(err)
	}
	var owner int64
	if err := s.a.db.QueryRow(`SELECT id FROM users WHERE owner`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if code, out := s.do(s.session(owner), "POST", "/api/posts", map[string]any{"body": "mine", "audience": "public"}); code != 200 || out["posts"].([]any)[0].(map[string]any)["audience"] != "public" {
		t.Errorf("the owner of a single-profile instance, Public: %d %v", code, out["_raw"])
	}
}

// holdRetirement opens the transaction a delete of an opted-in member runs (the name's lock, the name
// retired, the row gone) and leaves it open. When make is true the account is first made inside it, so
// that the name is free in every snapshot taken before the commit: the case where a sign-up's first look
// found the name free. It returns the transaction and its backend's pid.
func holdRetirement(t *testing.T, e *tenv, name string, make bool) (*sql.Tx, int) {
	t.Helper()
	tx, err := e.a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	var pid int
	var id int64
	steps := []func() error{
		func() error { return tx.QueryRow(`SELECT pg_backend_pid()`).Scan(&pid) },
		func() error { _, err := tx.Exec(nameLockSQL, name); return err },
	}
	if make {
		steps = append(steps, func() error {
			return tx.QueryRow(`INSERT INTO users (username, display_name, pw_hash, federates) VALUES ($1, $1, 'x', true) RETURNING id`, name).Scan(&id)
		})
	} else {
		steps = append(steps, func() error { return tx.QueryRow(`SELECT id FROM users WHERE username = $1`, name).Scan(&id) })
	}
	steps = append(steps,
		func() error { _, err := tx.Exec(retireSQL, id); return err },
		func() error { _, err := tx.Exec(`DELETE FROM users WHERE id = $1`, id); return err })
	for _, s := range steps {
		if err := s(); err != nil {
			t.Fatal(err)
		}
	}
	return tx, pid
}

// blockedBy waits until some statement waits for the backend pid: the other side of a race has reached
// the point where it must wait for the retirement, whatever it waits on (the name's lock, or the unique
// index without it).
func blockedBy(t *testing.T, e *tenv, pid int) {
	t.Helper()
	waitFor(t, "a statement waiting for the retirement", func() bool {
		return e.count(`SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))`, pid) > 0
	})
}

// No account can be made under a name while a delete is retiring it: every door waits for the delete and
// then sees the name retired (nameLockSQL). Each case holds the retirement open, waits until the door is
// blocked on it, and commits; without the lock the door's insert would go ahead with the snapshot it took
// before the commit and give the name away. Then the real handlers race many times.
func TestDBRetireRace(t *testing.T) {
	e := newEnv(t)
	if err := e.fedStart(nil); err != nil {
		t.Fatal(err)
	}
	e.a.lim.signup = newLimiter(1<<20, time.Hour)
	var root int64
	if err := e.a.db.QueryRow(`SELECT id FROM users WHERE username = 'root'`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	rootTok := e.session(root)
	answer := func(f func() int) chan int {
		c := make(chan int, 1)
		go func() { c <- f() }()
		return c
	}

	// Sign-up whose first look found the name free (the account was made and is being deleted inside one
	// open transaction).
	tx, pid := holdRetirement(t, e, "sam", true)
	got := answer(func() int {
		code, _ := e.do("", "POST", "/api/signup", map[string]any{"username": "sam", "password": "flowa-test-only"})
		return code
	})
	blockedBy(t, e, pid)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if code := <-got; code != 409 || e.count(`SELECT count(*) FROM users WHERE username = 'sam'`) != 0 {
		t.Errorf("sign-up during a retirement: %d, %d accounts", code, e.count(`SELECT count(*) FROM users WHERE username = 'sam'`))
	}

	// Approving a request that waits under the name of an account being deleted.
	e.user("ria", "user")
	e.mustExec(`UPDATE users SET federates = true WHERE username = 'ria'`)
	var req int64
	if err := e.a.db.QueryRow(`INSERT INTO join_requests (username, display_name, pw_hash) VALUES ('ria', 'ria', 'x') RETURNING id`).Scan(&req); err != nil {
		t.Fatal(err)
	}
	tx, pid = holdRetirement(t, e, "ria", false)
	got = answer(func() int {
		code, _ := e.do(rootTok, "POST", fmt.Sprintf("/api/admin/requests/%d/approve", req), nil)
		return code
	})
	blockedBy(t, e, pid)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if code := <-got; code != 409 || e.count(`SELECT count(*) FROM users WHERE username = 'ria'`) != 0 {
		t.Errorf("approval during a retirement: %d", code)
	}

	// ADMIN_USER, whose first look found the name free.
	tx, pid = holdRetirement(t, e, "ada", true)
	type made struct {
		id  int64
		err error
	}
	adm := make(chan made, 1)
	go func() { id, err := e.a.seedAdmin("ada", "flowa-test-only"); adm <- made{id, err} }()
	blockedBy(t, e, pid)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if m := <-adm; m.id != 0 || m.err != nil || e.count(`SELECT count(*) FROM users WHERE username = 'ada'`) != 0 {
		t.Errorf("ADMIN_USER during a retirement: %d %v", m.id, m.err)
	}

	// ADMIN_USER and an approval take the name's lock and the request's row in the same order, so they cannot
	// wait for each other: with makeAdmin's transaction holding the name's lock, an approval waits for the
	// name before it touches the request, and makeAdmin's delete of the request goes ahead. In the other order
	// the two would deadlock and Postgres would abort one of them, which at start would stop the server.
	var lockReq int64
	if err := e.a.db.QueryRow(`INSERT INTO join_requests (username, display_name, pw_hash) VALUES ('lia', 'lia', 'x') RETURNING id`).Scan(&lockReq); err != nil {
		t.Fatal(err)
	}
	hold, err := e.a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var holdPID int
	if err := hold.QueryRow(`SELECT pg_backend_pid()`).Scan(&holdPID); err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(nameLockSQL, "lia"); err != nil {
		t.Fatal(err)
	}
	approved := answer(func() int {
		code, _ := e.do(rootTok, "POST", fmt.Sprintf("/api/admin/requests/%d/approve", lockReq), nil)
		return code
	})
	blockedBy(t, e, holdPID)
	if _, err := hold.Exec(`DELETE FROM join_requests WHERE username = 'lia'`); err != nil {
		t.Fatalf("makeAdmin's delete of the request, with an approval waiting: %v", err)
	}
	if err := hold.Commit(); err != nil {
		t.Fatal(err)
	}
	if code := <-approved; code != 404 {
		t.Errorf("the approval of a request ADMIN_USER took: %d", code)
	}

	// The real handlers against each other, many times: a delete of an opted-in member started together
	// with one door under the same name, each door in turn (a sign-up with no request in its way, an approval
	// of a request that waits under the name, and ADMIN_USER), so every door reaches its insert. However they
	// interleave, nobody gets the name. What these rounds prove: that the real handlers, in the orders a race
	// gives, never give the name away and never deadlock. They catch a missing lock on the delete through the
	// approval rounds (an approval's insert has no earlier look at the name). The sign-up and ADMIN_USER
	// rounds would pass without the lock too, because their first look sees the account or its retirement in
	// one snapshot; the interleaving only the lock closes for them is the held-open case above.
	for i := range 45 {
		name := fmt.Sprintf("race%d", i)
		uid, _ := e.user(name, "user")
		e.mustExec(`UPDATE users SET federates = true WHERE id = $1`, uid)
		door, what := func() bool { return false }, ""
		switch i % 3 {
		case 0:
			what = "sign-up"
			door = func() bool {
				c, _ := e.do("", "POST", "/api/signup", map[string]any{"username": name, "password": "flowa-test-only"})
				return c == 200
			}
		case 1:
			what = "approval"
			var rq int64
			if err := e.a.db.QueryRow(`INSERT INTO join_requests (username, display_name, pw_hash) VALUES ($1, $1, 'x') RETURNING id`, name).Scan(&rq); err != nil {
				t.Fatal(err)
			}
			door = func() bool {
				c, _ := e.do(rootTok, "POST", fmt.Sprintf("/api/admin/requests/%d/approve", rq), nil)
				return c == 200
			}
		case 2:
			what = "ADMIN_USER"
			door = func() bool {
				id, err := e.a.seedAdmin(name, "flowa-test-only")
				return err == nil && id != 0 && id != uid
			}
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		var deleted int
		var gave bool
		wg.Go(func() {
			<-start
			deleted, _ = e.do(rootTok, "DELETE", fmt.Sprintf("/api/admin/users/%d", uid), nil)
		})
		wg.Go(func() { <-start; gave = door() })
		close(start)
		wg.Wait()
		if deleted != 200 || gave || e.count(`SELECT count(*) FROM users WHERE username = $1`, name) != 0 ||
			e.count(`SELECT count(*) FROM retired_usernames WHERE username = $1`, name) != 1 {
			t.Fatalf("round %d (%s): delete %d, the name given %v", i, what, deleted, gave)
		}
	}
}
