# MOD: Moderators, admin panels and the skill map editor

Why, scope and risks: [epics.md](../epics.md#mod-moderators-admin-panels-and-the-skill-map-editor).
Order: [roadmap.md](../roadmap.md). Each milestone is one commit in the epic's pull request ([implementation plan](../implementation-plan.md)). An
item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** done. 29 of 29 items done, in the MOD pull request.

## Already in place

- [x] `users.role` with `user` and `admin`; `authed(true, ...)` refuses non-admins.
- [x] Admin page: feature flags, account list, password reset, delete account.
- [x] Admins can delete any post or comment.
- [x] Catalogs as JSON per prop (`web/catalog/*.json`) validated by `node test/catalog.js`.

## M1. Roles and permissions

- [x] **Permission table.** One Go map from role to permissions (`post.hide`, `user.mute`,
  `user.suspend`, `report.resolve`, `badge.revoke`, `catalog.edit`, `catalog.publish`, `flags.edit`,
  `user.role`), and a `can(u, perm)` helper used by every handler instead of `Role == "admin"`.
  DoD: `grep 'Role == "admin"\|Role != "admin"' main.go` finds nothing; a table-driven Go test
  covers every role and permission.
- [x] **Moderator role.** `role` accepts `moderator`; admins change roles on the Admin page; a user
  cannot change their own role; the last admin cannot be demoted.
  DoD: Go tests for self-change and last-admin refusals; `social.py` promotes a user and that user
  sees the moderation entry in the menu.
- [x] **UI follows the server.** Menu entries and buttons appear by permission, read from
  `GET /api/me` (`perms: [...]`).
  DoD: browser test signs in as user, moderator and admin and checks the menu for each.

## M2. Reports

- [x] **Report anything.** `reports` table (reporter, target kind `post`, `comment` or `user`, target
  id, reason from a fixed list plus optional note, status, timestamps). `POST /api/reports`; one
  open report per reporter and target.
  DoD: Go tests for duplicate, unknown target, and reporting oneself (refused).
- [x] **Report button.** "Report" in the menu of posts, comments and profiles, with the reasons
  Spam, Harassment, Unsafe advice, Not about flow arts, Other.
  DoD: `social.py` reports a post as another user and the report appears in the queue.
- [x] **Rate limit.** At most 10 reports per user per day.
  DoD: Go test for the 11th report.

## M3. Moderation queue and actions

- [x] **Queue.** Moderation page lists open reports grouped by target, oldest first, with the
  content, the reporters' reasons and the author's history (posts, previous actions).
  DoD: browser test with three reports on one post shows one entry with three reasons.
- [x] **Hide and restore.** Hiding a post or comment keeps it in the database with a reason; it
  disappears for everyone except its author (who sees "Hidden by a moderator: <reason>") and
  moderators. Restore undoes it.
  DoD: Go test for visibility per viewer; `social.py` hides and restores.
- [x] **Mute and suspend.** Mute (cannot post or comment) or suspend (signed out and cannot sign in)
  until a chosen time, with a reason. Suspension deletes the user's sessions.
  DoD: Go tests for each, including expiry; a suspended user's sign-in gets a message with the
  end time.
- [x] **Resolve.** Every report ends as Action taken or No action, with a note.
  DoD: the queue's count drops; resolved reports appear under History.
- [x] **People affected are told.** The author gets a notification for hide, mute or suspend with
  the reason; reporters get one when their report is resolved.
  DoD: `social.py` checks both notifications.
- [x] **Audit log.** `mod_actions` table records who, what, target, reason and when for every
  moderation and admin action (including flag and role changes). Admins see it, filterable by
  person and type.
  DoD: Go test that each action handler writes exactly one row; browser test filters the log.
- [x] **Self-reported data.** Moderators can revoke a badge or reset a user's reported stats, with a
  reason that goes to the audit log and to the user.
  DoD: Go test; the people list no longer ranks the reset user by the old XP.

## M4. Admin dashboard

- [x] **Numbers.** Users, active users in the last 7 and 30 days (from sessions and progress
  updates), sessions logged, posts, comments and open reports per week for 12 weeks.
  DoD: `GET /api/admin/dashboard` computes them in SQL; `TestDBDashboard` checks every number on
  fixture data, including a malformed practice date that must be ignored.
- [x] **Charts.** Small weekly bar charts with the values readable without hover, in the app's
  palette, at 420 px wide.
  DoD: `pages.py` screenshots the dashboard; reviewed in the PR.
- [x] **Accounts panel.** Search, filter by role and status, role change, mute and suspend from the
  account row, last active date.
  DoD: browser test finds a user by name and changes the role.

## M5. Catalogs in the database (no visible change)

- [x] **Nunchaku as JSON.** `web/data.js` and `web/details.js` become `web/catalog/nunchaku.json` in
  the catalog format; `catalogs.js` loads all five the same way.
  DoD: `node test/catalog.js web/catalog/*.json` passes for five files; a comparison of the catalog
  the page built before and after found 109 skills and 0 differences, and `docs/demo-coverage.md`
  regenerates byte for byte; every browser suite passes unchanged.
- [x] **One validator, in Go.** The catalog rules (`docs/catalog-format.md`) implemented in Go;
  `go test` runs it over every catalog; `test/catalog.js` and the Go validator agree on a fixture
  set of good and bad catalogs.
  DoD: 27 fixtures (`test/catalog-fixtures`); both validators give the expected verdict on each. The
  JavaScript rules now live in `web/catalog-check.js`, shared by `test/catalog.js` and the editor.
- [x] **Revisions table.** `catalog_revisions` (prop, revision number, JSON, author, note, status
  `draft` or `published`, timestamps). On first start each embedded catalog is imported as
  revision 1.
  DoD: Go test of the import on an empty database and that a second start imports nothing.
- [x] **Served from the database.** `GET /catalog/<prop>.json` returns the latest published
  revision with an ETag; the embedded file is the fallback if the table is empty.
  DoD: browser suites pass against the database-served catalogs.

## M6. Skill map editor

- [x] **Edit a draft.** An editor page per prop: branches, skills (name, description, tier, needs,
  track, steps, watch, drill, refs, star, care) and phases, saved as a draft revision.
  DoD: browser test edits a skill name and saves a draft; the live map is unchanged.
- [x] **Validate as you type.** Each field shows the validator's message for it; Publish is disabled
  while there are errors.
  DoD: browser test enters a prerequisite cycle and sees the error and a disabled Publish.
- [x] **Diff and impact.** Before publishing, a diff against the published revision, and for each
  changed or retired skill the number of users with progress on it.
  DoD: Go test of the impact query; browser test sees the diff for a renamed skill.
- [x] **Stable ids.** Ids are never reused. Renaming a skill keeps its id; changing an id records an
  alias, and progress documents are read through aliases. Retiring a skill hides it from the map
  but keeps progress.
  DoD: progress survives a rename and an id change (`editor.py` checks the progress document moves to
  the new id); removing a skill with progress is refused and retiring offered instead (`TestDBCatalogs`).
- [x] **Publish and roll back.** Publish makes the draft live for everyone at once; any earlier
  revision can be restored as a new revision. Both are audit-logged.
  DoD: browser test publishes, sees the change on the map, rolls back, sees it gone.
- [x] **Export.** Download any revision as JSON in the catalog format, so a fix made in the editor
  can be committed back to the repository.
  DoD: the exported file passes `node test/catalog.js` and round-trips to the same revision.
- [x] **Separate permissions.** Moderators with `catalog.edit` can draft; only `catalog.publish`
  (admins by default) can publish.
  DoD: Go test that a moderator's publish is refused.

## M7. Finish

- [x] **Flags.** `moderation` gates M2 and M3; `catalogEditor` gates M6; both default off until
  their milestone has had a week on the live instance.
  DoD: with each flag off, the pages and endpoints are absent (404) and the menu entries hidden.
- [x] **Docs.** README sections for roles, moderation and the editor; `docs/catalog-format.md` gains
  aliases and retirement; [multi-prop-architecture.md](../multi-prop-architecture.md) marks step 9
  done; this file shows every item checked.
  DoD: `test/docs.sh` passes.
