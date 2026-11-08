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
	raw := make([]byte, 16)
	rand.Read(raw)
	tok := hex.EncodeToString(raw)
	if _, err := e.a.db.Exec(`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, now() + interval '1 day')`, tokenHash(tok), id); err != nil {
		e.t.Fatal(err)
	}
	return id, tok
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
