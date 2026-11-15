package main

import (
	"context"
	"database/sql"
	"sync"
	"testing"
)

// What a member's Like or Undo of a post comes after is read inside the enqueue's transaction, once it holds the queue's
// lock (point 7 of the fourth review of FWR 6): an Undo still being enqueued when the next click's enqueue begins is
// waited for, and the next one comes after it. Here the Undo is held once it has read and written, the Like starts and
// reaches the queue's lock, and then the Undo commits: the Like comes after that Undo.
func TestDBLastUndoReadUnderTheLock(t *testing.T) {
	e := newTalkEnv(t)
	q, a := e.out, e.a
	const post = 777
	to := []deliveryTarget{{Actor: boID, Inbox: boID + "/inbox"}}
	undoID, likeID := "https://here.example/users/ana/activities/undo-a", "https://here.example/users/ana/activities/like-b"
	held, release, likeAtLock := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var heldOnce, atLockOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	q.enqueueHook = func(stage, id string) {
		switch {
		case stage == "read" && id == undoID:
			heldOnce.Do(func() { close(held) })
			<-release
		case stage == "lock" && id == likeID:
			atLockOnce.Do(func() { close(likeAtLock) })
		}
	}
	undoDone := make(chan error, 1)
	go func() {
		_, err := q.enqueueOrdered(context.Background(), keyOwner(e.anaID), undoID, []byte(`{"type":"Undo"}`), to,
			outOrder{afterTx: a.lastUndo(context.Background(), e.anaID, post)}, func(tx *sql.Tx) error {
				_, err := tx.Exec(`INSERT INTO ap_like_out (user_id, post_id, undo_id) VALUES ($1, $2, $3)`, e.anaID, post, undoID)
				return err
			})
		undoDone <- err
	}()
	<-held
	likeDone := make(chan error, 1)
	go func() {
		_, err := q.enqueueOrdered(context.Background(), keyOwner(e.anaID), likeID, []byte(`{"type":"Like"}`), to,
			outOrder{afterTx: a.lastUndo(context.Background(), e.anaID, post)}, nil)
		likeDone <- err
	}()
	<-likeAtLock
	releaseOnce.Do(func() { close(release) })
	if err := <-undoDone; err != nil {
		t.Fatal(err)
	}
	if err := <-likeDone; err != nil {
		t.Fatal(err)
	}
	q.enqueueHook = nil
	var after sql.NullString
	if err := a.db.QueryRow(`SELECT comes_after FROM ap_outbox WHERE ap_id = $1`, likeID).Scan(&after); err != nil || after.String != undoID {
		t.Errorf("the Like comes after %q, not the Undo enqueued just before it (%v)", after.String, err)
	}
}
