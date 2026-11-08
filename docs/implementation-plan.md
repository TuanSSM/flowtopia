# Implementation plan for the five epics

How the epics in [epics.md](epics.md) are built: in what order, on which branches, with which
technical choices, and how each is checked. The checklists stay the source of truth for what is
done (`docs/epic-<code>/todo.md`); this page explains how.

## Delivery

- **One pull request per epic, one commit per milestone.** The checklists said one PR per
  milestone; with about 30 milestones that would mean 30 stacked PRs to review and merge in order.
  An epic is the unit worth reviewing, and each milestone stays a separate commit with its own
  message, so it can still be read or reverted on its own.
- **Stacked branches**, because every epic touches `main.go`, `web/social.js` and the README:
  `epic/rpl` from `main`, `epic/mod` from `epic/rpl`, then `epic/lnk`, `epic/geo`, `epic/avt`. Each PR
  targets the branch below it; when one merges, the next is rebased onto `main` and retargeted.
- **CI on every branch.** `process_pr` only runs for PRs into `main`, so stacked branches are run
  through the same workflow with `workflow_dispatch` before their PR is opened, and again when they
  are retargeted.
- **Local checks first**, as `/pr-checks` says: gofmt, go vet, go test, syntax, catalogs, the browser
  suites on a throwaway database, and the physics suite when `sim/` or clips change.

## Testing that needs a database

Go tests so far are pure functions. Replies, moderation and link previews need handler tests against
Postgres. A new `db_test.go` runs only when `TEST_DATABASE_URL` is set: each test gets its own
Postgres schema (`search_path`), builds the tables, and talks to the real handlers through
`httptest`. Without the variable these tests are skipped and counted. CI's integration job, which
already has a Postgres service, runs them; the unit job reports them as skipped. To make the
handlers reachable from tests, the routes move from `main()` into `a.routes()`.

## RPL: replies and threaded comments

- `comments` gains `parent_id`, `deleted` and `edited_at`; `notifications` gains `comment_id`;
  `comment_likes` is new. All added with `IF NOT EXISTS`, so the live database migrates on start.
- One level of threading. A reply to a reply is stored under the top-level comment, and the person
  answered is @mentioned at the start unless the text already does it or they are answering
  themselves. Replies to a deleted comment, or across posts, are refused.
- Who is told: the person answered (`reply`), the post owner (`comment`) unless they are the one
  answered or the replier, and anyone @mentioned who is not already told. Edits only notify people
  newly mentioned.
- Deleting a comment with replies keeps it as "Comment deleted"; when its last reply goes, the
  placeholder goes too.
- The feed carries the two newest top-level comments of each post, each with its two newest
  replies, and the counts. The rest load on demand, 20 top-level comments or 50 replies at a time.
- A notification opens `#/post/<post>/<comment>`, which shows that comment's whole thread and
  scrolls to it.

## MOD: moderators, admin panels, skill map editor

- Permissions in one Go table, `can(u, perm)`, used by every handler. `GET /api/me` returns the
  user's permissions so the UI shows only what the server allows.
- Reports, the queue, hide and restore (soft, visible to the author and moderators), mute and
  suspend with an end time, revoking badges and resetting stats, and an audit log written by one
  helper so no action can skip it.
- Hidden content is filtered in SQL in every query that returns posts or comments, with a test per
  query, because one missed query would leak hidden content.
- Catalogs: nunchaku moves to `web/catalog/nunchaku.json` (a script proves the ids and fields are
  unchanged); one validator in Go; revisions in Postgres seeded from the embedded files; catalogs
  served from the latest published revision with the embedded file as fallback.
- Ids are never reused. Renames keep the id; changed ids become aliases that progress is read
  through; retired skills are hidden but keep their progress.

## LNK: link cards

- A parser for Instagram, TikTok and YouTube URLs with a table of real-shaped cases.
- A fetcher with an allowlist, a 3 second timeout, a 256 KB cap, redirects only within the
  allowlist, and no requests to private addresses. Tests point it at an `httptest` server through a
  test-only override of the endpoint table; the browser tests run with fetching disabled and seed
  previews directly.
- Thumbnails are shrunk and re-encoded by the server (standard library `image` packages) and served
  from Flowa, so visitors never contact the three sites. YouTube can play in place behind
  `videoEmbeds`, which adds one `frame-src` entry only while the flag is on.

## GEO: next-trick suggestions

- Curves and states are measured at build time in `sim/geometry.js` and written to
  `web/clips/features.json` by `node sim/bake.js`; CI checks the file is the one the sources build.
- Known-answer tests (circle, helix, figure-8, mirrored clips) come before any real clip is measured.
- Catalog `motion` fields describe skills without clips. Values come from each skill's own
  description and the clips; anything the description does not settle is `unknown`.
- Transition labels are counted anonymously from users who opt in, as increments and decrements, so
  no user id is stored and withdrawing consent removes a user's marks.
- **The gate, stated honestly.** With fewer than 30 labelled pairs there is nothing to fit or
  measure against. Until then the score uses fixed weights derived from the physics, the `suggest`
  flag stays off by default, and every suggestion is labelled as not yet checked against real combos.
  Once 30 pairs exist, the fitting script must reach an AUC of 0.75 and beat the prerequisite-only
  baseline, or CI fails.

## AVT: avatars

- Parts are built in code inside the figure bundle: hair, hats, glasses, eyes, brows, mouths and
  wristbands are small meshes attached to one bone each (head or forearm), so they follow the pose at
  no skinning cost. Outfits colour the existing body mesh per vertex by the bone that moves it
  (torso and arms for tops, hips and legs for bottoms, feet for shoes), with sleeve and leg lengths
  as styles. No new model files, no textures.
- Body presets scale the figure. A clip is moved as a whole so the hands keep their place relative to
  the shoulders, which leaves the physics exactly as simulated (uniform gravity does not care where
  the motion happens), and then every clip is checked against each preset's body with the same
  overlap and reach checks. A clip that fails on a preset plays on the standard body, and says so.
- Stickers render the avatar in poses built from the rig, checked like any pose, to transparent PNG.

## Order and checkpoints

1. RPL, then run every suite and open its PR.
2. MOD in four parts as separate commits: roles and permissions; reports and moderation; dashboard;
   catalogs and the editor.
3. LNK, then GEO, then AVT, each with its PR once its suites pass.

Each PR description lists what was verified and anything left for later, with the reason.
