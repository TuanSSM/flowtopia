package main

// Replies and likes across servers (FWR 6, docs/federation.md 6.3, 6.4, 9.1, 11): remote replies to a Public post here
// kept as comments under it, with the forward of ActivityPub 7.1.2; remote likes and their Undo; a member's comments
// on Public posts going out as notes, with their edits and deletions; a member's likes and comments on public posts of
// other servers. Everything through the real inbox (signature, decoder, origin rule, queue) and its worker, and the
// delivery queue's tables, with no worker of its own and the test's clock (follows_test.go's world).

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// repliesEnv is notesInEnv with ana's Public post p, two followers of ana on other servers (cat on social.example, bo's
// server, and fan on third.example), and a follower of cy on fourth.example; cy shares nothing.
type repliesEnv struct {
	*notesInEnv
	post   int64
	postID string // its id as other servers know it
	catID  int64
}

func newRepliesEnv(t *testing.T) *repliesEnv {
	t.Helper()
	ne := newNotesInEnv(t)
	ne.allow("third.example", "fourth.example")
	e := &repliesEnv{notesInEnv: ne}
	e.catID = e.remoteID(catID)
	fan := e.remoteAccount("fan", "third.example")
	fan2 := e.remoteAccount("fan2", "fourth.example")
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $3, 'accepted'), ($2, $3, 'accepted'), ($4, $5, 'accepted')`, e.catID, fan, e.anaID, fan2, e.cyID)
	out := e.must(200, e.ana, "POST", "/api/posts", map[string]any{"body": "a figure 8 at last", "audience": "public"})
	e.post = num(out["posts"].([]any)[0].(map[string]any)["id"])
	e.mustExec(`UPDATE users SET federates = false WHERE id = $1`, e.cyID) // cy shares nothing here (notesInEnv makes cy share)
	e.postID = fmt.Sprintf("https://here.example/users/ana/posts/%d", e.post)
	return e
}

// remoteAccount makes an account of another server with its cached actor, its own inbox and its server's shared inbox.
func (e *repliesEnv) remoteAccount(name, host string) int64 {
	e.t.Helper()
	ap := "https://" + host + "/users/" + name
	var id int64
	if err := e.a.db.QueryRow(`INSERT INTO users (username, pw_hash, domain, display_name) VALUES ($1, '!', $2, $3) RETURNING id`, name+"@"+host, host, name).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	e.mustExec(`INSERT INTO ap_actors (user_id, ap_id, kind, preferred_username, inbox, shared_inbox, url, key_id, public_key, fetched_at, expires_at)
		VALUES ($1, $2, 'Person', $3, $4, $5, $6, $7, 'pem', now(), now() + interval '1 day')`, id, ap, name, ap+"/inbox", "https://"+host+"/inbox", "https://"+host+"/@"+name, ap+"#main-key")
	return id
}

// postAs makes a post through the API and returns its id.
func (e *repliesEnv) postAs(tok string, body map[string]any) int64 {
	e.t.Helper()
	return num(e.must(200, tok, "POST", "/api/posts", body)["posts"].([]any)[0].(map[string]any)["id"])
}

// reply is a Note of actor answering inReplyTo, public, cc the actor's followers and more.
func reply(actor string, n int, inReplyTo, content string, cc ...string) map[string]any {
	return map[string]any{"id": fmt.Sprintf("%s/statuses/%d", actor, n), "type": "Note", "attributedTo": actor, "inReplyTo": inReplyTo, "content": content,
		"to": []string{asPublic}, "cc": append([]string{actor + "/followers"}, cc...), "url": fmt.Sprintf("%s/r/%d", actor, n)}
}

// comment is the kept comment of a remote id: its id here, post, parent, body and whether it is deleted.
type keptComment struct {
	id, post, parent int64
	body             string
	deleted          bool
}

func (e *repliesEnv) kept(apID string) (keptComment, bool) {
	e.t.Helper()
	var c keptComment
	err := e.a.db.QueryRow(`SELECT id, post_id, COALESCE(parent_id, 0), body, deleted FROM comments WHERE ap_id = $1`, apID).Scan(&c.id, &c.post, &c.parent, &c.body, &c.deleted)
	if err != nil && !strings.Contains(err.Error(), "no rows") {
		e.t.Fatal(err)
	}
	return c, err == nil
}

// forwards are the activities the instance actor enqueued (forwards are the only ones it sends here), with their
// bodies and the inboxes their rows were written for.
func (e *repliesEnv) forwards() []outRow {
	e.t.Helper()
	rows, err := e.a.db.Query(`SELECT o.ap_id, o.body, COALESCE(array_agg(d.inbox ORDER BY d.inbox) FILTER (WHERE d.inbox IS NOT NULL), '{}') FROM ap_outbox o
		LEFT JOIN ap_deliveries d ON d.outbox_id = o.id WHERE o.actor_id IS NULL GROUP BY o.id ORDER BY o.id`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []outRow
	for rows.Next() {
		var r outRow
		var ins []string
		if err := rows.Scan(&r.id, &r.body, (*stringArray)(&ins)); err != nil {
			e.t.Fatal(err)
		}
		r.inboxes = ins
		out = append(out, r)
	}
	return out
}

// notes is the kinds of the notifications member uid has from remote accounts, each marked fed, oldest first.
func (e *repliesEnv) notes(uid int64) []string {
	e.t.Helper()
	rows, err := e.a.db.Query(`SELECT kind, fed FROM notifications WHERE user_id = $1 AND actor_id IN (SELECT id FROM users WHERE domain <> '') ORDER BY id`, uid)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		var fed bool
		if err := rows.Scan(&k, &fed); err != nil {
			e.t.Fatal(err)
		}
		if !fed {
			k += " (not marked fed)"
		}
		out = append(out, k)
	}
	return out
}

// thread is a post's comments as its author's page reads them: the bodies of the top comments and their replies.
func (e *repliesEnv) thread(tok string, post int64) []map[string]any {
	e.t.Helper()
	var out []map[string]any
	res := e.must(200, tok, "GET", fmt.Sprintf("/api/posts/%d/comments", post), nil)
	for _, c := range res["comments"].([]any) {
		m := c.(map[string]any)
		out = append(out, m)
		for _, r := range anyList(m["replies"]) {
			out = append(out, r.(map[string]any))
		}
	}
	return out
}

func anyList(v any) []any { l, _ := v.([]any); return l }

func bodies(cs []map[string]any) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c["body"].(string))
	}
	return out
}

func TestDBRemoteReplies(t *testing.T) {
	e := newRepliesEnv(t)
	ctx := context.Background()
	anaFollowers := "https://here.example/users/ana/followers"

	// bo answers ana's post, addressed to ana and her followers, as Mastodon addresses a reply: kept as a comment under
	// it, its text as text, bo shown by handle and host, ana told once.
	body := activity(boID, "Create", reply(boID, 1, e.postID, `<p><span class="h-card"><a href="https://here.example/users/ana" class="u-url mention">@<span>ana</span></a></span> <b>nice</b> one</p>`,
		"https://here.example/users/ana", anaFollowers), nil)
	{
		var d map[string]any
		json.Unmarshal(body, &d)
		d["object"].(map[string]any)["tag"] = []any{map[string]any{"type": "Mention", "href": "https://here.example/users/ana", "name": "@ana@here.example"}}
		body, _ = json.Marshal(d)
	}
	if why := e.deliver(body, e.bo()); why != inAccepted || e.outcome() != "done:" {
		t.Fatalf("bo's reply: %v %s", why, e.outcome())
	}
	c1, ok := e.kept(boID + "/statuses/1")
	if !ok || c1.post != e.post || c1.parent != 0 || c1.body != "@ana@here.example nice one" {
		t.Fatalf("bo's reply is kept as %+v (%v)", c1, ok)
	}
	got := e.thread(e.ana, e.post)
	if len(got) != 1 || got[0]["remote"] == nil || got[0]["remote"].(map[string]any)["link"] != boID+"/r/1" {
		t.Fatalf("ana's post shows %v", got)
	}
	postOut := e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", e.post), nil)
	if u := postOut["users"].(map[string]any)[fmt.Sprint(e.boUser)].(map[string]any); u["remote"].(map[string]any)["handle"] != "bo@social.example" {
		t.Errorf("bo is shown as %v", u)
	}
	if p := postOut["posts"].([]any)[0].(map[string]any); num(p["commentCount"]) != 1 || len(anyList(p["comments"])) != 1 {
		t.Errorf("the post's count and preview: %v", p)
	}
	if n := e.notes(e.anaID); !slices.Equal(n, []string{"comment"}) {
		t.Errorf("ana was told %v", n)
	}

	// The forward (7.1.2): it named ana's followers, so it goes, as it came, to her followers on other servers but
	// bo's own server: fan's shared inbox on third.example, not cat's on social.example, and nobody of cy's.
	fw := e.forwards()
	if len(fw) != 1 || fw[0].body != string(body) || !slices.Equal(fw[0].inboxes, []string{"https://third.example/inbox"}) {
		t.Fatalf("forwards: %+v", fw)
	}
	// Sent once: the same delivery again is a repeat at the inbox, and another Create of the same reply is a repeat at
	// the worker; neither forwards anything more.
	e.deliver(body, e.bo())
	if e.last() != inDuplicate {
		t.Errorf("the same delivery again: %v", e.last())
	}
	if why := e.send(e.bo(), boID, "Create", reply(boID, 1, e.postID, "again", anaFollowers)); why != inAccepted || e.outcome() != "done: a repeat, deleted before it came, or its domain suspended" {
		t.Errorf("the same reply in another Create: %v %s", why, e.outcome())
	}
	if n := len(e.forwards()); n != 1 {
		t.Errorf("%d forwards after the repeats", n)
	}
	// Only for the collection named: a reply that names cy's followers and not ana's is kept and goes nowhere, and one
	// that names nobody's is not forwarded either.
	e.send(e.cat(), catID, "Create", reply(catID, 2, e.postID, "cat says hi", "https://here.example/users/cyd/followers"))
	e.send(e.eve(), eveID, "Create", reply(eveID, 3, e.postID, "eve says hi"))
	if _, ok := e.kept(catID + "/statuses/2"); !ok {
		t.Errorf("cat's reply is not kept: %s", e.outcome())
	}
	if n := len(e.forwards()); n != 1 {
		t.Errorf("%d forwards after replies that name no collection of the post's author", n)
	}

	// A reply to a reply joins its thread, starting with the person answered; to a member's comment it tells that member.
	if why := e.send(e.eve(), eveID, "Create", reply(eveID, 4, boID+"/statuses/1", "agreed")); why != inAccepted {
		t.Fatalf("eve's answer to bo: %v", why)
	}
	if c, _ := e.kept(eveID + "/statuses/4"); c.parent != c1.id || c.body != "@bo@social.example agreed" {
		t.Errorf("eve's answer to bo is kept as %+v", c)
	}
	cyCmt := num(e.must(200, e.cy, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "great stuff"})["comment"].(map[string]any)["id"])
	if e.count(`SELECT count(*) FROM ap_comment_out WHERE comment_id = $1`, cyCmt) != 0 {
		t.Error("the comment of cy, who shares nothing, is on its way out")
	}
	// cy never opted in: an answer to cy's comment is not kept (a member who never opted in gets nothing from other
	// servers); once cy shares, it is, and cy is told.
	e.send(e.eve(), eveID, "Create", reply(eveID, 6, fmt.Sprintf("https://here.example/users/cyd/comments/%d", cyCmt), "@cyd hello"))
	if _, ok := e.kept(eveID + "/statuses/6"); ok || e.outcome() != "done: dropped (it answers no Public post here, or one not shown)" {
		t.Errorf("an answer to the comment of a member who shares nothing: %s", e.outcome())
	}
	e.mustExec(`UPDATE users SET federates = true WHERE id = $1`, e.cyID)
	e.send(e.eve(), eveID, "Create", reply(eveID, 5, fmt.Sprintf("https://here.example/users/cyd/comments/%d", cyCmt), "@cyd thanks"))
	if c, _ := e.kept(eveID + "/statuses/5"); c.parent != cyCmt || c.body != "@cyd thanks" {
		t.Errorf("eve's answer to cy is kept as %+v", c)
	}
	if n := e.notes(e.cyID); !slices.Equal(n, []string{"reply"}) {
		t.Errorf("cy was told %v", n)
	}
	if n := e.notes(e.anaID); !slices.Equal(n, []string{"comment", "comment", "comment", "comment", "comment"}) {
		t.Errorf("ana was told %v", n)
	}

	// Update: only by the same account with a newer updated; another account cannot edit bo's reply.
	upd := reply(boID, 1, e.postID, "nice one, edited")
	upd["updated"] = "2026-11-15T10:00:00Z"
	if e.send(e.bo(), boID, "Update", upd); e.outcome() != "done:" {
		t.Errorf("bo's edit: %s", e.outcome())
	}
	if c, _ := e.kept(boID + "/statuses/1"); c.body != "nice one, edited" {
		t.Errorf("after bo's edit: %+v", c)
	}
	old := reply(boID, 1, e.postID, "an older version")
	old["updated"] = "2026-11-15T09:00:00Z"
	e.send(e.bo(), boID, "Update", old)
	if c, _ := e.kept(boID + "/statuses/1"); c.body != "nice one, edited" || e.outcome() != "done: ignored (not newer than the version kept)" {
		t.Errorf("an older edit: %+v %s", c, e.outcome())
	}
	e.send(e.bo(), boID, "Update", reply(boID, 1, e.postID, "no updated"))
	if e.outcome() != "done: ignored (an Update without updated)" {
		t.Errorf("an edit without updated: %s", e.outcome())
	}
	forged := reply(boID, 1, e.postID, "cat's words")
	forged["attributedTo"], forged["updated"] = catID, "2026-11-15T11:00:00Z"
	e.send(e.cat(), catID, "Update", forged)
	if c, _ := e.kept(boID + "/statuses/1"); c.body != "nice one, edited" || e.outcome() != "done: refused (the reply is another account's)" {
		t.Errorf("cat's edit of bo's reply: %+v %s", c, e.outcome())
	}

	// Nobody forges a member here as the author: a Note attributed to ana, or with an id on this server, is dropped at the
	// inbox (the origin rule on the object).
	fake := reply(catID, 9, e.postID, "I am ana")
	fake["attributedTo"] = "https://here.example/users/ana"
	if why := e.send(e.cat(), catID, "Create", fake); why != inDropped {
		t.Errorf("a reply attributed to ana: %v", why)
	}
	fake = reply(catID, 9, e.postID, "here")
	fake["id"] = "https://here.example/users/ana/comments/999"
	if why := e.send(e.cat(), catID, "Create", fake); why != inDropped {
		t.Errorf("a reply with an id of this server: %v", why)
	}

	// What is not answered: a This server post, a hidden post, a post of a member who stopped sharing, a post nobody has,
	// a direct reply, a reply to followers only; a post of another server stays FWR 5's (kept in ana's Home).
	local := e.postAs(e.ana, map[string]any{"body": "just us"})
	hidden := e.postAs(e.ana, map[string]any{"body": "hidden", "audience": "public"})
	_, admin := e.user("boss", "admin")
	e.must(200, admin, "POST", "/api/mod/actions", map[string]any{"action": "hide", "kind": "post", "id": hidden, "reason": "test"})
	direct := reply(catID, 20, e.postID, "psst")
	direct["to"], direct["cc"] = []string{"https://here.example/users/ana"}, nil
	fonly := reply(catID, 21, e.postID, "followers only")
	fonly["to"], fonly["cc"] = []string{catID + "/followers"}, nil
	for _, c := range []struct {
		name string
		note map[string]any
		want string
	}{
		{"a This server post", reply(catID, 10, fmt.Sprintf("https://here.example/users/ana/posts/%d", local), "hi"), "done: dropped (it answers no Public post here, or one not shown)"},
		{"a hidden post", reply(catID, 11, fmt.Sprintf("https://here.example/users/ana/posts/%d", hidden), "hi"), "done: dropped (it answers no Public post here, or one not shown)"},
		{"a post nobody has", reply(catID, 12, "https://here.example/users/ana/posts/999999", "hi"), "done: dropped (it answers no Public post here, or one not shown)"},
		{"the right number under another name", reply(catID, 13, fmt.Sprintf("https://here.example/users/cyd/posts/%d", e.post), "hi"), "done: dropped (it answers no Public post here, or one not shown)"},
		{"a direct reply", direct, "done: dropped (a reply addressed to fewer people than the post)"},
		{"a reply to followers only", fonly, "done: dropped (a reply addressed to fewer people than the post)"},
	} {
		if why := e.send(e.cat(), catID, "Create", c.note); why != inAccepted || e.outcome() != c.want {
			t.Errorf("%s: %v %s, want %s", c.name, why, e.outcome(), c.want)
		}
	}
	e.send(e.bo(), boID, "Create", noteObj(30, "my own thread", nil))
	e.send(e.cat(), catID, "Create", reply(catID, 14, boID+"/statuses/30", "a reply to bo's post"))
	if _, ok := e.kept(catID + "/statuses/14"); ok || e.outcome() != "done: dropped (nobody here follows the account)" {
		t.Errorf("a reply to a post of another server: %s", e.outcome())
	}
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"federates": false})
	e.send(e.cat(), catID, "Create", reply(catID, 15, e.postID, "hi"))
	if e.outcome() != "done: dropped (it answers no Public post here, or one not shown)" {
		t.Errorf("a reply to a member who stopped sharing: %s", e.outcome())
	}
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"federates": true})

	// A block, whichever blocked whom, between the account and the post's author or the thread's.
	e.mustExec(`INSERT INTO blocks (user_id, target_id) VALUES ($1, $2)`, e.anaID, e.catID)
	e.send(e.cat(), catID, "Create", reply(catID, 16, e.postID, "hi"))
	if e.outcome() != "done: dropped (a block stands between the account and the post or the thread)" {
		t.Errorf("a reply across a block: %s", e.outcome())
	}
	e.mustExec(`DELETE FROM blocks`)
	e.mustExec(`INSERT INTO blocks (user_id, target_id) VALUES ($1, $2)`, e.cyID, e.catID)
	e.send(e.cat(), catID, "Create", reply(catID, 17, fmt.Sprintf("https://here.example/users/cyd/comments/%d", cyCmt), "hi"))
	if e.outcome() != "done: dropped (a block stands between the account and the post or the thread)" {
		t.Errorf("a reply into cy's thread across cy's block: %s", e.outcome())
	}
	e.mustExec(`DELETE FROM blocks`)

	// The switches: follows off drops at the inbox; the caps: per post, per server an hour, in all.
	e.setFlag("follows", false)
	if why := e.send(e.cat(), catID, "Create", reply(catID, 18, e.postID, "hi")); why != inDropped {
		t.Errorf("follows off: %v", why)
	}
	e.setFlag("follows", true)
	e.mustExec(`INSERT INTO comments (post_id, user_id, body, ap_id) SELECT $1, $2, 'x', 'https://other.example/flood/' || g FROM generate_series(1, $3) g`, hidden, e.boUser, remoteRepliesPerPost)
	e.mustExec(`UPDATE posts SET hidden_at = NULL WHERE id = $1`, hidden)
	e.send(e.eve(), eveID, "Create", reply(eveID, 19, fmt.Sprintf("https://here.example/users/ana/posts/%d", hidden), "one more"))
	if e.outcome() != "done: dropped (the post holds as many replies from other servers as are kept)" {
		t.Errorf("past the cap per post: %s", e.outcome())
	}
	e.a.fed.repliesFull.Store(true)
	e.send(e.eve(), eveID, "Create", reply(eveID, 22, e.postID, "hi"))
	if e.outcome() != "done: dropped (as many replies from other servers are kept as may be)" {
		t.Errorf("past the cap in all: %s", e.outcome())
	}
	if err := e.a.remoteTalkHousekeep(ctx); err != nil || e.a.fed.repliesFull.Load() {
		t.Errorf("housekeeping's count leaves the cap in all reached: %v", err)
	}
	for range remoteRepliesPerHostHour {
		e.a.fed.repliesIn.add("other.example")
	}
	e.send(e.eve(), eveID, "Create", reply(eveID, 23, e.postID, "hi"))
	if e.outcome() != "done: dropped (its server sent more replies this hour than are kept)" {
		t.Errorf("past the hour's allowance: %s", e.outcome())
	}

	// Moderation: ana reports cat's reply, a moderator hides it, ana (the post's author) deletes bo's.
	catCmt, _ := e.kept(catID + "/statuses/2")
	e.must(200, e.ana, "POST", "/api/reports", map[string]any{"kind": "comment", "id": catCmt.id, "reason": "spam"})
	e.must(200, admin, "POST", "/api/mod/actions", map[string]any{"action": "hide", "kind": "comment", "id": catCmt.id, "reason": "test"})
	if slices.Contains(bodies(e.thread(e.cy, e.post)), "cat says hi") {
		t.Error("a hidden remote reply is shown to cy")
	}
	if e.count(`SELECT count(*) FROM notifications WHERE user_id IN (SELECT id FROM users WHERE domain <> '')`) != 0 {
		t.Error("an account of another server was told something here")
	}

	// Delete: only by the same account; with an answer under it, kept as a placeholder.
	e.send(e.cat(), catID, "Delete", map[string]any{"id": boID + "/statuses/1", "type": "Tombstone"})
	if c, _ := e.kept(boID + "/statuses/1"); c.deleted || e.outcome() != "done: refused (the reply is another account's)" {
		t.Errorf("cat's Delete of bo's reply: %+v %s", c, e.outcome())
	}
	e.send(e.bo(), boID, "Delete", map[string]any{"id": boID + "/statuses/1", "type": "Tombstone"})
	if c, ok := e.kept(boID + "/statuses/1"); !ok || !c.deleted || c.body != "" {
		t.Errorf("bo's Delete of his reply with an answer: %+v %v %s", c, ok, e.outcome())
	}
	e.send(e.eve(), eveID, "Delete", eveID+"/statuses/4")
	if _, ok := e.kept(eveID + "/statuses/4"); ok || e.count(`SELECT count(*) FROM comments WHERE ap_id = $1`, boID+"/statuses/1") != 0 {
		t.Errorf("eve's Delete of her answer leaves %d placeholders", e.count(`SELECT count(*) FROM comments WHERE ap_id = $1`, boID+"/statuses/1"))
	}
	eveCmt, _ := e.kept(eveID + "/statuses/3")
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/comments/%d", eveCmt.id), nil)
	if _, ok := e.kept(eveID + "/statuses/3"); ok {
		t.Error("ana could not delete a remote reply under her post")
	}
	e.must(404, e.cy, "DELETE", fmt.Sprintf("/api/comments/%d", catCmt.id), nil)
	eve5, _ := e.kept(eveID + "/statuses/5")
	e.must(403, e.ana, "PUT", fmt.Sprintf("/api/comments/%d", eve5.id), map[string]any{"body": "mine now"})

	// In local mode no remote reply reaches a page (the rows are kept until -purge-remote).
	before := len(e.thread(e.ana, e.post))
	f := e.a.fed
	e.a.fed = nil
	after := len(e.thread(e.ana, e.post))
	e.a.fed = f
	if before < 2 || after != 1 {
		t.Errorf("ana's post shows %d comments in a mode that federates and %d in local mode, want only cy's there", before, after)
	}
}

// stringArray reads a Postgres text array without importing pq here twice.
type stringArray []string

func (s *stringArray) Scan(v any) error {
	b, ok := v.([]byte)
	if !ok {
		return fmt.Errorf("not an array: %T", v)
	}
	str := strings.Trim(string(b), "{}")
	*s = nil
	if str != "" {
		for _, x := range strings.Split(str, ",") {
			*s = append(*s, strings.Trim(x, `"`))
		}
	}
	return nil
}

func TestDBRemoteLikes(t *testing.T) {
	e := newRepliesEnv(t)
	likes := func(post int64) int64 {
		return num(e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", post), nil)["posts"].([]any)[0].(map[string]any)["likes"])
	}
	like := func(n int, object string) []byte {
		return activity(boID, "Like", object, map[string]any{"id": fmt.Sprintf("%s#likes/%d", boID, n)})
	}

	// bo likes ana's post: counted once with the likes from here, ana told once; a repeat and a Like with content (an
	// emoji reaction) count nothing more.
	e.must(200, e.cy, "POST", fmt.Sprintf("/api/posts/%d/like", e.post), nil)
	if why := e.deliver(like(1, e.postID), e.bo()); why != inAccepted || e.outcome() != "done:" {
		t.Fatalf("bo's like: %v %s", why, e.outcome())
	}
	if n := likes(e.post); n != 2 {
		t.Errorf("%d likes, want cy's and bo's", n)
	}
	e.deliver(activity(boID, "Like", e.postID, map[string]any{"id": boID + "#likes/1b", "content": "🔥"}), e.bo())
	if n := likes(e.post); n != 2 || e.outcome() != "done: a repeat" {
		t.Errorf("a second Like: %d likes, %s", n, e.outcome())
	}
	// Undo, embedded: the like goes, the notification stays; liked again, ana is not told twice.
	e.deliver(activity(boID, "Undo", map[string]any{"id": boID + "#likes/1", "type": "Like", "actor": boID, "object": e.postID}, nil), e.bo())
	if n := likes(e.post); n != 1 {
		t.Errorf("after bo's Undo: %d likes", n)
	}
	e.deliver(like(2, e.postID), e.bo())
	if n := e.notes(e.anaID); likes(e.post) != 2 || !slices.Equal(n, []string{"like"}) {
		t.Errorf("liked again: ana was told %v", n)
	}
	// Undo by id; another account's Undo of bo's like, embedded or by id, is not his.
	e.deliver(activity(catID, "Undo", map[string]any{"id": boID + "#likes/2", "type": "Like", "actor": boID, "object": e.postID}, nil), e.cat())
	if e.last() != inDropped {
		t.Errorf("cat's Undo of bo's Like, embedded: %v", e.last())
	}
	e.deliver(activity(catID, "Undo", boID+"#likes/2", nil), e.cat())
	if likes(e.post) != 2 {
		t.Errorf("cat's Undo of bo's Like by id took it away (%s)", e.outcome())
	}
	e.deliver(activity(boID, "Undo", boID+"#likes/2", nil), e.bo())
	if likes(e.post) != 1 || e.outcome() != "done:" {
		t.Errorf("bo's Undo by id: %d likes, %s", likes(e.post), e.outcome())
	}

	// A comment here: liked into comment_likes, its author told.
	cmt := num(e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "thanks all"})["comment"].(map[string]any)["id"])
	e.deliver(activity(eveID, "Like", fmt.Sprintf("https://here.example/users/ana/comments/%d", cmt), nil), e.eve())
	if e.outcome() != "done:" || e.count(`SELECT count(*) FROM comment_likes WHERE comment_id = $1 AND ap_id IS NOT NULL`, cmt) != 1 {
		t.Errorf("eve's like of ana's comment: %s", e.outcome())
	}
	if n := e.notes(e.anaID); !slices.Equal(n, []string{"like", "comment_like"}) {
		t.Errorf("ana was told %v", n)
	}

	// What cannot be liked: what is not this server's (dropped at the inbox), a This server post, a hidden one, one
	// nobody has, one across a block; nothing with follows off.
	local := e.postAs(e.ana, map[string]any{"body": "just us"})
	hidden := e.postAs(e.ana, map[string]any{"body": "hidden", "audience": "public"})
	_, admin := e.user("boss", "admin")
	e.must(200, admin, "POST", "/api/mod/actions", map[string]any{"action": "hide", "kind": "post", "id": hidden, "reason": "test"})
	if e.deliver(like(3, boID+"/statuses/1"), e.bo()); e.last() != inDropped {
		t.Errorf("a Like of a post of another server: %v", e.last())
	}
	for _, id := range []int64{local, hidden, 999999} {
		if e.deliver(like(1000000+int(id), fmt.Sprintf("https://here.example/users/ana/posts/%d", id)), e.bo()); e.outcome() != "done: dropped (not a Public post here or a comment shown under one, or a block between them)" {
			t.Errorf("a Like of post %d: %s", id, e.outcome())
		}
	}
	e.mustExec(`INSERT INTO blocks (user_id, target_id) VALUES ($1, $2)`, e.anaID, e.catID)
	if e.deliver(activity(catID, "Like", e.postID, nil), e.cat()); e.outcome() != "done: dropped (not a Public post here or a comment shown under one, or a block between them)" {
		t.Errorf("a Like across a block: %s", e.outcome())
	}
	e.mustExec(`DELETE FROM blocks`)
	e.setFlag("follows", false)
	if e.deliver(activity(catID, "Like", e.postID, nil), e.cat()); e.last() != inDropped {
		t.Errorf("follows off: %v", e.last())
	}
	e.setFlag("follows", true)
	// The caps: in all, and the hour's allowance per server.
	e.a.fed.likesFull.Store(true)
	if e.deliver(activity(catID, "Like", e.postID, nil), e.cat()); e.outcome() != "done: dropped (as many likes from other servers are kept as may be)" {
		t.Errorf("past the cap in all: %s", e.outcome())
	}
	e.a.fed.likesFull.Store(false)
	for range remoteLikesPerHostHour {
		e.a.fed.likesIn.add("social.example")
	}
	if e.deliver(activity(catID, "Like", e.postID, nil), e.cat()); e.outcome() != "done: dropped (its server sent more likes this hour than are kept)" {
		t.Errorf("past the hour's allowance: %s", e.outcome())
	}
	if likes(e.post) != 1 {
		t.Errorf("%d likes after the refusals", likes(e.post))
	}
}

// A member's comments and likes going out (6.4): to whom, with what, and taken back after.
func TestDBCommentsOut(t *testing.T) {
	e := newRepliesEnv(t)
	ne := &notesEnv{followEnv: e.followEnv, anaID: e.anaID, ana: e.ana}
	produce := ne.produce
	last := func(kind string) outRow {
		t.Helper()
		var r outRow
		for _, x := range ne.outRows() {
			if x.kind == kind {
				r = x
			}
		}
		if r.kind == "" {
			t.Fatalf("no %s enqueued", kind)
		}
		return r
	}
	produce() // ana's post
	// ana comments on her own Public post: Create(Note) inReplyTo the post, to her followers on other servers.
	cmt := num(e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "and a second one"})["comment"].(map[string]any)["id"])
	produce()
	cr := last("Create")
	var act struct {
		ID     string
		Object outReplyNote
	}
	if err := json.Unmarshal([]byte(cr.body), &act); err != nil {
		t.Fatal(err)
	}
	noteID := fmt.Sprintf("https://here.example/users/ana/comments/%d", cmt)
	if act.ID != noteID+"/activity" || act.Object.ID != noteID || act.Object.InReplyTo != e.postID || act.Object.Content != "<p>and a second one</p>" ||
		!slices.Equal(act.Object.To, []string{publicCollection}) || !slices.Equal(act.Object.Cc, []string{"https://here.example/users/ana/followers"}) || act.Object.Updated != "" {
		t.Errorf("the Create of ana's comment: %s", cr.body)
	}
	if !slices.Equal(cr.inboxes, []string{catID + "/inbox", "https://third.example/inbox"}) {
		t.Errorf("it goes to %v", cr.inboxes)
	}
	// An edit inside the window, once the Create went (an Update waits for it): Update(Note) with updated, to the same
	// inboxes.
	e.mustExec(`DELETE FROM ap_deliveries WHERE outbox_id = (SELECT id FROM ap_outbox WHERE ap_id = $1)`, noteID+"/activity")
	e.must(200, e.ana, "PUT", fmt.Sprintf("/api/comments/%d", cmt), map[string]any{"body": "and a second one, edited"})
	produce()
	up := last("Update")
	if !strings.Contains(up.body, `"updated":"`) || !strings.Contains(up.body, "edited") || !strings.Contains(up.body, `"inReplyTo":"`+e.postID) || !slices.Equal(up.inboxes, cr.inboxes) {
		t.Errorf("the Update: %s to %v", up.body, up.inboxes)
	}
	// The Delete after its Create: tried once (it may have arrived), the Create is given up and the Delete goes.
	e.mustExec(`UPDATE ap_deliveries SET attempts = 1 WHERE outbox_id = (SELECT id FROM ap_outbox WHERE ap_id = $1)`, noteID+"/activity")
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/comments/%d", cmt), nil)
	produce()
	del := last("Delete")
	if del.id != noteID+"#delete" || !slices.Equal(del.inboxes, cr.inboxes) || e.count(`SELECT count(*) FROM ap_comment_out`) != 0 {
		t.Errorf("the Delete: %s to %v", del.id, del.inboxes)
	}
	// cy shares nothing: cy's comment stays here. Once cy shares, it goes to ana's followers too, cc ana.
	// A comment on a This server post stays here, whoever writes it.
	local := e.postAs(e.ana, map[string]any{"body": "just us"})
	e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", local), map[string]any{"body": "stays here too"})
	if e.count(`SELECT count(*) FROM ap_comment_out`) != 0 {
		t.Error("a comment on a This server post is on its way out")
	}
	e.must(200, e.cy, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "stays here"})
	if e.count(`SELECT count(*) FROM ap_comment_out`) != 0 {
		t.Error("cy's comment is on its way out")
	}
	e.must(200, e.cy, "PUT", "/api/me", map[string]any{"federates": true})
	if _, err := e.a.fed.ensureKey(context.Background(), keyOwner(e.cyID)); err != nil {
		t.Fatal(err)
	}
	e.must(200, e.cy, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "goes out"})
	produce()
	cy := last("Create")
	if !strings.Contains(cy.body, `"cc":["https://here.example/users/cyd/followers","https://here.example/users/ana"`) ||
		!slices.Equal(cy.inboxes, []string{"https://fourth.example/inbox", catID + "/inbox", "https://third.example/inbox"}) {
		t.Errorf("cy's comment: %s to %v", cy.body, cy.inboxes)
	}
	// ana answers bo's remote reply: inReplyTo bo's Note, bo mentioned and sent to.
	e.send(e.bo(), boID, "Create", reply(boID, 1, e.postID, "nice"))
	boCmt, _ := e.kept(boID + "/statuses/1")
	e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "thank you", "parentId": boCmt.id})
	produce()
	ans := last("Create")
	if !strings.Contains(ans.body, `"inReplyTo":"`+boID+`/statuses/1"`) || !strings.Contains(ans.body, `"href":"`+boID+`"`) || !strings.Contains(ans.body, "@<span>bo</span>") ||
		!slices.Contains(ans.inboxes, boID+"/inbox") {
		t.Errorf("ana's answer to bo: %s", ans.body)
	}
	// A moderator's hide takes a comment back; so does deleting the post.
	hideMe := num(e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "to be hidden"})["comment"].(map[string]any)["id"])
	produce()
	_, admin := e.user("boss", "admin")
	e.must(200, admin, "POST", "/api/mod/actions", map[string]any{"action": "hide", "kind": "comment", "id": hideMe, "reason": "test"})
	produce()
	if last("Delete").id != fmt.Sprintf("https://here.example/users/ana/comments/%d#delete", hideMe) {
		t.Error("a hidden comment is not taken back")
	}
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/posts/%d", e.post), nil)
	produce()
	if n := e.count(`SELECT count(*) FROM ap_comment_out`); n != 0 {
		t.Errorf("%d records left after the post's deletion", n)
	}
	if !slices.ContainsFunc(ne.outRows(), func(r outRow) bool { return r.kind == "Delete" && strings.Contains(r.id, "/users/cyd/comments/") }) {
		t.Error("cy's comment is not taken back with the post")
	}
}

// A member's like and comment on a public post of another server (6.4: Like and Undo to the author's inbox, a comment
// to the author and the member's followers), and what is refused.
func TestDBTalkToRemotePosts(t *testing.T) {
	e := newRepliesEnv(t)
	ne := &notesEnv{followEnv: e.followEnv, anaID: e.anaID, ana: e.ana}
	ne.produce()
	e.send(e.bo(), boID, "Create", noteObj(40, "bo's post", nil))
	p := e.remotePostID(boID + "/statuses/40")
	home := e.must(200, e.ana, "GET", "/api/home", nil)["posts"].([]any)
	if r := home[0].(map[string]any)["remote"].(map[string]any); r["talk"] != true {
		t.Errorf("bo's post in Home: %v", r)
	}
	seen := len(ne.outRows())
	// ana likes it: Like to bo's own inbox; unlike before it was ever tried: both taken back, nothing goes.
	if out := e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/like", p), nil); out["liked"] != true || num(out["likes"]) != 1 {
		t.Errorf("ana's like: %v", out)
	}
	rows := ne.outRows()[seen:]
	if len(rows) != 1 || rows[0].kind != "Like" || !slices.Equal(rows[0].inboxes, []string{boID + "/inbox"}) || !strings.Contains(rows[0].body, `"object":"`+boID+`/statuses/40"`) {
		t.Fatalf("the Like: %+v", rows)
	}
	e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/like", p), nil)
	if n := e.count(`SELECT count(*) FROM ap_deliveries WHERE state = 'queued' AND inbox = $1`, boID+"/inbox"); n != 0 || e.count(`SELECT count(*) FROM likes WHERE post_id = $1`, p) != 0 {
		t.Errorf("an unlike of a Like never tried leaves %d deliveries", n)
	}
	// Liked again and tried once: the Undo goes after it, embedding the Like.
	e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/like", p), nil)
	e.mustExec(`UPDATE ap_deliveries SET attempts = 1 WHERE state = 'queued'`)
	e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/like", p), nil)
	rows = ne.outRows()
	undo := rows[len(rows)-1]
	if undo.kind != "Undo" || !strings.Contains(undo.body, `"type":"Like"`) || !slices.Equal(undo.inboxes, []string{boID + "/inbox"}) {
		t.Errorf("the Undo: %+v", undo)
	}
	// ana comments on it: Create to bo and ana's followers, inReplyTo bo's post, cc bo.
	c := num(e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", p), map[string]any{"body": "lovely"})["comment"].(map[string]any)["id"])
	ne.produce()
	rows = ne.outRows()
	cr := rows[len(rows)-1]
	if cr.kind != "Create" || !strings.Contains(cr.body, `"inReplyTo":"`+boID+`/statuses/40"`) || !strings.Contains(cr.body, `"cc":["https://here.example/users/ana/followers","`+boID+`"]`) ||
		!slices.Equal(cr.inboxes, []string{boID + "/inbox", catID + "/inbox", "https://third.example/inbox"}) {
		t.Errorf("ana's comment on bo's post: %+v", cr)
	}
	// Who may not: cy, who cannot read it (404, as for any post not shown); cy reading it and sharing nothing (403, why);
	// a followers-only post of another server (404).
	e.must(404, e.cy, "POST", fmt.Sprintf("/api/posts/%d/like", p), nil)
	e.must(404, e.cy, "GET", fmt.Sprintf("/api/comments/%d/thread", c), nil)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, e.cyID, e.boUser)
	if out := e.must(403, e.cy, "POST", fmt.Sprintf("/api/posts/%d/like", p), nil); out["error"] != talkNotShared {
		t.Errorf("cy's like: %v", out)
	}
	e.must(403, e.cy, "POST", fmt.Sprintf("/api/posts/%d/comments", p), map[string]any{"body": "hi"})
	e.send(e.bo(), boID, "Create", noteObj(41, "for followers", map[string]any{"to": []string{boID + "/followers"}, "cc": nil}))
	fp := e.remotePostID(boID + "/statuses/41")
	e.must(404, e.ana, "POST", fmt.Sprintf("/api/posts/%d/like", fp), nil)
	e.must(404, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", fp), map[string]any{"body": "hi"})
	// bo deletes his post: ana's comment under it goes with it, and is taken back.
	e.mustExec(`UPDATE ap_deliveries SET attempts = 1 WHERE state = 'queued'`)
	e.send(e.bo(), boID, "Delete", map[string]any{"id": boID + "/statuses/40", "type": "Tombstone"})
	ne.produce()
	rows = ne.outRows()
	if d := rows[len(rows)-1]; d.kind != "Delete" || d.id != fmt.Sprintf("https://here.example/users/ana/comments/%d#delete", c) {
		t.Errorf("after bo's Delete: %+v", d)
	}
}

// Two Flowa instances over the real fetcher, signer, inbox and delivery queue (TestDBTwoFlowaNotes's world): bob at
// b.example follows ana here, answers her Public post, edits his answer, likes the post and takes the like back; each
// step is seen here. (FWR 8's container suite is the DoD's TestTwoInstances.)
func TestDBTwoFlowaReplies(t *testing.T) {
	e := newNotesEnv(t)
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
	bobID, bob := be.user("bob", "user")
	be.must(200, bob, "PUT", "/api/me", map[string]any{"federates": true})
	for _, o := range []keyOwner{keyOwner(bobID), instanceActor} {
		if _, err := bf.ensureKey(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}
	settleKeys(t, bf)
	e.c.fetch = actorStub(t, be.h)
	e.c.fetch.origin = e.a.fed.origin
	e.out.fetch = actorStub(t, be.h)
	e.out.fetch.origin = e.a.fed.origin
	bf.actors.fetch = actorStub(t, e.h)
	bf.actors.fetch.origin = bf.origin
	bf.actors.now, bf.actors.built = e.clock.now, time.Time{}
	bf.verifier.now, bf.verifier.built = e.clock.now, time.Time{}
	bf.inbox.now, bf.inbox.manual = e.clock.now, true
	var hmu sync.Mutex
	var heard []inboxReason
	bf.inbox.heard = func(r inboxReason) { hmu.Lock(); heard = append(heard, r); hmu.Unlock() }
	bq := bf.out
	bq.manual, bq.now, bq.built, bq.jitter = true, e.clock.now, time.Time{}, func() float64 { return 0 }
	bq.mu.Lock()
	bq.floor = time.Time{}
	bq.mu.Unlock()
	bq.fetch = actorStub(t, e.h)
	bq.fetch.origin = bf.origin
	ctx := context.Background()
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
	if _, err := bf.remoteActor(ctx, "https://here.example/users/ana"); err != nil {
		t.Fatal(err)
	}
	var anaThere int64
	if err := be.a.db.QueryRow(`SELECT user_id FROM ap_actors WHERE ap_id = 'https://here.example/users/ana'`).Scan(&anaThere); err != nil {
		t.Fatal(err)
	}
	be.must(200, bob, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", anaThere), nil)
	pump()
	p := e.postAs(e.ana, map[string]any{"body": "hello b.example", "audience": "public"})
	pump()
	var there int64
	if err := be.a.db.QueryRow(`SELECT id FROM posts WHERE ap_id = $1`, fmt.Sprintf("https://here.example/users/ana/posts/%d", p)).Scan(&there); err != nil {
		t.Fatalf("ana's post did not reach b.example: %v", err)
	}
	ne := &repliesEnv{notesInEnv: &notesInEnv{followEnv: e.followEnv}}
	// Reply.
	bc := num(be.must(200, bob, "POST", fmt.Sprintf("/api/posts/%d/comments", there), map[string]any{"body": "hello from b"})["comment"].(map[string]any)["id"])
	pump()
	got := ne.thread(e.ana, p)
	if len(got) != 1 || got[0]["body"] != "hello from b" || got[0]["remote"] == nil {
		t.Fatalf("ana's post here shows %v", got)
	}
	if n := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'comment' AND fed`, e.anaID); n != 1 {
		t.Errorf("ana was told %d times", n)
	}
	// Edit, inside bob's 15 minutes.
	be.must(200, bob, "PUT", fmt.Sprintf("/api/comments/%d", bc), map[string]any{"body": "hello from b, edited"})
	pump()
	if got := ne.thread(e.ana, p); len(got) != 1 || got[0]["body"] != "hello from b, edited" || got[0]["edited"] != true {
		t.Errorf("after bob's edit ana's post shows %v", got)
	}
	// Like and unlike.
	likes := func() int64 {
		return num(e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", p), nil)["posts"].([]any)[0].(map[string]any)["likes"])
	}
	be.must(200, bob, "POST", fmt.Sprintf("/api/posts/%d/like", there), nil)
	pump()
	if n := likes(); n != 1 {
		t.Errorf("after bob's like: %d likes here", n)
	}
	be.must(200, bob, "POST", fmt.Sprintf("/api/posts/%d/like", there), nil)
	pump()
	if n := likes(); n != 0 {
		t.Errorf("after bob's unlike: %d likes here", n)
	}
	// Delete.
	be.must(200, bob, "DELETE", fmt.Sprintf("/api/comments/%d", bc), nil)
	pump()
	if got := ne.thread(e.ana, p); len(got) != 0 {
		t.Errorf("after bob's deletion ana's post shows %v", got)
	}
	hmu.Lock()
	defer hmu.Unlock()
	if slices.ContainsFunc(heard, func(r inboxReason) bool { return r != inAccepted }) {
		t.Errorf("b.example's inbox answered %v", heard)
	}
}
