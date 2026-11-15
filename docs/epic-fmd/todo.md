# FMD: Federation moderation and administration

Why, scope and risks: [epics.md](../epics.md#fmd-federation-moderation-and-administration). Design:
[federation.md](../federation.md), section 10. Order: [roadmap.md](../roadmap.md#third-cycle).
Category: feature. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#91](https://github.com/TuanSSM/flowtopia/issues/91)); item 1 is done. The first item shipped in two parts, the first with FWR's inbox, because the allowlist is how
the experimental mode starts (F8). 1 of 8 items done. Every admin action below goes through `audit`.
Every setting, table, route and test named below is new unless the design note says it exists.

- [x] **Domain policies and the allowlist.** `domain_policies` (allow, limit, suspend, reject media,
  reject reports, public and private comments); the `policy` setting (allowlist or open); an Admin
  page; the permissions `federation.read` and `federation.manage` in `rolePerms`. Checked in the
  fetcher, the inbox and the fan-out. A limited domain's follows become follow requests, as
  Mastodon's domain Limit does (federation.md section 10). The Admin page says that the allowlist
  alone does not stop an unlisted server reading public actors and posts; authorized fetch does
  (federation.md 7.6). DoD: `TestDBDomainPolicies` (each policy in and out; the allowlist refuses an
  unlisted host both ways; a Follow from a limited domain waits for approval),
  `TestPermissionTable` updated; a screenshot of the page.
  Built so far (with FWR's inbox, federation.md section 10, "As built (FMD 1, ...)"): `domain_policies`
  (migration 17), the `federation_policy` setting (allowlist by default), `federation.read` and
  `federation.manage` (`fedPerms`, listed only in a mode that federates), the admin API and the Federation tab
  of the Admin page (allow and suspend with a reason, every change audited), the inbox's 403 for a suspended or
  unlisted domain before any work, the fetcher refusing every host the policy does not let in (both ways: no
  key, actor or search by handle reaches an unlisted server in allowlist mode); `TestPolicyDecide`,
  `TestDBDomainPolicies`, `TestDBPolicyRefusesFetch`, `TestDBPolicyBothWays`, `TestPermissionTable` updated, the
  inbox's cases in `TestInboxRefusals`, and the page in the `federated` browser suite with its screenshot.
  Built with the rest (federation.md section 10, "As built (FMD 1, the rest)"): the fan-out proven to pass `admits`
  on every path (it did already: `TestFanOutPassesAdmits` reads every producer, enqueue, fan-out and request from the
  code, and `TestDBFanOutDueAsksPolicy` covers an activity that waited for room); `limit` (its accounts shown only to
  their followers: replies listed and counted, notifications, posts only naming a member, bio, posts in a search by
  handle; a Follow from it a request whatever the member's setting, asked of the stored policy; follows kept, requests
  left waiting when lifted); reject media (pictures kept as links, the kept files removed, never fetched after);
  reject reports stored and shown with what it does not do yet; the public comment and the note for staff, the about
  page's list of limited and suspended domains when an admin publishes it (off by default); the Admin page paged with
  a search, every value editable, the mode switch asking first, the sentence on authorized fetch. DoD: each policy in
  and out in `TestDBDomainPolicies` (allow and suspend, and its parts for limit with a Follow from a limited domain
  waiting for approval, and reject media), the allowlist both ways in `TestDBPolicyBothWays`, `TestPermissionTable`
  (it lists both permissions since the first part; nothing new to add), the screenshot `federated-admin-federation`
  with one domain of each kind; and between two real instances, 20 checks of the `federation` suite (a Follow from a
  limited domain waiting, a reply hidden, a picture kept as a link and never fetched, a suspension refusing both ways and
  lifted). No migration.
  **Not in this item, by the design:** what reject reports gates (a Flag from that domain) is item 3, and authorized
  fetch is item 4, as the Admin page says; import and export of the list are "later" in section 10, not built.
  **For the owner (defaults built):** whether an exact-handle search should find an account of a limited domain at
  all (built: it answers with the profile and a notice, no posts and no bio, so the account can still be followed);
  whether likes of limited accounts should stop counting (built: they count, as counts name nobody); whether lifting
  reject media should fetch the old pictures again (built: no, only an edited post brings them back); whether the
  about page should list the moderated servers by default (built: off until an admin ticks it).
- [ ] **Actions on remote accounts.** `mute` and `suspend` in `handleModAction` act as limit and
  suspend on a remote account (limit: hidden from all but its followers, its follows become
  requests; suspend: its follows removed and not restored on unsuspend, as in Mastodon); a new
  action `purge_remote` deletes its content; the queue and the
  accounts page show remote accounts with their handle. DoD: `TestDBRemoteAccountActions`;
  `TestDBModeration` unchanged for local accounts.
- [ ] **Reports across servers.** Reporting remote content uses the existing reports; resolving can
  forward a Flag from the instance actor without the reporter's name (`reports.forwarded_at`), in
  Mastodon's shape (the account then the posts in `object`, the comment in `content`); a Flag in
  becomes a report from the remote instance with its `content` as the note, unless the domain
  rejects reports. DoD:
  `TestDBFlagInOut`; `TestTwoInstances` covers a forwarded report.
- [ ] **Authorized fetch.** The `authorized_fetch` setting (off by default, F11): signed GETs
  required except for WebFinger, NodeInfo, the instance actor and the public key part of an actor;
  `Cache-Control: private` and `Vary: Accept, Signature` (federation.md 7.6). DoD:
  `TestDBAuthorizedFetch`; `TestContentNegotiation` gains the `Vary` case.
- [ ] **Retention.** `retention_days` (30 by default) and the `housekeep` steps of federation.md
  9.3: remote posts nobody touched, remote accounts with nothing left, inbox rows, failed
  deliveries, the picture cache cut to 160 MB when it passes 200 MB. DoD: `TestDBHousekeeping` gains
  a case per step.
- [ ] **The federation dashboard.** A tile on the admin dashboard (queued, oldest undelivered,
  delivered and failed in 24 hours, hosts open and unavailable, dropped rows, the clock difference),
  a page per host with the last errors and a retry button, and announcements as a notice and a
  notification kind `announcement`. DoD: `TestDBDashboard` extended; a screenshot in
  `test/browser/pages.py`.
- [ ] **Member blocks of domains, and flood blocks.** `domain_blocks_member` from Settings (the
  member's followers on that domain are removed, as Mastodon's "Hiding an entire server" does); a
  host refused by its allowance 5 times in an hour is blocked for an hour and shown on the
  dashboard. DoD: `TestDBMemberDomainBlocks`, `TestDBHostFloodBlock`.
- [ ] **Leave the fediverse.** An admin action in federated mode that sends Delete(Person) for every
  local actor, waits for the queue to drain and records it, so a later switch to `local` leaves
  remote servers nothing to retry; local mode after federating answers 410 with a Tombstone for
  actor URLs and 404 for WebFinger (federation.md 4.3). DoD: `TestDBLeaveFediverse`; `TestTwoInstances` sees the accounts gone on the other side.
