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

	"github.com/lib/pq"
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

// wired is the delivery queue pointed at a stub inbox server, which answers each host with the status set for it (202
// when none is).
type wired struct {
	e     *talkEnv
	stub  *inboxStub
	mu    sync.Mutex
	codes map[string]int
}

func (e *talkEnv) wire() *wired {
	w := &wired{e: e, stub: newInboxStub(e.clock.now, e.pub), codes: map[string]int{}}
	w.stub.answer = func(host, _, _ string) (int, string) {
		w.mu.Lock()
		defer w.mu.Unlock()
		if c, ok := w.codes[host]; ok {
			return c, ""
		}
		return 202, ""
	}
	e.out.fetch = deliveryStub(e.t, w.stub)
	e.out.fetch.origin = e.a.fed.origin
	// fan's server moves to a name the stub answers for (deliveryStub), before the comments the test makes.
	e.allow(inboxB)
	e.mustExec(`UPDATE ap_actors SET ap_id = 'https://' || $1 || '/users/fan', inbox = 'https://' || $1 || '/users/fan/inbox', shared_inbox = 'https://' || $1 || '/inbox'
		WHERE ap_id = 'https://third.example/users/fan'`, inboxB)
	e.mustExec(`UPDATE users SET domain = $1, username = 'fan@' || $1 WHERE username = 'fan@third.example'`, inboxB)
	e.out.pace.wait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return w
}

func (w *wired) answer(host string, code int) {
	w.mu.Lock()
	w.codes[host] = code
	w.mu.Unlock()
}

// drain runs the queue's steps until none has work at this moment of the test's clock.
func (w *wired) drain() {
	w.e.t.Helper()
	for i := 0; ; i++ {
		worked, err := w.e.out.step(context.Background())
		if err != nil {
			w.e.t.Fatal(err)
		}
		if !worked {
			return
		}
		if i > 200 {
			w.e.t.Fatal("the queue never ran out of work")
		}
	}
}

// got lists the activities host was sent, in order, each by what follows the comment's Note id (note).
func (w *wired) got(host, note string) []string {
	var out []string
	for _, r := range w.stub.requests() {
		if r.host == host && strings.HasPrefix(r.id, note) {
			out = append(out, strings.TrimPrefix(r.id, note))
		}
	}
	return out
}

const (
	inboxA = "social.example" // cat's server, one of ana's followers
	inboxB = "b.fan.example"  // fan's, once wire moved it
)

// Every order of a comment's Create, its edits and its Delete, at each inbox on its own (finding B1, and point 2 of the
// second review): an Update is enqueued at once and comes after the activity before it, so at an inbox where that one is
// still on its way the Update waits, and at the others it goes; the Delete takes back the comment's whole line at each
// inbox, so nothing deleted goes out after it, and it goes to every inbox an earlier activity may have reached.
func TestDBCommentOrders(t *testing.T) {
	t.Run("Create untried, an edit, the Delete: nothing goes", func(t *testing.T) {
		e := newTalkEnv(t)
		cmt, note := e.comment("first")
		e.ne.produce()
		e.edit(cmt, "first, edited")
		e.ne.produce()
		if ups := e.updates(note); len(ups) != 1 || len(ups[0].inboxes) != 2 {
			t.Fatalf("the Update of an edit: %v", ups)
		}
		e.del(cmt)
		e.ne.produce()
		d, dok := e.queued(note + "#delete")
		if d != 0 || !dok || e.count(`SELECT count(*) FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.ap_id LIKE $1 || '%' AND d.state = 'queued'`, note) != 0 {
			t.Errorf("queued after the Delete: Delete %d (made %v)", d, dok)
		}
	})
	t.Run("Create tried, an edit, the Delete: the line is given up, the Delete goes", func(t *testing.T) {
		e := newTalkEnv(t)
		cmt, note := e.comment("first")
		e.ne.produce()
		e.mustExec(`UPDATE ap_deliveries SET attempts = 1 WHERE state = 'queued' AND outbox_id = (SELECT id FROM ap_outbox WHERE ap_id = $1)`, note+"/activity")
		e.edit(cmt, "first, edited")
		e.ne.produce()
		ups := e.updates(note)
		e.del(cmt)
		e.ne.produce()
		c, _ := e.queued(note + "/activity")
		u, _ := e.queued(ups[0].id)
		if d, _ := e.queued(note + "#delete"); c != 0 || u != 0 || d != 2 {
			t.Errorf("after the Delete: Create %d queued, Update %d, Delete %d", c, u, d)
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
	t.Run("Create untried, its Update claimed at one inbox, the Delete: the Delete stays there", func(t *testing.T) {
		// A worker that claimed the Update holds it only until it sees the Create still queued; the Delete is not
		// dropped at that inbox meanwhile, since the Update is not among the rows given up.
		e := newTalkEnv(t)
		cmt, note := e.comment("first")
		e.ne.produce()
		e.edit(cmt, "first, edited")
		e.ne.produce()
		ups := e.updates(note)
		lease := e.clock.now().Add(deliveryLease)
		e.mustExec(`UPDATE ap_deliveries SET attempts = 1, locked_until = $2 WHERE outbox_id = (SELECT id FROM ap_outbox WHERE ap_id = $1) AND host = $3`, ups[0].id, lease, inboxA)
		e.del(cmt)
		e.ne.produce()
		at := e.count(`SELECT count(*) FROM ap_deliveries d JOIN ap_outbox o ON o.id = d.outbox_id WHERE o.ap_id = $1 AND d.host = $2 AND d.next_at >= $3`, note+"#delete", inboxA, lease)
		if d, _ := e.queued(note + "#delete"); d != 1 || at != 1 {
			t.Errorf("Delete queued %d, at the inbox of the claimed Update after its lease %d", d, at)
		}
	})
	t.Run("Create sent at A and retrying at B, an edit, the Delete: each inbox gets them in order", func(t *testing.T) {
		e := newTalkEnv(t)
		w := e.wire()
		w.answer(inboxB, 503)
		cmt, note := e.comment("first")
		w.drain()
		e.edit(cmt, "first, edited")
		w.drain()
		if a, b := w.got(inboxA, note), w.got(inboxB, note); !slices.Equal(a, []string{"/activity", "#update-1"}) || !slices.Equal(b, []string{"/activity"}) {
			t.Fatalf("with B failing: A got %v, B got %v", a, b)
		}
		// The Delete gives up B's Create (tried once, it may have arrived) and the Update behind it, and goes at once.
		e.del(cmt)
		w.answer(inboxB, 202)
		w.drain()
		a, b := w.got(inboxA, note), w.got(inboxB, note)
		if !slices.Equal(a, []string{"/activity", "#update-1", "#delete"}) || !slices.Equal(b, []string{"/activity", "#delete"}) {
			t.Errorf("A got %v, B got %v", a, b)
		}
	})
	t.Run("the Create in flight at A as the Delete is sent there: the Delete waits for it", func(t *testing.T) {
		e := newTalkEnv(t)
		w := e.wire()
		cmt, note := e.comment("first")
		e.ne.produce()
		e.edit(cmt, "first, edited")
		e.ne.produce()
		// Another worker holds A's Create (a request may be in flight); the Delete takes back the Update and waits for
		// the lease, but a claim made after its fan-out's snapshot is not seen there: the send asks again.
		lease := e.clock.now().Add(deliveryLease)
		e.mustExec(`UPDATE ap_deliveries SET attempts = 1, locked_until = $3 WHERE host = $1 AND outbox_id = (SELECT id FROM ap_outbox WHERE ap_id = $2)`, inboxA, note+"/activity", lease)
		e.del(cmt)
		e.ne.produce()
		e.mustExec(`UPDATE ap_deliveries SET next_at = $2 WHERE outbox_id = (SELECT id FROM ap_outbox WHERE ap_id = $1)`, note+"#delete", e.clock.now())
		w.drain()
		if a := w.got(inboxA, note); len(a) != 0 {
			t.Errorf("A got %v while its Create was in flight", a)
		}
	})
	t.Run("an edit while the Create retries at B: the Update waits there, then follows it", func(t *testing.T) {
		e := newTalkEnv(t)
		w := e.wire()
		w.answer(inboxB, 503)
		cmt, note := e.comment("first")
		w.drain()
		e.edit(cmt, "second")
		w.drain()
		w.answer(inboxB, 500)
		e.clock.add(deliveryRetries[0] + time.Minute)
		w.drain() // B fails the Create again; the Update still waits behind it
		e.edit(cmt, "third")
		w.drain()
		if b := w.got(inboxB, note); !slices.Equal(b, []string{"/activity", "/activity"}) {
			t.Fatalf("B got %v while its Create retries", b)
		}
		w.answer(inboxB, 202)
		e.clock.add(deliveryRetries[len(deliveryRetries)-1] + time.Minute)
		w.drain()
		a, b := w.got(inboxA, note), w.got(inboxB, note)
		if !slices.Equal(a, []string{"/activity", "#update-1", "#update-2"}) || !slices.Equal(b, []string{"/activity", "/activity", "/activity", "#update-1", "#update-2"}) {
			t.Errorf("A got %v, B got %v", a, b)
		}
		ups := e.updates(note)
		if len(ups) != 2 || !strings.Contains(ups[1].body, "third") {
			t.Errorf("the second Update: %v", ups)
		}
	})
	t.Run("the Create given up for good at B: the Update goes there alone", func(t *testing.T) {
		e := newTalkEnv(t)
		w := e.wire()
		w.answer(inboxB, 503)
		cmt, note := e.comment("first")
		w.drain()
		e.edit(cmt, "second")
		w.drain()
		// Its attempts spent, or its 48 hours past: the row fails, and what came after it is no longer held.
		e.mustExec(`UPDATE ap_deliveries SET state = 'failed', locked_until = NULL, failed_at = now() WHERE host = $1 AND outbox_id = (SELECT id FROM ap_outbox WHERE ap_id = $2)`, inboxB, note+"/activity")
		w.answer(inboxB, 202)
		e.clock.add(deliveryRetries[0] + time.Minute)
		w.drain()
		if b := w.got(inboxB, note); !slices.Equal(b, []string{"/activity", "#update-1"}) {
			t.Errorf("B got %v", b)
		}
	})
	t.Run("an Update waiting for its fan-out, the Delete: the Update never goes, the Delete goes at once", func(t *testing.T) {
		e := newTalkEnv(t)
		cmt, note := e.comment("first")
		e.ne.produce()
		e.delivered(note + "/activity")
		e.edit(cmt, "first, edited")
		e.ne.produce()
		ups := e.updates(note)
		// As a full queue leaves it: the activity written, its rows not yet.
		e.mustExec(`UPDATE ap_outbox SET fanned_out = false, targets = $2 WHERE ap_id = $1`, ups[0].id, pq.Array(ups[0].inboxes))
		e.mustExec(`DELETE FROM ap_deliveries WHERE outbox_id = (SELECT id FROM ap_outbox WHERE ap_id = $1)`, ups[0].id)
		e.del(cmt)
		e.ne.produce()
		d, _ := e.queued(note + "#delete")
		waiting := e.count(`SELECT count(*) FROM ap_outbox WHERE NOT fanned_out`)
		u, _ := e.queued(ups[0].id)
		if d != 2 || waiting != 0 || u != 0 {
			t.Errorf("Delete queued %d, activities waiting %d, Update queued %d", d, waiting, u)
		}
	})
	t.Run("Create tried, an edit, a moderator's hide: the Delete goes at once", func(t *testing.T) {
		e := newTalkEnv(t)
		cmt, note := e.comment("first")
		e.ne.produce()
		e.mustExec(`UPDATE ap_deliveries SET attempts = 1 WHERE state = 'queued' AND outbox_id = (SELECT id FROM ap_outbox WHERE ap_id = $1)`, note+"/activity")
		e.edit(cmt, "first, edited")
		e.ne.produce()
		_, admin := e.user("boss", "admin")
		e.must(200, admin, "POST", "/api/mod/actions", map[string]any{"action": "hide", "kind": "comment", "id": cmt, "reason": "test"})
		e.ne.produce()
		u, _ := e.queued(e.updates(note)[0].id)
		if d, _ := e.queued(note + "#delete"); d != 2 || u != 0 {
			t.Errorf("after the hide: Delete %d queued, Update %d queued", d, u)
		}
	})
}

// A like, its Undo and a like again of a post of another server (point 8 of the second review): the second Like comes
// after the Undo, so while the Undo still retries at the author's inbox the Like waits, and the author's server ends
// with the post liked.
func TestDBLikeAgainAfterUndo(t *testing.T) {
	e := newTalkEnv(t)
	w := e.wire()
	e.send(e.bo(), boID, "Create", noteObj(40, "bo's post", nil))
	p := e.remotePostID(boID + "/statuses/40")
	like := func() { e.t.Helper(); e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/like", p), nil) }
	sent := func() []string {
		kinds := map[string]string{}
		for _, r := range e.ne.outRows() {
			kinds[r.id] = r.kind
		}
		var out []string
		for _, r := range w.stub.requests() {
			if r.path == "/users/bo/inbox" {
				out = append(out, kinds[r.id])
			}
		}
		return out
	}
	w.answer(inboxA, 503)
	like()
	w.drain() // the Like is tried once, and retries
	like()    // the Undo: the Like is given up, the Undo goes after its attempt
	w.drain() // and is tried once, and retries
	like()    // liked again
	w.drain()
	if got := sent(); !slices.Equal(got, []string{"Like", "Undo"}) {
		t.Fatalf("while the Undo retries: %v", got)
	}
	w.answer(inboxA, 202)
	e.clock.add(deliveryRetries[len(deliveryRetries)-1] + time.Minute)
	w.drain()
	if got := sent(); !slices.Equal(got, []string{"Like", "Undo", "Undo", "Like"}) {
		t.Errorf("bo's inbox got %v", got)
	}
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
	// And about the member answered, whose name opens the answer (point 5 of the second review): in ana's own thread,
	// an answer to fay, who stops sharing before the Create is made, stays here.
	fayID, fay := e.member("fay", false)
	top, _ := e.comment("ana's thread")
	e.ne.produce()
	fayCmt := num(e.must(200, fay, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "fay here", "parentId": top})["comment"].(map[string]any)["id"])
	e.ne.produce()
	toFay := num(e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "hello fay", "parentId": fayCmt})["comment"].(map[string]any)["id"])
	e.mustExec(`UPDATE users SET federates = false WHERE id = $1`, fayID)
	e.ne.produce()
	if _, ok := e.queued(fmt.Sprintf("https://here.example/users/ana/comments/%d/activity", toFay)); ok {
		t.Error("the answer to fay went out after fay stopped sharing")
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

// Local mode on a database that federated (finding S1): replies and likes of other servers are left out of lists before
// their LIMIT and out of counts, so a page of comments is full and its counts agree with it; in a mode that federates,
// a reply of an account its server suspended is neither listed nor counted.
func TestDBRemoteHiddenInLocalMode(t *testing.T) {
	for _, part := range hideRemoteParts {
		if !strings.Contains(postCols("$6")+commentCols+feedCommentsSQL+repliesSQL, part[0]) {
			t.Errorf("hideRemote's fragment is in none of the statements it is for: %s", part[0])
		}
	}
	// On a database that never federated (point 6 of the second review): every fragment plainStatements takes out is
	// there, and what is left asks nothing of an account of another server, the preview being the statement of before.
	for _, part := range plainParts {
		if !strings.Contains(feedSQL+commentsSQL(feedCommentsSQL)+commentsSQL(repliesSQL), part[0]) {
			t.Errorf("plainStatements' fragment is in none of the statements it is for: %s", part[0])
		}
	}
	for _, q := range []string{plainStatements(feedSQL), plainStatements(commentsSQL(feedCommentsSQL)), plainStatements(commentsSQL(repliesSQL))} {
		if strings.Contains(q, "ap_actors") || strings.Contains(q, "ap_id IS NULL OR") || strings.Contains(q, "comments_remote") {
			t.Errorf("a statement of local mode on a database that never federated still asks about other servers: %s", q)
		}
	}
	if !strings.Contains(plainStatements(commentsSQL(feedCommentsSQL)), feedCommentsLocalSQL) {
		t.Error("the preview in local mode is not the statement of before")
	}
	e := newRepliesEnv(t)
	for i := 0; i < commentPage+2; i++ {
		e.mustExec(`INSERT INTO comments (post_id, user_id, body, ap_id) VALUES ($1, $2, 'r', $3)`, e.post, e.boUser, fmt.Sprintf("%s/statuses/x%d", boID, i))
	}
	e.mustExec(`INSERT INTO likes (post_id, user_id, ap_id) VALUES ($1, $2, $3), ($1, $4, $5)`, e.post, e.boUser, boID+"#l", e.catID, catID+"#l")
	e.must(200, e.cy, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "local one"})
	e.must(200, e.cy, "POST", fmt.Sprintf("/api/posts/%d/like", e.post), nil)
	// cy answers one of the remote replies: in a thread another server's reply began, so not listed in local mode, and
	// not counted either.
	var first int64
	e.a.db.QueryRow(`SELECT id FROM comments WHERE ap_id = $1`, boID+"/statuses/x0").Scan(&first)
	e.must(200, e.cy, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "an answer", "parentId": first})
	post := func() map[string]any {
		return e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d", e.post), nil)["posts"].([]any)[0].(map[string]any)
	}
	if p := post(); num(p["commentCount"]) != commentPage+4 || num(p["likes"]) != 3 {
		t.Fatalf("federated: %v comments, %v likes", p["commentCount"], p["likes"])
	}
	f := e.a.fed
	e.a.fed, e.a.remoteKept = nil, true
	res := e.must(200, e.ana, "GET", fmt.Sprintf("/api/posts/%d/comments", e.post), nil)
	p := post()
	e.a.fed, e.a.remoteKept = f, false
	if got := bodies(func() []map[string]any {
		var out []map[string]any
		for _, c := range res["comments"].([]any) {
			out = append(out, c.(map[string]any))
		}
		return out
	}()); !slices.Equal(got, []string{"local one"}) || res["more"] != false {
		t.Errorf("local mode, a page of comments: %q, more %v", got, res["more"])
	}
	if num(p["commentCount"]) != 1 || num(p["likes"]) != 1 || len(anyList(p["comments"])) != 1 {
		t.Errorf("local mode, the post: %v comments, %v likes, %d previewed", p["commentCount"], p["likes"], len(anyList(p["comments"])))
	}
	// In local mode on a database that never federated, the app runs the plain statements.
	e.a.fed = nil
	if q := e.a.localStatements(commentsSQL(feedCommentsSQL)); !strings.Contains(q, feedCommentsLocalSQL) {
		t.Error("local mode on a database that never federated runs the statements of a federating mode")
	}
	e.a.fed = f
	// A start in local mode on this database finds that it keeps rows of other servers.
	local := newApp(e.a.db, t.TempDir())
	if err := local.start(func(string) string { return "" }); err != nil || !local.remoteKept || local.fed != nil {
		t.Errorf("a start in local mode: %v, remote rows kept %v", err, local.remoteKept)
	}
	// A suspended account's replies: neither listed nor counted.
	e.mustExec(`UPDATE ap_actors SET state = 'suspended' WHERE user_id = $1`, e.boUser)
	if p := post(); num(p["commentCount"]) != 1 || len(anyList(p["comments"])) != 1 {
		t.Errorf("a suspended account's replies: counted %v, previewed %d", p["commentCount"], len(anyList(p["comments"])))
	}
}

// An answer under a post of another server that could not go there (point 4 of the second review): it answers a member
// here who no longer shares their profile, or a hidden comment. It is refused with the reason, never kept here unseen by
// that post's people.
func TestDBRemoteAnswerThatWouldStay(t *testing.T) {
	e := newTalkEnv(t)
	e.send(e.bo(), boID, "Create", noteObj(70, "bo's post", nil))
	p := e.remotePostID(boID + "/statuses/70")
	comment := func(parent int64) map[string]any { return map[string]any{"body": "an answer", "parentId": parent} }
	bobID, bob := e.member("bob", false)
	adminID, admin := e.member("boss", false)
	e.mustExec(`UPDATE users SET role = 'admin' WHERE id = $1`, adminID)
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state) VALUES ($1, $3, 'accepted'), ($2, $3, 'accepted')`, bobID, adminID, e.boUser)
	bobCmt := num(e.must(200, bob, "POST", fmt.Sprintf("/api/posts/%d/comments", p), map[string]any{"body": "bob here"})["comment"].(map[string]any)["id"])
	e.ne.produce()
	e.mustExec(`UPDATE users SET federates = false WHERE id = $1`, bobID)
	if out := e.must(403, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", p), comment(bobCmt)); out["error"] != talkStaysHere {
		t.Errorf("an answer to a member who stopped sharing: %v", out)
	}
	e.mustExec(`UPDATE users SET federates = true WHERE id = $1`, bobID)
	e.must(200, admin, "POST", "/api/mod/actions", map[string]any{"action": "hide", "kind": "comment", "id": bobCmt, "reason": "test"})
	e.ne.produce()
	if out := e.must(403, admin, "POST", fmt.Sprintf("/api/posts/%d/comments", p), comment(bobCmt)); out["error"] != talkStaysHere {
		t.Errorf("an answer to a hidden comment: %v", out)
	}
	if n := e.count(`SELECT count(*) FROM comments WHERE post_id = $1 AND body LIKE '%an answer'`, p); n != 0 {
		t.Errorf("%d refused answers kept", n)
	}
	// An answer that can go still goes.
	e.must(200, admin, "POST", "/api/mod/actions", map[string]any{"action": "restore", "kind": "comment", "id": bobCmt, "reason": "test"})
	e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", p), comment(bobCmt))
	if n := e.count(`SELECT count(*) FROM ap_comment_out o JOIN comments c ON c.id = o.comment_id WHERE c.post_id = $1 AND c.body LIKE '%an answer'`, p); n != 1 {
		t.Errorf("the answer that can go has %d records", n)
	}
}

// The forward's bounds (finding S2): never a body that names anyone in bto or bcc, never one larger than forwardBodyMax,
// none while an activity waits for its fan-out (the members' own deliveries first), and an hourly bound per member and
// in all beside the one per sending server.
func TestDBForwardBounds(t *testing.T) {
	e := newRepliesEnv(t)
	followers := "https://here.example/users/ana/followers"
	n := 0
	try := func(more map[string]any) int {
		t.Helper()
		n++
		note := reply(boID, 100+n, e.postID, "hi", followers)
		body := activity(boID, "Create", note, more)
		e.deliver(body, e.bo())
		if _, ok := e.kept(fmt.Sprintf("%s/statuses/%d", boID, 100+n)); !ok {
			t.Fatalf("reply %d not kept: %s", n, e.outcome())
		}
		return len(e.forwards())
	}
	if got := try(nil); got != 1 {
		t.Fatalf("the first reply: %d forwards", got)
	}
	if got := try(map[string]any{"bcc": []string{"https://social.example/users/secret"}}); got != 1 {
		t.Errorf("a reply with bcc was forwarded")
	}
	if got := try(map[string]any{"padding": strings.Repeat("x", forwardBodyMax)}); got != 1 {
		t.Errorf("a reply larger than %d bytes was forwarded", forwardBodyMax)
	}
	// The queue is asked before any allowance is taken (point 3 of the second review): with room for one forward in
	// all, a reply refused while an activity waits leaves that room to the next.
	e.a.fed.forwardsAll = newLimiter(1, time.Hour)
	e.mustExec(`INSERT INTO ap_outbox (ap_id, actor_id, body, fanned_out, created_at) VALUES ('https://here.example/actor/w/1', $1, '{}', false, now())`, e.anaID)
	if got := try(nil); got != 1 {
		t.Errorf("a reply was forwarded while an activity waits for its fan-out")
	}
	e.mustExec(`DELETE FROM ap_outbox WHERE ap_id = 'https://here.example/actor/w/1'`)
	if got := try(nil); got != 2 {
		t.Errorf("the reply refused by the queue took the allowance: %d forwards", got)
	}
	// The Federation tab reads the counts kept since the start.
	e.a.fed.commentsNotSent.Add(2)
	nums, err := e.out.numbers(context.Background())
	if err != nil || nums.ForwardsSkipped != 3 || nums.CommentsNotSent != 2 {
		t.Errorf("the numbers: %+v %v", nums, err)
	}
	e.a.fed.forwardsAll = newLimiter(forwardsMaxHour, time.Hour)
	for range forwardsPerMemberHour {
		e.a.fed.forwardsFor.add(fmt.Sprint(e.anaID))
	}
	if got := try(nil); got != 2 {
		t.Errorf("a reply was forwarded past the member's hourly bound")
	}
	e.a.fed.forwardsFor = newLimiter(forwardsPerMemberHour, time.Hour)
	for range forwardsMaxHour {
		e.a.fed.forwardsAll.add("")
	}
	if got := try(nil); got != 2 {
		t.Errorf("a reply was forwarded past the bound in all")
	}
	e.a.fed.forwardsAll = newLimiter(forwardsMaxHour, time.Hour)
	if got := try(nil); got != 3 {
		t.Errorf("within the bounds again: %d forwards", got)
	}
}

// The small points of the review (C1, C2, C3, C5, C7).
func TestDBRemoteTalkSmallPoints(t *testing.T) {
	e := newTalkEnv(t)
	// C2: a member's comment on an unlisted post of another server is unlisted: the public collection in cc only.
	e.send(e.bo(), boID, "Create", noteObj(60, "unlisted over there", map[string]any{"to": []string{boID + "/followers"}, "cc": []string{asPublic}}))
	up := e.remotePostID(boID + "/statuses/60")
	c := num(e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", up), map[string]any{"body": "nice"})["comment"].(map[string]any)["id"])
	e.ne.produce()
	for _, r := range e.ne.outRows() {
		if r.id == fmt.Sprintf("https://here.example/users/ana/comments/%d/activity", c) {
			if strings.Contains(r.body, `"to":["https://www.w3.org/ns/activitystreams#Public"`) || !strings.Contains(r.body, `"cc":["https://www.w3.org/ns/activitystreams#Public"`) {
				t.Errorf("a comment on an unlisted post: %s", r.body)
			}
		}
	}
	// C3: an Update that adds inReplyTo to a kept post leaves it a post, and its Delete removes it.
	e.send(e.bo(), boID, "Create", noteObj(61, "a post", nil))
	edit := noteObj(61, "now a reply", map[string]any{"inReplyTo": e.postID, "updated": time.Now().UTC().Format(time.RFC3339)})
	e.send(e.bo(), boID, "Update", edit)
	if _, ok := e.kept(boID + "/statuses/61"); ok || e.count(`SELECT count(*) FROM posts WHERE ap_id = $1 AND body = 'now a reply'`, boID+"/statuses/61") != 1 {
		t.Errorf("an Update adding inReplyTo to a kept post: %s", e.outcome())
	}
	e.send(e.bo(), boID, "Delete", map[string]any{"id": boID + "/statuses/61", "type": "Tombstone"})
	if e.count(`SELECT count(*) FROM posts WHERE ap_id = $1`, boID+"/statuses/61")+e.count(`SELECT count(*) FROM comments WHERE ap_id = $1`, boID+"/statuses/61") != 0 {
		t.Error("something carrying the deleted id is left")
	}
	// C7: the commenter's blocks apply to the post's author's followers.
	e.must(200, e.cy, "PUT", "/api/me", map[string]any{"federates": true})
	if _, err := e.a.fed.ensureKey(context.Background(), keyOwner(e.cyID)); err != nil {
		t.Fatal(err)
	}
	e.mustExec(`INSERT INTO blocks (user_id, target_id) VALUES ($1, $2)`, e.cyID, e.catID)
	cyc := num(e.must(200, e.cy, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "from cy"})["comment"].(map[string]any)["id"])
	e.ne.produce()
	for _, r := range e.ne.outRows() {
		if r.id == fmt.Sprintf("https://here.example/users/cyd/comments/%d/activity", cyc) && (slices.Contains(r.inboxes, catID+"/inbox") || !slices.Contains(r.inboxes, "https://third.example/inbox")) {
			t.Errorf("cy's comment, cy blocking cat: %v", r.inboxes)
		}
	}
	// C1: a removed remote reply names nobody: no account and no link.
	e.send(e.bo(), boID, "Create", reply(boID, 62, e.postID, "hi"))
	b, _ := e.kept(boID + "/statuses/62")
	e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "thanks", "parentId": b.id})
	e.send(e.bo(), boID, "Delete", boID)
	res := e.must(200, e.cy, "GET", fmt.Sprintf("/api/comments/%d/thread", b.id), nil)
	root := res["comments"].([]any)[0].(map[string]any)
	if num(root["userId"]) != 0 || root["remote"] != nil || res["users"].(map[string]any)[fmt.Sprint(e.boUser)] != nil {
		t.Errorf("the placeholder of a gone account's reply: %v, users %v", root, res["users"])
	}
	// C5: the probe of comments gone without a Delete goes a batch a pass, on from where it stopped: a first batch of
	// records whose comments are kept, then records of comments gone, which the second pass reaches.
	e.mustExec(`DELETE FROM ap_comment_out`)
	e.mustExec(`INSERT INTO comments (post_id, user_id, body) SELECT $1, $2, 'c' || g FROM generate_series(1, $3) g`, e.post, e.anaID, remoteBatch)
	e.mustExec(`INSERT INTO ap_comment_out (comment_id, post_id, user_id, state, targets) SELECT id, post_id, user_id, 'sent', '[]' FROM comments WHERE body LIKE 'c%' AND user_id = $1`, e.anaID)
	e.mustExec(`INSERT INTO ap_comment_out (comment_id, post_id, user_id, state, targets) SELECT 1000000 + g, 1, $1, 'sent', '[]' FROM generate_series(1, 200) g`, e.anaID)
	gone := func() int {
		return e.count(`SELECT count(*) FROM ap_comment_out WHERE comment_id > 1000000 AND state = 'delete'`)
	}
	if err := e.a.remoteTalkHousekeep(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := gone()
	if err := e.a.remoteTalkHousekeep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if second := gone(); first != 0 || second != 200 {
		t.Errorf("the probe marked %d on its first pass, %d after its second, want 0 and 200", first, second)
	}
	if postFollowersApartSQL == postRecipientsSQL {
		t.Error("postFollowersApartSQL found nothing to add blocks to")
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

// A restart (point 1 of the second review): a comment's record left waiting at the stop is looked for at the first
// step of the next process, which starts its workers for it, in each state the record can wait in.
func TestDBCommentRecordsAfterRestart(t *testing.T) {
	for _, c := range []struct {
		name string
		make func(e *talkEnv) string // leaves a record waiting and says the activity the next process makes of it
	}{
		{"a Create", func(e *talkEnv) string {
			_, note := e.comment("made before the stop")
			return note + "/activity"
		}},
		{"a Create held while the switches could not be read", func(e *talkEnv) string {
			keep := e.a.fed.switches
			e.a.fed.switches = func() (bool, bool) { return false, false }
			_, note := e.comment("held")
			e.a.sendOut(context.Background())
			e.a.fed.switches = keep
			return note + "/activity"
		}},
		{"an Update the full queue held back", func(e *talkEnv) string {
			cmt, note := e.comment("first")
			e.ne.produce()
			e.edit(cmt, "edited before the stop")
			e.mustExec(`UPDATE ap_comment_out SET tries = 1, due_at = now() - interval '1 second' WHERE state = 'update'`)
			return note + "#update-1"
		}},
		{"a Delete the full queue held back", func(e *talkEnv) string {
			cmt, note := e.comment("first")
			e.ne.produce()
			e.del(cmt)
			e.mustExec(`UPDATE ap_comment_out SET tries = 1, due_at = now() - interval '1 second' WHERE state = 'delete'`)
			return note + "#delete"
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newTalkEnv(t)
			want := c.make(e)
			if n := e.count(`SELECT count(*) FROM ap_comment_out WHERE state <> 'sent'`); n != 1 {
				t.Fatalf("%d records wait at the stop", n)
			}
			// Nothing else for the workers: the rows sent, the switch off so the next process's workers do nothing on
			// their own while the test asks its producer.
			e.mustExec(`DELETE FROM ap_deliveries`)
			e.setFlag("federation", false)
			b := e.again()
			if err := b.fedStart(map[string]string{"FLOWA_ORIGIN": "https://here.example"}); err != nil {
				t.Fatal(err)
			}
			if !b.a.fed.out.running.Load() {
				t.Error("the workers did not start for the record")
			}
			b.a.fed.switches = func() (bool, bool) { return true, true }
			if did, err := b.a.commentOut(context.Background()); !did || err != nil {
				t.Errorf("the producer after the start: %v %v", did, err)
			}
			if _, ok := e.queued(want); !ok {
				t.Errorf("%s was not made after the start", want)
			}
		})
	}
}

// A moderator's hide, then a restore (point 7 of the second review): the hide sends the Delete, the restore sends
// nothing, and an edit after it sends nothing either; the moderator is told the comment stays on this server.
func TestDBRestoredCommentStaysHere(t *testing.T) {
	e := newTalkEnv(t)
	cmt, note := e.comment("first")
	e.ne.produce()
	e.delivered(note + "/activity")
	_, admin := e.user("boss", "admin")
	e.must(200, admin, "POST", "/api/mod/actions", map[string]any{"action": "hide", "kind": "comment", "id": cmt, "reason": "test"})
	e.ne.produce()
	if d, _ := e.queued(note + "#delete"); d != 2 {
		t.Fatalf("the hide's Delete: %d queued", d)
	}
	sent := func() int { return e.count(`SELECT count(*) FROM ap_outbox WHERE ap_id LIKE $1 || '%'`, note) }
	before := sent()
	if out := e.must(200, admin, "POST", "/api/mod/actions", map[string]any{"action": "restore", "kind": "comment", "id": cmt}); out["note"] != restoredStaysHere {
		t.Errorf("the restore's answer: %v", out)
	}
	e.ne.produce()
	e.edit(cmt, "first, edited after the restore")
	e.ne.produce()
	if n := sent(); n != before {
		t.Errorf("%d activities went out after the restore", n-before)
	}
}

// The members a remote reply concerns are told in the order of their ids (point 10 of the second review), so two
// workers trimming the same members' notifications take them in the same order: here ana, the post's author, before
// gus, the member answered, who came later, though the member answered was told first before.
func TestDBReplyNotesInOrder(t *testing.T) {
	e := newTalkEnv(t)
	gusID, gus := e.member("gus", false)
	gc := num(e.must(200, gus, "POST", fmt.Sprintf("/api/posts/%d/comments", e.post), map[string]any{"body": "gus here"})["comment"].(map[string]any)["id"])
	e.deliver(activity(boID, "Create", reply(boID, 90, fmt.Sprintf("https://here.example/users/gus/comments/%d", gc), "hi gus", "https://here.example/users/gus"), nil), e.bo())
	var toAna, toGus int64
	e.a.db.QueryRow(`SELECT COALESCE((SELECT max(id) FROM notifications WHERE user_id = $1 AND fed), 0), COALESCE((SELECT max(id) FROM notifications WHERE user_id = $2 AND fed), 0)`, e.anaID, gusID).Scan(&toAna, &toGus)
	if toAna == 0 || toGus == 0 || toAna > toGus {
		t.Errorf("ana told as notification %d, gus %d (%s)", toAna, toGus, e.outcome())
	}
}
