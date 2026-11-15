package main

// Posts from followed accounts in Home (FWR 5, docs/federation.md 6.3, 6.6, 6.7, 9.2 to 9.4): Create, Update and
// Delete of a post and Announce of a local one, through the real inbox (signature, decoder, origin rule, queue) and
// its worker, with what each member then sees in Home, the Community timeline, a post opened alone and the bell.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lib/pq"
)

const catID = "https://social.example/users/cat" // a second account of bo's server
const eveID = "https://other.example/users/eve"  // an account of another server

// notesInEnv is followEnv with ana (who shares her profile and follows bo, accepted), cy (a member who follows
// nobody), cat beside bo on social.example and eve on other.example, each with a key of their own.
type notesInEnv struct {
	*followEnv
	anaID, cyID, boUser int64
	ana, cy             string
}

func newNotesInEnv(t *testing.T) *notesInEnv {
	t.Helper()
	f := newFollowEnv(t)
	f.a.fed.media.manual = true
	f.allow("other.example")
	e := &notesInEnv{followEnv: f}
	e.anaID, e.ana = f.member("ana", false)
	e.cyID, e.cy = f.user("cyd", "user")
	f.mustExec(`UPDATE users SET federates = true WHERE id = $1`, e.cyID) // cyd shares their profile, so a post may name them
	f.w.put(catID, worldDoc{body: actorDoc(catID, "cat", "Cat", &f.other.PublicKey, nil)})
	f.w.put(eveID, worldDoc{body: actorDoc(eveID, "eve", "Eve", &f.other.PublicKey, nil)})
	ra, err := f.a.fed.remoteActor(context.Background(), boID)
	if err != nil {
		t.Fatal(err)
	}
	e.boUser = ra.UserID
	f.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, e.anaID, e.boUser)
	return e
}

func (e *notesInEnv) cat() signer { return e.signer(e.other, catID+"#main-key", sigCavage) }
func (e *notesInEnv) eve() signer { return e.signer(e.other, eveID+"#main-key", sigCavage) }

// noteObj is a post of bo's: public, with the given number and content, and more applied last (nil removes).
func noteObj(n int, content string, more map[string]any) map[string]any {
	o := map[string]any{"id": fmt.Sprintf("%s/statuses/%d", boID, n), "type": "Note", "attributedTo": boID, "content": content,
		"to": []string{asPublic}, "cc": []string{boID + "/followers"}, "url": fmt.Sprintf("https://social.example/@bo/%d", n)}
	for k, v := range more {
		if v == nil {
			delete(o, k)
		} else {
			o[k] = v
		}
	}
	return o
}

// send delivers an activity of kind by actor, signed by sign, and runs the worker.
func (e *notesInEnv) send(sign signer, actor, kind string, object any) inboxReason {
	e.t.Helper()
	return e.deliver(activity(actor, kind, object, nil), sign)
}

// home is the ids and bodies of the posts in a member's Home.
func (e *notesInEnv) home(tok string) []string {
	e.t.Helper()
	var out []string
	for _, p := range e.must(200, tok, "GET", "/api/home", nil)["posts"].([]any) {
		out = append(out, p.(map[string]any)["body"].(string))
	}
	return out
}

func (e *notesInEnv) remotePostID(apID string) int64 {
	e.t.Helper()
	var id int64
	if err := e.a.db.QueryRow(`SELECT id FROM posts WHERE ap_id = $1`, apID).Scan(&id); err != nil {
		e.t.Fatalf("%s is not kept: %v", apID, err)
	}
	return id
}

func TestDBRemoteNotes(t *testing.T) {
	e := newNotesInEnv(t)
	ctx := context.Background()
	kept := func() int { return e.count(`SELECT count(*) FROM posts WHERE ap_id IS NOT NULL`) }

	// A post of an account ana follows: kept, in ana's Home with bo's handle and the link to the original, and in
	// nobody else's Home, not the Community timeline, not opened alone by cy, not on any public page or count.
	if why := e.send(e.bo(), boID, "Create", noteObj(1, "<p>Hello <b>flow</b> world</p>", nil)); why != inAccepted || e.outcome() != "done:" {
		t.Fatalf("a followed account's post: %v %s", why, e.outcome())
	}
	p1 := e.remotePostID(boID + "/statuses/1")
	if h := e.home(e.ana); !slices.Equal(h, []string{"Hello flow world"}) {
		t.Errorf("ana's Home: %q", h)
	}
	homeOut := e.must(200, e.ana, "GET", "/api/home", nil)
	got := homeOut["posts"].([]any)[0].(map[string]any)
	if r, _ := got["remote"].(map[string]any); r == nil || r["link"] != "https://social.example/@bo/1" {
		t.Errorf("the post as ana's Home shows it: %v", got)
	}
	if u := homeOut["users"].(map[string]any)[fmt.Sprint(e.boUser)].(map[string]any); u["remote"].(map[string]any)["handle"] != "bo@social.example" {
		t.Errorf("its author: %v", u)
	}
	if h := e.home(e.cy); len(h) != 0 {
		t.Errorf("cy's Home shows %q", h)
	}
	for _, tok := range []string{e.ana, e.cy} {
		if tl := e.must(200, tok, "GET", "/api/posts", nil)["posts"].([]any); len(tl) != 0 {
			t.Errorf("the Community timeline shows %d remote posts", len(tl))
		}
	}
	e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", p1), nil)
	e.must(404, e.cy, "GET", fmt.Sprintf("/api/posts/%d", p1), nil)
	// Since FWR 6 ana may like it (and take the like back), and cy, who cannot read it, may not (replies_test.go holds
	// the rest); it is never bookmarked, since the bookmarks list holds posts of this server; cy cannot even report what
	// cy cannot read.
	if out := e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/like", p1), nil); out["liked"] != true {
		t.Errorf("ana's like of a remote post: %v", out)
	}
	if out := e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/like", p1), nil); out["liked"] != false {
		t.Errorf("ana's unlike of a remote post: %v", out)
	}
	e.must(404, e.cy, "POST", fmt.Sprintf("/api/posts/%d/like", p1), nil)
	e.must(404, e.cy, "POST", fmt.Sprintf("/api/posts/%d/comments", p1), map[string]any{"body": "hi"})
	e.must(404, e.ana, "POST", "/api/bookmarks", map[string]any{"kind": "post", "ref": fmt.Sprint(p1)})
	e.must(404, e.cy, "POST", "/api/reports", map[string]any{"kind": "post", "id": p1, "reason": "spam"})
	e.must(200, e.ana, "POST", "/api/reports", map[string]any{"kind": "post", "id": p1, "reason": "spam"})
	var nodeinfo map[string]any
	if code, body, _ := e.visit("GET", "/nodeinfo/2.1", "", 1, nil); code != 200 || json.Unmarshal([]byte(body), &nodeinfo) != nil {
		t.Fatalf("nodeinfo: %d", code)
	}
	if n := nodeinfo["usage"].(map[string]any)["localPosts"].(float64); n != 0 {
		t.Errorf("NodeInfo counts %v local posts, with one remote post kept", n)
	}
	if code, body, _ := e.visit("GET", "/users/ana/outbox?page=true", "", 1, map[string]string{"Accept": "application/activity+json"}); code != 200 || strings.Contains(body, "Hello") {
		t.Errorf("ana's outbox: %d %.300s", code, body)
	}

	// The same post again under another activity id: one row.
	if e.send(e.bo(), boID, "Create", noteObj(1, "<p>Hello again</p>", nil)); kept() != 1 || e.outcome() != "done: a repeat, deleted before it came, or of a domain suspended meanwhile" {
		t.Errorf("a repeat: %d kept, %s", kept(), e.outcome())
	}

	// Mentions: a short remote @anna becomes @anna@social.example and notifies nobody; the local anna is never told;
	// cy, named by a Mention whose href is cy's actor id, is told, and may open the post from the bell.
	annaID, anna := e.user("anna", "user")
	b, _ := os.ReadFile("testdata/federation/notes/mention.json")
	e.deliver(b, e.bo())
	pm := e.remotePostID(boID + "/statuses/109")
	var body string
	e.a.db.QueryRow(`SELECT body FROM posts WHERE id = $1`, pm).Scan(&body)
	if body != "Thanks @anna@social.example and @cyd@here.example! Also @anna, written plainly." {
		t.Errorf("the mentions as kept: %q", body)
	}
	if n := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1`, annaID); n != 0 {
		t.Errorf("the local anna was told %d times of a remote @anna", n)
	}
	if e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'mention' AND post_id = $2 AND actor_id = $3`, e.cyID, pm, e.boUser) != 1 {
		t.Error("cy was not told of the mention")
	}
	e.must(200, e.cy, "GET", fmt.Sprintf("/api/posts/%d", pm), nil)
	e.must(404, anna, "GET", fmt.Sprintf("/api/posts/%d", pm), nil)
	if h := e.home(e.cy); len(h) != 0 {
		t.Errorf("a mention put the post in cy's Home: %q", h)
	}

	// An account nobody here follows, mentioning nobody: dropped and counted.
	e.send(e.cat(), catID, "Create", map[string]any{"id": catID + "/statuses/1", "type": "Note", "attributedTo": catID, "content": "hi", "to": []string{asPublic}})
	if e.outcome() != "done: dropped (nobody here follows the account)" || e.count(`SELECT count(*) FROM posts WHERE ap_id = $1`, catID+"/statuses/1") != 0 {
		t.Errorf("a post of an account nobody follows: %s", e.outcome())
	}

	// The origin rule on the object, not only the activity: bo cannot create a post on another origin, nor one
	// attributed to someone else; the inbox drops both, and the worker refuses one that reached it.
	for name, o := range map[string]map[string]any{
		"an id on another origin":     noteObj(2, "x", map[string]any{"id": "https://other.example/statuses/2"}),
		"attributed to another":       noteObj(3, "x", map[string]any{"attributedTo": catID}),
		"attributed to two":           noteObj(4, "x", map[string]any{"attributedTo": []string{boID, catID}}),
		"given by id only, unfetched": {"id": boID + "/statuses/5"},
	} {
		var obj any = o
		if len(o) == 1 {
			obj = o["id"]
		}
		if why := e.send(e.bo(), boID, "Create", obj); why != inDropped || kept() != 2 {
			t.Errorf("a Create of a post %s: %v, %d kept", name, why, kept())
		}
	}
	e.mustExec(`INSERT INTO ap_inbox (activity_id, actor_id, host, kind, body, received_at, next_at) VALUES ($1, $2, 'social.example', 'Create', $3, $4, $4)`,
		boID+"/activities/forged", e.boUser, string(activity(boID, "Create", noteObj(6, "x", map[string]any{"id": "https://other.example/statuses/6"}), map[string]any{"id": boID + "/activities/forged"})), e.clock.now())
	e.q.step(ctx)
	if e.outcome() != "done: refused (the post is not the account's own, on its origin)" || kept() != 2 {
		t.Errorf("a post on another origin past the inbox: %s", e.outcome())
	}

	// Update: ignored without updated, or not newer than the version kept; taken when newer, whole; refused from
	// another account of the same server, and dropped at the inbox from another origin.
	t0 := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	upd := func(sign signer, actor string, content string, more map[string]any) string {
		e.send(sign, actor, "Update", noteObj(1, content, more))
		var b string
		e.a.db.QueryRow(`SELECT body FROM posts WHERE id = $1`, p1).Scan(&b)
		return b
	}
	if b := upd(e.bo(), boID, "<p>edited without updated</p>", nil); b != "Hello flow world" || e.outcome() != "done: ignored (an Update without updated)" {
		t.Errorf("an Update without updated: %q %s", b, e.outcome())
	}
	if b := upd(e.bo(), boID, "<p>edited</p>", map[string]any{"updated": t0.Format(time.RFC3339)}); b != "edited" {
		t.Errorf("an Update with updated: %q %s", b, e.outcome())
	}
	if b := upd(e.bo(), boID, "<p>older</p>", map[string]any{"updated": t0.Add(-time.Hour).Format(time.RFC3339)}); b != "edited" || e.outcome() != "done: ignored (not newer than the version kept)" {
		t.Errorf("an older Update: %q %s", b, e.outcome())
	}
	if b := upd(e.bo(), boID, "<p>same</p>", map[string]any{"updated": t0.Format(time.RFC3339)}); b != "edited" {
		t.Errorf("an Update as old as the one kept: %q", b)
	}
	// cat is followed here too (by cyd), so its Update reaches the check of who wrote the post.
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) SELECT $1, user_id, 'accepted' FROM ap_actors WHERE ap_id = $2`, e.cyID, catID)
	if b := upd(e.cat(), catID, "<p>cat was here</p>", map[string]any{"attributedTo": catID, "updated": t0.Add(time.Hour).Format(time.RFC3339)}); b != "edited" || e.outcome() != "done: refused (the post is another account's)" {
		t.Errorf("an Update by another account of the same server: %q %s", b, e.outcome())
	}
	e.send(e.eve(), eveID, "Update", noteObj(1, "<p>eve</p>", map[string]any{"attributedTo": eveID, "updated": t0.Add(time.Hour).Format(time.RFC3339)}))
	if e.last() != inDropped {
		t.Errorf("an Update of bo's post from another origin: %v", e.last())
	}
	// An Update of a recent post never kept is taken as its Create would be.
	if e.send(e.bo(), boID, "Update", noteObj(7, "<p>first seen edited</p>", map[string]any{"updated": t0.Format(time.RFC3339), "published": e.clock.now().Add(-time.Hour).Format(time.RFC3339)})); e.count(`SELECT count(*) FROM posts WHERE ap_id = $1`, boID+"/statuses/7") != 1 {
		t.Errorf("an Update of a post never kept, of a followed account: %s", e.outcome())
	}

	// Visibility (6.7): a direct message is dropped; a followers-only post is read by followers and by a member its
	// addressing names; one naming a member only in a tag, not in its addressing, is not read by them.
	b, _ = os.ReadFile("testdata/federation/notes/direct.json")
	e.deliver(b, e.bo())
	if e.outcome() != "done: dropped (a direct message)" || e.count(`SELECT count(*) FROM posts WHERE ap_id = $1`, boID+"/statuses/108") != 0 {
		t.Errorf("a direct message: %s", e.outcome())
	}
	e.send(e.bo(), boID, "Create", noteObj(8, "<p>for followers @cy</p>", map[string]any{"to": []string{boID + "/followers"}, "cc": nil,
		"tag": []any{map[string]any{"type": "Mention", "href": "https://here.example/users/cyd", "name": "@cyd"}}}))
	p8 := e.remotePostID(boID + "/statuses/8")
	e.must(404, e.cy, "GET", fmt.Sprintf("/api/posts/%d", p8), nil)
	if e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND post_id = $2`, e.cyID, p8) != 0 {
		t.Error("cy was told of a followers-only post that is not addressed to cy")
	}
	e.send(e.bo(), boID, "Create", noteObj(9, "<p>for followers and @cy</p>", map[string]any{"to": []string{boID + "/followers"}, "cc": []string{"https://here.example/users/cyd"},
		"tag": []any{map[string]any{"type": "Mention", "href": "https://here.example/users/cyd", "name": "@cyd"}}}))
	e.must(200, e.cy, "GET", fmt.Sprintf("/api/posts/%d", e.remotePostID(boID+"/statuses/9")), nil)

	// Delete: from another origin dropped at the inbox, by another account refused, by bo the post goes with its
	// notifications and pictures; a Delete before its Create is remembered and the late Create refused.
	e.send(e.eve(), eveID, "Delete", boID+"/statuses/109")
	if e.last() != inDropped || e.count(`SELECT count(*) FROM posts WHERE id = $1`, pm) != 1 {
		t.Errorf("a Delete from another origin: %v", e.last())
	}
	e.send(e.cat(), catID, "Delete", boID+"/statuses/109")
	if e.outcome() != "done: refused (the post is another account's)" || e.count(`SELECT count(*) FROM posts WHERE id = $1`, pm) != 1 {
		t.Errorf("a Delete by another account: %s", e.outcome())
	}
	e.mustExec(`INSERT INTO ap_media (post_id, n, kind, url, state, file, bytes) VALUES ($1, 0, 'picture', 'https://files.social.example/x.png', 'ok', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.jpg', 3)`, pm)
	media := e.a.fed.media.dir
	os.MkdirAll(media, 0o755)
	os.WriteFile(filepath.Join(media, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.jpg"), []byte("jpg"), 0o644)
	e.send(e.bo(), boID, "Delete", map[string]any{"id": boID + "/statuses/109", "type": "Tombstone"})
	if e.outcome() != "done:" || e.count(`SELECT count(*) FROM posts WHERE id = $1`, pm)+e.count(`SELECT count(*) FROM notifications WHERE post_id = $1`, pm)+e.count(`SELECT count(*) FROM ap_media WHERE post_id = $1`, pm) != 0 {
		t.Errorf("bo's Delete: %s", e.outcome())
	}
	if _, err := os.Stat(filepath.Join(media, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.jpg")); err == nil {
		t.Error("the deleted post's picture is still on disk")
	}
	e.send(e.bo(), boID, "Delete", boID+"/statuses/10")
	e.send(e.bo(), boID, "Create", noteObj(10, "<p>too late</p>", nil))
	if e.count(`SELECT count(*) FROM posts WHERE ap_id = $1`, boID+"/statuses/10") != 0 {
		t.Error("a Create after its Delete was kept")
	}

	// A post with pictures: the first four fetchable ones wanted by the cache, the rest links; none fetched here.
	b, _ = os.ReadFile("testdata/federation/notes/attachments.json")
	e.deliver(b, e.bo())
	pa := e.remotePostID(boID + "/statuses/103")
	var states []string
	e.a.db.QueryRow(`SELECT array_agg(state || ':' || kind ORDER BY n) FROM ap_media WHERE post_id = $1`, pa).Scan(pq.Array(&states))
	if !slices.Equal(states, []string{"wanted:picture", "link:picture", "link:video", "link:audio", "wanted:picture"}) {
		t.Errorf("the attachments: %v", states)
	}
	out := e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", pa), nil)["posts"].([]any)[0].(map[string]any)
	ms := out["remote"].(map[string]any)["media"].([]any)
	if len(ms) != 5 || ms[0].(map[string]any)["src"] != nil || ms[0].(map[string]any)["alt"] != "Two poi in a fire circle" || ms[2].(map[string]any)["link"] != "https://files.social.example/media/c.mp4" {
		t.Errorf("the attachments as shown: %v", ms)
	}

	// Announce of ana's Public post: one share notification however often; of anything else, dropped.
	pub := num(e.must(200, e.ana, "POST", "/api/posts", map[string]any{"body": "my public post", "audience": "public"})["posts"].([]any)[0].(map[string]any)["id"])
	for range 2 {
		e.send(e.bo(), boID, "Announce", fmt.Sprintf("https://here.example/users/ana/posts/%d", pub))
	}
	if e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'share' AND post_id = $2 AND actor_id = $3`, e.anaID, pub, e.boUser) != 1 {
		t.Error("ana was not told once that bo shared her post")
	}
	if why := e.send(e.bo(), boID, "Announce", catID+"/statuses/1"); why != inDropped {
		t.Errorf("an Announce of another server's post: %v", why)
	}
	local := num(e.must(200, e.ana, "POST", "/api/posts", map[string]any{"body": "this server only"})["posts"].([]any)[0].(map[string]any)["id"])
	e.send(e.bo(), boID, "Announce", fmt.Sprintf("https://here.example/users/ana/posts/%d", local))
	if e.outcome() != "done: dropped (not a Public post of a member here)" {
		t.Errorf("an Announce of a This server post: %s", e.outcome())
	}

	// The switches: follows off drops a Create at the inbox; switches that cannot be read are 503, never a drop,
	// and a row that waits is tried again by the worker.
	e.setFlag("follows", false)
	if why := e.send(e.bo(), boID, "Create", noteObj(11, "off", nil)); why != inDropped {
		t.Errorf("follows off: %v", why)
	}
	e.setFlag("follows", true)
	e.a.fed.followsOn = func() (bool, bool) { return false, false }
	if why := e.send(e.bo(), boID, "Create", noteObj(12, "unread", nil)); why != inFlags {
		t.Errorf("switches unread at the inbox: %v", why)
	}
	e.mustExec(`INSERT INTO ap_inbox (activity_id, actor_id, host, kind, body, received_at, next_at) VALUES ($1, $2, 'social.example', 'Create', $3, $4, $4)`,
		boID+"/activities/unread", e.boUser, string(activity(boID, "Create", noteObj(13, "x", nil), map[string]any{"id": boID + "/activities/unread"})), e.clock.now())
	e.q.step(ctx)
	if s := e.outcome(); s != "queued:the switches could not be read" {
		t.Errorf("switches unread at the worker: %s", s)
	}
	e.a.fed.followsOn = e.a.followsKnown
	e.clock.add(time.Hour)
	e.q.step(ctx)
	if e.count(`SELECT count(*) FROM posts WHERE ap_id = $1`, boID+"/statuses/13") != 1 {
		t.Errorf("the row that waited for the switches: %s", e.outcome())
	}

	// A block: ana blocks bo, which ends her follow, so bo's next post is not kept and its posts leave her Home.
	e.must(200, e.ana, "PUT", fmt.Sprintf("/api/fed/accounts/%d/block", e.boUser), nil)
	if h := e.home(e.ana); !slices.Equal(h, []string{"this server only", "my public post"}) {
		t.Errorf("after the block ana's Home shows %q", h)
	}
	e.send(e.bo(), boID, "Create", noteObj(14, "<p>after the block</p>", nil))
	if e.outcome() != "done: dropped (nobody here follows the account)" {
		t.Errorf("a post after the only follower blocked: %s", e.outcome())
	}
	// A block hides from the blocker what another member's follow keeps: cy follows bo, ana's block still holds.
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, e.cyID, e.boUser)
	e.send(e.bo(), boID, "Create", noteObj(15, "<p>for cy</p>", nil))
	if h := e.home(e.cy); !slices.Contains(h, "for cy") {
		t.Errorf("cy's Home: %q", h)
	}
	e.must(404, e.ana, "GET", fmt.Sprintf("/api/posts/%d", e.remotePostID(boID+"/statuses/15")), nil)
}

// The bounds: a post per account past remotePostsPerAccount drops that account's oldest, a sending host's posts past
// its allowance in an hour are dropped and counted, and housekeeping removes posts past their retention, past the
// cap in all, and the files no row names, a batch at a time.
func TestDBRemoteNotesBounds(t *testing.T) {
	e := newNotesInEnv(t)
	e.mustExec(`INSERT INTO posts (user_id, body, ap_id) SELECT $1, 'old ' || g, $2 || '/old/' || g FROM generate_series(1, $3) g`, e.boUser, boID, remotePostsPerAccount)
	e.send(e.bo(), boID, "Create", noteObj(1, "<p>new</p>", nil))
	if n := e.count(`SELECT count(*) FROM posts WHERE user_id = $1`, e.boUser); n != remotePostsPerAccount {
		t.Errorf("bo has %d posts kept, want %d", n, remotePostsPerAccount)
	}
	if e.count(`SELECT count(*) FROM posts WHERE ap_id = $1`, boID+"/old/1") != 0 || e.count(`SELECT count(*) FROM posts WHERE ap_id = $1`, boID+"/statuses/1") != 1 {
		t.Error("the oldest did not make room for the newest")
	}
	e.a.fed.notesIn = newLimiter(1, time.Hour)
	e.send(e.bo(), boID, "Create", noteObj(2, "<p>one</p>", nil))
	e.send(e.bo(), boID, "Create", noteObj(3, "<p>two</p>", nil))
	if e.outcome() != "done: dropped (its server sent more posts this hour than are kept)" || e.count(`SELECT count(*) FROM posts WHERE ap_id = $1`, boID+"/statuses/3") != 0 {
		t.Errorf("past the host's allowance: %s", e.outcome())
	}
	// Retention: the 200 kept are made old; one is reported, so it stays; a picture of one goes with it, and a file
	// no row names (older than a stop's write could be) goes too, while a new one stays.
	e.mustExec(`UPDATE posts SET created_at = now() - interval '31 days' WHERE ap_id IS NOT NULL`)
	keep := e.remotePostID(boID + "/statuses/1")
	e.mustExec(`INSERT INTO reports (reporter_id, target_kind, target_id, reason) VALUES ($1, 'post', $2, 'spam')`, e.anaID, keep)
	dir := e.a.fed.media.dir
	os.MkdirAll(dir, 0o755)
	gone, orphan, fresh := strings.Repeat("b", 32)+".jpg", strings.Repeat("c", 32)+".jpg", strings.Repeat("d", 32)+".jpg"
	for _, f := range []string{gone, orphan, fresh} {
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(dir, orphan), old, old)
	e.mustExec(`INSERT INTO ap_media (post_id, n, kind, url, state, file, bytes) SELECT id, 0, 'picture', 'https://x.example/a.png', 'ok', $1, 1 FROM posts WHERE ap_id = $2`, gone, boID+"/old/100")
	n, err := e.a.remoteHousekeep()
	if err != nil {
		t.Fatal(err)
	}
	if n != remotePostsPerAccount-1 || e.count(`SELECT count(*) FROM posts WHERE ap_id IS NOT NULL`) != 1 {
		t.Errorf("retention removed %d, %d left", n, e.count(`SELECT count(*) FROM posts WHERE ap_id IS NOT NULL`))
	}
	for f, want := range map[string]bool{gone: false, orphan: false, fresh: true} {
		if _, err := os.Stat(filepath.Join(dir, f)); (err == nil) != want {
			t.Errorf("%s on disk: %v, want %v", f, err == nil, want)
		}
	}
	// The cap in all: 30 kept and new, with the cap at 25 for this run, leaves the newest 25.
	e.mustExec(`DELETE FROM reports`)
	e.mustExec(`INSERT INTO posts (user_id, body, ap_id, created_at) SELECT $1, 'n ' || g, $2 || '/n/' || g, now() - (30 - g) * interval '1 minute' FROM generate_series(1, 30) g`, e.boUser, boID)
	var removed int
	var files []string
	if err := e.a.db.QueryRow(remoteCapSQL, 25, remoteBatch).Scan(&removed, pq.Array(&files)); err != nil {
		t.Fatal(err)
	}
	if left := e.count(`SELECT count(*) FROM posts WHERE ap_id IS NOT NULL`); left != 25 || e.count(`SELECT count(*) FROM posts WHERE ap_id = $1`, boID+"/n/30") != 1 {
		t.Errorf("past the cap %d are left", left)
	}
	// The remembered deletions go after 7 days.
	e.mustExec(`INSERT INTO ap_deleted (ap_id, user_id, at) VALUES ('https://social.example/x', $1, now() - interval '8 days')`, e.boUser)
	if _, err := e.a.remoteHousekeep(); err != nil || e.count(`SELECT count(*) FROM ap_deleted`) != 0 {
		t.Errorf("a remembered deletion past 7 days: %v", err)
	}
}

/* ---------- the picture cache ---------- */

// pngOf is a PNG of w by h px of one colour; huge declares w by h in its header but holds no pixel data.
func pngOf(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 7)
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func hugePNG(w, h int) []byte {
	b := pngOf(1, 1)
	// The IHDR chunk's width and height, and its CRC made right again, so DecodeConfig believes it.
	out := slices.Clone(b)
	be := func(off, v int) {
		out[off], out[off+1], out[off+2], out[off+3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
	}
	be(16, w)
	be(20, h)
	crc := crc32.ChecksumIEEE(out[12:29])
	be(29, int(crc))
	return out
}

// The cache's own copy of a picture, without a database: the size read before decoding (a small file declaring an
// enormous picture is refused unread), only JPEG, PNG and GIF as their bytes say, at most mediaSide px and
// mediaOut bytes, and nothing of the file but its pixels.
func TestMediaCacheBudget(t *testing.T) {
	bomb := hugePNG(100000, 100000)
	if len(bomb) > 100 {
		t.Fatalf("the bomb is %d bytes", len(bomb))
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(bomb)); err != nil || cfg.Width != 100000 {
		t.Fatalf("the bomb does not declare its size: %v %v", cfg, err)
	}
	if _, _, _, err := shrinkRemote(bomb); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("a small file declaring 100,000 px a side: %v", err)
	}
	if _, _, _, err := shrinkRemote(hugePNG(mediaInSide+1, 10)); err == nil {
		t.Errorf("one px over %d: decoded", mediaInSide)
	}
	for name, b := range map[string][]byte{"html": []byte("<html><script>alert(1)</script></html>"), "svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`),
		"webp": append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 32)...), "empty": nil} {
		if _, _, _, err := shrinkRemote(b); err == nil {
			t.Errorf("%s taken as a picture", name)
		}
	}
	// A big PNG with text chunks after its pixels comes back a JPEG within the bounds, the text gone.
	src := append(pngOf(3000, 1000), []byte("tEXtComment\x00secret location")...)
	out, w, h, err := shrinkRemote(src)
	if err != nil {
		t.Fatal(err)
	}
	if w != mediaSide || h != mediaSide/3 || len(out) > mediaOut || !bytes.HasPrefix(out, []byte{0xff, 0xd8}) || bytes.Contains(out, []byte("secret")) {
		t.Errorf("the copy: %dx%d, %d bytes", w, h, len(out))
	}
	// A GIF's first frame.
	g := gifOf()
	if _, _, _, err := shrinkRemote(g); err != nil {
		t.Errorf("a GIF: %v", err)
	}
	if !mediaTemporary(&fetchError{Fail: failTimeout}) || !mediaTemporary(&fetchError{Fail: failStatus, Status: 503}) || mediaTemporary(&fetchError{Fail: failRefused}) ||
		mediaTemporary(&fetchError{Fail: failType}) || mediaTemporary(&fetchError{Fail: failTooLarge}) {
		t.Error("which failures are tried again")
	}
}

// The picture cache with its database, at a budget of a few files: fetched by the worker through the guarded fetcher
// with no connection held while it waits, served from this server to who may read the post, evicted least
// recently used first by bytes and by files, the row kept so the post shows the link and the picture is wanted
// again when read; refused for a private address, a wrong type, a redirect to a private address, a picture too large.
func TestDBMediaCacheBudget(t *testing.T) {
	e := newNotesInEnv(t)
	m := e.a.fed.media
	var slow atomic.Bool
	release := make(chan struct{})
	waiting := make(chan struct{}, 1)
	pic := pngOf(400, 300)
	h := http.NewServeMux()
	h.HandleFunc("/p/", func(w http.ResponseWriter, r *http.Request) {
		if slow.Load() {
			waiting <- struct{}{}
			<-release
		}
		w.Header().Set("Content-Type", "image/jpeg") // a lie: the bytes are a PNG, and they decide
		w.Write(pic)
	})
	h.HandleFunc("/html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("<html>not a picture</html>"))
	})
	h.HandleFunc("/bomb", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(hugePNG(50000, 50000))
	})
	h.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(make([]byte, 9<<20))
	})
	h.HandleFunc("/toprivate", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://private.example/p/x.png", http.StatusFound)
	})
	f, _ := fedStub(t, h)
	f.origin = e.a.fed.origin
	m.fetch = f
	e.allow("pics.example", "private.example")
	m.maxFiles, m.lowFiles, m.maxBytes, m.lowBytes = 3, 2, 1<<30, 1<<30
	var nowMu atomic.Int64
	nowMu.Store(time.Now().Unix())
	m.now = func() time.Time { return time.Unix(nowMu.Add(1), 0) } // each read a second later: the order of use is sure

	// A post with four pictures of pics.example, read by ana (who follows bo).
	att := func(urls ...string) []any {
		var out []any
		for _, u := range urls {
			out = append(out, map[string]any{"type": "Document", "mediaType": "image/png", "url": u, "name": "alt of " + u})
		}
		return out
	}
	e.send(e.bo(), boID, "Create", noteObj(1, "four", map[string]any{"attachment": att("https://pics.example/p/1.png", "https://pics.example/p/2.png", "https://pics.example/p/3.png", "https://pics.example/p/4.png")}))
	post := e.remotePostID(boID + "/statuses/1")
	step := func() {
		t.Helper()
		if worked, err := m.step(context.Background()); !worked || err != nil {
			t.Fatalf("the worker: %v %v", worked, err)
		}
	}
	// The first fetch waits at the stub: no database connection is held meanwhile.
	slow.Store(true)
	done := make(chan struct{})
	go func() { defer close(done); m.step(context.Background()) }()
	<-waiting
	if n := e.a.db.Stats().InUse; n != 0 {
		t.Errorf("%d connections held while a picture is fetched", n)
	}
	slow.Store(false)
	close(release)
	<-done
	for range 3 {
		step()
	}
	files := func() []string {
		ents, _ := os.ReadDir(m.dir)
		var out []string
		for _, e := range ents {
			out = append(out, e.Name())
		}
		return out
	}
	// Four fetched, three files at most, two after an eviction: the first two fetched were used longest ago.
	var states []string
	e.a.db.QueryRow(`SELECT array_agg(state ORDER BY n) FROM ap_media WHERE post_id = $1`, post).Scan(pq.Array(&states))
	if !slices.Equal(states, []string{"evicted", "evicted", "ok", "ok"}) || len(files()) != 2 {
		t.Errorf("after four fetches at a budget of 3 files: %v, %d files", states, len(files()))
	}
	// Served from this server, with the headers of a picture, to ana; not to cy, who cannot read the post.
	var okID int64
	e.a.db.QueryRow(`SELECT id FROM ap_media WHERE post_id = $1 AND state = 'ok' ORDER BY n LIMIT 1`, post).Scan(&okID)
	code, body, hdr := e.visit("GET", fmt.Sprintf("/api/fed/media/%d.jpg", okID), e.ana, 1, nil)
	if code != 200 || !strings.HasPrefix(body, "\xff\xd8") || hdr.Get("Content-Type") != "image/jpeg" || hdr.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("the picture for ana: %d %q", code, hdr)
	}
	if code, _, _ := e.visit("GET", fmt.Sprintf("/api/fed/media/%d.jpg", okID), e.cy, 1, nil); code != 404 {
		t.Errorf("the picture for cy: %d", code)
	}
	// Reading the post just after the eviction does not ask for them again (a full cache would evict and fetch in turn
	// as a member scrolls); an hour later it does, and shows them as links meanwhile.
	e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", post), nil)
	if n := e.count(`SELECT count(*) FROM ap_media WHERE post_id = $1 AND state = 'wanted'`, post); n != 0 {
		t.Errorf("%d evicted pictures wanted again at once", n)
	}
	nowMu.Add(int64(mediaRewantEvery / time.Second))
	out := e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", post), nil)["posts"].([]any)[0].(map[string]any)
	ms := out["remote"].(map[string]any)["media"].([]any)
	if ms[0].(map[string]any)["src"] != nil || ms[2].(map[string]any)["src"] != fmt.Sprintf("/api/fed/media/%d.jpg", okID) || ms[0].(map[string]any)["alt"] != "alt of https://pics.example/p/1.png" {
		t.Errorf("the pictures as shown: %v", ms)
	}
	if e.count(`SELECT count(*) FROM ap_media WHERE post_id = $1 AND state = 'wanted'`, post) != 2 {
		t.Error("the evicted pictures are not wanted again")
	}
	// With mediaWantedMax waiting already, a read asks for none: at most that many ever wait.
	e.mustExec(`UPDATE ap_media SET state = 'evicted', next_at = now() - interval '2 hours' WHERE post_id = $1 AND state = 'wanted'`, post)
	e.mustExec(`INSERT INTO ap_media (post_id, n, kind, url, state) SELECT $1, 100 + g, 'picture', 'https://pics.example/f/' || g, 'wanted' FROM generate_series(1, $2) g`, post, mediaWantedMax)
	e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", post), nil)
	if n := e.count(`SELECT count(*) FROM ap_media WHERE state = 'wanted'`); n != mediaWantedMax {
		t.Errorf("%d pictures wait, more than the %d allowed", n, mediaWantedMax)
	}
	e.mustExec(`DELETE FROM ap_media WHERE post_id = $1 AND n >= 100`, post)
	e.mustExec(`UPDATE ap_media SET state = 'wanted' WHERE post_id = $1 AND state = 'evicted'`, post)
	// The budget in bytes: one picture's worth evicts down to the low mark.
	var one int
	e.a.db.QueryRow(`SELECT max(bytes) FROM ap_media WHERE state = 'ok'`).Scan(&one)
	m.maxFiles, m.lowFiles, m.maxBytes, m.lowBytes = 100, 100, int64(2*one), int64(one)
	step()
	if n := e.count(`SELECT count(*) FROM ap_media WHERE state = 'ok'`); n != 1 || len(files()) != 1 {
		t.Errorf("by bytes: %d kept, %d files", n, len(files()))
	}
	// Refused for good: a private address, a redirect to one, a body that is not a picture, a bomb, a picture over 8 MB.
	e.send(e.bo(), boID, "Create", noteObj(2, "bad", map[string]any{"attachment": att("https://private.example/p/x.png", "https://pics.example/toprivate", "https://pics.example/html", "https://pics.example/bomb")}))
	bad := e.remotePostID(boID + "/statuses/2")
	e.send(e.bo(), boID, "Create", noteObj(3, "big", map[string]any{"attachment": att("https://pics.example/big")}))
	for i := 0; i < 10; i++ {
		if worked, _ := m.step(context.Background()); !worked {
			break
		}
	}
	if n := e.count(`SELECT count(*) FROM ap_media WHERE post_id IN ($1, $2) AND state = 'failed'`, bad, e.remotePostID(boID+"/statuses/3")); n != 5 {
		var s []string
		e.a.db.QueryRow(`SELECT array_agg(url || ' ' || state) FROM ap_media WHERE post_id IN ($1, $2)`, bad, e.remotePostID(boID+"/statuses/3")).Scan(pq.Array(&s))
		t.Errorf("refused pictures: %v", s)
	}
	// A stop as the worker claims gives the picture back, its try not counted.
	e.mustExec(`UPDATE ap_media SET state = 'wanted', tries = 0, next_at = now() - interval '1 minute', locked_until = NULL WHERE post_id = $1 AND n = 0`, post)
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if worked, _ := m.step(cctx); worked {
		t.Error("a stopped worker claimed a picture")
	}
	if e.count(`SELECT count(*) FROM ap_media WHERE post_id = $1 AND n = 0 AND state = 'wanted' AND tries = 0 AND locked_until IS NULL`, post) != 1 {
		t.Error("the picture is not waiting as it was")
	}
}

/* ---------- helpers ---------- */

func gifOf() []byte {
	var b bytes.Buffer
	img := image.NewPaletted(image.Rect(0, 0, 8, 8), []color.Color{color.Black, color.White})
	gif.Encode(&b, img, nil)
	return b.Bytes()
}

// Home in a mode that federates, and everything FWR 5 runs, on a pool of one connection (TestDBOneConnection's rule):
// the inbox's delivery of a post with a picture, its worker, Home, the post alone, the picture cache's worker and its
// answer, housekeeping and a Delete. None holds a connection while it waits for another.
func TestDBRemoteNotesOneConnection(t *testing.T) {
	e := newNotesInEnv(t)
	m := e.a.fed.media
	h := http.NewServeMux()
	h.HandleFunc("/p.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngOf(64, 48))
	})
	f, _ := fedStub(t, h)
	f.origin = e.a.fed.origin
	m.fetch = f
	e.allow("pics.example")
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
	within("a Create with a picture, and its worker", func() {
		e.send(e.bo(), boID, "Create", noteObj(1, "<p>one connection</p>", map[string]any{"attachment": []any{map[string]any{"type": "Document", "mediaType": "image/png", "url": "https://pics.example/p.png", "name": "alt"}}}))
	})
	var post int64
	within("Home", func() {
		if h := e.home(e.ana); !slices.Equal(h, []string{"one connection"}) {
			t.Errorf("Home: %q", h)
		}
		post = e.remotePostID(boID + "/statuses/1")
	})
	within("the picture cache's worker", func() {
		if worked, err := m.step(context.Background()); !worked || err != nil {
			t.Errorf("the worker: %v %v", worked, err)
		}
	})
	within("the post alone and its picture", func() {
		out := e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", post), nil)["posts"].([]any)[0].(map[string]any)
		src, _ := out["remote"].(map[string]any)["media"].([]any)[0].(map[string]any)["src"].(string)
		if code, _, _ := e.visit("GET", src, e.ana, 1, nil); src == "" || code != 200 {
			t.Errorf("the picture %q: %d", src, code)
		}
	})
	within("housekeeping", func() {
		if _, err := e.a.housekeep(); err != nil {
			t.Error(err)
		}
	})
	within("a Delete", func() { e.send(e.bo(), boID, "Delete", boID+"/statuses/1") })
	if e.count(`SELECT count(*) FROM posts WHERE ap_id IS NOT NULL`)+e.count(`SELECT count(*) FROM ap_media`) != 0 {
		t.Error("the post or its picture is left")
	}
}
