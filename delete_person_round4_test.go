package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A comment's Update dropped because the comment is no longer shown, and an edit that turns a sent record into an
// Update, start the record's tries afresh (2 of the fourth review): an Update that failed six times and was dropped
// does not leave the next edit's Update four tries from its end.
func TestDBCommentTriesAfresh(t *testing.T) {
	e := newNotesEnv(t)
	e.setFlag("community", true)
	bo, _ := e.remote("bo", "h1.fan.example", true)
	e.follows(bo)
	p := e.postAs(e.ana, map[string]any{"body": "x", "audience": "public"})
	out := e.must(200, e.ana, "POST", fmt.Sprintf("/api/posts/%d/comments", p), map[string]any{"body": "a comment"})
	cid := num(out["comment"].(map[string]any)["id"])
	e.produced()
	record := func() (state string, tries int, due bool) {
		var d *time.Time
		if err := e.a.db.QueryRow(`SELECT state, tries, due_at FROM ap_comment_out WHERE comment_id = $1`, cid).Scan(&state, &tries, &d); err != nil {
			t.Fatal(err)
		}
		return state, tries, d != nil
	}
	if st, _, _ := record(); st != "sent" {
		t.Fatalf("the setup: the comment's record is %s", st)
	}
	e.must(200, e.ana, "PUT", fmt.Sprintf("/api/comments/%d", cid), map[string]any{"body": "edited once"})
	e.mustExec(`UPDATE ap_comment_out SET tries = 6, due_at = now() - interval '1 minute' WHERE comment_id = $1`, cid) // as after six failures
	e.must(200, e.ana, "PUT", "/api/me", map[string]any{"federates": false})
	e.a.fed.commentsDue.Store(true)
	e.produce()
	if st, tries, due := record(); st != "sent" || tries != 0 || due {
		t.Errorf("an Update dropped for a comment no longer shown: %s with %d tries, due %v", st, tries, due)
	}
	// The edit's own transition, from a record left sent with a count (as an older build could leave it).
	e.mustExec(`UPDATE ap_comment_out SET tries = 6, due_at = now() WHERE comment_id = $1`, cid)
	e.must(200, e.ana, "PUT", fmt.Sprintf("/api/comments/%d", cid), map[string]any{"body": "edited again"})
	if st, tries, due := record(); st != "update" || tries != 0 || due {
		t.Errorf("an edit of a sent comment: %s with %d tries, due %v", st, tries, due)
	}
}

// outStateResetReasons are the statements that change a record's state and keep its tries, each with why (none today).
var outStateResetReasons = map[string]string{}

// Every statement that changes the state of a record of ap_post_out or ap_comment_out resets its tries and its wait, or
// says why it does not (2 of the fourth review): read from main.go, so a new one cannot forget it. The list the code
// keeps beside outNotTried names them.
func TestOutStateChangesReset(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	setsState := regexp.MustCompile(`(SET|,)\s+state = `)
	seen := 0
	for _, q := range regexp.MustCompile("(?s)`[^`]*`").FindAllString(string(src), -1) {
		if !strings.Contains(q, "UPDATE ap_post_out") && !strings.Contains(q, "UPDATE ap_comment_out") {
			continue
		}
		for _, part := range strings.Split(q, "UPDATE ap_")[1:] {
			if !strings.HasPrefix(part, "post_out") && !strings.HasPrefix(part, "comment_out") {
				continue
			}
			set := part
			if i := strings.Index(set, "WHERE"); i >= 0 {
				set = set[:i]
			}
			if !setsState.MatchString(set) {
				continue
			}
			seen++
			resets := (strings.Contains(set, "tries = 0") || strings.Contains(set, "THEN 0 ELSE tries")) &&
				(strings.Contains(set, "due_at = NULL") || strings.Contains(set, "THEN NULL ELSE due_at"))
			if !resets && outStateResetReasons[strings.Join(strings.Fields(set), " ")] == "" {
				t.Errorf("a change of a record's state keeps its tries and wait, and gives no reason: UPDATE ap_%s", strings.Join(strings.Fields(set), " "))
			}
		}
	}
	if seen < 10 {
		t.Errorf("only %d changes of a record's state found in main.go: the test no longer reads them", seen)
	}
}

// The sentence of sharing again names the time while the clock is believed and it is ahead, neither the time nor the
// clock once it is past, and the clock, with no time, while the clock is doubted (4 of the fourth review).
func TestDBLeavingSentences(t *testing.T) {
	e := newNotesEnv(t)
	f := e.a.fed
	now := e.clock.now()
	if s := f.leavingSentenceAt(now.Add(time.Hour)); !strings.Contains(s, "UTC") || strings.Contains(s, "clock") {
		t.Errorf("ahead, the clock believed: %q", s)
	}
	if s := f.leavingSentenceAt(now.Add(-time.Hour)); strings.Contains(s, "UTC") || strings.Contains(s, "clock") {
		t.Errorf("past, the clock believed: %q", s)
	}
	e.out.doubt()
	if !e.out.doubting() {
		t.Fatal("the setup: the clock is not doubted")
	}
	for _, at := range []time.Time{now.Add(time.Hour), now.Add(-time.Hour)} {
		if s := f.leavingSentenceAt(at); strings.Contains(s, "UTC") || !strings.Contains(s, "clock") {
			t.Errorf("the clock doubted: %q", s)
		}
	}
}
