package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The statements local mode runs on a database that never federated (plainLocal, plainStatements), pinned as text (small
// point 6 of the third review of FWR 6), so they cannot drift without a test saying so. The files in testdata/plain-local
// were written by this test from plainStatements and checked against the same statements of origin/main before FWR 6
// (5318e09: feedSQL, commentsSQL of feedCommentsSQL and of repliesSQL, and the two pages of comments as handleComments
// and handleReplies wrote them): they are those statements word for word, but for the two columns commentCols has
// added since (c.ap_id IS NOT NULL, c.ap_extra), which read only the row the statement holds. A change meant to touch
// local mode writes them again with FLOWA_UPDATE_GOLDEN=1 and says why in its pull request.
func TestPlainStatementsPinned(t *testing.T) {
	for name, q := range map[string]string{
		"feed.sql":         plainStatements(feedSQL),
		"preview.sql":      plainStatements(commentsSQL(feedCommentsSQL)),
		"replies.sql":      plainStatements(commentsSQL(repliesSQL)),
		"comment-page.sql": plainStatements(commentsSQL(commentPageSQL)),
		"reply-page.sql":   plainStatements(commentsSQL(replyPageSQL)),
	} {
		path := filepath.Join("testdata", "plain-local", name)
		if os.Getenv("FLOWA_UPDATE_GOLDEN") != "" {
			if err := os.WriteFile(path, []byte(q+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(want) != q+"\n" {
			t.Errorf("local mode's statement %s is not the one pinned in %s:\n%s", name, path, q)
		}
	}
}
