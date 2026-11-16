package main

// Actions on remote accounts (docs/federation.md section 10, "As built (FMD 2)"; FMD item 2): limit, suspend and purge
// one account of another server, through the moderators' route, with their undo, in a mode that federates. The
// instance is repliesEnv's: ana follows bo, cat follows ana (both of social.example), and ana has a Public post.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// heldEnv is repliesEnv with a moderator, an admin, and dee, a member who follows cat.
type heldEnv struct {
	*repliesEnv
	admin, mod string
	deeID      int64
	dee        string
}

func newHeldEnv(t *testing.T) *heldEnv {
	t.Helper()
	e := &heldEnv{repliesEnv: newRepliesEnv(t)}
	_, e.admin = e.user("boss", "admin")
	_, e.mod = e.user("mo", "moderator")
	e.deeID, e.dee = e.member("dee", false)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, e.deeID, e.catID)
	e.mustExec(`UPDATE ap_actors SET summary = 'cat bio' WHERE user_id = $1`, e.catID)
	return e
}

// later is an end time a day from now, as the page sends it.
func later() string {
	return time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
}

// act sends one moderator action on account id and gives the status and the answer.
func (e *heldEnv) act(tok, action string, id int64, reason string) (int, map[string]any) {
	e.t.Helper()
	body := map[string]any{"action": action, "kind": "user", "id": id, "reason": reason}
	if action == "mute" || action == "suspend" {
		body["until"] = later()
	}
	return e.do(tok, "POST", "/api/mod/actions", body)
}

func (e *heldEnv) mustAct(tok, action string, id int64, reason string) map[string]any {
	e.t.Helper()
	code, out := e.act(tok, action, id, reason)
	if code != 200 {
		e.t.Fatalf("%s of %d: %d %v", action, id, code, out)
	}
	return out
}

// count of the post's comments as a member reads it, and the bodies of its thread.
func (e *heldEnv) seen(tok string) ([]string, int64) {
	e.t.Helper()
	n := num(e.must(200, tok, "GET", fmt.Sprintf("/api/posts/%d", e.post), nil)["posts"].([]any)[0].(map[string]any)["commentCount"])
	return bodies(e.thread(tok, e.post)), n
}

// bell is the kinds of a member's notifications and the unread count.
func (e *heldEnv) bell(tok string) ([]string, int64) {
	e.t.Helper()
	out := e.must(200, tok, "GET", "/api/notifications", nil)
	var kinds []string
	for _, it := range out["items"].([]any) {
		kinds = append(kinds, it.(map[string]any)["kind"].(string))
	}
	return kinds, num(out["unread"])
}

func (e *heldEnv) profile(tok string, id int64) map[string]any {
	e.t.Helper()
	return e.must(200, tok, "GET", fmt.Sprintf("/api/fed/accounts/%d", id), nil)
}

// mentionOnly is a post of cat's that only names ana, kept here for the mention.
func (e *heldEnv) mentionOnly(n int) int64 {
	e.t.Helper()
	id := fmt.Sprintf("%s/statuses/%d", catID, n)
	e.send(e.cat(), catID, "Create", map[string]any{"id": id, "type": "Note", "attributedTo": catID, "content": "<p>@ana look</p>",
		"to": []string{asPublic}, "cc": []string{"https://here.example/users/ana"}, "tag": []any{map[string]any{"type": "Mention", "href": "https://here.example/users/ana", "name": "@ana"}}})
	return e.remotePostID(id)
}

func TestDBRemoteAccountActions(t *testing.T) {
	t.Run("limit in and out", testRemoteLimit)
	t.Run("suspend in and out", testRemoteSuspend)
	t.Run("suspended account sends", testRemoteSuspendedSends)
	t.Run("who may act", testRemoteWhoMay)
	t.Run("purge", testRemotePurge)
	t.Run("purge interrupted and repeated", testRemotePurgeInterrupted)
	t.Run("purge of thousands", testRemotePurgeThousands)
	t.Run("two admins at once", testRemoteTwoAdmins)
	t.Run("a follow while suspending", testRemoteFollowRace)
	t.Run("domain and account", testRemoteDomainAndAccount)
	t.Run("odd host", testRemoteOddHost)
	t.Run("statements", testRemoteStatements)
	t.Run("the list and the queue", testRemoteListAndQueue)
}

// Limit: as a limited domain, for one account. Hidden from ana, who does not follow cat: its reply (listed and counted),
// its notification, a post that only names her, its bio; shown to dee, who follows it. A Follow from it waits for
// approval though ana approves nobody by hand. Follows stay. Lifted, everything shows and the request still waits.
func testRemoteLimit(t *testing.T) {
	e := newHeldEnv(t)
	e.mustExec(`DELETE FROM follows WHERE follower_id = $1 AND followee_id = $2`, e.catID, e.anaID) // so cat's Follow below is new
	e.send(e.cat(), catID, "Create", reply(catID, 1, e.postID, "hi from cat"))
	e.send(e.bo(), boID, "Create", reply(boID, 2, e.postID, "hi from bo"))
	named := e.mentionOnly(3)
	if got, n := e.seen(e.ana); len(got) != 2 || n != 2 {
		t.Fatalf("before the limit ana reads %v, %d", got, n)
	}

	out := e.mustAct(e.mod, "mute", e.catID, "spam waves")
	if !strings.Contains(fmt.Sprint(out["note"]), "Limited until") {
		t.Errorf("the answer: %v", out)
	}
	if !e.a.fed.policyNow().limiting(time.Now()) {
		t.Error("the policy in memory does not know an account is limited")
	}
	if got, n := e.seen(e.ana); !slices.Equal(got, []string{"hi from bo"}) || n != 1 {
		t.Errorf("ana, who does not follow cat, reads %v, counted %d", got, n)
	}
	if got, n := e.seen(e.dee); !slices.Contains(got, "hi from cat") || n != 2 {
		t.Errorf("dee, who follows cat, reads %v, counted %d", got, n)
	}
	if k, _ := e.bell(e.ana); slices.Contains(k, "mention") || slices.Contains(k, "comment") && len(k) > 1 {
		t.Errorf("ana's bell shows cat's notifications: %v", k)
	}
	e.must(404, e.ana, "GET", fmt.Sprintf("/api/posts/%d", named), nil)
	if a := e.profile(e.ana, e.catID)["account"].(map[string]any); a["limited"] != fedLimitedHere || a["bio"] != nil {
		t.Errorf("cat's profile for ana: %v", a)
	}
	if a := e.profile(e.dee, e.catID)["account"].(map[string]any); a["limited"] != fedLimitedHere || a["bio"] != "cat bio" {
		t.Errorf("cat's profile for dee: %v", a)
	}
	// Staff see what holds it; a member does not.
	if m, ok := e.profile(e.mod, e.catID)["moderation"].(map[string]any); !ok || m["limitReason"] != "spam waves" || m["limitedUntil"] == nil || m["suspendedUntil"] != nil {
		t.Errorf("the moderator's view: %v", m)
	}
	if _, ok := e.profile(e.ana, e.catID)["moderation"]; ok {
		t.Error("a member is shown the moderation of an account")
	}
	e.deliver(activity(catID, "Follow", "https://here.example/users/ana", nil), e.cat())
	if e.state(e.catID, e.anaID) != "pending" || e.state(e.deeID, e.catID) != "accepted" {
		t.Errorf("cat's Follow of ana %q, dee's follow of cat %q", e.state(e.catID, e.anaID), e.state(e.deeID, e.catID))
	}
	if e.count(`SELECT count(*) FROM mod_actions WHERE action = 'limit_remote' AND target_user_id = $1 AND reason = 'spam waves' AND detail->>'handle' = 'cat@social.example'`, e.catID) != 1 {
		t.Error("the limit is not in the audit log")
	}
	if e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'moderation'`, e.catID) != 0 {
		t.Error("the account of another server was told, as a member is")
	}

	// Lifted: everything shows, the request still waits.
	e.mustAct(e.mod, "unmute", e.catID, "")
	if got, n := e.seen(e.ana); len(got) != 2 || n != 2 {
		t.Errorf("after the limit ana reads %v, %d", got, n)
	}
	e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", named), nil)
	if e.state(e.catID, e.anaID) != "pending" {
		t.Errorf("lifting the limit answered the request: %q", e.state(e.catID, e.anaID))
	}
	if code, _ := e.act(e.mod, "unmute", e.catID, ""); code != http.StatusConflict {
		t.Errorf("a second lift: %d", code)
	}
	if e.a.fed.policyNow().limiting(time.Now()) {
		t.Error("the statements keep the look after the last limit was lifted")
	}
	// A limit whose end passed hides nothing, though the policy in memory still runs the look until it is read again.
	e.mustAct(e.mod, "mute", e.catID, "again")
	e.mustExec(`UPDATE users SET muted_until = now() - interval '1 second' WHERE id = $1`, e.catID)
	if got, _ := e.seen(e.ana); len(got) != 2 {
		t.Errorf("a limit that ended still hides: %v", got)
	}
	e.mustExec(`DELETE FROM follows WHERE follower_id = $1 AND followee_id = $2`, e.catID, e.anaID)
	e.deliver(activity(catID, "Follow", "https://here.example/users/ana", nil), e.cat())
	if e.state(e.catID, e.anaID) != "accepted" {
		t.Errorf("a Follow after the limit ended: %q", e.state(e.catID, e.anaID))
	}
}

// Suspend: its follows both ways end (and members who followed it are told), its own inbox's queued deliveries are
// given up (its server's shared inbox, and a delivery in flight, are not), what it wrote is hidden from everyone,
// followers included, nobody can follow it, and lifting shows its content again without the follows.
func testRemoteSuspend(t *testing.T) {
	e := newHeldEnv(t)
	ctx := context.Background()
	e.send(e.cat(), catID, "Create", reply(catID, 1, e.postID, "hi from cat"))
	named := e.mentionOnly(3)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, e.anaID, e.catID)
	// Three deliveries waiting for cat's own inbox (one in flight) and one for its server's shared inbox.
	for i, inbox := range []string{catID + "/inbox", catID + "/inbox", catID + "/inbox", "https://social.example/inbox"} {
		if _, err := e.out.enqueue(ctx, instanceActor, fmt.Sprintf("https://here.example/x/%d", i), []byte(`{"type":"Like"}`), []deliveryTarget{{Actor: map[bool]string{true: boID, false: catID}[strings.HasSuffix(inbox, ".example/inbox")], Inbox: inbox}}); err != nil {
			t.Fatal(err)
		}
	}
	e.mustExec(`UPDATE ap_deliveries SET locked_until = $1 WHERE id = (SELECT min(id) FROM ap_deliveries WHERE inbox = $2)`, e.clock.now().Add(time.Minute), catID+"/inbox")

	out := e.mustAct(e.admin, "suspend", e.catID, "harassment")
	if !strings.Contains(fmt.Sprint(out["note"]), "3 follows ended") {
		t.Errorf("the answer: %v", out["note"])
	}
	for _, p := range [][2]int64{{e.catID, e.anaID}, {e.anaID, e.catID}, {e.deeID, e.catID}} {
		if s := e.state(p[0], p[1]); s != "" {
			t.Errorf("the follow %d of %d is %q after the suspension", p[0], p[1], s)
		}
	}
	for _, m := range []int64{e.anaID, e.deeID} {
		if e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'moderation' AND text LIKE '%suspended @cat@social.example%'`, m) != 1 {
			t.Errorf("member %d is not told their follow ended", m)
		}
	}
	if e.count(`SELECT count(*) FROM notifications WHERE actor_id = $1 AND kind IN ('follow', 'follow_request')`, e.catID) != 0 {
		t.Error("cat's follow notifications stay")
	}
	if n := e.count(`SELECT count(*) FROM ap_deliveries WHERE inbox = $1 AND state = 'failed' AND last_error = 'the account is suspended here'`, catID+"/inbox"); n != 2 {
		t.Errorf("deliveries given up for cat's inbox: %d, want 2 (the one in flight finishes)", n)
	}
	if e.count(`SELECT count(*) FROM ap_deliveries WHERE inbox = 'https://social.example/inbox' AND state = 'queued'`) != 1 {
		t.Error("the shared inbox of cat's server lost its delivery")
	}
	d := e.count(`SELECT count(*) FROM mod_actions WHERE action = 'suspend_remote' AND target_user_id = $1 AND (detail->>'followsEnded')::int = 3 AND (detail->>'membersTold')::int = 2 AND (detail->>'deliveriesDropped')::int = 2`, e.catID)
	if d != 1 {
		t.Error("the suspension's audit line does not say what it ended")
	}
	// Hidden from everyone, a follower too (a follow written in the moment of the suspension).
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, e.deeID, e.catID)
	for _, tok := range []string{e.ana, e.dee} {
		if got, n := e.seen(tok); slices.Contains(got, "hi from cat") || n != 0 {
			t.Errorf("a member reads %v, %d while cat is suspended", got, n)
		}
	}
	e.must(404, e.ana, "GET", fmt.Sprintf("/api/posts/%d", named), nil)
	if k, _ := e.bell(e.ana); slices.Contains(k, "mention") || slices.Contains(k, "comment") {
		t.Errorf("ana's bell while cat is suspended: %v", k)
	}
	e.mustExec(`DELETE FROM follows WHERE follower_id = $1 AND followee_id = $2`, e.deeID, e.catID)
	// Nobody can follow it, and its profile says why, with no bio.
	p := e.profile(e.ana, e.catID)
	if a := p["account"].(map[string]any); a["limited"] != fedSuspendedHere || a["bio"] != nil || p["view"].(map[string]any)["cannot"] != fedFollowSuspendedHere {
		t.Errorf("cat's profile while suspended: %v", p)
	}
	if code, out := e.do(e.ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", e.catID), nil); code != 403 || out["error"] != fedFollowSuspendedHere {
		t.Errorf("following a suspended account: %d %v", code, out)
	}
	// A post of ana's goes to nobody there.
	before := e.count(`SELECT count(*) FROM ap_deliveries WHERE inbox = $1`, catID+"/inbox")
	var recipients int
	if err := e.a.db.QueryRow(`SELECT count(*) FROM (`+postRecipientsSQL+`) r WHERE r.ap_id = $4`, e.anaID, "{"+catID+"}", 100, catID).Scan(&recipients); err != nil {
		t.Fatal(err)
	}
	if recipients != 0 || e.count(`SELECT count(*) FROM ap_deliveries WHERE inbox = $1`, catID+"/inbox") != before {
		t.Errorf("a post of ana's would go to cat: %d", recipients)
	}

	// Lifted: what it wrote shows again; the follows do not come back.
	e.mustAct(e.admin, "unsuspend", e.catID, "")
	if got, _ := e.seen(e.ana); !slices.Contains(got, "hi from cat") {
		t.Errorf("after the suspension ana reads %v", got)
	}
	e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", named), nil)
	if e.state(e.catID, e.anaID) != "" || e.state(e.anaID, e.catID) != "" {
		t.Error("lifting the suspension restored the follows")
	}
	if code, _ := e.act(e.admin, "unsuspend", e.catID, ""); code != http.StatusConflict {
		t.Errorf("a second lift: %d", code)
	}
	if v := e.profile(e.ana, e.catID)["view"].(map[string]any); v["cannot"] != nil {
		t.Errorf("cat cannot be followed after the suspension: %v", v)
	}
}

// What a suspended account sends: each type is dropped at the door (202, nothing stored) but a Delete and an Undo, which
// only take away and are acted on; a row stored before the suspension is dropped by the worker.
func testRemoteSuspendedSends(t *testing.T) {
	e := newHeldEnv(t)
	e.send(e.cat(), catID, "Create", reply(catID, 1, e.postID, "first"))
	e.deliver(activity(catID, "Like", e.postID, nil), e.cat())
	if e.count(`SELECT count(*) FROM likes WHERE user_id = $1`, e.catID) != 1 {
		t.Fatal("cat's like was not kept")
	}
	// A row stored before the suspension, handled after it.
	e.clock.add(time.Second)
	e.inboxEnv.post("/inbox", activity(catID, "Create", reply(catID, 9, e.postID, "queued before"), nil), e.cat(), nil)
	e.mustAct(e.admin, "suspend", e.catID, "spam")
	for {
		worked, err := e.q.step(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	if e.outcome() != "done:"+remoteSuspendedDrop {
		t.Errorf("a row stored before the suspension: %s", e.outcome())
	}
	if _, ok := e.kept(catID + "/statuses/9"); ok {
		t.Error("a reply stored before the suspension was kept")
	}
	rows := func() int { return e.count(`SELECT count(*) FROM ap_inbox WHERE actor_id = $1`, e.catID) }
	for _, c := range []struct {
		kind   string
		object any
	}{
		{"Create", reply(catID, 2, e.postID, "while suspended")},
		{"Update", reply(catID, 1, e.postID, "edited while suspended")},
		{"Like", e.postID},
		{"Announce", e.postID},
		{"Follow", "https://here.example/users/ana"},
		{"Accept", map[string]any{"type": "Follow", "actor": "https://here.example/users/ana", "object": catID, "id": "https://here.example/f/1"}},
		{"Block", "https://here.example/users/ana"},
	} {
		was := rows()
		if why := e.deliver(activity(catID, c.kind, c.object, nil), e.cat()); why != inDropped || rows() != was {
			t.Errorf("a %s from a suspended account: %v, %d rows stored", c.kind, why, rows()-was)
		}
	}
	if _, ok := e.kept(catID + "/statuses/2"); ok {
		t.Error("a reply from a suspended account was kept")
	}
	if c, _ := e.kept(catID + "/statuses/1"); c.body != "first" {
		t.Errorf("an Update from a suspended account changed its reply: %q", c.body)
	}
	if e.state(e.catID, e.anaID) != "" {
		t.Error("a Follow from a suspended account was written")
	}
	// An Undo of its like and a Delete of its reply are acted on.
	if why := e.deliver(activity(catID, "Undo", map[string]any{"type": "Like", "actor": catID, "object": e.postID, "id": e.likeID(e.catID)}, nil), e.cat()); why != inAccepted {
		t.Errorf("an Undo from a suspended account: %v %s", why, e.outcome())
	}
	if e.count(`SELECT count(*) FROM likes WHERE user_id = $1`, e.catID) != 0 {
		t.Errorf("the Undo of a like was not acted on: %s", e.outcome())
	}
	if why := e.deliver(activity(catID, "Delete", catID+"/statuses/1", nil), e.cat()); why != inAccepted {
		t.Errorf("a Delete from a suspended account: %v", why)
	}
	if _, ok := e.kept(catID + "/statuses/1"); ok {
		t.Errorf("the Delete of its reply was not acted on: %s", e.outcome())
	}
	// Its own Delete removes it, as from anyone.
	if why := e.deliver(activity(catID, "Delete", catID, nil), e.cat()); why != inAccepted {
		t.Errorf("its own Delete: %v", why)
	}
	if e.count(`SELECT count(*) FROM ap_actors WHERE user_id = $1 AND state = 'gone'`, e.catID) != 1 {
		t.Errorf("its own Delete was not acted on: %s", e.outcome())
	}
}

// likeID is the id the like of account uid on ana's post was kept with.
func (e *heldEnv) likeID(uid int64) string {
	e.t.Helper()
	var id string
	if err := e.a.db.QueryRow(`SELECT COALESCE(ap_id, '') FROM likes WHERE user_id = $1`, uid).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// Who may do what: a member nothing, a moderator limit and suspend, an admin purge too; a reason, an end time and the
// allowance as for any action; purge_remote refused for a member here, and the actions for members only refused for
// an account of another server, while a member here is muted as before.
func testRemoteWhoMay(t *testing.T) {
	e := newHeldEnv(t)
	for _, c := range []struct {
		tok, action string
		want        int
	}{
		{e.ana, "mute", 403}, {e.ana, "suspend", 403}, {e.ana, "purge_remote", 403},
		{e.mod, "purge_remote", 403},
		{e.mod, "revoke_badge", 400}, {e.mod, "hide_stats", 400},
	} {
		if code, out := e.act(c.tok, c.action, e.catID, "why"); code != c.want {
			t.Errorf("%s by %s: %d %v, want %d", c.action, c.tok[:4], code, out, c.want)
		}
	}
	if code, out := e.act(e.mod, "mute", e.catID, ""); code != 400 || !strings.Contains(fmt.Sprint(out["error"]), "reason") {
		t.Errorf("a limit without a reason: %d %v", code, out)
	}
	if code, _ := e.do(e.mod, "POST", "/api/mod/actions", map[string]any{"action": "suspend", "kind": "user", "id": e.catID, "reason": "x", "until": time.Now().Add(400 * 24 * time.Hour).UTC().Format(time.RFC3339)}); code != 400 {
		t.Errorf("a suspension past a year: %d", code)
	}
	if code, out := e.act(e.admin, "purge_remote", e.anaID, "x"); code != 400 || !strings.Contains(fmt.Sprint(out["error"]), "another server") {
		t.Errorf("purging a member here: %d %v", code, out)
	}
	if code, _ := e.act(e.admin, "purge_remote", 999999, "x"); code != 404 {
		t.Errorf("purging nobody: %d", code)
	}
	if code, _ := e.act(e.admin, "purge_remote", e.catID, "x"); code != http.StatusConflict {
		t.Errorf("purging an account not suspended: %d", code)
	}
	// A member here: muted as before, and told.
	e.mustAct(e.mod, "mute", e.cyID, "loud")
	if e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'moderation'`, e.cyID) != 1 || e.count(`SELECT count(*) FROM mod_actions WHERE action = 'mute' AND target_user_id = $1`, e.cyID) != 1 {
		t.Error("muting a member here is not what it was")
	}
	// The allowance: a moderator acting on accounts of other servers is held to it.
	e.a.lim.remoteActs = newLimiter(2, time.Hour)
	e.mustAct(e.mod, "mute", e.catID, "one")
	e.mustAct(e.mod, "unmute", e.catID, "")
	if code, _ := e.act(e.mod, "mute", e.catID, "three"); code != http.StatusTooManyRequests {
		t.Errorf("past the allowance: %d", code)
	}
}

// purgeFixture gives cat what a purge removes: posts with pictures on disk, replies (one answered by ana), likes of a
// post and of a reply, follows both ways, notifications, a remembered deletion and inbox rows; and a delivery waiting
// for its own inbox. It returns the files of the pictures.
func (e *heldEnv) purgeFixture(posts int) []string {
	e.t.Helper()
	e.mustExec(`INSERT INTO posts (user_id, body, ap_id, audience) SELECT $1, 'cat post ' || g, $2 || '/statuses/p' || g, 'public' FROM generate_series(1, $3::int) g`, e.catID, catID, posts)
	dir := e.a.fed.mediaDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		e.t.Fatal(err)
	}
	var files []string
	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("%032x.jpg", 0xca7000+i) // the cache's own names, which removeMedia removes
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			e.t.Fatal(err)
		}
		files = append(files, filepath.Join(dir, name))
		e.mustExec(`INSERT INTO ap_media (post_id, n, kind, url, state, file, bytes) SELECT id, 0, 'picture', 'https://social.example/p.png', 'ok', $2, 1 FROM posts WHERE ap_id = $1`, fmt.Sprintf("%s/statuses/p%d", catID, i), name)
	}
	e.send(e.cat(), catID, "Create", reply(catID, 1, e.postID, "answered"))
	e.send(e.cat(), catID, "Create", reply(catID, 2, e.postID, "alone"))
	answered, _ := e.kept(catID + "/statuses/1")
	e.comment(e.ana, e.post, answered.id, "ana answers cat")
	e.deliver(activity(catID, "Like", e.postID, nil), e.cat())
	mine := e.comment(e.ana, e.post, 0, "ana's own comment")
	e.mustExec(`INSERT INTO comment_likes (comment_id, user_id) VALUES ($1, $2)`, num(mine["id"]), e.catID)
	e.mustExec(`INSERT INTO comment_likes (comment_id, user_id) VALUES ($1, $2)`, answered.id, e.deeID)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted') ON CONFLICT DO NOTHING`, e.anaID, e.catID)
	e.mustExec(`INSERT INTO ap_deleted (ap_id, user_id, at) VALUES ($1, $2, now())`, catID+"/statuses/gone", e.catID)
	e.mustExec(`INSERT INTO ap_inbox (activity_id, actor_id, host, kind, body, received_at, next_at, state) VALUES ($1, $2, 'social.example', 'Create', NULL, now(), now(), 'done')`, catID+"/a/old", e.catID)
	e.mustExec(`UPDATE users SET display_name = 'Cat', bio = 'b' WHERE id = $1`, e.catID)
	return files
}

// what is everything a purge must leave at zero for cat, by name, as the counts are read from the tables.
func (e *heldEnv) whatCatHas() map[string]int {
	e.t.Helper()
	got := map[string]int{}
	for name, q := range map[string]string{
		"posts":                `SELECT count(*) FROM posts WHERE user_id = $1`,
		"pictures":             `SELECT count(*) FROM ap_media m JOIN posts p ON p.id = m.post_id WHERE p.user_id = $1`,
		"replies with text":    `SELECT count(*) FROM comments WHERE user_id = $1 AND NOT (deleted AND body = '')`,
		"likes":                `SELECT count(*) FROM likes WHERE user_id = $1`,
		"likes of replies":     `SELECT count(*) FROM comment_likes WHERE user_id = $1`,
		"likes on its replies": `SELECT count(*) FROM comment_likes WHERE comment_id IN (SELECT id FROM comments WHERE user_id = $1)`,
		"follows":              `SELECT count(*) FROM follows WHERE follower_id = $1 OR followee_id = $1`,
		"notifications":        `SELECT count(*) FROM notifications WHERE actor_id = $1`,
		"deletions":            `SELECT count(*) FROM ap_deleted WHERE user_id = $1`,
		"inbox rows":           `SELECT count(*) FROM ap_inbox WHERE actor_id = $1`,
		"profile":              `SELECT count(*) FROM users WHERE id = $1 AND (display_name <> '' OR bio <> '')`,
		"summary":              `SELECT count(*) FROM ap_actors WHERE user_id = $1 AND summary <> ''`,
	} {
		got[name] = e.count(q, e.catID)
	}
	return got
}

func nothingLeft(t *testing.T, got map[string]int) {
	t.Helper()
	for k, v := range got {
		if v != 0 {
			t.Errorf("after the purge, %s: %d", k, v)
		}
	}
}

// Purge: everything cat left goes, but the placeholder of its answered reply (ana's answer stays), its handle, key and
// suspension; the files of its pictures go; a delivery for its own inbox is given up; run again, it removes nothing.
func testRemotePurge(t *testing.T) {
	e := newHeldEnv(t)
	files := e.purgeFixture(3)
	e.mustAct(e.admin, "suspend", e.catID, "spam")
	if _, err := e.out.enqueue(context.Background(), instanceActor, "https://here.example/x/late", []byte(`{"type":"Like"}`), []deliveryTarget{{Actor: catID, Inbox: catID + "/inbox"}}); err != nil {
		t.Fatal(err)
	}
	out := e.mustAct(e.admin, "purge_remote", e.catID, "spam account")
	if out["done"] != true {
		t.Fatalf("the purge: %v", out)
	}
	nothingLeft(t, e.whatCatHas())
	for _, f := range files {
		if _, err := os.Stat(f); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the picture %s is still on disk", f)
		}
	}
	if got := strings.Join(bodies(e.thread(e.ana, e.post)), "|"); !strings.Contains(got, "ana answers cat") || strings.Contains(got, "answered|") || strings.Contains(got, "alone") {
		t.Errorf("the thread after the purge: %v", got)
	}
	if e.count(`SELECT count(*) FROM comments WHERE user_id = $1 AND deleted AND body = ''`, e.catID) != 1 {
		t.Error("the answered reply is not kept as a placeholder")
	}
	if e.count(`SELECT count(*) FROM users u JOIN ap_actors a ON a.user_id = u.id WHERE u.id = $1 AND u.username = 'cat@social.example' AND u.suspended_until > now() AND a.key_id <> ''`, e.catID) != 1 {
		t.Error("the purge did not keep the handle, the key and the suspension")
	}
	if e.count(`SELECT count(*) FROM ap_deliveries WHERE inbox = $1 AND state = 'queued'`, catID+"/inbox") != 0 {
		t.Error("a delivery for its inbox still waits")
	}
	if e.count(`SELECT count(*) FROM mod_actions WHERE action = 'purge_remote' AND target_user_id = $1 AND (detail->>'done')::boolean AND (detail->'removed'->>'posts')::int = 3`, e.catID) != 1 {
		t.Error("the purge's audit line does not say what went")
	}
	// Again: nothing to remove, and it says so.
	out = e.mustAct(e.admin, "purge_remote", e.catID, "again")
	n := int64(0)
	for _, v := range out["removed"].(map[string]any) {
		n += num(v)
	}
	if out["done"] != true || n != 0 {
		t.Errorf("a second purge: %v", out)
	}
	// What arrives now is refused, as the suspension stays.
	if why := e.deliver(activity(catID, "Create", reply(catID, 5, e.postID, "after"), nil), e.cat()); why != inDropped {
		t.Errorf("a reply after the purge: %v", why)
	}
}

// A purge stopped part way (a step's error, as a connection dropped) leaves what it did not reach, and run again ends
// where an uninterrupted one would. The page's call that runs out of time says more is left, and the next goes on.
func testRemotePurgeInterrupted(t *testing.T) {
	e := newHeldEnv(t)
	e.purgeFixture(7)
	e.mustAct(e.admin, "suspend", e.catID, "spam")
	steps := 0
	e.a.fed.purgeHook = func(step string, took int64) error {
		if steps++; steps == 3 {
			return errors.New("connection dropped")
		}
		return nil
	}
	got, done, err := e.a.fed.purgeRemote(context.Background(), e.catID, catID+"/inbox", time.Now().Add(time.Minute), 2)
	if err == nil || done || got["posts"] != 6 {
		t.Fatalf("the interrupted purge: %v %v %v", got, done, err)
	}
	if left := e.whatCatHas(); left["posts"] != 1 || left["likes"] == 0 {
		t.Errorf("after the interruption: %v", left)
	}
	e.a.fed.purgeHook = nil
	// Through the route, a budget that runs out after each statement: the page is told more is left until the end.
	e.a.fed.purgeBudget = time.Nanosecond
	calls := 0
	for {
		calls++
		out := e.mustAct(e.admin, "purge_remote", e.catID, "spam")
		if out["done"] == true {
			break
		}
		if !strings.Contains(fmt.Sprint(out["note"]), "more is left") || calls > 50 {
			t.Fatalf("a part of the purge: %v", out)
		}
	}
	if calls < 2 {
		t.Errorf("the budget did not cut the purge into parts: %d calls", calls)
	}
	nothingLeft(t, e.whatCatHas())
	if e.count(`SELECT count(*) FROM mod_actions WHERE action = 'purge_remote' AND target_user_id = $1`, e.catID) != calls {
		t.Error("each call of the purge is not its own audit line")
	}
}

// A purge of an account with thousands of posts keeps every statement within its batch: the hook sees each.
func testRemotePurgeThousands(t *testing.T) {
	e := newHeldEnv(t)
	e.mustExec(`INSERT INTO posts (user_id, body, ap_id) SELECT $1, 'p' || g, $2 || '/statuses/t' || g FROM generate_series(1, 2600) g`, e.catID, catID)
	e.mustExec(`INSERT INTO likes (post_id, user_id) SELECT id, $1 FROM posts WHERE ap_id IS NULL`, e.catID)
	e.mustExec(`UPDATE users SET suspended_until = now() + interval '1 day' WHERE id = $1`, e.catID)
	var largest int64
	statements := map[string]int{}
	e.a.fed.purgeHook = func(step string, took int64) error {
		largest = max(largest, took)
		statements[step]++
		return nil
	}
	got, done, err := e.a.fed.purgeRemote(context.Background(), e.catID, catID+"/inbox", time.Now().Add(time.Minute), remotePurgeBatch)
	if err != nil || !done || got["posts"] != 2600 {
		t.Fatalf("the purge: %v %v %v", got, done, err)
	}
	if largest > remotePurgeBatch || statements["posts"] != 2600/remotePurgeBatch+1 {
		t.Errorf("a statement took %d rows (batch %d); the posts took %d statements", largest, remotePurgeBatch, statements["posts"])
	}
	nothingLeft(t, e.whatCatHas())
}

// Two admins suspending at once, and one limiting while another suspends: each answers, each is audited, and the
// account ends suspended and limited with no follow left.
func testRemoteTwoAdmins(t *testing.T) {
	e := newHeldEnv(t)
	_, admin2 := e.user("boss2", "admin")
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, e.anaID, e.catID)
	var wg sync.WaitGroup
	codes := make([]int, 3)
	for i, c := range []struct{ tok, action string }{{e.admin, "suspend"}, {admin2, "suspend"}, {e.mod, "mute"}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], _ = e.act(c.tok, c.action, e.catID, "at once")
		}()
	}
	wg.Wait()
	if !slices.Equal(codes, []int{200, 200, 200}) {
		t.Errorf("the answers: %v", codes)
	}
	if e.count(`SELECT count(*) FROM follows WHERE follower_id = $1 OR followee_id = $1`, e.catID) != 0 {
		t.Error("a follow is left")
	}
	if e.count(`SELECT count(*) FROM mod_actions WHERE action IN ('suspend_remote', 'limit_remote') AND target_user_id = $1`, e.catID) != 3 {
		t.Error("not every action is in the audit log")
	}
	if e.count(`SELECT count(*) FROM users WHERE id = $1 AND suspended_until > now() AND muted_until > now()`, e.catID) != 1 {
		t.Error("the account is not both suspended and limited")
	}
	if e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'moderation'`, e.anaID) != 1 {
		t.Error("ana is not told once that her follow ended")
	}
}

// waitBlocked waits until a statement of this test's database waits for a lock. One that never waits is a failure,
// and the test goes on (the transaction is committed after it), so what that statement did is checked too.
func (e *heldEnv) waitBlocked() {
	e.t.Helper()
	for i := 0; i < 400; i++ {
		if e.count(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond) // the poll of a lock wait, which nothing announces
	}
	e.t.Error("waited 4 s for a statement to wait on the account's row")
}

// A Follow from cat handled while its suspension is being written, both ways round: no follow is left; and a member's
// follow of it likewise.
func testRemoteFollowRace(t *testing.T) {
	e := newHeldEnv(t)
	e.mustExec(`DELETE FROM follows WHERE follower_id = $1 AND followee_id = $2`, e.catID, e.anaID)
	// The suspension's first statement holds the account's row; the Follow, stored before, waits for it and then sees it.
	tx, err := e.a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE users SET suspended_until = now() + interval '1 day' WHERE id = $1`, e.catID); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.clock.add(time.Second)
		e.inboxEnv.post("/inbox", activity(catID, "Follow", "https://here.example/users/ana", nil), e.cat(), nil)
		for {
			if worked, _ := e.q.step(context.Background()); !worked {
				return
			}
		}
	}()
	e.waitBlocked()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	<-done
	if e.state(e.catID, e.anaID) != "" || e.outcome() != "done:"+remoteSuspendedDrop {
		t.Errorf("a Follow while the suspension was written: %q, %s", e.state(e.catID, e.anaID), e.outcome())
	}
	e.mustExec(`UPDATE users SET suspended_until = NULL WHERE id = $1`, e.catID)

	// The other way: a Follow's statement holds the row (as followInSQL does) and the suspension waits, then ends it.
	tx, err = e.a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`SELECT 1 FROM users WHERE id = $1 FOR SHARE`, e.catID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, e.catID, e.anaID); err != nil {
		t.Fatal(err)
	}
	codes := make(chan int, 1)
	go func() { c, _ := e.act(e.admin, "suspend", e.catID, "race"); codes <- c }()
	e.waitBlocked()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if c := <-codes; c != 200 || e.state(e.catID, e.anaID) != "" {
		t.Errorf("a suspension written while a Follow was: %d, the follow %q", c, e.state(e.catID, e.anaID))
	}
	e.mustAct(e.admin, "unsuspend", e.catID, "")

	// A member's follow of it, while the suspension holds the row: refused once it commits.
	tx, err = e.a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE users SET suspended_until = now() + interval '1 day' WHERE id = $1`, e.catID); err != nil {
		t.Fatal(err)
	}
	go func() {
		c, _ := e.do(e.ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/follow", e.catID), nil)
		codes <- c
	}()
	e.waitBlocked()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if c := <-codes; c != 403 || e.state(e.anaID, e.catID) != "" {
		t.Errorf("a member's follow while the suspension was written: %d, %q", c, e.state(e.anaID, e.catID))
	}
}

// An account moving between a limited domain and a limit of its own: hidden while either holds, shown when neither.
func testRemoteDomainAndAccount(t *testing.T) {
	e := newHeldEnv(t)
	e.send(e.cat(), catID, "Create", reply(catID, 1, e.postID, "hi from cat"))
	shown := func() bool { got, _ := e.seen(e.ana); return slices.Contains(got, "hi from cat") }
	domain := func(policy string) {
		e.must(200, e.admin, "PUT", "/api/admin/federation/domains/social.example", map[string]any{"policy": policy})
	}
	domain("limit")
	e.mustAct(e.mod, "mute", e.catID, "both")
	domain("allow")
	if shown() {
		t.Error("the account's own limit does not hold once its domain is allowed")
	}
	domain("limit")
	e.mustAct(e.mod, "unmute", e.catID, "")
	if shown() {
		t.Error("the domain's limit does not hold once the account's is lifted")
	}
	domain("allow")
	if !shown() {
		t.Error("neither limited, the reply is still hidden")
	}
	if e.a.fed.policyNow().limiting(time.Now()) {
		t.Error("with neither limited, the statements keep the look")
	}
}

// An account kept under a host written with upper case and a trailing dot (as one stored before the cache refused
// them could be) is acted on by its id like any: the look reads the account's row, not its domain.
func testRemoteOddHost(t *testing.T) {
	e := newHeldEnv(t)
	odd := e.remoteAccount("odd", "Odd.Example.")
	e.mustAct(e.mod, "mute", odd, "odd")
	var free bool
	if err := e.a.db.QueryRow(`SELECT `+limitFree("$1::bigint", "$2::bigint"), odd, e.anaID).Scan(&free); err != nil || free {
		t.Errorf("the limited account of an odd host is shown: %v %v", free, err)
	}
	list := e.must(200, e.mod, "GET", "/api/admin/federation/accounts", nil)["accounts"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["handle"] != "odd@Odd.Example." {
		t.Errorf("the list: %v", list)
	}
	e.mustAct(e.admin, "suspend", odd, "odd")
	e.mustAct(e.admin, "purge_remote", odd, "odd")
}

// With no domain limited and no account limited or suspended, the statements are those of before, byte for byte; with
// one account held, they carry the look; once its end passes, the next read of the policy takes the look out again.
func testRemoteStatements(t *testing.T) {
	e := newHeldEnv(t)
	for name, q := range map[string]string{"feed": feedSQL, "post": postOneSQL, "home": homeSQL, "preview": commentsSQL(feedCommentsSQL)} {
		if e.a.localStatements(q) != unlimited(q) {
			t.Errorf("%s: an instance that holds nobody runs the look", name)
		}
	}
	e.mustAct(e.mod, "mute", e.catID, "x")
	if e.a.localStatements(feedSQL) != feedSQL {
		t.Error("with an account limited, the statements run without the look")
	}
	e.mustExec(`UPDATE users SET muted_until = now() - interval '1 second' WHERE id = $1`, e.catID)
	if err := e.a.fed.loadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.a.localStatements(feedSQL) != unlimited(feedSQL) {
		t.Error("after the limit ended, the statements keep the look")
	}
	// The policy in memory holds the end, so the look goes when it passes without another read.
	p := *e.a.fed.policyNow()
	p.heldUntil = time.Now().Add(-time.Second)
	if p.limiting(time.Now()) {
		t.Error("a held account whose end passed keeps the look")
	}
}

// The Federation tab's list (paged, for staff), and a report about a reply of cat's in the queue, its author marked as
// an account of another server.
func testRemoteListAndQueue(t *testing.T) {
	e := newHeldEnv(t)
	for i := 0; i < remoteHeldPage+5; i++ {
		id := e.remoteAccount(fmt.Sprintf("s%d", i), "third.example")
		e.mustExec(`UPDATE users SET suspended_until = now() + interval '1 day', suspend_reason = 'spam' WHERE id = $1`, id)
	}
	e.mustExec(`UPDATE users SET muted_until = now() - interval '1 day' WHERE id = $1`, e.boUser) // ended: not listed
	out := e.must(200, e.mod, "GET", "/api/admin/federation/accounts", nil)
	first := out["accounts"].([]any)
	if len(first) != remoteHeldPage || num(out["next"]) == 0 {
		t.Fatalf("the first page: %d, next %v", len(first), out["next"])
	}
	rest := e.must(200, e.mod, "GET", fmt.Sprintf("/api/admin/federation/accounts?after=%d", num(out["next"])), nil)["accounts"].([]any)
	if len(rest) != 5 || rest[0].(map[string]any)["suspendReason"] != "spam" {
		t.Errorf("the second page: %v", rest)
	}
	e.must(403, e.ana, "GET", "/api/admin/federation/accounts", nil)

	e.send(e.cat(), catID, "Create", reply(catID, 1, e.postID, "rude"))
	c, _ := e.kept(catID + "/statuses/1")
	e.must(200, e.ana, "POST", "/api/reports", map[string]any{"kind": "comment", "id": c.id, "reason": "harassment"})
	items := e.must(200, e.mod, "GET", "/api/mod/queue", nil)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["author"].(map[string]any)["remote"] != true || items[0].(map[string]any)["author"].(map[string]any)["username"] != "cat@social.example" {
		t.Errorf("the queue: %v", items)
	}
	// The local accounts in the queue are not marked.
	cmt := e.comment(e.cy, e.post, 0, "a comment of cy's")
	e.must(200, e.ana, "POST", "/api/reports", map[string]any{"kind": "comment", "id": num(cmt["id"]), "reason": "spam"})
	for _, it := range e.must(200, e.mod, "GET", "/api/mod/queue", nil)["items"].([]any) {
		a := it.(map[string]any)["author"].(map[string]any)
		if (a["username"] == "cyd") == (a["remote"] == true) {
			t.Errorf("an author marked wrongly: %v", a)
		}
	}
}

// In local mode the route is what it was: purge_remote is an unknown action, and nothing of these permissions is
// listed.
func TestDBRemoteActionsLocal(t *testing.T) {
	e := newEnv(t)
	_, admin := e.user("boss", "admin")
	var remote int64
	if err := e.a.db.QueryRow(`INSERT INTO users (username, pw_hash, domain) VALUES ('r@x.example', '!', 'x.example') RETURNING id`).Scan(&remote); err != nil {
		t.Fatal(err)
	}
	if code, out := e.do(admin, "POST", "/api/mod/actions", map[string]any{"action": "purge_remote", "kind": "user", "id": remote, "reason": "x"}); code != 400 || out["error"] != "Unknown action." {
		t.Errorf("purge_remote in local mode: %d %v", code, out)
	}
	if strings.Contains(fmt.Sprint(e.must(200, admin, "GET", "/api/me", nil)["perms"]), permFedAccounts) {
		t.Error("local mode lists federation.accounts")
	}
}
