# INS: Instance modes and settings

Why, scope and risks: [epics.md](../epics.md#ins-instance-modes-and-settings). Design:
[federation.md](../federation.md). Order: [roadmap.md](../roadmap.md#third-cycle).
Category: feature. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** design written, waiting for review (issue [#88](https://github.com/TuanSSM/flowtopia/issues/88)). 0 of 7 items done. Every setting, table, route and
test named below is new unless the design note says it exists.

- [ ] **Design.** [federation.md](../federation.md): the modes, identity, protocol, security, queues,
  data model, administration, budgets, rollout and the sixteen decisions F1 to F16. DoD: reviewed by
  the owner, the decisions answered or the recommendations accepted, linked from the README.
  Built: the note, linked from the README's "What comes next". Open: the owner's review.
- [ ] **A mode setting that changes nothing by default.** `FLOWA_MODE` (`local` by default,
  `federated`, `single`; anything else stops the start with a message), the `instance_settings`
  table (with `flowa-schema7.dump` in `testdata/backups` first, unless another epic added it), the
  mode on the dashboard. No federation route exists yet. DoD: `TestInstanceMode` for the parsing;
  `TestDBLocalModeUnchanged` compares the route patterns with a list checked in before this pull
  request and fails on any outbound dial during start-up; `TestDBMigrations` and the restore drill
  green.
- [ ] **Member blocks.** `blocks`; a block and an unblock endpoint with an allowance in `limits`; a
  blocked member's posts and comments are hidden from the blocker, and the blocked member cannot
  comment on, like or mention the blocker. Behind the `community` flag. DoD: `TestDBBlocks`;
  `TestDBQueryPlans` holds the timeline with 5,000 blocks; a check in `test/browser/social.py`.
- [ ] **Follows and a Home timeline behind a flag.** `follows` (local accounts only at this step);
  Follow and Unfollow on profiles; Home beside Community; the `follows` flag, off by default; counts
  on the profile, lists shown only to the account itself (F10). DoD: `TestDBFollows`;
  `TestDBQueryPlans` holds Home with 500 followed accounts; `TestDBFlagsGateAPI` covers the flag; a
  browser check and a screenshot of Home in `test/browser/pages.py`.
- [ ] **Instance information and rules.** Description, contact and rules in `instance_settings`,
  edited in Admin (permission `flags.edit`), audited, shown at sign-up and on an about page that
  visitors can open. DoD: `TestDBInstanceInfo`; a browser check that sign-up shows the rules; a
  screenshot of the about page.
- [ ] **Sign-up by approval.** The `registration` setting: open, approval or closed (the `signups`
  flag stays as the switch between open and closed); an account waiting for approval cannot sign in
  and is listed in Accounts with Approve and Decline, both audited. DoD: `TestDBRegistrationApproval`;
  `TestDBSignupLimits` unchanged.
- [ ] **Single-profile mode.** `FLOWA_MODE=single`: refused at start with more than one local
  account (F15); sign-up answers 403 whatever the flag says; the root page shows the owner's profile
  to visitors as a small page the server writes (no script); Community shows the owner's posts.
  Federation stays off here (FRD and FWR add it). DoD: `TestDBSingleMode`; a new browser suite
  `single.py` in `ALL` in `test/ci-plan.sh` and its self-test; a screenshot of the visitor's root
  page.
