# PRT: Portability

Why, scope and risks: [epics.md](../epics.md#prt-portability). Design:
[federation.md](../federation.md). Order: [roadmap.md](../roadmap.md#third-cycle).
Category: feature. Size: S. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** later (issue [#92](https://github.com/TuanSSM/flowtopia/issues/92)); waits for FWR. 0 of 4 items done. Every setting, table, route and test named below
is new.

- [ ] **Export.** A member downloads their profile, posts (with pictures), comments, follows and
  blocks as one archive, in every mode (local included), at most once an hour. Progress documents
  keep their own export on the page. DoD: `TestDBExport` (the archive holds exactly the member's rows
  and nothing of anyone else's).
- [ ] **Move out.** The member sets `alsoKnownAs` on the new account; on Flowa, "I moved" sets
  `movedTo` and sends Move to followers after checking that the target lists this account. DoD:
  `TestDBMoveOut`; the `federation` suite moves a member between its two instances and the followers
  follow the new account.
- [ ] **Move in.** A Move from a followed remote account whose target lists it in `alsoKnownAs`
  makes local followers follow the target and unfollow the old account; anything else is ignored.
  DoD: `TestDBMoveIn` with a recorded Mastodon Move fixture.
- [ ] **Import a following list.** A CSV in the form Mastodon exports (one account address per
  line), at most 1,000 rows, followed through the queue at the delivery rate. DoD:
  `TestDBImportFollows` (bad lines reported, duplicates skipped, the limit held).
