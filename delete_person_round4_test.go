package main

import (
	"fmt"
	"os"
	"regexp"
	"slices"
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

// outStateChange is a statement's SET part that changes a record's state, as outStateChanges finds it.
type outStateChange struct {
	set    string // the SET part, its words joined by one space
	resets bool   // it resets the tries and the wait
}

var (
	outStringRE    = regexp.MustCompile("(?s)`[^`]*`" + `|"(?:[^"\\\n]|\\.)*"`)
	outUpdateRE    = regexp.MustCompile(`(?i)\bUPDATE\s+ap_(post|comment)_out\b`)
	outWhereRE     = regexp.MustCompile(`(?i)\bWHERE\b`)
	outSetsStateRE = regexp.MustCompile(`(?i)\bstate\s*=`)
	outTriesRE     = regexp.MustCompile(`(?i)\btries\s*=\s*0\b|(?i)\bTHEN\s+0\s+ELSE\s+tries\b`)
	outDueRE       = regexp.MustCompile(`(?i)\bdue_at\s*=\s*NULL\b|(?i)\bTHEN\s+NULL\s+ELSE\s+due_at\b`)
)

// outStateChanges reads Go source for the statements, in back-quoted or double-quoted strings and in any letter case,
// that change the state of a record of ap_post_out or ap_comment_out.
func outStateChanges(src string) []outStateChange {
	var out []outStateChange
	for _, q := range outStringRE.FindAllString(src, -1) {
		locs := outUpdateRE.FindAllStringIndex(q, -1)
		for i, l := range locs {
			set := q[l[1]:]
			if i+1 < len(locs) {
				set = q[l[1]:locs[i+1][0]]
			}
			if w := outWhereRE.FindStringIndex(set); w != nil {
				set = set[:w[0]]
			}
			if !outSetsStateRE.MatchString(set) {
				continue
			}
			out = append(out, outStateChange{set: strings.Join(strings.Fields(q[l[0]:l[1]]+set), " "), resets: outTriesRE.MatchString(set) && outDueRE.MatchString(set)})
		}
	}
	return out
}

// Every statement that changes the state of a record of ap_post_out or ap_comment_out resets its tries and its wait, or
// says why it does not (2 of the fourth review): read from main.go, so a new one cannot forget it. The list the code
// keeps beside outNotTried names them. The reading takes lower case, no spaces around =, double-quoted strings, and
// does not take retries = 0 for tries = 0 (the last review); it still finds every statement the first reading found.
func TestOutStateChangesReset(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	found := outStateChanges(string(src))
	for _, c := range found {
		if !c.resets && outStateResetReasons[c.set] == "" {
			t.Errorf("a change of a record's state keeps its tries and wait, and gives no reason: %s", c.set)
		}
	}
	if len(found) < 10 {
		t.Errorf("only %d changes of a record's state found in main.go: the test no longer reads them", len(found))
	}
	// The first reading (back quotes, capitals, "state = " with its spaces): each statement it found is found now.
	setsState := regexp.MustCompile(`(SET|,)\s+state = `)
	first := 0
	for _, q := range regexp.MustCompile("(?s)`[^`]*`").FindAllString(string(src), -1) {
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
			first++
			want := strings.Join(strings.Fields("UPDATE ap_"+set), " ")
			if !slices.ContainsFunc(found, func(c outStateChange) bool { return c.set == want }) {
				t.Errorf("the first reading found a statement this one does not: %s", want)
			}
		}
	}
	t.Logf("the first reading found %d statements, this one %d", first, len(found))
	if first == 0 || len(found) < first {
		t.Errorf("the first reading found %d statements, this one %d", first, len(found))
	}
	// What the first reading missed or let through, in a source written for the test.
	for _, c := range []struct {
		src    string
		n      int
		resets bool
	}{
		{"x := `update ap_post_out set state='sent', tries = 0, due_at = null where post_id = $1`", 1, true},
		{"x := `UPDATE ap_comment_out SET state='delete' WHERE comment_id = $1`", 1, false},
		{`x := "UPDATE ap_post_out SET state = 'sent', tries = 0, due_at = NULL WHERE post_id = $1"`, 1, true},
		{"x := `UPDATE ap_post_out SET state = 'sent', retries = 0, due_at = NULL WHERE post_id = $1`", 1, false},
		{"x := `UPDATE ap_post_out SET tries = 0 WHERE post_id = $1 AND state = 'sent'`", 0, false},
		{"x := `UPDATE ap_post_out SET last_state = 'x' WHERE post_id = $1`", 0, false},
	} {
		got := outStateChanges(c.src)
		if len(got) != c.n || c.n == 1 && got[0].resets != c.resets {
			t.Errorf("%s: found %+v, want %d statements resetting %v", c.src, got, c.n, c.resets)
		}
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
