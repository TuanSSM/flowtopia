# PRT: Portability

Why, scope and risks: [epics.md](../epics.md#prt-portability). Design:
[federation.md](../federation.md). Order: [roadmap.md](../roadmap.md#third-cycle).
Category: feature. Size: S. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#92](https://github.com/TuanSSM/flowtopia/issues/92)). 0 of 4 items done: Export is built and waits
for its pull request; Move out, Move in and the import wait for FWR. Every setting, table, route and test named below
is new.

- [ ] **Export.** A member downloads their profile, posts (with pictures), comments, follows and
  blocks as one archive, in every mode (local included), at most once an hour. Progress documents
  keep their own export on the page. DoD: `TestDBExport` (the archive holds exactly the member's rows
  and nothing of anyone else's).
  - Built: `GET /api/export` behind the `export` flag (off by default, as `follows`), and Your data in
    Settings with Download my data and the sentence that practice progress keeps its own Export JSON on the
    same page. One zip made while it is sent: `README.txt`, `profile.json`, the profile picture, `posts.json`
    and `pictures/`, `comments.json`, `following.json`, `followers.json`, `blocks.json`, and `manifest.json`
    last (format version 1, counts, the files). Once an hour per member through `limits`; a failed or
    cut-off download is given back, and at most 6 may be begun in an hour; at most 2 are made at once across
    the server, the next told to try again in a minute (503). 512 MB and 30 minutes at most,
    a page of 500 rows or one picture held at a time, a connection held only while a page is read. Checks:
    `TestDBExport` (the DoD, with markers on every row of anyone else, the secrets and the pictures'
    metadata), `TestDBExportLimits`, `TestDBExportOnlyOwn`, `TestDBExportCaps`,
    `TestDBExportManyPictures` (a 51 MB archive within a 24 MB heap budget), `TestDBExportClientGone`,
    `TestDBExportOneConnection`, `TestDBExportModes`, `TestDBExportAtOnce`, `TestDBExportPictureGone`,
    `TestDBExportEstimateBound`, `TestExportSizeReadsNoPicture`, the export's plans in `TestDBQueryPlans`;
    the `social`, `keys`, `a11y` and `contrast` suites;
    the `settings-data` screenshots in `pages.py`. The box stays open until the pull request merges with
    `process_pr_result` passed (the roadmap's definition of done).
  - Open: a suspended member cannot sign in, so cannot download their data until the suspension ends;
    there is no export by an admin of a member's data for a legal request (not designed, so not built);
    the archive is a plain documented shape, and an ActivityStreams form (`actor.json`, `outbox.json`, as
    Mastodon's archive) can come with the Move items.
  - For the owner: likes and bookmarks are left out (the item names profile, posts, comments, follows and
    blocks); a hidden post or comment is included with the reason the member is already shown; the flag is
    off by default; the numbers (one an hour, 6 begun an hour, 60 asks an hour, 2 at once, 512 MB, 30 minutes,
    8 KB/s after a grace of 1 minute, a stall of 1 minute at most per write) are constants in `main.go`. With
    those, two members reading at 8 KB/s can still hold both places for up to 30 minutes each (the time cap), six
    times an hour. A reader just under the rate keeps its place for some minutes, because what is counted
    includes what waits in the connection's buffers; one that stops reading lets it go once a write has waited
    the 1 minute stall. The size bound counts text at six times its bytes, so for
    text in scripts that take 3 or 4 bytes a character it refuses at roughly a fifth of 512 MB of real archive,
    which takes tens of thousands of long comments.
- [ ] **Move out.** The member sets `alsoKnownAs` on the new account; on Flowa, "I moved" sets
  `movedTo` and sends Move to followers after checking that the target lists this account (Mastodon
  checks the same before it accepts a Move: its ActivityPub page, "Account migration"). DoD:
  `TestDBMoveOut`; the `federation` suite moves a member between its two instances and the followers
  follow the new account.
- [ ] **Move in.** A Move from a followed remote account whose target lists it in `alsoKnownAs`
  makes local followers follow the target and unfollow the old account; anything else is ignored.
  DoD: `TestDBMoveIn` with a recorded Mastodon Move fixture.
- [ ] **Import a following list.** A CSV in the form Mastodon exports (one account address per
  line; Mastodon's documentation says only "CSV", so the item reads a real export first and
  corrects this), at most 1,000 rows, followed through the queue at the delivery rate. DoD:
  `TestDBImportFollows` (bad lines reported, duplicates skipped, the limit held).
