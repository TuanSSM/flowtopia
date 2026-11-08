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
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
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
	a := &app{db: db, dataDir: t.TempDir(), fails: map[string]*failCount{}}
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
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rd)
	r.Header.Set("Content-Type", "application/json")
	if tok != "" {
		r.AddCookie(&http.Cookie{Name: cookieName, Value: tok})
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	out := map[string]any{}
	json.Unmarshal(w.Body.Bytes(), &out)
	out["_raw"] = w.Body.String()
	return w.Code, out
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
	// Off by default: the endpoints do not exist.
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
	out := e.must(200, b, "GET", "/api/admin/dashboard", nil)
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
