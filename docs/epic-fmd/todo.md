# FMD: Federation moderation and administration

Why, scope and risks: [epics.md](../epics.md#fmd-federation-moderation-and-administration). Design:
[federation.md](../federation.md), section 10. Order: [roadmap.md](../roadmap.md#third-cycle).
Category: feature. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** not started (issue [#91](https://github.com/TuanSSM/flowtopia/issues/91)). The first item ships together with FWR's inbox, because the allowlist is how
the experimental mode starts (F8). 0 of 8 items done. Every admin action below goes through `audit`.
Every setting, table, route and test named below is new unless the design note says it exists.

- [ ] **Domain policies and the allowlist.** `domain_policies` (allow, limit, suspend, reject media,
  reject reports, public and private comments); the `policy` setting (allowlist or open); an Admin
  page; the permissions `federation.read` and `federation.manage` in `rolePerms`. Checked in the
  fetcher, the inbox and the fan-out. DoD: `TestDBDomainPolicies` (each policy in and out; the
  allowlist refuses an unlisted host both ways), `TestPermissionTable` updated; a screenshot of the
  page.
- [ ] **Actions on remote accounts.** `mute` and `suspend` in `handleModAction` act as limit and
  suspend on a remote account; a new action `purge_remote` deletes its content; the queue and the
  accounts page show remote accounts with their handle. DoD: `TestDBRemoteAccountActions`;
  `TestDBModeration` unchanged for local accounts.
- [ ] **Reports across servers.** Reporting remote content uses the existing reports; resolving can
  forward a Flag from the instance actor without the reporter's name (`reports.forwarded_at`); a Flag
  in becomes a report from the remote instance, unless the domain rejects reports. DoD:
  `TestDBFlagInOut`; `TestTwoInstances` covers a forwarded report.
- [ ] **Authorized fetch.** The `authorized_fetch` setting (off by default, F11): signed GETs
  required except for WebFinger, NodeInfo and the instance actor; `Cache-Control: private`. DoD:
  `TestDBAuthorizedFetch`.
- [ ] **Retention.** `retention_days` (30 by default) and the `housekeep` steps of federation.md
  9.3: remote posts nobody touched, remote accounts with nothing left, inbox rows, failed
  deliveries, the picture cache cut to 160 MB when it passes 200 MB. DoD: `TestDBHousekeeping` gains
  a case per step.
- [ ] **The federation dashboard.** A tile on the admin dashboard (queued, oldest undelivered,
  delivered and failed in 24 hours, hosts open and unavailable, dropped rows, the clock difference),
  a page per host with the last errors and a retry button, and announcements as a notice and a
  notification kind `announcement`. DoD: `TestDBDashboard` extended; a screenshot in
  `test/browser/pages.py`.
- [ ] **Member blocks of domains, and flood blocks.** `domain_blocks_member` from Settings; a host
  refused by its allowance 5 times in an hour is blocked for an hour and shown on the dashboard. DoD:
  `TestDBMemberDomainBlocks`, `TestDBHostFloodBlock`.
- [ ] **Leave the fediverse.** An admin action in federated mode that sends Delete(Person) for every
  local actor, waits for the queue to drain and records it, so a later switch to `local` leaves
  remote servers nothing to retry; local mode after federating answers 410 for actor and WebFinger
  URLs. DoD: `TestDBLeaveFediverse`; `TestTwoInstances` sees the accounts gone on the other side.
