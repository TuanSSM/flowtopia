package main

// Points from the review of FWR 6 (PR #116): a comment's Create, Updates and Delete never reach an inbox out of order and
// nothing deleted goes out after its Delete; nothing that names a member who does not share leaves this server; an edit
// made while the producer reads the record is not lost; switches that could not be read hold a comment's way out.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// talkEnv is repliesEnv with the producer at hand.
type talkEnv struct {
	*repliesEnv
	ne *notesEnv
}

func newTalkEnv(t *testing.T) *talkEnv {
	e := newRepliesEnv(t)
	ne := &notesEnv{followEnv: e.followEnv, anaID: e.anaID, ana: e.ana}
	ne.produce() // ana's post
	return &talkEnv{repliesEnv: e, ne: ne}
}

// comment makes a comment of ana's on her post and returns its id and its Note's id.
func (e *talkEnv) comment(body string) (int64, string) {
	e.t.Helper()
	cmt := num(e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": body})["comment"].(map[string]any)["id"])
	return cmt, fmt.Sprintf("https://here.example/users/ana/comments/%d", cmt)
}

// queued is the number of deliveries still queued for the activity of id, and outbox whether the activity exists.
func (e *talkEnv) queued(id string) (int, bool) {
	e.t.Helper()
	return e.count(`SELECT count(*) FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.ap_id = $1 AND d.state = 'queued'`, id),
		e.count(`SELECT count(*) FROM ap_outbox WHERE ap_id = $1`, id) == 1
}

// delivered makes every queued delivery of the activity id sent, as a 2xx leaves it (the row goes).
func (e *talkEnv) delivered(id string) {
	e.t.Helper()
	e.mustExec(`DELETE FROM ap_deliveries WHERE state = 'queued' AND outbox_id = (SELECT id FROM ap_outbox WHERE ap_id = $1)`, id)
}

func (e *talkEnv) edit(cmt int64, body string) {
	e.t.Helper()
	e.must(200, e.ana, "PUT", fmt.Sprintf("/api/comments/%d", cmt), map[string]any{"body": body})
}

func (e *talkEnv) del(cmt int64) {
	e.t.Helper()
	e.must(200, e.ana, "DELETE", fmt.Sprintf("/api/comments/%d", cmt), nil)
}

// updates are the Update activities of a comment, oldest first.
func (e *talkEnv) updates(note string) []outRow {
	e.t.Helper()
	var out []outRow
	for _, r := range e.ne.outRows() {
		if r.kind == "Update" && strings.HasPrefix(r.id, note+"#update-") {
			out = append(out, r)
		}
	}
	return out
}

// Every order of a comment's Create, its edits and its Delete (finding B1): an Update is never enqueued while the
// activity before it has a delivery queued, so at each inbox nothing overtakes; the Delete takes back the last activity,
// so nothing deleted goes out after it, and it goes to every inbox an earlier activity may have reached.
func TestDBCommentOrders(t *testing.T) {
	t.Run("Create untried, an edit, the Delete: nothing goes", func(t *testing.T) {
		e := newTalkEnv(t)
		cmt, note := e.comment("first")
		e.ne.produce()
		e.edit(cmt, "first, edited")
		e.ne.produce()
		if ups := e.updates(note); len(ups) != 0 {
			t.Fatalf("an Update was enqueued while its Create waits untried: %v", ups[0].id)
		}
		e.del(cmt)
		e.ne.produce()
		c, _ := e.queued(note + "/activity")
		d, dok := e.queued(note + "#delete")
		if c != 0 || d != 0 || !dok || e.count(`SELECT count(*) FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.ap_id LIKE $1 || '%' AND d.state = 'queued'`, note) != 0 {
			t.Errorf("queued after the Delete: Create %d, Delete %d (made %v)", c, d, dok)
		}
	})
	t.Run("Create tried, an edit, the Delete: the Update is held, the Delete goes", func(t *testing.T) {
		e := newTalkEnv(t)
		cmt, note := e.comment("first")
		e.ne.produce()
		e.mustExec(`UPDATE ap_deliveries SET attempts = 1 WHERE state = 'queued' AND outbox_id = (SELECT id FROM ap_outbox WHERE ap_id = $1)`, note+"/activity")
		e.edit(cmt, "first, edited")
		e.ne.produce()
		if ups := e.updates(note); len(ups) != 0 {
			t.Fatalf("an Update was enqueued while its Create is still queued: %v", ups[0].id)
		}
		e.del(cmt)
		e.ne.produce()
		c, _ := e.queued(note + "/activity")
		if d, _ := e.queued(note + "#delete"); c != 0 || d != 2 || len(e.updates(note)) != 0 {
			t.Errorf("after the Delete: Create %d queued, Delete %d queued, %d Updates", c, d, len(e.updates(note)))
		}
	})
	t.Run("Create sent, an Update untried, the Delete: the Update is given up, the Delete goes", func(t *testing.T) {
		e := newTalkEnv(t)
		cmt, note := e.comment("first")
		e.ne.produce()
		e.delivered(note + "/activity")
		e.edit(cmt, "first, edited")
		e.ne.produce()
		ups := e.updates(note)
		if len(ups) != 1 || len(ups[0].inboxes) != 2 {
			t.Fatalf("the Update after a Create sent: %v", ups)
		}
		e.del(cmt)
		e.ne.produce()
		u, _ := e.queued(ups[0].id)
		if d, _ := e.queued(note + "#delete"); u != 0 || d != 2 {
			t.Errorf("after the Delete: Update %d queued, Delete %d queued (it must go where the Create went)", u, d)
		}
	})
	t.Run("Create sent, an Update in flight, the Delete: the Delete waits for its lease", func(t *testing.T) {
		e := newTalkEnv(t)
		cmt, note := e.comment("first")
		e.ne.produce()
		e.delivered(note + "/activity")
		e.edit(cmt, "first, edited")
		e.ne.produce()
		ups := e.updates(note)
		lease := e.clock.now().Add(deliveryLease)
		e.mustExec(`UPDATE ap_deliveries SET attempts = 1, locked_until = $2 WHERE state = 'queued' AND outbox_id = (SELECT id FROM ap_outbox WHERE ap_id = $1)`, ups[0].id, lease)
		e.del(cmt)
		e.ne.produce()
		u, _ := e.queued(ups[0].id)
		d, _ := e.queued(note + "#delete")
		early := e.count(`SELECT count(*) FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.ap_id = $1 AND d.next_at < $2`, note+"#delete", lease)
		if u != 2 || d != 2 || early != 0 {
			t.Errorf("Update in flight %d, Delete queued %d, of them due before the Update's lease ends %d", u, d, early)
		}
	})
	t.Run("Create tried, an edit held, a moderator's hide: the Delete goes at once", func(t *testing.T) {
		e := newTalkEnv(t)
		cmt, note := e.comment("first")
		e.ne.produce()
		e.mustExec(`UPDATE ap_deliveries SET attempts = 1 WHERE state = 'queued' AND outbox_id = (SELECT id FROM ap_outbox WHERE ap_id = $1)`, note+"/activity")
		e.edit(cmt, "first, edited")
		e.ne.produce()
		_, admin := e.user("boss", "admin")
		e.must(200, admin, "POST", "/api/mod/actions", map[string]any{"action": "hide", "kind": "comment", "id": cmt, "reason": "test"})
		e.ne.produce()
		if d, _ := e.queued(note + "#delete"); d != 2 || len(e.updates(note)) != 0 {
			t.Errorf("after the hide: Delete %d queued, %d Updates", d, len(e.updates(note)))
		}
	})
	t.Run("two edits: the second waits for the first, and carries the newest text", func(t *testing.T) {
		e := newTalkEnv(t)
		cmt, note := e.comment("first")
		e.ne.produce()
		e.delivered(note + "/activity")
		e.edit(cmt, "second")
		e.ne.produce()
		e.edit(cmt, "third")
		e.ne.produce()
		if ups := e.updates(note); len(ups) != 1 || !strings.Contains(ups[0].body, "second") {
			t.Fatalf("two edits, the first Update queued: %d Updates", len(ups))
		}
		e.edit(cmt, "fourth")
		e.delivered(e.updates(note)[0].id)
		e.clock.add(deliveryOwnWait + time.Second)
		e.ne.produce()
		ups := e.updates(note)
		if len(ups) != 2 || !strings.Contains(ups[1].body, "fourth") || strings.Contains(ups[1].body, "third") {
			t.Errorf("after the first Update went: %d Updates, the last %s", len(ups), ups[len(ups)-1].body)
		}
	})
}

// Nothing that names a member who does not share their profile leaves this server (finding B2): an answer to their
// comment, or in a thread they began, and an answer to a hidden comment stay here; a name of theirs in the text stays
// text, never a Mention or a link to their actor.
func TestDBCommentsNameNoNonSharer(t *testing.T) {
	e := newTalkEnv(t)
	records := func() int { return e.count(`SELECT count(*) FROM ap_comment_out`) }
	cyCmt := num(e.must(200, e.cy, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "secret cy"})["comment"].(map[string]any)["id"])
	e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "thanks", "parentId": cyCmt})
	if n := records(); n != 0 {
		t.Errorf("an answer to a member who shares nothing is on its way out (%d records)", n)
	}
	// In a thread cy began, answering a member who shares: still cy's thread.
	_, bob := e.member("bob", false)
	bobCmt := num(e.must(200, bob, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "me too", "parentId": cyCmt})["comment"].(map[string]any)["id"])
	if records() != 0 {
		t.Error("bob's answer in cy's thread is on its way out")
	}
	e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "hi bob", "parentId": bobCmt})
	if records() != 0 {
		t.Error("an answer in a thread begun by a member who shares nothing is on its way out")
	}
	// An answer to a hidden comment (staff may answer one).
	_, admin := e.member("boss", false)
	e.mustExec(`UPDATE users SET role = 'admin' WHERE username = 'boss'`)
	anaCmt, _ := e.comment("ana's top")
	e.ne.produce()
	e.must(200, admin, "POST", "/api/mod/actions", map[string]any{"action": "hide", "kind": "comment", "id": anaCmt, "reason": "test"})
	e.ne.produce()
	before := records()
	e.must(200, admin, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "about this", "parentId": anaCmt})
	if records() != before {
		t.Error("an answer to a hidden comment is on its way out")
	}
	// cy named in the middle of a comment: text, no Mention tag, no link to cy's actor.
	_, note := e.comment("I agree with @cyd on this")
	e.ne.produce()
	var cr outRow
	for _, r := range e.ne.outRows() {
		if r.id == note+"/activity" {
			cr = r
		}
	}
	if !strings.Contains(cr.body, "@cyd") || strings.Contains(cr.body, "/users/cyd") || strings.Contains(cr.body, `"Mention"`) {
		t.Errorf("a non-sharing member named in the text: %s", cr.body)
	}
	// The producer asks again: a thread whose first author stops sharing before the Create is made stays here.
	_, ed := e.member("edd", false)
	var edID int64
	e.a.db.QueryRow(`SELECT id FROM users WHERE username = 'edd'`).Scan(&edID)
	edCmt := num(e.must(200, ed, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "edd here"})["comment"].(map[string]any)["id"])
	e.ne.produce()
	ans := num(e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "hello edd", "parentId": edCmt})["comment"].(map[string]any)["id"])
	e.mustExec(`UPDATE users SET federates = false WHERE id = $1`, edID)
	e.ne.produce()
	if _, ok := e.queued(fmt.Sprintf("https://here.example/users/ana/comments/%d/activity", ans)); ok {
		t.Error("the answer went out after the thread's first author stopped sharing")
	}
	for _, r := range e.ne.outRows() {
		if strings.Contains(r.body, "secret cy") || strings.Contains(r.body, fmt.Sprintf("/users/cyd/comments/%d", cyCmt)) {
			t.Errorf("cy's comment is named in %s", r.id)
		}
	}
}

// An edit made while the producer holds the record it read (finding S4): the Create it was making rolls back, and the
// next pass sends the text as edited; no Update is needed and the old text never leaves.
func TestDBCommentEditWhileProduced(t *testing.T) {
	e := newTalkEnv(t)
	cmt, note := e.comment("old words")
	reached, release := make(chan struct{}), make(chan struct{})
	var once, rel sync.Once
	e.a.fed.commentHook = func(stage string) {
		if stage != "read" {
			return
		}
		first := false
		once.Do(func() { first = true; close(reached) })
		if first {
			<-release
		}
	}
	defer rel.Do(func() { close(release) })
	done := make(chan struct{})
	go func() { defer close(done); e.ne.produce() }()
	<-reached
	e.edit(cmt, "new words")
	rel.Do(func() { close(release) })
	<-done
	e.ne.produce()
	var cr outRow
	for _, r := range e.ne.outRows() {
		if r.id == note+"/activity" {
			cr = r
		}
	}
	if !strings.Contains(cr.body, "new words") || strings.Contains(cr.body, "old words") || len(e.updates(note)) != 0 {
		t.Errorf("the Create after an edit during its making: %s, %d Updates", cr.body, len(e.updates(note)))
	}
}

// Switches that could not be read when a member comments (finding S5): the way out is written and held, not dropped;
// once they read, the Create goes.
func TestDBCommentSwitchesUnread(t *testing.T) {
	e := newTalkEnv(t)
	f := e.a.fed
	keep := f.switches
	f.switches = func() (bool, bool) { return false, false }
	_, note := e.comment("while unread")
	if n := e.count(`SELECT count(*) FROM ap_comment_out`); n != 1 {
		t.Fatalf("%d records written while the switches could not be read", n)
	}
	if _, err := e.a.sendOut(context.Background()); !errors.Is(err, errSwitchesUnread) {
		t.Errorf("the producer while the switches cannot be read: %v", err)
	}
	f.switches = keep
	e.ne.produce()
	if _, ok := e.queued(note + "/activity"); !ok {
		t.Error("the comment did not go out once the switches read")
	}
	_ = slices.Contains[[]string]
}

// A notification that fails once (finding S3): the reply or the like rolls back with it, the worker tries the row again,
// and the member is told; nothing is kept with nobody told.
func TestDBRemoteTalkNotifiedAfterFailure(t *testing.T) {
	e := newRepliesEnv(t)
	failOnce := func() {
		var once sync.Once
		e.a.fed.notifyHook = func() error {
			var err error
			once.Do(func() { err = errors.New("failing once") })
			return err
		}
	}
	again := func() {
		t.Helper()
		e.clock.add(inboxRetries[0] + time.Second)
		for {
			worked, err := e.q.step(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !worked {
				return
			}
		}
	}
	failOnce()
	e.send(e.bo(), boID, "Create", reply(boID, 1, e.postID, "hi"))
	if _, ok := e.kept(boID + "/statuses/1"); ok || !strings.HasPrefix(e.outcome(), "queued:") {
		t.Fatalf("the reply whose notification failed: kept %v, %s", ok, e.outcome())
	}
	again()
	if _, ok := e.kept(boID + "/statuses/1"); !ok || !slices.Equal(e.notes(e.anaID), []string{"comment"}) {
		t.Errorf("after the retry: kept %v, ana told %v (%s)", ok, e.notes(e.anaID), e.outcome())
	}
	failOnce()
	e.deliver(activity(boID, "Like", e.postID, map[string]any{"id": boID + "#l1"}), e.bo())
	again()
	if !slices.Equal(e.notes(e.anaID), []string{"comment", "like"}) || e.count(`SELECT count(*) FROM likes WHERE post_id = $1`, e.post) != 1 {
		t.Errorf("after the like's retry: ana told %v (%s)", e.notes(e.anaID), e.outcome())
	}
}

// A full delivery queue refuses a comment's Create (finding S2): the record is held on the delivery schedule and tried
// again, never removed with nothing sent.
func TestDBCommentQueueFull(t *testing.T) {
	e := newTalkEnv(t)
	e.mustExec(`INSERT INTO ap_outbox (ap_id, actor_id, body, fanned_out, created_at) SELECT 'https://here.example/actor/w/' || g, NULL, '{}', false, now() FROM generate_series(1, $1) g`, deliveryWaitingMax)
	_, note := e.comment("while the queue is full")
	e.ne.produce()
	var tries int
	if err := e.a.db.QueryRow(`SELECT tries FROM ap_comment_out`).Scan(&tries); err != nil || tries != 1 {
		t.Fatalf("the record after a full queue: tries %d, %v", tries, err)
	}
	e.mustExec(`DELETE FROM ap_outbox WHERE ap_id LIKE 'https://here.example/actor/w/%'`)
	e.ne.produce()
	if _, ok := e.queued(note + "/activity"); ok {
		t.Error("the Create went before its wait was over")
	}
	e.clock.add(deliveryRetries[0] + time.Second)
	e.ne.produce()
	if n, ok := e.queued(note + "/activity"); !ok || n != 2 {
		t.Errorf("after its wait: made %v, %d queued", ok, n)
	}
}
