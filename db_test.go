package main

// Tests against a real Postgres, through the real handlers. They run when TEST_DATABASE_URL
// points at a database that may be written to (CI's integration job sets it); otherwise they
// are skipped. Each test gets its own schema, so tests never see each other's rows.

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
	return &tenv{t: t, a: a, h: a.routes(map[string]asset{})}
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
	// A second admin can be demoted; the last one cannot be deleted.
	cID, _ := e.user("cal", "admin")
	e.must(200, admin, "PUT", fmt.Sprintf("/api/admin/users/%d/role", cID), map[string]any{"role": "user"})
	if n := e.count(`SELECT count(*) FROM mod_actions WHERE action = 'role'`); n != 2 {
		t.Fatalf("role changes logged %d times, want 2", n)
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
	act(mod, 200, map[string]any{"action": "hide_stats", "id": ownerID})
	people := e.must(200, other, "GET", "/api/users", nil)["users"].([]any)
	if top := people[0].(map[string]any); top["username"] == "owner" {
		t.Fatal("hidden stats still rank first")
	}
	// Every action above was logged once.
	for action, want := range map[string]int{"hide": 2, "restore": 1, "resolve": 1, "mute": 1, "unmute": 1, "suspend": 1, "revoke_badge": 1, "hide_stats": 1} {
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
	e.h = e.a.routes(map[string]asset{"/index.html": {body: []byte("<!doctype html>"), ctype: "text/html", etag: `"1"`}})
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
	// bea changes her mind; then withdraws (her browser sends the reverse change).
	e.must(200, b, "POST", "/api/suggest/labels", map[string]any{"prop": "nunchaku", "deltas": []map[string]any{{"a": "F5", "b": "F7", "from": "works", "to": "blocked"}}})
	got := e.must(200, c, "GET", "/api/suggest/counts?prop=nunchaku", nil)["counts"].([]any)
	if len(got) != 1 || num(got[0].(map[string]any)["works"]) != 1 || num(got[0].(map[string]any)["blocked"]) != 1 {
		t.Fatalf("counts: %v", got)
	}
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
	_ = bID
	// Usage counters feed the dashboard.
	e.must(200, b, "POST", "/api/suggest/usage", map[string]any{"shown": 5, "added": 2, "worked": 1})
	e.must(200, c, "POST", "/api/suggest/usage", map[string]any{"shown": 3, "added": 1, "worked": 0})
	e.must(400, c, "POST", "/api/suggest/usage", map[string]any{"shown": -1})
	sug := e.must(200, admin, "GET", "/api/admin/dashboard", nil)["suggest"].(map[string]any)
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
	}
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
	e.setFlag("community", false)
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
	e.setFlag("game", true)
	badge["keys"] = []string{"sessions-10"}
	if got := e.must(200, una, "POST", "/api/achievements", badge); num(got["postId"]) == 0 {
		t.Fatalf("with both flags on a badge posts its milestone: %v", got["_raw"])
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
