package main

import "testing"

// Migration 23 run again over tables an earlier draft of it made (point 6 of the fourth review of FWR 6): the columns
// the review added come back, and the statement runs whole. A database that recorded a draft as version 23 does not run
// it again by itself (migrate applies each version once); this is what running it by hand gives such a database.
func TestDBMigration23OverDraft(t *testing.T) {
	e := newEnv(t)
	e.mustExec(`ALTER TABLE ap_comment_out DROP COLUMN last_ap_id, DROP COLUMN sent_at`)
	e.mustExec(`ALTER TABLE ap_outbox DROP COLUMN comes_after, DROP COLUMN undoes_chain`)
	e.mustExec(`DROP TABLE ap_like_out`)
	e.mustExec(migrations[22])
	for _, c := range [][2]string{{"ap_comment_out", "last_ap_id"}, {"ap_comment_out", "sent_at"}, {"ap_outbox", "comes_after"}, {"ap_outbox", "undoes_chain"}, {"ap_like_out", "undo_id"}} {
		if e.count(`SELECT count(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`, c[0], c[1]) != 1 {
			t.Errorf("%s.%s is missing after migration 23 ran over an earlier draft", c[0], c[1])
		}
	}
}
