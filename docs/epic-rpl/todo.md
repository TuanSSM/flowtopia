# RPL: Replies and threaded comments

Why, scope and risks: [epics.md](../epics.md#rpl-replies-and-threaded-comments). Order:
[roadmap.md](../roadmap.md). Each milestone is one pull request. An item is checked only when its
DoD holds and the [roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item)
is met.

**Status:** not started. 0 of 15 items done.

## Already in place

- [x] Flat comments on posts, with delete by the author, the post owner or an admin.
- [x] Comment notifications to the post owner, and @mentions in comments.
- [x] `community` flag gating the timeline.

## M1. Replies on the server

- [ ] **Schema.** `comments` gains `parent_id BIGINT NULL REFERENCES comments(id)` and
  `deleted BOOLEAN NOT NULL DEFAULT false`, added with `ALTER TABLE ... IF NOT EXISTS`.
  DoD: a fresh database and the live one's schema both start; a `go test` builds the schema twice
  without error.
- [ ] **Create a reply.** `POST /api/posts/{id}/comments` accepts `parentId`. A reply to a reply is
  stored under the top-level comment (one level of threading), with the answered person's
  @mention added to the start of the text if it is missing.
  DoD: Go test covers parent on another post (refused 400), parent missing (404), reply to a reply
  (stored under the root, mention prefixed once).
- [ ] **Reply notifications.** The author of the answered comment gets a `reply` notification; the
  post owner still gets `comment`; nobody is told twice for one reply.
  DoD: `social.py` checks the three-person case (post owner, commenter, replier) gives exactly one
  notification each to owner and commenter.
- [ ] **Delete keeps the thread.** Deleting a comment that has replies sets `deleted` and blanks the
  text; one without replies is removed. Deleting a post still removes everything.
  DoD: Go test and `social.py` check both cases; the API returns `deleted: true` with empty body.

## M2. Comments loaded per post

- [ ] **Endpoint.** `GET /api/posts/{id}/comments?after=` returns top-level comments oldest first,
  20 at a time, each with its 2 newest replies and a reply count.
  DoD: Go test with 45 comments pages correctly; a comment's replies come back with
  `GET /api/comments/{id}/replies?after=`.
- [ ] **Feed stays light.** The feed returns the 2 newest top-level comments per post and a count,
  not every comment. The field name stays `comments`, so older clients still render.
  DoD: feed JSON for a post with 200 comments is under 10 KB; `social.py` still passes unchanged
  checks for posts with fewer than 3 comments.

## M3. Replies in the UI

- [ ] **Reply action.** Each comment has Reply, which opens the input under that thread prefilled
  with `@name ` and focused.
  DoD: `social.py` replies from the UI and finds the reply under the right comment.
- [ ] **Folding.** Threads show the 2 newest replies; "View N more replies" loads the rest; a post
  shows "View all N comments" past the first 2.
  DoD: `social.py` with 5 replies sees 2, clicks, sees 5.
- [ ] **Deleted placeholder.** A deleted comment with replies shows "Comment deleted" in grey with
  its replies still under it.
  DoD: `social.py` deletes a parent and checks the placeholder and replies.
- [ ] **Bell text.** Reply notifications read "<name> replied to your comment" and open the post
  scrolled to the reply.
  DoD: `social.py` clicks the notification and the reply element is in view.

## M4. Likes on comments

- [ ] **Server.** `comment_likes` table; `POST /api/comments/{id}/like` toggles in one statement,
  as post likes do. Comment authors are told once per comment.
  DoD: Go test for the toggle and the single notification.
- [ ] **UI.** A small heart and count on each comment and reply, optimistic and corrected from the
  response.
  DoD: `social.py` likes a reply and the count persists after reload.

## M5. Finish

- [ ] **Edit window.** The author can edit a comment for 15 minutes; it shows "edited".
  DoD: Go test refuses an edit at 16 minutes; `social.py` edits within the window.
- [ ] **Phone layout.** Threads indent by 16 px and keep the input reachable at 420 px.
  DoD: `pages.py` adds a "Post with replies, phone" screenshot; reviewed in the PR.
- [ ] **Docs.** README's community section and Community API list replies, comment likes and the
  new endpoints; this file shows every item checked.
  DoD: `test/docs.sh` passes; the PR links this file.
