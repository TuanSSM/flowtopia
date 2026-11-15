package main

// The second review of FWR 5 (PR #115): a moderator's delete that must hold, an old post's edit, an edit that loses
// to a newer one, what a self-deleted account leaves, the share of posts kept for a mention, staff and pictures, a
// domain suspended while a post is being stored, the cost of asking again for evicted pictures, the time a post is
// dated, a named member who stops sharing, and -purge-remote with the Federation switch on.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// A moderator's delete holds past 7 days and past the cap of the others; an edit of a post never kept is taken as
// new only when the post is recent, and a late post is dated by its own published time (second review, finding 1).
func TestDBRemoteStaffDeleteHolds(t *testing.T) {
	e := newNotesInEnv(t)
	_, mod := e.user("mo", "moderator")
	e.send(e.bo(), boID, "Create", noteObj(1, "<p>one</p>", map[string]any{"published": time.Now().UTC().Format(time.RFC3339)}))
	e.must(200, mod, "DELETE", fmt.Sprintf("/api/posts/%d", e.remotePostID(boID+"/statuses/1")), nil)
	e.mustExec(`UPDATE ap_deleted SET at = now() - interval '8 days' WHERE ap_id = $1`, boID+"/statuses/1")
	e.mustExec(`INSERT INTO ap_deleted (ap_id, user_id, at) SELECT 'https://social.example/f/' || g, $1, now() FROM generate_series(1, $2) g`, e.boUser, remoteDeletedMax+5)
	if _, err := e.a.remoteHousekeep(); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM ap_deleted WHERE ap_id = $1 AND by_staff`, boID+"/statuses/1") != 1 {
		t.Fatal("the moderator's delete was forgotten by the 7 days or the cap")
	}
	e.send(e.bo(), boID, "Update", noteObj(1, "<p>back</p>", map[string]any{"published": time.Now().UTC().Format(time.RFC3339), "updated": time.Now().UTC().Format(time.RFC3339)}))
	if e.kept(boID+"/statuses/1") != 0 {
		t.Errorf("an edit brought back what a moderator deleted: %s", e.outcome())
	}
	// An edit of an old post never kept is not taken as new.
	old := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	e.send(e.bo(), boID, "Update", noteObj(2, "<p>old</p>", map[string]any{"published": old, "updated": time.Now().UTC().Format(time.RFC3339)}))
	if e.kept(boID+"/statuses/2") != 0 || !strings.Contains(e.outcome(), "published too long ago") {
		t.Errorf("an edit of an old post never kept: %s", e.outcome())
	}
	// A post dated two days ago is kept with that time, not the time it arrived.
	two := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	e.send(e.bo(), boID, "Create", noteObj(3, "<p>late</p>", map[string]any{"published": two.Format(time.RFC3339)}))
	var at time.Time
	e.a.db.QueryRow(`SELECT created_at FROM posts WHERE ap_id = $1`, boID+"/statuses/3").Scan(&at)
	if !at.Equal(two) {
		t.Errorf("a late post is dated %v, want its published %v", at, two)
	}
}

// An older edit that would remove a post loses to a newer edit that committed while it waited (finding 4).
func TestDBRemoteRemoveLosesToNewer(t *testing.T) {
	e := newNotesInEnv(t)
	f := e.a.fed
	ctx := context.Background()
	t0 := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	e.send(e.bo(), boID, "Create", noteObj(1, "<p>one</p>", nil))
	s, err := f.remoteActor(ctx, boID)
	if err != nil {
		t.Fatal(err)
	}
	direct, _ := decodeActivity(activity(boID, "Update", noteObj(1, "<p>private</p>", map[string]any{"to": []string{"https://here.example/users/ana"}, "cc": nil, "updated": t0.Format(time.RFC3339)}), nil), inboxBodyMax)
	newer, _ := decodeActivity(activity(boID, "Update", noteObj(1, "<p>public again</p>", map[string]any{"updated": t0.Add(time.Hour).Format(time.RFC3339)}), nil), inboxBodyMax)
	paused, release := make(chan struct{}), make(chan struct{})
	var once, rel sync.Once
	f.noteHook = func(stage string) {
		if stage == "remove locking" {
			once.Do(func() { close(paused) })
			<-release
		}
	}
	defer rel.Do(func() { close(release) })
	done := make(chan string, 1)
	go func() { out, _ := f.noteIn(ctx, s, direct, time.Now(), nil); done <- out }()
	<-paused
	if out, err := f.noteIn(ctx, s, newer, time.Now(), nil); err != nil || out != "" {
		t.Fatalf("the newer edit: %q %v", out, err)
	}
	rel.Do(func() { close(release) })
	if out := <-done; out != " ignored (not newer than the version kept)" {
		t.Errorf("the older edit said %q", out)
	}
	if e.kept(boID+"/statuses/1") != 1 {
		t.Error("the older edit removed the newer version")
	}
}

// A self-deleted account's shares go with it (finding 5); staff see the pictures of what they moderate, members do not
// see a suspended account's (finding 7).
func TestDBRemoteGoneSharesAndStaffPictures(t *testing.T) {
	e := newNotesInEnv(t)
	_, mod := e.user("mo", "moderator")
	e.send(e.bo(), boID, "Create", noteObj(1, "<p>x</p>", nil))
	post := e.remotePostID(boID + "/statuses/1")
	e.picture(post, strings.Repeat("e", 32)+".jpg")
	var media int64
	e.a.db.QueryRow(`SELECT id FROM ap_media WHERE post_id = $1`, post).Scan(&media)
	src := fmt.Sprintf("/api/fed/media/%d.jpg", media)
	e.mustExec(`UPDATE ap_actors SET state = 'suspended' WHERE user_id = $1`, e.boUser)
	if code, _, _ := e.visit("GET", src, e.ana, 1, nil); code != 404 {
		t.Errorf("a member, a suspended account's picture: %d", code)
	}
	e.must(200, mod, "GET", fmt.Sprintf("/api/posts/%d", post), nil)
	if code, _, _ := e.visit("GET", src, mod, 1, nil); code != 200 {
		t.Errorf("a moderator, the picture of the post they may open: %d", code)
	}
	e.mustExec(`UPDATE ap_actors SET state = 'ok' WHERE user_id = $1`, e.boUser)
	pub := num(e.must(200, e.ana, "POST", "/api/posts", map[string]any{"body": "shared", "audience": "public"})["posts"].([]any)[0].(map[string]any)["id"])
	e.send(e.bo(), boID, "Announce", fmt.Sprintf("https://here.example/users/ana/posts/%d", pub))
	if e.count(`SELECT count(*) FROM notifications WHERE kind = 'share' AND actor_id = $1`, e.boUser) != 1 {
		t.Fatal("no share told")
	}
	e.send(e.bo(), boID, "Delete", boID)
	if n := e.count(`SELECT count(*) FROM notifications WHERE actor_id = $1 AND kind IN ('share', 'mention')`, e.boUser); n != 0 {
		t.Errorf("a deleted account's %d shares are left", n)
	}
}

// Posts kept for a mention: one host's share of the cap, and the mark cleared when the account is followed (finding 6).
func TestDBRemoteMentionShare(t *testing.T) {
	e := newNotesInEnv(t)
	e.w.put(danID, worldDoc{body: actorDoc(danID, "dan", "Dan", &e.other.PublicKey, nil)})
	dan, err := e.a.fed.remoteActor(context.Background(), danID)
	if err != nil {
		t.Fatal(err)
	}
	e.mustExec(`INSERT INTO users (username, pw_hash, domain) VALUES ('x@other.example', '!', 'other.example')`)
	var other int64
	e.a.db.QueryRow(`SELECT id FROM users WHERE username = 'x@other.example'`).Scan(&other)
	e.mustExec(`INSERT INTO posts (user_id, body, ap_id, ap_mention_only, created_at) SELECT $1, 'o', 'https://other.example/o/' || g, true, now() - interval '1 day' FROM generate_series(1, 10) g`, other)
	e.mustExec(`INSERT INTO posts (user_id, body, ap_id, ap_mention_only) SELECT $1, 'd', $2 || '/d/' || g, true FROM generate_series(1, $3) g`, dan.UserID, danID, remoteMentionPerHost+50)
	if _, err := e.a.remoteHousekeep(); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM posts WHERE user_id = $1`, dan.UserID); n != remoteMentionPerHost {
		t.Errorf("social.example keeps %d posts for a mention, want its share of %d", n, remoteMentionPerHost)
	}
	if n := e.count(`SELECT count(*) FROM posts WHERE user_id = $1`, other); n != 10 {
		t.Errorf("another host's posts pushed out: %d left", n)
	}
	// ana's follow of dan is accepted: dan's posts are a follow's now, not kept for a mention.
	followID := "https://here.example/users/ana/activities/f1"
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state, ap_id) VALUES ($1, $2, 'pending', $3)`, e.anaID, dan.UserID, followID)
	if out, err := e.a.fed.followAnswered(context.Background(), dan, idOrObject{ID: followID}, true); err != nil || out != "" {
		t.Fatalf("the Accept: %q %v", out, err)
	}
	if n := e.count(`SELECT count(*) FROM posts WHERE user_id = $1 AND ap_mention_only`, dan.UserID); n != 0 {
		t.Errorf("%d posts of a followed account still marked as kept for a mention", n)
	}
}

// A domain suspended while a worker holds a Create between its policy check and its store keeps nothing (finding 8).
func TestDBRemoteSuspendedWhileStoring(t *testing.T) {
	e := newNotesInEnv(t)
	f := e.a.fed
	ctx := context.Background()
	s, err := f.remoteActor(ctx, boID)
	if err != nil {
		t.Fatal(err)
	}
	act, _ := decodeActivity(activity(boID, "Create", noteObj(1, "<p>late</p>", nil), nil), inboxBodyMax)
	paused, release := make(chan struct{}), make(chan struct{})
	var once, rel sync.Once
	f.noteHook = func(stage string) {
		if stage == "create locking" {
			once.Do(func() { close(paused) })
			<-release
		}
	}
	defer rel.Do(func() { close(release) })
	done := make(chan string, 1)
	go func() { out, _ := f.noteIn(ctx, s, act, time.Now(), nil); done <- out }()
	<-paused
	_, admin := e.user("boss", "admin")
	e.must(200, admin, "PUT", "/api/admin/federation/domains/social.example", map[string]any{"policy": "suspend"})
	rel.Do(func() { close(release) })
	<-done
	if e.kept(boID+"/statuses/1") != 0 {
		t.Error("a post was stored right after its domain's suspension")
	}
}

// A page with an evicted picture runs the statement that asks for it again only when one is due, and a second read
// runs none (finding 9); a post dated ahead is shown at the time the inbox took it (finding 10); a named member who
// stops sharing their profile can no longer open the post (finding 11).
func TestDBRemoteReadsCostAndRights(t *testing.T) {
	e := newNotesInEnv(t)
	m := e.a.fed.media
	ran := 0
	var mu sync.Mutex
	m.rewanted = func() { mu.Lock(); ran++; mu.Unlock() }
	runs := func() int { mu.Lock(); defer mu.Unlock(); return ran }
	e.send(e.bo(), boID, "Create", noteObj(1, "<p>pic</p>", map[string]any{"published": "2099-01-01T00:00:00Z"}))
	post := e.remotePostID(boID + "/statuses/1")
	e.mustExec(`INSERT INTO ap_media (post_id, n, kind, url, state, next_at) VALUES ($1, 0, 'picture', 'https://pics.example/a.png', 'evicted', now())`, post)
	e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", post), nil)
	if runs() != 0 {
		t.Errorf("a picture evicted just now cost %d statements", runs())
	}
	e.mustExec(`UPDATE ap_media SET next_at = now() - interval '2 hours' WHERE post_id = $1`, post)
	e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", post), nil)
	e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", post), nil)
	if runs() != 1 || e.count(`SELECT count(*) FROM ap_media WHERE post_id = $1 AND state = 'wanted'`, post) != 1 {
		t.Errorf("two reads of a due picture ran %d statements", runs())
	}
	// Dated ahead: shown at the time the inbox took it.
	var extra []byte
	var received time.Time
	e.a.db.QueryRow(`SELECT ap_extra FROM posts WHERE id = $1`, post).Scan(&extra)
	e.a.db.QueryRow(`SELECT received_at FROM ap_inbox WHERE activity_id LIKE $1 || '%' AND kind = 'Create' ORDER BY id DESC LIMIT 1`, boID+"/activities/").Scan(&received)
	var v remoteView
	json.Unmarshal(extra, &v)
	if v.Published == nil || !v.Published.Equal(received) {
		t.Errorf("a post dated ahead is shown at %v, want when it was received, %v", v.Published, received)
	}
	// A named member reads only while they share their profile.
	b := map[string]any{"tag": []any{map[string]any{"type": "Mention", "href": "https://here.example/users/cyd", "name": "@cyd"}}}
	e.send(e.bo(), boID, "Create", noteObj(2, "<p>hi @cyd</p>", b))
	p2 := e.remotePostID(boID + "/statuses/2")
	e.must(200, e.cy, "GET", fmt.Sprintf("/api/posts/%d", p2), nil)
	e.mustExec(`UPDATE users SET federates = false WHERE id = $1`, e.cyID)
	e.must(404, e.cy, "GET", fmt.Sprintf("/api/posts/%d", p2), nil)
}

// -purge-remote says when the Federation switch is on (finding 13).
func TestDBPurgeRemoteSwitchOn(t *testing.T) {
	e := newNotesInEnv(t)
	var out strings.Builder
	if err := purgeWith(e.a.db, t.TempDir(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "the Federation switch is on") {
		t.Errorf("with the switch on: %q", out.String())
	}
	e.setFlag(fedFlag, false)
	out.Reset()
	if err := purgeWith(e.a.db, t.TempDir(), &out); err != nil || strings.Contains(out.String(), "switch is on") {
		t.Errorf("with the switch off: %q %v", out.String(), err)
	}
}
