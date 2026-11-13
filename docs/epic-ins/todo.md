# INS: Instance modes and settings

Why, scope and risks: [epics.md](../epics.md#ins-instance-modes-and-settings). Design:
[federation.md](../federation.md). Order: [roadmap.md](../roadmap.md#third-cycle).
Category: feature. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#88](https://github.com/TuanSSM/flowtopia/issues/88)). 6 of 7 items done; the design waits for the owner's review. Every setting, table, route and
test named below is new unless the design note says it exists.

- [ ] **Design.** [federation.md](../federation.md): the modes, identity, protocol, security, queues,
  data model, administration, budgets, rollout and the sixteen decisions F1 to F16. DoD: reviewed by
  the owner, the decisions answered or the recommendations accepted, linked from the README.
  Built: the note, linked from the README's "What comes next". Open: the owner's review.
- [x] **A mode setting that changes nothing by default.** `FLOWA_MODE`: unset, empty or `local` is
  today; `federated` and `single` are known words that stop the start with "not built yet" until
  their items ship; anything else stops it with a message naming the three. Capitals and spaces
  around the word are forgiven. Every setting is read before anything is written. The mode on the
  dashboard, and one `FLOWA_MODE: ${FLOWA_MODE:-}` line in `compose.yml`. No table and no federation
  route yet. DoD: `TestInstanceMode` for the parsing; `TestDBLocalModeUnchanged` compares the routes
  with `testdata/routes-local.txt` (generated from `main` at 72b1b0c, before the change), fails on
  any HTTP connection the start, the housekeeping or the first requests open to another machine
  (through the fetcher or around it; Postgres is not one), holds that every address federation will
  use answers as an unknown one, that the dashboard says the mode, and that a refused start writes
  nothing; a check in `test/browser/mod.py`.
- [x] **Member blocks.** `blocks`, the first migration of this plan: `flowa-schema7.dump` goes into
  `testdata/backups` first (`test/restore.sh --fixture` on the commit before), unless another epic
  added it. A block and an unblock endpoint, by username, with an allowance in `limits`.
  A block hides both ways: neither member sees the other's posts, comments or replies, and neither
  can comment on, reply to or like what the other writes. A mention across a block is stored and
  shown to others, and the member mentioned is not told. Staff see past a member's block, and
  reports work as before. Behind the `community` flag. DoD: `TestDBBlocks`; `TestDBOneConnection`
  (no write endpoint needs a second database connection while it holds one); `TestDBQueryPlans`
  holds the timeline with 5,000 blocks; a check in `test/browser/social.py`.
- [x] **Follows and a Home timeline behind a flag.** `follows` (local accounts only at this step);
  Follow and Unfollow on profiles; Home beside Community; the `follows` flag, off by default; counts
  on the profile, lists shown only to the account itself (F10). DoD: `TestDBFollows`;
  `TestDBQueryPlans` holds Home with 500 followed accounts; `TestDBFlagsGateAPI` covers the flag; a
  browser check and a screenshot of Home in `test/browser/pages.py`. Built: migration 9 (no `state`
  or `ap_id` until FWR 3 reads them), `PUT|DELETE /api/users/{name}/follow`, `GET /api/home`,
  `GET /api/following` and `/api/followers` for the account itself; a block removes the follows both
  ways and a follow across one is refused with the same 403 sentence either way; at most 1,000
  follows per member, which bounds a page of Home; `TestDBFollows`, `TestDBFollowsAtOnce`, the plan
  cases, the flag in `TestDBFlagsGateAPI`, the checks in `social.py` and the pictures in `pages.py`;
  `flowa-schema8.dump`, the backup of the schema before this migration, made from `main` after
  member blocks merged.
- [x] **Instance information and rules.** The `instance_settings` table lands here, with its first
  reader. Description, contact and rules, edited in Admin (permission `flags.edit`), audited, shown
  at sign-up and on an about page. The about page is in the app for signed-in members in every mode;
  a signed-out visitor can open it only once an admin has saved a description, so an instance that
  sets nothing answers nothing new. DoD: `TestDBInstanceInfo` (with nothing saved the visitor's
  about answers 404 and `TestDBLocalModeUnchanged` still passes); a browser check that sign-up shows
  the rules; a screenshot of the about page.
  Built: migration 10 (`instance_settings`, three keys: description, contact, rules);
  `GET|PUT /api/admin/instance` (the whole document against the version read, 409 when stale, audited
  with the parts that changed), `GET /api/instance` for members and `GET /api/about` for visitors (cached,
  limited by address, 404 as an unknown address until a description is saved); the limits (500
  characters of description, a contact of 200 that is an email address, an http or https link or text,
  20 rules of 200 characters with explanations of 1,000, all plain text); Admin > Instance; the rules on
  the sign-up form and the about page; `TestDBInstanceInfo`, `TestInstanceText`, the about address and
  the empty reads in `TestDBLocalModeUnchanged`, the save in `TestDBOneConnection`; checks in `mod.py`,
  stops in `a11y.py` and `contrast.py`, and the about pictures in `pages.py`.
- [x] **Sign-up by approval.** The `registration` setting: open or approval, while the `signups` flag
  stays the one switch that closes sign-up; a person waiting for approval cannot sign in and is listed in
  Accounts with Approve and Decline, both audited. DoD: `TestDBRegistrationApproval`;
  `TestDBSignupLimits` unchanged.
  Built: `registration` in `instance_settings` with two values, `open` (what nothing saved means) and
  `approval`, while closed stays the `signups` flag alone (the truth table is in the README); edited in
  Admin > Instance through the same document, version check and audit row. Migration 11 adds
  `join_requests`, a table of its own rather than a column on `users`, so a waiting person is no account
  in any list, count, profile, mention, follow, block or session; the name is taken while it waits. An
  optional reason of 500 characters, plain text, read only by admins. `GET /api/admin/requests`,
  `POST /api/admin/requests/{id}/approve` and `DELETE /api/admin/requests/{id}` (user.manage, 400 an hour
  per admin, audited, 404 when already answered); Decline deletes the request and frees the name. At most
  200 wait (503 past that), every request counts against the sign-up allowance before its password is
  hashed (a refused one costs no hash), each insert checks the other table for the name, housekeeping
  removes requests older than 30 days, `seedAdmin` makes the operator's account whatever the setting says. `TestDBRegistrationApproval`, the new routes in
  `TestDBLocalModeUnchanged` and `TestDBOneConnection`; checks in `mod.py`, stops in `a11y.py` and
  `contrast.py`, and the sign-up form and Accounts pictures in `pages.py`; `flowa-schema10.dump`.
- [x] **Single-profile mode.** `FLOWA_MODE=single`: refused at start with more than one local
  account (F15); sign-up answers 403 whatever the flag says; the root page shows the owner's profile
  to visitors as a small page the server writes (no script); Community shows the owner's posts.
  Federation stays off here (FRD and FWR add it). DoD: `TestDBSingleMode` (two or more local accounts refused; no account and no `ADMIN_USER` refused
  with a message saying to set one; a sole account that is not the owner made the owner as
  `ensureOwner` does); a new browser suite
  `single.py` in `ALL` in `test/ci-plan.sh` and its self-test; a screenshot of the visitor's root
  page.
  Built: `single` in `modesBuilt` (`federated` still stops the start; `FLOWA_FEDERATE` is not read yet). At
  start, before anything is written: more than one account refused by count, no account without
  `ADMIN_USER` and `ADMIN_PASSWORD` refused, an `ADMIN_USER` that would be a second account refused, a sole
  suspended account refused; a sole member or moderator made the admin, then the owner by `ensureOwner`; a
  mute and waiting requests to join logged and left alone. Sign-up answers 403 before anything is read,
  counted or hashed; the routes for requests to join, deleting an account, changing a role and handing over
  ownership answer 403; `/api/me` says `single: true` to the signed-in owner and gives a visitor the
  `signups` flag off, without the mode. The visitor's allowance is counted before a session cookie is looked
  up, and a cookie that cannot be a session token is never looked up, on any route (`currentUser`). A signed-out visitor at `/` gets the owner's profile
  from an `html/template` (no script, the CSP of federation.md 5.6, `no-cache` with an ETag, `Vary: Cookie`),
  kept in memory until a profile or instance save or ten minutes, 300 reads an hour per address with its
  picture at `/profile-picture` (`testdata/routes-single.txt`); no post and no number on it. The owner signed
  in gets the app at `/`, signed out at `/index.html`; the People list is hidden and Admin > Instance says
  nobody can join. `TestDBSingleMode`, `TestDBSingleRoutes`, the single-mode part of `TestDBOneConnection`,
  `TestInstanceMode` updated; `test/browser/single.py` (the `single` suite, run with `FLOWA_MODE=single`), and
  the screenshots job runs it again for `visitor.png` and `visitor-phone.png`.
