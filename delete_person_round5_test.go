package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// everyOtherWrite makes every other update or deletion of table fail, as a disk that fills and empties would: the
// first write after it is refused, the next goes through. A producer that counts its own refused write as the record's
// failure then writes the failure (the second write) and logs "failed"; one that does not leaves the record as it was.
func (e *notesEnv) everyOtherWrite(table string) {
	e.t.Helper()
	e.mustExec(`CREATE SEQUENCE IF NOT EXISTS every_other`)
	e.mustExec(`CREATE OR REPLACE FUNCTION every_other_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		IF nextval('every_other') % 2 = 1 THEN RAISE EXCEPTION 'could not extend file'; END IF; RETURN COALESCE(NEW, OLD); END $$`)
	e.mustExec(`CREATE TRIGGER every_other_write BEFORE UPDATE OR DELETE ON ` + table + ` FOR EACH ROW EXECUTE FUNCTION every_other_write()`)
}

// The producers' own writes that the last review found outside outWrite (a comment's Update held for the gap between
// two Updates, a post's Create dropped for a post no longer shown, and branches of a profile's Update: no longer
// shown, the switches unread, no key yet, and the write made with the enqueue): each write refused once is this server's trouble, not a failure of the record,
// so no try is counted and nothing is logged as failed. Each case fails with the plain write it had.
func TestDBOwnWritesAreNoTries(t *testing.T) {
	t.Run("a comment's Update held for the gap", func(t *testing.T) {
		e := newNotesEnv(t)
		e.setFlag("community", true)
		bo, _ := e.remote("bo", "h1.fan.example", true)
		e.follows(bo)
		p := e.postAs(e.ana, map[string]any{"body": "x", "audience": "public"})
		out := e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", p), map[string]any{"body": "a comment"})
		cid := num(out["comment"].(map[string]any)["id"])
		e.produced()
		e.must(200, e.ana, "PUT", fmt.Sprintf("/api/comments/%d", cid), map[string]any{"body": "edited at once"})
		if n := e.count(`SELECT count(*) FROM ap_comment_out WHERE comment_id = $1 AND state = 'update' AND sent_at IS NOT NULL`, cid); n != 1 {
			t.Fatal("the setup: no Update waiting for the gap")
		}
		e.everyOtherWrite("ap_comment_out")
		logs := logged(t)
		e.a.fed.commentsDue.Store(true)
		e.a.commentOut(context.Background())
		if n := e.count(`SELECT count(*) FROM ap_comment_out WHERE comment_id = $1 AND tries = 0`, cid); n != 1 || strings.Contains(logs.String(), fmt.Sprintf("comment %d failed", cid)) {
			t.Errorf("a refused hold counted as the record's failure: tries 0 %d, log %q", n, logs.String())
		}
	})
	t.Run("a post's Create no longer shown", func(t *testing.T) {
		e := newNotesEnv(t)
		bo, _ := e.remote("bo", "h1.fan.example", true)
		e.follows(bo)
		p := e.postAs(e.ana, map[string]any{"body": "x", "audience": "public"})
		e.must(200, e.ana, "PUT", "/api/me", map[string]any{"federates": false})
		e.everyOtherWrite("ap_post_out")
		logs := logged(t)
		e.a.fed.postsDue.Store(true)
		e.a.postOut(context.Background())
		if n := e.count(`SELECT count(*) FROM ap_post_out WHERE post_id = $1 AND state = 'create' AND tries = 0`, p); n != 1 || strings.Contains(logs.String(), fmt.Sprintf("post %d failed", p)) {
			t.Errorf("a refused drop counted as the record's failure: kept with no try %d, log %q", n, logs.String())
		}
	})
	for _, branch := range []string{"no longer shown", "switches unread", "no key yet", "sent"} {
		t.Run("a profile's Update, "+branch, func(t *testing.T) {
			e := newNotesEnv(t)
			bo, _ := e.remote("bo", "h1.fan.example", true)
			e.follows(bo)
			e.produce()
			e.must(200, e.ana, "PUT", "/api/me", map[string]any{"displayName": "Ana Again"})
			e.mustExec(`UPDATE ap_person_out SET due_at = $2 WHERE user_id = $1`, e.anaID, e.clock.now().Add(-1e9))
			f := e.a.fed
			switch branch {
			case "no longer shown":
				e.mustExec(`UPDATE users SET federates = false WHERE id = $1`, e.anaID)
			case "switches unread":
				keep := f.followsOn
				f.followsOn = func() (bool, bool) { return true, false }
				t.Cleanup(func() { f.followsOn = keep })
			case "no key yet":
				// The key maker is left to finish what it has, then held: a request for ana's key still waiting, or one
				// it had just taken, would make the key again after the delete, and the update would then be sent,
				// not put off.
				settleKeys(t, f)
				hold := make(chan struct{})
				f.mu.Lock()
				f.beforeKey = func(keyOwner) { <-hold }
				f.mu.Unlock()
				t.Cleanup(func() { close(hold) })
				e.mustExec(`DELETE FROM ap_keys WHERE user_id = $1`, e.anaID)
			}
			// "sent" changes nothing: the update is made, and the refused write is the one that goes with its enqueue.
			e.everyOtherWrite("ap_person_out")
			logs := logged(t)
			f.personAt.Store(0)
			did, err := e.a.personOut(context.Background())
			if _, counted := f.personTries.Load(e.anaID); counted || strings.Contains(logs.String(), "profile update of member") {
				t.Errorf("a refused write counted as the update's failure (did %v, %v): log %q", did, err, logs.String())
			}
			if did || err == nil {
				t.Errorf("a refused write: did %v, %v; want no work and the write's error", did, err)
			}
		})
	}
}
