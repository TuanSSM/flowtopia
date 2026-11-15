package main

// Replies and likes across servers (FWR 6) held to the server's rules for the pool and the bell: no path holds a
// connection while it waits for another, and the notifications other servers leave stay under FWR 3's bound.

import (
	"fmt"
	"testing"
	"time"
)

// Every path of this item on a pool of one connection (TestDBOneConnection's rule): a reply in with its forward and
// notifications, a like and its Undo, a member's comment out with its edit and deletion, a like of a post of another
// server and its unlike, and housekeeping; none holds a connection while it waits for another.
func TestDBRepliesOneConnection(t *testing.T) {
	e := newRepliesEnv(t)
	ne := &notesEnv{followEnv: e.followEnv, anaID: e.anaID, ana: e.ana}
	e.send(e.bo(), boID, "Create", noteObj(50, "bo's post", nil))
	remote := e.remotePostID(boID + "/statuses/50")
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
	within("a reply in, forwarded", func() {
		e.send(e.bo(), boID, "Create", reply(boID, 1, e.postID, "hi", "https://here.example/users/ana/followers"))
	})
	within("a like in and its Undo", func() {
		e.deliver(activity(boID, "Like", e.postID, map[string]any{"id": boID + "#l1"}), e.bo())
		e.deliver(activity(boID, "Undo", boID+"#l1", nil), e.bo())
	})
	within("a comment out, edited and deleted", func() {
		cmt := num(e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "out"})["comment"].(map[string]any)["id"])
		ne.produce()
		e.must(200, e.ana, "PUT", fmt.Sprintf("/api/comments/%d", cmt), map[string]any{"body": "out, edited"})
		ne.produce()
		e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/comments/%d", cmt), nil)
		ne.produce()
	})
	within("a like of a post of another server and its unlike", func() {
		e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/like", remote), nil)
		e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/like", remote), nil)
	})
	within("housekeeping", func() {
		if _, err := e.a.housekeep(); err != nil {
			t.Error(err)
		}
	})
	if len(e.forwards()) != 1 || e.count(`SELECT count(*) FROM ap_comment_out`) != 0 || e.count(`SELECT count(*) FROM comments WHERE ap_id IS NOT NULL`) != 1 {
		t.Errorf("forwards %d, records %d, replies %d", len(e.forwards()), e.count(`SELECT count(*) FROM ap_comment_out`), e.count(`SELECT count(*) FROM comments WHERE ap_id IS NOT NULL`))
	}
}

// What replies and likes from other servers leave in a member's bell is bounded with the rest of what other servers
// leave (remoteNotesPerHost from one server, remoteNotesMax from all), the oldest dropped, never a notification a member
// here left.
func TestDBRemoteTalkNotesBounded(t *testing.T) {
	e := newRepliesEnv(t)
	e.must(200, e.cy, "POST", fmt.Sprintf("/api/posts/%d/like", e.post), nil)
	for i := range remoteNotesPerHost + 5 {
		e.send(e.eve(), eveID, "Create", reply(eveID, 100+i, e.postID, fmt.Sprintf("reply %d", i)))
	}
	if n := e.count(`SELECT count(*) FROM comments WHERE ap_id IS NOT NULL`); n != remoteNotesPerHost+5 {
		t.Fatalf("%d replies kept", n)
	}
	if n := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND fed`, e.anaID); n != remoteNotesPerHost {
		t.Errorf("ana keeps %d notifications from other.example, want %d", n, remoteNotesPerHost)
	}
	if n := e.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND NOT fed AND kind = 'like'`, e.anaID); n != 1 {
		t.Errorf("cy's like's notification: %d", n)
	}
}

// Replies and likes follow item 5's rules after its review: an account that deletes itself, or whose domain is
// suspended, loses its replies, likes and notifications in the statement that removes its posts (a reply a member here
// answered kept as a placeholder, so the answer stays); the stored domain policy is asked in the statement that keeps one;
// a reply of an account its server suspended is not listed; a reply deleted here, or deleted by its author before it
// came, is remembered (ap_deleted), so an edit or a late Create does not bring it back; a reply is dated by its own
// published time, never later than it arrived; and an edit that narrows a kept reply takes it out.
func TestDBRemoteTalkFollowsItem5(t *testing.T) {
	e := newRepliesEnv(t)
	_, admin := e.user("boss", "admin")
	thread := func() []string { return bodies(e.thread(e.ana, e.post)) }
	likes := func() int64 {
		return num(e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", e.post), nil)["posts"].([]any)[0].(map[string]any)["likes"])
	}

	// Dated by its own published time; one from the future by when it arrived.
	early := reply(boID, 1, e.postID, "an hour ago")
	early["published"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	e.send(e.bo(), boID, "Create", early)
	late := reply(boID, 2, e.postID, "from the future")
	late["published"] = time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	e.send(e.bo(), boID, "Create", late)
	var ageEarly, ageLate float64
	e.a.db.QueryRow(`SELECT extract(epoch FROM now() - created_at) FROM comments WHERE ap_id = $1`, boID+"/statuses/1").Scan(&ageEarly)
	e.a.db.QueryRow(`SELECT extract(epoch FROM now() - created_at) FROM comments WHERE ap_id = $1`, boID+"/statuses/2").Scan(&ageLate)
	if ageEarly < 3000 || ageLate < -60 || ageLate > 600 { // the inbox's clock is the test's, a little ahead of the database's
		t.Errorf("replies dated %.0f s and %.0f s ago, want an hour and when they arrived", ageEarly, ageLate)
	}

	// An edit that addresses a kept reply to fewer people takes it out; an older one does not.
	narrow := reply(boID, 2, e.postID, "now for my followers")
	narrow["to"], narrow["cc"], narrow["updated"] = []string{boID + "/followers"}, nil, time.Now().UTC().Format(time.RFC3339)
	if e.send(e.bo(), boID, "Update", narrow); e.outcome() != "done: removed (an edit addressed it to fewer people than the post)" {
		t.Errorf("a narrowing edit: %s", e.outcome())
	}
	if _, ok := e.kept(boID + "/statuses/2"); ok {
		t.Error("the narrowed reply is still kept")
	}

	// A reply of an account its server says is suspended is not listed, until it is lifted.
	e.mustExec(`UPDATE ap_actors SET state = 'suspended' WHERE user_id = $1`, e.boUser)
	if got := thread(); len(got) != 0 {
		t.Errorf("a suspended account's replies are listed: %q", got)
	}
	e.mustExec(`UPDATE ap_actors SET state = 'ok' WHERE user_id = $1`, e.boUser)

	// A Delete before its Create is remembered, whoever follows the account: the late Create keeps nothing.
	e.send(e.cat(), catID, "Delete", map[string]any{"id": catID + "/statuses/7", "type": "Tombstone"})
	e.send(e.cat(), catID, "Create", reply(catID, 7, e.postID, "too late"))
	if _, ok := e.kept(catID + "/statuses/7"); ok {
		t.Errorf("a reply deleted before it came is kept: %s", e.outcome())
	}

	// A reply the post's author deletes here is remembered: its author's edit does not bring it back.
	e.send(e.eve(), eveID, "Create", reply(eveID, 1, e.postID, "eve here"))
	ev, _ := e.kept(eveID + "/statuses/1")
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/comments/%d", ev.id), nil)
	edit := reply(eveID, 1, e.postID, "eve again")
	edit["published"], edit["updated"] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339)
	e.send(e.eve(), eveID, "Update", edit)
	if _, ok := e.kept(eveID + "/statuses/1"); ok || e.count(`SELECT count(*) FROM ap_deleted WHERE ap_id = $1 AND by_staff`, eveID+"/statuses/1") != 1 {
		t.Errorf("a reply deleted here came back with its author's edit: %s", e.outcome())
	}

	// The stored policy: a domain suspended after the worker read the policy keeps no reply or like of it.
	var was string
	e.a.db.QueryRow(`SELECT policy FROM domain_policies WHERE domain = 'other.example'`).Scan(&was)
	e.mustExec(`UPDATE domain_policies SET policy = 'suspend' WHERE domain = 'other.example'`)
	e.send(e.eve(), eveID, "Create", reply(eveID, 2, e.postID, "eve once more"))
	e.deliver(activity(eveID, "Like", e.postID, nil), e.eve())
	if _, ok := e.kept(eveID + "/statuses/2"); ok || likes() != 0 {
		t.Errorf("a domain suspended as stored: the reply kept %v, %d likes", ok, likes())
	}
	e.mustExec(`UPDATE domain_policies SET policy = $1 WHERE domain = 'other.example'`, was)

	// An account that deletes itself: its reply a member answered stays as a placeholder, its likes and notifications go.
	bo1, _ := e.kept(boID + "/statuses/1")
	e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "thanks", "parentId": bo1.id})
	e.deliver(activity(boID, "Like", e.postID, map[string]any{"id": boID + "#l9"}), e.bo())
	if likes() != 1 || len(e.notes(e.anaID)) == 0 {
		t.Fatalf("before bo's own Delete: %d likes, notes %v", likes(), e.notes(e.anaID))
	}
	e.send(e.bo(), boID, "Delete", boID)
	if c, ok := e.kept(boID + "/statuses/1"); !ok || !c.deleted || c.body != "" || likes() != 0 ||
		e.count(`SELECT count(*) FROM notifications WHERE actor_id = $1`, e.boUser) != 0 {
		t.Errorf("after bo's own Delete: %+v %v, %d likes, %d notifications", c, ok, likes(), e.count(`SELECT count(*) FROM notifications WHERE actor_id = $1`, e.boUser))
	}
	if got := thread(); len(got) != 2 || got[1] != "@bo@social.example thanks" {
		t.Errorf("the thread after bo's own Delete: %q", got)
	}

	// A suspended domain: its accounts' replies and likes and their notifications go in the suspension's statement.
	e.send(e.cat(), catID, "Create", reply(catID, 8, e.postID, "cat here"))
	e.deliver(activity(catID, "Like", e.postID, nil), e.cat())
	if _, ok := e.kept(catID + "/statuses/8"); !ok || likes() != 1 {
		t.Fatalf("cat's reply and like: %s", e.outcome())
	}
	e.must(200, admin, "PUT", "/api/admin/federation/domains/social.example", map[string]any{"policy": "suspend"})
	if _, ok := e.kept(catID + "/statuses/8"); ok || likes() != 0 || e.count(`SELECT count(*) FROM notifications WHERE actor_id = $1`, e.catID) != 0 {
		t.Errorf("after the suspension: cat's reply kept %v, %d likes, %d notifications", ok, likes(), e.count(`SELECT count(*) FROM notifications WHERE actor_id = $1`, e.catID))
	}
}
