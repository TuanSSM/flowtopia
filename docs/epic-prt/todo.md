# PRT: Portability

Why, scope and risks: [epics.md](../epics.md#prt-portability). Design:
[federation.md](../federation.md). Order: [roadmap.md](../roadmap.md#third-cycle).
Category: feature. Size: S. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#92](https://github.com/TuanSSM/flowtopia/issues/92)). 1 of 4 items done: Export; Move out
is built and waits for its pull request; Move in and the import follow, each in its own pull request. Every setting,
table, route and test named below is new.

- [x] **Export.** A member downloads their profile, posts (with pictures), comments, follows and
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
    the `settings-data` screenshots in `pages.py`.
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
  - Built: `POST /api/me/move` (the new account's handle or address and the member's password) and `DELETE
    /api/me/move` (taking the redirect back, with the password), `PUT` and `DELETE /api/me/aliases` (the accounts a
    member moves here from, at most 5, named in the actor's `alsoKnownAs`), behind the `moves` flag (off by default, a
    flag of a mode that federates only), in "Beyond this server" in Settings; migration 25 (`users.moved_to`,
    `moved_at`, `also_known_as`, `ap_actors.also_known_as`) with `flowa-schema24.dump`. The target is fetched again
    whatever the cache holds, through the fetcher and the domain policy, and the move goes on only when its
    `alsoKnownAs` names this member's actor; then one transaction writes `movedTo` and an Update(Person) and a Move to
    the followers on other servers, the Move held behind the Update at each inbox (`comes_after`), every rule of the
    queue applying to both. Members here who follow the member and share their profile follow the new account and stop
    following the old one. After it the profile is a redirect: the actor, the public page, the visitor page of a
    single-profile instance and the profile in the app say where it went; it takes no new follower (here, or from
    another server: Reject), makes no Public post and follows nobody new; another move waits 30 days. Checks:
    `TestDBMoveOut` (the DoD), `TestDBMoveOutAliases`, `TestDBMoveOutOrder`, `TestDBMoveOutPolicy`,
    `TestDBMoveOutMidway`, `TestDBMoveOutAtOnce`, `TestDBMoveOutTarget`, `TestDBMoveOutFollowIn`,
    `TestDBMoveOutFlagAndModes`, the moves' plans in `TestDBQueryPlans`; the `federation` suite (dee of one moves to gil
    of two, refused until gil names her; her actor names `movedTo`; eli, her follower on one, follows gil on two; two
    takes the Move without an error); the `federated` browser suite (the card, its names, contrast and keyboard path,
    the refusals' words; picture `federated-move`). The followers who follow the new account in the suite are the
    member's followers on her own server, which that server moves itself; followers on the other server follow it once
    Move in (the next item) is built there. The box stays open until the pull request merges with `process_pr_result`
    passed.
  - Open: a Move received is still counted and dropped (Move in); a member here who could not follow the new account
    (they do not share their profile) is not told by a notification, only by the profile; the owner's trial should show
    how Mastodon takes this Move (its pages do not say whether it wants the Update first or reads `movedTo`: federation.md
    lists what is unverified).
  - For the owner: the 30 days count from the last move, also when the redirect was taken back (Mastodon's 30 days);
    taking the redirect back is allowed at any time, keeps the followers who moved where they are, and sends an Update;
    a second move after 30 days is allowed while still redirecting, and replaces the target; a moved member may still
    post to this server, comment, like, practise, export and delete the account (Mastodon disables posting on the old
    account; here only Public posts are refused, since posts to this server never claim to be the new account), and
    their likes and comments on posts of other servers still go out; moving to an account on this same server is
    refused (not built); a server under "limit" may be moved to; aliases ask no password (Mastodon calls them harmless);
    the numbers are constants in `main.go`: 5 moves or undoings an hour per member, 30 alias changes an hour, 5 aliases,
    16 entries of a remote actor's `alsoKnownAs` kept (a target that lists more than 16 accounts before this one is
    refused), 2,048 bytes an entry, 10,000 members here moved at once.
- [ ] **Move in.** A Move from a followed remote account whose target lists it in `alsoKnownAs`
  makes local followers follow the target and unfollow the old account; anything else is ignored.
  DoD: `TestDBMoveIn` with a recorded Mastodon Move fixture.
- [ ] **Import a following list.** A CSV in the form Mastodon exports (one account address per
  line; Mastodon's documentation says only "CSV", so the item reads a real export first and
  corrects this), at most 1,000 rows, followed through the queue at the delivery rate. DoD:
  `TestDBImportFollows` (bad lines reported, duplicates skipped, the limit held).
