package main

// Tests against a real Postgres, through the real handlers. They run when TEST_DATABASE_URL
// points at a database that may be written to (CI's integration job sets it); otherwise they
// are skipped. Each test gets its own schema, so tests never see each other's rows.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lib/pq"
)

type tenv struct {
	t *testing.T
	a *app
	h http.Handler
}

func newEnv(t *testing.T) *tenv {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	raw := make([]byte, 6)
	rand.Read(raw)
	name := "t_" + hex.EncodeToString(raw)
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(`DROP SCHEMA ` + name + ` CASCADE`); admin.Close() })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	db, err := openDB(u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a := newApp(db, t.TempDir())
	a.hosts = []string{"example.com"} // the Host httptest gives requests
	return &tenv{t: t, a: a, h: a.routes(staticOf(t, nil))}
}

// user makes an account and a session; it returns the user's id and session token.
func (e *tenv) user(name, role string) (int64, string) {
	e.t.Helper()
	var id int64
	if err := e.a.db.QueryRow(`INSERT INTO users (username, display_name, pw_hash, role, props, onboarded) VALUES ($1, $1, 'x', $2, '["nunchaku"]', true) RETURNING id`, name, role).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id, e.session(id)
}

// session signs a user in again and returns the new session token.
func (e *tenv) session(id int64) string {
	e.t.Helper()
	raw := make([]byte, 16)
	rand.Read(raw)
	tok := hex.EncodeToString(raw)
	if _, err := e.a.db.Exec(`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, now() + interval '1 day')`, tokenHash(tok), id); err != nil {
		e.t.Fatal(err)
	}
	return tok
}

// do sends a request as the session's user and decodes the JSON answer.
func (e *tenv) do(tok, method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	code, out, _ := e.doH(tok, method, path, body, nil)
	return code, out
}

// doH is do with request headers, and it also returns the headers of the answer.
func (e *tenv) doH(tok, method, path string, body any, hdr map[string]string) (int, map[string]any, http.Header) {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rd)
	r.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	if tok != "" {
		r.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	out := map[string]any{}
	json.Unmarshal(w.Body.Bytes(), &out)
	out["_raw"] = w.Body.String()
	return w.Code, out, w.Result().Header
}

// from sends a request with no session from the given address ("203.0.113.9:5000") and returns the
// status. via is a visitor's address when the connection is a trusted proxy's.
func (e *tenv) from(remote, via, method, path string, body any) int {
	e.t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.RemoteAddr = remote
	if via != "" {
		r.Header.Set("X-Forwarded-For", via)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w.Code
}

// logBuf is a log output that a test can read while the server may still be writing to it.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// logged collects what the server logs until the test ends.
func logged(t *testing.T) *logBuf {
	var buf logBuf
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	refusals.mu.Lock()
	refusals.since, refusals.seen, refusals.dropped = time.Now(), map[string]bool{}, 0
	refusals.mu.Unlock()
	return &buf
}

// must sends a request and fails the test unless the status is want.
func (e *tenv) must(want int, tok, method, path string, body any) map[string]any {
	e.t.Helper()
	code, out := e.do(tok, method, path, body)
	if code != want {
		e.t.Fatalf("%s %s: got %d, want %d: %v", method, path, code, want, out["_raw"])
	}
	return out
}

func num(v any) int64 { f, _ := v.(float64); return int64(f) }

func (e *tenv) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.a.db.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// post makes a post and returns its id.
func (e *tenv) post(tok, body string) int64 {
	out := e.must(200, tok, "POST", "/api/posts", map[string]any{"body": body})
	return num(out["posts"].([]any)[0].(map[string]any)["id"])
}

// comment adds a comment or reply and returns the new comment.
func (e *tenv) comment(tok string, post, parent int64, body string) map[string]any {
	e.t.Helper()
	out := e.must(200, tok, "POST", fmt.Sprintf("/api/posts/%d/comments", post), map[string]any{"body": body, "parentId": parent})
	return out["comment"].(map[string]any)
}

// kinds lists the kinds of a user's notifications, oldest first.
func (e *tenv) kinds(uid int64) []string {
	rows, err := e.a.db.Query(`SELECT kind FROM notifications WHERE user_id = $1 ORDER BY id`, uid)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var k string
		rows.Scan(&k)
		out = append(out, k)
	}
	return out
}

func TestDBSchemaTwice(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 2; i++ {
		if _, err := e.a.db.Exec(schema); err != nil {
			t.Fatal(err)
		}
		if _, err := e.a.db.Exec(schemaSocial); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDBMigrations(t *testing.T) {
	e := newEnv(t)
	// newEnv migrated a fresh schema: every migration is recorded once.
	if n := e.count(`SELECT count(*) FROM schema_migrations`); n != len(migrations) {
		t.Fatalf("recorded %d of %d migrations", n, len(migrations))
	}
	// A second start applies nothing.
	if err := migrate(e.a.db); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM schema_migrations`); n != len(migrations) {
		t.Fatalf("after a second start: %d", n)
	}
	// A database from before versioning (tables, no record) takes the old schema again harmlessly.
	if _, err := e.a.db.Exec(`DROP TABLE schema_migrations`); err != nil {
		t.Fatal(err)
	}
	_, tok := e.user("kept", "user")
	if err := migrate(e.a.db); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM schema_migrations`); n != len(migrations) {
		t.Fatalf("baseline: %d", n)
	}
	if got := e.must(200, tok, "GET", "/api/me", nil)["user"].(map[string]any)["username"]; got != "kept" {
		t.Fatalf("data must survive: %v", got)
	}
}

func TestDBReplies(t *testing.T) {
	e := newEnv(t)
	ownerID, owner := e.user("owner", "user")
	bID, b := e.user("bea", "user")
	cID, c := e.user("cal", "user")
	_, d := e.user("dan", "user")
	p := e.post(owner, "My figure-8")
	other := e.post(owner, "Another post")

	root := e.comment(b, p, 0, "How do you keep the plane?")
	rid := num(root["id"])
	rep := e.comment(c, p, rid, "Watch the elbow")
	if num(rep["parentId"]) != rid || rep["body"] != "@bea Watch the elbow" {
		t.Fatalf("reply: %v", rep)
	}
	// Who was told: the person answered gets a reply, the owner a comment, once each.
	if k := e.kinds(bID); strings.Join(k, ",") != "reply" {
		t.Fatalf("bea got %v", k)
	}
	if k := e.kinds(ownerID); strings.Join(k, ",") != "comment,comment" {
		t.Fatalf("owner got %v", k)
	}
	// A reply to a reply joins the same thread, with the person answered named once.
	r2 := e.comment(d, p, num(rep["id"]), "@cal agreed")
	if num(r2["parentId"]) != rid || r2["body"] != "@cal agreed" {
		t.Fatalf("reply to reply: %v", r2)
	}
	// Answering yourself adds no mention.
	r3 := e.comment(c, p, num(rep["id"]), "and relax the wrist")
	if r3["body"] != "and relax the wrist" {
		t.Fatalf("self reply: %v", r3)
	}
	if k := e.kinds(cID); strings.Join(k, ",") != "reply" {
		t.Fatalf("cal got %v (one reply from dan, none for answering himself)", k)
	}
	// When the owner is the one answered they get one notification, a reply.
	oc := e.comment(owner, p, 0, "Thanks all")
	before := len(e.kinds(ownerID))
	e.comment(b, p, num(oc["id"]), "Nice one @owner")
	if k := e.kinds(ownerID)[before:]; strings.Join(k, ",") != "reply" {
		t.Fatalf("owner answered got %v", k)
	}
	// Refusals: a parent on another post, a missing parent, a deleted parent.
	e.must(400, c, "POST", fmt.Sprintf("/api/posts/%d/comments", other), map[string]any{"body": "x", "parentId": rid})
	e.must(404, c, "POST", fmt.Sprintf("/api/posts/%d/comments", p), map[string]any{"body": "x", "parentId": 999999})
	e.must(200, b, "DELETE", fmt.Sprintf("/api/comments/%d", rid), nil)
	e.must(400, c, "POST", fmt.Sprintf("/api/posts/%d/comments", p), map[string]any{"body": "x", "parentId": rid})
	// The thread opens from any of its comments, with every reply.
	th := e.must(200, owner, "GET", fmt.Sprintf("/api/comments/%d/thread", num(r3["id"])), nil)
	got := th["comments"].([]any)[0].(map[string]any)
	if num(got["id"]) != rid || len(got["replies"].([]any)) != 3 || got["deleted"] != true || got["body"] != "" || num(th["focus"]) != num(r3["id"]) {
		t.Fatalf("thread: %v", th["_raw"])
	}
}

func TestDBDeleteKeepsThread(t *testing.T) {
	e := newEnv(t)
	_, owner := e.user("owner", "user")
	_, b := e.user("bea", "user")
	_, c := e.user("cal", "user")
	p := e.post(owner, "post")
	lone := e.comment(b, p, 0, "no replies")
	e.must(200, b, "DELETE", fmt.Sprintf("/api/comments/%d", num(lone["id"])), nil)
	if e.count(`SELECT count(*) FROM comments WHERE id = $1`, num(lone["id"])) != 0 {
		t.Fatal("a comment without replies should be removed")
	}
	root := e.comment(b, p, 0, "parent")
	rep := e.comment(c, p, num(root["id"]), "child")
	out := e.must(200, b, "DELETE", fmt.Sprintf("/api/comments/%d", num(root["id"])), nil)
	if out["placeholder"] != true || e.count(`SELECT count(*) FROM comments WHERE id = $1 AND deleted AND body = ''`, num(root["id"])) != 1 {
		t.Fatalf("parent should stay as a placeholder: %v", out)
	}
	// Someone else cannot delete it; liking or editing a placeholder is refused.
	e.must(404, c, "DELETE", fmt.Sprintf("/api/comments/%d", num(root["id"])), nil)
	e.must(400, c, "POST", fmt.Sprintf("/api/comments/%d/like", num(root["id"])), nil)
	e.must(400, b, "PUT", fmt.Sprintf("/api/comments/%d", num(root["id"])), map[string]any{"body": "back"})
	// When the last reply goes, so does the placeholder.
	e.must(200, c, "DELETE", fmt.Sprintf("/api/comments/%d", num(rep["id"])), nil)
	if e.count(`SELECT count(*) FROM comments WHERE post_id = $1`, p) != 0 {
		t.Fatal("the placeholder should go with its last reply")
	}
	// The feed counts only comments that are not deleted.
	r := e.comment(b, p, 0, "again")
	e.comment(c, p, num(r["id"]), "child")
	e.must(200, owner, "DELETE", fmt.Sprintf("/api/comments/%d", num(r["id"])), nil) // the post owner may delete
	feed := e.must(200, owner, "GET", "/api/posts", nil)
	if n := num(feed["posts"].([]any)[0].(map[string]any)["commentCount"]); n != 1 {
		t.Fatalf("commentCount %d, want 1", n)
	}
}

func TestDBCommentPaging(t *testing.T) {
	e := newEnv(t)
	e.a.lim.comments = newLimiter(1000, time.Hour) // this test writes 200 comments as one member
	_, owner := e.user("owner", "user")
	p := e.post(owner, "busy post")
	var first int64
	for i := 0; i < 45; i++ {
		id := num(e.comment(owner, p, 0, fmt.Sprintf("comment %d", i))["id"])
		if i == 0 {
			first = id
		}
	}
	for i := 0; i < 60; i++ {
		e.comment(owner, p, first, fmt.Sprintf("reply %d", i))
	}
	var after int64
	sizes := []int{}
	for {
		out := e.must(200, owner, "GET", fmt.Sprintf("/api/posts/%d/comments?after=%d", p, after), nil)
		list := out["comments"].([]any)
		sizes = append(sizes, len(list))
		after = num(list[len(list)-1].(map[string]any)["id"])
		if out["more"] != true {
			break
		}
	}
	if fmt.Sprint(sizes) != "[20 20 5]" {
		t.Fatalf("pages %v", sizes)
	}
	page := e.must(200, owner, "GET", fmt.Sprintf("/api/posts/%d/comments", p), nil)
	c0 := page["comments"].([]any)[0].(map[string]any)
	if num(c0["replyCount"]) != 60 || len(c0["replies"].([]any)) != 2 {
		t.Fatalf("first comment: %d replies, %d shown", num(c0["replyCount"]), len(c0["replies"].([]any)))
	}
	reps := e.must(200, owner, "GET", fmt.Sprintf("/api/comments/%d/replies", first), nil)
	if len(reps["comments"].([]any)) != 50 || reps["more"] != true {
		t.Fatalf("replies page: %d, more %v", len(reps["comments"].([]any)), reps["more"])
	}
	// The feed carries only the newest two comments, so it stays small.
	for i := 0; i < 95; i++ {
		e.comment(owner, p, 0, strings.Repeat("long words here ", 6))
	}
	code, feed := e.do(owner, "GET", "/api/posts", nil)
	fp := feed["posts"].([]any)[0].(map[string]any)
	if code != 200 || len(fp["comments"].([]any)) != 2 || num(fp["commentCount"]) != 200 || len(feed["_raw"].(string)) > 10000 {
		t.Fatalf("feed: %d comments shown, count %d, %d bytes", len(fp["comments"].([]any)), num(fp["commentCount"]), len(feed["_raw"].(string)))
	}
}

func TestDBEditComment(t *testing.T) {
	e := newEnv(t)
	_, owner := e.user("owner", "user")
	_, b := e.user("bea", "user")
	calID, _ := e.user("cal", "user")
	danID, _ := e.user("dan", "user")
	p := e.post(owner, "post")
	c := e.comment(b, p, 0, "thanks @cal")
	id := num(c["id"])
	out := e.must(200, b, "PUT", fmt.Sprintf("/api/comments/%d", id), map[string]any{"body": "thanks @cal and @dan"})
	if out["comment"].(map[string]any)["edited"] != true {
		t.Fatalf("not marked edited: %v", out)
	}
	// Only the newly mentioned person is told.
	if k := e.kinds(calID); len(k) != 1 {
		t.Fatalf("cal told %d times", len(k))
	}
	if k := e.kinds(danID); strings.Join(k, ",") != "mention" {
		t.Fatalf("dan got %v", k)
	}
	e.must(403, owner, "PUT", fmt.Sprintf("/api/comments/%d", id), map[string]any{"body": "not mine"})
	e.must(400, b, "PUT", fmt.Sprintf("/api/comments/%d", id), map[string]any{"body": "  "})
	e.a.db.Exec(`UPDATE comments SET created_at = now() - interval '16 minutes' WHERE id = $1`, id)
	e.must(403, b, "PUT", fmt.Sprintf("/api/comments/%d", id), map[string]any{"body": "too late"})
}

func TestDBCommentLikes(t *testing.T) {
	e := newEnv(t)
	_, owner := e.user("owner", "user")
	bID, b := e.user("bea", "user")
	p := e.post(owner, "post")
	id := num(e.comment(b, p, 0, "hello")["id"])
	path := fmt.Sprintf("/api/comments/%d/like", id)
	if out := e.must(200, owner, "POST", path, nil); out["liked"] != true || num(out["likes"]) != 1 {
		t.Fatalf("like: %v", out)
	}
	if out := e.must(200, owner, "POST", path, nil); out["liked"] != false || num(out["likes"]) != 0 {
		t.Fatalf("unlike: %v", out)
	}
	e.must(200, owner, "POST", path, nil)
	if k := e.kinds(bID); strings.Join(k, ",") != "comment_like" {
		t.Fatalf("bea got %v, want one comment_like", k)
	}
	// Likes show to the viewer who gave them.
	page := e.must(200, owner, "GET", fmt.Sprintf("/api/posts/%d/comments", p), nil)
	if c := page["comments"].([]any)[0].(map[string]any); c["liked"] != true || num(c["likes"]) != 1 {
		t.Fatalf("listing: %v", c)
	}
	e.must(404, owner, "POST", "/api/comments/999999/like", nil)
}

// setFlag turns a feature flag on or off for the test database.
func (e *tenv) setFlag(name string, on bool) {
	e.t.Helper()
	if err := e.a.setFlags(map[string]bool{name: on}); err != nil {
		e.t.Fatal(err)
	}
}

func TestPermissionTable(t *testing.T) {
	// Every permission a handler asks for belongs to the admin, and users have none.
	all := []string{permContentDelete, permPostHide, permUserMute, permUserSuspend, permReportResolve, permBadgeRevoke, permDashboard, permAudit,
		permCatalogEdit, permCatalogPublish, permFlagsEdit, permUserRole, permUserManage}
	for _, p := range all {
		if !can(&user{Role: "admin"}, p) {
			t.Errorf("admin lacks %s", p)
		}
		if can(&user{Role: "user"}, p) {
			t.Errorf("user has %s", p)
		}
	}
	modHas := map[string]bool{permContentDelete: true, permPostHide: true, permUserMute: true, permUserSuspend: true, permReportResolve: true, permBadgeRevoke: true, permDashboard: true, permCatalogEdit: true}
	for _, p := range all {
		if can(&user{Role: "moderator"}, p) != modHas[p] {
			t.Errorf("moderator %s: got %v, want %v", p, can(&user{Role: "moderator"}, p), modHas[p])
		}
	}
	if can(nil, permDashboard) || can(&user{Role: "nobody"}, permDashboard) {
		t.Error("no user or an unknown role must have no permissions")
	}
	if !outranks("admin", "moderator") || !outranks("moderator", "user") || outranks("moderator", "moderator") || outranks("user", "admin") {
		t.Error("ranks are wrong")
	}
}

func TestDBRoles(t *testing.T) {
	e := newEnv(t)
	adminID, admin := e.user("boss", "admin")
	bID, b := e.user("bea", "user")
	e.must(200, admin, "PUT", fmt.Sprintf("/api/admin/users/%d/role", bID), map[string]any{"role": "moderator"})
	me := e.must(200, b, "GET", "/api/me", nil)
	perms := fmt.Sprint(me["perms"])
	if !strings.Contains(perms, permReportResolve) || strings.Contains(perms, permUserRole) {
		t.Fatalf("moderator perms: %v", perms)
	}
	// Refusals: changing your own role, an unknown role, a moderator changing roles, demoting the last admin.
	e.must(400, admin, "PUT", fmt.Sprintf("/api/admin/users/%d/role", adminID), map[string]any{"role": "user"})
	e.must(400, admin, "PUT", fmt.Sprintf("/api/admin/users/%d/role", bID), map[string]any{"role": "king"})
	e.must(403, b, "PUT", fmt.Sprintf("/api/admin/users/%d/role", adminID), map[string]any{"role": "user"})
	e.must(403, b, "PUT", "/api/flags", map[string]any{"flags": map[string]bool{"tour": true}})
	// The owner (SEC-12). With no owner yet, the longest-standing admin becomes it, once.
	role := func(id int64) string { return fmt.Sprintf("/api/admin/users/%d/role", id) }
	owner := func(id int64) string { return fmt.Sprintf("/api/admin/users/%d/owner", id) }
	to := func(r string) map[string]any { return map[string]any{"role": r} }
	cID, cal := e.user("cal", "admin")
	dID, dee := e.user("dee", "admin")
	// Before there is an owner nobody can take an admin's role away.
	e.must(403, admin, "PUT", role(cID), to("user"))
	for i := 0; i < 2; i++ {
		if err := e.a.ensureOwner(); err != nil {
			t.Fatal(err)
		}
	}
	if n := e.count(`SELECT count(*) FROM users WHERE owner`); n != 1 || e.count(`SELECT count(*) FROM users WHERE owner AND id = $1`, adminID) != 1 {
		t.Fatalf("%d owners, want the first admin alone", n)
	}
	if u := e.must(200, admin, "GET", "/api/me", nil)["user"].(map[string]any); u["owner"] != true {
		t.Fatalf("the owner's own account does not say so: %v", u["owner"])
	}
	if u := e.must(200, cal, "GET", "/api/me", nil)["user"].(map[string]any); u["owner"] != nil {
		t.Fatalf("an admin is marked owner: %v", u["owner"])
	}
	// Admins are peers: one cannot demote another, and nobody changes or deletes the owner.
	if out := e.must(403, cal, "PUT", role(dID), to("moderator")); !strings.Contains(fmt.Sprint(out["error"]), "Only the owner") {
		t.Fatalf("an admin demoting an admin: %v", out["error"])
	}
	if out := e.must(403, cal, "PUT", role(adminID), to("user")); !strings.Contains(fmt.Sprint(out["error"]), "owner's role cannot be changed") {
		t.Fatalf("an admin demoting the owner: %v", out["error"])
	}
	if out := e.must(403, cal, "DELETE", fmt.Sprintf("/api/admin/users/%d", adminID), nil); !strings.Contains(fmt.Sprint(out["error"]), "owner's account cannot be deleted") {
		t.Fatalf("an admin deleting the owner: %v", out["error"])
	}
	e.must(200, cal, "PUT", role(adminID), to("admin")) // no change is no offence
	// An admin still makes admins; only taking the role away is the owner's.
	eID, _ := e.user("eve", "user")
	e.must(200, cal, "PUT", role(eID), to("admin"))
	e.must(403, cal, "PUT", role(eID), to("user"))
	e.must(200, admin, "PUT", role(eID), to("user"))
	// The database holds the two rules too: one owner, and the owner is an admin.
	if _, err := e.a.db.Exec(`UPDATE users SET owner = true WHERE id = $1`, cID); err == nil {
		t.Fatal("the database let a second owner in")
	}
	if _, err := e.a.db.Exec(`UPDATE users SET role = 'user' WHERE id = $1`, adminID); err == nil {
		t.Fatal("the database let the owner stop being an admin")
	}
	// Handing over: only the owner, only to another admin who is not suspended.
	e.must(403, cal, "PUT", owner(dID), nil)
	e.must(403, cal, "PUT", owner(cID), nil)
	e.must(400, admin, "PUT", owner(adminID), nil)
	e.must(400, admin, "PUT", owner(bID), nil)
	e.must(404, admin, "PUT", owner(999999), nil)
	e.must(403, b, "PUT", owner(bID), nil)
	e.a.db.Exec(`UPDATE users SET suspended_until = now() + interval '1 day' WHERE id = $1`, dID)
	e.must(400, admin, "PUT", owner(dID), nil)
	e.a.db.Exec(`UPDATE users SET suspended_until = NULL WHERE id = $1`, dID)
	if n := e.count(`SELECT count(*) FROM users WHERE owner AND id = $1`, adminID); n != 1 {
		t.Fatal("a refused hand-over moved ownership")
	}
	e.must(200, admin, "PUT", owner(dID), nil)
	if e.count(`SELECT count(*) FROM users WHERE owner`) != 1 || e.count(`SELECT count(*) FROM users WHERE owner AND id = $1`, dID) != 1 {
		t.Fatal("after the hand-over dee alone is the owner")
	}
	if e.count(`SELECT count(*) FROM mod_actions WHERE action = 'owner' AND actor_id = $1 AND target_user_id = $2`, adminID, dID) != 1 {
		t.Fatal("the hand-over is not in the audit log")
	}
	if k := e.kinds(dID); strings.Join(k, ",") != "moderation" {
		t.Fatalf("dee told %v", k)
	}
	// The old owner is an admin like any other now: cannot demote, can be demoted.
	e.must(403, admin, "PUT", role(cID), to("user"))
	e.must(403, admin, "PUT", owner(adminID), nil)
	e.must(200, dee, "PUT", role(cID), to("user"))
	e.must(200, dee, "PUT", role(adminID), to("moderator"))
	e.must(200, dee, "PUT", role(adminID), to("admin"))
	admin = e.session(adminID)
	if n := e.count(`SELECT count(*) FROM mod_actions WHERE action = 'role'`); n != 6 {
		t.Fatalf("role changes logged %d times, want 6", n)
	}
	if k := e.kinds(bID); strings.Join(k, ",") != "moderation" {
		t.Fatalf("bea told %v", k)
	}
	e.must(200, admin, "PUT", "/api/flags", map[string]any{"flags": map[string]bool{"tour": true}})
	if e.count(`SELECT count(*) FROM mod_actions WHERE action = 'flags'`) != 1 {
		t.Fatal("flag change not logged")
	}
}

func TestDBReports(t *testing.T) {
	e := newEnv(t)
	_, owner := e.user("owner", "user")
	_, b := e.user("bea", "user")
	p := e.post(owner, "buy cheap nunchaku at spam.example")
	// With the flag off the endpoints do not exist.
	e.setFlag("moderation", false)
	e.must(404, b, "POST", "/api/reports", map[string]any{"kind": "post", "id": p, "reason": "spam"})
	e.setFlag("moderation", true)
	e.must(200, b, "POST", "/api/reports", map[string]any{"kind": "post", "id": p, "reason": "spam", "note": "link farm"})
	e.must(409, b, "POST", "/api/reports", map[string]any{"kind": "post", "id": p, "reason": "spam"})
	e.must(400, owner, "POST", "/api/reports", map[string]any{"kind": "post", "id": p, "reason": "spam"})
	e.must(400, b, "POST", "/api/reports", map[string]any{"kind": "post", "id": p, "reason": "boring"})
	e.must(400, b, "POST", "/api/reports", map[string]any{"kind": "planet", "id": p, "reason": "spam"})
	e.must(404, b, "POST", "/api/reports", map[string]any{"kind": "post", "id": 999999, "reason": "spam"})
	// Ten a day.
	for i := 0; i < 9; i++ {
		q := e.post(owner, fmt.Sprintf("post %d", i))
		e.must(200, b, "POST", "/api/reports", map[string]any{"kind": "post", "id": q, "reason": "offtopic"})
	}
	q := e.post(owner, "one more")
	e.must(429, b, "POST", "/api/reports", map[string]any{"kind": "post", "id": q, "reason": "offtopic"})
}

func TestDBModeration(t *testing.T) {
	e := newEnv(t)
	e.setFlag("moderation", true)
	_, admin := e.user("boss", "admin")
	modID, mod := e.user("mo", "moderator")
	ownerID, owner := e.user("owner", "user")
	r1ID, r1 := e.user("rae", "user")
	_, r2 := e.user("rob", "user")
	_, other := e.user("ola", "user")
	p := e.post(owner, "rude post")
	c := e.comment(owner, e.post(other, "fine post"), 0, "rude comment")
	for _, tok := range []string{r1, r2, other} {
		e.must(200, tok, "POST", "/api/reports", map[string]any{"kind": "post", "id": p, "reason": "harassment"})
	}
	e.must(200, r1, "POST", "/api/reports", map[string]any{"kind": "comment", "id": num(c["id"]), "reason": "harassment"})
	// The queue groups reports by what they are about.
	qu := e.must(200, mod, "GET", "/api/mod/queue", nil)
	items := qu["items"].([]any)
	first := items[0].(map[string]any)
	if len(items) != 2 || first["kind"] != "post" || len(first["reports"].([]any)) != 3 || first["author"].(map[string]any)["username"] != "owner" {
		t.Fatalf("queue: %v", qu["_raw"])
	}
	e.must(403, owner, "GET", "/api/mod/queue", nil)
	if n := num(e.must(200, mod, "GET", "/api/notifications", nil)["queue"]); n != 2 {
		t.Fatalf("queue count %d", n)
	}
	// Hiding needs a reason, then the post is gone for everyone but its author and moderators.
	act := func(tok string, want int, body map[string]any) { e.must(want, tok, "POST", "/api/mod/actions", body) }
	act(mod, 400, map[string]any{"action": "hide", "kind": "post", "id": p})
	act(mod, 200, map[string]any{"action": "hide", "kind": "post", "id": p, "reason": "Personal attack"})
	act(mod, 409, map[string]any{"action": "hide", "kind": "post", "id": p, "reason": "again"})
	sees := func(tok string) bool {
		code, _ := e.do(tok, "GET", fmt.Sprintf("/api/posts/%d", p), nil)
		return code == 200
	}
	if sees(r1) || !sees(owner) || !sees(mod) || !sees(admin) {
		t.Fatalf("visibility of a hidden post: reporter %v, owner %v, mod %v, admin %v", sees(r1), sees(owner), sees(mod), sees(admin))
	}
	if got := e.must(200, owner, "GET", fmt.Sprintf("/api/posts/%d", p), nil)["posts"].([]any)[0].(map[string]any)["hidden"]; got != "Personal attack" {
		t.Fatalf("author should see the reason, got %v", got)
	}
	feed := e.must(200, r1, "GET", "/api/posts", nil)
	for _, x := range feed["posts"].([]any) {
		if num(x.(map[string]any)["id"]) == p {
			t.Fatal("hidden post in someone else's feed")
		}
	}
	e.must(404, r1, "POST", fmt.Sprintf("/api/posts/%d/like", p), nil)
	e.must(404, r1, "POST", fmt.Sprintf("/api/posts/%d/comments", p), map[string]any{"body": "hi"})
	// A hidden comment disappears for others; its author still sees it.
	act(mod, 200, map[string]any{"action": "hide", "kind": "comment", "id": num(c["id"]), "reason": "Rude"})
	cPost := num(c["postId"])
	if list := e.must(200, other, "GET", fmt.Sprintf("/api/posts/%d/comments", cPost), nil)["comments"].([]any); len(list) != 0 {
		t.Fatalf("hidden comment visible to others: %v", list)
	}
	if list := e.must(200, owner, "GET", fmt.Sprintf("/api/posts/%d/comments", cPost), nil)["comments"].([]any); len(list) != 1 {
		t.Fatal("author should still see their hidden comment")
	}
	act(mod, 200, map[string]any{"action": "restore", "kind": "comment", "id": num(c["id"])})
	if list := e.must(200, other, "GET", fmt.Sprintf("/api/posts/%d/comments", cPost), nil)["comments"].([]any); len(list) != 1 {
		t.Fatal("restored comment should be back")
	}
	// Resolving closes every report about the post and tells each reporter.
	e.must(200, mod, "POST", "/api/mod/resolve", map[string]any{"kind": "post", "id": p, "outcome": "actioned", "note": "hidden"})
	e.must(404, mod, "POST", "/api/mod/resolve", map[string]any{"kind": "post", "id": p, "outcome": "actioned"})
	if k := e.kinds(r1ID); strings.Join(k, ",") != "moderation" {
		t.Fatalf("reporter told %v", k)
	}
	if e.count(`SELECT count(*) FROM reports WHERE status = 'actioned'`) != 3 {
		t.Fatal("all three reports should be resolved")
	}
	// Mute: no posting or commenting until the end time; likes still work.
	until := func(d string) string { return time.Now().Add(mustDur(d)).UTC().Format(time.RFC3339) }
	act(mod, 400, map[string]any{"action": "mute", "id": ownerID, "reason": "cool off", "until": until("-1h")})
	act(mod, 200, map[string]any{"action": "mute", "id": ownerID, "reason": "cool off", "until": until("24h")})
	code, out := e.do(owner, "POST", "/api/posts", map[string]any{"body": "still here"})
	if code != 403 || !strings.Contains(out["error"].(string), "muted until") {
		t.Fatalf("muted post: %d %v", code, out)
	}
	e.must(200, owner, "POST", fmt.Sprintf("/api/posts/%d/like", e.post(other, "x")), nil)
	act(mod, 200, map[string]any{"action": "unmute", "id": ownerID})
	e.post(owner, "back")
	// A mute that ended no longer applies.
	e.a.db.Exec(`UPDATE users SET muted_until = now() - interval '1 minute', mute_reason = 'old' WHERE id = $1`, ownerID)
	e.post(owner, "expired mute")
	// Suspend: signed out at once, sign-in explains, ends at the given time.
	act(mod, 200, map[string]any{"action": "suspend", "id": ownerID, "reason": "spam", "until": until("48h")})
	if code, _ := e.do(owner, "GET", "/api/posts", nil); code != 401 {
		t.Fatalf("suspended session still works: %d", code)
	}
	e.a.db.Exec(`UPDATE users SET pw_hash = $2 WHERE id = $1`, ownerID, hashPassword("owner-password"))
	code, out = e.do("", "POST", "/api/signin", map[string]any{"username": "owner", "password": "owner-password"})
	if code != 403 || !strings.Contains(out["error"].(string), "suspended until") {
		t.Fatalf("suspended sign-in: %d %v", code, out)
	}
	e.a.db.Exec(`UPDATE users SET suspended_until = now() - interval '1 minute' WHERE id = $1`, ownerID)
	e.must(200, "", "POST", "/api/signin", map[string]any{"username": "owner", "password": "owner-password"})
	owner = e.session(ownerID)
	// Rank: a moderator cannot act on another moderator or an admin, or on themself.
	m2ID, _ := e.user("mia", "moderator")
	act(mod, 403, map[string]any{"action": "mute", "id": m2ID, "reason": "x", "until": until("1h")})
	act(mod, 400, map[string]any{"action": "mute", "id": modID, "reason": "x", "until": until("1h")})
	act(r2, 403, map[string]any{"action": "hide", "kind": "post", "id": e.post(other, "y"), "reason": "x"})
	// Badges and stats: a revoked badge is not granted again; hidden stats drop out of the ranking.
	e.must(200, owner, "POST", "/api/achievements", map[string]any{"keys": []string{"streak-100"}})
	e.must(200, owner, "PUT", "/api/me", map[string]any{"stats": map[string]any{"xp": 999999, "level": 99}})
	act(mod, 200, map[string]any{"action": "revoke_badge", "id": ownerID, "key": "streak-100", "reason": "No sessions logged"})
	e.must(200, owner, "POST", "/api/achievements", map[string]any{"keys": []string{"streak-100"}})
	if e.count(`SELECT count(*) FROM achievements WHERE user_id = $1 AND key = 'streak-100'`, ownerID) != 0 {
		t.Fatal("revoked badge granted again")
	}
	// A branch badge, which ends in a capital letter, can be revoked like any other.
	e.must(200, owner, "POST", "/api/achievements", map[string]any{"keys": []string{"branch:nunchaku:F"}})
	act(mod, 200, map[string]any{"action": "revoke_badge", "id": ownerID, "key": "branch:nunchaku:F", "reason": "Levels set in one minute"})
	e.must(200, owner, "POST", "/api/achievements", map[string]any{"keys": []string{"branch:nunchaku:F"}})
	if e.count(`SELECT count(*) FROM achievements WHERE user_id = $1 AND key = 'branch:nunchaku:F'`, ownerID) != 0 {
		t.Fatal("revoked branch badge granted again")
	}
	act(mod, 200, map[string]any{"action": "hide_stats", "id": ownerID})
	people := e.must(200, other, "GET", "/api/users", nil)["users"].([]any)
	if top := people[0].(map[string]any); top["username"] == "owner" {
		t.Fatal("hidden stats still rank first")
	}
	// Every action above was logged once (two badges were revoked).
	for action, want := range map[string]int{"hide": 2, "restore": 1, "resolve": 1, "mute": 1, "unmute": 1, "suspend": 1, "revoke_badge": 2, "hide_stats": 1} {
		if n := e.count(`SELECT count(*) FROM mod_actions WHERE action = $1`, action); n != want {
			t.Errorf("%s logged %d times, want %d", action, n, want)
		}
	}
	// The audit log filters by person and action, for admins only.
	log := e.must(200, admin, "GET", "/api/admin/audit?actor=mo&action=hide", nil)["items"].([]any)
	if len(log) != 2 {
		t.Fatalf("audit filter: %d rows", len(log))
	}
	e.must(403, mod, "GET", "/api/admin/audit", nil)
}

func mustDur(s string) time.Duration { d, _ := time.ParseDuration(s); return d }

func TestDBDashboard(t *testing.T) {
	e := newEnv(t)
	adminID, admin := e.user("boss", "admin")
	bID, b := e.user("bea", "moderator")
	cID, _ := e.user("cal", "user")
	// cal has not been active for 20 days; bea and the admin just signed in (their sessions).
	e.a.db.Exec(`UPDATE sessions SET created_at = now() - interval '20 days' WHERE user_id = $1`, cID)
	// Practice sessions: two this week and one three weeks ago, plus a malformed date that must be ignored.
	today := time.Now().Format("2006-01-02")
	old := time.Now().AddDate(0, 0, -21).Format("2006-01-02")
	doc := fmt.Sprintf(`{"sessions":[{"date":"%s","minutes":20},{"date":"%s","minutes":10},{"date":"%s","minutes":5},{"date":"soon","minutes":1}]}`, today, today, old)
	if _, err := e.a.db.Exec(`INSERT INTO progress (user_id, prop, rev, state) VALUES ($1, 'nunchaku', 1, $2)`, bID, doc); err != nil {
		t.Fatal(err)
	}
	e.a.db.Exec(`INSERT INTO progress (user_id, prop, rev, state, updated_at) VALUES ($1, 'poi', 1, '{"sessions":"broken"}', now() - interval '20 days')`, cID)
	p := e.post(b, "this week")
	e.comment(admin, p, 0, "hi")
	old2 := e.post(b, "a month ago")
	e.a.db.Exec(`UPDATE posts SET created_at = now() - interval '28 days' WHERE id = $1`, old2)
	e.a.db.Exec(`INSERT INTO reports (reporter_id, target_kind, target_id, reason) VALUES ($1, 'post', $2, 'spam')`, adminID, p)
	e.must(403, e.session(cID), "GET", "/api/admin/dashboard", nil)
	e.a.version = "main.abc1234"
	out := e.must(200, b, "GET", "/api/admin/dashboard", nil)
	if out["version"] != "main.abc1234" {
		t.Fatalf("version: %v", out["version"])
	}
	tot := out["totals"].(map[string]any)
	if num(tot["users"]) != 3 || num(tot["moderators"]) != 1 || num(tot["admins"]) != 1 || num(tot["openReports"]) != 1 {
		t.Fatalf("totals: %v", tot)
	}
	// cal's fresh session from e.session above makes three active in the week.
	if num(tot["active7"]) != 3 || num(tot["active30"]) != 3 {
		t.Fatalf("active: %v", tot)
	}
	weeks := out["weeks"].([]any)
	if len(weeks) != 12 {
		t.Fatalf("%d weeks", len(weeks))
	}
	last := weeks[11].(map[string]any)
	if num(last["sessions"]) != 2 || num(last["posts"]) != 1 || num(last["comments"]) != 1 || num(last["reports"]) != 1 {
		t.Fatalf("this week: %v", last)
	}
	var sessions, posts int64
	for _, x := range weeks {
		sessions += num(x.(map[string]any)["sessions"])
		posts += num(x.(map[string]any)["posts"])
	}
	if sessions != 3 || posts != 2 {
		t.Fatalf("12-week totals: %d sessions, %d posts", sessions, posts)
	}
	// The count toward the fit of the suggestion weights: two pairs that someone marked Works or
	// Blocked, one of them first in the week before this one, and a third with only "needs a bridge",
	// which the fit does not read. TestDBTransitionCounts follows the marks that make these rows.
	if _, err := e.a.db.Exec(`INSERT INTO transition_counts (prop, a, b, works, bridge, blocked, first_week) VALUES
		('nunchaku', 'F5', 'F7', 2, 0, 0, (date_trunc('week', now()) - interval '1 week')::date),
		('nunchaku', 'F7', 'R1', 0, 1, 1, date_trunc('week', now())::date),
		('nunchaku', 'R1', 'F5', 0, 4, 0, NULL)`); err != nil {
		t.Fatal(err)
	}
	sug := e.must(200, admin, "GET", "/api/admin/dashboard", nil)["suggest"].(map[string]any)
	if num(sug["pairs"]) != 2 || num(sug["gate"]) != 30 || num(sug["recent"]) != 1 || sug["opens"] != gateOpens(2, 1, time.Now()) {
		t.Fatalf("the gate: %v, want 2 pairs of 30, 1 of them recent, and the week that pace reaches 30", sug)
	}
}

// TestDBAccounts covers the account rules of the fourth audit pull request: passwords nobody
// should pick, hashes made stronger at sign-in, names that show, and a last-active time the
// account keeps when its sessions are removed.
func TestDBAccounts(t *testing.T) {
	e := newEnv(t)
	_, admin := e.user("boss", "admin")
	_, mod := e.user("mona", "moderator")
	signup := func(want int, name, pw string) map[string]any {
		t.Helper()
		return e.must(want, "", "POST", "/api/signup", map[string]any{"username": name, "password": pw})
	}
	cookie := func(h http.Header) string {
		t.Helper()
		for _, c := range (&http.Response{Header: h}).Cookies() {
			if c.Name == cookieName && c.Value != "" {
				return c.Value
			}
		}
		t.Fatal("no session cookie")
		return ""
	}

	// SEC-11: the most used passwords are refused whatever the case, and so is the username.
	for _, pw := range []string{"password", "PASSWORD1", " qwerty123 ", "Sunshine1", "sandy.beach", "Sandy.Beach"} {
		if out := signup(400, "sandy.beach", pw); !strings.Contains(fmt.Sprint(out["error"]), "password") {
			t.Fatalf("sign-up with %q: %v", pw, out["error"])
		}
	}
	if out := signup(400, "sandy.beach", "short"); !strings.Contains(fmt.Sprint(out["error"]), "8 to 200") {
		t.Fatalf("a short password: %v", out["error"])
	}
	if e.count(`SELECT count(*) FROM users WHERE username = 'sandy.beach'`) != 0 {
		t.Fatal("a refused sign-up made an account")
	}
	_, _, hdr := e.doH("", "POST", "/api/signup", map[string]any{"username": "sandy.beach", "password": "wind on the dunes"}, nil)
	sandy := cookie(hdr)
	var sandyID int64
	if err := e.a.db.QueryRow(`SELECT id FROM users WHERE username = 'sandy.beach'`).Scan(&sandyID); err != nil {
		t.Fatal(err)
	}
	// The same rules when changing a password and when an admin sets one.
	change := func(next string) map[string]any { return map[string]any{"current": "wind on the dunes", "next": next} }
	e.must(400, sandy, "PUT", "/api/me/password", change("iloveyou1"))
	e.must(400, sandy, "PUT", "/api/me/password", change("SANDY.beach"))
	reset := fmt.Sprintf("/api/admin/users/%d/password", sandyID)
	e.must(400, admin, "PUT", reset, map[string]any{"next": "12345678"})
	e.must(400, admin, "PUT", reset, map[string]any{"next": "sandy.beach"})
	if out := e.must(200, sandy, "GET", "/api/me", nil); out["user"] == nil {
		t.Fatal("a refused reset signed the member out")
	}

	// SEC-11: a hash made with fewer rounds is made again at the next sign-in, once.
	hashOf := func() string {
		var h string
		if err := e.a.db.QueryRow(`SELECT pw_hash FROM users WHERE id = $1`, sandyID).Scan(&h); err != nil {
			t.Fatal(err)
		}
		return h
	}
	if hashRounds(hashOf()) != pwRounds {
		t.Fatalf("a new account is hashed with %d rounds", hashRounds(hashOf()))
	}
	weak := hashWith("wind on the dunes", 200000)
	e.a.db.Exec(`UPDATE users SET pw_hash = $2 WHERE id = $1`, sandyID, weak)
	in := func(pw string) map[string]any { return map[string]any{"username": "sandy.beach", "password": pw} }
	e.must(401, "", "POST", "/api/signin", in("wind on the dune"))
	if hashOf() != weak {
		t.Fatal("a wrong password changed the stored hash")
	}
	e.must(200, "", "POST", "/api/signin", in("wind on the dunes"))
	strong := hashOf()
	if hashRounds(strong) != pwRounds || !checkPassword(strong, "wind on the dunes") {
		t.Fatalf("after sign-in the hash has %d rounds", hashRounds(strong))
	}
	e.must(200, "", "POST", "/api/signin", in("wind on the dunes"))
	if hashOf() != strong {
		t.Fatal("a hash that is strong already was made again")
	}
	if out := e.must(200, sandy, "GET", "/api/me", nil); out["user"] == nil {
		t.Fatal("making the hash again signed the other device out")
	}

	// UN-12: names and bios keep only what shows.
	put := func(want int, body map[string]any) map[string]any {
		t.Helper()
		return e.must(want, sandy, "PUT", "/api/me", body)
	}
	name := func() string {
		return e.must(200, sandy, "GET", "/api/me", nil)["user"].(map[string]any)["displayName"].(string)
	}
	for _, bad := range []string{"\u200b\u200b", "\u3164", "   ", "...", "\u202eevil", strings.Repeat("x", 41)} {
		put(400, map[string]any{"displayName": bad})
	}
	if name() != "sandy.beach" {
		t.Fatalf("a refused name was stored: %q", name())
	}
	put(200, map[string]any{"displayName": "  Sandy\u200b   Beach\u200f "})
	if name() != "Sandy Beach" {
		t.Fatalf("name %q, want it cleaned", name())
	}
	put(400, map[string]any{"bio": "spins \u202e poi"})
	put(200, map[string]any{"bio": "\u200b\n  spins poi\u2060 "})
	if bio := e.must(200, sandy, "GET", "/api/me", nil)["user"].(map[string]any)["bio"]; bio != "spins poi" {
		t.Fatalf("bio %q", bio)
	}
	e.must(400, "", "POST", "/api/signup", map[string]any{"username": "ghost2", "password": "wind on the dunes", "displayName": "\u2800"})
	if e.count(`SELECT count(*) FROM users WHERE username = 'ghost2'`) != 0 {
		t.Fatal("an account with an empty-looking name was made")
	}

	// UN-16: the message names the service with the right article.
	out := put(400, map[string]any{"links": map[string]string{"instagram": "not a handle!"}})
	if !strings.Contains(fmt.Sprint(out["error"]), "an Instagram handle") {
		t.Fatalf("instagram: %v", out["error"])
	}
	out = put(400, map[string]any{"links": map[string]string{"tiktok": "not a handle!"}})
	if !strings.Contains(fmt.Sprint(out["error"]), "a TikTok handle") {
		t.Fatalf("tiktok: %v", out["error"])
	}

	// HP-10: last active is the account's own record, so a suspension, which removes the
	// sessions, does not blank it; and the hourly touch moves it on.
	lastActive := func(id int64) any {
		for _, x := range e.must(200, admin, "GET", "/api/admin/users", nil)["users"].([]any) {
			if m := x.(map[string]any); num(m["id"]) == id {
				return m["lastActive"]
			}
		}
		t.Fatalf("user %d is not in the list", id)
		return nil
	}
	if lastActive(sandyID) == nil {
		t.Fatal("no last active after signing up")
	}
	e.a.db.Exec(`UPDATE users SET last_seen = now() - interval '3 days' WHERE id = $1`, sandyID)
	e.a.db.Exec(`UPDATE sessions SET created_at = now() - interval '3 days', last_seen = now() - interval '2 hours' WHERE user_id = $1`, sandyID)
	e.must(200, sandy, "GET", "/api/me", nil)
	if e.count(`SELECT count(*) FROM users WHERE id = $1 AND last_seen > now() - interval '1 minute'`, sandyID) != 1 {
		t.Fatal("an hour of use did not move the account's last-seen time")
	}
	e.must(200, mod, "POST", "/api/mod/actions", map[string]any{"action": "suspend", "id": sandyID, "reason": "spam",
		"until": time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)})
	if e.count(`SELECT count(*) FROM sessions WHERE user_id = $1`, sandyID) != 0 {
		t.Fatal("a suspension should end the sessions")
	}
	got, _ := lastActive(sandyID).(string)
	when, err := time.Parse(time.RFC3339, got)
	if err != nil || time.Since(when) > time.Minute {
		t.Fatalf("last active after a suspension: %q", got)
	}
	tot := e.must(200, admin, "GET", "/api/admin/dashboard", nil)["totals"].(map[string]any)
	if num(tot["active7"]) != 3 {
		t.Fatalf("a suspended member who was here today counts as active: %v", tot["active7"])
	}
	// HP-09: the sign-in refusal carries the end time in a form the page can make local.
	code, sus := e.do("", "POST", "/api/signin", in("wind on the dunes"))
	msg := fmt.Sprint(sus["error"])
	if i := strings.Index(msg, "suspended until "); code != 403 || i < 0 {
		t.Fatalf("suspended sign-in: %d %v", code, msg)
	} else if _, err := time.Parse(time.RFC3339, strings.TrimSuffix(strings.Fields(msg[i+len("suspended until "):])[0], ".")); err != nil {
		t.Fatalf("the end time in %q is not RFC 3339", msg)
	}
}

// renameIn changes a skill id everywhere a catalog mentions it: the skill, needs and phases.
func renameIn(cat map[string]any, from, to string) {
	for _, x := range cat["skills"].([]any) {
		sk := x.(map[string]any)
		if sk["id"] == from {
			sk["id"], sk["name"] = to, "Renamed skill"
		}
		for i, n := range sk["needs"].([]any) {
			if strings.TrimSuffix(n.(string), "!") == from {
				sk["needs"].([]any)[i] = to + strings.TrimPrefix(n.(string), from)
			}
		}
	}
	for _, x := range cat["phases"].([]any) {
		p := x.(map[string]any)
		f := strings.Fields(p["skills"].(string))
		for i := range f {
			if f[i] == from {
				f[i] = to
			}
		}
		p["skills"] = strings.Join(f, " ")
		for i, r := range p["req"].([]any) {
			if strings.TrimSuffix(r.(string), "!") == from {
				p["req"].([]any)[i] = to + strings.TrimPrefix(r.(string), from)
			}
		}
	}
}

func TestDBCatalogs(t *testing.T) {
	e := newEnv(t)
	if err := e.a.seedCatalogs(); err != nil {
		t.Fatal(err)
	}
	if err := e.a.seedCatalogs(); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM catalog_revisions WHERE status = 'published'`); n != 5 {
		t.Fatalf("%d revisions after seeding twice, want 5", n)
	}
	_, admin := e.user("boss", "admin")
	modID, mod := e.user("mo", "moderator")
	userID, member := e.user("sam", "user")
	// Served from the database, with an ETag.
	code, cat := e.do("", "GET", "/catalog/poi.json", nil)
	if code != 200 || cat["id"] != "poi" {
		t.Fatalf("catalog file: %d", code)
	}
	e.must(404, "", "GET", "/catalog/drums.json", nil)
	// With its flag off the editor does not exist; it needs catalog.edit.
	e.setFlag("catalogEditor", false)
	e.must(404, admin, "GET", "/api/admin/catalog/poi", nil)
	e.setFlag("catalogEditor", true)
	e.must(403, member, "GET", "/api/admin/catalog/poi", nil)
	ed := e.must(200, mod, "GET", "/api/admin/catalog/poi", nil)
	if num(ed["rev"]) != 1 || ed["shippedUpdate"] != false {
		t.Fatalf("editor state: rev %v, shippedUpdate %v", ed["rev"], ed["shippedUpdate"])
	}
	pub := ed["published"].(map[string]any)
	leaf := pub["skills"].([]any)[1].(map[string]any)["id"].(string)
	// Someone has progress on the leaf skill.
	e.a.db.Exec(`INSERT INTO progress (user_id, prop, rev, state) VALUES ($1, 'poi', 1, $2)`, userID, fmt.Sprintf(`{"skills":{"%s":{"O":2}}}`, leaf))
	// A draft with a problem is saved, with the problem reported; publishing it is refused.
	broken := cloneJSON(pub)
	broken["skills"].([]any)[0].(map[string]any)["tier"] = 9
	out := e.must(200, mod, "PUT", "/api/admin/catalog/poi/draft", map[string]any{"body": broken})
	if len(out["errors"].([]any)) == 0 {
		t.Fatal("a broken draft should report errors")
	}
	e.must(403, mod, "POST", "/api/admin/catalog/poi/publish", map[string]any{})
	e.must(400, admin, "POST", "/api/admin/catalog/poi/publish", map[string]any{})
	// Removing a skill with progress is blocked, and the diff says how many people it touches.
	removed := cloneJSON(pub)
	keep := []any{}
	for _, x := range removed["skills"].([]any) {
		if x.(map[string]any)["id"] != leaf {
			keep = append(keep, x)
		}
	}
	removed["skills"] = keep
	e.must(200, mod, "PUT", "/api/admin/catalog/poi/draft", map[string]any{"body": removed})
	diff := e.must(200, mod, "GET", "/api/admin/catalog/poi/diff", nil)
	ch := diff["changes"].([]any)
	if len(ch) != 1 || ch[0].(map[string]any)["kind"] != "removed" || num(ch[0].(map[string]any)["people"]) != 1 || len(diff["blocked"].([]any)) == 0 {
		t.Fatalf("diff: %v", diff["_raw"])
	}
	e.must(400, admin, "POST", "/api/admin/catalog/poi/publish", map[string]any{})
	// Renaming it with an alias is safe; publishing makes revision 2 and is logged.
	renamed := cloneJSON(pub)
	newID := leaf[:1] + "99"
	renameIn(renamed, leaf, newID)
	renamed["aliases"] = map[string]any{leaf: newID}
	e.must(200, mod, "PUT", "/api/admin/catalog/poi/draft", map[string]any{"body": renamed})
	diff = e.must(200, mod, "GET", "/api/admin/catalog/poi/diff", nil)
	kinds := []string{}
	for _, c := range diff["changes"].([]any) {
		kinds = append(kinds, c.(map[string]any)["kind"].(string))
	}
	sort.Strings(kinds)
	if !strings.Contains(strings.Join(kinds, ","), "renamed") || strings.Contains(strings.Join(kinds, ","), "added") || len(diff["blocked"].([]any)) != 0 {
		t.Fatalf("rename diff: %v", diff["_raw"])
	}
	if whole := fmt.Sprint(diff["map"]); !strings.Contains(whole, "aliases") || strings.Contains(whole, "skills") {
		t.Fatalf("the rename adds an alias, a change to the map itself and not a skill: %v", whole)
	}
	if out := e.must(200, admin, "POST", "/api/admin/catalog/poi/publish", map[string]any{"note": "clearer name"}); num(out["rev"]) != 2 {
		t.Fatalf("publish: %v", out)
	}
	_, cat = e.do("", "GET", "/catalog/poi.json", nil)
	if cat["aliases"].(map[string]any)[leaf] != newID {
		t.Fatalf("published catalog lacks the alias: %v", cat["aliases"])
	}
	if e.count(`SELECT count(*) FROM catalog_revisions WHERE status = 'draft'`) != 0 || e.count(`SELECT count(*) FROM mod_actions WHERE action = 'catalog_publish' AND actor_id IS NOT NULL`) != 1 {
		t.Fatal("publishing should clear the draft and be logged once")
	}
	// The old id cannot come back as a skill while it is an alias.
	reuse := cloneJSON(renamed)
	delete(reuse, "aliases")
	reuse["skills"] = append(reuse["skills"].([]any), skillLike(reuse, leaf))
	e.must(200, mod, "PUT", "/api/admin/catalog/poi/draft", map[string]any{"body": reuse})
	e.must(400, admin, "POST", "/api/admin/catalog/poi/publish", map[string]any{})
	e.must(200, mod, "DELETE", "/api/admin/catalog/poi/draft", nil)
	// Once progress has moved to the new id, restoring revision 1 would orphan it, so it is refused.
	e.a.db.Exec(`UPDATE progress SET state = $2 WHERE user_id = $1`, userID, fmt.Sprintf(`{"skills":{"%s":{"O":2}}}`, newID))
	e.must(400, admin, "POST", "/api/admin/catalog/poi/revisions/1/restore", nil)
	e.a.db.Exec(`DELETE FROM progress WHERE user_id = $1`, userID)
	if out := e.must(200, admin, "POST", "/api/admin/catalog/poi/revisions/1/restore", nil); num(out["rev"]) != 3 {
		t.Fatalf("restore: %v", out)
	}
	// Shared concepts: naming one changes the map itself, ticking it changes the skill, and a concept
	// no skill names is a note, not a problem.
	_, live := e.do("", "GET", "/catalog/poi.json", nil)
	named := cloneJSON(live)
	delete(named, "_raw") // the test helper's copy of the response text is not part of the map
	named["concepts"] = map[string]any{"plane": "The flat sheet a circle is drawn in: wall, wheel or floor."}
	out = e.must(200, mod, "PUT", "/api/admin/catalog/poi/draft", map[string]any{"body": named})
	if len(out["errors"].([]any)) != 0 || !strings.Contains(fmt.Sprint(out["warnings"]), "no skill names it") {
		t.Fatalf("a concept no skill names should be a note: %v", out["_raw"])
	}
	diff = e.must(200, mod, "GET", "/api/admin/catalog/poi/diff", nil)
	if len(diff["changes"].([]any)) != 0 || fmt.Sprint(diff["map"]) != "[concepts]" {
		t.Fatalf("a named concept is a change to the map and to no skill: %v", diff["_raw"])
	}
	first := named["skills"].([]any)[0].(map[string]any)
	first["concepts"] = []any{"plane"}
	out = e.must(200, mod, "PUT", "/api/admin/catalog/poi/draft", map[string]any{"body": named})
	if len(out["errors"].([]any)) != 0 || strings.Contains(fmt.Sprint(out["warnings"]), "no skill names it") {
		t.Fatalf("a ticked concept: %v", out["_raw"])
	}
	diff = e.must(200, mod, "GET", "/api/admin/catalog/poi/diff", nil)
	if ch := diff["changes"].([]any); len(ch) != 1 || ch[0].(map[string]any)["id"] != first["id"] || fmt.Sprint(ch[0].(map[string]any)["fields"]) != "[concepts]" {
		t.Fatalf("a ticked concept is a change to that skill: %v", diff["_raw"])
	}
	first["concepts"] = []any{"weave"}
	if out = e.must(200, mod, "PUT", "/api/admin/catalog/poi/draft", map[string]any{"body": named}); !strings.Contains(fmt.Sprint(out["errors"]), "not one of this catalog's concepts") {
		t.Fatalf("a concept the map does not name should be a problem: %v", out["_raw"])
	}
	e.must(400, admin, "POST", "/api/admin/catalog/poi/publish", map[string]any{})
	e.must(200, mod, "DELETE", "/api/admin/catalog/poi/draft", nil)
	// Export: the catalog format, as a download that passes validation.
	r := httptest.NewRequest("GET", "/api/admin/catalog/poi/revisions/2", nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: mod})
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if errs, _ := validateCatalog(w.Body.Bytes()); w.Code != 200 || len(errs) > 0 || !strings.Contains(w.Header().Get("Content-Disposition"), "poi-revision-2.json") {
		t.Fatalf("export: %d %v %s", w.Code, errs, w.Header().Get("Content-Disposition"))
	}
	_ = modID
	// A newer shipped file is imported by itself while nobody has edited the catalog, and only offered
	// once someone has.
	e.a.db.Exec(`UPDATE catalog_revisions SET shipped_hash = 'older' WHERE prop = 'staff'`)
	e.a.seedCatalogs()
	if e.count(`SELECT max(rev) FROM catalog_revisions WHERE prop = 'staff'`) != 2 {
		t.Fatal("a changed shipped file should be imported as revision 2")
	}
	e.a.db.Exec(`UPDATE catalog_revisions SET shipped_hash = 'older' WHERE prop = 'poi'`)
	e.a.seedCatalogs()
	if e.count(`SELECT max(rev) FROM catalog_revisions WHERE prop = 'poi'`) != 3 {
		t.Fatal("an edited catalog must not be replaced by a shipped one")
	}
	if ed := e.must(200, admin, "GET", "/api/admin/catalog/poi", nil); ed["shippedUpdate"] != true {
		t.Fatal("the editor should offer the shipped version")
	}
	e.must(200, admin, "POST", "/api/admin/catalog/poi/shipped", nil)
	e.must(200, admin, "POST", "/api/admin/catalog/poi/publish", map[string]any{})
	if ed := e.must(200, admin, "GET", "/api/admin/catalog/poi", nil); ed["shippedUpdate"] != false {
		t.Fatal("once published, the shipped version is no longer offered")
	}
}

func cloneJSON(v map[string]any) map[string]any {
	b, _ := json.Marshal(v)
	out := map[string]any{}
	json.Unmarshal(b, &out)
	return out
}

func skillLike(cat map[string]any, id string) map[string]any {
	s := cloneJSON(cat["skills"].([]any)[0].(map[string]any))
	s["id"], s["needs"] = id, []any{}
	return s
}

func TestDBLinkCards(t *testing.T) {
	e := newEnv(t)
	srv, hits := stub(t)
	e.a.fetch = newFetcher(srv.URL, true)
	_, owner := e.user("owner", "user")
	_, mod := e.user("moe", "moderator")
	_, other := e.user("ola", "user")
	e.setFlag("moderation", true)
	post := func(tok string, links ...string) int64 {
		out := e.must(200, tok, "POST", "/api/posts", map[string]any{"body": "watch this", "links": links})
		return num(out["posts"].([]any)[0].(map[string]any)["id"])
	}
	card := func(tok string, id int64) map[string]any {
		p := e.must(200, tok, "GET", fmt.Sprintf("/api/posts/%d", id), nil)["posts"].([]any)[0].(map[string]any)
		return p["links"].([]any)[0].(map[string]any)
	}
	// Posting returns at once; the preview arrives in the background.
	start := time.Now()
	p := post(owner, "https://youtu.be/dQw4w9WgXcQ?si=tracking")
	if time.Since(start) > time.Second {
		t.Fatalf("posting waited %v for the preview", time.Since(start))
	}
	var c map[string]any
	for i := 0; i < 40; i++ {
		if c = card(owner, p); c["status"] == "ok" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if c["status"] != "ok" || c["title"] != "Figure-8 drill" || c["url"] != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" || !strings.HasPrefix(c["thumb"].(string), "api/previews/") || c["ratio"] != "16:9" {
		t.Fatalf("card: %v", c)
	}
	// The thumbnail is Flowa's own JPEG.
	r := httptest.NewRequest("GET", "/"+c["thumb"].(string), nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: other})
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || w.Body.Len() > maxThumbOut {
		t.Fatalf("thumbnail: %d %s %d bytes", w.Code, w.Header().Get("Content-Type"), w.Body.Len())
	}
	// The same link again is not fetched again.
	before := atomic.LoadInt64(hits)
	post(other, "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	time.Sleep(300 * time.Millisecond)
	if atomic.LoadInt64(hits) != before {
		t.Fatal("a known link was fetched again")
	}
	// Limits and refusals.
	e.must(400, owner, "POST", "/api/posts", map[string]any{"body": "x", "links": []string{"https://example.com/video"}})
	e.must(400, owner, "POST", "/api/posts", map[string]any{"body": "x", "links": []string{"https://youtu.be/aaaaaaaaaa1", "https://youtu.be/aaaaaaaaaa2", "https://youtu.be/aaaaaaaaaa3", "https://youtu.be/aaaaaaaaaa4", "https://youtu.be/aaaaaaaaaa5"}})
	// A preview that fails becomes a plain card, and the post is unaffected.
	slow := post(owner, "https://www.tiktok.com/@a/video/9999999999")
	for i := 0; i < 80; i++ {
		if c = card(owner, slow); c["status"] == "failed" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if c["status"] != "failed" || c["ratio"] != "9:16" {
		t.Fatalf("failed preview: %v", c)
	}
	// Hidden: the post's preview is no longer served to others.
	e.must(200, mod, "POST", "/api/mod/actions", map[string]any{"action": "hide", "kind": "post", "id": p, "reason": "spam"})
	thumb := card(owner, p)["thumb"].(string) // the author still sees the card
	// ola's own post links to the same video, so the thumbnail is still served to her; delete that post to check.
	e.a.db.Exec(`DELETE FROM posts WHERE user_id = (SELECT id FROM users WHERE username = 'ola')`)
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/"+thumb, nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: other})
	e.h.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatalf("preview of a hidden post served: %d", w.Code)
	}
	// With the flag off, links stay out of cards and nothing is fetched.
	e.setFlag("linkCards", false)
	e.a.flagsAt = time.Time{}
	before = atomic.LoadInt64(hits)
	out := e.must(200, owner, "POST", "/api/posts", map[string]any{"body": "plain link", "links": []string{"https://youtu.be/bbbbbbbbbbb"}})
	time.Sleep(300 * time.Millisecond)
	if _, has := out["posts"].([]any)[0].(map[string]any)["links"]; has || atomic.LoadInt64(hits) != before {
		t.Fatal("with linkCards off a link became a card or was fetched")
	}
}

func TestDBStaticCSP(t *testing.T) {
	e := newEnv(t)
	e.h = e.a.routes(staticOf(t, map[string]string{"index.html": "<!doctype html>"}))
	get := func() string {
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		return w.Header().Get("Content-Security-Policy")
	}
	e.setFlag("videoEmbeds", false)
	off := get()
	e.setFlag("videoEmbeds", true)
	e.a.flagsAt = time.Time{}
	if on := get(); off != csp(false) || on != csp(true) {
		t.Fatalf("CSP:\n%s\n%s", off, on)
	}
}

func TestDBTransitionCounts(t *testing.T) {
	e := newEnv(t)
	_, admin := e.user("boss", "admin")
	bID, b := e.user("bea", "user")
	_, c := e.user("cal", "user")
	label := map[string]any{"prop": "nunchaku", "deltas": []map[string]any{{"a": "F5", "b": "F7", "from": "untested", "to": "works"}}}
	e.setFlag("suggest", false)
	e.must(404, b, "POST", "/api/suggest/labels", label)
	e.setFlag("suggest", true)
	// Without consent, nothing is accepted.
	e.must(403, b, "POST", "/api/suggest/labels", label)
	e.must(200, b, "PUT", "/api/me", map[string]any{"prefs": map[string]bool{"shareTransitions": true}})
	e.must(200, c, "PUT", "/api/me", map[string]any{"prefs": map[string]bool{"shareTransitions": true}})
	e.must(200, b, "POST", "/api/suggest/labels", label)
	e.must(200, c, "POST", "/api/suggest/labels", label)
	// Being asked about transitions after a run is a preference of its own, kept apart from sharing,
	// and a preference the server does not know is refused.
	asked := e.must(200, c, "PUT", "/api/me", map[string]any{"prefs": map[string]bool{"askTransitions": false}})["user"].(map[string]any)["prefs"].(map[string]any)
	if asked["askTransitions"] != false || asked["shareTransitions"] != true {
		t.Fatalf("prefs after turning the question off: %v", asked)
	}
	e.must(400, c, "PUT", "/api/me", map[string]any{"prefs": map[string]bool{"askEverything": true}})
	// bea changes her mind; then withdraws (her browser sends the reverse change).
	e.must(200, b, "POST", "/api/suggest/labels", map[string]any{"prop": "nunchaku", "deltas": []map[string]any{{"a": "F5", "b": "F7", "from": "works", "to": "blocked"}}})
	got := e.must(200, c, "GET", "/api/suggest/counts?prop=nunchaku", nil)["counts"].([]any)
	if len(got) != 1 || num(got[0].(map[string]any)["works"]) != 1 || num(got[0].(map[string]any)["blocked"]) != 1 {
		t.Fatalf("counts: %v", got)
	}
	// The pair counts toward the fit since this week, and the dashboard says so: one pair of the 30
	// needed, none of them from the four whole weeks before this one, so no date yet.
	thisWeek := `SELECT count(*) FROM transition_counts WHERE a = 'F5' AND b = 'F7' AND first_week = date_trunc('week', now())::date`
	if e.count(thisWeek) != 1 {
		t.Fatal("a pair with a Works or Blocked mark remembers the week it first had one")
	}
	if sug := e.must(200, admin, "GET", "/api/admin/dashboard", nil)["suggest"].(map[string]any); num(sug["pairs"]) != 1 || num(sug["gate"]) != gatePairs || num(sug["recent"]) != 0 || sug["opens"] != "" {
		t.Fatalf("the gate with one pair: %v", sug)
	}
	// A mark that only says "needs a bridge" is not one the fit reads: it does not count, and has no week.
	e.must(200, b, "POST", "/api/suggest/labels", map[string]any{"prop": "nunchaku", "deltas": []map[string]any{{"a": "F7", "b": "R1", "from": "untested", "to": "bridge"}}})
	if e.count(`SELECT count(*) FROM transition_counts WHERE a = 'F7' AND b = 'R1' AND first_week IS NULL`) != 1 {
		t.Fatal("a pair with only a bridge mark has no first week")
	}
	e.must(200, b, "POST", "/api/suggest/labels", map[string]any{"prop": "nunchaku", "deltas": []map[string]any{{"a": "F7", "b": "R1", "from": "bridge", "to": "untested"}}})
	e.must(200, b, "POST", "/api/suggest/labels", map[string]any{"prop": "nunchaku", "deltas": []map[string]any{{"a": "F5", "b": "F7", "from": "blocked", "to": "untested"}}})
	// Counts never go below zero, whatever a client sends.
	e.must(200, b, "POST", "/api/suggest/labels", map[string]any{"prop": "nunchaku", "deltas": []map[string]any{{"a": "F5", "b": "F7", "from": "blocked", "to": "untested"}}})
	if n := e.count(`SELECT blocked FROM transition_counts WHERE a = 'F5' AND b = 'F7'`); n != 0 {
		t.Fatalf("blocked = %d", n)
	}
	// No column of the table can say who marked what.
	cols := e.count(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'transition_counts' AND table_schema = current_schema() AND column_name LIKE '%user%'`)
	if cols != 0 {
		t.Fatal("transition_counts must not have a user column")
	}
	e.must(400, b, "POST", "/api/suggest/labels", map[string]any{"prop": "nunchaku", "deltas": []map[string]any{{"a": "F5", "b": "<b>", "from": "untested", "to": "works"}}})
	e.must(400, b, "GET", "/api/suggest/counts", nil)
	if all := e.must(200, admin, "GET", "/api/suggest/counts", nil)["counts"].([]any); len(all) != 1 {
		t.Fatalf("export: %v", all)
	}
	// Turning sharing off first and then withdrawing (what the profile page does) takes the mark out;
	// without consent nothing else is accepted.
	e.must(200, c, "PUT", "/api/me", map[string]any{"prefs": map[string]bool{"shareTransitions": false}})
	e.must(403, c, "POST", "/api/suggest/labels", label)
	e.must(403, c, "POST", "/api/suggest/labels", map[string]any{"prop": "nunchaku", "deltas": []map[string]any{{"a": "F5", "b": "F7", "from": "works", "to": "untested"}, {"a": "F5", "b": "F8", "from": "untested", "to": "works"}}})
	e.must(200, c, "POST", "/api/suggest/labels", map[string]any{"prop": "nunchaku", "deltas": []map[string]any{{"a": "F5", "b": "F7", "from": "works", "to": "untested"}}})
	if got := e.must(200, c, "GET", "/api/suggest/counts?prop=nunchaku", nil)["counts"].([]any); len(got) != 0 {
		t.Fatalf("after withdrawal: %v", got)
	}
	// With the last mark withdrawn the pair no longer counts, and its week is forgotten with it.
	if e.count(`SELECT count(*) FROM transition_counts WHERE first_week IS NOT NULL`) != 0 {
		t.Fatal("a pair nobody marks any more keeps no week")
	}
	if sug := e.must(200, admin, "GET", "/api/admin/dashboard", nil)["suggest"].(map[string]any); num(sug["pairs"]) != 0 {
		t.Fatalf("the gate after withdrawal: %v", sug)
	}
	// Twelve pairs, four of them first marked in the last four whole weeks: a pair a week, so the
	// eighteen still missing take eighteen weeks. One marked this week and one from before the column
	// existed count as pairs and not toward the pace.
	if _, err := e.a.db.Exec(`INSERT INTO transition_counts (prop, a, b, works, blocked, first_week)
		SELECT 'poi', 'A' || g, 'B' || g, g % 2, 1 - g % 2,
			CASE WHEN g <= 4 THEN (date_trunc('week', now()) - g * interval '1 week')::date WHEN g = 5 THEN date_trunc('week', now())::date
				WHEN g = 6 THEN NULL ELSE (date_trunc('week', now()) - (g + 3) * interval '1 week')::date END
		FROM generate_series(1, 12) g`); err != nil {
		t.Fatal(err)
	}
	e.a.db.Exec(`INSERT INTO transition_counts (prop, a, b, bridge) VALUES ('poi', 'A1', 'B2', 3)`) // bridge only: not a pair for the fit
	sug := e.must(200, admin, "GET", "/api/admin/dashboard", nil)["suggest"].(map[string]any)
	if num(sug["pairs"]) != 12 || num(sug["recent"]) != 4 || sug["opens"] != gateOpens(12, 4, time.Now()) || sug["opens"] == "" {
		t.Fatalf("the gate with twelve pairs: %v", sug)
	}
	e.a.db.Exec(`DELETE FROM transition_counts WHERE prop = 'poi'`)
	_ = bID
	// Usage counters feed the dashboard.
	e.must(200, b, "POST", "/api/suggest/usage", map[string]any{"shown": 5, "added": 2, "worked": 1})
	e.must(200, c, "POST", "/api/suggest/usage", map[string]any{"shown": 3, "added": 1, "worked": 0})
	e.must(400, c, "POST", "/api/suggest/usage", map[string]any{"shown": -1})
	sug = e.must(200, admin, "GET", "/api/admin/dashboard", nil)["suggest"].(map[string]any)
	if num(sug["shown"]) != 8 || num(sug["added"]) != 3 || num(sug["worked"]) != 1 {
		t.Fatalf("usage: %v", sug)
	}
}

func TestDBAvatars(t *testing.T) {
	e := newEnv(t)
	bID, b := e.user("bea", "user")
	_, c := e.user("cal", "user")
	cfg := map[string]any{"v": 1, "body": "tall", "build": "lean", "skin": "#46271a", "hair": "curly", "props": map[string]any{"nunchaku": "#e0533a"}}
	e.must(200, b, "PUT", "/api/me", map[string]any{"avatarCfg": cfg})
	got := e.must(200, c, "GET", "/api/users/bea", nil)
	if av := got["user"].(map[string]any)["avatarCfg"].(map[string]any); av["hair"] != "curly" || av["build"] != "lean" {
		t.Fatalf("avatar on the profile: %v", av)
	}
	// Refused: an unknown option, a colour off its palette, a body that does not exist; nothing changes.
	for _, bad := range []map[string]any{{"v": 1, "hair": "mohawk"}, {"v": 1, "skin": "#00ff00"}, {"v": 1, "body": "giant"}} {
		e.must(400, b, "PUT", "/api/me", map[string]any{"avatarCfg": bad})
	}
	if n := e.count(`SELECT count(*) FROM users WHERE id = $1 AND avatar_cfg->>'hair' = 'curly'`, bID); n != 1 {
		t.Fatal("a refused avatar must leave the saved one")
	}
	// Clean skills are listed only with avatars on, and not for someone whose stats were hidden.
	e.setFlag("avatars", false)
	e.must(200, b, "PUT", "/api/progress/nunchaku", map[string]any{"rev": 0, "state": map[string]any{"skills": map[string]any{
		"F5": map[string]any{"O": 2}, "F3": map[string]any{"O": 1}, "F4": map[string]any{"L": 0, "R": 3}, "F6": map[string]any{"note": "x"}}}})
	if _, ok := e.must(200, c, "GET", "/api/users/bea", nil)["clean"]; ok {
		t.Fatal("clean skills listed with avatars off")
	}
	e.setFlag("avatars", true)
	clean := e.must(200, c, "GET", "/api/users/bea", nil)["clean"].(map[string]any)["nunchaku"].([]any)
	if len(clean) != 2 || clean[0] != "F4" || clean[1] != "F5" {
		t.Fatalf("clean: %v", clean)
	}
	e.a.db.Exec(`UPDATE users SET stats_hidden = true WHERE id = $1`, bID)
	if _, ok := e.must(200, c, "GET", "/api/users/bea", nil)["clean"]; ok {
		t.Fatal("clean skills listed for hidden stats")
	}
	// An empty object removes the avatar.
	e.must(200, b, "PUT", "/api/me", map[string]any{"avatarCfg": map[string]any{}})
	if n := e.count(`SELECT count(*) FROM users WHERE id = $1 AND avatar_cfg = '{}'`, bID); n != 1 {
		t.Fatal("the avatar was not removed")
	}
}

func TestDBHousekeeping(t *testing.T) {
	e := newEnv(t)
	uid, tok := e.user("hana", "user")
	e.a.db.Exec(`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ('old', $1, now() - interval '1 hour')`, uid)
	e.a.db.Exec(`INSERT INTO notifications (user_id, kind, read, created_at) VALUES ($1, 'like', true, now() - interval '200 days'), ($1, 'like', false, now() - interval '200 days'), ($1, 'like', true, now())`, uid)
	pid := e.post(tok, "with a link")
	e.a.db.Exec(`UPDATE posts SET data = '{"links":[{"url":"https://youtu.be/kept"}]}' WHERE id = $1`, pid)
	e.a.db.Exec(`INSERT INTO link_previews (url, site, kind, fetched_at) VALUES ('https://youtu.be/kept', 'youtube', 'video', now() - interval '3 days'),
		('https://youtu.be/gone', 'youtube', 'video', now() - interval '3 days'), ('https://youtu.be/new', 'youtube', 'video', now())`)
	n, err := e.a.housekeep()
	if err != nil {
		t.Fatal(err)
	}
	if n["sessions"] != 1 || n["notifications"] != 1 || n["previews"] != 1 {
		t.Fatalf("removed: %v", n)
	}
	// The live session, unread and recent notifications, and previews in use or just fetched stay.
	e.must(200, tok, "GET", "/api/me", nil)
	if c := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1`, uid); c != 2 {
		t.Fatalf("notifications left: %d", c)
	}
	if c := e.count(`SELECT count(*) FROM link_previews WHERE url IN ('https://youtu.be/kept', 'https://youtu.be/new')`); c != 2 {
		t.Fatalf("previews left: %d", c)
	}
}

func TestDBHardening(t *testing.T) {
	e := newEnv(t)
	adminID, admin := e.user("boss", "admin")
	admin2ID, admin2 := e.user("boss2", "admin")
	modID, mod := e.user("mona", "moderator")
	_, mod2 := e.user("milo", "moderator")
	userID, member := e.user("una", "user")

	// Deleting someone else's post or comment needs a higher role (S2).
	adminPost := e.post(admin, "admin's post")
	e.must(404, mod, "DELETE", fmt.Sprintf("/api/posts/%d", adminPost), nil)
	modPost := e.post(mod2, "a moderator's post")
	e.must(404, mod, "DELETE", fmt.Sprintf("/api/posts/%d", modPost), nil)
	e.must(200, admin, "DELETE", fmt.Sprintf("/api/posts/%d", modPost), nil)
	memberPost := e.post(member, "a member's post")
	adminComment := num(e.comment(admin, memberPost, 0, "from the admin")["id"])
	e.must(404, mod, "DELETE", fmt.Sprintf("/api/comments/%d", adminComment), nil)
	e.must(200, member, "DELETE", fmt.Sprintf("/api/comments/%d", adminComment), nil) // the post's owner may
	memberComment := num(e.comment(member, adminPost, 0, "from a member")["id"])
	e.must(200, mod, "DELETE", fmt.Sprintf("/api/comments/%d", memberComment), nil)
	ownPost := e.post(mod, "mine")
	e.must(200, mod, "DELETE", fmt.Sprintf("/api/posts/%d", ownPost), nil)

	// One admin cannot reset another's password, delete them, or reset their own here (S3).
	pw := map[string]any{"next": "a new password 123"}
	e.must(404, admin, "PUT", fmt.Sprintf("/api/admin/users/%d/password", admin2ID), pw)
	e.must(400, admin, "PUT", fmt.Sprintf("/api/admin/users/%d/password", adminID), pw)
	e.must(200, admin, "PUT", fmt.Sprintf("/api/admin/users/%d/password", modID), pw)
	e.must(403, admin, "DELETE", fmt.Sprintf("/api/admin/users/%d", admin2ID), nil)
	e.must(200, admin2, "GET", "/api/me", nil)

	// A commenter cannot keep reading or writing in a thread on a hidden post (S10).
	p := e.post(member, "soon hidden")
	_, carl := e.user("carl", "user")
	root := num(e.comment(carl, p, 0, "first")["id"])
	e.comment(member, p, root, "reply")
	e.a.db.Exec(`UPDATE posts SET hidden_at = now(), hidden_reason = 'spam' WHERE id = $1`, p)
	e.must(404, carl, "GET", fmt.Sprintf("/api/comments/%d/replies", root), nil)
	e.must(404, carl, "POST", fmt.Sprintf("/api/comments/%d/like", root), nil)
	e.must(200, member, "GET", fmt.Sprintf("/api/comments/%d/replies", root), nil)

	// Only known badges, and not while muted (S7).
	e.must(400, member, "POST", "/api/achievements", map[string]any{"keys": []string{"made-up-badge"}, "prop": "nunchaku"})
	e.must(200, member, "POST", "/api/achievements", map[string]any{"keys": []string{"sessions-1"}, "prop": "nunchaku"})
	// The badges the page builds from a prop's map: its roadmap and each branch with a skill in use.
	// A batch with one unknown key grants nothing.
	had := e.count(`SELECT count(*) FROM achievements WHERE user_id = $1`, userID)
	for _, bad := range []string{"gates:unicycle", "gates", "gates:nunchaku:F", "branch:nunchaku", "branch:nunchaku:Z", "branch:nunchaku:f", "branch:nunchaku:FR", "branch:unicycle:F", "trophy:nunchaku"} {
		e.must(400, member, "POST", "/api/achievements", map[string]any{"keys": []string{"sessions-10", bad}, "prop": "nunchaku"})
	}
	if e.count(`SELECT count(*) FROM achievements WHERE user_id = $1`, userID) != had {
		t.Fatal("a batch with an unknown badge should grant nothing")
	}
	perProp := e.must(200, member, "POST", "/api/achievements", map[string]any{"keys": []string{"gates:nunchaku", "branch:nunchaku:F", "branch:poi:" + poiBranch(t, e)}, "prop": "nunchaku"})
	if len(perProp["new"].([]any)) != 3 {
		t.Fatalf("the roadmap and branch badges of a prop should be granted: %v", perProp["_raw"])
	}
	e.a.db.Exec(`UPDATE users SET muted_until = now() + interval '1 hour' WHERE id = $1`, userID)
	e.must(403, member, "POST", "/api/achievements", map[string]any{"keys": []string{"sessions-10"}, "prop": "nunchaku"})
	e.a.db.Exec(`UPDATE users SET muted_until = NULL WHERE id = $1`, userID)

	// Allowances per hour (S8).
	e.a.lim.posts = newLimiter(2, time.Hour)
	_, fast := e.user("fast", "user")
	e.post(fast, "one")
	e.post(fast, "two")
	e.must(429, fast, "POST", "/api/posts", map[string]any{"body": "three"})
	// A post the server turns down costs a tenth, so thirty mistakes leave room to post (UN-06).
	e.a.lim.posts = newLimiter(30, time.Hour)
	_, typo := e.user("typo", "user")
	for i := 0; i < 30; i++ {
		e.must(400, typo, "POST", "/api/posts", map[string]any{"body": strings.Repeat("x", maxPost+1)})
	}
	e.post(typo, "the one that fits")
	// Likes and comments on a post that is not there cost a tenth as well.
	e.a.lim.likes, e.a.lim.comments = newLimiter(2, time.Hour), newLimiter(2, time.Hour)
	for i := 0; i < 10; i++ {
		e.must(404, typo, "POST", "/api/posts/999999/like", nil)
		e.must(400, typo, "POST", fmt.Sprintf("/api/posts/%d/comments", memberPost), map[string]any{"body": ""})
	}
	e.must(200, typo, "POST", fmt.Sprintf("/api/posts/%d/like", memberPost), nil)
	e.comment(typo, memberPost, 0, "one comment after ten empty ones")
	e.must(429, typo, "POST", fmt.Sprintf("/api/posts/%d/like", memberPost), nil)
	e.must(429, typo, "POST", fmt.Sprintf("/api/posts/%d/comments", memberPost), map[string]any{"body": "too many"})

	// Progress documents up to 512 KB (S6).
	big := strings.Repeat("x", maxProgress)
	if code, _ := e.do(member, "PUT", "/api/progress/nunchaku", map[string]any{"rev": 0, "state": map[string]any{"notes": big}}); code < 400 {
		t.Fatalf("a progress document over the limit was taken (%d)", code)
	}

	// Pictures: cleaned on the way in, and served by URL to others (S9, S6).
	pic := dataURL(t, "jpeg", 40, 40, []byte("GPS 1,2"))
	e.must(200, member, "PUT", "/api/me", map[string]any{"avatar": pic})
	e.must(400, member, "PUT", "/api/me", map[string]any{"avatar": "data:image/svg+xml;base64,PHN2Zy8+"})
	list := e.must(200, admin, "GET", "/api/users", nil)["users"].([]any)
	var url string
	for _, x := range list {
		if m := x.(map[string]any); m["username"] == "una" {
			url = m["avatar"].(string)
		}
	}
	if !strings.HasPrefix(url, fmt.Sprintf("api/users/%d/avatar?v=", userID)) {
		t.Fatalf("the people list should carry a picture URL, got %.40q", url)
	}
	r := httptest.NewRequest("GET", "/"+url, nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: admin})
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || bytes.Contains(w.Body.Bytes(), []byte("GPS")) {
		t.Fatalf("picture: %d %q", w.Code, w.Header().Get("Content-Type"))
	}

	// Headers on API responses, and the Host check (S4, S11).
	r = httptest.NewRequest("GET", "/api/me", nil)
	w = httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	for k, v := range map[string]string{"X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "Cross-Origin-Opener-Policy": "same-origin"} {
		if w.Header().Get(k) != v {
			t.Errorf("%s: %q", k, w.Header().Get(k))
		}
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Error("API responses need frame-ancestors 'none'")
	}
	r = httptest.NewRequest("POST", "/api/signup", strings.NewReader(`{"username":"rebind","password":"long enough pw"}`))
	r.Host = "rebind.attacker.example"
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != http.StatusMisdirectedRequest || e.count(`SELECT count(*) FROM users WHERE username = 'rebind'`) != 0 {
		t.Fatalf("a request for a foreign name must be refused: %d", w.Code)
	}

	// Sessions: sign out other devices, and an unused session ends (S12).
	other := e.session(userID)
	got := e.must(200, member, "POST", "/api/me/signout-others", nil)
	if num(got["ended"]) < 1 {
		t.Fatalf("ended: %v", got)
	}
	signedIn := func(tok string) bool { return e.must(200, tok, "GET", "/api/me", nil)["user"] != nil }
	if signedIn(other) || !signedIn(member) {
		t.Fatal("only the other device is signed out")
	}
	idle := e.session(userID)
	e.a.db.Exec(`UPDATE sessions SET last_seen = now() - interval '15 days' WHERE token_hash = $1`, tokenHash(idle))
	if signedIn(idle) {
		t.Fatal("a session unused for 15 days must end")
	}
	_ = adminID
}

func TestDBSigninLimits(t *testing.T) {
	e := newEnv(t)
	out := logged(t)
	e.must(200, "", "POST", "/api/signup", map[string]any{"username": "vic", "password": "the right password"})
	bad := map[string]any{"username": "vic", "password": "a-wrong-guess"}
	right := map[string]any{"username": "vic", "password": "the right password"}
	// Per address and username: 8 failures, then even the right password waits (S5).
	for i := 0; i < maxFails; i++ {
		e.must(401, "", "POST", "/api/signin", bad)
	}
	e.must(429, "", "POST", "/api/signin", right)
	e.must(429, "", "POST", "/api/signin", right)
	// The log has one line for the run of failures and one for the refusals, with the address and
	// neither the username nor a password (SEC-15).
	if got := out.String(); strings.Count(got, "refused: wrong username or password from 192.0.2.1\n") != 1 ||
		strings.Count(got, "refused: too many sign-in attempts from 192.0.2.1\n") != 1 || strings.Count(got, "refused:") != 2 ||
		strings.Contains(got, "vic") || strings.Contains(got, "a-wrong-guess") || strings.Contains(got, "the right password") {
		t.Fatalf("the log after a run of failed sign-ins:\n%s", got)
	}
	// Per account: 20 wrong guesses, the 8 above and 12 more from 12 addresses, do not lock the member
	// out (SEC-04). A wrong password from a new address is told to wait, the right one signs in, and
	// that clears the count.
	for i := 0; i < 12; i++ {
		if code := e.from(fmt.Sprintf("198.51.100.%d:5000", i+1), "", "POST", "/api/signin", bad); code != 401 {
			t.Fatalf("guess %d from its own address: %d", i+1, code)
		}
	}
	if code := e.from("198.51.100.21:5000", "", "POST", "/api/signin", bad); code != 429 {
		t.Fatalf("a wrong password for an account under attack: %d, want 429", code)
	}
	if code := e.from("198.51.100.21:5000", "", "POST", "/api/signin", right); code != 200 {
		t.Fatalf("the right password for an account under attack: %d, want 200", code)
	}
	if code := e.from("198.51.100.22:5000", "", "POST", "/api/signin", bad); code != 401 {
		t.Fatalf("after a good sign-in the count starts again: %d, want 401", code)
	}
	// A wrong password while the account is over its count still counts against the address.
	e.a.lim.account = newLimiter(1, failWindow)
	for i := 0; i < maxFails; i++ {
		e.from("198.51.100.30:5000", "", "POST", "/api/signin", bad)
	}
	if code := e.from("198.51.100.30:5000", "", "POST", "/api/signin", right); code != 429 {
		t.Fatalf("an address that kept guessing is refused even with the right password: %d", code)
	}
	e.a.lim.account = newLimiter(20, failWindow)
	// Per address across usernames: spraying many accounts is limited too.
	e.a.lim.signin = newLimiter(maxFails, failWindow)
	e.a.lim.signinIP = newLimiter(3, failWindow)
	for _, n := range []string{"amy", "ben", "cat"} {
		e.must(401, "", "POST", "/api/signin", map[string]any{"username": n, "password": "guess guess"})
	}
	e.must(429, "", "POST", "/api/signin", map[string]any{"username": "dan", "password": "guess guess"})
	// Sign-ups per address.
	e.a.lim.signup = newLimiter(1, time.Hour)
	e.must(200, "", "POST", "/api/signup", map[string]any{"username": "one1", "password": "long enough pw"})
	e.must(429, "", "POST", "/api/signup", map[string]any{"username": "two2", "password": "long enough pw"})
	// Wrong current passwords on the profile.
	vid := int64(e.count(`SELECT id FROM users WHERE username = 'vic'`))
	tok := e.session(vid)
	e.a.lim.password = newLimiter(2, failWindow)
	for i := 0; i < 2; i++ {
		e.must(403, tok, "PUT", "/api/me/password", map[string]any{"current": "nope nope", "next": "another password"})
	}
	e.must(429, tok, "PUT", "/api/me/password", map[string]any{"current": "the right password", "next": "another password"})
}

// Behind a reverse proxy every connection comes from the proxy; the limits must still count each
// visitor on their own (the feedback after the first server deploy).
func TestDBLimitsBehindProxy(t *testing.T) {
	e := newEnv(t)
	via := func(visitor, method, path string, body any) int {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.RemoteAddr = "172.18.0.2:41000" // the proxy, in the Docker network
		r.Header.Set("X-Forwarded-For", visitor)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		return w.Code
	}
	e.a.lim.signup = newLimiter(1, time.Hour)
	if via("203.0.113.1", "POST", "/api/signup", map[string]any{"username": "first", "password": "long enough pw"}) != 200 ||
		via("203.0.113.2", "POST", "/api/signup", map[string]any{"username": "second", "password": "long enough pw"}) != 200 {
		t.Fatal("two visitors behind one proxy each get their own sign-up allowance")
	}
	if via("203.0.113.1", "POST", "/api/signup", map[string]any{"username": "third", "password": "long enough pw"}) != 429 {
		t.Fatal("the same visitor is still limited")
	}
	// One visitor guessing passwords does not lock everyone else out.
	e.a.lim.signinIP = newLimiter(3, failWindow)
	for _, n := range []string{"amy", "ben", "cat", "dan"} {
		via("198.51.100.66", "POST", "/api/signin", map[string]any{"username": n, "password": "guess guess"})
	}
	if via("198.51.100.66", "POST", "/api/signin", map[string]any{"username": "first", "password": "long enough pw"}) != 429 {
		t.Fatal("the guessing visitor is blocked")
	}
	if via("203.0.113.2", "POST", "/api/signin", map[string]any{"username": "second", "password": "long enough pw"}) != 200 {
		t.Fatal("another visitor still signs in")
	}
	// IPv6: every address in one /64 is one visitor, and the next /64 is another (SEC-05).
	for i, n := range []string{"amy", "ben", "cat", "dan"} {
		via(fmt.Sprintf("2001:db8:7:1::%x", i+1), "POST", "/api/signin", map[string]any{"username": n, "password": "guess guess"})
	}
	if via("2001:db8:7:1:ffff::9", "POST", "/api/signin", map[string]any{"username": "first", "password": "long enough pw"}) != 429 {
		t.Fatal("a new address in the same /64 is the same visitor")
	}
	if via("2001:db8:7:2::1", "POST", "/api/signin", map[string]any{"username": "first", "password": "long enough pw"}) != 200 {
		t.Fatal("the next /64 is someone else")
	}
	e.a.lim.signup = newLimiter(1, time.Hour)
	if via("2001:db8:8:1::1", "POST", "/api/signup", map[string]any{"username": "sixth", "password": "long enough pw"}) != 200 ||
		via("2001:db8:8:1::2", "POST", "/api/signup", map[string]any{"username": "seventh", "password": "long enough pw"}) != 429 {
		t.Fatal("sign-ups are counted per /64")
	}
}

// Sign-ups count what costs something: a new account is one, a taken name a tenth, and a form the
// server turns down nothing (UN-05).
func TestDBSignupLimits(t *testing.T) {
	e := newEnv(t)
	out := logged(t)
	pw := "long enough pw"
	for _, bad := range []map[string]any{
		{"username": "x", "password": pw}, {"username": "has space", "password": pw}, {"username": "fine", "password": "short"},
		{"username": "fine", "password": pw, "displayName": strings.Repeat("d", 41)}, {"username": "", "password": ""},
		{"username": "x", "password": pw}, {"username": "x", "password": pw}, {"username": "x", "password": pw},
		{"username": "x", "password": pw}, {"username": "x", "password": pw},
	} {
		e.must(400, "", "POST", "/api/signup", bad)
	}
	e.must(200, "", "POST", "/api/signup", map[string]any{"username": "first", "password": pw})
	for i := 0; i < 10; i++ {
		e.must(409, "", "POST", "/api/signup", map[string]any{"username": "first", "password": pw})
	}
	// One account and ten taken names are two of the ten: eight more accounts, and then no more.
	for i := 0; i < 8; i++ {
		e.must(200, "", "POST", "/api/signup", map[string]any{"username": fmt.Sprintf("more%d", i), "password": pw})
	}
	e.must(429, "", "POST", "/api/signup", map[string]any{"username": "eleventh", "password": pw})
	e.must(429, "", "POST", "/api/signup", map[string]any{"username": "x", "password": pw}) // refused before it is read
	if n := e.count(`SELECT count(*) FROM users`); n != 9 {
		t.Fatalf("%d accounts, want 9", n)
	}
	if got := out.String(); strings.Count(got, "refused: too many new accounts from 192.0.2.1\n") != 1 || strings.Count(got, "refused:") != 1 {
		t.Fatalf("the log after refused sign-ups:\n%s", got)
	}
	// Another address has its own count.
	if code := e.from("203.0.113.77:5000", "", "POST", "/api/signup", map[string]any{"username": "elsewhere", "password": pw}); code != 200 {
		t.Fatalf("another address: %d", code)
	}
}

// The community and game flags turn the API off too, not only the menu (MAP-03).
func TestDBFlagsGateAPI(t *testing.T) {
	e := newEnv(t)
	uid, una := e.user("una", "user")
	p := e.post(una, "before the flag goes off")
	c := num(e.comment(una, p, 0, "a comment")["id"])
	e.must(200, una, "PUT", "/api/me", map[string]any{"avatar": dataURL(t, "jpeg", 40, 40, nil)})
	community := [][2]string{
		{"GET", "/api/users"}, {"GET", "/api/users/una"},
		{"GET", "/api/posts"}, {"POST", "/api/posts"}, {"GET", fmt.Sprintf("/api/posts/%d", p)}, {"DELETE", fmt.Sprintf("/api/posts/%d", p)},
		{"POST", fmt.Sprintf("/api/posts/%d/like", p)}, {"POST", fmt.Sprintf("/api/posts/%d/comments", p)}, {"GET", fmt.Sprintf("/api/posts/%d/comments", p)},
		{"DELETE", fmt.Sprintf("/api/comments/%d", c)}, {"PUT", fmt.Sprintf("/api/comments/%d", c)}, {"GET", fmt.Sprintf("/api/comments/%d/replies", c)},
		{"GET", fmt.Sprintf("/api/comments/%d/thread", c)}, {"POST", fmt.Sprintf("/api/comments/%d/like", c)},
		{"GET", "/api/notifications"}, {"PUT", "/api/notifications/read"},
		{"GET", "/api/blocks"}, {"PUT", "/api/users/someone/block"}, {"DELETE", "/api/users/someone/block"},
	}
	// Follows and Home need the follows flag, off by default, and the community flag as well.
	follows := [][2]string{{"GET", "/api/home"}, {"GET", "/api/following"}, {"GET", "/api/followers"},
		{"PUT", "/api/users/someone/follow"}, {"DELETE", "/api/users/someone/follow"}}
	body := map[string]any{"body": "hello"}
	gated := func(routes [][2]string, off bool) {
		t.Helper()
		for _, r := range routes {
			code, out := e.do(una, r[0], r[1], body)
			raw, _ := out["_raw"].(string)
			if closed := code == 404 && strings.Contains(raw, "404 page not found"); closed != off {
				t.Errorf("%s %s with the flag %s: %d %.60q", r[0], r[1], map[bool]string{true: "off", false: "on"}[off], code, raw)
			}
		}
	}
	badge := map[string]any{"keys": []string{"sessions-1"}, "prop": "nunchaku"}
	gated(follows, true)
	e.setFlag("follows", true)
	gated(follows, false)
	e.setFlag("community", false)
	gated(follows, true)
	e.setFlag("follows", false)
	gated(community, true)
	if e.count(`SELECT count(*) FROM posts`) != 1 || e.count(`SELECT count(*) FROM comments`) != 1 {
		t.Fatal("nothing is written or deleted while community is off")
	}
	// Pictures stay, and so do badges, but a badge posts no milestone to a timeline that is off.
	r := httptest.NewRequest("GET", fmt.Sprintf("/api/users/%d/avatar", uid), nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: una})
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("a picture while community is off: %d", w.Code)
	}
	if got := e.must(200, una, "POST", "/api/achievements", badge); num(got["postId"]) != 0 || e.count(`SELECT count(*) FROM posts`) != 1 {
		t.Fatalf("a badge while community is off made a post: %v", got["_raw"])
	}
	e.setFlag("community", true)
	e.setFlag("game", false)
	gated([][2]string{{"POST", "/api/achievements"}}, true)
	gated(community[:3], false)
	gated(follows, true)
	e.setFlag("game", true)
	badge["keys"] = []string{"sessions-10"}
	if got := e.must(200, una, "POST", "/api/achievements", badge); num(got["postId"]) == 0 {
		t.Fatalf("with both flags on a badge posts its milestone: %v", got["_raw"])
	}
}

// postIDs lists the ids of the posts an answer of the posts endpoints holds, in order.
func postIDs(out map[string]any) []int64 {
	ids := []int64{}
	list, _ := out["posts"].([]any)
	for _, p := range list {
		ids = append(ids, num(p.(map[string]any)["id"]))
	}
	return ids
}

// commentIDs lists the ids of the comments and their replies in an answer, in order.
func commentIDs(list []any) []int64 {
	ids := []int64{}
	for _, x := range list {
		c := x.(map[string]any)
		ids = append(ids, num(c["id"]))
		if reps, ok := c["replies"].([]any); ok {
			ids = append(ids, commentIDs(reps)...)
		}
	}
	return ids
}

// Member blocks (migration 8): the endpoints, and what a block hides and refuses, in both directions,
// without standing between anyone and moderation.
func TestDBBlocks(t *testing.T) {
	e := newEnv(t)
	e.setFlag("moderation", true)
	anaID, ana := e.user("ana", "user") // the blocker
	boID, bo := e.user("bo", "user")    // the one ana blocks
	cyID, cy := e.user("cy", "user")    // someone else
	modID, mod := e.user("mo", "moderator")
	block := func(tok, name string, want int) {
		t.Helper()
		e.must(want, tok, "PUT", "/api/users/"+name+"/block", nil)
	}
	unblock := func(tok, name string, want int) {
		t.Helper()
		e.must(want, tok, "DELETE", "/api/users/"+name+"/block", nil)
	}
	feed := func(tok, path string) []int64 { t.Helper(); return postIDs(e.must(200, tok, "GET", path, nil)) }
	has := func(ids []int64, id int64) bool { return slices.Contains(ids, id) }
	rows := func(a, b int64) int {
		return e.count(`SELECT count(*) FROM blocks WHERE user_id = $1 AND target_id = $2`, a, b)
	}

	// What is there before the block: bo's post with a comment by cy, ana's post with bo's comment and
	// like, a thread bo began under cy's post with cy's reply, and the notifications all that made.
	boPost := e.post(bo, "bo's post")
	cyOnBo := num(e.comment(cy, boPost, 0, "cy on bo")["id"])
	anaPost := e.post(ana, "ana's post")
	boOnAna := num(e.comment(bo, anaPost, 0, "bo on ana")["id"])
	cyOnAna := num(e.comment(cy, anaPost, 0, "cy on ana")["id"])
	e.must(200, bo, "POST", fmt.Sprintf("/api/posts/%d/like", anaPost), nil)
	cyPost := e.post(cy, "cy's post")
	boRoot := num(e.comment(bo, cyPost, 0, "bo starts a thread")["id"])
	cyReply := num(e.comment(cy, cyPost, boRoot, "cy answers bo")["id"])
	cyRoot := num(e.comment(cy, cyPost, 0, "cy starts another")["id"])
	anaOnCy := num(e.comment(ana, cyPost, cyRoot, "ana answers cy")["id"])
	e.must(200, ana, "POST", "/api/bookmarks", map[string]any{"kind": "post", "ref": fmt.Sprint(boPost)})
	// Likes each way, on a post and on a comment, to be taken back across the block.
	e.must(200, bo, "POST", fmt.Sprintf("/api/comments/%d/like", anaOnCy), nil)
	e.must(200, ana, "POST", fmt.Sprintf("/api/posts/%d/like", boPost), nil)
	e.must(200, ana, "POST", fmt.Sprintf("/api/comments/%d/like", boOnAna), nil)
	if n := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND actor_id = $2`, anaID, boID); n != 3 {
		t.Fatalf("before the block ana has %d notifications from bo, want a comment, a like and a comment like", n)
	}

	// The endpoints, by username as the profile is: nobody blocks themself, a name that is no account is
	// a 404 both ways, twice is once.
	boBefore := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND actor_id <> $2`, boID, anaID)
	block(ana, "ana", 400)
	block(ana, "ANA", 400)
	unblock(ana, "ana", 400)
	block(ana, "nobody", 404)
	unblock(ana, "nobody", 404)
	block(ana, fmt.Sprint(boID), 404) // an id is not a name
	block(ana, "bo", 200)
	block(ana, "BO", 200)
	if rows(anaID, boID) != 1 {
		t.Fatalf("blocking twice made %d rows", rows(anaID, boID))
	}
	unblock(ana, "mo", 200) // no such block: nothing to do, and the same answer
	list := e.must(200, ana, "GET", "/api/blocks", nil)
	if bl := list["blocks"].([]any); len(bl) != 1 || bl[0].(map[string]any)["user"].(map[string]any)["username"] != "bo" || num(list["total"]) != 1 {
		t.Fatalf("ana's list of blocks: %v", list["_raw"])
	}
	if bl := e.must(200, bo, "GET", "/api/blocks", nil)["blocks"].([]any); len(bl) != 0 {
		t.Fatalf("bo is shown who blocked him: %v", bl)
	}

	// The block took the notifications between them with it, and the blocked member is not told: bo
	// has exactly the notifications he had before (all from cy), none from ana, and ana none from bo.
	if n := e.count(`SELECT count(*) FROM notifications WHERE (user_id = $1 AND actor_id = $2) OR (user_id = $2 AND actor_id = $1)`, anaID, boID); n != 0 {
		t.Fatalf("%d notifications between ana and bo are left after the block", n)
	}
	if n := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1`, boID); n != boBefore || boBefore == 0 {
		t.Fatalf("bo has %d notifications after the block, %d not from ana before: the block told him something", n, boBefore)
	}
	if n := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND actor_id = $2`, boID, anaID); n != 0 {
		t.Fatalf("bo has %d notifications from ana", n)
	}
	if n := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND actor_id = $2`, anaID, boID); n != 0 {
		t.Fatalf("ana has %d notifications from bo", n)
	}

	// The timeline, a profile's posts, one post, the people list and the bookmarks: bo is gone for
	// ana, and ana for bo. cy sees both.
	if ids := feed(ana, "/api/posts"); has(ids, boPost) || !has(ids, anaPost) || !has(ids, cyPost) {
		t.Fatalf("ana's timeline: %v (bo's post is %d)", ids, boPost)
	}
	if ids := feed(bo, "/api/posts"); has(ids, anaPost) || !has(ids, boPost) {
		t.Fatalf("bo's timeline still shows ana's post: %v", ids)
	}
	if ids := feed(cy, "/api/posts"); !has(ids, anaPost) || !has(ids, boPost) {
		t.Fatalf("cy's timeline lost a post: %v", ids)
	}
	if ids := feed(ana, "/api/posts?user=bo"); len(ids) != 0 {
		t.Fatalf("bo's profile shows ana his posts: %v", ids)
	}
	e.must(404, ana, "GET", fmt.Sprintf("/api/posts/%d", boPost), nil)
	e.must(404, bo, "GET", fmt.Sprintf("/api/posts/%d", anaPost), nil)
	prof := e.must(200, ana, "GET", "/api/users/bo", nil)
	if prof["blocked"] != true || num(prof["posts"]) != 0 || num(prof["likes"]) != 0 {
		t.Fatalf("bo's profile as ana sees it: blocked %v, %v posts, %v likes", prof["blocked"], prof["posts"], prof["likes"])
	}
	prof = e.must(200, bo, "GET", "/api/users/ana", nil)
	if prof["blocked"] != false || num(prof["posts"]) != 0 {
		t.Fatalf("ana's profile as bo sees it: blocked %v, %v posts", prof["blocked"], prof["posts"])
	}
	if got := num(e.must(200, cy, "GET", "/api/users/ana", nil)["posts"]); got != 1 {
		t.Fatalf("ana's profile as cy sees it: %d posts", got)
	}
	people := func(tok string) []string {
		out := []string{}
		for _, u := range e.must(200, tok, "GET", "/api/users", nil)["users"].([]any) {
			out = append(out, u.(map[string]any)["username"].(string))
		}
		sort.Strings(out)
		return out
	}
	if got := people(ana); slices.Contains(got, "bo") || !slices.Contains(got, "cy") {
		t.Fatalf("people as ana sees them: %v", got)
	}
	if got := people(bo); slices.Contains(got, "ana") {
		t.Fatalf("people as bo sees them: %v", got)
	}
	if got := people(cy); !slices.Contains(got, "ana") || !slices.Contains(got, "bo") {
		t.Fatalf("people as cy sees them: %v", got)
	}
	if bm := e.must(200, ana, "GET", "/api/bookmarks", nil); len(bm["postIds"].([]any)) != 0 {
		t.Fatalf("ana's bookmark of bo's post is still listed: %v", bm["postIds"])
	}

	// Comments: bo's comment under ana's post stays, for others; ana is not shown it, nor counted it.
	// The thread bo began under cy's post goes for ana whole, cy's reply with it, and opening it is a 404.
	onPost := func(tok string, id int64) (map[string]any, []int64) {
		t.Helper()
		p := e.must(200, tok, "GET", fmt.Sprintf("/api/posts/%d", id), nil)["posts"].([]any)[0].(map[string]any)
		all := e.must(200, tok, "GET", fmt.Sprintf("/api/posts/%d/comments", id), nil)["comments"].([]any)
		return p, commentIDs(all)
	}
	p, ids := onPost(ana, anaPost)
	if has(ids, boOnAna) || !has(ids, cyOnAna) || num(p["commentCount"]) != 1 || has(commentIDs(p["comments"].([]any)), boOnAna) {
		t.Fatalf("ana's own post, as ana sees it: comments %v, count %v", ids, p["commentCount"])
	}
	if _, ids = onPost(cy, boPost); !has(ids, cyOnBo) {
		t.Fatalf("cy's comment under bo's post, as cy sees it: %v", ids)
	}
	p, ids = onPost(cy, anaPost)
	if !has(ids, boOnAna) || num(p["commentCount"]) != 2 {
		t.Fatalf("ana's post, as cy sees it: comments %v, count %v", ids, p["commentCount"])
	}
	p, ids = onPost(ana, cyPost)
	if has(ids, boRoot) || has(ids, cyReply) || !has(ids, cyRoot) || !has(ids, anaOnCy) || num(p["commentCount"]) != 2 {
		t.Fatalf("cy's post, as ana sees it: comments %v, count %v", ids, p["commentCount"])
	}
	p, ids = onPost(bo, cyPost)
	if has(ids, anaOnCy) || !has(ids, cyRoot) || num(p["commentCount"]) != 3 {
		t.Fatalf("cy's post, as bo sees it: comments %v (ana's reply is %d), count %v", ids, anaOnCy, p["commentCount"])
	}
	if c := e.must(200, bo, "GET", fmt.Sprintf("/api/comments/%d/replies", cyRoot), nil)["comments"].([]any); has(commentIDs(c), anaOnCy) {
		t.Fatal("bo is shown ana's reply in cy's thread")
	}
	if _, ids = onPost(cy, cyPost); !has(ids, boRoot) || !has(ids, cyReply) || !has(ids, anaOnCy) {
		t.Fatalf("cy's post, as cy sees it: %v", ids)
	}
	for _, path := range []string{"/api/comments/%d/replies", "/api/comments/%d/thread"} {
		e.must(404, ana, "GET", fmt.Sprintf(path, boRoot), nil)
		e.must(404, ana, "GET", fmt.Sprintf(path, cyReply), nil)
		e.must(200, cy, "GET", fmt.Sprintf(path, boRoot), nil)
	}

	// What bo cannot do to ana: comment on her post, answer her comment anywhere, join her thread,
	// like her post or comment. Each is answered as if the thing were not there. Mentioning her is
	// stored, and she is not told.
	refused := func(tok, method, path string, body any, msg string) {
		t.Helper()
		code, out := e.do(tok, method, path, body)
		if code != 404 || out["error"] != msg {
			t.Fatalf("%s %s: %d %v, want 404 %q", method, path, code, out["_raw"], msg)
		}
	}
	// Either side takes back a like given before the block, on a post or a comment: it is answered
	// liked false, with no count of a thing they are not shown, so it tells nothing new. Liking it
	// again is refused like any other like across the block.
	likes := func() int {
		return e.count(`SELECT count(*) FROM likes WHERE (user_id = $1 AND post_id = $2) OR (user_id = $3 AND post_id = $4)`, boID, anaPost, anaID, boPost) +
			e.count(`SELECT count(*) FROM comment_likes WHERE (user_id = $1 AND comment_id = $2) OR (user_id = $3 AND comment_id = $4)`, boID, anaOnCy, anaID, boOnAna)
	}
	if likes() != 4 {
		t.Fatalf("before the unlikes there are %d of the 4 likes", likes())
	}
	for _, x := range []struct {
		tok, path, gone string
	}{
		{bo, fmt.Sprintf("/api/posts/%d/like", anaPost), "No such post."},
		{bo, fmt.Sprintf("/api/comments/%d/like", anaOnCy), "That comment is gone."},
		{ana, fmt.Sprintf("/api/posts/%d/like", boPost), "No such post."},
		{ana, fmt.Sprintf("/api/comments/%d/like", boOnAna), "That comment is gone."},
	} {
		if out := e.must(200, x.tok, "POST", x.path, nil); out["liked"] != false || out["likes"] != nil {
			t.Fatalf("taking back a like across a block, %s: %v", x.path, out["_raw"])
		}
		refused(x.tok, "POST", x.path, nil, x.gone)
	}
	if likes() != 0 {
		t.Fatalf("%d likes across the block are left after taking them back", likes())
	}
	if out := e.must(200, cy, "GET", fmt.Sprintf("/api/posts/%d", anaPost), nil); num(out["posts"].([]any)[0].(map[string]any)["likes"]) != 0 {
		t.Fatalf("bo's like still counts for cy: %v", out["_raw"])
	}
	// A like taken back where the post is shown answers with the count, as before.
	e.must(200, cy, "POST", fmt.Sprintf("/api/posts/%d/like", anaPost), nil)
	if out := e.must(200, cy, "POST", fmt.Sprintf("/api/posts/%d/like", anaPost), nil); out["liked"] != false || out["likes"] != float64(0) {
		t.Fatalf("cy takes back a like: %v", out["_raw"])
	}

	before := e.count(`SELECT count(*) FROM comments`)
	refused(bo, "POST", fmt.Sprintf("/api/posts/%d/comments", anaPost), map[string]any{"body": "hi"}, "No such post.")
	refused(bo, "POST", fmt.Sprintf("/api/posts/%d/comments", cyPost), map[string]any{"body": "hi", "parentId": anaOnCy}, "That comment is gone.")
	anaRoot := num(e.comment(ana, cyPost, 0, "ana's own thread")["id"])
	cyInAna := num(e.comment(cy, cyPost, anaRoot, "cy answers ana")["id"])
	refused(bo, "POST", fmt.Sprintf("/api/posts/%d/comments", cyPost), map[string]any{"body": "me too", "parentId": cyInAna}, "That comment is gone.")
	if n := e.count(`SELECT count(*) FROM comments`); n != before+2 {
		t.Fatalf("bo's refused comments were written: %d, want %d", n, before+2)
	}
	// And the other way round: ana cannot answer or like what she blocked either.
	if likes() != 0 {
		t.Fatal("a refused like was written")
	}
	// Replies from both sides at the same moment, on a pool of one connection: each is refused, neither
	// waits for ever (a check run beside the open transaction on a second connection would), nothing is
	// written and nobody is told.
	nBefore := e.count(`SELECT count(*) FROM notifications`)
	e.a.db.SetMaxOpenConns(1)
	type answer struct {
		code int
		raw  any
	}
	at := make(chan answer, 2)
	for _, x := range []struct {
		tok    string
		parent int64
	}{{bo, anaOnCy}, {ana, boRoot}} {
		go func() {
			code, out := e.do(x.tok, "POST", fmt.Sprintf("/api/posts/%d/comments", cyPost), map[string]any{"body": "at the same moment", "parentId": x.parent})
			at <- answer{code, out["_raw"]}
		}()
	}
	for range 2 {
		select {
		case got := <-at:
			if got.code != 404 {
				t.Fatalf("a reply across a block at the same moment: %d %v", got.code, got.raw)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a reply across a block did not answer in 10 s on one connection: something asks a.db while the reply's transaction is open")
		}
	}
	e.a.db.SetMaxOpenConns(8)
	if n := e.count(`SELECT count(*) FROM comments`); n != before+2 {
		t.Fatalf("a reply at the same moment slipped through: %d comments, want %d", n, before+2)
	}
	if n := e.count(`SELECT count(*) FROM notifications`); n != nBefore {
		t.Fatalf("replies refused at the same moment made %d notifications", n-nBefore)
	}
	mention := e.post(bo, "a nod to @ana")
	e.comment(bo, cyPost, cyRoot, "and @ana again")
	if n := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND actor_id = $2`, anaID, boID); n != 0 {
		t.Fatalf("a mention across a block made %d notifications", n)
	}
	if e.count(`SELECT count(*) FROM posts WHERE id = $1 AND body LIKE '%@ana%'`, mention) != 1 {
		t.Fatal("the text of a post that mentions ana is kept")
	}
	e.comment(cy, cyPost, cyRoot, "cy mentions @ana")
	if n := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'mention'`, anaID); n != 1 {
		t.Fatalf("cy's mention of ana: %d notifications, want 1", n)
	}
	// A notification can still slip in beside a block (its delete and notifyAt's insert can cross), so the
	// bell asks about blocks too: one written straight after the block is neither listed nor counted, on
	// either side, while a moderator's notice from the same account still is.
	bell := func(tok string) (ids []int64, unread int64) {
		t.Helper()
		out := e.must(200, tok, "GET", "/api/notifications", nil)
		for _, it := range out["items"].([]any) {
			ids = append(ids, num(it.(map[string]any)["id"]))
		}
		return ids, num(out["unread"])
	}
	note := func(to, from int64, kind string) int64 {
		t.Helper()
		var id int64
		if err := e.a.db.QueryRow(`INSERT INTO notifications (user_id, actor_id, kind, post_id, text) VALUES ($1, $2, $3, $4, 'x') RETURNING id`, to, from, kind, anaPost).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	anaIDs, anaUnread := bell(ana)
	boIDs, boUnread := bell(bo)
	slipped, toBo, notice := note(anaID, boID, "like"), note(boID, anaID, "reply"), note(anaID, boID, "moderation")
	if ids, unread := bell(ana); has(ids, slipped) || !has(ids, notice) || len(ids) != len(anaIDs)+1 || unread != anaUnread+1 {
		t.Fatalf("ana's bell after a like slipped past the block: %v unread %d (before %v unread %d; slipped %d, notice %d)", ids, unread, anaIDs, anaUnread, slipped, notice)
	}
	if ids, unread := bell(bo); has(ids, toBo) || len(ids) != len(boIDs) || unread != boUnread {
		t.Fatalf("bo's bell after a reply slipped past the block: %v unread %d (before %v unread %d)", ids, unread, boIDs, boUnread)
	}
	if _, err := e.a.db.Exec(`DELETE FROM notifications WHERE id = ANY($1)`, pq.Array([]int64{slipped, toBo, notice})); err != nil {
		t.Fatal(err)
	}
	// Reports still work across a block, both ways.
	e.must(200, ana, "POST", "/api/reports", map[string]any{"kind": "post", "id": boPost, "reason": "spam"})
	e.must(200, bo, "POST", "/api/reports", map[string]any{"kind": "user", "id": anaID, "reason": "spam"})

	// A page of the timeline is full however many of the newest posts are by bo, and the next page
	// goes on from where it stopped.
	if _, err := e.a.db.Exec(`INSERT INTO posts (user_id, body) SELECT $1, 'filler ' || g FROM generate_series(1, 30) g`, cyID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.db.Exec(`INSERT INTO posts (user_id, body) SELECT $1, 'bo again ' || g FROM generate_series(1, 50) g`, boID); err != nil {
		t.Fatal(err)
	}
	page := e.must(200, ana, "GET", "/api/posts", nil)
	first := postIDs(page)
	if len(first) != pageSize || page["more"] != true {
		t.Fatalf("ana's first page behind 50 posts by bo: %d posts, more %v", len(first), page["more"])
	}
	next := feed(ana, fmt.Sprintf("/api/posts?before=%d", first[len(first)-1]))
	for _, id := range append(first, next...) {
		if e.count(`SELECT count(*) FROM posts WHERE id = $1 AND user_id = $2`, id, boID) != 0 {
			t.Fatalf("a post by bo on ana's pages: %d", id)
		}
	}
	if len(next) < 10 || slices.Contains(next, first[0]) {
		t.Fatalf("ana's second page: %v", next)
	}

	// Blocking someone who blocks you: two rows, and taking one back leaves the other in force.
	block(bo, "ana", 200)
	if rows(anaID, boID) != 1 || rows(boID, anaID) != 1 {
		t.Fatal("both blocks are kept")
	}
	unblock(ana, "bo", 200)
	unblock(ana, "bo", 200)
	if ids := feed(ana, "/api/posts?user=bo"); len(ids) != 0 {
		t.Fatalf("bo's block of ana still hides his posts from her: %v", ids)
	}
	refused(ana, "POST", fmt.Sprintf("/api/posts/%d/like", boPost), nil, "No such post.")
	unblock(bo, "ana", 200)
	if ids := feed(ana, "/api/posts?user=bo"); len(ids) == 0 {
		t.Fatalf("with no block left ana sees bo's posts again: %v", ids)
	}
	if _, ids = onPost(ana, anaPost); !has(ids, boOnAna) {
		t.Fatalf("bo's comment comes back after the unblock: %v", ids)
	}
	e.must(200, bo, "POST", fmt.Sprintf("/api/posts/%d/comments", anaPost), map[string]any{"body": "talking again"})
	if n := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND actor_id = $2 AND kind = 'comment'`, anaID, boID); n != 1 {
		t.Fatalf("after the unblock ana is told of bo's comment: %d", n)
	}

	// A muted member blocks and unblocks; a suspended member cannot sign in at all, and the blocks
	// they made hold while they are away.
	if _, err := e.a.db.Exec(`UPDATE users SET muted_until = now() + interval '1 day', mute_reason = 'test' WHERE id = $1`, anaID); err != nil {
		t.Fatal(err)
	}
	block(ana, "bo", 200)
	unblock(ana, "bo", 200)
	block(ana, "bo", 200)
	if _, err := e.a.db.Exec(`UPDATE users SET muted_until = NULL, suspended_until = now() + interval '1 day' WHERE id = $1`, anaID); err != nil {
		t.Fatal(err)
	}
	block(ana, "mo", 401)
	if ids := feed(bo, "/api/posts?user=ana"); len(ids) != 0 {
		t.Fatalf("a suspended member's block no longer holds: %v", ids)
	}
	if _, err := e.a.db.Exec(`UPDATE users SET suspended_until = NULL WHERE id = $1`, anaID); err != nil {
		t.Fatal(err)
	}
	ana = e.session(anaID)

	// A member who blocks a moderator: the moderator still sees the member's posts and moderates
	// them, and the member is told of it; the moderator cannot comment on or like them.
	block(ana, "mo", 200)
	if ids := feed(mod, "/api/posts?user=ana"); !has(ids, anaPost) {
		t.Fatalf("a moderator ana blocked no longer sees her posts: %v", ids)
	}
	e.must(200, mod, "GET", fmt.Sprintf("/api/posts/%d/comments", anaPost), nil)
	refused(mod, "POST", fmt.Sprintf("/api/posts/%d/like", anaPost), nil, "No such post.")
	refused(mod, "POST", fmt.Sprintf("/api/posts/%d/comments", anaPost), map[string]any{"body": "hello"}, "No such post.")
	e.must(200, mod, "POST", "/api/mod/actions", map[string]any{"action": "hide", "kind": "post", "id": anaPost, "reason": "test"})
	e.must(200, mod, "POST", "/api/mod/actions", map[string]any{"action": "restore", "kind": "post", "id": anaPost})
	if got := e.kinds(anaID); slices.Index(got, "moderation") < 0 || e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'moderation'`, anaID) != 2 {
		t.Fatalf("ana is not told of the moderator's actions: %v", got)
	}

	// A moderator who blocks a member: the member's posts leave the moderator's timeline, but a
	// report of them reaches the queue and the moderator acts on it.
	unblock(ana, "mo", 200)
	deeID, dee := e.user("dee", "user")
	deePost := e.post(dee, "dee's post")
	block(mod, "dee", 200)
	if ids := feed(mod, "/api/posts"); has(ids, deePost) {
		t.Fatal("a moderator's own block hides the member from their timeline")
	}
	e.must(200, cy, "POST", "/api/reports", map[string]any{"kind": "post", "id": deePost, "reason": "spam"})
	queued := false
	for _, it := range e.must(200, mod, "GET", "/api/mod/queue", nil)["items"].([]any) {
		queued = queued || num(it.(map[string]any)["id"]) == deePost
	}
	if !queued {
		t.Fatal("the report of a post by someone the moderator blocked is not in the queue")
	}
	e.must(200, mod, "POST", "/api/mod/actions", map[string]any{"action": "hide", "kind": "post", "id": deePost, "reason": "spam"})
	if e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'moderation'`, deeID) != 1 {
		t.Fatal("dee is not told of the moderator's action")
	}

	// A deleted account's blocks go with it, whichever side it was on.
	block(cy, "dee", 200)
	block(dee, "bo", 200)
	if _, err := e.a.db.Exec(`DELETE FROM users WHERE id = $1`, deeID); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM blocks WHERE user_id = $1 OR target_id = $1`, deeID); n != 0 {
		t.Fatalf("%d blocks of a deleted account are left", n)
	}
	block(cy, "dee", 404)
	if e.count(`SELECT count(*) FROM blocks WHERE user_id = $1`, modID) != 0 {
		t.Fatal("the moderator's block of dee went with dee")
	}
	// The database refuses a block of oneself whatever the handler does.
	if _, err := e.a.db.Exec(`INSERT INTO blocks (user_id, target_id) VALUES ($1, $1)`, boID); err == nil {
		t.Fatal("the database took a block of oneself")
	}

	// Blocks are by name, so they put a name to nobody: zed never posted and is not among the people
	// (a hundred members have more XP), and trying ids around his, as block and then the list of
	// blocks, finds nothing to block and lists nobody.
	if _, err := e.a.db.Exec(`INSERT INTO users (username, pw_hash, stats) SELECT 'xp' || g, 'x', jsonb_build_object('xp', 1000) FROM generate_series(1, 100) g`); err != nil {
		t.Fatal(err)
	}
	zedID, _ := e.user("zed", "user")
	if got := people(cy); slices.Contains(got, "zed") || len(got) != 100 {
		t.Fatalf("zed is in the people list, or it is not full: %d names", len(got))
	}
	for id := zedID - 3; id <= zedID+3; id++ {
		if code, out := e.do(cy, "PUT", fmt.Sprintf("/api/users/%d/block", id), nil); code != 404 {
			t.Fatalf("blocking by the id %d: %d %v", id, code, out["_raw"])
		}
	}
	if list := e.must(200, cy, "GET", "/api/blocks", nil); strings.Contains(list["_raw"].(string), "zed") || len(list["blocks"].([]any)) != 0 {
		t.Fatalf("cy's list of blocks after guessing ids: %v", list["_raw"])
	}
	if n := e.count(`SELECT count(*) FROM blocks WHERE target_id = $1`, zedID); n != 0 {
		t.Fatalf("guessing ids blocked zed %d times", n)
	}
}

// Follows and Home (migration 9): the follows flag is off on an instance that sets nothing, new or
// older; with it on, the endpoints, what Home shows, what a block, a deleted account, a mute and
// hidden stats do to follows, the follow notification, the counts for everyone and the lists for the
// member alone.
func TestDBFollows(t *testing.T) {
	e := newEnv(t)
	anaID, ana := e.user("ana", "user")
	boID, bo := e.user("bo", "user")
	cyID, cy := e.user("cy", "user")
	modID, mod := e.user("mo", "moderator")
	follow := func(tok, name string, want int) map[string]any {
		t.Helper()
		return e.must(want, tok, "PUT", "/api/users/"+name+"/follow", nil)
	}
	unfollow := func(tok, name string, want int) {
		t.Helper()
		e.must(want, tok, "DELETE", "/api/users/"+name+"/follow", nil)
	}
	rows := func(a, b int64) int {
		return e.count(`SELECT count(*) FROM follows WHERE follower_id = $1 AND followee_id = $2`, a, b)
	}
	home := func(tok, q string) map[string]any { t.Helper(); return e.must(200, tok, "GET", "/api/home"+q, nil) }
	has := func(ids []int64, id int64) bool { return slices.Contains(ids, id) }
	told := func(to, from int64) int {
		return e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND actor_id = $2 AND kind = 'follow'`, to, from)
	}

	// Off by default: on a new instance, and on one where an admin saved every other flag before
	// follows existed, the routes are not there and the profile carries nothing of follows.
	off := func(why string) {
		t.Helper()
		f, err := e.a.flags()
		if err != nil {
			t.Fatal(err)
		}
		if f["follows"] {
			t.Fatalf("%s: the follows flag is on", why)
		}
		for _, r := range [][2]string{{"GET", "/api/home"}, {"GET", "/api/following"}, {"GET", "/api/followers"}, {"PUT", "/api/users/bo/follow"}, {"DELETE", "/api/users/bo/follow"}} {
			if code, out := e.do(ana, r[0], r[1], nil); code != 404 || !strings.Contains(out["_raw"].(string), "404 page not found") {
				t.Fatalf("%s: %s %s answers %d %v", why, r[0], r[1], code, out["_raw"])
			}
		}
		prof := e.must(200, ana, "GET", "/api/users/bo", nil)
		for _, k := range []string{"followers", "following", "followed"} {
			if _, ok := prof[k]; ok {
				t.Fatalf("%s: the profile says %s: %v", why, k, prof["_raw"])
			}
		}
	}
	off("a new instance")
	saved := map[string]bool{}
	for k, v := range flagDefaults {
		if k != "follows" {
			saved[k] = v
		}
	}
	e.setFlag("community", true)
	if err := e.a.setFlags(saved); err != nil {
		t.Fatal(err)
	}
	off("an instance whose flags were all saved before follows existed")
	if e.count(`SELECT count(*) FROM follows`) != 0 {
		t.Fatal("a follow was written while the flag is off")
	}
	e.setFlag("follows", true)

	// The endpoints, by username as blocks are: nobody follows themself, a name that is no account is
	// a 404 both ways, an id is not a name, twice is once, and unfollowing what is not followed is fine.
	follow(ana, "ana", 400)
	follow(ana, "ANA", 400)
	unfollow(ana, "ana", 400)
	follow(ana, "nobody", 404)
	unfollow(ana, "nobody", 404)
	follow(ana, fmt.Sprint(boID), 404)
	if got := follow(ana, "bo", 200); got["following"] != true {
		t.Fatalf("follow answers %v", got["_raw"])
	}
	follow(ana, "BO", 200)
	if rows(anaID, boID) != 1 {
		t.Fatalf("following twice made %d rows", rows(anaID, boID))
	}
	unfollow(ana, "cy", 200)
	if _, err := e.a.db.Exec(`INSERT INTO follows (follower_id, followee_id) VALUES ($1, $1)`, boID); err == nil {
		t.Fatal("the database took a follow of oneself")
	}

	// The notification: told once, in the bell, with who; unfollowing and following again within a
	// day tells nothing new, a day later it does.
	if told(boID, anaID) != 1 {
		t.Fatalf("bo was told %d times of ana's follow, want once", told(boID, anaID))
	}
	n := e.must(200, bo, "GET", "/api/notifications", nil)
	if it := n["items"].([]any)[0].(map[string]any); it["kind"] != "follow" || num(it["actorId"]) != anaID || num(n["unread"]) != 1 {
		t.Fatalf("bo's bell: %v", n["_raw"])
	}
	unfollow(ana, "bo", 200)
	if rows(anaID, boID) != 0 {
		t.Fatal("unfollow left the row")
	}
	follow(ana, "bo", 200)
	if told(boID, anaID) != 1 {
		t.Fatalf("unfollow and follow again within a day told bo %d times", told(boID, anaID))
	}
	if _, err := e.a.db.Exec(`UPDATE notifications SET created_at = now() - interval '25 hours' WHERE kind = 'follow'`); err != nil {
		t.Fatal(err)
	}
	unfollow(ana, "bo", 200)
	follow(ana, "bo", 200)
	if told(boID, anaID) != 2 {
		t.Fatalf("a follow again a day later told bo %d times in all, want 2", told(boID, anaID))
	}

	// Home: the posts of whom ana follows and her own, newest first, in the timeline's shape; cy's is
	// not there, and Community still has all three.
	boPost := e.post(bo, "bo's post")
	cyPost := e.post(cy, "cy's post")
	anaPost := e.post(ana, "ana's post")
	h := home(ana, "")
	if ids := postIDs(h); !slices.Equal(ids, []int64{anaPost, boPost}) {
		t.Fatalf("ana's Home: %v, want her post %d and bo's %d", ids, anaPost, boPost)
	}
	if _, ok := h["users"].(map[string]any)[fmt.Sprint(boID)]; !ok || h["more"] != false {
		t.Fatalf("Home is not in the timeline's shape: %v", h["_raw"])
	}
	if ids := postIDs(e.must(200, ana, "GET", "/api/posts", nil)); !has(ids, cyPost) || !has(ids, boPost) || !has(ids, anaPost) {
		t.Fatalf("the Community timeline changed: %v", ids)
	}
	if ids := postIDs(home(cy, "")); !slices.Equal(ids, []int64{cyPost}) {
		t.Fatalf("cy follows nobody, so Home is cy's own post: %v", ids)
	}

	// A hidden post leaves Home as it leaves Community, for all but its author and staff.
	if _, err := e.a.db.Exec(`UPDATE posts SET hidden_at = now(), hidden_reason = 'test' WHERE id = $1`, boPost); err != nil {
		t.Fatal(err)
	}
	follow(mod, "bo", 200)
	if has(postIDs(home(ana, "")), boPost) || !has(postIDs(home(bo, "")), boPost) || !has(postIDs(home(mod, "")), boPost) {
		t.Fatal("a hidden post in Home: shown to ana, or not to bo or the moderator")
	}
	if _, err := e.a.db.Exec(`UPDATE posts SET hidden_at = NULL, hidden_reason = '' WHERE id = $1`, boPost); err != nil {
		t.Fatal(err)
	}

	// Paging: before= gives the next page, with nothing twice and nothing lost.
	var many []int64
	for i := 0; i < pageSize+5; i++ {
		many = append(many, e.post(bo, fmt.Sprintf("bo again %d", i)))
	}
	first := home(ana, "")
	page1 := postIDs(first)
	if len(page1) != pageSize || first["more"] != true || page1[0] != many[len(many)-1] {
		t.Fatalf("Home's first page: %d posts, more %v", len(page1), first["more"])
	}
	page2 := postIDs(home(ana, fmt.Sprintf("?before=%d", page1[len(page1)-1])))
	all := append(slices.Clone(page1), page2...)
	want := append(slices.Clone(many), boPost, anaPost)
	slices.Sort(want)
	slices.Reverse(want)
	if !slices.Equal(all, want) {
		t.Fatalf("Home's two pages: %v, want %v", all, want)
	}

	// Counts for everyone, exact; whether the viewer follows; hidden stats and a mute change nothing.
	follow(cy, "bo", 200)
	follow(bo, "ana", 200)
	if _, err := e.a.db.Exec(`UPDATE users SET stats_hidden = true WHERE id = $1`, boID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.db.Exec(`UPDATE users SET muted_until = now() + interval '1 day' WHERE id = $1`, cyID); err != nil {
		t.Fatal(err)
	}
	follow(cy, "ana", 200) // a muted member may follow
	for _, v := range []struct {
		tok, name                      string
		followers, following, followed int64
	}{{ana, "bo", 3, 1, 1}, {cy, "bo", 3, 1, 1}, {mod, "ana", 2, 1, 0}, {bo, "bo", 3, 1, 0}, {ana, "cy", 0, 2, 0}} {
		prof := e.must(200, v.tok, "GET", "/api/users/"+v.name, nil)
		followed := map[bool]int64{true: 1}[prof["followed"] == true]
		if num(prof["followers"]) != v.followers || num(prof["following"]) != v.following || followed != v.followed {
			t.Fatalf("%s's profile: %v followers, %v following, followed %v; want %d, %d, %d", v.name, prof["followers"], prof["following"], prof["followed"], v.followers, v.following, v.followed)
		}
	}

	// The lists, to the member alone: bo sees who follows him and whom he follows. Nobody sees another's,
	// and the profile names nobody.
	names := func(out map[string]any, key string) []string {
		got := []string{}
		for _, m := range out[key].([]any) {
			got = append(got, m.(map[string]any)["user"].(map[string]any)["username"].(string))
		}
		sort.Strings(got)
		return got
	}
	if got := e.must(200, bo, "GET", "/api/followers", nil); !slices.Equal(names(got, "followers"), []string{"ana", "cy", "mo"}) || num(got["total"]) != 3 {
		t.Fatalf("bo's followers: %v", got["_raw"])
	}
	if got := e.must(200, bo, "GET", "/api/following", nil); !slices.Equal(names(got, "following"), []string{"ana"}) || num(got["total"]) != 1 {
		t.Fatalf("whom bo follows: %v", got["_raw"])
	}
	for _, path := range []string{"/api/users/bo/followers", "/api/users/bo/following", "/api/followers?user=bo"} {
		code, out := e.do(cy, "GET", path, nil)
		if raw := out["_raw"].(string); code == 200 && (strings.Contains(raw, `"mo"`) || strings.Contains(raw, `"username":"ana"`)) {
			t.Fatalf("%s shows cy whom bo's lists hold: %s", path, raw)
		}
	}
	if raw := e.must(200, cy, "GET", "/api/users/bo", nil)["_raw"].(string); strings.Contains(raw, `"ana"`) || strings.Contains(raw, `"mo"`) {
		t.Fatalf("bo's profile names his followers to cy: %s", raw)
	}

	// A block removes the follows between the two, both ways, and their follow notifications; neither
	// can follow the other while it holds, refused with one sentence that does not say who blocked whom
	// (acrossBlock); others keep theirs.
	if rows(anaID, boID) != 1 || rows(boID, anaID) != 1 {
		t.Fatal("ana and bo do not follow each other before the block")
	}
	e.must(200, ana, "PUT", "/api/users/bo/block", nil)
	if rows(anaID, boID) != 0 || rows(boID, anaID) != 0 {
		t.Fatalf("after the block: %d follows from ana to bo, %d back", rows(anaID, boID), rows(boID, anaID))
	}
	if told(boID, anaID)+told(anaID, boID) != 0 {
		t.Fatal("follow notifications between ana and bo outlast the block")
	}
	if rows(cyID, boID) != 1 || rows(modID, boID) != 1 {
		t.Fatal("the block took someone else's follow")
	}
	acrossBlock := func() {
		t.Helper()
		for _, v := range [][2]string{{ana, "bo"}, {bo, "ana"}} {
			if out := follow(v[0], v[1], 403); out["error"] != "You cannot follow this account." {
				t.Fatalf("a follow across a block is refused in other words: %v", out["_raw"])
			}
		}
	}
	acrossBlock()
	unfollow(bo, "ana", 200)
	if rows(anaID, boID)+rows(boID, anaID) != 0 || told(boID, anaID)+told(anaID, boID) != 0 {
		t.Fatal("a refused follow wrote a row or told someone")
	}
	// A follow that slipped past the block (each statement under READ COMMITTED) still shows nothing in
	// Home; a moderator whom bo blocked still sees bo's posts there, as in Community.
	if _, err := e.a.db.Exec(`INSERT INTO follows (follower_id, followee_id) VALUES ($1, $2)`, anaID, boID); err != nil {
		t.Fatal(err)
	}
	if has(postIDs(home(ana, "")), many[0]) || len(postIDs(home(ana, ""))) != 1 {
		t.Fatalf("Home shows ana bo's posts across her block: %v", postIDs(home(ana, "")))
	}
	e.must(200, bo, "PUT", "/api/users/mo/block", nil)
	if rows(modID, boID) != 0 {
		t.Fatal("bo's block of the moderator left the moderator's follow")
	}
	if _, err := e.a.db.Exec(`INSERT INTO follows (follower_id, followee_id) VALUES ($1, $2)`, modID, boID); err != nil {
		t.Fatal(err)
	}
	if !has(postIDs(home(mod, "")), many[len(many)-1]) {
		t.Fatal("the moderator bo blocked does not see bo's posts in Home")
	}
	// The other way round, for a member who is not staff: bo blocks ana while ana's follow of bo is in
	// the table, as a follow written at the same moment as a block can be. Ana's Home shows none of bo's
	// posts, the refusal is the same sentence, and neither the counts nor the lists show the pair.
	e.must(200, ana, "DELETE", "/api/users/bo/block", nil)
	e.must(200, bo, "PUT", "/api/users/ana/block", nil)
	if rows(anaID, boID) != 0 {
		t.Fatal("bo's block left ana's follow of him")
	}
	if _, err := e.a.db.Exec(`INSERT INTO follows (follower_id, followee_id) VALUES ($1, $2)`, anaID, boID); err != nil {
		t.Fatal(err)
	}
	if ids := postIDs(home(ana, "")); has(ids, boPost) || has(ids, many[0]) || has(ids, many[len(many)-1]) || len(ids) != 1 {
		t.Fatalf("Home shows ana bo's posts though bo blocked her: %v", ids)
	}
	acrossBlock()
	for _, v := range []struct {
		name                 string
		followers, following int64
	}{{"bo", 1, 0}, {"ana", 1, 0}} {
		prof := e.must(200, cy, "GET", "/api/users/"+v.name, nil)
		if num(prof["followers"]) != v.followers || num(prof["following"]) != v.following {
			t.Fatalf("across bo's block, %s's counts as cy sees them: %v followers, %v following; want %d, %d", v.name, prof["followers"], prof["following"], v.followers, v.following)
		}
	}
	if got := e.must(200, ana, "GET", "/api/users/bo", nil); got["followed"] != false {
		t.Fatalf("bo's profile tells ana she follows him across his block: %v", got["_raw"])
	}
	if got := e.must(200, bo, "GET", "/api/followers", nil); slices.Contains(names(got, "followers"), "ana") || num(got["total"]) != 1 {
		t.Fatalf("bo's followers across his block of ana: %v", got["_raw"])
	}
	if got := e.must(200, ana, "GET", "/api/following", nil); len(names(got, "following")) != 0 || num(got["total"]) != 0 {
		t.Fatalf("whom ana follows across bo's block: %v", got["_raw"])
	}
	e.must(200, bo, "DELETE", "/api/users/ana/block", nil)
	follow(bo, "ana", 200)
	if told(anaID, boID) != 1 {
		t.Fatal("after the unblock a follow tells again")
	}

	// A deleted account's follows go with it, both ways.
	follow(ana, "cy", 200)
	if _, err := e.a.db.Exec(`DELETE FROM users WHERE id = $1`, cyID); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM follows WHERE follower_id = $1 OR followee_id = $1`, cyID); n != 0 {
		t.Fatalf("%d follows of a deleted account are left", n)
	}
	follow(ana, "cy", 404)

	// At most maxFollows: one more is refused with the reason, and following one already followed is not.
	if _, err := e.a.db.Exec(`INSERT INTO users (username, pw_hash) SELECT 'f' || g, 'x' FROM generate_series(1, $1::int) g`, maxFollows); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.db.Exec(`INSERT INTO follows (follower_id, followee_id) SELECT $1, id FROM users WHERE username LIKE 'f%' AND id <> $1 ON CONFLICT DO NOTHING`, modID); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM follows WHERE follower_id = $1`, modID) < maxFollows {
		t.Fatal("the moderator does not follow the most there can be")
	}
	if out := follow(mod, "ana", 400); !strings.Contains(out["error"].(string), "Unfollow someone first") {
		t.Fatalf("one follow too many: %v", out["_raw"])
	}
	follow(mod, "f1", 200)
}

// Follows sent at the same moment: two identical follows are one row, one notification and two
// plain answers, never the limit's refusal; and the limit holds when many follows race for its
// last place, so a member never follows more than maxFollows accounts.
func TestDBFollowsAtOnce(t *testing.T) {
	e := newEnv(t)
	e.setFlag("follows", true)
	anaID, ana := e.user("ana", "user")
	if _, err := e.a.db.Exec(`INSERT INTO users (username, pw_hash) SELECT 't' || lpad(g::text, 2, '0'), 'x' FROM generate_series(1, 20) g`); err != nil {
		t.Fatal(err)
	}
	type answer struct {
		code int
		raw  string
	}
	at := func(paths []string) []answer {
		t.Helper()
		out := make([]answer, len(paths))
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i, p := range paths {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				code, got := e.do(ana, "PUT", p, nil)
				out[i] = answer{code, got["_raw"].(string)}
			}()
		}
		close(start)
		wg.Wait()
		return out
	}
	var twice []string
	for g := 1; g <= 20; g++ {
		p := fmt.Sprintf("/api/users/t%02d/follow", g)
		twice = append(twice, p, p)
	}
	for _, a := range at(twice) {
		if a.code != 200 {
			t.Errorf("a follow sent twice at once: %d %s", a.code, a.raw)
		}
	}
	if n := e.count(`SELECT count(*) FROM follows WHERE follower_id = $1`, anaID); n != 20 {
		t.Fatalf("20 follows sent twice each made %d rows", n)
	}
	if n := e.count(`SELECT count(*) FROM notifications WHERE actor_id = $1 AND kind = 'follow'`, anaID); n != 20 {
		t.Fatalf("20 follows sent twice each told %d times in all, want 20", n)
	}

	// The last place: ana follows maxFollows - 1 accounts, and 20 follows of others arrive together.
	if _, err := e.a.db.Exec(`INSERT INTO users (username, pw_hash) SELECT 'f' || g, 'x' FROM generate_series(1, $1::int) g`, maxFollows); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.db.Exec(`INSERT INTO follows (follower_id, followee_id) SELECT $1, id FROM users WHERE username LIKE 'f%' ORDER BY id LIMIT $2`, anaID, maxFollows-1-20); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM follows WHERE follower_id = $1`, anaID); n != maxFollows-1 {
		t.Fatalf("ana follows %d before the race, want %d", n, maxFollows-1)
	}
	var last []string
	for _, name := range []string{"f990", "f991", "f992", "f993", "f994", "f995", "f996", "f997", "f998", "f999", "f1000",
		"f989", "f988", "f987", "f986", "f985", "f984", "f983", "f982", "f981"} {
		last = append(last, "/api/users/"+name+"/follow")
	}
	ok := 0
	for _, a := range at(last) {
		switch {
		case a.code == 200:
			ok++
		case a.code == 400 && strings.Contains(a.raw, "Unfollow someone first"):
		default:
			t.Errorf("a follow racing for the last place: %d %s", a.code, a.raw)
		}
	}
	if n := e.count(`SELECT count(*) FROM follows WHERE follower_id = $1`, anaID); n != maxFollows || ok != 1 {
		t.Fatalf("after the race ana follows %d accounts with %d follows taken, want %d and 1", n, ok, maxFollows)
	}
}

// Every write that opens a transaction, and the social writes beside them, answer on a pool of one
// connection: a handler that asks a.db while it holds a transaction would wait for a second connection
// for ever, and with the pool's 8 held that way every request that needs the database would hang.
// Each request has 10 s; one that does not answer fails the test instead of hanging it.
func TestDBOneConnection(t *testing.T) {
	e := newEnv(t)
	if err := e.a.seedCatalogs(); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"moderation", "catalogEditor", "suggest", "community", "game", "follows"} {
		e.setFlag(f, true)
	}
	adminID, admin := e.user("boss", "admin")
	admin2ID, _ := e.user("deputy", "admin")
	modID, mod := e.user("mo", "moderator")
	anaID, ana := e.user("ana", "user")
	_, bo := e.user("bo", "user")
	goneID, _ := e.user("gone", "user")
	if _, err := e.a.db.Exec(`UPDATE users SET owner = true WHERE id = $1`, adminID); err != nil {
		t.Fatal(err)
	}
	e.a.db.SetMaxOpenConns(1)
	within := func(want int, tok, method, path string, body any) map[string]any {
		t.Helper()
		type answer struct {
			code int
			out  map[string]any
		}
		at := make(chan answer, 1)
		go func() { code, out := e.do(tok, method, path, body); at <- answer{code, out} }()
		select {
		case got := <-at:
			if got.code != want {
				t.Fatalf("%s %s on one connection: got %d, want %d: %v", method, path, got.code, want, got.out["_raw"])
			}
			return got.out
		case <-time.After(10 * time.Second):
			t.Fatalf("%s %s did not answer in 10 s on one connection: it asks a.db while it holds a transaction", method, path)
			return nil
		}
	}
	id := func(out map[string]any, key string) int64 { return num(out[key].(map[string]any)["id"]) }

	within(200, ana, "PUT", "/api/me", map[string]any{"bio": "one connection"})
	p := num(within(200, ana, "POST", "/api/posts", map[string]any{"body": "a post"})["posts"].([]any)[0].(map[string]any)["id"])
	pm := num(within(200, ana, "POST", "/api/posts", map[string]any{"body": "a post that tells @boss"})["posts"].([]any)[0].(map[string]any)["id"]) // told after the lookup's rows close
	within(200, bo, "POST", fmt.Sprintf("/api/posts/%d/like", p), nil)
	within(200, bo, "POST", fmt.Sprintf("/api/posts/%d/like", p), nil)
	c := id(within(200, bo, "POST", fmt.Sprintf("/api/posts/%d/comments", p), map[string]any{"body": "a comment"}), "comment")
	r := id(within(200, ana, "POST", fmt.Sprintf("/api/posts/%d/comments", p), map[string]any{"body": "a reply", "parentId": c}), "comment")
	within(200, ana, "POST", fmt.Sprintf("/api/comments/%d/like", c), nil)
	within(200, ana, "POST", fmt.Sprintf("/api/comments/%d/like", c), nil)
	within(200, ana, "PUT", fmt.Sprintf("/api/comments/%d", r), map[string]any{"body": "a reply, edited"})
	within(200, ana, "PUT", "/api/notifications/read", map[string]any{})
	within(200, ana, "PUT", "/api/users/bo/follow", nil)
	within(200, bo, "PUT", "/api/users/ana/follow", nil)
	within(200, ana, "DELETE", "/api/users/bo/follow", nil)
	within(200, ana, "PUT", "/api/users/bo/follow", nil)
	within(200, ana, "GET", "/api/home", nil)
	within(200, ana, "GET", "/api/followers", nil)
	within(200, ana, "PUT", "/api/users/bo/block", nil)
	// The block took the follows both ways in its own statement; following across it is refused.
	within(403, bo, "PUT", "/api/users/ana/follow", nil)
	// Across the block: the reply's check runs inside its transaction, and is refused there.
	within(404, bo, "POST", fmt.Sprintf("/api/posts/%d/comments", p), map[string]any{"body": "again", "parentId": r})
	within(200, ana, "GET", "/api/blocks", nil)
	within(200, ana, "DELETE", "/api/users/bo/block", nil)
	within(200, bo, "POST", "/api/reports", map[string]any{"kind": "post", "id": p, "reason": "spam"})
	within(200, mod, "POST", "/api/mod/actions", map[string]any{"action": "hide", "kind": "post", "id": p, "reason": "test"})
	within(200, mod, "POST", "/api/mod/actions", map[string]any{"action": "restore", "kind": "post", "id": p})
	within(200, mod, "POST", "/api/mod/resolve", map[string]any{"kind": "post", "id": p, "outcome": "dismissed"})
	within(200, admin, "PUT", fmt.Sprintf("/api/admin/users/%d/role", modID), map[string]any{"role": "user"})
	within(200, admin, "PUT", fmt.Sprintf("/api/admin/users/%d/owner", admin2ID), nil)
	inst := within(200, admin, "PUT", "/api/admin/instance", map[string]any{"description": "One connection.", "contact": "", "rules": []map[string]string{{"text": "Be kind", "hint": ""}}, "updatedAt": ""})
	within(409, admin, "PUT", "/api/admin/instance", map[string]any{"description": "Stale.", "contact": "", "rules": []any{}, "updatedAt": ""})
	within(200, admin, "PUT", "/api/admin/instance", map[string]any{"description": "One connection, again.", "contact": "", "rules": []any{}, "updatedAt": inst["updatedAt"]})
	within(200, admin, "GET", "/api/admin/instance", nil)
	within(200, ana, "GET", "/api/instance", nil)
	within(200, "", "GET", "/api/about", nil)
	// Open sign-up: the insert runs in a transaction that holds the name's lock (nameLockSQL).
	within(200, "", "POST", "/api/signup", map[string]any{"username": "opens", "password": "a fine long password"})
	// Sign-up by approval: the setting, a request (its count and insert run in one transaction), the
	// list, an approval and a decline.
	cur := within(200, admin, "GET", "/api/admin/instance", nil)
	within(200, admin, "PUT", "/api/admin/instance", map[string]any{"description": cur["description"], "contact": "", "rules": []any{}, "registration": "approval", "updatedAt": cur["updatedAt"]})
	within(202, "", "POST", "/api/signup", map[string]any{"username": "asks", "password": "a fine long password", "reason": "One connection."})
	within(202, "", "POST", "/api/signup", map[string]any{"username": "asks2", "password": "a fine long password"})
	within(403, "", "POST", "/api/signin", map[string]any{"username": "asks", "password": "a fine long password"})
	within(200, admin, "GET", "/api/admin/requests", nil)
	var asks, asks2 int64
	e.a.db.QueryRow(`SELECT id FROM join_requests WHERE username = 'asks'`).Scan(&asks)
	e.a.db.QueryRow(`SELECT id FROM join_requests WHERE username = 'asks2'`).Scan(&asks2)
	within(200, admin, "POST", fmt.Sprintf("/api/admin/requests/%d/approve", asks), nil)
	within(200, admin, "DELETE", fmt.Sprintf("/api/admin/requests/%d", asks2), nil)
	within(200, admin, "GET", "/api/admin/dashboard", nil)
	within(200, admin, "DELETE", fmt.Sprintf("/api/admin/users/%d", goneID), nil)
	pub := within(200, admin, "GET", "/api/admin/catalog/poi", nil)["published"]
	within(200, admin, "PUT", "/api/admin/catalog/poi/draft", map[string]any{"body": pub})
	within(200, admin, "POST", "/api/admin/catalog/poi/publish", map[string]any{"note": "one connection"})
	within(200, admin, "POST", "/api/admin/catalog/poi/revisions/1/restore", nil)
	within(200, ana, "PUT", "/api/me", map[string]any{"prefs": map[string]bool{"shareTransitions": true}})
	within(200, ana, "POST", "/api/suggest/labels", map[string]any{"prop": "nunchaku", "deltas": []map[string]any{{"a": "F5", "b": "F7", "from": "untested", "to": "works"}}})
	within(200, ana, "DELETE", fmt.Sprintf("/api/comments/%d", r), nil)
	within(200, bo, "DELETE", fmt.Sprintf("/api/comments/%d", c), nil)
	within(200, ana, "DELETE", fmt.Sprintf("/api/posts/%d", p), nil)
	within(200, ana, "DELETE", fmt.Sprintf("/api/posts/%d", pm), nil)
	e.a.db.SetMaxOpenConns(8)
	if e.count(`SELECT count(*) FROM users WHERE id = $1`, goneID) != 0 || e.count(`SELECT count(*) FROM posts WHERE user_id = $1`, anaID) != 0 ||
		e.count(`SELECT count(*) FROM users WHERE username = 'asks'`) != 1 || e.count(`SELECT count(*) FROM join_requests`) != 0 {
		t.Fatal("the writes on one connection did not all happen")
	}

	// Single-profile mode on one connection: the start that makes the sole member the admin and owner,
	// the visitor's page made while a save drops it, the owner's app, and the closed routes.
	s := newEnv(t)
	_, owner := s.user("solo", "user")
	s.a.db.SetMaxOpenConns(1)
	done, st := make(chan error, 1), staticOf(t, map[string]string{"index.html": "<!doctype html><title>Flowa</title>"})
	go func() { done <- s.singleStart(nil, st) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the single-mode start did not finish in 10 s on one connection")
	}
	e = s
	within(200, "", "GET", "/", nil)
	within(200, owner, "PUT", "/api/me", map[string]any{"bio": "one connection, one profile"})
	within(200, "", "GET", "/", nil)
	within(200, owner, "GET", "/", nil)
	inst = within(200, owner, "GET", "/api/admin/instance", nil)
	within(200, owner, "PUT", "/api/admin/instance", map[string]any{"description": "Mine.", "contact": "", "rules": []any{}, "updatedAt": inst["updatedAt"]})
	within(200, "", "GET", "/", nil)
	within(403, "", "POST", "/api/signup", map[string]any{"username": "another", "password": "a fine long password"})
	within(403, owner, "GET", "/api/admin/requests", nil)
	s.a.db.SetMaxOpenConns(8)
	if s.count(`SELECT count(*) FROM users WHERE role = 'admin' AND owner`) != 1 {
		t.Fatal("the sole member was not made the admin and the owner on one connection")
	}

	// The public addresses of a mode that federates (FRD 5), with the skill names to read from the database
	// (an empty cache), for a post with a chip and a combo: each answers on one connection, so none holds a
	// connection while it asks for another.
	f := newPubFixture(t)
	asked := func(x *tenv, path, accept string, want int) {
		t.Helper()
		x.a.namesMu.Lock()
		x.a.names = nil
		x.a.namesMu.Unlock()
		at := make(chan int, 1)
		go func() { at <- pubAsk(x, "GET", path, accept).Code }()
		select {
		case code := <-at:
			if code != want {
				t.Errorf("GET %s (%q) on one connection: %d, want %d", path, accept, code, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("GET %s (%q) did not answer in 10 s on one connection: it holds a connection while it asks for another", path, accept)
		}
	}
	f.e.a.db.SetMaxOpenConns(1)
	for _, accept := range []string{browser, apAccept} {
		asked(f.e, "/users/ana", accept, 200)
		asked(f.e, fmt.Sprintf("/users/ana/posts/%d", f.pub), accept, 200)
		asked(f.e, "/actor", accept, 200)
	}
	asked(f.e, "/users/ana/avatar.png", "", 200)
	asked(f.e, fmt.Sprintf("/users/ana/posts/%d/picture.jpg", f.pub), "", 200)
	// The outbox and the collections (FRD 6), with 25 more Public posts with chips and a combo, so its first
	// page makes 20 Creates and a second page follows; the counts with both their flags on.
	f.e.a.db.SetMaxOpenConns(8)
	f.e.setFlag("follows", true)
	f.e.mustExec(`INSERT INTO posts (user_id, body, audience, data) SELECT $1, 'chips ' || g, 'public',
		'{"tags":[{"prop":"poi","skill":"F1"},{"prop":"staff","skill":"F1"}],"combo":{"id":"c","name":"C","role":"","prop":"nunchaku","seq":["F7","F5"],"reps":[1,1]}}'
		FROM generate_series(1, 25) g`, f.ana)
	var older int64
	f.e.a.db.QueryRow(`SELECT id FROM posts WHERE user_id = $1 AND audience = 'public' AND hidden_at IS NULL AND kind = 'post' ORDER BY id DESC OFFSET 19 LIMIT 1`, f.ana).Scan(&older)
	f.e.a.db.SetMaxOpenConns(1)
	for _, p := range []string{"/users/ana/outbox", "/users/ana/outbox?page=true", fmt.Sprintf("/users/ana/outbox?page=true&before=%d", older),
		fmt.Sprintf("/users/ana/posts/%d/activity", f.pub), "/users/ana/followers", "/users/ana/following", "/users/ana/featured", "/actor/outbox"} {
		asked(f.e, p, apAccept, 200)
	}
	f.e.a.db.SetMaxOpenConns(8)
	// Single mode with FLOWA_FEDERATE=on: the page at / lists the owner's Public posts.
	sf := newEnv(t)
	if err := sf.fedSingleStart(nil); err != nil {
		t.Fatal(err)
	}
	sf.mustExec(`INSERT INTO posts (user_id, body, audience, data) SELECT id, 'chips', 'public', '{"tags":[{"prop":"poi","skill":"F1"}],"combo":{"id":"c","name":"C","role":"","prop":"poi","seq":["F1"],"reps":[1]}}' FROM users WHERE owner`)
	sf.h = sf.a.routes(staticOf(t, map[string]string{"index.html": "<!doctype html><title>Flowa</title>"}))
	sf.a.dropVisitorPage()
	sf.a.db.SetMaxOpenConns(1)
	asked(sf, "/", "", 200)
	sf.a.db.SetMaxOpenConns(8)
	// Remote actors (FRD 7) on one connection: a fetch waiting on the network holds no connection and no
	// lock (the row is read and closed first, the write is a transaction after), so while one is held at the
	// stub the pool's one connection still answers, another actor is fetched and stored meanwhile, and the
	// held one finishes once let go.
	re := newRemoteEnv(t)
	slow, quick := "https://remote.example/users/slow", "https://other.example/users/quick"
	re.w.put(slow, worldDoc{body: actorDoc(slow, "slow", "", nil, nil)})
	re.w.put(quick, worldDoc{body: actorDoc(quick, "quick", "", nil, nil)})
	re.a.db.SetMaxOpenConns(1)
	release := re.w.holdAt(slow)
	defer release()
	held := make(chan error, 1)
	go func() { _, err := re.actor(slow); held <- err }()
	<-re.w.arrived
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var one int
	if err := re.a.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("while a fetch waits on the network, the pool's one connection is taken: %v", err)
	}
	if row, err := re.a.fed.remoteActor(ctx, quick); err != nil || row.Username != "quick@other.example" {
		t.Fatalf("another actor while one waits on the network, on one connection: %+v %v", row, err)
	}
	release()
	select {
	case err := <-held:
		if err != nil {
			t.Fatalf("the held fetch: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("the held fetch did not finish on one connection")
	}
	re.a.db.SetMaxOpenConns(8)
}

// The public pages under load at the real pool size: 40 requests at once, at a cold names cache, for a
// profile whose posts have chips and a combo, as pages and as documents, and its outbox, two of its pages,
// a Create and its collections (FRD 6). Every one is answered, within a
// bound the requests carry as their context, so a pool held up would show as failed answers, not a hang.
func TestDBPublicPool(t *testing.T) {
	f := newPubFixture(t)
	e := f.e
	e.a.db.SetMaxOpenConns(8) // as openDB sets it
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	e.setFlag("follows", true)
	e.mustExec(`INSERT INTO posts (user_id, body, audience, data) SELECT $1, 'chips ' || g, 'public',
		'{"tags":[{"prop":"poi","skill":"F1"},{"prop":"staff","skill":"F1"}],"combo":{"id":"c","name":"C","role":"","prop":"nunchaku","seq":["F7","F5"],"reps":[1,1]}}'
		FROM generate_series(1, 25) g`, f.ana)
	e.a.namesMu.Lock()
	e.a.names = nil
	e.a.namesMu.Unlock()
	// The pages and documents of FRD 5, and the outbox, its pages, a Create and the collections of FRD 6.
	paths := []string{"/users/ana", fmt.Sprintf("/users/ana/posts/%d", f.pub), "/users/ana/outbox?page=true", "/users/ana/outbox",
		fmt.Sprintf("/users/ana/outbox?page=true&before=%d", f.pub+1), fmt.Sprintf("/users/ana/posts/%d/activity", f.pub),
		"/users/ana/followers", "/users/ana/following", "/users/ana/featured", "/actor/outbox"}
	codes := make([]int, 40)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range codes {
		wg.Go(func() {
			p := paths[i%len(paths)]
			r := httptest.NewRequest("GET", p, nil).WithContext(ctx)
			r.RemoteAddr = "198.51.100.20:4000"
			accept := apAccept // the documents of FRD 6 send a browser on with 303
			if i%len(paths) < 2 && (i/len(paths))%2 == 0 {
				accept = browser
			}
			r.Header.Set("Accept", accept)
			w := httptest.NewRecorder()
			<-start
			e.h.ServeHTTP(w, r)
			codes[i] = w.Code
		})
	}
	close(start)
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-ctx.Done():
		<-finished // the requests carry ctx: once it ends every query returns, and so does every request
	}
	for i, c := range codes {
		if c != 200 {
			t.Errorf("request %d (%s): %d", i, paths[i%len(paths)], c)
		}
	}
}

func TestDBBookmarks(t *testing.T) {
	e := newEnv(t)
	_, kim := e.user("kim", "user")
	leeID, lee := e.user("lee", "user")
	p := e.post(lee, "a post worth keeping")
	ref := fmt.Sprint(p)
	// Toggling: on, then off, then on again.
	if out := e.must(200, kim, "POST", "/api/bookmarks", map[string]any{"kind": "post", "ref": ref}); out["on"] != true {
		t.Fatalf("first toggle: %v", out)
	}
	if out := e.must(200, kim, "POST", "/api/bookmarks", map[string]any{"kind": "post", "ref": ref}); out["on"] != false {
		t.Fatalf("second toggle: %v", out)
	}
	e.must(200, kim, "POST", "/api/bookmarks", map[string]any{"kind": "post", "ref": ref})
	e.must(200, kim, "POST", "/api/bookmarks", map[string]any{"kind": "skill", "ref": "nunchaku:F5"})
	e.must(200, kim, "POST", "/api/bookmarks", map[string]any{"kind": "combo", "ref": "m3k9ab1x"})
	list := e.must(200, kim, "GET", "/api/bookmarks", nil)
	if fmt.Sprint(list["skills"]) != "[nunchaku:F5]" || fmt.Sprint(list["combos"]) != "[m3k9ab1x]" || fmt.Sprint(list["postIds"]) != "["+ref+"]" {
		t.Fatalf("list: %v", list["_raw"])
	}
	posts := list["posts"].([]any)
	if len(posts) != 1 || num(posts[0].(map[string]any)["id"]) != p || list["users"].(map[string]any)[fmt.Sprint(leeID)] == nil {
		t.Fatalf("posts in the list: %v", list["_raw"])
	}
	// Bad refs and kinds.
	for _, in := range []map[string]any{{"kind": "skill", "ref": "drums:F1"}, {"kind": "skill", "ref": "F1"}, {"kind": "combo", "ref": "has space"}, {"kind": "nope", "ref": "x"}, {"kind": "post", "ref": "abc"}} {
		e.must(400, kim, "POST", "/api/bookmarks", in)
	}
	e.must(404, kim, "POST", "/api/bookmarks", map[string]any{"kind": "post", "ref": "999999"})
	// A post hidden by a moderator leaves the list and cannot be bookmarked by others.
	e.a.db.Exec(`UPDATE posts SET hidden_at = now(), hidden_reason = 'spam' WHERE id = $1`, p)
	if l := e.must(200, kim, "GET", "/api/bookmarks", nil); fmt.Sprint(l["postIds"]) != "[]" {
		t.Fatalf("hidden post still listed: %v", l["_raw"])
	}
	e.a.db.Exec(`UPDATE posts SET hidden_at = NULL, hidden_reason = '' WHERE id = $1`, p)
	// The cap: with 500 bookmarks, one more is refused but removing still works.
	for i := 0; i < maxBookmarks-3; i++ {
		e.a.db.Exec(`INSERT INTO bookmarks (user_id, kind, ref) VALUES ((SELECT id FROM users WHERE username = 'kim'), 'combo', $1)`, fmt.Sprintf("fill%d", i))
	}
	e.must(400, kim, "POST", "/api/bookmarks", map[string]any{"kind": "skill", "ref": "poi:F1"})
	if out := e.must(200, kim, "POST", "/api/bookmarks", map[string]any{"kind": "skill", "ref": "nunchaku:F5"}); out["on"] != false {
		t.Fatalf("removing at the cap: %v", out)
	}
	// Deleting the post drops its bookmark at the next housekeeping.
	e.a.db.Exec(`DELETE FROM posts WHERE id = $1`, p)
	n, err := e.a.housekeep()
	if err != nil || n["bookmarks"] != 1 {
		t.Fatalf("housekeep: %v %v", n, err)
	}
	if c := e.count(`SELECT count(*) FROM bookmarks WHERE kind = 'post'`); c != 0 {
		t.Fatalf("post bookmarks left: %d", c)
	}
	// Someone else sees none of it, and with the flag off the endpoints are gone.
	if l := e.must(200, lee, "GET", "/api/bookmarks", nil); fmt.Sprint(l["skills"]) != "[]" || fmt.Sprint(l["combos"]) != "[]" {
		t.Fatalf("lee sees kim's bookmarks: %v", l["_raw"])
	}
	e.setFlag("bookmarks", false)
	e.must(404, kim, "GET", "/api/bookmarks", nil)
}

// A post or a comment sent twice under one Idempotency-Key is made once: the second tap on a
// slow network, or the retry after a lost answer, gets the first one back.
func TestDBIdempotentPost(t *testing.T) {
	e := newEnv(t)
	_, ann := e.user("ann", "user")
	_, bob := e.user("bob", "user")
	key := func(k string) map[string]string { return map[string]string{"Idempotency-Key": k} }
	postID := func(out map[string]any) int64 {
		t.Helper()
		list, _ := out["posts"].([]any)
		if len(list) != 1 {
			t.Fatalf("no post in the answer: %v", out["_raw"])
		}
		return num(list[0].(map[string]any)["id"])
	}
	posts := func() int { return e.count(`SELECT count(*) FROM posts`) }

	// The same key twice: one post, the same answer, and the repeat says it is one.
	code, out, h := e.doH(ann, "POST", "/api/posts", map[string]any{"body": "first drop"}, key("key-aaaa-0001"))
	if code != 200 || h.Get("Idempotency-Replayed") != "" {
		t.Fatalf("first: %d %v %v", code, out["_raw"], h)
	}
	first := postID(out)
	code, out, h = e.doH(ann, "POST", "/api/posts", map[string]any{"body": "first drop"}, key("key-aaaa-0001"))
	if code != 200 || postID(out) != first || h.Get("Idempotency-Replayed") != "true" || posts() != 1 {
		t.Fatalf("repeat: %d, post %d want %d, replayed %q, %d posts", code, postID(out), first, h.Get("Idempotency-Replayed"), posts())
	}
	// Another member with the same key, and the same member with another key, each make their own.
	if _, out, _ = e.doH(bob, "POST", "/api/posts", map[string]any{"body": "mine"}, key("key-aaaa-0001")); postID(out) == first {
		t.Fatal("bob was given ann's post")
	}
	if _, out, _ = e.doH(ann, "POST", "/api/posts", map[string]any{"body": "second"}, key("key-aaaa-0002")); postID(out) == first || posts() != 3 {
		t.Fatalf("a new key must make a new post: %d posts", posts())
	}
	// Without a key nothing is remembered: two requests are two posts.
	e.post(ann, "plain")
	e.post(ann, "plain")
	if posts() != 5 {
		t.Fatalf("%d posts, want 5", posts())
	}
	// A key that is not a key is refused before anything is written.
	for _, bad := range []string{"short", "has space in it", strings.Repeat("k", 65), "semi;colon-key"} {
		if code, _, _ := e.doH(ann, "POST", "/api/posts", map[string]any{"body": "x"}, key(bad)); code != 400 {
			t.Fatalf("key %q: %d, want 400", bad, code)
		}
	}
	// A try that was turned down is forgotten, so the corrected post goes through under the same key.
	if code, _, _ := e.doH(ann, "POST", "/api/posts", map[string]any{"body": "   "}, key("key-aaaa-0003")); code != 400 {
		t.Fatalf("empty post: %d", code)
	}
	if code, out, h := e.doH(ann, "POST", "/api/posts", map[string]any{"body": "now with words"}, key("key-aaaa-0003")); code != 200 || h.Get("Idempotency-Replayed") != "" || posts() != 6 {
		t.Fatalf("corrected post: %d %v, %d posts", code, out["_raw"], posts())
	}
	// Eight copies at once, as a burst of taps would send them: one post, eight identical answers.
	ids := make(chan int64, 8)
	for i := 0; i < 8; i++ {
		go func() {
			code, out, _ := e.doH(ann, "POST", "/api/posts", map[string]any{"body": "burst"}, key("key-aaaa-0004"))
			list, _ := out["posts"].([]any)
			if code != 200 || len(list) != 1 {
				ids <- -int64(code)
				return
			}
			ids <- num(list[0].(map[string]any)["id"])
		}()
	}
	burst := <-ids
	for i := 1; i < 8; i++ {
		if id := <-ids; id != burst || id <= 0 {
			t.Fatalf("burst answers differ: %d and %d", burst, id)
		}
	}
	if posts() != 7 {
		t.Fatalf("%d posts after the burst, want 7", posts())
	}

	// Comments: the same key on the same post is one comment, and the key is separate per post.
	path := fmt.Sprintf("/api/posts/%d/comments", first)
	code, out, _ = e.doH(bob, "POST", path, map[string]any{"body": "nice"}, key("key-bbbb-0001"))
	if code != 200 {
		t.Fatalf("comment: %d %v", code, out["_raw"])
	}
	cid := num(out["comment"].(map[string]any)["id"])
	code, out, h = e.doH(bob, "POST", path, map[string]any{"body": "nice"}, key("key-bbbb-0001"))
	if code != 200 || num(out["comment"].(map[string]any)["id"]) != cid || postID(out) != first || h.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("repeated comment: %d %v", code, out["_raw"])
	}
	if n := e.count(`SELECT count(*) FROM comments`); n != 1 {
		t.Fatalf("%d comments, want 1", n)
	}
	if n := e.count(`SELECT count(*) FROM notifications WHERE kind = 'comment'`); n != 1 {
		t.Fatalf("ann was told %d times about one comment", n)
	}
	other := e.post(ann, "another post")
	if _, out, _ = e.doH(bob, "POST", fmt.Sprintf("/api/posts/%d/comments", other), map[string]any{"body": "nice"}, key("key-bbbb-0001")); num(out["comment"].(map[string]any)["id"]) == cid {
		t.Fatal("the key of a comment on one post answered for another post")
	}
	// A repeat of a comment that was removed since says so; it does not write it again.
	e.a.db.Exec(`DELETE FROM comments WHERE id = $1`, cid)
	if code, _, _ := e.doH(bob, "POST", path, map[string]any{"body": "nice"}, key("key-bbbb-0001")); code != 404 {
		t.Fatalf("repeat of a removed comment: %d, want 404", code)
	}

	// A repeat is a read: it is answered when the hourly allowance is used up, and costs nothing.
	e.a.lim.posts = newLimiter(1, time.Hour)
	if code, _, _ := e.doH(bob, "POST", "/api/posts", map[string]any{"body": "last one"}, key("key-cccc-0001")); code != 200 {
		t.Fatalf("post within the allowance: %d", code)
	}
	if code, _, h := e.doH(bob, "POST", "/api/posts", map[string]any{"body": "last one"}, key("key-cccc-0001")); code != 200 || h.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("repeat with the allowance used up: %d", code)
	}
	if code, _, _ := e.doH(bob, "POST", "/api/posts", map[string]any{"body": "one more"}, key("key-cccc-0002")); code != 429 {
		t.Fatalf("a new post over the allowance: %d, want 429", code)
	}
	// The refused one was forgotten too: once there is room, its key makes the post.
	e.a.lim.posts = newLimiter(5, time.Hour)
	if code, _, h := e.doH(bob, "POST", "/api/posts", map[string]any{"body": "one more"}, key("key-cccc-0002")); code != 200 || h.Get("Idempotency-Replayed") != "" {
		t.Fatalf("after the allowance came back: %d", code)
	}

	// Keys are forgotten after the window, and the store never grows past its cap.
	s := e.a.sent
	s.mu.Lock()
	for _, en := range s.m {
		en.at = time.Now().Add(-onceWindow - time.Minute)
	}
	s.swept = time.Time{}
	s.mu.Unlock()
	before := posts()
	if code, _, h := e.doH(ann, "POST", "/api/posts", map[string]any{"body": "first drop"}, key("key-aaaa-0001")); code != 200 || h.Get("Idempotency-Replayed") != "" || posts() != before+1 {
		t.Fatalf("a key older than the window must make a new post: %d, %d posts", code, posts())
	}
	s.mu.Lock()
	left := len(s.m)
	s.mu.Unlock()
	if left != 1 {
		t.Fatalf("%d keys remembered after the sweep, want 1", left)
	}
}

// A profile save writes only what it names, and a save made against an older copy of the bio,
// links and preferences is turned down instead of undoing what another device saved.
func TestDBProfileRev(t *testing.T) {
	e := newEnv(t)
	_, phone := e.user("pat", "user")
	id := int64(e.count(`SELECT id FROM users WHERE username = 'pat'`))
	laptop := e.session(id)
	userOf := func(out map[string]any) map[string]any { t.Helper(); return out["user"].(map[string]any) }
	rev := func(tok string) string {
		t.Helper()
		r, _ := userOf(e.must(200, tok, "GET", "/api/me", nil))["rev"].(string)
		if r == "" {
			t.Fatal("GET /api/me carries no rev")
		}
		return r
	}
	start := rev(phone)
	if rev(laptop) != start {
		t.Fatal("two sessions of one member see different revs")
	}
	// The phone saves a bio. The answer carries the new rev.
	out := e.must(200, phone, "PUT", "/api/me", map[string]any{"bio": "Poi since 2019.", "rev": start})
	afterBio, _ := userOf(out)["rev"].(string)
	if afterBio == "" || afterBio == start {
		t.Fatalf("rev after a bio change: %q (was %q)", afterBio, start)
	}
	// The laptop, still on the old copy, saves a link: turned down, nothing written, and the
	// answer carries the account as it is now so the page can show it.
	code, out := e.do(laptop, "PUT", "/api/me", map[string]any{"links": map[string]string{"instagram": "pat.spins"}, "rev": start})
	if code != 409 || userOf(out)["bio"] != "Poi since 2019." || userOf(out)["rev"] != afterBio || !strings.Contains(fmt.Sprint(out["error"]), "another device") {
		t.Fatalf("stale save: %d %v", code, out["_raw"])
	}
	if n := e.count(`SELECT count(*) FROM users WHERE username = 'pat' AND links = '{}'::jsonb`); n != 1 {
		t.Fatal("a refused save wrote its link")
	}
	// Saved again on the fresh rev, the link lands and the bio is still there.
	out = e.must(200, laptop, "PUT", "/api/me", map[string]any{"links": map[string]string{"instagram": "pat.spins"}, "rev": afterBio})
	u := userOf(out)
	if u["bio"] != "Poi since 2019." || fmt.Sprint(u["links"]) != "map[instagram:pat.spins]" {
		t.Fatalf("after the second save: %v", out["_raw"])
	}
	afterLink, _ := u["rev"].(string)
	// Links and preferences are kept per key: naming one leaves the others, an empty one goes.
	out = e.must(200, phone, "PUT", "/api/me", map[string]any{"links": map[string]string{"youtube": "@patspins"}, "prefs": map[string]bool{"autoPost": true}})
	u = userOf(out)
	if l := u["links"].(map[string]any); l["instagram"] != "pat.spins" || l["youtube"] == nil {
		t.Fatalf("links after adding one: %v", u["links"])
	}
	out = e.must(200, phone, "PUT", "/api/me", map[string]any{"links": map[string]string{"instagram": ""}, "prefs": map[string]bool{"shareTransitions": true}})
	u = userOf(out)
	if l := u["links"].(map[string]any); len(l) != 1 || l["youtube"] == nil {
		t.Fatalf("links after clearing one: %v", u["links"])
	}
	if p := u["prefs"].(map[string]any); p["autoPost"] != true || p["shareTransitions"] != true {
		t.Fatalf("prefs kept per key: %v", u["prefs"])
	}
	if u["rev"] == afterLink {
		t.Fatal("the rev did not move with the links")
	}
	// What the page writes by itself does not move the rev and does not need one: stats, the
	// notes already read, the display name, the picture. A save made on the same rev still lands.
	cur := rev(laptop)
	e.must(200, phone, "PUT", "/api/me", map[string]any{"stats": map[string]any{"xp": 120, "level": 2}})
	e.must(200, phone, "PUT", "/api/me", map[string]any{"seen": []string{"tour"}})
	e.must(200, phone, "PUT", "/api/me", map[string]any{"displayName": "Pat"})
	e.must(200, phone, "PUT", "/api/me", map[string]any{"avatar": dataURL(t, "jpeg", 40, 40, nil)})
	if rev(laptop) != cur {
		t.Fatal("stats, seen, name or picture moved the profile rev")
	}
	out = e.must(200, laptop, "PUT", "/api/me", map[string]any{"bio": "Poi and staff.", "rev": cur})
	u = userOf(out)
	// And the bio save did not put back the old stats, name, picture or read notes it had loaded.
	if u["displayName"] != "Pat" || fmt.Sprint(u["seen"]) != "[tour]" || u["avatar"] == "" || !strings.Contains(fmt.Sprint(u["stats"]), "120") {
		t.Fatalf("a bio save overwrote other fields: %v", out["_raw"])
	}
	// A stats write that arrives after the bio save keeps the bio (it used to write every column).
	e.must(200, phone, "PUT", "/api/me", map[string]any{"stats": map[string]any{"xp": 130, "level": 2}})
	if got := userOf(e.must(200, phone, "GET", "/api/me", nil))["bio"]; got != "Poi and staff." {
		t.Fatalf("bio after a later stats write: %v", got)
	}
	// A wrong rev is refused whatever else the body holds, and a body that fails its checks is a
	// 400 before the rev is looked at. An empty body changes nothing and answers with the account.
	e.must(409, phone, "PUT", "/api/me", map[string]any{"stats": map[string]any{"xp": 1}, "rev": "0000000000000000"})
	e.must(400, phone, "PUT", "/api/me", map[string]any{"bio": strings.Repeat("x", 301), "rev": "0000000000000000"})
	e.must(400, phone, "PUT", "/api/me", map[string]any{"prefs": map[string]bool{"nope": true}})
	if got := userOf(e.must(200, phone, "PUT", "/api/me", map[string]any{})); got["bio"] != "Poi and staff." || got["rev"] == "" {
		t.Fatalf("empty save: %v", got)
	}
	// Only the owner's own copy carries the rev.
	_, other := e.user("quin", "user")
	if raw := e.must(200, other, "GET", "/api/users/pat", nil)["_raw"].(string); strings.Contains(raw, `"rev"`) {
		t.Fatalf("someone else's view of the profile carries a rev: %s", raw)
	}
	// Two saves at once on the same rev: exactly one wins.
	cur = rev(phone)
	codes := make(chan int, 2)
	for _, b := range []string{"From the phone.", "From the laptop."} {
		go func() {
			c, _ := e.do(phone, "PUT", "/api/me", map[string]any{"bio": b, "rev": cur})
			codes <- c
		}()
	}
	if a, b := <-codes, <-codes; a+b != 200+409 {
		t.Fatalf("two saves on one rev answered %d and %d, want one 200 and one 409", a, b)
	}
}

// The link to the machine's start page is off unless FLOWA_HOME_URL is set, and only admins get it.
func TestDBHomeLink(t *testing.T) {
	e := newEnv(t)
	_, admin := e.user("admin", "admin")
	_, mod := e.user("mo", "moderator")
	_, member := e.user("sandy", "user")
	for _, tok := range []string{admin, mod, member, ""} {
		if out := e.must(200, tok, "GET", "/api/me", nil); out["homeUrl"] != nil {
			t.Fatalf("with no FLOWA_HOME_URL the answer has a home link: %v", out["homeUrl"])
		}
	}
	e.a.homeURL = "http://pi.local:8080/"
	if out := e.must(200, admin, "GET", "/api/me", nil); out["homeUrl"] != "http://pi.local:8080/" {
		t.Fatalf("an admin gets %v, want the link", out["homeUrl"])
	}
	for who, tok := range map[string]string{"a moderator": mod, "a member": member, "a visitor": ""} {
		if out := e.must(200, tok, "GET", "/api/me", nil); out["homeUrl"] != nil {
			t.Fatalf("%s is told where the home page is: %v", who, out["homeUrl"])
		}
	}
}

// Reading notifications: one by id, all at once, and never someone else's.
func TestDBNotificationsRead(t *testing.T) {
	e := newEnv(t)
	kimID, kim := e.user("kim", "user")
	leeID, lee := e.user("lee", "user")
	add := func(uid int64, n int) []int64 {
		t.Helper()
		ids := []int64{}
		for i := 0; i < n; i++ {
			var id int64
			if err := e.a.db.QueryRow(`INSERT INTO notifications (user_id, kind, actor_id) VALUES ($1, 'like', $2) RETURNING id`, uid, leeID).Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		return ids
	}
	// state returns the unread count the bell shows and, for each listed notification, whether it is read.
	state := func(tok string) (int64, map[int64]bool) {
		t.Helper()
		out := e.must(200, tok, "GET", "/api/notifications", nil)
		read := map[int64]bool{}
		for _, it := range out["items"].([]any) {
			m := it.(map[string]any)
			read[num(m["id"])] = m["read"] == true
		}
		return num(out["unread"]), read
	}
	k := add(kimID, 3)
	l := add(leeID, 2)
	if n, read := state(kim); n != 3 || len(read) != 3 || read[k[0]] || read[k[1]] || read[k[2]] {
		t.Fatalf("new notifications: unread %d, read %v", n, read)
	}
	// Signed out there is nothing to read or to mark.
	e.must(401, "", "GET", "/api/notifications", nil)
	e.must(401, "", "PUT", "/api/notifications/read", map[string]any{})
	// One by id, the way opening it from the bell does. Doing it twice changes nothing more.
	for i := 0; i < 2; i++ {
		e.must(200, kim, "PUT", "/api/notifications/read", map[string]any{"ids": []int64{k[1]}})
		if n, read := state(kim); n != 2 || read[k[0]] || !read[k[1]] || read[k[2]] {
			t.Fatalf("after reading one (time %d): unread %d, read %v", i+1, n, read)
		}
	}
	// Someone else's ids, and ids that do not exist, mark nothing: not theirs, and not kim's own.
	e.must(200, kim, "PUT", "/api/notifications/read", map[string]any{"ids": []int64{l[0], l[1], 999999, -1, 0}})
	if n, _ := state(lee); n != 2 {
		t.Fatalf("kim read lee's notifications: lee has %d unread, want 2", n)
	}
	if n, _ := state(kim); n != 2 {
		t.Fatalf("ids that are not kim's changed kim's count to %d, want 2", n)
	}
	// What is not a list of numbers is refused and marks nothing.
	for _, body := range []any{map[string]any{"ids": "all"}, map[string]any{"ids": []string{"x"}}, "ids", []int64{k[0]}} {
		e.must(400, kim, "PUT", "/api/notifications/read", body)
	}
	huge := make([]int64, 4000)
	for i := range huge {
		huge[i] = int64(1000000 + i)
	}
	e.must(400, kim, "PUT", "/api/notifications/read", map[string]any{"ids": huge})
	if n, _ := state(kim); n != 2 {
		t.Fatalf("a refused request marked something: unread %d, want 2", n)
	}
	// "Mark all read" sends no ids, or an empty list; both mark every one of kim's and only kim's.
	e.must(200, kim, "PUT", "/api/notifications/read", map[string]any{"ids": []int64{}})
	if n, read := state(kim); n != 0 || !read[k[0]] || !read[k[2]] {
		t.Fatalf("after marking all: unread %d, read %v", n, read)
	}
	if n, _ := state(lee); n != 2 {
		t.Fatalf("kim's mark all reached lee: lee has %d unread, want 2", n)
	}
	// A new one after that is unread again, and an empty body marks it.
	fresh := add(kimID, 1)[0]
	if n, read := state(kim); n != 1 || read[fresh] {
		t.Fatalf("a new notification: unread %d, read %v", n, read[fresh])
	}
	e.must(200, kim, "PUT", "/api/notifications/read", map[string]any{})
	if n, _ := state(kim); n != 0 {
		t.Fatalf("after an empty body: unread %d, want 0", n)
	}
	// The list is capped but the count is not: unread ones older than the list still count, and
	// marking all reaches them too.
	add(kimID, maxNotifList+5)
	if n, read := state(kim); n != maxNotifList+5 || len(read) != maxNotifList {
		t.Fatalf("a long list: unread %d (want %d), listed %d (want %d)", n, maxNotifList+5, len(read), maxNotifList)
	}
	e.must(200, kim, "PUT", "/api/notifications/read", map[string]any{})
	if c := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND NOT read`, kimID); c != 0 {
		t.Fatalf("marking all left %d unread beyond the list", c)
	}
	// With the community off the route is gone, and what was unread stays unread.
	e.setFlag("community", false)
	e.must(404, lee, "PUT", "/api/notifications/read", map[string]any{})
	if c := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND NOT read`, leeID); c != 2 {
		t.Fatalf("a refused request marked lee's: %d unread, want 2", c)
	}
}

// The build's name is for the people who run Flowa: the dashboard tells them, and nobody else is told.
func TestDBVersion(t *testing.T) {
	e := newEnv(t)
	_, admin := e.user("boss", "admin")
	_, mod := e.user("mo", "moderator")
	_, member := e.user("sandy", "user")
	// With no VERSION set the build says so, instead of an empty line on the dashboard.
	if out := e.must(200, admin, "GET", "/api/admin/dashboard", nil); out["version"] != "dev" {
		t.Fatalf("with no VERSION the dashboard says %v, want dev", out["version"])
	}
	e.a.version = "v1.2.3"
	for who, tok := range map[string]string{"an admin": admin, "a moderator": mod} {
		if out := e.must(200, tok, "GET", "/api/admin/dashboard", nil); out["version"] != "v1.2.3" {
			t.Fatalf("%s is told %v, want v1.2.3", who, out["version"])
		}
	}
	e.must(403, member, "GET", "/api/admin/dashboard", nil)
	e.must(401, "", "GET", "/api/admin/dashboard", nil)
	// Nothing a member or a visitor can fetch names the build: which commit runs is a help to an attacker.
	for who, tok := range map[string]string{"a member": member, "a visitor": ""} {
		for _, path := range []string{"/api/me", "/healthz", "/api/admin/dashboard"} {
			_, out, hdr := e.doH(tok, "GET", path, nil, nil)
			if strings.Contains(fmt.Sprint(out["_raw"], hdr), "v1.2.3") {
				t.Fatalf("%s reads the version at %s", who, path)
			}
		}
	}
}

// TestDBHealthz: a working instance answers 200; a probe from the machine itself reads the schema version,
// the build and whether TLS roots are present, and anyone else only that it works. A schema older than the
// build needs, or a database that is there without its tables, is not healthy.
func TestDBHealthz(t *testing.T) {
	e := newEnv(t)
	e.a.version = "v1.2.3"
	fresh := func() { e.a.healthMu.Lock(); e.a.healthAt = time.Time{}; e.a.healthMu.Unlock() }
	code, out := probe(t, e.h, "127.0.0.1:4000", nil)
	want := map[string]any{"ok": true, "database": true, "schema": float64(len(migrations)), "migrations": float64(len(migrations)), "tlsRoots": rootsPresent(), "version": "v1.2.3"}
	if code != 200 || !reflect.DeepEqual(out, want) {
		t.Fatalf("a local probe gets %d %v, want 200 %v", code, out, want)
	}
	if code, out := probe(t, e.h, "203.0.113.9:5000", nil); code != 200 || len(out) != 1 || out["ok"] != true {
		t.Fatalf("a visitor gets %d %v, want 200 and only ok", code, out)
	}

	// Probes within a second share one answer, so a flood of them is not a flood of queries.
	if _, err := e.a.db.Exec(`DELETE FROM schema_migrations WHERE version = $1`, len(migrations)); err != nil {
		t.Fatal(err)
	}
	if code, out := probe(t, e.h, "127.0.0.1:4000", nil); code != 200 || out["schema"] != float64(len(migrations)) {
		t.Fatalf("a second probe in the same second gets %d %v, want the first answer again", code, out)
	}
	// After that the database is asked again. Its schema is now one migration short of what the build needs.
	fresh()
	code, out = probe(t, e.h, "127.0.0.1:4000", nil)
	if code != 503 || out["ok"] != false || out["database"] != true || out["schema"] != float64(len(migrations)-1) {
		t.Fatalf("with a migration missing a local probe gets %d %v, want 503 with the database up and schema %d", code, out, len(migrations)-1)
	}
	if code, out := probe(t, e.h, "203.0.113.9:5000", nil); code != 503 || len(out) != 1 || out["ok"] != false {
		t.Fatalf("with a migration missing a visitor gets %d %v, want 503 and only ok", code, out)
	}
	// A schema newer than the build knows is what going back an image leaves, and that works.
	if _, err := e.a.db.Exec(`INSERT INTO schema_migrations (version) VALUES ($1), ($2)`, len(migrations), len(migrations)+1); err != nil {
		t.Fatal(err)
	}
	fresh()
	if code, out := probe(t, e.h, "127.0.0.1:4000", nil); code != 200 || out["schema"] != float64(len(migrations)+1) || out["migrations"] != float64(len(migrations)) {
		t.Fatalf("with a newer schema a local probe gets %d %v, want 200 and schema %d", code, out, len(migrations)+1)
	}
	// The database answers but the table is gone: up, and not healthy.
	if _, err := e.a.db.Exec(`ALTER TABLE schema_migrations RENAME TO schema_migrations_away`); err != nil {
		t.Fatal(err)
	}
	fresh()
	if code, out := probe(t, e.h, "127.0.0.1:4000", nil); code != 503 || out["database"] != true || out["schema"] != float64(0) {
		t.Fatalf("with the table gone a local probe gets %d %v, want 503 with the database up and schema 0", code, out)
	}
}

// TestDBRequestLog: with a database behind it, a member's write gets a line that says how it went and
// not what was written, and a write over the allowance says which allowance and whose.
func TestDBRequestLog(t *testing.T) {
	e := newEnv(t)
	id, tok := e.user("sandy", "user")
	post := func() *http.Request {
		r := httptest.NewRequest("POST", "/api/posts", strings.NewReader(`{"body":"a secret thought"}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
		return r
	}
	lines := logLines(t, e.a, e.h, post())
	if len(lines) != 1 || lines[0]["method"] != "POST" || lines[0]["route"] != "/api/posts" || lines[0]["status"] != float64(200) || num(lines[0]["bytes"]) < 20 {
		t.Fatalf("a post: got %v, want one line for POST /api/posts with status 200 and the bytes answered", lines)
	}
	for _, secret := range []string{"secret thought", "sandy", tok} {
		if strings.Contains(fmt.Sprint(lines[0]["_raw"]), secret) {
			t.Fatalf("the log line carries %q: %s", secret, lines[0]["_raw"])
		}
	}
	// Thirty posts an hour is the allowance; the next one is refused and its line says so.
	for i := 0; i < 30; i++ {
		e.a.lim.posts.add(fmt.Sprint(id))
	}
	lines = logLines(t, e.a, e.h, post())
	if len(lines) != 1 || lines[0]["status"] != float64(429) || lines[0]["refused"] != fmt.Sprintf("too many posts by member %d", id) {
		t.Fatalf("a post over the allowance: got %v, want 429 and the refusal named", lines)
	}
}

// planNode is one step of a query plan as EXPLAIN (FORMAT JSON) gives it.
type planNode struct {
	Type     string     `json:"Node Type"`
	Relation string     `json:"Relation Name"`
	Rows     float64    `json:"Actual Rows"`
	Loops    float64    `json:"Actual Loops"`
	Removed  float64    `json:"Rows Removed by Filter"`
	Plans    []planNode `json:"Plans"`
}

// walk calls f for n and every step under it.
func (n planNode) walk(f func(planNode)) {
	f(n)
	for _, c := range n.Plans {
		c.walk(f)
	}
}

// The queries behind the timeline, the people list, the notifications, the bookmarks and the admin
// dashboard, read with EXPLAIN ANALYZE on a database the size the roadmap plans for: 500 members,
// 10,000 posts, 30,000 comments, 110,000 likes, 20,000 notifications. None of them may read one of
// the tables that grow with use from end to end, and each has a bound on the rows it reads at all,
// which is what the time a query takes comes from. The tables with a row or a few per member
// (users, sessions, progress) and the reports are read whole where the question is about all of
// them; whole says which, and the bound still holds them.
func TestDBQueryPlans(t *testing.T) {
	e := newEnv(t)
	db := e.a.db
	for _, q := range []string{
		`INSERT INTO users (username, pw_hash, last_seen, stats) SELECT 'm' || g, 'x', now() - (g % 60) * interval '1 day', jsonb_build_object('xp', g * 7 % 5000) FROM generate_series(1, 500) g`,
		// 2,000 remote accounts (FRD 7), on 50 hosts, with ids of their own so the members' ids above stay
		// 1 to 500: the most XP, signed in just now, each with its cached actor and key. Every plan below holds
		// with them, and none of them is read by a statement about local members.
		`INSERT INTO users (id, username, pw_hash, domain, last_seen, stats) SELECT 1000000 + g, 'r' || g || '@h' || (g % 50) || '.example', '!', 'h' || (g % 50) || '.example', now(), '{"xp": 99999}' FROM generate_series(1, 2000) g`,
		`INSERT INTO ap_actors (user_id, ap_id, kind, inbox, key_id, public_key, fetched_at, expires_at) SELECT id, 'https://' || domain || '/users/' || id, 'Person', 'https://' || domain || '/inbox',
			'https://' || domain || '/users/' || id || '#main-key', 'pem', now() - (id % 1000) * interval '1 minute', now() + interval '1 day' FROM users WHERE domain <> ''`,
		`INSERT INTO posts (user_id, body, created_at, hidden_at) SELECT 1 + (g * 7919) % 500, 'post ' || g, now() - (10000 - g) * interval '13 minutes', CASE WHEN g % 97 = 0 THEN now() END FROM generate_series(1, 10000) g`,
		`INSERT INTO comments (post_id, user_id, body, created_at) SELECT 1 + (g * 31) % 10000, 1 + (g * 13) % 500, 'c' || g, now() - (30000 - g) * interval '4 minutes' FROM generate_series(1, 30000) g`,
		`INSERT INTO comments (post_id, user_id, body, parent_id) SELECT c.post_id, 1 + (c.id * 17) % 500, 'r' || c.id, c.id FROM comments c WHERE c.id % 5 = 0`,
		`INSERT INTO likes (post_id, user_id) SELECT p, u FROM generate_series(1, 10000) p, generate_series(1, 500) u WHERE (p * 31 + u * 17) % 100 < 2`,
		`INSERT INTO comment_likes (comment_id, user_id) SELECT c, 1 + (c * 7) % 500 FROM generate_series(1, 30000, 3) c`,
		`INSERT INTO notifications (user_id, actor_id, kind, post_id, comment_id, read) SELECT 1 + g % 500, 1 + (g * 11) % 500, 'comment', 1 + (g * 37) % 10000, 1 + (g * 41) % 30000, g % 3 = 0 FROM generate_series(1, 20000) g`,
		`INSERT INTO bookmarks (user_id, kind, ref) SELECT DISTINCT 1 + g % 500, 'post', (1 + (g * 43) % 10000)::text FROM generate_series(1, 5000) g`,
		`INSERT INTO sessions (token_hash, user_id, expires_at) SELECT 's' || g, 1 + g % 500, now() + interval '30 days' FROM generate_series(1, 1500) g`,
		`INSERT INTO progress (user_id, prop, state) SELECT g, 'nunchaku', '{"sessions":[{"date":"2026-11-03","minutes":20},{"date":"2026-10-20","minutes":15}]}' FROM generate_series(1, 500) g`,
		`INSERT INTO reports (reporter_id, target_kind, target_id, reason, status) SELECT 1 + g % 500, 'post', g, 'spam', CASE WHEN g % 4 = 0 THEN 'open' ELSE 'resolved' END FROM generate_series(1, 200) g`,
		`INSERT INTO blocks (user_id, target_id) SELECT u, 1 + (u * 37 + k * 101) % 500 FROM generate_series(1, 500) u, generate_series(1, 4) k WHERE 1 + (u * 37 + k * 101) % 500 <> u ON CONFLICT DO NOTHING`,
		`ANALYZE`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	const viewer = 5
	var newest, mine []int64
	if err := db.QueryRow(`SELECT array_agg(id) FROM (SELECT id FROM posts ORDER BY id DESC LIMIT $1) p`, pageSize).Scan(pq.Array(&newest)); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT array_agg(ref::bigint) FROM bookmarks WHERE user_id = $1 AND kind = 'post'`, viewer).Scan(pq.Array(&mine)); err != nil || len(mine) == 0 {
		t.Fatalf("member %d has no bookmarks to read: %v", viewer, err)
	}
	var roots []int64
	if err := db.QueryRow(`SELECT array_agg(id) FROM comments WHERE post_id = ANY($1) AND parent_id IS NULL`, pq.Array(newest)).Scan(pq.Array(&roots)); err != nil {
		t.Fatal(err)
	}
	// The tables that grow with every post, like and comment: reading one whole is what an index is for.
	grows := map[string]bool{"posts": true, "comments": true, "likes": true, "comment_likes": true, "notifications": true, "bookmarks": true, "blocks": true, "follows": true}
	var none []int64
	type planCase struct {
		name  string
		sql   string
		args  []any
		most  float64 // rows the plan may read, counting every step
		whole string  // small tables this one reads from end to end, by design
	}
	// on is where the plans are asked: the database, or a transaction a statement that writes is asked in
	// and rolled back.
	var on interface {
		QueryRow(string, ...any) *sql.Row
	} = db
	plans := func(cases []planCase) {
		t.Helper()
		for _, c := range cases {
			var raw []byte
			if err := on.QueryRow(`EXPLAIN (ANALYZE, FORMAT JSON, TIMING OFF, BUFFERS OFF) `+c.sql, c.args...).Scan(&raw); err != nil {
				t.Errorf("%s: %v", c.name, err)
				continue
			}
			var out []struct {
				Plan planNode `json:"Plan"`
			}
			if err := json.Unmarshal(raw, &out); err != nil || len(out) != 1 {
				t.Errorf("%s: the plan cannot be read: %v", c.name, err)
				continue
			}
			read := 0.0
			scanned := map[string]bool{}
			out[0].Plan.walk(func(n planNode) {
				read += (n.Rows + n.Removed) * max(n.Loops, 1)
				if n.Type == "Seq Scan" {
					scanned[n.Relation] = true
					if grows[n.Relation] {
						t.Errorf("%s: reads the whole of %s (%.0f rows) where an index should answer", c.name, n.Relation, (n.Rows+n.Removed)*max(n.Loops, 1))
					} else if !strings.Contains(" "+c.whole+" ", " "+n.Relation+" ") {
						t.Errorf("%s: reads the whole of %s, which this query was not expected to", c.name, n.Relation)
					}
				}
			})
			for _, w := range strings.Fields(c.whole) {
				if !scanned[w] {
					t.Logf("%s: no longer reads %s whole; take it off the list", c.name, w)
				}
			}
			if read > c.most {
				t.Errorf("%s: the plan reads %.0f rows, more than the %.0f it may", c.name, read, c.most)
			}
			t.Logf("%-44s %6.0f rows read", c.name, read)
		}
	}
	plans([]planCase{
		{"timeline, first page", feedSQL, []any{viewer, 0, 0, 0, pageSize + 1, false, pq.Array(none)}, 1500, ""},
		{"timeline, an older page", feedSQL, []any{viewer, 5000, 0, 0, pageSize + 1, false, pq.Array(none)}, 1500, ""},
		{"timeline as a moderator sees it", feedSQL, []any{viewer, 0, 0, 0, pageSize + 1, true, pq.Array(none)}, 1500, ""},
		{"one member's posts", feedSQL, []any{viewer, 0, 7, 0, pageSize + 1, false, pq.Array(none)}, 1500, ""},
		{"one post", feedSQL, []any{viewer, 0, 0, 4321, 2, false, pq.Array(none)}, 300, ""},
		{"the posts a member bookmarked", feedSQL, []any{viewer, 0, 0, 0, len(mine) + 1, false, pq.Array(mine)}, 1500, ""},
		{"the comments under a page of posts", commentsSQL(feedCommentsSQL), []any{viewer, false, pq.Array(newest), previewComments}, 1500, ""},
		{"the replies under those comments", commentsSQL(repliesSQL), []any{viewer, false, pq.Array(roots), previewReplies}, 1500, ""},
		{"notifications", notificationsSQL, []any{viewer, maxNotifList}, 400, ""},
		{"the unread count of the bell", unreadSQL, []any{viewer}, 400, ""},
		{"bookmarks", bookmarksSQL, []any{viewer}, 200, ""},
		// Local members are read through users_local (migration 15), so the 2,000 remote accounts cost nothing.
		{"people", peopleSQL, []any{viewer, false}, 1500, ""},
		{"dashboard, totals", dashTotalsSQL, nil, 1500, ""},
		{"dashboard, active members", dashActiveSQL, nil, 12000, "sessions progress"},
		{"dashboard, open reports", dashOpenSQL, nil, 800, "reports"},
		{"dashboard, the weeks", dashWeeksSQL, []any{dashWeeks}, 60000, "progress reports"},
	})
	// Blocks (migration 8): the reader has blocked 5,000 accounts, which wrote a quarter of 10,000 newer
	// posts and half of the 20,000 comments under them, and 20,000 other blocks are between other members. Every
	// part of the timeline asks about blocks once per post or comment, through the primary key, so a
	// page reads about what it reads without blocks; the reader's own list is read through the same key.
	for _, q := range []string{
		`INSERT INTO users (username, pw_hash) SELECT 'blk' || g, 'x' FROM generate_series(1, 5000) g`,
		`INSERT INTO blocks (user_id, target_id) SELECT 5, id FROM users WHERE username LIKE 'blk%'`,
		`INSERT INTO blocks (user_id, target_id) SELECT 500 + g, 1 + (g * 7 + k * 991) % 5500 FROM generate_series(1, 5000) g, generate_series(1, 4) k WHERE 1 + (g * 7 + k * 991) % 5500 <> 500 + g ON CONFLICT DO NOTHING`,
		`INSERT INTO posts (user_id, body, created_at) SELECT CASE WHEN g % 4 = 0 THEN 500 + g / 4 ELSE 1 + (g * 7919) % 500 END, 'newer ' || g, now() - (10000 - g) * interval '1 minute' FROM generate_series(1, 10000) g`,
		`INSERT INTO comments (post_id, user_id, body) SELECT 10000 + (g * 31) % 10000 + 1, CASE WHEN g % 2 = 0 THEN 501 + g % 5000 ELSE 1 + (g * 13) % 500 END, 'n' || g FROM generate_series(1, 20000) g`,
		`ANALYZE`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	if n := e.count(`SELECT count(*) FROM blocks WHERE user_id = $1`, viewer); n < 5000 {
		t.Fatalf("the reader has %d blocks, want 5,000 or more", n)
	}
	if err := db.QueryRow(`SELECT array_agg(id) FROM (SELECT id FROM posts p WHERE NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = p.user_id) ORDER BY id DESC LIMIT $2) p`, viewer, pageSize).Scan(pq.Array(&newest)); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT array_agg(id) FROM comments WHERE post_id = ANY($1) AND parent_id IS NULL`, pq.Array(newest)).Scan(pq.Array(&roots)); err != nil {
		t.Fatal(err)
	}
	plans([]planCase{
		{"timeline with 5,000 blocks, first page", feedSQL, []any{viewer, 0, 0, 0, pageSize + 1, false, pq.Array(none)}, 1500, ""},
		{"timeline with 5,000 blocks, an older page", feedSQL, []any{viewer, 15000, 0, 0, pageSize + 1, false, pq.Array(none)}, 1500, ""},
		{"timeline with 5,000 blocks, as a moderator", feedSQL, []any{viewer, 0, 0, 0, pageSize + 1, true, pq.Array(none)}, 1500, ""},
		{"a blocked account's posts", feedSQL, []any{viewer, 0, 600, 0, pageSize + 1, false, pq.Array(none)}, 300, ""},
		{"one post with 5,000 blocks", feedSQL, []any{viewer, 0, 0, newest[0], 2, false, pq.Array(none)}, 300, ""},
		{"comments with 5,000 blocks", commentsSQL(feedCommentsSQL), []any{viewer, false, pq.Array(newest), previewComments}, 1500, ""},
		{"replies with 5,000 blocks", commentsSQL(repliesSQL), []any{viewer, false, pq.Array(roots), previewReplies}, 1500, ""},
		// The list sorts the reader's own blocks to show the latest 500: each of them is read, in the
		// index and then in the table, and only the 500 accounts shown are looked up.
		{"the list of 5,000 blocks, its first 500", blockListSQL, []any{viewer, maxBlockList}, 13000, ""},
	})
	// Follows (migration 9): 1,000 more members wrote 30,000 more posts, the reader follows 500 of them,
	// and 30,000 other follows are between other members. A page of Home reads at most one page of posts
	// per account followed, through posts_user, and no table whole; the lists and the counts read the
	// rows of the member they are about, through the primary key or follows_followee.
	for _, q := range []string{
		`INSERT INTO users (username, pw_hash) SELECT 'fw' || g, 'x' FROM generate_series(1, 1000) g`,
		`INSERT INTO posts (user_id, body, created_at, hidden_at) SELECT u.id, 'followed ' || g, now() - (30000 - g) * interval '20 seconds', CASE WHEN g % 89 = 0 THEN now() END
			FROM generate_series(1, 30000) g JOIN users u ON u.username = 'fw' || (1 + (g * 7919) % 1000)`,
		`INSERT INTO follows (follower_id, followee_id) SELECT 5, id FROM users WHERE username LIKE 'fw%' AND substr(username, 3)::int % 2 = 0`,
		`INSERT INTO follows (follower_id, followee_id) SELECT 500 + g, id FROM generate_series(1, 5000) g CROSS JOIN generate_series(1, 6) k JOIN users ON username = 'fw' || (1 + (g * 13 + k * 337) % 1000) ON CONFLICT DO NOTHING`,
		`ANALYZE`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	if n := e.count(`SELECT count(*) FROM follows WHERE follower_id = $1`, viewer); n != 500 {
		t.Fatalf("the reader follows %d accounts, want 500", n)
	}
	var followed, older int64
	if err := db.QueryRow(`SELECT min(followee_id) FROM follows WHERE follower_id = $1`, viewer).Scan(&followed); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT min(id) FROM (SELECT id FROM posts p WHERE p.user_id IN (SELECT followee_id FROM follows WHERE follower_id = $1) ORDER BY id DESC LIMIT 200) p`, viewer).Scan(&older); err != nil {
		t.Fatal(err)
	}
	// A page of Home reads at most a page of 21 posts per account (501 of them, the reader's own too),
	// through posts_user, whatever the size of posts; each of those rows passes four steps of the plan
	// (the index, its LIMIT, the join over the accounts and the merge), which with the 500 rows of
	// follows and the 21 posts shown is the bound below. It does not grow with the posts table, only
	// with the accounts followed, which maxFollows caps. The list of whom a member follows reads users
	// whole where 500 lookups would cost more, as the planner sees it: a row per member, like people.
	homeMost := float64((500+1)*(pageSize+1)*4 + 8000)
	plans([]planCase{
		{"Home, 500 accounts followed, first page", homeSQL, []any{viewer, 0, pageSize + 1, false}, homeMost, ""},
		{"Home, 500 accounts followed, an older page", homeSQL, []any{viewer, older, pageSize + 1, false}, homeMost, ""},
		{"Home, 500 accounts followed, as a moderator", homeSQL, []any{viewer, 0, pageSize + 1, true}, homeMost, ""},
		{"Home of a member who follows nobody", homeSQL, []any{followed, 0, pageSize + 1, false}, 300, ""},
		{"the 500 accounts a member follows", followingSQL, []any{viewer, maxFollowList}, 13000, "users"},
		{"the followers of a member", followersSQL, []any{followed, maxFollowList}, 600, ""},
		{"the counts on a profile", followCountsSQL, []any{followed}, 600, ""},
		{"the counts of a member who follows 500", followCountsSQL, []any{viewer}, 1500, ""},
		// The public counts of a followers collection (FRD 6): the same rows of follows, and each other side's
		// row of users by its primary key, to ask whether it may be shown.
		{"the public counts of a member", publicFollowCountsSQL, []any{followed, false}, 600, ""},
		{"the public counts of a member who follows 500", publicFollowCountsSQL, []any{viewer, false}, 2000, ""},
	})
	// The public face (migration 14, FRD 5): two members opted in, a third of 600 more posts of one of them
	// Public, some hidden, 70,000 posts in all, 2,000 retired names and 500 keys. Each answer is one statement on an index: the gate on the unique username, the actor's
	// row and key by their keys, a page of Public posts through posts_public, a post through its key.
	var pubPost int64
	for _, q := range []string{
		`UPDATE users SET federates = true WHERE id IN (7, 9)`,
		`INSERT INTO retired_usernames (username) SELECT 'gone' || g FROM generate_series(1, 2000) g`,
		`INSERT INTO ap_keys (user_id, key_id, public_key, private_sealed) SELECT g, '/users/m' || g || '#main-key', 'pem', 'v1.x' FROM generate_series(1, 500) g`,
		`INSERT INTO posts (user_id, body, audience, hidden_at) SELECT 7, 'mine ' || g, CASE WHEN g % 3 = 0 THEN 'public' ELSE 'local' END, CASE WHEN g % 41 = 0 THEN now() END FROM generate_series(1, 600) g`,
		`ANALYZE`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	if err := db.QueryRow(`SELECT max(id) FROM posts WHERE user_id = 7 AND audience = 'public' AND hidden_at IS NULL`).Scan(&pubPost); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM posts WHERE user_id = 7 AND audience = 'public'`) < pageSize {
		t.Fatal("too few Public posts to page")
	}
	plans([]planCase{
		{"public: the gate of an actor", actorGateSQL, []any{"m7", false}, 10, ""},
		{"public: the gate of a name nobody has", actorGateSQL, []any{"nobody", false}, 10, ""},
		{"public: an actor document", actorDocSQL, []any{7}, 10, ""},
		{"public: a profile page", profilePageSQL, []any{"m7", false, 0, pubPage + 1}, 150, ""},
		{"public: a profile page, older posts", profilePageSQL, []any{"m7", false, pubPost, pubPage + 1}, 150, ""},
		{"public: the page of a member not shown", profilePageSQL, []any{"m8", false, 0, pubPage + 1}, 10, ""},
		{"public: a post", publicPostSQL, []any{"m7", false, pubPost}, 10, ""},
		{"public: a post's picture", postPictureSQL, []any{"m7", false, pubPost}, 10, ""},
		{"public: a profile picture", avatarSQL, []any{"m7", false}, 10, ""},
	})
	// The outbox and the collections (FRD 6). A page of the outbox is the profile page's statement, held
	// above. Its count reads posts_public for the one member, so it reads the member's Public posts and none
	// of the 400 others they wrote or anyone else's 70,000: a bound of their Public posts and a few rows for
	// the gate. The collections' gate is the actor's, and their counts followCountsSQL, both held above.
	pubCount := e.count(`SELECT count(*) FROM posts WHERE user_id = 7 AND audience = 'public' AND hidden_at IS NULL`)
	plans([]planCase{
		{"public: an outbox and its count", outboxSQL, []any{"m7", false}, float64(pubCount) + 10, ""},
		{"public: the outbox of a member not shown", outboxSQL, []any{"m8", false}, 10, ""},
		{"public: the outbox of a name nobody has", outboxSQL, []any{"nobody", false}, 10, ""},
	})
	// Remote actors (FRD 7), the 2,000 inserted at the top among 8,500 members. A lookup by id or by key id is
	// one row through its index. The members who opted in and have no key are read once at a start, with
	// users and ap_keys whole, as before. The removal of the least recently fetched (asked in a transaction that
	// is rolled back, since EXPLAIN ANALYZE runs it) walks ap_actors_fetched oldest first and asks each
	// candidate's posts, likes, follows and blocks through their indexes, one at a time: about six rows for
	// each account it removes, whatever the size of those tables.
	var someID, someKey string
	if err := db.QueryRow(`SELECT ap_id, key_id FROM ap_actors ORDER BY user_id LIMIT 1 OFFSET 777`).Scan(&someID, &someKey); err != nil {
		t.Fatal(err)
	}
	plans([]planCase{
		{"remote: an actor by its id", actorByIDSQL, []any{someID}, 10, ""},
		{"remote: an id nobody has", actorByIDSQL, []any{"https://nobody.example/users/x"}, 10, ""},
		{"remote: an actor by its key id", actorByKeySQL, []any{someKey}, 10, ""},
		{"remote: a key id nobody has", actorByKeySQL, []any{"https://nobody.example/users/x#main-key"}, 10, ""},
		{"keys: members who opted in and have none", missingKeysSQL, []any{keyQueue - 2}, 20000, "users ap_keys"},
	})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	on = tx
	plans([]planCase{{"remote: removing the 200 fetched longest ago", evictActorsSQL, []any{200}, 3000, ""}})
	on = db
	tx.Rollback()
	// The indexes migration 6 adds are the ones those plans stand on: without one of them a plan above fails.
	for _, idx := range []string{"likes_user", "comment_likes_user", "posts_created", "comments_created"} {
		if e.count(`SELECT count(*) FROM pg_indexes WHERE schemaname = current_schema() AND indexname = $1`, idx) != 1 {
			t.Errorf("the index %s is missing", idx)
		}
	}
}

// The fit workflow reads the shared counts with a token and no account (FLOWA_COUNTS_TOKEN): every
// prop's pairs, to the one who has the token, and to nobody when there is no token or the wrong one.
func TestDBCountsToken(t *testing.T) {
	e := newEnv(t)
	_, member := e.user("bea", "user")
	if _, err := e.a.db.Exec(`INSERT INTO transition_counts (prop, a, b, works, bridge, blocked) VALUES
		('nunchaku', 'F5', 'F7', 3, 0, 1), ('poi', 'A1', 'A2', 0, 2, 0), ('staff', 'S1', 'S2', 0, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef"
	ask := func(bearer string) (int, map[string]any) {
		hdr := map[string]string{}
		if bearer != "" {
			hdr["Authorization"] = bearer
		}
		code, out, _ := e.doH("", "GET", "/api/suggest/counts", nil, hdr)
		return code, out
	}
	// No token is set: nothing opens the counts but a session, and a made-up token is turned away.
	if code, _ := ask("Bearer " + token); code != 401 {
		t.Fatalf("with no token set, a token got %d, want 401", code)
	}
	if code, _ := ask("Bearer "); code != 401 {
		t.Fatalf("with no token set, an empty token got %d, want 401: an unset token must not equal an empty one", code)
	}
	if code, _ := ask(""); code != 401 {
		t.Fatalf("without a session or a token: %d, want 401", code)
	}
	e.a.countsTok = token
	code, out := ask("Bearer " + token)
	counts, _ := out["counts"].([]any)
	if code != 200 || len(counts) != 2 {
		t.Fatalf("with the token: %d and %v, want 200 and the two pairs anyone marked, of every prop", code, out["_raw"])
	}
	if first := counts[0].(map[string]any); first["prop"] != "nunchaku" || num(first["works"]) != 3 || num(first["blocked"]) != 1 {
		t.Fatalf("the first pair: %v", first)
	}
	for name, bearer := range map[string]string{
		"a wrong token":               "Bearer " + strings.Repeat("x", len(token)),
		"the token with one more":     "Bearer " + token + "0",
		"the start of the token":      "Bearer " + token[:32],
		"an empty token":              "Bearer ",
		"the token under another way": "Basic " + token,
	} {
		if code, _ := ask(bearer); code != 401 {
			t.Errorf("%s got %d, want 401", name, code)
		}
	}
	// The token opens this one answer and nothing else, and a member's session works as before.
	if code, _, _ := e.doH("", "GET", "/api/admin/dashboard", nil, map[string]string{"Authorization": "Bearer " + token}); code != 401 {
		t.Errorf("the token on the dashboard: %d, want 401", code)
	}
	if code, _, _ := e.doH("", "POST", "/api/suggest/labels", map[string]any{"prop": "nunchaku", "deltas": []any{}}, map[string]string{"Authorization": "Bearer " + token}); code != 401 {
		t.Errorf("the token on a write: %d, want 401", code)
	}
	if got := e.must(200, member, "GET", "/api/suggest/counts?prop=nunchaku", nil)["counts"].([]any); len(got) != 1 {
		t.Errorf("a member's own read: %v", got)
	}
	// With suggestions switched off there is nothing to read, token or not.
	e.setFlag("suggest", false)
	if code, _ := ask("Bearer " + token); code != 404 {
		t.Errorf("with the suggest flag off: %d, want 404", code)
	}
}

// poiBranch is the letter of a poi branch with a skill in use, read from the published map.
func poiBranch(t *testing.T, e *tenv) string {
	t.Helper()
	_, cat := e.do("", "GET", "/catalog/poi.json", nil)
	for _, s := range cat["skills"].([]any) {
		if m := s.(map[string]any); m["retired"] != true {
			return m["id"].(string)[:1]
		}
	}
	t.Fatal("the poi map has no skill in use")
	return ""
}

// noDial is a transport that opens no connection and fails the test when asked to.
// countingDB counts the reads made through it, for a check that a step reads the database once.
type countingDB struct {
	db *sql.DB
	n  int
}

func (c *countingDB) QueryRow(q string, args ...any) *sql.Row {
	c.n++
	return c.db.QueryRow(q, args...)
}

type noDial struct{ t *testing.T }

func (n noDial) RoundTrip(r *http.Request) (*http.Response, error) {
	n.t.Errorf("an outbound request to %s", r.URL.Host)
	return nil, errors.New("no outbound request is expected")
}

// routesFile reads a list of mux patterns: one a line, with # for comments.
func routesFile(t *testing.T, name string) []string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

// A server that is given no setting is a local instance, and it is what it was before instance modes
// existed: the same routes (testdata/routes-local.txt), no HTTP connection opened to another machine
// by the start, the housekeeping or the first requests, and an ordinary "not found" at every address
// federation will use. A setting that cannot be run stops the start before a row is written.
//
// What it cannot see: code that dials with net.Dial or a transport of its own. The server has one
// dialer, the fetcher's, and federation is planned to keep it so (docs/federation.md, 7.8).
func TestDBLocalModeUnchanged(t *testing.T) {
	e := newEnv(t)
	// Anything that goes around the fetcher uses the default transport: make that fail the test.
	was := http.DefaultTransport
	http.DefaultTransport = noDial{t}
	t.Cleanup(func() { http.DefaultTransport = was })

	none := func(string) string { return "" }
	if err := e.a.start(none); err != nil {
		t.Fatal(err)
	}
	if e.a.mode != modeLocal {
		t.Fatalf("mode %q with nothing set, want local", e.a.mode)
	}
	if e.a.fetch == nil || e.a.fetch.off || e.a.fetch.base != "" {
		t.Fatalf("the link preview fetcher is not as it was: %+v", e.a.fetch)
	}

	// The routes, as a set: what is new and what is gone are both failures.
	st := staticOf(t, map[string]string{"index.html": "<!doctype html><title>Flowa</title>"})
	got := slices.Clone(e.a.table(st).patterns)
	want := routesFile(t, "testdata/routes-local.txt")
	sort.Strings(got)
	if !sort.StringsAreSorted(want) {
		t.Error("testdata/routes-local.txt is not sorted")
	}
	for _, p := range got {
		if _, ok := slices.BinarySearch(want, p); !ok {
			t.Errorf("a route local mode did not have: %q (if every instance should have it, add it to testdata/routes-local.txt)", p)
		}
	}
	for _, p := range want {
		if _, ok := slices.BinarySearch(got, p); !ok {
			t.Errorf("a route local mode had is gone: %q", p)
		}
	}
	if len(got) != len(want) {
		t.Errorf("%d routes, the list has %d", len(got), len(want))
	}

	// What federation will answer is not there: each address answers as an address nobody has.
	e.h = e.a.routes(st)
	ask := func(method, path, accept, ctype string) (int, string) {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		if accept != "" {
			r.Header.Set("Accept", accept)
		}
		if ctype != "" {
			r.Header.Set("Content-Type", ctype)
		}
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	const ap = `application/activity+json`
	const ld = `application/ld+json; profile="https://www.w3.org/ns/activitystreams"`
	for _, accept := range []string{"", ap, ld, "application/jrd+json"} {
		code0, body0 := ask("GET", "/nobody-has-this-address", accept, "")
		if code0 != http.StatusNotFound {
			t.Fatalf("an unknown address answers %d, want 404", code0)
		}
		// /api/about too: the visitor's about page exists only once an admin has saved a description.
		for _, p := range []string{"/api/about", "/.well-known/webfinger?resource=acct:ana@example.com", "/.well-known/nodeinfo", "/.well-known/host-meta", "/nodeinfo/2.0", "/nodeinfo/2.1", "/actor", "/inbox", "/users/ana", "/users/ana/inbox", "/users/ana/outbox", "/users/ana/followers", "/users/ana/following", "/@ana", "/api/v1/instance", "/api/v2/instance"} {
			if code, body := ask("GET", p, accept, ""); code != code0 || body != body0 {
				t.Errorf("GET %s (Accept %q) answers %d %q, an unknown address answers %d %q", p, accept, code, body, code0, body0)
			}
		}
	}
	// Only the JSON row reaches the mux: guard answers 415 to the other two types at any address
	// today, which is itself what an inbox would have to change, and would show here.
	for _, ctype := range []string{ap, ld, "application/json"} {
		code0, body0 := ask("POST", "/nobody-has-this-address", "", ctype)
		if code0 < 400 {
			t.Fatalf("a POST to an unknown address answers %d", code0)
		}
		for _, p := range []string{"/api/about", "/inbox", "/actor/inbox", "/users/ana/inbox", "/users/ana/outbox"} {
			if code, body := ask("POST", p, "", ctype); code != code0 || body != body0 {
				t.Errorf("POST %s (%s) answers %d %q, an unknown address answers %d %q", p, ctype, code, body, code0, body0)
			}
		}
	}

	// The pages, the health answer and an hour's housekeeping, with a member signed in.
	_, admin := e.user("root", "admin")
	for _, p := range []string{"/", "/healthz", "/api/me", "/api/admin/dashboard", "/api/posts"} {
		r := httptest.NewRequest("GET", p, nil)
		r.AddCookie(&http.Cookie{Name: cookieName, Value: admin})
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s answers %d", p, w.Code)
		}
	}
	if _, err := e.a.housekeep(); err != nil {
		t.Fatal(err)
	}
	if n := e.a.fetch.dials.Load(); n != 0 {
		t.Errorf("%d connections were tried by a start, an hour's housekeeping and five requests; want none", n)
	}

	// Nothing about the instance has been saved, so /api/me says nothing new to a visitor or a member,
	// and the signed-in about page and the editor answer with nothing in them.
	for _, tok := range []string{"", admin} {
		if _, out := e.do(tok, "GET", "/api/me", nil); out["instance"] != nil {
			t.Errorf("/api/me (%q) with nothing saved has instance: %v", tok, out["instance"])
		}
	}
	if code, out := e.do(admin, "GET", "/api/instance", nil); code != 200 || out["description"] != "" {
		t.Errorf("GET /api/instance with nothing saved: %d %v", code, out["_raw"])
	}
	if code, out := e.do(admin, "GET", "/api/admin/instance", nil); code != 200 || out["updatedAt"] != "" || out["registration"] != "open" {
		t.Errorf("GET /api/admin/instance with nothing saved: %d %v", code, out["_raw"])
	}
	// Sign-up by approval was never chosen: no request waits, and the dashboard has no count of them.
	if code, out := e.do(admin, "GET", "/api/admin/requests", nil); code != 200 || len(out["requests"].([]any)) != 0 {
		t.Errorf("GET /api/admin/requests with nothing saved: %d %v", code, out["_raw"])
	}
	if _, out := e.do(admin, "GET", "/api/admin/dashboard", nil); out["totals"].(map[string]any)["waiting"] != nil {
		t.Errorf("the dashboard with nothing saved counts requests to join: %v", out["totals"])
	}

	// The dashboard says which mode runs.
	if code, out := e.do(admin, "GET", "/api/admin/dashboard", nil); code != 200 || out["mode"] != "local" {
		t.Errorf("dashboard: %d, mode %v; want local", code, out["mode"])
	}

	// A mode that cannot run, or a word that is none, stops the start before the admin account is
	// made: the database is as it was.
	for _, v := range []string{"single", "federated", "public"} {
		if modesBuilt[instanceMode(v)] {
			continue
		}
		f := newEnv(t)
		env := map[string]string{"FLOWA_MODE": v, "ADMIN_USER": "owner", "ADMIN_PASSWORD": "flowa-test-only"}
		if err := f.a.start(func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), "FLOWA_MODE") {
			t.Errorf("FLOWA_MODE=%s: start gave %v, want an error naming the setting", v, err)
		}
		var n int
		if err := f.a.db.QueryRow(`SELECT count(*) FROM users`).Scan(&n); err != nil || n != 0 {
			t.Errorf("FLOWA_MODE=%s: %d accounts after a refused start (%v), want none", v, n, err)
		}
		if f.a.fetch != nil {
			t.Errorf("FLOWA_MODE=%s: a fetcher was made by a refused start", v)
		}
	}

	// The settings a deployment already has still start, with the mode said in any case.
	g := newEnv(t)
	// The settings federation will read are nothing to a local instance: no name is added to the
	// ones it answers to.
	env := map[string]string{"FLOWA_MODE": " Local ", "ADMIN_USER": "owner", "ADMIN_PASSWORD": "flowa-test-only", "FLOWA_HOSTS": "flowa.example.org", "FLOWA_PREVIEW_FETCH": "off",
		"FLOWA_ORIGIN": "https://public.example.net", "FLOWA_KEY_SECRET": "flowa-test-only", "FLOWA_FEDERATE": "on"}
	g.a.hosts = nil
	if err := g.a.start(func(k string) string { return env[k] }); err != nil || g.a.mode != modeLocal || !g.a.fetch.off {
		t.Errorf("a start with the settings of a deployment: %v, mode %q", err, g.a.mode)
	}
	if !reflect.DeepEqual(g.a.hosts, []string{"flowa.example.org"}) {
		t.Errorf("the names answered to are %v, want only the one FLOWA_HOSTS gives", g.a.hosts)
	}
	g.h = g.a.routes(staticOf(t, nil))
	for host, want := range map[string]int{"public.example.net": http.StatusMisdirectedRequest, "flowa.example.org": http.StatusOK} {
		r := httptest.NewRequest("GET", "/api/me", nil)
		r.Host = host
		w := httptest.NewRecorder()
		g.h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("a request for %s answers %d in local mode, want %d", host, w.Code, want)
		}
	}
	var owners int
	if err := g.a.db.QueryRow(`SELECT count(*) FROM users WHERE username = 'owner' AND role = 'admin'`).Scan(&owners); err != nil || owners != 1 {
		t.Errorf("the admin account of a first start: %d (%v), want 1", owners, err)
	}
}

// Instance information: nothing saved answers as before; an admin with flags.edit saves the whole
// document against the version read, every part is plain text within its limits, every save is
// audited, and the visitor's about page opens only while a description is saved.
func TestDBInstanceInfo(t *testing.T) {
	e := newEnv(t)
	adminID, admin := e.user("boss", "admin")
	deputyID, deputy := e.user("deputy", "admin")
	_, mod := e.user("mo", "moderator")
	_, ana := e.user("ana", "user")
	raw := func(path, remote string) (int, string, http.Header) {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		if remote != "" {
			r.RemoteAddr = remote
		}
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		return w.Code, w.Body.String(), w.Result().Header
	}
	code0, body0, hdr0 := raw("/nobody-has-this-address", "")
	unknown := func(what string) {
		t.Helper()
		code, body, hdr := raw("/api/about", "")
		if code != code0 || body != body0 || hdr.Get("Content-Type") != hdr0.Get("Content-Type") || hdr.Get("Content-Security-Policy") != hdr0.Get("Content-Security-Policy") {
			t.Errorf("%s: the visitor's about answers %d %q (%s), an unknown address %d %q (%s)", what, code, body, hdr.Get("Content-Type"), code0, body0, hdr0.Get("Content-Type"))
		}
	}
	type doc = map[string]any
	put := func(tok string, d doc) (int, map[string]any) {
		t.Helper()
		return e.do(tok, "PUT", "/api/admin/instance", d)
	}
	rules := func(texts ...string) []map[string]string {
		out := []map[string]string{}
		for _, s := range texts {
			out = append(out, map[string]string{"text": s, "hint": ""})
		}
		return out
	}
	full := func(desc, contact string, r []map[string]string, ver string) doc {
		return doc{"description": desc, "contact": contact, "rules": r, "updatedAt": ver}
	}

	// Nothing saved: the visitor's about is an unknown address, /api/me says nothing new, a member's
	// about page is empty, and sign-up works as before.
	unknown("nothing saved")
	for _, tok := range []string{"", ana} {
		if _, out := e.do(tok, "GET", "/api/me", nil); out["instance"] != nil {
			t.Errorf("with nothing saved /api/me (%q) gained %v", tok, out["instance"])
		}
	}
	if out := e.must(200, ana, "GET", "/api/instance", nil); out["description"] != "" || out["contact"] != "" || len(out["rules"].([]any)) != 0 {
		t.Errorf("a member's about with nothing saved: %v", out["_raw"])
	}
	if out := e.must(200, admin, "GET", "/api/admin/instance", nil); out["updatedAt"] != "" || out["updatedBy"] != nil || num(out["limits"].(map[string]any)["description"]) != 500 {
		t.Errorf("the editor with nothing saved: %v", out["_raw"])
	}
	if code, out := e.do("", "POST", "/api/signup", map[string]string{"username": "newbie", "password": "a fine long password"}); code != 200 || len(out) != 2 || out["user"] == nil {
		t.Errorf("sign-up with nothing saved: %d %v", code, out["_raw"])
	}
	// A flood of reads on an instance that has saved nothing costs no query each: the empty answer is
	// served from memory. The table is moved out of the way, so a read that asked the database would
	// fail with 500; the reads above filled the cache moments ago, well inside its ten seconds.
	if _, err := e.a.db.Exec(`ALTER TABLE instance_settings RENAME TO instance_settings_away`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if code, body, _ := raw("/api/about", ""); code != code0 || body != body0 {
			t.Fatalf("read %d of the visitor's about with the table away: %d %q", i+1, code, body)
		}
		if code, out := e.do("", "GET", "/api/me", nil); code != 200 || out["instance"] != nil {
			t.Fatalf("read %d of /api/me with the table away: %d %v", i+1, code, out["_raw"])
		}
	}
	if _, err := e.a.db.Exec(`ALTER TABLE instance_settings_away RENAME TO instance_settings`); err != nil {
		t.Fatal(err)
	}

	// Only an admin with flags.edit reads the editor or saves.
	empty := full("A description", "", rules(), "")
	for _, tok := range []string{ana, mod} {
		if code, _ := put(tok, empty); code != 403 {
			t.Errorf("a save by a member or a moderator: %d, want 403", code)
		}
		if code, _ := e.do(tok, "GET", "/api/admin/instance", nil); code != 403 {
			t.Errorf("the editor read by a member or a moderator: %d, want 403", code)
		}
	}
	if code, _ := put("", empty); code != 401 {
		t.Errorf("a save by a visitor: %d, want 401", code)
	}

	// The limits: each refused with the field it is about, and nothing written.
	long := func(n int) string { return strings.Repeat("é", n) }
	many := make([]string, 21)
	for i := range many {
		many[i] = fmt.Sprintf("Rule %d", i+1)
	}
	for _, c := range []struct {
		name  string
		d     doc
		field string
	}{
		{"501 characters of description", full(long(501), "", rules(), ""), "description"},
		{"201 characters of contact", full("", long(201), rules(), ""), "contact"},
		{"21 rules", full("", "", rules(many...), ""), "rules"},
		{"an empty rule", full("", "", rules("Be kind", "   "), ""), "rules.1.text"},
		{"201 characters in a rule", full("", "", rules(long(201)), ""), "rules.0.text"},
		{"1,001 characters of explanation", full("", "", []map[string]string{{"text": "Be kind", "hint": long(1001)}}, ""), "rules.0.hint"},
		{"a NUL in the description", full("Hello\x00", "", rules(), ""), "description"},
		{"an escape in a rule", full("", "", rules("Be \x1b[31mkind"), ""), "rules.0.text"},
		{"a bell in the contact", full("", "ring\x07", rules(), ""), "contact"},
		{"a line break in a rule", full("", "", rules("Be kind\nalways"), ""), "rules.0.text"},
		{"a line break in the contact", full("", "a\nb", rules(), ""), "contact"},
		{"a direction override", full("\u202eadmin", "", rules(), ""), "description"},
		{"a javascript: contact", full("", "javascript:alert(1)", rules(), ""), "contact"},
		{"a JavaScript: contact", full("", "JavaScript:alert(document.cookie)", rules(), ""), "contact"},
		{"a data: contact", full("", "data:text/html,<script>alert(1)</script>", rules(), ""), "contact"},
		{"a javascript: link with a host", full("", "javascript://example.org/%0Aalert(1)", rules(), ""), "contact"},
		{"a mailto: contact", full("", "mailto:admins@example.org", rules(), ""), "contact"},
		{"a link with a name in it", full("", "https://bank.example@evil.example/", rules(), ""), "contact"},
		{"a link with a space", full("", "https://example.org/a b", rules(), ""), "contact"},
		{"half a document", doc{"description": "Only this", "updatedAt": ""}, ""},
	} {
		code, out := put(admin, c.d)
		if code != 400 || out["field"] != c.field {
			t.Errorf("%s: %d, field %v, want 400 and %q: %v", c.name, code, out["field"], c.field, out["_raw"])
		}
	}
	if n := e.count(`SELECT count(*) FROM instance_settings`); n != 0 {
		t.Fatalf("%d rows after refused saves, want none", n)
	}
	unknown("after refused saves")

	// A save: HTML is kept as text and given back as text, line breaks are kept in the description
	// and the explanations (at most one empty line in a row), and the ends are trimmed.
	desc := "<b>Welcome</b> & <script>alert(1)</script>\r\n\r\n\r\n\r\nSecond paragraph.  "
	hint := `No <a href="javascript:alert(1)">links</a>` + "\n\nThat is all."
	first := doc{"description": desc, "contact": "  <admins@example.org>  ", "updatedAt": "",
		"rules": []map[string]string{{"text": " <i>Be kind</i> ", "hint": hint}, {"text": "Spin away from people", "hint": ""}}}
	if code, out := put(admin, first); code != 200 || out["contactKind"] != "text" {
		t.Fatalf("a contact in angle brackets is kept as text: %d %v", code, out["_raw"])
	}
	v0, _ := e.must(200, admin, "GET", "/api/admin/instance", nil)["updatedAt"].(string)
	first["contact"], first["updatedAt"] = "  admins@example.org  ", v0
	out := e.must(200, admin, "PUT", "/api/admin/instance", first)
	wantDesc := "<b>Welcome</b> & <script>alert(1)</script>\n\nSecond paragraph."
	v1, _ := out["updatedAt"].(string)
	if out["description"] != wantDesc || out["contact"] != "admins@example.org" || out["contactKind"] != "email" || out["updatedBy"] != "boss" || v1 == "" || v1 == v0 {
		t.Fatalf("after a save: %v", out["_raw"])
	}
	if r := out["rules"].([]any); len(r) != 2 || r[0].(map[string]any)["text"] != "<i>Be kind</i>" || r[0].(map[string]any)["hint"] != hint || r[1].(map[string]any)["text"] != "Spin away from people" {
		t.Errorf("the rules after a save: %v", out["rules"])
	}
	// The audit rows: who, which parts, how many rules; not the text.
	var actor int64
	var detail string
	if err := e.a.db.QueryRow(`SELECT actor_id, detail::text FROM mod_actions WHERE action = 'instance' ORDER BY id LIMIT 1`).Scan(&actor, &detail); err != nil || actor != adminID ||
		detail != `{"parts": ["description", "contact", "rules"], "rules": 2}` {
		t.Errorf("the audit row of the first save: %d %s (%v)", actor, detail, err)
	}
	if err := e.a.db.QueryRow(`SELECT detail::text FROM mod_actions WHERE action = 'instance' ORDER BY id DESC LIMIT 1`).Scan(&detail); err != nil || detail != `{"parts": ["contact"], "rules": 2}` {
		t.Errorf("the audit row of the second save: %s (%v)", detail, err)
	}
	// The visitor's about answers at once (the cache was dropped), with the text as typed, as JSON.
	code, body, hdr := raw("/api/about", "")
	var got instanceInfo
	if json.Unmarshal([]byte(body), &got); code != 200 || got.Description != wantDesc || got.Rules[0].Text != "<i>Be kind</i>" || hdr.Get("Content-Type") != "application/json" || strings.Contains(body, "<script>") {
		t.Errorf("the visitor's about after a save: %d %s %q", code, hdr.Get("Content-Type"), body)
	}
	if _, me := e.do("", "GET", "/api/me", nil); me["instance"] == nil || me["instance"].(map[string]any)["about"] != true || len(me["instance"].(map[string]any)["rules"].([]any)) != 2 {
		t.Errorf("a visitor's /api/me after a save: %v", me["instance"])
	}
	if _, me := e.do(ana, "GET", "/api/me", nil); fmt.Sprint(me["instance"]) != "map[about:true]" {
		t.Errorf("a member's /api/me after a save: %v", me["instance"])
	}
	if out := e.must(200, ana, "GET", "/api/instance", nil); out["description"] != wantDesc {
		t.Errorf("a member's about after a save: %v", out["_raw"])
	}

	// A save made against an older version is refused with the state as it is, and changes nothing.
	audits := e.count(`SELECT count(*) FROM mod_actions WHERE action = 'instance'`)
	code, out = put(deputy, full("Mine instead", "", rules(), v0))
	if code != 409 || out["updatedAt"] != v1 || out["description"] != wantDesc || out["error"] == nil {
		t.Errorf("a stale save: %d %v", code, out["_raw"])
	}
	if e.count(`SELECT count(*) FROM mod_actions WHERE action = 'instance'`) != audits {
		t.Error("a stale save was audited")
	}
	// A save of one part against the version read: only that part is written and logged.
	out = e.must(200, deputy, "PUT", "/api/admin/instance", doc{"description": wantDesc, "contact": "https://forum.example.org/flowa", "rules": out["rules"], "updatedAt": v1})
	v2, _ := out["updatedAt"].(string)
	if v2 == v1 || out["contactKind"] != "link" || out["updatedBy"] != "deputy" {
		t.Errorf("after the deputy's save: %v", out["_raw"])
	}
	if err := e.a.db.QueryRow(`SELECT detail::text FROM mod_actions WHERE action = 'instance' ORDER BY id DESC LIMIT 1`).Scan(&detail); err != nil || detail != `{"parts": ["contact"], "rules": 2}` {
		t.Errorf("the audit row of a one-part save: %s (%v)", detail, err)
	}
	if code, _ := put(admin, full(wantDesc, "", rules("Be kind"), v1)); code != 409 {
		t.Errorf("the first admin's save against the version before the deputy's: %d, want 409", code)
	}

	// The description cleared: the visitor's about is an unknown address again at once, while the rules
	// still show at sign-up and members keep their about page.
	out = e.must(200, admin, "PUT", "/api/admin/instance", full("", "https://forum.example.org/flowa", rules("Be kind", "Spin away from people"), v2))
	v3, _ := out["updatedAt"].(string)
	unknown("the description cleared")
	if _, me := e.do("", "GET", "/api/me", nil); fmt.Sprint(me["instance"]) != "map[about:false rules:[map[hint: text:Be kind] map[hint: text:Spin away from people]]]" {
		t.Errorf("a visitor's /api/me with rules and no description: %v", me["instance"])
	}
	if code, _ := e.do(ana, "GET", "/api/instance", nil); code != 200 {
		t.Errorf("a member's about with no description: %d", code)
	}

	// The visitor's limit, by address, once there is something to read.
	out = e.must(200, admin, "PUT", "/api/admin/instance", full("Back again.", "", rules(), v3))
	for i := 1; i <= 121; i++ {
		code, _, _ := raw("/api/about", "203.0.113.7:4000")
		if want := map[bool]int{true: 200, false: 429}[i <= 120]; code != want {
			t.Fatalf("read %d of the about page from one address: %d, want %d", i, code, want)
		}
	}
	if code, _, _ := raw("/api/about", "203.0.113.8:4000"); code != 200 {
		t.Errorf("another address after one ran out: %d", code)
	}

	// The account that saved is deleted: the rows stay, with nobody named, and nothing breaks.
	v4, _ := out["updatedAt"].(string)
	e.must(200, deputy, "PUT", "/api/admin/instance", full("Back again.", "the desk at the gym", rules(), v4))
	// Admins cannot delete each other, so the deputy is made a member first, as an owner would.
	if _, err := e.a.db.Exec(`UPDATE users SET role = 'user' WHERE id = $1`, deputyID); err != nil {
		t.Fatal(err)
	}
	e.must(200, admin, "DELETE", fmt.Sprintf("/api/admin/users/%d", deputyID), nil)
	if n := e.count(`SELECT count(*) FROM instance_settings WHERE updated_by IS NULL`); n != 1 {
		t.Errorf("%d rows with nobody named after the deleted account, want 1 (the contact)", n)
	}
	if out := e.must(200, admin, "GET", "/api/admin/instance", nil); out["updatedBy"] != nil || out["contact"] != "the desk at the gym" || out["contactKind"] != "text" {
		t.Errorf("the editor after the account that saved last was deleted: %v", out["_raw"])
	}
	if code, _, _ := raw("/api/about", "203.0.113.9:4000"); code != 200 {
		t.Errorf("the visitor's about after the account that saved was deleted: %d", code)
	}

	// A whole document at its limits, in characters of 3 bytes and of 4 bytes in UTF-8, saves and reads
	// back the same: the limit on the request's size is in bytes, and must not be met first.
	for _, ch := range []string{"漢", "🌀"} {
		of := func(n int) string { return strings.Repeat(ch, n) }
		rs := make([]map[string]string, maxInstanceRules)
		for i := range rs {
			rs[i] = map[string]string{"text": of(maxRuleText), "hint": of(maxRuleHint)}
		}
		v, _ := e.must(200, admin, "GET", "/api/admin/instance", nil)["updatedAt"].(string)
		d := doc{"description": of(maxInstanceDescription), "contact": of(maxInstanceContact), "rules": rs, "updatedAt": v}
		if b, _ := json.Marshal(d); len(b) <= 64<<10 {
			t.Errorf("the document in %q is %d bytes, which does not test the size", ch, len(b))
		}
		if code, out := put(admin, d); code != 200 {
			t.Errorf("a whole document at its limits in %q: %d %v", ch, code, out["error"])
			continue
		}
		out := e.must(200, ana, "GET", "/api/instance", nil)
		got, _ := out["rules"].([]any)
		same := out["description"] == of(maxInstanceDescription) && out["contact"] == of(maxInstanceContact) && len(got) == maxInstanceRules
		for _, r := range got {
			m, _ := r.(map[string]any)
			same = same && m["text"] == of(maxRuleText) && m["hint"] == of(maxRuleHint)
		}
		if !same {
			t.Errorf("the document in %q does not read back the same", ch)
		}
	}

	// Everything cleared: the instance answers as one that never saved anything.
	v5, _ := e.must(200, admin, "GET", "/api/admin/instance", nil)["updatedAt"].(string)
	e.must(200, admin, "PUT", "/api/admin/instance", full("", "", rules(), v5))
	unknown("everything cleared")
	if _, me := e.do("", "GET", "/api/me", nil); me["instance"] != nil {
		t.Errorf("a visitor's /api/me with everything cleared: %v", me["instance"])
	}
}

// Sign-up by approval: with nothing saved sign-up is as before; with approval on a sign-up is a request
// that waits, is no account anywhere, cannot sign in, and is approved or declined by an admin with
// user.manage, once, audited; the waiting cap, housekeeping, the setting switched back, the flag off
// and the operator's account all behave as the README says.
func TestDBRegistrationApproval(t *testing.T) {
	e := newEnv(t)
	logs := logged(t)
	e.setFlag("follows", true)
	adminID, admin := e.user("boss", "admin")
	_, deputy := e.user("deputy", "admin")
	_, mod := e.user("mo", "moderator")
	_, ana := e.user("ana", "user")
	if _, err := e.a.db.Exec(`UPDATE users SET owner = true WHERE id = $1`, adminID); err != nil {
		t.Fatal(err)
	}
	const pw = "a fine long password"
	// Each sign-up and sign-in comes from an address of its own, so the allowances (TestDBSignupLimits,
	// TestDBSigninLimits) are never what answers here.
	// sendFrom sends from one given address, for the checks of the allowance itself.
	var sent atomic.Int32 // some are sent at once, from goroutines
	sendFrom := func(remote, path string, body map[string]any) (int, map[string]any, http.Header) {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", path, bytes.NewReader(b))
		r.RemoteAddr = remote
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		out := map[string]any{}
		json.Unmarshal(w.Body.Bytes(), &out)
		out["_raw"] = w.Body.String()
		return w.Code, out, w.Result().Header
	}
	send := func(path string, body map[string]any) (int, map[string]any, http.Header) {
		t.Helper()
		addr := int(sent.Add(1))
		return sendFrom(fmt.Sprintf("198.51.%d.%d:4000", 100+addr/250, addr%250+1), path, body)
	}
	signup := func(body map[string]any) (int, map[string]any, http.Header) {
		t.Helper()
		return send("/api/signup", body)
	}
	signin := func(name, pass string) (int, string, http.Header) {
		t.Helper()
		code, out, hdr := send("/api/signin", map[string]any{"username": name, "password": pass})
		return code, out["_raw"].(string), hdr
	}
	hasCookie := func(h http.Header) bool {
		for _, c := range (&http.Response{Header: h}).Cookies() {
			if c.Name == cookieName && c.Value != "" {
				return true
			}
		}
		return false
	}
	version := func() string {
		t.Helper()
		v, _ := e.must(200, admin, "GET", "/api/admin/instance", nil)["updatedAt"].(string)
		return v
	}
	setReg := func(tok, reg string) (int, map[string]any) {
		t.Helper()
		cur := e.must(200, admin, "GET", "/api/admin/instance", nil)
		return e.do(tok, "PUT", "/api/admin/instance", map[string]any{"description": cur["description"], "contact": cur["contact"], "rules": cur["rules"], "registration": reg, "updatedAt": cur["updatedAt"]})
	}
	waitingCount := func() int { return e.count(`SELECT count(*) FROM join_requests`) }
	totals := func() map[string]any {
		t.Helper()
		return e.must(200, admin, "GET", "/api/admin/dashboard", nil)["totals"].(map[string]any)
	}

	// Nothing saved: sign-up is as before, an account and a session at once, the answer shaped as
	// before; the editor says open; nothing new shows to a visitor or on the dashboard.
	code, out, hdr := signup(map[string]any{"username": "plain", "password": pw, "reason": "ignored while sign-up is open"})
	if code != 200 || len(out) != 2 || out["user"] == nil || !hasCookie(hdr) || waitingCount() != 0 {
		t.Fatalf("sign-up with nothing saved: %d %v (cookie %v, %d requests)", code, out["_raw"], hasCookie(hdr), waitingCount())
	}
	if out := e.must(200, admin, "GET", "/api/admin/instance", nil); out["registration"] != "open" || out["updatedAt"] != "" {
		t.Errorf("the editor with nothing saved: %v", out["_raw"])
	}
	if _, me := e.do("", "GET", "/api/me", nil); me["instance"] != nil {
		t.Errorf("a visitor's /api/me with nothing saved: %v", me["instance"])
	}
	if tot := totals(); tot["waiting"] != nil {
		t.Errorf("the dashboard with nothing saved has a waiting count: %v", tot)
	}
	// Saving open over nothing saved changes nothing, writes no row and is not audited.
	if code, out := setReg(admin, "open"); code != 200 || e.count(`SELECT count(*) FROM instance_settings`) != 0 || e.count(`SELECT count(*) FROM mod_actions WHERE action = 'instance'`) != 0 {
		t.Errorf("saving open over nothing: %d %v", code, out["_raw"])
	}

	// Who may change it, and to what.
	for _, tok := range []string{ana, mod} {
		if code, _ := setReg(tok, "approval"); code != 403 {
			t.Errorf("a member or moderator turning approval on: %d, want 403", code)
		}
	}
	for _, bad := range []string{"closed", "Approval", ""} {
		if code, out := setReg(admin, bad); code != 400 || out["field"] != "registration" {
			t.Errorf("registration %q: %d %v", bad, code, out["_raw"])
		}
	}
	v0 := version()
	if code, out := setReg(admin, "approval"); code != 200 || out["registration"] != "approval" {
		t.Fatalf("turning approval on: %d %v", code, out["_raw"])
	}
	var detail string
	if err := e.a.db.QueryRow(`SELECT detail::text FROM mod_actions WHERE action = 'instance' ORDER BY id DESC LIMIT 1`).Scan(&detail); err != nil || detail != `{"parts": ["registration"], "rules": 0, "registration": "approval"}` {
		t.Errorf("the audit row of turning approval on: %s (%v)", detail, err)
	}
	// A save against the version before is refused (409), whichever part it changes.
	if code, _ := e.do(deputy, "PUT", "/api/admin/instance", map[string]any{"description": "", "contact": "", "rules": []any{}, "registration": "open", "updatedAt": v0}); code != 409 {
		t.Errorf("a stale save of the registration: %d, want 409", code)
	}
	// A save that leaves registration out keeps it.
	if out := e.must(200, admin, "PUT", "/api/admin/instance", map[string]any{"description": "", "contact": "", "rules": []any{}, "updatedAt": version()}); out["registration"] != "approval" {
		t.Errorf("a save without registration: %v", out["_raw"])
	}
	// A visitor is told before typing; a member is told nothing; the dashboard counts the requests.
	if _, me := e.do("", "GET", "/api/me", nil); fmt.Sprint(me["instance"]) != "map[about:false approval:true]" {
		t.Errorf("a visitor's /api/me with approval on: %v", me["instance"])
	}
	if _, me := e.do(ana, "GET", "/api/me", nil); me["instance"] != nil {
		t.Errorf("a member's /api/me with approval on: %v", me["instance"])
	}
	if tot := totals(); tot["waiting"] == nil || num(tot["waiting"]) != 0 {
		t.Errorf("the dashboard with approval on and nobody waiting: %v", tot)
	}

	// A request: 202, no session, no account; the reason is plain text, cleaned as the instance text is.
	users0 := e.count(`SELECT count(*) FROM users`)
	code, out, hdr = signup(map[string]any{"username": "Asker", "password": pw, "displayName": "Asker A", "reason": "<b>I spin poi</b>\r\n\r\n\r\n\r\nAt the park.  "})
	if code != 202 || out["waiting"] != true || len(out) != 2 || hasCookie(hdr) || hdr.Get("Set-Cookie") != "" {
		t.Fatalf("a request to join: %d %v (Set-Cookie %q)", code, out["_raw"], hdr.Get("Set-Cookie"))
	}
	if e.count(`SELECT count(*) FROM users`) != users0 {
		t.Error("a request made an account")
	}
	var reason, display string
	if err := e.a.db.QueryRow(`SELECT reason, display_name FROM join_requests WHERE username = 'asker'`).Scan(&reason, &display); err != nil || reason != "<b>I spin poi</b>\n\nAt the park." || display != "Asker A" {
		t.Errorf("the request as stored: %q %q (%v)", reason, display, err)
	}
	for _, c := range []struct {
		name, reason string
	}{{"501 characters", strings.Repeat("é", 501)}, {"a NUL", "hi\x00"}, {"an escape", "\x1b[31mred"}, {"a direction override", "\u202eadmin"}} {
		if code, out, _ := signup(map[string]any{"username": "toolong", "password": pw, "reason": c.reason}); code != 400 || !strings.Contains(fmt.Sprint(out["error"]), "reason") {
			t.Errorf("a reason with %s: %d %v", c.name, code, out["_raw"])
		}
	}
	if code, out, _ := signup(map[string]any{"username": "fullreason", "password": pw, "reason": strings.Repeat("é", 500)}); code != 202 {
		t.Errorf("a reason of 500 characters: %d %v", code, out["_raw"])
	}
	if code, out, _ := signup(map[string]any{"username": "noreason", "password": pw}); code != 202 {
		t.Errorf("a request with no reason: %d %v", code, out["_raw"])
	}
	// The name waits, so it is taken; and a name with an account cannot be asked for.
	for _, name := range []string{"asker", "ana"} {
		if code, out, _ := signup(map[string]any{"username": name, "password": pw}); code != 409 {
			t.Errorf("asking for %s: %d %v", name, code, out["_raw"])
		}
	}
	if waitingCount() != 3 {
		t.Fatalf("%d requests, want 3", waitingCount())
	}

	// Signing in while waiting: the right password is told it waits, with no session; a wrong one gets
	// exactly what a name nobody has gets.
	if code, body, hdr := signin("asker", pw); code != 403 || !strings.Contains(body, waitingSentence) || hasCookie(hdr) {
		t.Errorf("signing in while waiting: %d %s", code, body)
	}
	_, nobody, _ := signin("nobody-at-all", pw)
	if code, body, hdr := signin("asker", "not the password"); code != 401 || body != nobody || hasCookie(hdr) {
		t.Errorf("a wrong password for a waiting name: %d %s, a name nobody has: %s", code, body, nobody)
	}
	if n := e.count(`SELECT count(*) FROM sessions`); n != 5 {
		t.Errorf("%d sessions, want five: the four the test made and the open sign-up's", n)
	}
	// A member's name, a waiting name and an unknown one each cost one read (signinLookup, the only read
	// handleSignin makes before the password is checked, counted through a wrapper of QueryRow) and one
	// password hash (pbkdf2Runs, around a whole sign-in).
	for _, c := range []struct {
		name           string
		request, found bool
	}{{"plain", false, true}, {"asker", true, true}, {"nobody-at-all", false, false}} {
		q := &countingDB{db: e.a.db}
		u, hash, request, err := signinLookup(q, c.name)
		if q.n != 1 || request != c.request || (err == nil) != c.found || (c.found && (u.Username != c.name || hash == "")) {
			t.Errorf("reading %s for a sign-in: %d reads, request %v, %v", c.name, q.n, request, err)
		}
		runs := pbkdf2Runs.Load()
		signin(c.name, "not the password")
		if n := pbkdf2Runs.Load() - runs; n != 1 {
			t.Errorf("a sign-in as %s checked %d hashes, want exactly one", c.name, n)
		}
	}

	// A waiting person is nobody anywhere: not in the people list, no profile, no posts, no mention,
	// no follow, no block, not in Accounts, not a member on the dashboard (counted on its own). The lists
	// are read first, and by a moderator, who sees past blocks; the open sign-up's account shows they list
	// a new account at all.
	if out := e.must(200, mod, "GET", "/api/users", nil); strings.Contains(out["_raw"].(string), `"asker"`) || !strings.Contains(out["_raw"].(string), `"plain"`) {
		t.Errorf("the people list shows a waiting name, or not a new account: %v", out["_raw"])
	}
	if out := e.must(200, admin, "GET", "/api/admin/users", nil); strings.Contains(out["_raw"].(string), `"asker"`) || !strings.Contains(out["_raw"].(string), `"plain"`) {
		t.Errorf("Accounts lists a waiting name among the accounts: %v", out["_raw"])
	}
	for _, w := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/users/asker", 404}, {"GET", "/api/posts?user=asker", 404},
		{"PUT", "/api/users/asker/follow", 404}, {"PUT", "/api/users/asker/block", 404},
	} {
		if code, out := e.do(ana, w.method, w.path, nil); code != w.want {
			t.Errorf("%s %s for a waiting name: %d %v", w.method, w.path, code, out["_raw"])
		}
	}
	notes := e.count(`SELECT count(*) FROM notifications`)
	e.post(ana, "hello @asker and @noreason")
	if e.count(`SELECT count(*) FROM notifications`) != notes {
		t.Error("a mention of a waiting name made a notification")
	}
	if tot := totals(); num(tot["users"]) != int64(users0) || num(tot["waiting"]) != 3 {
		t.Errorf("the dashboard while three wait: %v", tot)
	}

	// The list of requests is for user.manage only, and shows the reason.
	for tok, want := range map[string]int{"": 401, ana: 403, mod: 403} {
		if code, _ := e.do(tok, "GET", "/api/admin/requests", nil); code != want {
			t.Errorf("the list of requests for %q: %d, want %d", tok, code, want)
		}
	}
	list := e.must(200, admin, "GET", "/api/admin/requests", nil)
	reqs := list["requests"].([]any)
	if len(reqs) != 3 || num(list["max"]) != maxWaiting || num(list["days"]) != joinWaitDays {
		t.Fatalf("the list of requests: %v", list["_raw"])
	}
	if r0 := reqs[0].(map[string]any); r0["username"] != "asker" || r0["reason"] != "<b>I spin poi</b>\n\nAt the park." || r0["displayName"] != "Asker A" || r0["created"] == nil {
		t.Errorf("the oldest request: %v", r0)
	}
	idOf := func(name string) int64 {
		t.Helper()
		var id int64
		if err := e.a.db.QueryRow(`SELECT id FROM join_requests WHERE username = $1`, name).Scan(&id); err != nil {
			t.Fatalf("the request of %s: %v", name, err)
		}
		return id
	}
	approve := func(tok string, id int64) (int, map[string]any) {
		return e.do(tok, "POST", fmt.Sprintf("/api/admin/requests/%d/approve", id), nil)
	}
	decline := func(tok string, id int64) (int, map[string]any) {
		return e.do(tok, "DELETE", fmt.Sprintf("/api/admin/requests/%d", id), nil)
	}
	audits := func(action, name string) int {
		return e.count(`SELECT count(*) FROM mod_actions WHERE action = $1 AND detail->>'username' = $2`, action, name)
	}
	askerID := idOf("asker")

	// Without user.manage: refused, and nothing changes.
	for _, tok := range []string{ana, mod} {
		if code, _ := approve(tok, askerID); code != 403 {
			t.Errorf("approve without the permission: %d", code)
		}
		if code, _ := decline(tok, askerID); code != 403 {
			t.Errorf("decline without the permission: %d", code)
		}
	}
	if code, _ := approve("", askerID); code != 401 || waitingCount() != 3 || e.count(`SELECT count(*) FROM users WHERE username = 'asker'`) != 0 {
		t.Errorf("approve by a visitor: %d, %d waiting", code, waitingCount())
	}

	// Approve: an ordinary account, once, audited; the person signs in with the password they chose.
	if code, out := approve(admin, askerID); code != 200 || out["username"] != "asker" {
		t.Fatalf("approve: %d %v", code, out["_raw"])
	}
	var role string
	var owner bool
	if err := e.a.db.QueryRow(`SELECT role, owner, display_name FROM users WHERE username = 'asker'`).Scan(&role, &owner, &display); err != nil || role != "user" || owner || display != "Asker A" {
		t.Errorf("the approved account: %s %v %s (%v)", role, owner, display, err)
	}
	if code, _ := approve(admin, askerID); code != 404 {
		t.Errorf("approve twice: %d, want 404", code)
	}
	if code, _ := decline(admin, askerID); code != 404 {
		t.Errorf("decline after approve: %d, want 404", code)
	}
	if n := audits("approve_user", "asker"); n != 1 {
		t.Errorf("%d audit rows for one approval, want 1", n)
	}
	if e.count(`SELECT count(*) FROM mod_actions WHERE action = 'approve_user' AND target_user_id = (SELECT id FROM users WHERE username = 'asker')`) != 1 {
		t.Error("the approval's audit row does not name the account")
	}
	if code, body, hdr := signin("asker", pw); code != 200 || !hasCookie(hdr) {
		t.Errorf("signing in after approval: %d %s", code, body)
	}
	if code, _ := e.do(ana, "GET", "/api/users/asker", nil); code != 200 {
		t.Errorf("the profile after approval: %d", code)
	}

	// Decline: the request is gone, once, audited; signing in answers as for a name nobody has; the name
	// is free again.
	nrID := idOf("noreason")
	if code, out := decline(admin, nrID); code != 200 {
		t.Fatalf("decline: %d %v", code, out["_raw"])
	}
	if code, _ := decline(admin, nrID); code != 404 {
		t.Errorf("decline twice: %d, want 404", code)
	}
	if code, _ := approve(admin, nrID); code != 404 {
		t.Errorf("approve after decline: %d, want 404", code)
	}
	if n := audits("decline_user", "noreason"); n != 1 {
		t.Errorf("%d audit rows for one decline, want 1", n)
	}
	if code, body, _ := signin("noreason", pw); code != 401 || body != nobody {
		t.Errorf("signing in after a decline: %d %s", code, body)
	}
	if code, out, _ := signup(map[string]any{"username": "noreason", "password": pw}); code != 202 {
		t.Errorf("the declined name asked for again: %d %v", code, out["_raw"])
	}

	// Two admins at once: one approval and one 404; approve and decline together: one wins.
	for _, name := range []string{"race", "tug"} {
		if code, out, _ := signup(map[string]any{"username": name, "password": pw}); code != 202 {
			t.Fatalf("asking as %s: %d %v", name, code, out["_raw"])
		}
	}
	atOnce := func(f, g func() int) []int {
		var wg sync.WaitGroup
		codes := make([]int, 2)
		for i, h := range []func() int{f, g} {
			wg.Add(1)
			go func() { defer wg.Done(); codes[i] = h() }()
		}
		wg.Wait()
		sort.Ints(codes)
		return codes
	}
	raceID, tugID := idOf("race"), idOf("tug")
	if got := atOnce(func() int { c, _ := approve(admin, raceID); return c }, func() int { c, _ := approve(deputy, raceID); return c }); fmt.Sprint(got) != "[200 404]" {
		t.Errorf("two admins approving at once: %v", got)
	}
	if e.count(`SELECT count(*) FROM users WHERE username = 'race'`) != 1 || audits("approve_user", "race") != 1 {
		t.Error("two approvals at once made more than one account or audit row")
	}
	if got := atOnce(func() int { c, _ := approve(admin, tugID); return c }, func() int { c, _ := decline(deputy, tugID); return c }); fmt.Sprint(got) != "[200 404]" {
		t.Errorf("approve and decline at once: %v", got)
	}
	if n := audits("approve_user", "tug") + audits("decline_user", "tug"); n != 1 || waitingCount() != 2 {
		t.Errorf("approve and decline at once: %d audit rows, %d waiting", n, waitingCount())
	}

	// An account and a request under one name, as the moment an admin switches the setting can leave
	// them: the list says the name is taken, Approve answers 409 and changes nothing, Decline removes it.
	if _, err := e.a.db.Exec(`INSERT INTO join_requests (username, display_name, pw_hash) VALUES ('clash', 'clash', 'x')`); err != nil {
		t.Fatal(err)
	}
	e.user("clash", "user")
	var clash map[string]any
	for _, q := range e.must(200, admin, "GET", "/api/admin/requests", nil)["requests"].([]any) {
		if m := q.(map[string]any); m["username"] == "clash" {
			clash = m
		} else if m["taken"] != false {
			t.Errorf("a request whose name is free is listed as taken: %v", m)
		}
	}
	if clash == nil || clash["taken"] != true {
		t.Errorf("the request whose name an account took: %v", clash)
	}
	clashID := idOf("clash")
	if code, out := approve(admin, clashID); code != 409 || !strings.Contains(fmt.Sprint(out["error"]), "Decline it") || e.count(`SELECT count(*) FROM join_requests WHERE username = 'clash'`) != 1 || audits("approve_user", "clash") != 0 {
		t.Errorf("approving a request whose name is taken: %d %v", code, out["_raw"])
	}
	if code, _ := decline(admin, clashID); code != 200 || e.count(`SELECT count(*) FROM join_requests WHERE username = 'clash'`) != 0 || e.count(`SELECT count(*) FROM users WHERE username = 'clash'`) != 1 {
		t.Errorf("declining a request whose name is taken: %d", code)
	}
	// Each insert checks the other table in the same statement. A request held at the queue's lock while an
	// account takes its name is refused when the lock is let go, and stores nothing.
	ctx := context.Background()
	held, err := e.a.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := held.ExecContext(ctx, `SELECT pg_advisory_lock(7130653)`); err != nil {
		t.Fatal(err)
	}
	raced := make(chan int, 1)
	go func() { code, _, _ := signup(map[string]any{"username": "raced", "password": pw}); raced <- code }()
	for since := time.Now(); e.count(`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND objid = 7130653 AND NOT granted`) < 1; time.Sleep(10 * time.Millisecond) {
		if time.Since(since) > 10*time.Second {
			t.Error("the request never waited for the queue's lock")
			break
		}
	}
	e.user("raced", "user")
	held.ExecContext(ctx, `SELECT pg_advisory_unlock(7130653)`)
	held.Close()
	if code := <-raced; code != 409 || e.count(`SELECT count(*) FROM join_requests WHERE username = 'raced'`) != 0 {
		t.Errorf("a request whose name an account took while it waited for the lock: %d", code)
	}

	// The cap: at most maxWaiting wait; past it sign-up says to come back, and nothing is stored.
	if _, err := e.a.db.Exec(`INSERT INTO join_requests (username, display_name, pw_hash) SELECT 'filler' || g, 'filler', 'x' FROM generate_series(1, $1) g`, maxWaiting-waitingCount()); err != nil {
		t.Fatal(err)
	}
	code, out, _ = signup(map[string]any{"username": "onetoomany", "password": pw})
	if code != 503 || !strings.Contains(fmt.Sprint(out["error"]), "not taking requests") || waitingCount() != maxWaiting {
		t.Errorf("a request past the cap: %d %v, %d waiting", code, out["_raw"], waitingCount())
	}
	if !strings.Contains(logs.String(), "refused: too many requests to join waiting from 198.51.") {
		t.Errorf("the refusal past the cap is not in the log:\n%s", logs.String())
	}
	// Requests at the same moment at the cap less one: exactly one more waits. The test holds the table
	// against writes (reads go on), so the three requests are sure to be in flight together: the first has
	// counted 199 and waits to write, the others wait for the lock the count is made under. Were the count
	// not made under that lock, all three would count 199, wait to write, and all be stored.
	e.a.db.Exec(`DELETE FROM join_requests WHERE username = 'filler1'`)
	hold, err := e.a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(`LOCK TABLE join_requests IN EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		body := map[string]any{"username": fmt.Sprintf("flood%d", i), "password": pw}
		wg.Add(1)
		go func() { defer wg.Done(); signup(body) }()
	}
	inFlight := `SELECT count(*) FROM pg_locks WHERE NOT granted AND (relation = 'join_requests'::regclass OR (locktype = 'advisory' AND objid = 7130653))`
	for since := time.Now(); e.count(inFlight) < 3; time.Sleep(10 * time.Millisecond) {
		if time.Since(since) > 10*time.Second {
			t.Errorf("the three requests were not all waiting after 10 s: %d", e.count(inFlight))
			break
		}
	}
	hold.Rollback()
	wg.Wait()
	if waitingCount() != maxWaiting {
		t.Errorf("three requests at once at the cap less one: %d waiting, want %d", waitingCount(), maxWaiting)
	}
	// With the queue full, one address asking again and again: each request counts against its allowance
	// like any attempt, so the eleventh gets the same 429 as the open path gives, and none of the eleven
	// costs a password hash (pbkdf2Runs counts every hash made or checked).
	const tooManyBody = `{"error":"Too many new accounts from this address. Try again in an hour."}` + "\n" // the open path's, which TestDBSignupLimits holds
	runs := pbkdf2Runs.Load()
	var codes []int
	var last map[string]any
	for i := 0; i < 11; i++ {
		var code int
		code, last, _ = sendFrom("203.0.113.7:4000", "/api/signup", map[string]any{"username": fmt.Sprintf("full%d", i), "password": pw})
		codes = append(codes, code)
	}
	if fmt.Sprint(codes) != "[503 503 503 503 503 503 503 503 503 503 429]" || last["_raw"] != tooManyBody {
		t.Errorf("eleven requests from one address while the queue is full: %v, the last %q", codes, last["_raw"])
	}
	if n := pbkdf2Runs.Load() - runs; n != 0 {
		t.Errorf("requests refused by a full queue or the allowance hashed %d passwords, want none", n)
	}
	e.a.db.Exec(`DELETE FROM join_requests WHERE username LIKE 'filler%' OR username LIKE 'flood%'`)
	// Room again: thirty requests at the same moment from one address hash at most the ten the allowance
	// lets through, and ten wait. The open path counts the same way, before it hashes.
	burst := func(prefix, remote string) (map[int]int, int64) {
		t.Helper()
		runs := pbkdf2Runs.Load()
		var mu sync.Mutex
		got := map[int]int{}
		var wg sync.WaitGroup
		for i := 0; i < 30; i++ {
			body := map[string]any{"username": fmt.Sprintf("%s%d", prefix, i), "password": pw}
			wg.Add(1)
			go func() {
				defer wg.Done()
				code, _, _ := sendFrom(remote, "/api/signup", body)
				mu.Lock()
				got[code]++
				mu.Unlock()
			}()
		}
		wg.Wait()
		return got, pbkdf2Runs.Load() - runs
	}
	if got, n := burst("burst", "203.0.113.8:4000"); got[202] != 10 || got[429] != 20 || n > 10 {
		t.Errorf("thirty requests at once from one address: %v, %d hashes; want ten 202, twenty 429, at most ten hashes", got, n)
	}
	e.a.db.Exec(`DELETE FROM join_requests WHERE username LIKE 'burst%'`)

	// Housekeeping lets go of requests that waited more than 30 days, with a line in the log.
	e.a.db.Exec(`INSERT INTO join_requests (username, display_name, pw_hash, created_at) VALUES ('old', 'old', 'x', now() - interval '31 days'), ('recent', 'recent', 'x', now() - interval '29 days')`)
	if n, err := e.a.housekeep(); err != nil || n["requests"] != 1 {
		t.Errorf("housekeeping: %v (%v)", n, err)
	}
	if e.count(`SELECT count(*) FROM join_requests WHERE username = 'old'`) != 0 || e.count(`SELECT count(*) FROM join_requests WHERE username = 'recent'`) != 1 {
		t.Error("housekeeping removed the wrong requests")
	}
	e.a.db.Exec(`INSERT INTO join_requests (username, display_name, pw_hash, created_at) VALUES ('older', 'older', 'x', now() - interval '40 days')`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.a.housekeeping(ctx, time.Hour)
	if !strings.Contains(logs.String(), "housekeeping: removed 1 requests to join that waited more than 30 days\n") {
		t.Errorf("the housekeeping log:\n%s", logs.String())
	}
	e.a.db.Exec(`DELETE FROM join_requests WHERE username = 'recent'`)

	// Switched back to open: the waiting requests keep waiting, their names stay taken, a new sign-up is
	// an account at once, and the dashboard still counts them.
	before := waitingCount()
	if code, out := setReg(admin, "open"); code != 200 || out["registration"] != "open" || waitingCount() != before || before == 0 {
		t.Fatalf("back to open: %d %v, %d waiting (was %d)", code, out["_raw"], waitingCount(), before)
	}
	if _, me := e.do("", "GET", "/api/me", nil); me["instance"] != nil {
		t.Errorf("a visitor's /api/me back to open: %v", me["instance"])
	}
	if code, body, _ := signin("fullreason", pw); code != 403 || !strings.Contains(body, waitingSentence) {
		t.Errorf("a waiting person after the switch back: %d %s", code, body)
	}
	if code, _, _ := signup(map[string]any{"username": "fullreason", "password": pw}); code != 409 {
		t.Errorf("a waiting name after the switch back: %d", code)
	}
	if code, out, hdr := signup(map[string]any{"username": "openagain", "password": pw}); code != 200 || !hasCookie(hdr) || len(out) != 2 {
		t.Errorf("an open sign-up after the switch back: %d %v", code, out["_raw"])
	}
	// The open path's insert checks for a request in the same statement: an open sign-up held at the users
	// table while a request takes its name makes no account.
	hold, err = e.a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(`LOCK TABLE users IN EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	go func() { code, _, _ := signup(map[string]any{"username": "openraced", "password": pw}); raced <- code }()
	for since := time.Now(); e.count(`SELECT count(*) FROM pg_locks WHERE relation = 'users'::regclass AND NOT granted`) < 1; time.Sleep(10 * time.Millisecond) {
		if time.Since(since) > 10*time.Second {
			t.Error("the open sign-up never waited for the users table")
			break
		}
	}
	e.a.db.Exec(`INSERT INTO join_requests (username, display_name, pw_hash) VALUES ('openraced', 'openraced', 'x')`)
	hold.Rollback()
	if code := <-raced; code != 409 || e.count(`SELECT count(*) FROM users WHERE username = 'openraced'`) != 0 {
		t.Errorf("an open sign-up whose name a request took while it waited: %d", code)
	}
	e.a.db.Exec(`DELETE FROM join_requests WHERE username = 'openraced'`)
	if tot := totals(); num(tot["waiting"]) != int64(before) {
		t.Errorf("the dashboard after the switch back: %v", tot)
	}
	// The open path under the same burst: it counted the allowance before hashing already.
	if got, n := burst("openburst", "203.0.113.9:4000"); got[200] != 10 || got[429] != 20 || n > 10 {
		t.Errorf("thirty open sign-ups at once from one address: %v, %d hashes; want ten 200, twenty 429, at most ten hashes", got, n)
	}

	// The flag off: sign-up is closed whatever the setting, and the waiting requests can still be
	// approved or declined.
	if code, out := setReg(admin, "approval"); code != 200 {
		t.Fatalf("approval on again: %d %v", code, out["_raw"])
	}
	e.setFlag("signups", false)
	if code, out, _ := signup(map[string]any{"username": "closed", "password": pw}); code != 403 || out["error"] != "New sign-ups are turned off. Ask the admin for an account." {
		t.Errorf("sign-up with the flag off: %d %v", code, out["_raw"])
	}
	if code, _ := approve(admin, idOf("fullreason")); code != 200 {
		t.Errorf("approve with the flag off: %d", code)
	}
	if code, _ := decline(admin, idOf("noreason")); code != 200 {
		t.Errorf("decline with the flag off: %d", code)
	}
	e.setFlag("signups", true)

	// The operator's account is made, approved, whatever the setting says, and a request under its name
	// gives way to it; the owner is an approved admin.
	if code, _, _ := signup(map[string]any{"username": "chief", "password": pw}); code != 202 {
		t.Fatalf("asking as chief: %d", code)
	}
	if _, err := e.a.seedAdmin("chief", "flowa-test-only"); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM join_requests WHERE username = 'chief'`) != 0 || e.count(`SELECT count(*) FROM users WHERE username = 'chief' AND role = 'admin'`) != 1 {
		t.Error("seedAdmin with a request under its name")
	}
	if !strings.Contains(logs.String(), `removed the request to join that waited under the name "chief"`) {
		t.Error("seedAdmin did not log the request it removed")
	}
	if code, body, _ := signin("chief", "flowa-test-only"); code != 200 {
		t.Errorf("the seeded admin signs in with approval on: %d %s", code, body)
	}
	f := newEnv(t)
	if _, err := f.a.db.Exec(`INSERT INTO instance_settings (key, value) VALUES ('registration', '"approval"')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.seedAdmin("first", "flowa-test-only"); err != nil {
		t.Fatal(err)
	}
	if err := f.a.ensureOwner(); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT count(*) FROM users WHERE username = 'first' AND role = 'admin' AND owner`); n != 1 || f.count(`SELECT count(*) FROM join_requests`) != 0 {
		t.Errorf("a first start with approval on: %d owners, %d requests", n, f.count(`SELECT count(*) FROM join_requests`))
	}
	if code, out := f.do("", "POST", "/api/signin", map[string]any{"username": "first", "password": "flowa-test-only"}); code != 200 {
		t.Errorf("the first owner signs in with approval on: %d %v", code, out["_raw"])
	}
}

// singleStart runs the start in single mode with the settings given, and builds the routes it answers.
func (e *tenv) singleStart(env map[string]string, st *static) error {
	e.t.Helper()
	all := map[string]string{"FLOWA_MODE": "single"}
	maps.Copy(all, env)
	if err := e.a.start(func(k string) string { return all[k] }); err != nil {
		return err
	}
	e.h = e.a.routes(st)
	return nil
}

// visit asks for a path with no session from an address of its own (n), or with the cookie given, and
// returns the status, the body and the headers.
func (e *tenv) visit(method, path, cookie string, n int, hdr map[string]string) (int, string, http.Header) {
	e.t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.RemoteAddr = fmt.Sprintf("203.0.%d.%d:5000", 113+n/250, n%250+1)
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: cookieName, Value: cookie})
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w.Code, w.Body.String(), w.Result().Header
}

// pngData is a small PNG as a data URL, as the page sends a profile picture.
func pngData(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// Single-profile mode (FLOWA_MODE=single): the start keeps to one account, the owner, and refuses with
// a sentence that says what to do before anything is written; sign-up and the routes that make, remove
// or demote an account are closed; a signed-out visitor to / gets the owner's profile as a small page
// the server writes, with no script, no post and no number, from memory; a signed-in owner gets the app.
func TestDBSingleMode(t *testing.T) {
	const pw = "flowa-test-only"
	app := staticOf(t, map[string]string{"index.html": "<!doctype html><title>Flowa app</title>", "app.css": "body{}", "icon.svg": "<svg/>"})
	rows := func(e *tenv) string {
		t.Helper()
		var s string
		if err := e.a.db.QueryRow(`SELECT COALESCE(string_agg(concat_ws('|', id, username, role, owner, display_name, bio), ',' ORDER BY id), '') FROM users`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	refused := func(name string, env map[string]string, prepare func(*tenv), want ...string) {
		t.Helper()
		e := newEnv(t)
		if prepare != nil {
			prepare(e)
		}
		before, cats := rows(e), e.count(`SELECT count(*) FROM catalog_revisions`)
		err := e.singleStart(env, app)
		if err == nil {
			t.Errorf("%s: the start went ahead, want it refused", name)
			return
		}
		for _, w := range want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: %q does not say %q", name, err, w)
			}
		}
		if after := rows(e); after != before || e.count(`SELECT count(*) FROM catalog_revisions`) != cats {
			t.Errorf("%s: a refused start wrote: accounts %q, then %q", name, before, after)
		}
	}
	two := func(e *tenv) { e.user("ana", "admin"); e.user("bo", "user") }
	refused("two accounts", nil, two, "2 local accounts", "local mode", "Nothing was changed or deleted")
	refused("two accounts, ADMIN_USER one of them", map[string]string{"ADMIN_USER": "ana", "ADMIN_PASSWORD": pw}, two, "2 local accounts")
	refused("three accounts", nil, func(e *tenv) { two(e); e.user("cy", "moderator") }, "3 local accounts")
	refused("no account, no ADMIN_USER", nil, nil, "no account yet", "ADMIN_USER", "ADMIN_PASSWORD")
	refused("no account, ADMIN_USER without a password", map[string]string{"ADMIN_USER": "ana"}, nil, "ADMIN_USER", "ADMIN_PASSWORD")
	refused("ADMIN_USER would be a second account", map[string]string{"ADMIN_USER": "newbie", "ADMIN_PASSWORD": pw}, func(e *tenv) { e.user("ana", "admin") }, "ADMIN_USER=newbie", "@ana", "second account")
	refused("the sole account suspended", nil, func(e *tenv) {
		id, _ := e.user("ana", "admin")
		e.a.db.Exec(`UPDATE users SET suspended_until = now() + interval '1 day' WHERE id = $1`, id)
	}, "@ana", "suspended until", "local mode")

	// No account and ADMIN_USER: it is made, the admin and the owner. A request to join left waiting is
	// logged here too.
	e := newEnv(t)
	firstLogs := logged(t)
	if _, err := e.a.db.Exec(`INSERT INTO join_requests (username, display_name, pw_hash) VALUES ('early', 'early', 'x')`); err != nil {
		t.Fatal(err)
	}
	if err := e.singleStart(map[string]string{"ADMIN_USER": " Owner ", "ADMIN_PASSWORD": pw}, app); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(firstLogs.String(), "1 requests to join wait") {
		t.Errorf("a first start with a request waiting does not log it: %s", firstLogs.String())
	}
	if got := rows(e); !strings.HasSuffix(got, "|owner|admin|t|Admin|") || e.count(`SELECT count(*) FROM users`) != 1 {
		t.Errorf("a first start with ADMIN_USER: %q", got)
	}
	if e.a.mode != modeSingle {
		t.Errorf("mode %q, want single", e.a.mode)
	}
	// And the next start with the same settings finds it and adds nothing.
	if err := e.singleStart(map[string]string{"ADMIN_USER": "owner", "ADMIN_PASSWORD": pw}, app); err != nil || e.count(`SELECT count(*) FROM users`) != 1 {
		t.Errorf("a second start: %v, %d accounts", err, e.count(`SELECT count(*) FROM users`))
	}

	// A sole account that is a member is made the admin, and then the owner by ensureOwner; one that is an
	// admin and not the owner is made the owner. A mute is logged and the start goes on.
	for _, role := range []string{"user", "moderator", "admin"} {
		f := newEnv(t)
		logs := logged(t)
		id, _ := f.user("ana", role)
		f.a.db.Exec(`UPDATE users SET muted_until = now() + interval '1 day' WHERE id = $1`, id)
		if err := f.singleStart(nil, app); err != nil {
			t.Fatalf("a sole %s: %v", role, err)
		}
		if got := rows(f); got != fmt.Sprintf("%d|ana|admin|t|ana|", id) {
			t.Errorf("a sole %s after a single start: %q, want the admin and the owner", role, got)
		}
		if !strings.Contains(logs.String(), "@ana, is muted until") {
			t.Errorf("a sole muted %s: the log does not say so: %s", role, logs.String())
		}
	}

	// Moving between modes on one database: local with one account, then single, then local again. The
	// second move changes nothing in the data; the first changes only what makes the account the owner.
	m := newEnv(t)
	anaID, _ := m.user("ana", "admin")
	m.a.db.Exec(`UPDATE users SET owner = true, bio = 'kept' WHERE id = $1`, anaID)
	if err := m.a.start(func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	local := rows(m)
	var reqID int64
	if err := m.a.db.QueryRow(`INSERT INTO join_requests (username, display_name, pw_hash) VALUES ('asks', 'asks', $1) RETURNING id`, hashPassword("a fine long password")).Scan(&reqID); err != nil {
		t.Fatal(err)
	}
	logs := logged(t)
	if err := m.singleStart(nil, app); err != nil {
		t.Fatalf("local to single with one account: %v", err)
	}
	if got := rows(m); got != local {
		t.Errorf("local to single changed the accounts: %q, was %q", got, local)
	}
	if !strings.Contains(logs.String(), "1 requests to join wait") {
		t.Errorf("a request left from local mode is not logged at a single start: %s", logs.String())
	}
	// The request left from local mode: never an account. Its right password is a wrong one here.
	m.a.lim = newLimits()
	if code, out := m.do("", "POST", "/api/signin", map[string]string{"username": "asks", "password": "a fine long password"}); code != 401 || strings.Contains(out["_raw"].(string), "waiting") {
		t.Errorf("signing in with a request in single mode: %d %v, want the answer to a wrong password", code, out["_raw"])
	}
	if err := m.a.start(func(string) string { return "" }); err != nil {
		t.Fatalf("single to local: %v", err)
	}
	if got := rows(m); got != local || m.count(`SELECT count(*) FROM join_requests WHERE id = $1`, reqID) != 1 {
		t.Errorf("single to local changed the data: %q, was %q", got, local)
	}

	// From here, one instance in single mode with a profile full of markup, numbers, a post and badges.
	s := newEnv(t)
	logged(t)
	ownerID, owner := s.user("ana", "user")
	if err := s.singleStart(nil, app); err != nil {
		t.Fatal(err)
	}
	s.a.version = "v9.8.7-flowa-test"
	s.a.lim = newLimits()
	const name = `<b>"Ana" & 'co'</b>`
	const bio = `<script>alert(1)</script> "quoted" 'apostrophes' <a href="javascript:alert(2)">x</a> javascript:alert(3)` + "\nsecond line"
	s.must(200, owner, "PUT", "/api/me", map[string]any{"displayName": name, "bio": bio,
		"links": map[string]string{"instagram": "https://instagram.com/ana.flow", "youtube": "@ana-spins"},
		"stats": map[string]any{"xp": 98765, "level": 43, "streak": 4321}})
	s.post(owner, "SECRET-POST-TEXT, not for visitors")
	s.a.db.Exec(`INSERT INTO achievements (user_id, key) VALUES ($1, 'zz-badge-key')`, ownerID)
	inst := s.must(200, owner, "GET", "/api/admin/instance", nil)
	s.must(200, owner, "PUT", "/api/admin/instance", map[string]any{"description": "INSTANCE-DESC <i>x</i>", "contact": "admin@example.com",
		"rules": []map[string]string{{"text": "RULE-ONE <b>", "hint": "HINT-ONE"}}, "registration": "approval", "updatedAt": inst["updatedAt"]})

	// Sign-up is closed whatever the flag and the setting say, before any hash or count; the routes
	// for requests to join and for changing accounts are closed too.
	s.setFlag("signups", true)
	runs := pbkdf2Runs.Load()
	for i := 0; i < 15; i++ {
		code, out := s.do("", "POST", "/api/signup", map[string]string{"username": fmt.Sprintf("new%d", i), "password": "a fine long password"})
		if code != 403 || out["error"] != singleOnly {
			t.Fatalf("sign-up %d in single mode: %d %v", i, code, out["_raw"])
		}
	}
	if pbkdf2Runs.Load() != runs || s.count(`SELECT count(*) FROM users`) != 1 || s.count(`SELECT count(*) FROM join_requests`) != 0 || s.a.lim.signup.over(limitKey("192.0.2.1")) {
		t.Error("a refused sign-up in single mode hashed, counted or wrote something")
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/admin/requests"}, {"POST", "/api/admin/requests/1/approve"}, {"DELETE", "/api/admin/requests/1"},
		{"DELETE", fmt.Sprintf("/api/admin/users/%d", ownerID)}, {"PUT", fmt.Sprintf("/api/admin/users/%d/role", ownerID)}, {"PUT", fmt.Sprintf("/api/admin/users/%d/owner", ownerID)},
	} {
		for _, tok := range []string{owner, ""} {
			if code, out := s.do(tok, c.method, c.path, map[string]string{"role": "user"}); code != 403 || out["error"] != singleOnly {
				t.Errorf("%s %s (%q) in single mode: %d %v, want 403", c.method, c.path, tok, code, out["_raw"])
			}
		}
	}
	// /api/me says single to the owner, never to a visitor (who is told that sign-up is off instead), and
	// never that joining is by approval.
	for _, tok := range []string{"", owner} {
		out := s.must(200, tok, "GET", "/api/me", nil)
		if (out["single"] == true) != (tok != "") || (tok == "" && out["flags"].(map[string]any)["signups"] != false) {
			t.Errorf("/api/me (%q) in single mode: single %v, signups %v", tok, out["single"], out["flags"].(map[string]any)["signups"])
		}
		if in, _ := out["instance"].(map[string]any); in["approval"] != nil {
			t.Errorf("/api/me (%q) in single mode says joining is by approval", tok)
		}
	}

	// The visitor's page.
	visits := 0
	visit := func(method, path, cookie string, hdr map[string]string) (int, string, http.Header) {
		t.Helper()
		visits++
		return s.visit(method, path, cookie, visits, hdr)
	}
	code, body, hdr := visit("GET", "/", "", nil)
	if code != 200 {
		t.Fatalf("the visitor's page: %d %s", code, body)
	}
	for k, want := range map[string]string{"Content-Security-Policy": visitorCSP, "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-cache",
		"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer", "Cross-Origin-Opener-Policy": "same-origin"} {
		if hdr.Get(k) != want {
			t.Errorf("the visitor's page: %s is %q, want %q", k, hdr.Get(k), want)
		}
	}
	if visitorCSP != "default-src 'none'; img-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'" {
		t.Errorf("the visitor's CSP is not the one of docs/federation.md 5.6: %q", visitorCSP)
	}
	if !slices.Contains(hdr.Values("Vary"), "Cookie") || hdr.Get("ETag") == "" || hdr.Get("Permissions-Policy") == "" {
		t.Errorf("the visitor's page: Vary %q, ETag %q", hdr.Values("Vary"), hdr.Get("ETag"))
	}
	for _, want := range []string{
		`<html lang="en">`, `&lt;b&gt;&#34;Ana&#34; &amp; &#39;co&#39;&lt;/b&gt;`, "@ana", `&lt;script&gt;alert(1)&lt;/script&gt;`, "second line",
		`href="https://www.instagram.com/ana.flow/" rel="nofollow noopener noreferrer"`, `href="https://www.youtube.com/@ana-spins" rel="nofollow noopener noreferrer"`,
		"INSTANCE-DESC &lt;i&gt;x&lt;/i&gt;", `href="mailto:admin@example.com"`, "RULE-ONE &lt;b&gt;", "HINT-ONE", `href="index.html#/signin">Sign in</a>`,
		`href="` + hashedName("app.css", app.plain["/app.css"].hash) + `"`, "<h1 ", "<main", "<header",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the visitor's page does not hold %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, `aria-hidden="true">B</span>`) { // the b of <b>, the first letter typed
		t.Errorf("the circle without a picture does not hold the first letter of the name:\n%s", body)
	}
	if strings.Count(body, "<h1") != 1 {
		t.Errorf("the visitor's page has %d h1 elements, want 1", strings.Count(body, "<h1"))
	}
	low := strings.ToLower(body)
	for _, not := range []string{"<script", "javascript:alert(2)\"", `href="javascript`, "<a href=\"javascript", "secret-post-text", "98765", "4321", ">43<", "level", "streak", " xp",
		"badge", "zz-badge-key", "v9.8.7", "single", "local", "federat", "member", "1 account", "approval", "<form", "<input", "sign up", "create an account", "style=", "onerror", "onclick"} {
		if strings.Contains(low, strings.ToLower(not)) {
			t.Errorf("the visitor's page holds %q:\n%s", not, body)
		}
	}
	// HEAD is the same headers with no body; the ETag gets 304; a write to / is refused as before.
	if code, b, h := visit("HEAD", "/", "", nil); code != 200 || b != "" || h.Get("ETag") != hdr.Get("ETag") || h.Get("Content-Security-Policy") != visitorCSP {
		t.Errorf("HEAD /: %d %q %v", code, b, h)
	}
	if code, b, h := visit("GET", "/", "", map[string]string{"If-None-Match": hdr.Get("ETag")}); code != 304 || b != "" || !slices.Contains(h.Values("Vary"), "Cookie") {
		t.Errorf("GET / with its ETag: %d %q", code, b)
	}
	if code, _, _ := visit("POST", "/", "", map[string]string{"Content-Type": "application/json"}); code != http.StatusMethodNotAllowed {
		t.Errorf("POST / in single mode: %d, want 405", code)
	}

	// The owner signed in gets the app at /, as on any instance, also told apart by the cookie.
	code, body, h := visit("GET", "/", owner, nil)
	if code != 200 || !strings.Contains(body, "Flowa app") || h.Get("Content-Security-Policy") != csp(true) || !slices.Contains(h.Values("Vary"), "Cookie") || !slices.Contains(h.Values("Vary"), "Accept-Encoding") {
		t.Errorf("the signed-in owner at /: %d %q %v", code, body, h)
	}
	// A cookie that is garbage, an expired session, a session past its idle time, or a suspended account
	// is no session: the page.
	var expired, idle string
	{
		raw := make([]byte, 16)
		rand.Read(raw)
		expired = hex.EncodeToString(raw)
		s.a.db.Exec(`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, now() - interval '1 minute')`, tokenHash(expired), ownerID)
		rand.Read(raw)
		idle = hex.EncodeToString(raw)
		s.a.db.Exec(`INSERT INTO sessions (token_hash, user_id, expires_at, last_seen) VALUES ($1, $2, now() + interval '1 day', now() - interval '30 days')`, tokenHash(idle), ownerID)
	}
	for _, c := range []string{"garbage", "", expired, idle} {
		if code, b, _ := visit("GET", "/", c, nil); code != 200 || !strings.Contains(b, "pub-name") || strings.Contains(b, "Flowa app") {
			t.Errorf("GET / with the cookie %q: %d, want the visitor's page", c, code)
		}
	}
	// The app stays at /index.html for a signed-out owner, so Sign in reaches the form.
	if code, b, _ := visit("GET", "/index.html", "", nil); code != 200 || !strings.Contains(b, "Flowa app") {
		t.Errorf("GET /index.html signed out: %d %q, want the app", code, b)
	}

	// The picture: none yet, then the one the owner saves, served from memory with the page.
	if code, _, _ := visit("GET", "/profile-picture", "", nil); code != 404 {
		t.Errorf("the owner's picture before there is one: %d, want 404", code)
	}
	s.must(200, owner, "PUT", "/api/me", map[string]any{"avatar": pngData(t)})
	_, body, _ = visit("GET", "/", "", nil)
	if !strings.Contains(body, `<img class="pub-pic" src="profile-picture?v=`) {
		t.Errorf("the page after a picture was saved has none:\n%s", body)
	}
	code, pic, h := visit("GET", "/profile-picture", "", nil)
	if code != 200 || h.Get("Content-Type") != "image/png" || !strings.HasPrefix(pic, "\x89PNG") {
		t.Errorf("the owner's picture: %d %q", code, h.Get("Content-Type"))
	}
	// Under the address the page gives it (with the hash of its bytes) the picture is kept for good; under
	// any other it is asked for again.
	addr := regexp.MustCompile(`src="(profile-picture\?v=[0-9a-f]+)"`).FindStringSubmatch(body)
	if addr == nil {
		t.Fatalf("no picture address on the page:\n%s", body)
	}
	for path, want := range map[string]string{"/" + addr[1]: "max-age=31536000, immutable", "/profile-picture": "no-cache", "/profile-picture?v=0123456789abcdef": "no-cache"} {
		if code, _, h := visit("GET", path, "", nil); code != 200 || h.Get("Cache-Control") != want {
			t.Errorf("GET %s: %d, Cache-Control %q, want %q", path, code, h.Get("Cache-Control"), want)
		}
	}
	// The type is what the bytes decode as: a PNG stored under a JPEG prefix is sent as image/png, and
	// bytes that are no picture under an image prefix are not sent, nor named on the page.
	lie := func(v string) {
		t.Helper()
		if _, err := s.a.db.Exec(`UPDATE users SET avatar = $1 WHERE id = $2`, v, ownerID); err != nil {
			t.Fatal(err)
		}
		s.a.dropVisitorPage()
	}
	lie(strings.Replace(pngData(t), "data:image/png", "data:image/jpeg", 1))
	if code, pic, h := visit("GET", "/profile-picture", "", nil); code != 200 || h.Get("Content-Type") != "image/png" || !strings.HasPrefix(pic, "\x89PNG") {
		t.Errorf("a PNG stored as a JPEG: %d %q, want image/png", code, h.Get("Content-Type"))
	}
	lie("data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("<svg onload=alert(1)>")))
	if code, _, _ := visit("GET", "/profile-picture", "", nil); code != 404 {
		t.Errorf("bytes that are no picture under an image prefix: %d, want 404", code)
	}
	if _, b, _ := visit("GET", "/", "", nil); strings.Contains(b, "profile-picture") || !strings.Contains(b, "pub-initial") {
		t.Error("the page names a picture that is no picture")
	}
	s.must(200, owner, "PUT", "/api/me", map[string]any{"avatar": pngData(t)})
	// A bio that is only white space makes no empty paragraph.
	if _, err := s.a.db.Exec(`UPDATE users SET bio = $1 WHERE id = $2`, " \n\t  \n", ownerID); err != nil {
		t.Fatal(err)
	}
	s.a.dropVisitorPage()
	// (The bio's paragraph is the one right after the names; the instance's description has one too.)
	if _, b, _ := visit("GET", "/", "", nil); strings.Contains(b, "</div></div>\n<p class=\"pub-text\">") || !strings.Contains(b, "pub-name") {
		t.Errorf("a white-space bio makes a paragraph:\n%s", b)
	}
	if code, _, _ := visit("GET", fmt.Sprintf("/api/users/%d/avatar", ownerID), "", nil); code != 401 {
		t.Errorf("the members' picture address for a visitor: %d, want 401 as before", code)
	}

	// The page is dropped when the owner saves their profile and when an admin saves the instance.
	s.must(200, owner, "PUT", "/api/me", map[string]any{"bio": "A NEW BIO"})
	if _, b, _ := visit("GET", "/", "", nil); !strings.Contains(b, "A NEW BIO") {
		t.Error("the page after a profile save still shows the old bio")
	}
	inst = s.must(200, owner, "GET", "/api/admin/instance", nil)
	s.must(200, owner, "PUT", "/api/admin/instance", map[string]any{"description": "A NEW DESCRIPTION", "contact": "", "rules": []any{}, "updatedAt": inst["updatedAt"]})
	_, b, _ := visit("GET", "/", "", nil)
	if !strings.Contains(b, "A NEW DESCRIPTION") || strings.Contains(b, "RULE-ONE") {
		t.Error("the page after an instance save still shows the old information")
	}
	// A save of what the page does not show (the stats) keeps it.
	s.a.pageMu.Lock()
	kept := s.a.page
	s.a.pageMu.Unlock()
	s.must(200, owner, "PUT", "/api/me", map[string]any{"stats": map[string]any{"xp": 5}})
	s.a.pageMu.Lock()
	if s.a.page != kept {
		t.Error("a save of the stats alone dropped the visitor's page")
	}
	s.a.pageMu.Unlock()

	// A flood costs no query each: with the tables the page is made from moved away, the page and the
	// picture still come from memory, to visitors from many addresses.
	for _, q := range []string{`ALTER TABLE users RENAME TO users_away`, `ALTER TABLE instance_settings RENAME TO instance_settings_away`} {
		if _, err := s.a.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 60; i++ {
		if code, b2, _ := visit("GET", "/", "", nil); code != 200 || b2 != b {
			t.Fatalf("visit %d with the tables away: %d", i+1, code)
		}
		if code, _, _ := visit("GET", "/profile-picture", "", nil); code != 200 {
			t.Fatalf("picture %d with the tables away: %d", i+1, code)
		}
	}
	for _, q := range []string{`ALTER TABLE users_away RENAME TO users`, `ALTER TABLE instance_settings_away RENAME TO instance_settings`} {
		if _, err := s.a.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	// The limit by address: 300 reads an hour of the page and its picture, then 429 for that address only.
	for i := 0; i < 300; i++ {
		path := "/"
		if i%2 == 1 {
			path = "/profile-picture"
		}
		if code, _, _ := s.visit("GET", path, "", 9999, nil); code != 200 {
			t.Fatalf("read %d from one address: %d", i+1, code)
		}
	}
	if code, _, h := s.visit("GET", "/", "", 9999, nil); code != 429 || h.Get("Retry-After") == "" {
		t.Errorf("read 301 from one address: %d, want 429", code)
	}
	if code, _, _ := visit("GET", "/", "", nil); code != 200 {
		t.Errorf("another address after one ran out: %d", code)
	}
	// An address over its allowance is refused before any cookie is looked up, the owner's too: telling the
	// owner apart would cost the query the allowance is there to spare. The app's own address still opens.
	if code, _, _ := s.visit("GET", "/", owner, 9999, nil); code != 429 {
		t.Errorf("the signed-in owner from an address that ran out of visitor reads: %d, want 429", code)
	}
	if code, b, _ := s.visit("GET", "/index.html", owner, 9999, nil); code != 200 || !strings.Contains(b, "Flowa app") {
		t.Errorf("the app at /index.html from an address that ran out of visitor reads: %d", code)
	}

	// A local server on the same database answers / exactly as before: the app, with its own CSP and
	// no Vary on the cookie, for a visitor as for a member, and no picture address.
	l := &tenv{t: t, a: newApp(s.a.db, t.TempDir())}
	l.a.hosts = []string{"example.com"}
	if err := l.a.start(func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	l.h = l.a.routes(app)
	for _, c := range []string{"", owner} {
		code, b, h := l.visit("GET", "/", c, 1, nil)
		if code != 200 || !strings.Contains(b, "Flowa app") || h.Get("Content-Security-Policy") != csp(true) || slices.Contains(h.Values("Vary"), "Cookie") || h.Get("ETag") != `"`+app.plain["/index.html"].hash+`"` {
			t.Errorf("GET / (%q) in local mode on the same database: %d %q %v", c, code, b, h)
		}
	}
	if code, _, _ := l.visit("GET", "/profile-picture", "", 1, nil); code != 404 {
		t.Errorf("the picture address in local mode: %d, want 404", code)
	}
	// A visitor's /api/me differs between the two modes only in what says how to join: the signups flag,
	// and the approval a local instance with sign-up by approval tells a visitor. The mode is not told.
	lo, so := l.must(200, "", "GET", "/api/me", nil), s.must(200, "", "GET", "/api/me", nil)
	if lo["single"] != nil || lo["flags"].(map[string]any)["signups"] != true || lo["instance"].(map[string]any)["approval"] != true {
		t.Errorf("/api/me in local mode: %v", lo["_raw"])
	}
	delete(lo, "_raw")
	delete(so, "_raw")
	delete(lo["instance"].(map[string]any), "approval")
	so["flags"].(map[string]any)["signups"] = true
	if !reflect.DeepEqual(lo, so) {
		t.Errorf("a visitor's /api/me differs between local and single mode beyond the signups flag:\n%v\n%v", lo, so)
	}
}

// randomToken is a well-formed session token that names no session, as startSession makes them.
func randomToken() string {
	raw := make([]byte, 32)
	rand.Read(raw)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// A cookie on / costs a stranger nothing they can multiply: one that cannot be a session token is
// answered as no cookie, with no query; a well-formed one is counted against the address before it is
// looked up, so an address over its allowance gets 429 with no query. The database's one connection is
// held while it matters, so a request that asked it would hang and fail the test.
func TestDBSingleVisitorCookies(t *testing.T) {
	s := newEnv(t)
	_, owner := s.user("ana", "admin")
	if err := s.singleStart(nil, staticOf(t, map[string]string{"index.html": "<!doctype html><title>Flowa app</title>"})); err != nil {
		t.Fatal(err)
	}
	s.a.lim = newLimits()
	if code, _, _ := s.visit("GET", "/", "", 1, nil); code != 200 {
		t.Fatalf("the visitor's page: %d", code)
	}
	// hold takes the database's only connection until release is called.
	hold := func(t *testing.T) (release func()) {
		t.Helper()
		s.a.db.SetMaxOpenConns(1)
		tx, err := s.a.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		return func() { tx.Rollback(); s.a.db.SetMaxOpenConns(8) }
	}
	// quick answers a request that must not need the database, or fails the test when it waits for it.
	quick := func(t *testing.T, release func(), method, path, cookie string, n int) (int, string) {
		t.Helper()
		type answer struct {
			code int
			body string
		}
		at := make(chan answer, 1)
		go func() { c, b, _ := s.visit(method, path, cookie, n, nil); at <- answer{c, b} }()
		select {
		case a := <-at:
			return a.code, a.body
		case <-time.After(3 * time.Second):
			release()
			t.Fatalf("%s %s with the cookie %q waited for the database", method, path, cookie)
			return 0, ""
		}
	}

	// Cookies that cannot be a session token, of several shapes: the page, no query.
	t.Run("cookies that cannot be a session", func(t *testing.T) {
		release := hold(t)
		before := sessionLookups.Load()
		for i, c := range []string{"x", "garbage", "short-token", strings.Repeat("A", 65), "abc$def!ghi%jklmnop", "Zm9vYmFyYmF6cXV4==", "a.b.c.d.e.f.g.h.i.j", "<script>alert(1)</script>"} {
			for _, m := range []string{"GET", "HEAD"} {
				if code, b := quick(t, release, m, "/", c, 100+i); code != 200 || (m == "GET" && !strings.Contains(b, "pub-name")) {
					t.Errorf("%s / with the cookie %q: %d, want the visitor's page", m, c, code)
				}
			}
		}
		if code, _ := quick(t, release, "GET", "/profile-picture", randomToken(), 200); code != 404 {
			t.Errorf("the picture with a cookie while the database is held: %d, want 404 from memory", code)
		}
		release()
		if n := sessionLookups.Load() - before; n != 0 {
			t.Errorf("%d sessions were looked up for cookies that cannot be one, want none", n)
		}
	})

	// Well-formed tokens that name no session, 320 from one address: at most the allowance (300) is
	// looked up, and the rest are refused with 429 without a query, the database held for them.
	t.Run("made-up tokens past the allowance", func(t *testing.T) {
		before := sessionLookups.Load()
		for i := 0; i < 300; i++ {
			if code, _, _ := s.visit("GET", "/", randomToken(), 7777, nil); code != 200 {
				t.Fatalf("made-up token %d: %d", i+1, code)
			}
		}
		release := hold(t)
		for i := 0; i < 20; i++ {
			for _, m := range []string{"GET", "HEAD"} {
				if code, _ := quick(t, release, m, "/", randomToken(), 7777); code != 429 {
					t.Errorf("%s / with made-up token %d past the allowance: %d, want 429", m, 301+i, code)
				}
			}
		}
		if code, _ := quick(t, release, "GET", "/profile-picture", randomToken(), 7777); code != 429 {
			t.Errorf("the picture past the allowance: %d, want 429", code)
		}
		release()
		if n := sessionLookups.Load() - before; n > 300 {
			t.Errorf("%d sessions were looked up for 320 made-up tokens from one address, want at most 300", n)
		}
	})

	// The owner reloading / 400 times in an hour from one address is never refused: a proven session
	// gives its count back.
	for i := 0; i < 400; i++ {
		if code, b, _ := s.visit("GET", "/", owner, 8888, nil); code != 200 || !strings.Contains(b, "Flowa app") {
			t.Fatalf("the owner's reload %d: %d", i+1, code)
		}
	}
}

// A single-profile instance has the routes of a local one and the two of testdata/routes-single.txt,
// no more: the visitor's page at / and the owner's picture.
func TestDBSingleRoutes(t *testing.T) {
	e := newEnv(t)
	e.user("ana", "admin")
	if err := e.singleStart(nil, staticOf(t, nil)); err != nil {
		t.Fatal(err)
	}
	st := staticOf(t, map[string]string{"index.html": "<!doctype html><title>Flowa</title>"})
	got := slices.Clone(e.a.table(st).patterns)
	extra := routesFile(t, "testdata/routes-single.txt")
	want := append(routesFile(t, "testdata/routes-local.txt"), extra...)
	sort.Strings(got)
	sort.Strings(want)
	if !sort.StringsAreSorted(extra) {
		t.Error("testdata/routes-single.txt is not sorted")
	}
	if !slices.Equal(got, want) {
		for _, p := range got {
			if _, ok := slices.BinarySearch(want, p); !ok {
				t.Errorf("a route single mode was not to have: %q (add it to testdata/routes-single.txt if it is meant)", p)
			}
		}
		for _, p := range want {
			if _, ok := slices.BinarySearch(got, p); !ok {
				t.Errorf("a route single mode is to have and has not: %q", p)
			}
		}
	}
}

// federatedBuilt builds federated mode for one test when this version does not (modesBuilt), and puts
// modesBuilt back after. Federated mode is built since discovery (FRD 4), so today it does nothing; it
// is kept so the tests of the parts below discovery say what they need.
func federatedBuilt(t *testing.T) {
	t.Helper()
	if modesBuilt[modeFederated] {
		return
	}
	modesBuilt[modeFederated] = true
	t.Cleanup(func() { delete(modesBuilt, modeFederated) })
}

// fedEnv is what a federated start is given unless a test says otherwise.
func fedEnv(more map[string]string) func(string) string {
	all := map[string]string{"FLOWA_MODE": "federated", "FLOWA_ORIGIN": "https://flowa.example.org", "FLOWA_KEY_SECRET": testSecret,
		"ADMIN_USER": "root", "ADMIN_PASSWORD": "flowa-test-only", "FLOWA_PREVIEW_FETCH": "off"}
	maps.Copy(all, more)
	return func(k string) string { return all[k] }
}

// fedStart starts the app in federated mode, and stops its key worker before the test's database goes.
func (e *tenv) fedStart(more map[string]string) error {
	e.t.Helper()
	federatedBuilt(e.t)
	err := e.a.start(fedEnv(more))
	if f := e.a.fed; f != nil {
		e.t.Cleanup(func() {
			c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			f.stop(c)
		})
	}
	e.h = e.a.routes(staticOf(e.t, nil))
	return err
}

// again is a second server on the same database, as during a deploy or after a restart.
func (e *tenv) again() *tenv {
	a := newApp(e.a.db, e.t.TempDir())
	a.hosts = []string{"example.com"}
	return &tenv{t: e.t, a: a}
}

// waitFor polls cond until it holds, or fails after 30 s (a key takes up to two seconds on the Pi).
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(30 * time.Second); !cond(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("waited 30 s for %s", what)
		}
	}
}

// countKeys replaces generateKey for one test with one that counts the keys made.
func countKeys(t *testing.T) *atomic.Int32 {
	var n atomic.Int32
	was := generateKey
	generateKey = func() (*rsa.PrivateKey, error) { n.Add(1); return was() }
	t.Cleanup(func() { generateKey = was })
	return &n
}

// refusesKeys checks that a refused federation makes no key for a member who has none: requestKey
// declines, ensureKey declines, and a request that reaches the worker anyway (put on its queue
// directly) is taken and declined, with no key made and no row written.
func refusesKeys(t *testing.T, e *tenv, f *federation, name string) {
	t.Helper()
	if f.refusal() == "" {
		t.Fatal("federation is not refused")
	}
	id, _ := e.user(name, "user")
	o := keyOwner(id)
	var made atomic.Int32
	f.gen = func() (*rsa.PrivateKey, error) { made.Add(1); return generateKey() }
	if f.requestKey(o) {
		t.Errorf("%s: requestKey took a request while federation is refused", name)
	}
	if ok, err := f.ensureKey(context.Background(), o); ok || !errors.Is(err, errRefused) {
		t.Errorf("%s: ensureKey while refused: %v, %v", name, ok, err)
	}
	type result struct {
		o    keyOwner
		made bool
		err  error
	}
	handled := make(chan result, 1)
	f.handled = func(o keyOwner, made bool, err error) { handled <- result{o, made, err} }
	f.once.Do(f.startWorker)
	f.ask <- o
	select {
	case r := <-handled:
		if r.o != o || r.made || !errors.Is(r.err, errRefused) {
			t.Errorf("%s: the worker took the request and answered %v, %v", name, r.made, r.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("%s: waited 30 s for the worker to take the request", name)
	}
	if n := e.count(`SELECT count(*) FROM ap_keys WHERE user_id = $1`, id); n != 0 || made.Load() != 0 {
		t.Errorf("%s: %d keys made and %d rows written while federation is refused", name, made.Load(), n)
	}
}

// A mode that federates reads FLOWA_ORIGIN and FLOWA_KEY_SECRET before anything is written, and stops
// the start with a message that names the setting and never shows the secret. The origin's host is a
// name the server answers to then, the routes are those of local mode and discovery's three, and the
// start says once what this version of federation does.
func TestDBFederatedStart(t *testing.T) {
	logs := logged(t)
	for _, mode := range []string{"local", "single"} {
		if err := rotateCommand(fedEnv(map[string]string{"FLOWA_MODE": mode}), io.Discard); err == nil || !strings.Contains(err.Error(), "does not federate") {
			t.Errorf("-rotate-keys in %s mode: %v, want does not federate", mode, err)
		}
	}
	if err := rotateCommand(fedEnv(nil), io.Discard); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("-rotate-keys in federated mode without a database: %v, want DATABASE_URL", err)
	}
	federatedBuilt(t)
	for _, c := range []struct {
		name string
		env  map[string]string
		says []string
	}{
		{"no origin", map[string]string{"FLOWA_ORIGIN": ""}, []string{"FLOWA_ORIGIN", "not set"}},
		{"an http origin", map[string]string{"FLOWA_ORIGIN": "http://flowa.example.org"}, []string{"FLOWA_ORIGIN", "https"}},
		{"an origin on an address", map[string]string{"FLOWA_ORIGIN": "https://192.0.2.7"}, []string{"FLOWA_ORIGIN", "IP address"}},
		{"no secret", map[string]string{"FLOWA_KEY_SECRET": ""}, []string{"FLOWA_KEY_SECRET", "not set", "openssl rand -hex 32"}},
		{"a password for a secret", map[string]string{"FLOWA_KEY_SECRET": "flowa-test-only"}, []string{"FLOWA_KEY_SECRET", "wrong length"}},
		{"a secret that is not hex", map[string]string{"FLOWA_KEY_SECRET": strings.Repeat("secretzz", 8)}, []string{"FLOWA_KEY_SECRET", "hexadecimal"}},
		{"a secret of zeros", map[string]string{"FLOWA_KEY_SECRET": strings.Repeat("0", 64)}, []string{"FLOWA_KEY_SECRET", "not random"}},
	} {
		e := newEnv(t)
		err := e.fedStart(c.env)
		if err == nil {
			t.Errorf("%s: the start went ahead", c.name)
			continue
		}
		for _, w := range append(c.says, "FLOWA_MODE=federated") {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: %q does not say %q", c.name, err, w)
			}
		}
		if v := c.env["FLOWA_KEY_SECRET"]; v != "" && strings.Contains(err.Error(), v[:8]) {
			t.Errorf("%s: the message shows the secret: %q", c.name, err)
		}
		if n := e.count(`SELECT count(*) FROM users`) + e.count(`SELECT count(*) FROM ap_keys`) + e.count(`SELECT count(*) FROM instance_settings`) + e.count(`SELECT count(*) FROM catalog_revisions`); n != 0 || e.a.fed != nil {
			t.Errorf("%s: a refused start wrote %d rows, or made the federation state", c.name, n)
		}
	}

	e := newEnv(t)
	e.a.hosts = nil
	if err := e.fedStart(map[string]string{"FLOWA_HOSTS": "flowa.lan", "FLOWA_ORIGIN": "HTTPS://Flowa.Example.ORG:443/"}); err != nil {
		t.Fatal(err)
	}
	if e.a.mode != modeFederated || e.a.fed == nil || e.a.fed.origin.url != "https://flowa.example.org" {
		t.Fatalf("mode %q, federation %v", e.a.mode, e.a.fed)
	}
	if !reflect.DeepEqual(e.a.hosts, []string{"flowa.lan", "flowa.example.org"}) {
		t.Errorf("the names answered to are %v, want FLOWA_HOSTS and the origin's host", e.a.hosts)
	}
	waitFor(t, "the instance actor's key", func() bool { return e.count(`SELECT count(*) FROM ap_keys`) == 1 })
	if !e.a.federating() {
		t.Errorf("a federated start with good settings does not federate: %q", e.a.fed.refusal())
	}
	for host, want := range map[string]int{"flowa.example.org": 200, "FLOWA.example.org:443": 200, "flowa.lan": 200, "flowa.example.net": http.StatusMisdirectedRequest} {
		r := httptest.NewRequest("GET", "/api/me", nil)
		r.Host = host
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("a request for %s answers %d, want %d", host, w.Code, want)
		}
	}
	// What is new is what testdata/routes-federated.txt lists and nothing else: the routes are those of local mode and those.
	got := slices.Sorted(slices.Values(e.a.table(staticOf(t, nil)).patterns))
	if want := slices.Sorted(slices.Values(slices.Concat(routesFile(t, "testdata/routes-local.txt"), routesFile(t, "testdata/routes-federated.txt")))); !slices.Equal(got, want) {
		t.Errorf("federated mode serves other routes than local mode and routes-federated.txt:\n%v\n%v", got, want)
	}
	if n := strings.Count(logs.String(), fedBuiltLog); n != 1 {
		t.Errorf("the start says what federation does %d times, want once", n)
	}
	// No answer, no audit row and no log line holds the secret, the private key or its sealed form.
	// The log captured here is what log.Printf writes. main's log.Fatal paths print only the errors of
	// parseMode, parseFederation, start, openDB and rotateCommand; the refused starts above, TestKeySecret
	// and TestDBKeysGenerated assert that those errors do not hold the secret.
	var pub, sealed string
	if err := e.a.db.QueryRow(`SELECT public_key, private_sealed FROM ap_keys`).Scan(&pub, &sealed); err != nil {
		t.Fatal(err)
	}
	k, _, err := e.a.fed.privateKey(context.Background(), instanceActor)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	leaks := []string{testSecret, strings.ToUpper(testSecret), sealed, sealed[3:40], base64.StdEncoding.EncodeToString(der)[:64], privPEM[30:90], "PRIVATE KEY", k.D.String()[:40]}
	_, admin := e.user("boss", "admin")
	var answers strings.Builder
	for _, p := range []string{"/", "/healthz", "/api/me", "/api/admin/dashboard", "/api/admin/instance", "/api/instance", "/api/admin/audit", "/api/admin/users", "/api/about", "/actor", "/.well-known/webfinger?resource=acct:root@flowa.example.org"} {
		for _, remote := range []string{"192.0.2.1:1234", "127.0.0.1:1234"} {
			r := httptest.NewRequest("GET", p, nil)
			r.RemoteAddr = remote
			r.Host = "flowa.example.org"
			r.AddCookie(&http.Cookie{Name: cookieName, Value: admin})
			w := httptest.NewRecorder()
			e.h.ServeHTTP(w, r)
			fmt.Fprintf(&answers, "%s %d %s %v\n", p, w.Code, w.Body.String(), w.Result().Header)
		}
	}
	var audit string
	if err := e.a.db.QueryRow(`SELECT COALESCE(string_agg(concat_ws('|', action, target_kind, reason, detail::text), ','), '') FROM mod_actions`).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	var settings string
	if err := e.a.db.QueryRow(`SELECT COALESCE(string_agg(key || '=' || value::text, ','), '') FROM instance_settings`).Scan(&settings); err != nil {
		t.Fatal(err)
	}
	for where, text := range map[string]string{"an answer": answers.String(), "the audit log": audit, "the log": logs.String(), "instance_settings": settings, "the public key column": pub} {
		for _, l := range leaks {
			if strings.Contains(text, l) {
				t.Errorf("%s holds %.12q...", where, l)
			}
		}
	}
	if !strings.Contains(answers.String(), `"mode":"federated"`) {
		t.Errorf("the dashboard does not say the mode:\n%.400s", answers.String())
	}
}

// Keys: none in local or single mode; in a mode that federates the instance actor's key is made in the
// background, once, by one worker, without a database connection held while it is made; two makers at
// once leave one key that both read; a key that does not open is never replaced by a start; and
// -rotate-keys replaces every key, one at a time.
func TestDBKeysGenerated(t *testing.T) {
	logs := logged(t)
	made := countKeys(t)
	ctx := context.Background()
	settings, err := parseFederation(modeFederated, fedEnv(nil))
	if err != nil {
		t.Fatal(err)
	}

	// Local and single mode, with both settings present: no federation state, no key, no row.
	l := newEnv(t)
	if err := l.a.start(fedEnv(map[string]string{"FLOWA_MODE": "local"})); err != nil || l.a.fed != nil || l.a.federating() {
		t.Fatalf("a local start: %v, federation %v", err, l.a.fed)
	}
	s := newEnv(t)
	if err := s.singleStart(map[string]string{"ADMIN_USER": "root", "ADMIN_PASSWORD": "flowa-test-only", "FLOWA_ORIGIN": "https://flowa.example.org", "FLOWA_KEY_SECRET": testSecret}, staticOf(t, nil)); err != nil || s.a.fed != nil || s.a.federating() {
		t.Fatalf("a single start: %v, federation %v", err, s.a.fed)
	}
	for _, x := range []*tenv{l, s} {
		if n := x.count(`SELECT count(*) FROM ap_keys`) + x.count(`SELECT count(*) FROM instance_settings`); n != 0 {
			t.Errorf("%s mode wrote %d rows of keys or settings", x.a.mode, n)
		}
	}
	if made.Load() != 0 {
		t.Fatalf("%d keys were made in local and single mode", made.Load())
	}

	// Federated: the instance actor's key, in the background.
	e := newEnv(t)
	if err := e.fedStart(nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the instance actor's key", func() bool { return e.count(`SELECT count(*) FROM ap_keys`) == 1 })
	var keyID, pub, sealed string
	var owner sql.NullInt64
	if err := e.a.db.QueryRow(`SELECT key_id, user_id, public_key, private_sealed FROM ap_keys`).Scan(&keyID, &owner, &pub, &sealed); err != nil {
		t.Fatal(err)
	}
	if keyID != "/actor#main-key" || owner.Valid || !strings.HasPrefix(sealed, "v1.") || strings.Contains(sealed, "PRIVATE") {
		t.Errorf("the instance actor's row: %q, owner %v, sealed %.10s", keyID, owner, sealed)
	}
	block, rest := pem.Decode([]byte(pub))
	if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatalf("the public key is not one SPKI PEM: %q", pub)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	rpub, isRSA := parsed.(*rsa.PublicKey)
	if err != nil || !isRSA || rpub.N.BitLen() != 2048 {
		t.Fatalf("the public key: %T %v", parsed, err)
	}
	k, id, err := e.a.fed.privateKey(ctx, instanceActor)
	if err != nil || id != "/actor#main-key" || !k.PublicKey.Equal(rpub) {
		t.Fatalf("the private key does not open or match: %v", err)
	}
	if k2, _, _ := e.a.fed.privateKey(ctx, instanceActor); k2 != k {
		t.Error("the second read of a key is not the one kept in memory")
	}
	waitFor(t, "the log line of the key", func() bool { return strings.Contains(logs.String(), "keys: made the key of the instance actor in ") })
	if made.Load() != 1 {
		t.Errorf("%d keys made for the instance actor, want 1", made.Load())
	}

	// A restart finds the key and makes none.
	r := e.again()
	if err := r.fedStart(nil); err != nil || !r.a.federating() {
		t.Fatalf("a restart: %v, %q", err, r.a.fed.refusal())
	}
	if made.Load() != 1 || e.count(`SELECT count(*) FROM ap_keys`) != 1 {
		t.Errorf("a restart made a key: %d made", made.Load())
	}

	// Two goroutines ask for one member's key at once: one key, made by one of them, read by both.
	anaID, _ := e.user("ana", "user")
	f := newFederation(e.a.db, settings)
	results := make(chan bool, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			ok, err := f.ensureKey(ctx, keyOwner(anaID))
			if err != nil {
				t.Error(err)
			}
			results <- ok
		})
	}
	wg.Wait()
	if a, b := <-results, <-results; a == b {
		t.Errorf("two makers at once: made %v and %v, want exactly one", a, b)
	}
	if n := e.count(`SELECT count(*) FROM ap_keys WHERE user_id = $1 AND key_id = '/users/ana#main-key'`, anaID); n != 1 {
		t.Errorf("ana has %d keys", n)
	}

	// Two servers, each making a key before either writes it: the unique index keeps one, both read it.
	boID, _ := e.user("bo", "user")
	var both sync.WaitGroup
	both.Add(2)
	race := func() (*rsa.PrivateKey, error) { k, err := generateKey(); both.Done(); both.Wait(); return k, err }
	f1, f2 := newFederation(e.a.db, settings), newFederation(e.a.db, settings)
	f1.gen, f2.gen = race, race
	var m1, m2 bool
	var e1, e2 error
	wg.Go(func() { m1, e1 = f1.ensureKey(ctx, keyOwner(boID)) })
	wg.Go(func() { m2, e2 = f2.ensureKey(ctx, keyOwner(boID)) })
	wg.Wait()
	if e1 != nil || e2 != nil || m1 == m2 {
		t.Fatalf("two servers: made %v (%v) and %v (%v), want exactly one", m1, e1, m2, e2)
	}
	k1, _, err1 := f1.privateKey(ctx, keyOwner(boID))
	k2, _, err2 := f2.privateKey(ctx, keyOwner(boID))
	if err1 != nil || err2 != nil || !k1.Equal(k2) {
		t.Errorf("the two servers read different keys: %v, %v", err1, err2)
	}
	// The same for the instance actor, whose owner is NULL: still one key.
	fresh := newEnv(t)
	both.Add(2)
	i1, i2 := newFederation(fresh.a.db, settings), newFederation(fresh.a.db, settings)
	i1.gen, i2.gen = race, race
	wg.Go(func() { m1, e1 = i1.ensureKey(ctx, instanceActor) })
	wg.Go(func() { m2, e2 = i2.ensureKey(ctx, instanceActor) })
	wg.Wait()
	if n := fresh.count(`SELECT count(*) FROM ap_keys WHERE user_id IS NULL`); e1 != nil || e2 != nil || m1 == m2 || n != 1 {
		t.Errorf("two servers making the instance actor's key: made %v (%v) and %v (%v), %d keys; want exactly one", m1, e1, m2, e2, n)
	}

	// The worker makes keys with no connection held: with one connection in the pool, the database
	// answers while a key is being made.
	// Asked for three keys at once, it makes them one at a time.
	cyID, _ := e.user("cy", "user")
	cy2, _ := e.user("cy2", "user")
	cy3, _ := e.user("cy3", "user")
	e.a.db.SetMaxOpenConns(1)
	held := make(chan string, 16)
	var active, most atomic.Int32
	w := newFederation(e.a.db, settings)
	w.gen = func() (*rsa.PrivateKey, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
		}
		if n := e.a.db.Stats().InUse; n != 0 {
			held <- fmt.Sprintf("%d connections in use while a key is made", n)
		}
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		var one int
		if err := e.a.db.QueryRowContext(c, `SELECT 1`).Scan(&one); err != nil {
			held <- "the database did not answer while a key was made: " + err.Error()
		}
		return generateKey()
	}
	// The worker says when it is done with each request (handled): the test waits for that, not by asking
	// the database, because a question of its own on the one connection while a key is being made would be
	// counted above as a connection the worker holds.
	finished := make(chan keyOwner, 3)
	w.handled = func(o keyOwner, _ bool, _ error) { finished <- o }
	for _, id := range []int64{cyID, cy2, cy3} {
		if !w.requestKey(keyOwner(id)) {
			t.Fatal("a request for a key was dropped with the queue nearly empty")
		}
	}
	for range 3 {
		select {
		case <-finished:
		case <-time.After(30 * time.Second):
			t.Fatal("waited 30 s for the worker to finish three requests")
		}
	}
	if n := e.count(`SELECT count(*) FROM ap_keys WHERE user_id IN ($1, $2, $3)`, cyID, cy2, cy3); n != 3 {
		t.Fatalf("%d of the three keys were made", n)
	}
	if most.Load() != 1 {
		t.Errorf("%d keys were made at once, want one at a time", most.Load())
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := w.stop(c); err != nil {
		t.Fatal(err)
	}
	e.a.db.SetMaxOpenConns(8)
	close(held)
	for msg := range held {
		t.Error(msg)
	}

	// stop waits for a key being made, until its context ends, and a key made after stop is not written.
	danID, _ := e.user("dan", "user")
	entered, release := make(chan struct{}), make(chan struct{})
	h := newFederation(e.a.db, settings)
	h.gen = func() (*rsa.PrivateKey, error) { close(entered); <-release; return generateKey() }
	h.requestKey(keyOwner(danID))
	<-entered
	short, cancelShort := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelShort()
	if err := h.stop(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("stop with a key being made returned %v before the key was done", err)
	}
	close(release)
	if err := h.stop(c); err != nil {
		t.Errorf("stop after the key was done: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM ap_keys WHERE user_id = $1`, danID); n != 0 {
		t.Errorf("a key made after stop was written")
	}

	// A deleted account takes its key with it.
	if _, err := e.a.db.Exec(`DELETE FROM users WHERE id = $1`, anaID); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM ap_keys WHERE key_id = '/users/ana#main-key'`); n != 0 {
		t.Error("a deleted account's key is still there")
	}

	// The wrong secret: the start says so once, federates nothing, makes no key over the one it cannot
	// open, and a request for the key makes none either.
	before, rows := made.Load(), e.count(`SELECT count(*) FROM ap_keys`)
	x := e.again()
	if err := x.fedStart(map[string]string{"FLOWA_KEY_SECRET": testSecret2}); err != nil {
		t.Fatal(err)
	}
	if x.a.federating() || !strings.Contains(x.a.fed.refusal(), "FLOWA_KEY_SECRET does not open the keys stored here") || !strings.Contains(x.a.fed.refusal(), "-rotate-keys") {
		t.Errorf("a start with the wrong secret: federating %v, %q", x.a.federating(), x.a.fed.refusal())
	}
	if _, _, err := x.a.fed.privateKey(ctx, instanceActor); !errors.Is(err, errOpenKey) || strings.Contains(err.Error(), testSecret2) {
		t.Errorf("opening with the wrong secret: %v", err)
	}
	refusesKeys(t, e, x.a.fed, "eve")
	var after string
	if err := e.a.db.QueryRow(`SELECT private_sealed FROM ap_keys WHERE user_id IS NULL`).Scan(&after); err != nil || after != sealed {
		t.Errorf("the instance actor's key was replaced by a start with the wrong secret (%v)", err)
	}
	if made.Load() != before || e.count(`SELECT count(*) FROM ap_keys`) != rows {
		t.Errorf("the wrong secret made %d keys", made.Load()-before)
	}
	if strings.Count(logs.String(), "FLOWA_KEY_SECRET does not open") != 1 {
		t.Errorf("the wrong secret is said %d times in the log, want once", strings.Count(logs.String(), "FLOWA_KEY_SECRET does not open"))
	}

	// -rotate-keys: only in a mode that federates and with the settings; then every key is new, one at a
	// time, and the cache drops the old ones. After a lost secret it is the way back.
	var out strings.Builder
	if err := rotateWith(e.a.db, modeLocal, fedEnv(nil), &out); err == nil || !strings.Contains(err.Error(), "does not federate") {
		t.Errorf("rotating in local mode: %v", err)
	}
	if err := rotateWith(e.a.db, modeFederated, fedEnv(map[string]string{"FLOWA_KEY_SECRET": ""}), &out); err == nil || !strings.Contains(err.Error(), "FLOWA_KEY_SECRET") {
		t.Errorf("rotating without the secret: %v", err)
	}
	if err := rotateCommand(fedEnv(map[string]string{"DATABASE_URL": ""}), &out); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("-rotate-keys without a database: %v", err)
	}
	oldPubs := map[string]string{}
	pubRows, err := e.a.db.Query(`SELECT key_id, public_key FROM ap_keys`)
	if err != nil {
		t.Fatal(err)
	}
	for pubRows.Next() {
		var id, p string
		pubRows.Scan(&id, &p)
		oldPubs[id] = p
	}
	pubRows.Close()
	before = made.Load()
	if err := rotateWith(e.a.db, modeFederated, fedEnv(map[string]string{"FLOWA_KEY_SECRET": testSecret2}), &out); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("rotated %d keys.", len(oldPubs)); !strings.Contains(out.String(), want) || !strings.Contains(out.String(), "Update") || !strings.Contains(out.String(), "Restart") {
		t.Errorf("-rotate-keys says %q, want %q and what it does not do yet", out.String(), want)
	}
	if got := int(made.Load() - before); got != len(oldPubs) {
		t.Errorf("rotation made %d keys for %d owners", got, len(oldPubs))
	}
	for id, p := range oldPubs {
		var now string
		e.a.db.QueryRow(`SELECT public_key FROM ap_keys WHERE key_id = $1`, id).Scan(&now)
		if now == p || now == "" {
			t.Errorf("%s: the key is the same after rotating", id)
		}
	}
	// A start with the secret the keys are now sealed with federates again, with a new key.
	y := e.again()
	if err := y.fedStart(map[string]string{"FLOWA_KEY_SECRET": testSecret2}); err != nil || !y.a.federating() {
		t.Errorf("a start after rotating with the new secret: %v, %q", err, y.a.fed.refusal())
	}
	if ny, _, err := y.a.fed.privateKey(ctx, instanceActor); err != nil || ny.Equal(k) {
		t.Errorf("after rotating, the instance actor's key: %v, or the same as before", err)
	}
	if _, _, err := newFederation(e.a.db, settings).privateKey(ctx, instanceActor); !errors.Is(err, errOpenKey) {
		t.Errorf("the old secret still opens a rotated key: %v", err)
	}
	// Rotation drops what a federation kept in memory.
	g := newFederation(e.a.db, fedSettings{origin: settings.origin, secret: mustSecret(t, testSecret2)})
	g1, _, err := g.privateKey(ctx, instanceActor)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.rotateKeys(ctx); err != nil {
		t.Fatal(err)
	}
	if g2, _, err := g.privateKey(ctx, instanceActor); err != nil || g2.Equal(g1) {
		t.Errorf("after rotating, the key kept in memory is still served (%v)", err)
	}
	// A key another rotation changes while this one runs is skipped, counted, said, and the command
	// fails (main exits non-zero), so the operator knows not every key is new.
	n := e.count(`SELECT count(*) FROM ap_keys`)
	was := generateKey
	var meddled atomic.Bool
	generateKey = func() (*rsa.PrivateKey, error) {
		if !meddled.Swap(true) { // the first key made is the instance actor's (NULL owner first)
			if _, err := e.a.db.Exec(`UPDATE ap_keys SET public_key = 'changed by another rotation' WHERE user_id IS NULL`); err != nil {
				t.Error(err)
			}
		}
		return was()
	}
	out.Reset()
	err = rotateWith(e.a.db, modeFederated, fedEnv(map[string]string{"FLOWA_KEY_SECRET": testSecret2}), &out)
	generateKey = was
	if want := fmt.Sprintf("rotated %d keys and skipped 1 that changed or went", n-1); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("a rotation that met another: %v, want an error saying %q", err, want)
	}
	if out.String() != "" {
		t.Errorf("a rotation that skipped a key still said it was done: %q", out.String())
	}
	if p := e.count(`SELECT count(*) FROM ap_keys WHERE public_key = 'changed by another rotation'`); p != 1 {
		t.Errorf("the key changed meanwhile was overwritten (%d left)", p)
	}

	// No log line holds a secret or a private key.
	for _, l := range []string{testSecret, testSecret2, "PRIVATE KEY", sealed[3:40], k.D.String()[:40]} {
		if strings.Contains(logs.String(), l) {
			t.Errorf("the log holds %.12q...", l)
		}
	}
}

// serve waits for the key worker as it does for a request in flight, and gives up at shutdownWait when
// the work does not end.
func TestDBServeStopsWorker(t *testing.T) {
	logs := logged(t)
	e := newEnv(t)
	settings, err := parseFederation(modeFederated, fedEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	// start serves on a fresh port with a federation whose next key is held, asks for that key, and
	// returns once the worker is making it.
	start := func(gen func() (*rsa.PrivateKey, error)) (cancel func(), served chan error) {
		t.Helper()
		f := newFederation(e.a.db, settings)
		entered := make(chan struct{})
		f.gen = func() (*rsa.PrivateKey, error) { close(entered); return gen() }
		a := newApp(e.a.db, t.TempDir())
		a.fed = f
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		served = make(chan error, 1)
		go func() { served <- a.serve(ctx, &http.Server{Handler: http.NotFoundHandler()}, ln, func() {}) }()
		f.requestKey(instanceActor)
		<-entered
		t.Cleanup(func() { cancel(); f.cancel(); f.wg.Wait() })
		return cancel, served
	}

	// A key being made when the stop comes is waited for.
	held := make(chan struct{})
	release := sync.OnceFunc(func() { close(held) })
	cancel, served := start(func() (*rsa.PrivateKey, error) { <-held; return generateKey() })
	t.Cleanup(release) // a failure below must not leave the worker held while the cleanup waits for it
	cancel()
	select {
	case err := <-served:
		t.Fatalf("serve returned (%v) while the key worker was still making a key", err)
	case <-time.After(300 * time.Millisecond): // fixed: showing that serve does not return early
	}
	release()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("serve: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after the key was made")
	}

	// Work that never ends is given up at the bound, shortened here from 5 s.
	was := shutdownWait
	shutdownWait = 400 * time.Millisecond
	t.Cleanup(func() { shutdownWait = was })
	never := make(chan struct{})
	cancel, served = start(func() (*rsa.PrivateKey, error) { <-never; return generateKey() })
	t.Cleanup(func() { close(never) }) // registered after start's, so it runs before the worker is waited for
	began := time.Now()
	cancel()
	select {
	case err := <-served:
		if took := time.Since(began); err != nil || took < 350*time.Millisecond || took > 3*time.Second {
			t.Errorf("serve with a worker that never ends returned %v after %s, want about %s", err, took, shutdownWait)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not give up at the bound")
	}
	if !strings.Contains(logs.String(), "the key worker did not stop in time") {
		t.Error("giving up on the worker is not logged")
	}
}

// The origin is frozen when it is first announced, and not before (docs/federation.md 4.3): starting in
// a mode that federates and making the keys records nothing, so a FLOWA_ORIGIN changed before any actor id
// went out is simply taken; NodeInfo, which names the origin but no actor id, announces nothing, and nor
// does the instance actor's WebFinger answer (a scanner or the operator's own check); the first WebFinger
// answer that names a member records the origin and the time once, however many arrive at once.
// From then on the same origin written another way starts as before, and another origin starts the app,
// says why once, and federates nothing. Two first records at once: one wins, both agree.
func TestDBOriginFrozen(t *testing.T) {
	logs := logged(t)
	ctx := context.Background()
	e := newEnv(t)
	if err := e.a.start(fedEnv(map[string]string{"FLOWA_MODE": "local"})); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM instance_settings`); n != 0 {
		t.Fatalf("local mode recorded %d settings", n)
	}
	recorded := func() int {
		return e.count(`SELECT count(*) FROM instance_settings WHERE key IN ('origin', 'first_federated_at')`)
	}
	actorWF := "/.well-known/webfinger?resource=" + url.QueryEscape("acct:flowa.example.org@flowa.example.org")
	memberWF := "/.well-known/webfinger?resource=" + url.QueryEscape("acct:ana@flowa.example.org")
	anaID, ana := e.user("ana", "user")
	if _, err := e.a.db.Exec(`UPDATE users SET federates = true WHERE id = $1`, anaID); err != nil {
		t.Fatal(err)
	}

	// The first start makes the instance actor's key and records nothing.
	r := e.again()
	if err := r.fedStart(nil); err != nil {
		t.Fatal(err)
	}
	// The start also asks for ana's key (she opted in), right after the instance actor's, so the number of
	// keys passes 1 on its way to 2: the wait is for the instance actor's own key, which stays once made.
	waitFor(t, "the instance actor's key", func() bool { return e.count(`SELECT count(*) FROM ap_keys WHERE user_id IS NULL`) == 1 })
	if n := recorded(); n != 0 {
		t.Fatalf("making the key recorded %d rows of the origin", n)
	}
	if !strings.Contains(logs.String(), "federation: the origin https://flowa.example.org is not announced yet") {
		t.Error("the start does not say the origin is not announced yet")
	}

	// Before it is announced, another FLOWA_ORIGIN is taken: it federates, its key opens (a key holds no
	// origin), and NodeInfo's discovery document, which names the origin, announces nothing.
	n := e.again()
	if err := n.fedStart(map[string]string{"FLOWA_ORIGIN": "https://flowa.example.net"}); err != nil || !n.a.federating() {
		t.Fatalf("another origin before any announcement: %v, %q", err, n.a.fed.refusal())
	}
	if w := ask(n.h, "GET", "/.well-known/nodeinfo", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "https://flowa.example.net/nodeinfo/2.1") {
		t.Errorf("NodeInfo under the new origin: %d %s", w.Code, w.Body)
	}
	if w := ask(n.h, "GET", "/nodeinfo/2.1", "", nil); w.Code != 200 {
		t.Errorf("/nodeinfo/2.1: %d", w.Code)
	}
	if w := ask(n.h, "GET", "/.well-known/webfinger?resource=acct:nobody@flowa.example.net", "", nil); w.Code != 404 {
		t.Errorf("an unknown name: %d", w.Code)
	}
	if w := ask(n.h, "GET", "/.well-known/webfinger?resource=acct:flowa.example.net@flowa.example.net", "", nil); w.Code != 200 {
		t.Errorf("the instance actor: %d", w.Code)
	}
	if c := recorded(); c != 0 {
		t.Fatalf("NodeInfo, a 404 or the instance actor's answer recorded the origin (%d rows)", c)
	}
	_, admin := e.user("boss", "admin")
	if _, out := n.do(admin, "GET", "/api/admin/dashboard", nil); out["federation"].(map[string]any)["announced"] != "" {
		t.Errorf("the dashboard before any announcement: %v", out["federation"])
	}

	// Back to the first origin, and the first answers that name an actor, eight at once: each is answered,
	// and the origin is recorded once.
	g := e.again()
	if err := g.fedStart(nil); err != nil || !g.a.federating() {
		t.Fatalf("the first origin again: %v", err)
	}
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Go(func() { codes[i] = ask(g.h, "GET", memberWF, fmt.Sprintf("198.51.100.%d:1000", i+1), nil).Code })
	}
	wg.Wait()
	if slices.ContainsFunc(codes, func(c int) bool { return c != 200 }) || recorded() != 2 {
		t.Fatalf("eight first answers at once: %v, %d rows", codes, recorded())
	}
	was, err := recordedOrigin(e.a.db)
	if err != nil || was != "https://flowa.example.org" {
		t.Fatalf("recorded %q, %v", was, err)
	}
	var first string
	if err := e.a.db.QueryRow(`SELECT value #>> '{}' FROM instance_settings WHERE key = 'first_federated_at'`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if at, err := time.Parse("2006-01-02T15:04:05.999999-07:00", first); err != nil || time.Since(at) > time.Minute {
		t.Errorf("first_federated_at is %q (%v)", first, err)
	}
	if _, out := g.do(admin, "GET", "/api/admin/dashboard", nil); out["federation"].(map[string]any)["announced"] != first {
		t.Errorf("the dashboard after the announcement: %v, want announced %s", out["federation"], first)
	}
	// The instance's about page and its editor read instance_settings too: these keys change neither.
	if code, out := g.do(admin, "GET", "/api/admin/instance", nil); code != 200 || out["updatedAt"] != "" {
		t.Errorf("the recorded origin shows in the instance editor: %d %v", code, out["_raw"])
	}

	for _, same := range []string{"https://flowa.example.org", "HTTPS://FLOWA.EXAMPLE.ORG", "https://flowa.example.org:443/", " https://Flowa.Example.Org/\n"} {
		s := e.again()
		if err := s.fedStart(map[string]string{"FLOWA_ORIGIN": same}); err != nil || !s.a.federating() {
			t.Errorf("FLOWA_ORIGIN=%q after https://flowa.example.org: %v, %q", same, err, s.a.fed.refusal())
		}
	}
	for i, moved := range []string{"https://flowa.example.net", "https://flowa.example.org:8443", "https://www.flowa.example.org"} {
		m := e.again()
		if err := m.fedStart(map[string]string{"FLOWA_ORIGIN": moved}); err != nil {
			t.Fatalf("a moved origin stopped the start: %v", err)
		}
		if i == 0 { // and no key is made for anyone under it
			refusesKeys(t, e, m.a.fed, "moved")
		}
		want := "federation refused: the origin was https://flowa.example.org and is now " + moved + "; actor ids cannot move"
		if m.a.federating() || m.a.fed.refusal() != want {
			t.Errorf("FLOWA_ORIGIN=%s: federating %v, %q", moved, m.a.federating(), m.a.fed.refusal())
		}
		if strings.Count(logs.String(), want) != 1 {
			t.Errorf("FLOWA_ORIGIN=%s: the log says it %d times, want once", moved, strings.Count(logs.String(), want))
		}
		// Nothing is announced under it: discovery answers 404, and Settings says it is unavailable, with the
		// reason for staff only.
		for _, p := range []string{actorWF, memberWF} {
			if w := ask(m.h, "GET", p, "", nil); w.Code != 404 {
				t.Errorf("FLOWA_ORIGIN=%s: WebFinger answers %d", moved, w.Code)
			}
		}
		_, mo := m.do(ana, "GET", "/api/me", nil)
		_, so := m.do(admin, "GET", "/api/me", nil)
		mf, _ := mo["federation"].(map[string]any)
		sf, _ := so["federation"].(map[string]any)
		if mf["unavailable"] != fedUnavailable || mf["refused"] != nil || sf["unavailable"] != fedUnavailable || sf["refused"] != want {
			t.Errorf("FLOWA_ORIGIN=%s: Settings says to a member %v, to an admin %v", moved, mf, sf)
		}
		// The app is served as in local mode, under the name the operator now gives it.
		host := strings.TrimPrefix(strings.Split(moved, ":")[1], "//")
		req := httptest.NewRequest("GET", "/api/me", nil)
		req.Host = host
		req.AddCookie(&http.Cookie{Name: cookieName, Value: admin})
		w := httptest.NewRecorder()
		m.h.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Errorf("FLOWA_ORIGIN=%s: the app at %s answers %d", moved, host, w.Code)
		}
	}
	if now, _ := recordedOrigin(e.a.db); now != was {
		t.Errorf("the recorded origin changed to %q", now)
	}

	// A server whose origin was recorded differently after it started (only by another process on the
	// same database, which a deploy does not do; the old and the new container would have to overlap with
	// two origins) finds out at its first answer: it refuses then, answers 404, and the record stays.
	fresh := newEnv(t)
	fid, _ := fresh.user("ana", "user")
	fresh.a.db.Exec(`UPDATE users SET federates = true WHERE id = $1`, fid)
	if err := fresh.fedStart(map[string]string{"FLOWA_ORIGIN": "https://other.example.net"}); err != nil || !fresh.a.federating() {
		t.Fatal(err)
	}
	if _, err := fresh.a.db.Exec(`INSERT INTO instance_settings (key, value) VALUES ('origin', '"https://flowa.example.org"')`); err != nil {
		t.Fatal(err)
	}
	if w := ask(fresh.h, "GET", "/.well-known/webfinger?resource=acct:ana@other.example.net", "", nil); w.Code != 404 {
		t.Errorf("the first answer under a recorded other origin: %d", w.Code)
	}
	if !strings.Contains(fresh.a.fed.refusal(), "the origin was https://flowa.example.org and is now https://other.example.net") || fresh.count(`SELECT count(*) FROM instance_settings WHERE key = 'first_federated_at'`) != 0 {
		t.Errorf("under a recorded other origin: %q", fresh.a.fed.refusal())
	}

	// Two first records at once, of the same origin and of two origins: one wins, both agree.
	race := func(origins ...string) []error {
		t.Helper()
		x := newEnv(t)
		var ready, wg sync.WaitGroup
		ready.Add(len(origins))
		errs := make([]error, len(origins))
		for i, o := range origins {
			po, err := parseOrigin(o)
			if err != nil {
				t.Fatal(err)
			}
			wg.Go(func() {
				tx, err := x.a.db.BeginTx(ctx, nil)
				if err != nil {
					errs[i] = err
					ready.Done()
					return
				}
				defer tx.Rollback()
				ready.Done()
				ready.Wait()
				if errs[i] = recordOrigin(ctx, tx, po); errs[i] == nil {
					errs[i] = tx.Commit()
				}
			})
		}
		wg.Wait()
		if n := x.count(`SELECT count(*) FROM instance_settings WHERE key = 'origin'`); n != 1 {
			t.Errorf("%d origins recorded", n)
		}
		return errs
	}
	if errs := race("https://flowa.example.org", "https://FLOWA.example.org:443"); errs[0] != nil || errs[1] != nil {
		t.Errorf("two records of one origin: %v", errs)
	}
	errs := race("https://flowa.example.org", "https://flowa.example.net")
	var moved originMoved
	if (errs[0] == nil) == (errs[1] == nil) || !(errors.As(errs[0], &moved) || errors.As(errs[1], &moved)) {
		t.Errorf("two records of two origins: %v, want one recorded and the other refused", errs)
	}
}
