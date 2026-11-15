package main

// The review of FWR 5 (PR #115): what a remote account that deletes itself or is suspended leaves, a domain's
// suspension, the memory of deletions, the picture worker at a start, who a mention may name, the cap of posts kept
// for a mention, a Create and a Delete at once, a moderator's delete, edits that take a post away from its readers,
// a full disk, the public addresses, an undecodable Delete, local mode on a database that federated, and follows
// that are pending or ended.

import (
	"context"
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

const danID = "https://social.example/users/dan" // an account of bo's server that nobody here follows

func (e *notesInEnv) dan() signer { return e.signer(e.other, danID+"#main-key", sigCavage) }

// keptCount is how many posts of the given ids are kept.
func (e *notesInEnv) kept(ids ...string) int {
	e.t.Helper()
	n := 0
	for _, id := range ids {
		n += e.count(`SELECT count(*) FROM posts WHERE ap_id = $1`, id)
	}
	return n
}

// picture gives post a cached picture whose file is on disk, and returns the file's path.
func (e *notesInEnv) picture(post int64, name string) string {
	e.t.Helper()
	dir := e.a.fed.media.dir
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, name)
	os.WriteFile(path, []byte("jpg"), 0o644)
	e.mustExec(`INSERT INTO ap_media (post_id, n, kind, url, state, file, bytes) VALUES ($1, 0, 'picture', 'https://files.social.example/x.png', 'ok', $2, 3)`, post, name)
	return path
}

func gone(path string) bool { _, err := os.Stat(path); return err != nil }

// An account its server says is suspended is not shown (Home, the post alone, its pictures) until it is lifted; one that
// deletes itself loses its posts, its pictures and its follows at once (findings 1 and 18).
func TestDBRemoteActorSuspendedOrGone(t *testing.T) {
	e := newNotesInEnv(t)
	e.send(e.bo(), boID, "Create", noteObj(1, "<p>for followers</p>", map[string]any{"to": []string{boID + "/followers"}, "cc": nil}))
	post := e.remotePostID(boID + "/statuses/1")
	file := e.picture(post, strings.Repeat("a", 32)+".jpg")
	var media int64
	e.a.db.QueryRow(`SELECT id FROM ap_media WHERE post_id = $1`, post).Scan(&media)
	src := fmt.Sprintf("/api/fed/media/%d.jpg", media)
	if h := e.home(e.ana); !slices.Equal(h, []string{"for followers"}) {
		t.Fatalf("Home before: %q", h)
	}
	e.mustExec(`UPDATE ap_actors SET state = 'suspended' WHERE user_id = $1`, e.boUser)
	if h := e.home(e.ana); len(h) != 0 {
		t.Errorf("a suspended account's posts in Home: %q", h)
	}
	e.must(404, e.ana, "GET", fmt.Sprintf("/api/posts/%d", post), nil)
	if code, _, _ := e.visit("GET", src, e.ana, 1, nil); code != 404 {
		t.Errorf("a suspended account's picture: %d", code)
	}
	e.mustExec(`UPDATE ap_actors SET state = 'ok' WHERE user_id = $1`, e.boUser)
	if h := e.home(e.ana); len(h) != 1 {
		t.Errorf("the suspension lifted, Home: %q", h)
	}
	// bo deletes itself: the posts, their pictures and the follows go in the same statement as the mark.
	e.send(e.bo(), boID, "Delete", boID)
	if e.outcome() != "done:" || e.count(`SELECT count(*) FROM posts WHERE user_id = $1`, e.boUser) != 0 || e.count(`SELECT count(*) FROM follows WHERE followee_id = $1 OR follower_id = $1`, e.boUser) != 0 ||
		e.count(`SELECT count(*) FROM ap_actors WHERE user_id = $1 AND state = 'gone'`, e.boUser) != 1 {
		t.Errorf("after bo's own Delete: %s", e.outcome())
	}
	if !gone(file) {
		t.Error("the deleted account's picture is still on disk")
	}
	if h := e.home(e.ana); len(h) != 0 {
		t.Errorf("Home after the account's deletion: %q", h)
	}
}

// Suspending a domain removes the posts its accounts left (kept for a follow or a mention), their notifications and
// pictures, and the picture route asks the policy (findings 2 and 18).
func TestDBRemoteDomainSuspended(t *testing.T) {
	e := newNotesInEnv(t)
	_, admin := e.user("boss", "admin")
	e.w.put(danID, worldDoc{body: actorDoc(danID, "dan", "Dan", &e.other.PublicKey, nil)})
	e.send(e.dan(), danID, "Create", map[string]any{"id": danID + "/statuses/1", "type": "Note", "attributedTo": danID, "content": "hi @cyd",
		"to": []string{asPublic}, "tag": []any{map[string]any{"type": "Mention", "href": "https://here.example/users/cyd", "name": "@cyd"}}})
	post := e.remotePostID(danID + "/statuses/1")
	file := e.picture(post, strings.Repeat("b", 32)+".jpg")
	pub := num(e.must(200, e.ana, "POST", "/api/posts", map[string]any{"body": "shared", "audience": "public"})["posts"].([]any)[0].(map[string]any)["id"])
	e.send(e.dan(), danID, "Announce", fmt.Sprintf("https://here.example/users/ana/posts/%d", pub))
	if e.count(`SELECT count(*) FROM notifications WHERE kind IN ('mention', 'share')`) != 2 {
		t.Fatalf("before: %d notifications", e.count(`SELECT count(*) FROM notifications WHERE kind IN ('mention', 'share')`))
	}
	var media int64
	e.a.db.QueryRow(`SELECT id FROM ap_media WHERE post_id = $1`, post).Scan(&media)
	e.must(200, admin, "PUT", "/api/admin/federation/domains/social.example", map[string]any{"policy": "suspend"})
	if e.count(`SELECT count(*) FROM posts WHERE ap_id IS NOT NULL`)+e.count(`SELECT count(*) FROM notifications WHERE kind IN ('mention', 'share')`)+e.count(`SELECT count(*) FROM ap_media`) != 0 {
		t.Error("the suspended domain's posts, notifications or pictures are left")
	}
	if !gone(file) {
		t.Error("the suspended domain's picture file is left")
	}
	// A picture of a domain suspended outside that statement (its row still there) is not served either.
	e.must(200, admin, "PUT", "/api/admin/federation/domains/social.example", map[string]any{"policy": "allow"})
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'accepted')`, e.anaID, e.boUser) // the suspension ended it
	e.send(e.bo(), boID, "Create", noteObj(2, "<p>again</p>", nil))
	p2 := e.remotePostID(boID + "/statuses/2")
	e.picture(p2, strings.Repeat("c", 32)+".jpg")
	e.a.db.QueryRow(`SELECT id FROM ap_media WHERE post_id = $1`, p2).Scan(&media)
	if code, _, _ := e.visit("GET", fmt.Sprintf("/api/fed/media/%d.jpg", media), e.ana, 1, nil); code != 200 {
		t.Fatalf("the picture while allowed: %d", code)
	}
	e.mustExec(`INSERT INTO domain_policies (domain, policy) VALUES ('files.social.example', 'allow')`)
	e.mustExec(`UPDATE domain_policies SET policy = 'suspend' WHERE domain = 'social.example'`)
	if err := e.a.fed.loadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := e.visit("GET", fmt.Sprintf("/api/fed/media/%d.jpg", media), e.ana, 1, nil); code != 404 {
		t.Errorf("a suspended domain's picture: %d", code)
	}
}

// The memory of deletions: an account's own, under its cap and its server's allowance, whoever follows it (finding 3).
func TestDBRemoteDeletedMemory(t *testing.T) {
	e := newNotesInEnv(t)
	e.w.put(danID, worldDoc{body: actorDoc(danID, "dan", "Dan", &e.other.PublicKey, nil)})
	// (b) bo cannot block cat's post by deleting its id ahead: cat, followed by cyd, posts it all the same.
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) SELECT $1, user_id, 'accepted' FROM ap_actors WHERE ap_id = $2`, e.cyID, catID)
	if _, err := e.a.fed.remoteActor(context.Background(), catID); err != nil {
		t.Fatal(err)
	}
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) SELECT $1, user_id, 'accepted' FROM ap_actors WHERE ap_id = $2 ON CONFLICT DO NOTHING`, e.cyID, catID)
	e.send(e.bo(), boID, "Delete", catID+"/statuses/1")
	e.send(e.cat(), catID, "Create", map[string]any{"id": catID + "/statuses/1", "type": "Note", "attributedTo": catID, "content": "mine", "to": []string{asPublic}})
	if e.kept(catID+"/statuses/1") != 1 {
		t.Errorf("cat's post, deleted ahead by bo: %s", e.outcome())
	}
	// (c) dan, followed by nobody, deletes a post naming cyd before its Create arrives: the late Create tells nobody.
	e.send(e.dan(), danID, "Delete", danID+"/statuses/1")
	e.send(e.dan(), danID, "Create", map[string]any{"id": danID + "/statuses/1", "type": "Note", "attributedTo": danID, "content": "hi @cyd",
		"to": []string{asPublic}, "tag": []any{map[string]any{"type": "Mention", "href": "https://here.example/users/cyd", "name": "@cyd"}}})
	if e.kept(danID+"/statuses/1") != 0 || e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'mention'`, e.cyID) != 0 {
		t.Errorf("a mention deleted before it came: %s", e.outcome())
	}
	// (a) the cap per account: past it a deletion is acted on and not remembered.
	e.mustExec(`DELETE FROM ap_deleted WHERE user_id = $1`, e.boUser)
	e.mustExec(`INSERT INTO ap_deleted (ap_id, user_id, at) SELECT $1 || '/d/' || g, $2, now() FROM generate_series(1, $3) g`, boID, e.boUser, remoteDeletedPerAccount)
	e.send(e.bo(), boID, "Delete", boID+"/statuses/77")
	if n := e.count(`SELECT count(*) FROM ap_deleted WHERE user_id = $1`, e.boUser); n != remoteDeletedPerAccount {
		t.Errorf("bo has %d deletions remembered, want the cap of %d", n, remoteDeletedPerAccount)
	}
	// The allowance per sending host.
	e.a.fed.deletesIn = newLimiter(0, time.Hour)
	e.send(e.dan(), danID, "Delete", danID+"/statuses/2")
	if e.count(`SELECT count(*) FROM ap_deleted WHERE ap_id = $1`, danID+"/statuses/2") != 0 || !strings.Contains(e.outcome(), "not remembered") {
		t.Errorf("past the host's allowance: %s", e.outcome())
	}
	// The cap in all, in housekeeping.
	e.mustExec(`INSERT INTO ap_deleted (ap_id, user_id, at) SELECT 'https://social.example/x/' || g, $1, now() - g * interval '1 second' FROM generate_series(1, $2) g`, e.boUser, remoteDeletedMax)
	if _, err := e.a.remoteHousekeep(); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM ap_deleted`); n != remoteDeletedMax {
		t.Errorf("%d deletions remembered in all, want %d", n, remoteDeletedMax)
	}
}

// The picture worker starts with the server when a picture waits (finding 4).
func TestDBMediaWorkerAtStart(t *testing.T) {
	e := newNotesInEnv(t)
	e.send(e.bo(), boID, "Create", noteObj(1, "x", map[string]any{"attachment": []any{map[string]any{"type": "Document", "mediaType": "image/png", "url": "https://pics.example/a.png"}}}))
	if e.count(`SELECT count(*) FROM ap_media WHERE state = 'wanted'`) != 1 {
		t.Fatal("no picture waits")
	}
	b := e.again()
	if err := b.fedStart(map[string]string{"FLOWA_ORIGIN": "https://here.example"}); err != nil {
		t.Fatal(err)
	}
	if !b.a.fed.media.started.Load() {
		t.Error("a restart left the waiting picture to the next one that arrives")
	}
	c := newNotesInEnv(t)
	d := c.again()
	if err := d.fedStart(map[string]string{"FLOWA_ORIGIN": "https://here.example"}); err != nil {
		t.Fatal(err)
	}
	if d.a.fed.media.started.Load() {
		t.Error("the worker started with nothing to fetch")
	}
}

// Who a mention may name: a member who shares their profile, not suspended (finding 5); a pending follow keeps
// nothing and shows nothing, and an unfollow hides the account's posts (finding 18).
func TestDBRemoteNotesWhoReads(t *testing.T) {
	e := newNotesInEnv(t)
	deeID, dee := e.user("dee", "user") // never opted in
	e.w.put(danID, worldDoc{body: actorDoc(danID, "dan", "Dan", &e.other.PublicKey, nil)})
	mention := func(n int, name string) map[string]any {
		return map[string]any{"id": fmt.Sprintf("%s/statuses/%d", danID, n), "type": "Note", "attributedTo": danID, "content": "hi @" + name,
			"to": []string{asPublic}, "tag": []any{map[string]any{"type": "Mention", "href": "https://here.example/users/" + name, "name": "@" + name}}}
	}
	e.send(e.dan(), danID, "Create", mention(1, "dee"))
	if e.kept(danID+"/statuses/1") != 0 || e.count(`SELECT count(*) FROM notifications WHERE user_id = $1`, deeID) != 0 {
		t.Errorf("a member who never opted in was named: %s", e.outcome())
	}
	e.mustExec(`UPDATE users SET suspended_until = now() + interval '1 day' WHERE id = $1`, e.cyID)
	e.send(e.dan(), danID, "Create", mention(2, "cyd"))
	if e.kept(danID+"/statuses/2") != 0 {
		t.Errorf("a suspended member was named: %s", e.outcome())
	}
	e.mustExec(`UPDATE users SET suspended_until = NULL WHERE id = $1`, e.cyID)
	// A pending follow (dee asked, dan has not accepted): nothing kept, nothing shown.
	danUser := int64(0)
	e.a.db.QueryRow(`SELECT user_id FROM ap_actors WHERE ap_id = $1`, danID).Scan(&danUser)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $2, 'pending')`, deeID, danUser)
	e.send(e.dan(), danID, "Create", map[string]any{"id": danID + "/statuses/3", "type": "Note", "attributedTo": danID, "content": "plain", "to": []string{asPublic}})
	if e.kept(danID+"/statuses/3") != 0 {
		t.Errorf("a post kept for a pending follow: %s", e.outcome())
	}
	e.mustExec(`INSERT INTO posts (user_id, body, ap_id) VALUES ($1, 'stored', $2)`, danUser, danID+"/statuses/4")
	if h := e.home(dee); len(h) != 0 {
		t.Errorf("a pending follow shows %q", h)
	}
	// An unfollow hides the account's posts.
	e.send(e.bo(), boID, "Create", noteObj(1, "<p>bo</p>", nil))
	if h := e.home(e.ana); len(h) != 1 {
		t.Fatalf("ana's Home: %q", h)
	}
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/fed/accounts/%d/follow", e.boUser), nil)
	if h := e.home(e.ana); len(h) != 0 {
		t.Errorf("after the unfollow: %q", h)
	}
	e.must(404, e.ana, "GET", fmt.Sprintf("/api/posts/%d", e.remotePostID(boID+"/statuses/1")), nil)
}

// Posts kept only for a mention have their own cap, so servers nobody follows cannot push followed accounts' posts
// out (finding 6).
func TestDBRemoteMentionOnlyCap(t *testing.T) {
	e := newNotesInEnv(t)
	e.w.put(danID, worldDoc{body: actorDoc(danID, "dan", "Dan", &e.other.PublicKey, nil)})
	e.send(e.dan(), danID, "Create", map[string]any{"id": danID + "/statuses/1", "type": "Note", "attributedTo": danID, "content": "hi @cyd",
		"to": []string{asPublic}, "tag": []any{map[string]any{"type": "Mention", "href": "https://here.example/users/cyd", "name": "@cyd"}}})
	e.send(e.bo(), boID, "Create", noteObj(1, "<p>followed</p>", nil))
	if e.count(`SELECT count(*) FROM posts WHERE ap_mention_only`) != 1 || e.count(`SELECT count(*) FROM posts WHERE ap_id = $1 AND NOT ap_mention_only`, boID+"/statuses/1") != 1 {
		t.Fatal("which post was kept for a mention")
	}
	var danUser int64
	e.a.db.QueryRow(`SELECT user_id FROM ap_actors WHERE ap_id = $1`, danID).Scan(&danUser)
	e.mustExec(`INSERT INTO posts (user_id, body, ap_id, ap_mention_only) SELECT $1, 'm', $2 || '/m/' || g, true FROM generate_series(1, $3) g`, danUser, danID, remoteMentionOnlyMax+10)
	if _, err := e.a.remoteHousekeep(); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM posts WHERE ap_mention_only`); n != remoteMentionOnlyMax {
		t.Errorf("%d posts kept for a mention, want the cap of %d", n, remoteMentionOnlyMax)
	}
	if e.count(`SELECT count(*) FROM posts WHERE ap_id = $1`, boID+"/statuses/1") != 1 {
		t.Error("the followed account's post went for the posts kept for a mention")
	}
}

// A Create and a Delete of one post handled at once take turns on its id: the Delete that comes while the Create holds
// its transaction waits for it and then removes the post (finding 7). The test holds the Create at its last step with a
// hook, starts the Delete, waits until the Delete either finished or waits on the lock, then lets the Create go.
func TestDBRemoteCreateDeleteRace(t *testing.T) {
	e := newNotesInEnv(t)
	f := e.a.fed
	ctx := context.Background()
	s, err := f.remoteActor(ctx, boID)
	if err != nil {
		t.Fatal(err)
	}
	act, err := decodeActivity(activity(boID, "Create", noteObj(1, "race", nil), nil), inboxBodyMax)
	if err != nil {
		t.Fatal(err)
	}
	held, release := make(chan struct{}), make(chan struct{})
	var heldOnce, releaseOnce sync.Once
	f.noteHook = func(stage string) {
		if stage == "create held" {
			heldOnce.Do(func() { close(held) })
			<-release
		}
	}
	defer releaseOnce.Do(func() { close(release) })
	created := make(chan string, 1)
	go func() { out, _ := f.noteIn(ctx, s, act); created <- out }()
	<-held
	deleted := make(chan string, 1)
	go func() { out, _ := f.noteDeleted(ctx, s, boID+"/statuses/1"); deleted <- out }()
	// The Delete either finishes (as it would without the lock: then the post outlives it) or waits on the lock.
	var out string
	waitFor(t, "the Delete to finish or to wait on the post's lock", func() bool {
		select {
		case out = <-deleted:
			return true
		default:
		}
		return e.count(`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`) > 0
	})
	releaseOnce.Do(func() { close(release) })
	<-created
	if out == "" {
		out = <-deleted
	}
	if e.kept(boID+"/statuses/1") != 0 {
		t.Errorf("the post outlived its Delete (the Delete said %q)", out)
	}
}

// Staff's delete of a remote post is remembered: the author's later edit does not bring it back (finding 8). An edit
// that makes a post direct, or takes out the only mention that kept it, removes it (finding 9).
func TestDBRemoteNotesTakenAway(t *testing.T) {
	e := newNotesInEnv(t)
	_, mod := e.user("mo", "moderator")
	t0 := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	e.send(e.bo(), boID, "Create", noteObj(1, "<p>one</p>", nil))
	e.must(200, mod, "DELETE", fmt.Sprintf("/api/posts/%d", e.remotePostID(boID+"/statuses/1")), nil)
	e.send(e.bo(), boID, "Update", noteObj(1, "<p>back</p>", map[string]any{"updated": t0.Format(time.RFC3339)}))
	if e.kept(boID+"/statuses/1") != 0 {
		t.Errorf("an edit brought back what a moderator deleted: %s", e.outcome())
	}
	e.send(e.bo(), boID, "Create", noteObj(2, "<p>two</p>", nil))
	e.send(e.bo(), boID, "Update", noteObj(2, "<p>now private</p>", map[string]any{"to": []string{"https://here.example/users/ana"}, "cc": nil, "updated": t0.Format(time.RFC3339)}))
	if e.kept(boID+"/statuses/2") != 0 || e.outcome() != "done: removed (an edit made it a direct message)" {
		t.Errorf("an edit to a direct message: %s", e.outcome())
	}
	e.w.put(danID, worldDoc{body: actorDoc(danID, "dan", "Dan", &e.other.PublicKey, nil)})
	note := func(content string, tag bool, upd string) map[string]any {
		o := map[string]any{"id": danID + "/statuses/1", "type": "Note", "attributedTo": danID, "content": content, "to": []string{asPublic}}
		if tag {
			o["tag"] = []any{map[string]any{"type": "Mention", "href": "https://here.example/users/cyd", "name": "@cyd"}}
		}
		if upd != "" {
			o["updated"] = upd
		}
		return o
	}
	e.send(e.dan(), danID, "Create", note("hi @cyd", true, ""))
	e.send(e.dan(), danID, "Update", note("hi nobody", false, t0.Format(time.RFC3339)))
	if e.kept(danID+"/statuses/1") != 0 || e.outcome() != "done: removed (an edit left nobody here who may read it)" {
		t.Errorf("an edit taking out the only mention: %s", e.outcome())
	}
}

// A write that fails (a full disk) holds the worker off, instead of fetching picture after picture it cannot keep
// (finding 11); shrinkPicture refuses a size over the bound it is given, before decoding (finding 12).
func TestDBMediaWriteFails(t *testing.T) {
	e := newNotesInEnv(t)
	m := e.a.fed.media
	h := http.NewServeMux()
	h.HandleFunc("/p/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngOf(20, 20))
	})
	f, _ := fedStub(t, h)
	f.origin = e.a.fed.origin
	m.fetch = f
	e.allow("pics.example")
	clock := time.Now().Add(time.Minute) // past the rows' next_at, which the database's clock writes
	var cmu sync.Mutex
	m.now = func() time.Time { cmu.Lock(); defer cmu.Unlock(); return clock }
	writes := 0
	m.writeFile = func([]byte) (string, error) { writes++; return "", fmt.Errorf("no space left on device") }
	e.send(e.bo(), boID, "Create", noteObj(1, "x", map[string]any{"attachment": []any{
		map[string]any{"type": "Document", "mediaType": "image/png", "url": "https://pics.example/p/1.png"},
		map[string]any{"type": "Document", "mediaType": "image/png", "url": "https://pics.example/p/2.png"}}}))
	if worked, _ := m.step(context.Background()); !worked || writes != 1 {
		t.Fatalf("the first try: %v, %d writes", worked, writes)
	}
	if worked, _ := m.step(context.Background()); worked || writes != 1 {
		t.Errorf("after a failed write the worker went on: %v, %d writes", worked, writes)
	}
	cmu.Lock()
	clock = clock.Add(mediaRetries[0] + time.Second)
	cmu.Unlock()
	m.writeFile = m.write
	if worked, err := m.step(context.Background()); !worked || err != nil {
		t.Errorf("after the hold: %v %v", worked, err)
	}
	if _, _, _, err := shrinkPicture(hugePNG(mediaInSide+1, 10), mediaInSide, mediaSide, mediaOut); err == nil {
		t.Errorf("shrinkPicture took a picture over %d px", mediaInSide)
	}
	if _, _, _, err := shrinkPicture(pngOf(100, 50), 99, mediaSide, mediaOut); err == nil {
		t.Error("shrinkPicture took a picture over the bound it was given")
	}
}

// The public addresses never serve a remote post, whoever's name is tried (finding 13); an undecodable Delete stored
// since migration 22 is not acted on (finding 14); a future published time is shown as the time received (finding 16).
func TestDBRemoteNotesEdges(t *testing.T) {
	e := newNotesInEnv(t)
	e.send(e.bo(), boID, "Create", noteObj(1, "<p>x</p>", nil))
	id := e.remotePostID(boID + "/statuses/1")
	as := map[string]string{"Accept": "application/activity+json"}
	for _, path := range []string{fmt.Sprintf("/users/bo@social.example/posts/%d", id), fmt.Sprintf("/users/ana/posts/%d", id),
		fmt.Sprintf("/users/ana/posts/%d/activity", id), fmt.Sprintf("/users/ana/posts/%d/picture.jpg", id)} {
		if code, _, _ := e.visit("GET", path, "", 1, as); code != 404 {
			t.Errorf("%s: %d", path, code)
		}
		if code, _, _ := e.visit("GET", path, "", 1, nil); code != 404 && !strings.HasSuffix(path, "/activity") { // an activity's address sends a browser to its post's page
			t.Errorf("%s as a page: %d", path, code)
		}
	}
	e.mustExec(`INSERT INTO ap_inbox (activity_id, actor_id, host, kind, body, received_at, next_at) VALUES ($1, $2, 'social.example', 'Delete', '{}', now(), $3)`,
		boID+"/activities/garbled", e.boUser, e.clock.now())
	e.q.step(context.Background())
	if e.outcome() != "done: not acted on (the stored activity does not read)" || e.count(`SELECT count(*) FROM ap_actors WHERE user_id = $1 AND state = 'ok'`, e.boUser) != 1 {
		t.Errorf("an undecodable Delete: %s", e.outcome())
	}
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	_, v, _ := remotePost(&apNote{ID: boID + "/statuses/2", Kind: "Note", Published: now.Add(48 * time.Hour)}, boID, now)
	if v.Published == nil || !v.Published.Equal(now) {
		t.Errorf("a post dated ahead is shown at %v", v.Published)
	}
}

// A database that federated, then started in local mode: remote posts are not shown (Home, the post alone, its
// pictures), as local mode shows nothing of federation; they come back when the mode federates again (finding 15).
func TestDBRemoteNotesInLocalMode(t *testing.T) {
	e := newNotesInEnv(t)
	e.send(e.bo(), boID, "Create", noteObj(1, "<p>remote</p>", nil))
	id := e.remotePostID(boID + "/statuses/1")
	local := e.again()
	if err := local.a.start(func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	local.h = local.a.routes(staticOf(t, nil))
	if local.a.fed != nil {
		t.Fatal("not local mode")
	}
	if posts := local.must(200, e.ana, "GET", "/api/home", nil)["posts"].([]any); len(posts) != 0 {
		t.Errorf("local mode's Home shows %d remote posts", len(posts))
	}
	local.must(404, e.ana, "GET", fmt.Sprintf("/api/posts/%d", id), nil)
	if h := e.home(e.ana); len(h) != 1 {
		t.Errorf("federated again, Home: %q", h)
	}
}
