package main

// Small points from the last review of remote posts (FWR 5): the accept of a follow and the mark of posts kept for a
// mention change in one statement, and a Create of a post older than the time remote posts are kept keeps nothing.

import (
	"context"
	"testing"
	"time"
)

// The accept and the clearing of the mention-only mark are one statement: a failure of the second part rolls back the
// first, so the retry finds the follow still waiting and clears the mark. The test makes the mark's update fail once
// (a trigger in this test's own database).
func TestDBAcceptClearsMentionOnlyAtOnce(t *testing.T) {
	e := newNotesInEnv(t)
	e.w.put(danID, worldDoc{body: actorDoc(danID, "dan", "Dan", &e.other.PublicKey, nil)})
	dan, err := e.a.fed.remoteActor(context.Background(), danID)
	if err != nil {
		t.Fatal(err)
	}
	e.mustExec(`INSERT INTO posts (user_id, body, ap_id, ap_mention_only) VALUES ($1, 'd', $2, true)`, dan.UserID, danID+"/d/1")
	followID := "https://here.example/users/ana/activities/f2"
	e.mustExec(`INSERT INTO follows (follower_id, followee_id, state, ap_id) VALUES ($1, $2, 'pending', $3)`, e.anaID, dan.UserID, followID)
	e.mustExec(`CREATE SEQUENCE fail_once`)
	e.mustExec(`CREATE FUNCTION fail_once() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF nextval('fail_once') = 1 THEN RAISE EXCEPTION 'failing once'; END IF; RETURN NEW; END $$`)
	e.mustExec(`CREATE TRIGGER fail_once BEFORE UPDATE OF ap_mention_only ON posts FOR EACH ROW EXECUTE FUNCTION fail_once()`)
	if _, err := e.a.fed.followAnswered(context.Background(), dan, idOrObject{ID: followID}, true); err == nil {
		t.Fatal("the first Accept did not meet the failure")
	}
	if out, err := e.a.fed.followAnswered(context.Background(), dan, idOrObject{ID: followID}, true); err != nil || out != "" {
		t.Errorf("the Accept tried again: %q %v", out, err)
	}
	if e.state(e.anaID, dan.UserID) != "accepted" || e.count(`SELECT count(*) FROM posts WHERE user_id = $1 AND ap_mention_only`, dan.UserID) != 0 {
		t.Errorf("after the retry: follow %q, %d posts still kept for a mention", e.state(e.anaID, dan.UserID), e.count(`SELECT count(*) FROM posts WHERE user_id = $1 AND ap_mention_only`, dan.UserID))
	}
}

// A Create of a post published before the time remote posts are kept is dropped at intake, with its reason counted:
// nothing stored, nobody told, nothing of the host's hourly allowance spent.
func TestDBRemoteOldCreateDropped(t *testing.T) {
	e := newNotesInEnv(t)
	old := noteObj(1, "<p>hi @cyd</p>", map[string]any{"published": time.Now().Add(-remoteKeep - 24*time.Hour).UTC().Format(time.RFC3339),
		"tag": []any{map[string]any{"type": "Mention", "href": "https://here.example/users/cyd", "name": "@cyd"}}})
	if why := e.send(e.bo(), boID, "Create", old); why != inAccepted || e.outcome() != "done: dropped (published before the time posts are kept here)" {
		t.Errorf("an old Create: %v %s", why, e.outcome())
	}
	if n := e.count(`SELECT count(*) FROM posts WHERE ap_id IS NOT NULL`) + e.count(`SELECT count(*) FROM notifications WHERE user_id = $1`, e.cyID); n != 0 {
		t.Errorf("%d rows kept for an old Create", n)
	}
	if h := e.q.stats.numbers(time.Now()).Handled["Create dropped (published before the time posts are kept here)"]; h != 1 {
		t.Errorf("the reason is counted %d times", h)
	}
	for range remoteNotesPerHostHour - 1 {
		e.a.fed.notesIn.add("social.example")
	}
	if !e.a.fed.notesIn.add("social.example") {
		t.Error("the old Create spent the host's hourly allowance")
	}
}
