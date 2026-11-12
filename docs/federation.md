# Federation: design and plan

The owner asked for ActivityPub, so that Flowa can run as a single-profile instance, as a federated
community, or as a local-only community as it does today, with what an operator expects from
Mastodon's administration in mind. This page is the design note for that work: what the code has
today, what federation needs, and the order of pull requests. The epics are
[INS](epics.md#ins-instance-modes-and-settings), [FRD](epics.md#frd-federation-read-side),
[FWR](epics.md#fwr-federation-write-side), [FMD](epics.md#fmd-federation-moderation-and-administration)
and [PRT](epics.md#prt-portability), with checklists under `docs/epic-<code>/todo.md`; their order
is in [roadmap.md](roadmap.md#third-cycle).

**It is a proposal waiting for the owner's review.** Nothing in it is built, and no code changed with
it. The decisions come first, because the rest depends on them. Until the owner answers one, the plan
proceeds on its recommendation, and each decision says what the plan assumes meanwhile.

**One rule above all others: federation is off by default, and turning nothing on changes nothing.**
A deployment that upgrades and sets no new setting behaves exactly as today: port 3169, the same
addresses, the same sign-up, no new outbound connection, no new public endpoint answering. Nothing in
this plan changes `compose.yml`'s ports, adds a reverse proxy or ends TLS in the app. HTTPS and the
public name are the operator's own reverse proxy's job; the app is told its public origin by one
setting and refuses to federate without it.

Sections 1 to 3 describe the code as it is. Code is named by file and by function, constant, table
or setting, never by line. Every name marked as existing was looked up with grep on `main` on
12 November 2026 (commit `72b1b0c`: 7 migrations, 15 flags). Every name this plan adds is marked
**new**. Sections 4 onward are the proposal.

## Contents

- [Decisions the owner is asked for](#decisions-the-owner-is-asked-for)
- [What was found on the way](#what-was-found-on-the-way)
- [1. The community side as it is](#1-the-community-side-as-it-is)
- [2. What federation can reuse](#2-what-federation-can-reuse)
- [3. What is missing](#3-what-is-missing)
- [4. Instance modes](#4-instance-modes)
- [5. Identity and discovery](#5-identity-and-discovery)
- [6. The protocol surface](#6-the-protocol-surface)
- [7. Security](#7-security)
- [8. Delivery and work queues](#8-delivery-and-work-queues)
- [9. Data model](#9-data-model)
- [10. Administration, with Mastodon as the reference](#10-administration-with-mastodon-as-the-reference)
- [11. The member's side](#11-the-members-side)
- [12. Budgets](#12-budgets)
- [13. How interoperability is shown without a live Mastodon](#13-how-interoperability-is-shown-without-a-live-mastodon)
- [14. Rollout, order of pull requests and rollback](#14-rollout-order-of-pull-requests-and-rollback)
- [15. Risks](#15-risks)
- [Sources](#sources)

## Decisions the owner is asked for

Each has the options and a recommendation. Later sections refer to them as F1 to F16.

**F1. Where the mode is set.** The instance mode (`local`, `federated`, `single`) changes the
identity of every account once federation has started.

- (a) An environment setting, `FLOWA_MODE` (**new**), read at start, like `FLOWA_HOSTS`.
- (b) An admin setting in the database, changed from the Admin page at runtime.
- Recommended: (a) for the mode, the public origin and the key secret, because they cannot change
  safely while the server runs and a click must not be able to announce an identity; (b) for policy
  that can change at runtime (federation policy, authorized fetch, registration mode, rules,
  retention), kept in `instance_settings` (**new**) and audited. A feature flag `federation`
  (**new**, default off) is the admin's pause switch. Assumed until answered: (a) and (b) as split.

**F2. Do members' posts leave the server by default?** Today every community endpoint needs a
session (section 1.3), so nothing a member writes is readable by anyone outside the instance.

- (a) In federated mode every member federates, and every post is public.
- (b) Each member opts in once ("Share my profile and public posts beyond this server"), and each
  post then has an audience: **Public** (federates) or **This server** (never leaves).
- (c) Each post chooses, with no opt-in per member.
- Recommended: (b), with the opt-in off for every existing member and for new members in federated
  mode, and on for the owner in single mode. Posts made before a member opted in never federate (no
  backfill). This keeps the roadmap's principle "nothing is shared without an action from the user".
  Assumed until answered: (b).

**F3. Which audiences in the first cut.** Mastodon has public, unlisted, followers only and direct.

- Recommended: outgoing, **Public** only (addressed to the public collection, copied to followers)
  and **This server** (not an ActivityPub audience at all: never sent). Incoming, public and unlisted
  are shown; followers-only posts from an account a member follows are shown to the local members who
  follow it and to nobody else; direct messages are refused, because the roadmap does not plan direct
  messages ("Not planned"). Followers-only outgoing is later, once follows have been used locally.
  Assumed until answered: as recommended.

**F4. Where remote posts appear.** Flowa has one shared timeline and no follow relation.

- (a) Mixed into the community timeline.
- (b) A new **Home** timeline (accounts the member follows, local or remote, and their own posts)
  beside the unchanged **Community** timeline (local posts only).
- Recommended: (b). In single mode Home is the owner's main timeline and Community is the owner's own
  posts. Assumed until answered: (b).

**F5. How remote accounts and posts are stored.**

- (a) Remote accounts are rows in `users` with a domain, and remote posts and replies are rows in
  `posts` and `comments` with an ActivityPub id, as Mastodon keeps local and remote accounts in one
  table. Likes, comments, notifications, reports, mutes and suspensions then work on remote content
  unchanged.
- (b) Separate tables for remote actors, notes, likes and replies, with their own queries.
- Recommended: (a), argued in section 9.2. It reuses most and builds nothing twice, but every query
  that lists members must say "local only", and an older build after a rollback would show remote
  accounts and posts as if local (section 14.4). Assumed until answered: (a).

**F6. Remote pictures.**

- (a) Hot-link them: the visitor's browser fetches from the remote server. Leaks every visitor's
  address to every server, and needs `img-src` opened to every host.
- (b) Proxy and cache them on the Pi, shrunk, in a bounded cache.
- (c) Show a link instead of the picture.
- Recommended: (b) with a cache of at most 200 MB on disk under `-data`, pictures shrunk to at most
  1280 px and 200 KB, avatars to 256 px and 40 KB, fetched on first view and dropped least recently
  used first. `img-src 'self' data: blob:` stays as it is. Assumed until answered: (b).

**F7. Private keys at rest.** Each federating account has an RSA key pair.

- (a) Stored as plain PEM in Postgres (what Mastodon is understood to do; not checked).
- (b) Sealed with AES-256-GCM under a secret from the environment, `FLOWA_KEY_SECRET` (**new**, 64
  hex characters), which is never in the database or the backups.
- Recommended: (b). `backup.sh` dumps the whole database every day; with (a) every backup file would
  hold every member's signing key. Without the secret the server does not federate. Losing it means
  new keys for every account, which remote servers pick up on their next failed verification
  (section 7.4). Assumed until answered: (b).

**F8. Federation policy at first.**

- (a) Open: talk to any server not blocked.
- (b) Allowlist: talk only to servers an admin listed.
- Recommended: (b) while federated mode is marked experimental, then (a) with domain blocks after the
  owner's trial against a real server (FWR's last item). "Safe to open up" is a roadmap principle.
  Assumed until answered: (b).

**F9. Give-up horizon for deliveries.** How long a post keeps being retried to a server that is down.

- Recommended: 48 hours with the schedule in section 8.3, about what the brief says Mastodon does
  (not checked against its code). Longer costs rows on a Pi; shorter loses posts to a server that is
  down over a weekend. Assumed until answered: 48 hours.

**F10. Followers and following lists.**

- Recommended: the counts are public, the members of each list are shown only to the account itself,
  as Mastodon allows an account to hide them. Assumed until answered: as recommended.

**F11. Authorized fetch (signed GETs required).**

- Recommended: an admin option, off by default, because some software cannot fetch with a signature
  and an instance that requires it loses those readers; on for an instance that wants to stop blocked
  servers reading public posts. Assumed until answered: off.

**F12. Stats and badges on a federated profile.** Level, streak, XP and badges are reported by the
browser until TRU ships ([progress-server.md](progress-server.md)).

- (a) Send them in the actor document as profile fields.
- (b) Send none of them until TRU's switch, and then only what the profile already shows.
- Recommended: (b). Another server would show a number Flowa cannot vouch for, with Flowa's name on
  it. The actor carries the props a member practises and their links, nothing else. Assumed until
  answered: (b).

**F13. Milestone posts.** Badges post themselves as milestones (`handleAchievements`, kind
`milestone`) when the member's `autoPost` preference is on.

- Recommended: milestone posts stay on the server (audience This server) until TRU's switch, for the
  reason in F12. Assumed until answered: as recommended.

**F14. The namespace for trick chips and combos.** Other Flowa servers should read them back as chips.

- (a) A term under each instance's own origin.
- (b) One fixed IRI for every Flowa: `https://github.com/TuanSSM/flowtopia/ns#`.
- Recommended: (b): one meaning across instances, owned by the project, documented in this page
  (section 6.5). It does not need to resolve to anything. Assumed until answered: (b).

**F15. Single mode with more than one account.**

- (a) Refuse to start in single mode while more than one local account exists.
- (b) Start, and make the other accounts read-only.
- Recommended: (a), with a log line naming the count and the way out (delete the other accounts in
  local mode first). Assumed until answered: (a).

**F16. What a signed-out visitor sees.** Today a signed-out visitor sees no community content at all.

- Recommended: in local mode, still nothing. In federated and single mode, the profile and the Public
  posts of members who opted in (F2), as small pages the server writes itself (no script, no API
  call), so a person on another server who opens a post link in a browser sees the post. In single
  mode the root page shows the owner's profile to visitors. Assumed until answered: as recommended.

## What was found on the way

These change the design and are not obvious from the brief.

1. **Nothing in the community is public today.** Every route in `socialRoutes` goes through
   `authed`, and so does `GET /api/users/{id}/avatar` and `GET /api/previews/{file}`. The README says
   "public profile page (`#/u/<username>`)", which means visible to members. Federation is the first
   feature that makes member content readable outside the instance; hence F2 and F16.
2. **The write guard would refuse every inbox delivery.** `guard` answers 415 to a write whose
   `Content-Type` does not start with `application/json`. Deliveries carry
   `application/activity+json` or `application/ld+json; profile="https://www.w3.org/ns/activitystreams"`.
   The inbox routes need their own content type check, and only they may skip the JSON one.
3. **The host check would refuse the public name.** `allowedHost` in `secure` accepts addresses,
   single-label and private names and `FLOWA_HOSTS`, and answers 421 to any other name. The host of
   the public origin has to be accepted by itself once it is set.
4. **The signature covers `host` and the path.** Behind the operator's reverse proxy, `r.Host` may be
   rewritten. Flowa verifies against the host of the public origin, not `r.Host`, and the operator's
   proxy must serve the app at the root of the origin without rewriting the path. Both go in the
   README's "Behind a reverse proxy" when FWR's inbox ships.
5. **The fetcher is an allowlist of nine preview hosts.** `fetcher.get` takes only hosts in the list
   it is given, only `https`, only GET, with `fetchTimeout` of 3 s, and its `CheckRedirect` refuses
   any host outside `previewHosts` and `thumbHosts`. Its dial guard (`publicIP` in the `Control`
   function of `newFetcher`) is exactly what federation needs; the host allowlist and the method are
   not. Section 7.8.
6. **No job queue.** Background work is `go a.preview(l)` per link and the hourly `housekeeping`.
   Nothing survives a restart, nothing retries, nothing is bounded by a queue.
7. **No keys, no signing.** No RSA, no `crypto/rsa` import, nothing per account.
8. **Pictures are data URLs in text columns.** `posts.image` and `users.avatar` hold the whole data
   URL. The feed carries post pictures inline in its JSON (`loadPosts` scans `p.image`), and avatars
   are served by `handleAvatar`, which needs a session. A remote server needs a plain public URL for
   each, so federation adds two public picture endpoints for public content only.
9. **Mentions of a remote handle would notify the wrong member.** `mentionRE` in `main.go` and
   `richText` in `web/social.js` read `@anna@social.example` as a mention of the local `anna`: the
   server would notify her (`notifyNames`) and the page would link to her profile. Both must learn the
   `@name@host` form before the local one (FWR, posts in).
10. **Usernames can be reused.** `handleDeleteUser` deletes the row and nothing keeps the name, so a
    new sign-up could take it and inherit another person's identity on every server that knew it.
    Federation keeps a list of retired names (section 9).
11. **Usernames Flowa allows that other servers may not.** `usernameRE` allows a name that ends in a
    dot or a dash (`ana.`, `bo-`). Whether Mastodon parses such a handle in a mention is not known
    here; the actor and WebFinger work regardless, and FRD's fixture tests check the mention form.
12. **No follow relation and no user-level blocks.** One shared timeline; mutes and suspensions are
    moderator actions only (`handleModAction`). Federation needs both a follow and a block.
13. **The database pool is 8 connections** (`openDB`, `SetMaxOpenConns(8)`). Workers and inbox
    handling must share a small part of it, or a delivery burst starves the pages (section 12).
14. **The next migration needs a schema 7 backup first.** `testdata/backups` holds
    `flowa-schema5.dump` and `flowa-schema6.dump`; `migrations` has 7 entries. TRU's note found the
    same. Whichever epic adds the next migration adds `flowa-schema7.dump` with it, and this plan
    names no migration numbers, because TRU may take the next one.
15. **The people list reads `users` whole** (`peopleSQL`, held by `TestDBQueryPlans`). With remote
    accounts in the same table (F5) it must filter to local accounts, and the plan test must hold with
    thousands of remote rows.

## 1. The community side as it is

### 1.1 Accounts and profiles

- `users` (migration 1 and `schemaSocial`): `id`, `username` (unique), `display_name`, `pw_hash`,
  `role`, `props`, `avatar` (a data URL), `bio`, `links` (handles for `linkKinds`: instagram, tiktok,
  youtube), `prefs`, `stats` (reported by the page), `avatar_cfg`, `owner`, `last_seen`, and the
  moderation columns `muted_until`, `mute_reason`, `suspended_until`, `suspend_reason`,
  `stats_hidden`.
- Usernames: `usernameRE`, lower case, 3 to 24 characters of letters, digits, dot, dash and
  underscore, starting with a letter or digit. No endpoint renames an account. The stable identifier
  in the page's URLs is the username (`#/u/<username>`); the API uses the numeric id for pictures
  (`api/users/{id}/avatar`) and the username for profiles (`GET /api/users/{username}`).
- `cleanName` checks display names, `maxBio` is 300 characters, `cleanImage` decodes and re-encodes
  every picture (JPEG or PNG, `maxAvatar` and `maxImage` bounds), dropping metadata.
- `pubUser` and `user.public()` are what others see: the stats are emptied when `stats_hidden`.
- One owner: `users.owner`, kept an admin by the constraint `users_owner_is_admin` and unique by
  `users_one_owner` (migration 5); `ensureOwner` at start.

### 1.2 Posts, comments, likes, bookmarks, notifications

- `posts`: `kind` (`post` or `milestone`), `body` (at most `maxPost`, 2000 characters), `image`,
  `data` (JSON with `tags`, the trick chips as `postTag` prop and skill, at most `maxTags` 10;
  `combo`, a `postCombo`; `links`, up to `maxLinks` 4 `postLink`), `prop` and `skill` (the first
  chip, for older clients), `hidden_at`, `hidden_reason`. Posts cannot be edited. `handleCreatePost`
  checks the mute, the `posts` allowance, the picture, the chips, the combo and the links, and uses
  `once` for the `Idempotency-Key`.
- `comments`: one level of threading (`parent_id`), `edited_at` with `editWindow` of 15 minutes,
  `deleted` kept as "Comment deleted" when it has replies, `hidden_at`. `handleComment`,
  `handleEditComment`, `handleDeleteComment`.
- `likes` and `comment_likes` (primary key per post or comment and member). `bookmarks` (migration 4).
- `notifications`: `kind`, `actor_id`, `post_id`, `comment_id`, `read`; written by `notify` and
  `notifyAt`, which skip one's own actions; mentions by `notifyMentions` and `notifyNames`, at most
  `maxMentions` 10 names, looked up among local usernames.
- Mentions: `mentionRE` on the server, `richText` in `web/social.js` on the page. There are no
  hashtags; the chips are the tags.
- Link cards: `link_previews`, filled by `preview` in the background through the `fetcher`, served
  by `handlePreviewImage` while a post the viewer may see links to it.

### 1.3 Who sees what

- One shared timeline: `feedSQL` reads every post newest first, hidden ones only for their author and
  for those with `post.hide`. `GET /api/posts` takes `user` and `before`.
- Every community route is `social(...)`, which is `flagged("community", authed("", h))`: a session
  is required, and the `community` flag turns the API off. A signed-out visitor sees nothing.
- The people list (`peopleSQL`, `handleUserList`) is the 100 members with the most XP.

### 1.4 Moderation, roles, audit

- Roles `user`, `moderator`, `admin` (`roleRank`), permissions in `rolePerms`, checked by `can`;
  acting on someone else's content checks `outranks`.
- Reports: `reports` with `reportReasons`, at most `maxReportsPerDay` 10 per reporter, one open report
  per reporter and target (`reports_open_once`); the queue (`handleQueue`, `fillQueueItem`) and
  `handleResolve`.
- Actions in `handleModAction`: `hide`, `restore`, `mute`, `unmute`, `suspend`, `unsuspend`,
  `revoke_badge`, `hide_stats`, `show_stats`, each audited through `audit` into `mod_actions`, read by
  `handleAudit`.
- Accounts: `handleUsers`, `handleDeleteUser`, `handleResetPassword`, `handleSetRole`,
  `handleSetOwner`. The dashboard: `handleDashboard` with `dashTotalsSQL`, `dashActiveSQL`,
  `dashOpenSQL`, `dashWeeksSQL`.

### 1.5 Sign-up, sessions, settings

- `handleSignup` refuses while the `signups` flag is off, counts sign-ups per address in the `signup`
  allowance (10 an hour), and refuses `commonPasswords`. Sessions are hashed tokens in the
  `flowa_session` cookie (`cookieName`), HttpOnly, SameSite Lax, ending after `sessionTTL` (30 days)
  or `sessionIdle` (14 days unused).
- Feature flags: `flagDefaults` (15, all on), changed by `handlePutFlags`, gated by `flagged` (404
  while off), cached by `flagsCached`.
- Settings read in `main`: `DATABASE_URL`, `ADMIN_USER`, `ADMIN_PASSWORD`, `VERSION`,
  `FLOWA_TRUSTED_PROXIES` (`parseProxies`, default `defaultProxies`), `FLOWA_HOME_URL` (`homeLink`),
  `FLOWA_COUNTS_TOKEN` (`countsToken`), `FLOWA_HOSTS`, and for tests `FLOWA_PREVIEW_BASE` and
  `FLOWA_PREVIEW_FETCH`. `compose.yml` publishes `3169:3169`, runs read-only with `mem_limit: 384m`,
  `cpus: "2"` and `pids_limit: 256`.

### 1.6 Housekeeping, log, health

- `housekeep` every hour (`housekeeping`): expired sessions, old read notifications, unused link
  previews, bookmarks of deleted posts. `TestDBHousekeeping`.
- The request log (`logged`, `requestLog`, `maxRequestLines` 240 a minute) writes the route pattern,
  never the path; refusals through `logRefusal` (`maxRefusalLines` 120 a minute).
- `GET /healthz` (`handleHealthz`, `checkHealth`): 200 or 503 for anyone, the detail (`health`:
  schema, migrations, TLS roots, version) only for `localProbe`.

## 2. What federation can reuse

| Existing | Used for |
| --- | --- |
| `newFetcher`'s dial guard (`publicIP`, `reservedNets`), `TestFetcherRefusals`, `TestPublicIPReserved` | The one way out to another host: every actor, key, object and picture fetch and every delivery (section 7.8). |
| `fetcher.busy` (one fetch at a time per host) | The idea of per-host concurrency, made a counted semaphore in the delivery workers. |
| `limiter`, `newLimiter`, `allowed`, `logRefusal` | Allowances per remote host on the inbox, per member for follows and blocks. The limiter already caps its own size. |
| `notify`, `notifyAt`, `notifications` | Follows, follow requests, remote replies, likes and mentions (remote actors are `users` rows under F5, so `actor_id` works). |
| `reports`, `handleQueue`, `handleResolve`, `handleModAction`, `audit`, `mod_actions` | Reports about remote content, actions on remote accounts, every federation admin action logged. |
| `rolePerms`, `can`, `outranks` | New permissions `federation.manage` and `federation.read` (**new**) beside the others. |
| `once` | Idempotent follow, like and block from the page. Inbox idempotency is by activity id in the database instead, because `once` forgets at restart. |
| `cleanImage`, `shrinkThumb` | Remote pictures shrunk before they are cached; local pictures re-encoded as today. |
| `housekeep` | Purging delivered rows, old inbox rows, remote posts nobody touched, the picture cache. |
| `checkHealth`, `handleDashboard` | Queue depth, oldest undelivered, failing hosts. |
| `flagged`, `flagDefaults` | The `federation` pause switch and the `follows` flag. |
| `users.owner`, `ensureOwner` | The owner of a single-profile instance. |
| `migrate` with `pg_advisory_lock(7130651)` | Every new table, appended and additive. |

## 3. What is missing

1. A public origin and a way to refuse federation without it.
2. Key pairs, signing and verification (no library: about 300 lines of standard library).
3. A durable, bounded work queue in Postgres for deliveries and for inbound activities.
4. A general guarded fetcher: any public host subject to domain policy, GET and POST, redirects
   re-checked, content types and sizes enforced.
5. Follows, blocks, a Home timeline, remote accounts and posts in the data model.
6. Public read surfaces: actor documents, the outbox, picture URLs, small HTML pages.
7. Remote HTML turned into text safely, and local text turned into the HTML other servers expect.
8. Domain policies and their admin page.

The three largest gaps are the queue (3), signatures (2) and the guarded general fetcher (4).

## 4. Instance modes

### 4.1 The setting

`FLOWA_MODE` (**new**): `local` (the default, and what an unset or empty value means), `federated`
or `single`. Any other value stops the server at start with a message naming the three. In single mode
`FLOWA_FEDERATE` (**new**, `on` or `off`, default `off`) says whether the one profile federates; in
federated mode it is implied and ignored.

### 4.2 What each mode means for every surface

| Surface | `local` (default) | `federated` | `single` |
| --- | --- | --- | --- |
| Port, addresses, `compose.yml` ports | as today | as today | as today |
| Sign-up | as today (`signups` flag) | as today, plus registration modes (section 10) | closed for good: `handleSignup` answers 403 whatever the flag says |
| Accounts | many | many | exactly one local account, the owner (F15) |
| Root page for a signed-out visitor | as today (Welcome) | as today | the owner's profile (F16) |
| Timelines | Community | Community (local posts) and Home (followed accounts) | Home (followed accounts) and the owner's posts |
| People list | as today | local members only; remote accounts by handle search | the owner's following |
| Follows | off unless the `follows` flag is on (local only) | local and remote | the owner follows; others follow the owner |
| Federation endpoints (WebFinger, NodeInfo, actors, inboxes, outboxes) | none: 404, as for any unknown path | answer | answer only with `FLOWA_FEDERATE=on` |
| Outbound connections | link previews only, as today | link previews and federation | as federated, or link previews only |
| Public pages for signed-out visitors | none | profiles and Public posts of members who opted in | the owner's profile and Public posts |
| Feature flags | as today | `federation` (pause), `follows` | as federated |

**Local means today.** In local mode the federation routes are not registered on the mux at all, the
queue workers are not started, and no key is generated. `TestDBLocalModeUnchanged` (**new**) builds
the route table with no new setting and compares its patterns with a list checked in today, and runs
the start-up with a fetcher that fails the test on any dial.

### 4.3 Moving between modes, and what cannot be undone

- `local` to `single`: allowed when one local account exists (F15). Nothing is announced unless
  `FLOWA_FEDERATE=on`.
- `local` or `single` to `federated`: allowed. The first time the server federates (it answers a
  WebFinger request, or delivers anything), it records the origin and the time in `instance_settings`
  (`origin`, `first_federated_at`).
- **Frozen once federated.** An actor id has been announced to other servers and cannot change.
  So the public origin and every username that has federated are frozen. If `FLOWA_ORIGIN` later
  differs from the recorded origin, the server starts, serves the app as in local mode, logs
  "federation refused: the origin was https://a and is now https://b; actor ids cannot move", shows
  it on the dashboard, and federates nothing. Moving a whole instance to a new name is not supported
  (members move one by one with PRT's Move).
- `federated` back to `local`: allowed, but the remote copies stay where they are. In local mode
  after federating, the actor and WebFinger URLs answer **410 Gone** (the one federation answer local
  mode gives, and only on an instance that recorded `first_federated_at`), so remote servers stop
  retrying. An admin who wants remote servers to forget the accounts first runs "Leave the
  fediverse" in federated mode (**new**, FMD), which sends Delete for each actor and waits for the
  queue to drain.
- Retired usernames stay retired in every mode.

## 5. Identity and discovery

### 5.1 Public origin

`FLOWA_ORIGIN` (**new**): `https://` plus a host and nothing else (no path, no query, no user, no
port unless needed). Without it, or with `http://`, federation is refused at start with a message.
Its host is added to the names `allowedHost` accepts. A test-only setting,
`FLOWA_FEDERATION_ALLOW_PRIVATE` (**new**), lets an `http://` origin and private addresses through
for the two-instance test, as `FLOWA_PREVIEW_BASE` does for the preview stub; the server logs it at
start, and `test/release.sh` fails if `compose.yml` names it.

### 5.2 Ids and URLs

| Thing | Id (also its URL) |
| --- | --- |
| Member | `https://flowa.example.org/users/ana` |
| Member's key | `https://flowa.example.org/users/ana#main-key` |
| Inbox, outbox | `.../users/ana/inbox`, `.../users/ana/outbox` |
| Followers, following, featured | `.../users/ana/followers`, `.../users/ana/following`, `.../users/ana/featured` |
| Post | `https://flowa.example.org/users/ana/posts/812` |
| Comment | `https://flowa.example.org/users/ana/comments/4410` |
| Post picture | `https://flowa.example.org/users/ana/posts/812/picture.jpg` |
| Avatar | `https://flowa.example.org/users/ana/avatar.jpg?v=<hash>` |
| Activity | `https://flowa.example.org/users/ana/activities/<random 16 bytes, base32>` |
| Shared inbox | `https://flowa.example.org/inbox` |
| Instance actor | `https://flowa.example.org/actor` |

Usernames never change (no endpoint renames), and the numeric post and comment ids never repeat
(`BIGSERIAL`), so these stay stable. A deleted member's name goes into `retired_usernames` (**new**)
and cannot be taken again.

### 5.3 WebFinger (RFC 7033)

`GET /.well-known/webfinger?resource=acct:ana@flowa.example.org` (**new**), `Content-Type:
application/jrd+json`:

```json
{
  "subject": "acct:ana@flowa.example.org",
  "aliases": ["https://flowa.example.org/users/ana"],
  "links": [
    {"rel": "self", "type": "application/activity+json", "href": "https://flowa.example.org/users/ana"},
    {"rel": "http://webfinger.net/rel/profile-page", "type": "text/html", "href": "https://flowa.example.org/users/ana"}
  ]
}
```

Edge cases, each a row of `TestWebFinger` (**new**): `acct:` with any case, a leading `@`, the actor
URL as the resource, the host compared with the origin's host case-insensitively and without a
trailing dot, an unknown name, a member who has not opted in (both 404, so the answer does not say
whether an account exists), a retired name (410), the instance actor (`acct:flowa.example.org@flowa.example.org`
resolves to `/actor`, as Mastodon is understood to do; to be checked), a missing or repeated
`resource` (400), a resource over 300 bytes (400). `Access-Control-Allow-Origin: *` is set, as the
RFC recommends for public resources.

**host-meta** (`/.well-known/host-meta`): later, only if a server is found that needs it to find
WebFinger. Mastodon is understood to go to WebFinger directly over HTTPS.

### 5.4 NodeInfo 2.x

`GET /.well-known/nodeinfo` (**new**) links to `/nodeinfo/2.1` (**new**), which answers:

```json
{
  "version": "2.1",
  "software": {"name": "flowa", "version": "v1.4.0", "repository": "https://github.com/TuanSSM/flowtopia"},
  "protocols": ["activitypub"],
  "services": {"inbound": [], "outbound": []},
  "openRegistrations": false,
  "usage": {"users": {"total": 12, "activeMonth": 7, "activeHalfyear": 10}, "localPosts": 340},
  "metadata": {"nodeName": "Flow arts Pi", "mode": "federated"}
}
```

Counts are of members who opted in to federation, not of every account, so a member who keeps to the
server is not counted. `openRegistrations` follows the registration mode. The version string follows
`buildVersion`; an admin can choose to send `"version": ""` (instance setting `nodeinfo_version`,
**new**), since which build runs helps an attacker, the reason `handleHealthz` keeps it private.

### 5.5 The actor document

`GET /users/ana` with `Accept: application/activity+json` or `application/ld+json` answers:

```json
{
  "@context": [
    "https://www.w3.org/ns/activitystreams",
    "https://w3id.org/security/v1",
    {
      "manuallyApprovesFollowers": "as:manuallyApprovesFollowers",
      "toot": "http://joinmastodon.org/ns#",
      "discoverable": "toot:discoverable",
      "featured": {"@id": "toot:featured", "@type": "@id"},
      "PropertyValue": "schema:PropertyValue",
      "value": "schema:value",
      "schema": "http://schema.org#",
      "flowa": "https://github.com/TuanSSM/flowtopia/ns#"
    }
  ],
  "id": "https://flowa.example.org/users/ana",
  "type": "Person",
  "preferredUsername": "ana",
  "name": "Ana",
  "summary": "<p>Poi and staff, mostly in the park.</p>",
  "url": "https://flowa.example.org/users/ana",
  "inbox": "https://flowa.example.org/users/ana/inbox",
  "outbox": "https://flowa.example.org/users/ana/outbox",
  "followers": "https://flowa.example.org/users/ana/followers",
  "following": "https://flowa.example.org/users/ana/following",
  "featured": "https://flowa.example.org/users/ana/featured",
  "endpoints": {"sharedInbox": "https://flowa.example.org/inbox"},
  "manuallyApprovesFollowers": false,
  "discoverable": false,
  "published": "2026-11-02T18:00:00Z",
  "icon": {"type": "Image", "mediaType": "image/jpeg", "url": "https://flowa.example.org/users/ana/avatar.jpg?v=3f9a1c"},
  "attachment": [
    {"type": "PropertyValue", "name": "Props", "value": "Poi, Staff"},
    {"type": "PropertyValue", "name": "Instagram", "value": "<a href=\"https://www.instagram.com/ana.flow\" rel=\"me nofollow noopener\" target=\"_blank\">ana.flow</a>"}
  ],
  "publicKey": {
    "id": "https://flowa.example.org/users/ana#main-key",
    "owner": "https://flowa.example.org/users/ana",
    "publicKeyPem": "-----BEGIN PUBLIC KEY-----\nMIIBIjANBgkqh...\n-----END PUBLIC KEY-----\n"
  }
}
```

Field names that matter for Mastodon and could not be checked against its documentation here:
`preferredUsername`, `inbox`, `outbox`, `publicKey.id`, `publicKey.owner`, `publicKey.publicKeyPem`,
`endpoints.sharedInbox`, `manuallyApprovesFollowers`, `discoverable`, `featured`, the
`PropertyValue` form of profile fields, and the `toot:` namespace IRI. **To verify in FRD's actor
item** against Mastodon's documentation and a recorded Mastodon actor. `discoverable` is false by
default (no directory listing); a member can turn it on. No stats, levels or badges (F12).

The instance actor `/actor` is `"type": "Application"`, `preferredUsername` the host, with its own
key, an inbox and no outbox items. It signs fetches that are not on behalf of a member (key fetches,
fetches under authorized fetch) and receives Flags.

### 5.6 Content negotiation on the same URL

`/users/{name}` and `/users/{name}/posts/{id}` answer JSON when `Accept` names
`application/activity+json` or `application/ld+json` (with or without the profile parameter), and
otherwise a small HTML page the server writes from a Go `html/template` (no script, CSP
`default-src 'none'; img-src 'self'; style-src 'self'`) with a link into the app (`/#/u/ana`,
`/#/post/812`). Every answer carries `Vary: Accept`. JSON answers carry
`Content-Type: application/activity+json` and `Cache-Control: public, max-age=180`, or `private` with
authorized fetch on. `TestContentNegotiation` (**new**) covers each `Accept` form, `*/*`, a browser's
header, and a missing one (HTML).

### 5.7 Keys

- RSA 2048, generated for a member when they opt in and for the instance actor when federation first
  starts, in the background, one at a time (key generation takes about a second or more on the Pi;
  measured in FRD's keys item), so no request waits for it.
- Stored in `ap_keys` (**new**): the public key as PEM, the private key sealed with AES-256-GCM under
  `FLOWA_KEY_SECRET` (F7), with the key id and creation time. The secret never goes into the database,
  the log or a backup.
- Rotation: `/nunchaku -rotate-keys` (**new**, a flag like `-healthcheck`) makes new keys for every
  local actor and sends Update(Person) to followers; remote servers that fail to verify with the old
  key refetch the actor (section 7.4). Also used after a lost secret.
- `alsoKnownAs` and `movedTo`: PRT (Move).

## 6. The protocol surface

### 6.1 Scope

Server to server only. **Client-to-server ActivityPub and the Mastodon client API are out of
scope**: Flowa's own page is its client, the client API is large (hundreds of endpoints, OAuth) and
would be a second interface to secure and keep working, and no member has asked for a third-party
app. Both are listed under "Out of scope" in FRD and FWR.

### 6.2 Endpoints (all **new**)

| Route | Answers |
| --- | --- |
| `GET /.well-known/webfinger` | section 5.3 |
| `GET /.well-known/nodeinfo`, `GET /nodeinfo/2.1` | section 5.4 |
| `GET /users/{name}` | actor or HTML (5.5, 5.6) |
| `GET /actor` | instance actor |
| `POST /users/{name}/inbox`, `POST /inbox`, `POST /actor/inbox` | inboxes (section 7) |
| `GET /users/{name}/outbox` | OrderedCollection of the member's Create activities for Public posts, `totalItems`, `first`, pages of 20 newest first by post id (`?page=true&max_id=812`) |
| `GET /users/{name}/followers`, `/following` | OrderedCollection with `totalItems` only; `first` and pages only for the account itself (F10) |
| `GET /users/{name}/featured` | an empty OrderedCollection at first (pinned posts do not exist yet); cheap and saves a 404 Mastodon would retry |
| `GET /users/{name}/posts/{id}`, `/comments/{id}` | the Note, or HTML; 404 for audience This server, 410 with a Tombstone for a deleted one |
| `GET /users/{name}/posts/{id}/picture.jpg`, `GET /users/{name}/avatar.jpg` | the stored picture, public only for federating members and Public posts |
| `GET /api/fed/...` | the page's own endpoints for follows, search by handle, blocks, Home (section 11), behind `authed` and the limits |

### 6.3 Activities in

The inbox verifies (section 7), stores the activity in `ap_inbox` and answers 202; a worker handles
it. "Actor" below is the verified signer's actor.

| Activity | Accepted when | Effect | Edge cases |
| --- | --- | --- | --- |
| Follow (object: a local actor) | the object is a local federating member; the actor is not blocked by the member or the domain | `follows` row `pending`; Accept sent at once unless the member approves followers by hand, then a follow request notification | a repeat Follow while accepted: Accept sent again (the remote may have lost it); Follow of the instance actor: Reject; Follow of a member who opted out since: Reject |
| Accept (object: our Follow, by id or embedded) | the actor is the target of that Follow | our `follows` row becomes `accepted` | Accept of an unknown or already accepted Follow: ignored; embedded Follow whose actor is not ours: refused |
| Reject (object: our Follow) | as Accept | row deleted, the member told once | Reject after Accept means "removed you as a follower": row deleted |
| Undo (object: Follow, Like, Announce, Block) | the inner activity's actor is the signer | the row it made is removed | Undo of something never seen: ignored, 202; the inner object by id only is matched by (actor, type, object) |
| Create (object: Note) | the Note's `attributedTo` and the activity's actor are the signer, same origin as the Note's id; and either the actor is followed by a local member, or the Note replies to a local post or comment, or mentions a local member | stored as a post (or a comment when `inReplyTo` is a known post or comment) | a Note already stored: ignored; `inReplyTo` unknown and nobody follows: dropped; direct (no Public, no followers collection in `to`/`cc`): dropped (F3); over 2000 characters: cut with a link to the original (section 6.6); Question, Article, Page: kept as text with a link, see 6.7 |
| Update (object: Note) | same origin and author as the stored Note | body, summary, attachments replaced; `updated` kept | Update of an unknown Note: fetched and stored only if it would have been accepted as a Create; Update older than the stored `updated`: ignored |
| Update (object: Person) | object id is the signer | cached actor replaced, key replaced | a different `publicKey` is a rotation: accepted, because the request was verified with the key it replaces or after a refetch |
| Delete (object: Note or Tombstone, by id or embedded) | the object's id has the signer's origin, and the stored Note's author is the signer | post or comment removed with its likes and replies as a local delete does | Delete of an unknown id: ignored, 202, and the id remembered for 7 days so a late Create is refused |
| Delete (object: the actor itself) | the object is the signer | the remote account's posts, comments, likes and follows purged; the row kept as a tombstone so the name shows as "deleted account" | the key cannot be fetched any more (the actor is gone): accepted when the signature verifies against the cached key; refused otherwise |
| Like (object: a local post or comment) | the object is ours and visible to the public | `likes` or `comment_likes` row for the remote account; notification as for a local like | Like of a hidden or This server post: ignored; Like with content (an emoji reaction, as Misskey sends): counted as a like |
| Announce (object: a local post) | the object is ours and Public | a notification "shared your post" (**new** kind `share`); no timeline entry | Announce of someone else's post: dropped in the first cut (Flowa has no boosts); the inner object is never trusted inline |
| Block (object: a local actor) | object is ours | our follows in both directions with that actor removed; the remote account's content hidden from that member | never told to the member (Mastodon does not tell either, as understood) |
| Flag (object: local actors and posts) | the domain does not have "reject reports" | a report in the queue, reporter the remote instance's account, the note as `note` | objects that are not ours are dropped; more than 20 objects: cut to 20 |
| Move (object: the actor, target: a new actor) | PRT: the target's `alsoKnownAs` names the actor | local followers re-follow the target | until PRT ships: ignored |
| Anything else (Add, Remove, Read, View, EmojiReact, ...) | | 202, dropped, counted per type on the dashboard | |

### 6.4 Activities out

| Activity | When | Sent to |
| --- | --- | --- |
| Create(Note) | a federating member posts with audience Public | followers' inboxes (shared inbox per host), plus mentioned remote actors |
| Create(Note) for a comment | a federating member comments on a Public post, local or remote | the commenter's followers, the post's author (and, for a local post, the author's followers), mentioned actors |
| Update(Note) | a comment is edited inside the 15-minute window | as the Create |
| Delete(Tombstone) | a federated post or comment is deleted by its author or by a moderator | everyone it was sent to (recorded in `ap_deliveries`) |
| Update(Person) | profile name, bio, avatar or links change, or keys rotate | followers; at most one in five minutes per member, the newest wins |
| Delete(Person) | a federating account is deleted | followers and every host it ever delivered to (bounded at 2,000 hosts) |
| Follow, Undo(Follow) | a member follows or unfollows a remote account | that actor's inbox |
| Accept, Reject | a follow request is answered (or answered at once for open accounts) | the follower's inbox |
| Like, Undo(Like) | a member likes or unlikes a remote post | the post author's inbox |
| Flag | a moderator chooses to forward a report about remote content | the remote instance's shared inbox, signed by the instance actor, with no reporter named |
| Move | PRT | followers |
| Block | not sent: the block removes follows with Undo and Reject, which says less about the member (F3 follows Flowa's privacy defaults) | |
| Announce | never in the first cut (no boosts) | |

### 6.5 A Flowa post as a Note

A post by `ana` with the body "Finally clean! Thanks @bo@social.example for the drill", a picture,
and one trick chip, nunchaku skill `F7` ("Six basic figure-8s" in `web/catalog/nunchaku.json`):

```json
{
  "@context": ["https://www.w3.org/ns/activitystreams", {"flowa": "https://github.com/TuanSSM/flowtopia/ns#", "sensitive": "as:sensitive", "Hashtag": "as:Hashtag"}],
  "id": "https://flowa.example.org/users/ana/activities/k3m9q2w7x1p4r8t5",
  "type": "Create",
  "actor": "https://flowa.example.org/users/ana",
  "published": "2026-11-12T18:04:05Z",
  "to": ["https://www.w3.org/ns/activitystreams#Public"],
  "cc": ["https://flowa.example.org/users/ana/followers", "https://social.example/users/bo"],
  "object": {
    "id": "https://flowa.example.org/users/ana/posts/812",
    "type": "Note",
    "attributedTo": "https://flowa.example.org/users/ana",
    "url": "https://flowa.example.org/users/ana/posts/812",
    "published": "2026-11-12T18:04:05Z",
    "to": ["https://www.w3.org/ns/activitystreams#Public"],
    "cc": ["https://flowa.example.org/users/ana/followers", "https://social.example/users/bo"],
    "sensitive": false,
    "content": "<p>Finally clean! Thanks <span class=\"h-card\"><a href=\"https://social.example/@bo\" class=\"u-url mention\">@<span>bo</span></a></span> for the drill</p><p>Tricks: Six basic figure-8s (nunchaku) <a href=\"https://flowa.example.org/#/prop/nunchaku\" class=\"mention hashtag\" rel=\"tag\">#<span>nunchaku</span></a></p>",
    "attachment": [
      {"type": "Image", "mediaType": "image/jpeg", "url": "https://flowa.example.org/users/ana/posts/812/picture.jpg", "name": "", "width": 1280, "height": 960}
    ],
    "tag": [
      {"type": "Mention", "href": "https://social.example/users/bo", "name": "@bo@social.example"},
      {"type": "Hashtag", "href": "https://flowa.example.org/#/prop/nunchaku", "name": "#nunchaku"}
    ],
    "flowa:tricks": [{"type": "flowa:Trick", "flowa:prop": "nunchaku", "flowa:skill": "F7", "name": "Six basic figure-8s"}]
  }
}
```

The rules:

- **content** is made from the plain text: `html.EscapeString` (standard library), paragraphs on blank
  lines as `<p>`, line breaks as `<br>`, URLs as `<a href>` with `rel="nofollow noopener"`, mentions
  of local and remote members as the `h-card` form above. Only `p`, `br`, `a` and `span` are written,
  which is a subset of what Mastodon is understood to keep (to verify).
- **The plain-text fallback** for chips and combos is always in `content` ("Tricks: ...", "Combo:
  Name: A, B, C"), so any server shows something sensible; another Flowa reads `flowa:tricks` and
  `flowa:combo` back into chips when the skill exists in its catalog, and shows the text otherwise.
  Skill names come from the latest published catalog (`latestCatalog`), so a renamed skill federates
  under its new name.
- **One Hashtag per prop** in the chips, so a post can be found by `#poi` on servers that index tags.
  The `href` is the prop's skill map. That Mastodon accepts an `href` with a fragment is not checked.
- **summary** is a content note; Flowa has none today, so it is left out. Incoming summaries are
  shown as a fold label.
- **attachment** is the post's picture as a public URL with its size; alt text is `name`, empty until
  the composer gets an alt text field (FWR, posts out: a field, at most 1,500 characters). An empty
  `name` is allowed but makes the picture inaccessible on other servers; the composer asks for it.
- **Link cards** go out as plain links in `content`. Remote servers make their own previews.
- **Milestone posts** do not federate until TRU (F13). **Hidden posts** are not served and a hide
  sends Delete; a restore does not send Create again (a moderator's restore is local).
- **Comments** are Notes with `inReplyTo` set to the post's id, or to the parent comment's id for a
  reply. Remote replies deeper than Flowa's one level join the thread of the top comment, starting with
  a mention of the person answered, as RPL does for a reply to a reply.

`TestNoteFromPost` (**new**) holds the Note for a fixture post byte for byte against a golden file, and
`TestNoteContentEscaping` (**new**) feeds hostile text (`<script>`, quotes, `javascript:` URLs,
bidirectional overrides) through it.

### 6.6 Remote content in

Remote HTML is **never stored or rendered as HTML.** It is turned into text by a small converter
(**new**, `htmlToText`): a state machine over the bytes that drops every tag, turns `<br>`, `</p>` and
`</li>` into line breaks, keeps the `href` of `<a>` as a URL in the text where the link text was not
already the URL, and decodes entities with `html.UnescapeString`. The page then renders it with
`richText`, which escapes everything and links URLs and mentions, exactly as for a local post. No HTML
sanitiser is needed, and none is added: `golang.org/x/net/html` would replace about 80 lines with a
module, which is not less code. `TestRemoteHTMLToText` (**new**) runs it over a corpus of hostile
inputs (unclosed tags, attributes with `>` inside quotes, comments, CDATA, `<script>` and `<style>`
contents dropped, 10,000 nested tags, invalid UTF-8) and over the recorded fixtures.

| Remote thing | What Flowa does |
| --- | --- |
| Text over 2000 characters | the first 2000 characters, then "Read the rest on its server" with the URL |
| `summary` (content warning) | the post folded behind the summary, as a button |
| `sensitive` pictures | blurred until tapped |
| Image attachments (JPEG, PNG, GIF first frame) | proxied and shrunk on first view (F6); at most 4 shown |
| WebP, AVIF, video, audio | a link "picture (or video) on the original server": the standard library decodes neither WebP nor AVIF, and video is out of scope ("Video hosting" is not planned) |
| Question (poll) | the question and options as text, "This is a poll; vote on its server" |
| Custom emoji (`Emoji` tags) | the `:shortcode:` left as text |
| Article, Page | the `name` as a title, the summary or the first 500 characters, and the link |
| Announce of others | dropped (6.3) |
| Unknown properties | ignored |

### 6.7 Addressing

The public collection is accepted in three forms: `https://www.w3.org/ns/activitystreams#Public`,
`as:Public` and `Public`, and sent only in the first. That Mastodon sends only the first form and
accepts the others is not checked here; the three are what the ActivityStreams context makes
equivalent. Incoming visibility is read as: Public in `to` is public; Public in `cc` only is
unlisted (shown in Home, like public); the actor's followers collection without Public is
followers-only (F3); anything else is direct and dropped.

## 7. Security

### 7.1 HTTP signatures

- **Sent:** draft-cavage-http-signatures-12 with `algorithm="rsa-sha256"`,
  `headers="(request-target) host date digest"` on POST and `"(request-target) host date"` on GET,
  `Digest: SHA-256=<base64 of the body's SHA-256>`, and `keyId` the actor's key id.
- **Accepted:** the same, with `hs2019` as the algorithm name too (which the draft defines as "derive
  from the key"), and RFC 9421 `Signature-Input` and `Signature` with `rsa-v1_5-sha256` and
  `Content-Digest`, covering at least `@method`, `@target-uri` (or `@authority` and `@path`) and,
  on POST, `content-digest`. The exact component set Mastodon sends with RFC 9421 could not be
  checked; **FRD's signatures item verifies it** against Mastodon's documentation and a recorded
  request.
- **Which form is sent to a host:** cavage by default. A host that has sent Flowa a valid RFC 9421
  signature is marked in `ap_hosts.sig` (**new**) and is sent RFC 9421 from then on; a 401 from such a
  host makes the next attempt use cavage and clears the mark. No double request on every delivery.
- A request is refused (401, with a short reason in the log, never the header) when: the signature
  does not cover `host`, `date` (or `created`) and, on POST, the digest; the digest does not match the
  body; the date is more than 12 hours old or 1 hour ahead (the window Mastodon is understood to use;
  to be checked); the key cannot be fetched; the signature does not verify after one refetch of the
  key.

Example of a delivery Flowa sends:

```http
POST /users/bo/inbox HTTP/1.1
Host: social.example
Date: Thu, 12 Nov 2026 18:04:06 GMT
Content-Type: application/activity+json
Digest: SHA-256=X48E9qOokqqrvdts8nOJRJN3OWDUoyWxBf7kbu9DBPE=
User-Agent: Flowa/v1.4.0 (+https://flowa.example.org/)
Signature: keyId="https://flowa.example.org/users/ana#main-key",algorithm="rsa-sha256",headers="(request-target) host date digest",signature="kO3...8w=="
```

The verifier uses the host of `FLOWA_ORIGIN` for `host`, and the path and query as received for
`(request-target)` (finding 4). `TestSignatureVectors` (**new**) checks signing and verifying against
the test keys and examples published in draft-cavage-12 and RFC 9421 Appendix B, copied into
`testdata/federation/vectors/` (**new**) with the section each came from, once the texts can be
opened.

### 7.2 Replay

- An activity id is processed once: `ap_inbox.activity_id` is unique; processed rows lose their body
  at once and are kept 7 days.
- A signature is accepted once: the SHA-256 of the `Signature` header is kept in memory for the 13
  hours a date can be valid, in a map of at most 100,000 entries (about 6 MB); when full, the oldest
  half is dropped, and a request older than the oldest kept is refused as stale.

### 7.3 Origin checks

- The key's `owner` must be the activity's `actor`, and the key id must be on the actor's host.
- The object's id, when the object is embedded, must have the actor's origin, except: Accept and
  Reject (the object is our Follow, on our origin), Undo (the object is the actor's own activity),
  Like, Announce, Flag and Block (the object is someone else's, by id, never trusted inline), and
  Create of a Note whose id has the actor's origin. An embedded object of another origin is fetched
  from its own origin by id instead, and that copy is used.
- Linked Data signatures (for activities forwarded by a third server) are not verified: a forwarded
  activity whose signer is not its actor is answered 202 and its object fetched from its origin by id.
- **Remote ids never decide local authority.** Who may delete or edit a post is decided by the stored
  author's `users.id` after the signer is mapped to it, never by comparing strings a remote document
  supplied. Only the origin may delete or update its objects.

### 7.4 Keys of remote actors

- Fetched with a signed GET by the instance actor (so authorized-fetch servers answer), through the
  guarded fetcher, cached in `ap_actors` with the document.
- A failed verification triggers one refetch of the key, at most once per actor per 10 minutes; a
  verification that then succeeds is a rotation. A failed fetch is cached negatively for 10 minutes,
  and a 410 marks the actor gone.
- At most 2 key fetches at once; an inbox request that has to wait longer than 5 s for one is answered
  503 with `Retry-After: 60`, and the sender retries.
- An in-memory cache of parsed public keys (at most 1,000, least recently used out) saves parsing PEM
  on each request.

### 7.5 Limits on what comes in

| Limit | Value |
| --- | --- |
| Inbox body | 256 KB (`http.MaxBytesReader`), 413 above |
| JSON depth | 24 levels, checked by a token walk before decoding, 400 above |
| Array length | 1,000 elements in `to`, `cc`, `tag`, `attachment`; longer is 400 |
| A string field | 64 KB; `content` is cut to 64 KB before conversion |
| Fetched documents (actors, objects) | 1 MB, `application/activity+json` or `application/ld+json` only |
| Fetched pictures | 8 MB in, 4096 px a side checked with `image.DecodeConfig` before decoding, as `shrinkThumb` does |
| Inbox requests per remote host | 300 in 5 minutes in a `limiter` keyed by host, 429 above |
| Inbox handling at once | 4, 8 more may wait 2 s, then 503 with `Retry-After: 30` |
| Inbound queue | 5,000 unprocessed rows; above it the inbox answers 503 with `Retry-After: 120` |

### 7.6 Authorized fetch

An admin option (F11) in `instance_settings`. When on, every federation GET except WebFinger,
NodeInfo, the instance actor and the public key part of an actor needs a valid signature, and the
signer's domain must not be blocked; answers are `Cache-Control: private`. `TestDBAuthorizedFetch`
(**new**).

### 7.7 Blocking a server that floods

A host that is refused by its allowance 5 times in an hour is blocked for an hour (`ap_hosts.until`),
and the dashboard says so. An admin's domain block is permanent until removed. Requests from a blocked
host are answered 403 before the body is read.

### 7.8 One way out

Every outbound request goes through one fetcher (`fetcher.do`, **new**, generalising `fetcher.get`):

- The dial guard of `newFetcher` stays as it is: a connection to a loopback, private, link-local or
  reserved address is refused at connect time, after DNS, so a name that resolves to `10.0.0.1` is
  refused. `TestFetcherRefusals` keeps passing unchanged.
- Hosts: the preview allowlist for link cards as today; for federation any host the domain policy
  allows (section 10). Only `https`.
- Redirects: at most 3, each re-checked against the scheme, the policy and the dial guard; a redirect
  to another origin is followed only for media, never for an actor or an object (whose id must match
  where it was fetched from).
- Time: 5 s for a document or a delivery (the delivery is retried later), 10 s for a picture.
- Size: as in 7.5. Content types: as in 7.5, others refused before reading the body.
- `TestFederationFetcherRefusals` (**new**) adds: a redirect to a private address, to `http`, to a
  blocked domain, a body over the cap, a slow body, a wrong content type, an id that does not match
  the fetched URL.

### 7.9 What is logged

The request log as today (route pattern, status, time). Refusals name the host and the reason
("signature: date too old", "digest mismatch", "domain blocked"). Never a body, a key, a signature, a
digest, a cookie or a member's content. Delivery failures name the host, the status and the attempt.

## 8. Delivery and work queues

### 8.1 Two queues in Postgres

- **Outbound:** an activity is written once to `ap_outbox`; one row per (activity, inbox) goes to
  `ap_deliveries`, with followers on the same host collapsed into that host's shared inbox.
- **Inbound:** a verified activity goes to `ap_inbox`, so a slow handler never holds the sender's
  connection.

No broker: one Go process, a few goroutines, rows claimed with `FOR UPDATE SKIP LOCKED` and a lease,
so two processes during a deploy cannot both send a row, and a crash leaves the row to be claimed
again when its lease ends.

```sql
-- claim the next row a worker may send (per-host concurrency is kept in memory; $1 hosts at their limit)
UPDATE ap_deliveries d SET locked_until = now() + interval '60 seconds', attempts = d.attempts + 1
WHERE d.id = (
  SELECT id FROM ap_deliveries
  WHERE state = 'queued' AND next_at <= now() AND (locked_until IS NULL OR locked_until < now())
    AND NOT (host = ANY($1))
  ORDER BY next_at LIMIT 1 FOR UPDATE SKIP LOCKED)
RETURNING d.id, d.outbox_id, d.inbox, d.host, d.attempts;
```

### 8.2 Workers

- `FLOWA_FEDERATION_WORKERS` (**new**, default 4, 1 to 8): goroutines that claim and send.
- Per host: 1 request at a time.
- Global: at most 10 requests a second out, and at most 3 database connections held by federation at
  once (a semaphore), leaving 5 of the pool's 8 for pages.
- Idle workers wait on a channel signalled when a row is written, and poll every 5 s otherwise; no
  busy loop.

### 8.3 Retries

Attempts after the first: 1 min, 5 min, 15 min, 30 min, 1 h, 2 h, 4 h, 8 h, 12 h, 12 h; each with
random jitter of plus or minus 20%. That is 11 attempts over about 40 hours; a row whose first attempt
is more than 48 hours old (F9) is given up as `failed`. A 4xx answer other than 408 and 429 gives up at
once (the remote refused it; retrying will not help); 410 for an inbox removes that follower. A 429
uses its `Retry-After` when it is between 1 minute and 12 hours.

### 8.4 Hosts

`ap_hosts` holds per host: consecutive failures, last success, last failure, state.

- **Circuit breaker:** 5 consecutive failures within 10 minutes open the circuit for 10 minutes; rows
  for that host are not claimed; one row is tried when it closes.
- **Unavailable:** no success for 7 days marks the host unavailable; its rows are failed at once and
  new ones are not written; one probe a day (a signed GET of its NodeInfo) or any valid inbound
  activity from it marks it available again.

### 8.5 Back-pressure and retention

- At most 50,000 queued rows in all and 2,000 per host. Above the per-host cap, new rows for that
  host are not written and counted as dropped on the dashboard; above the global cap, fan-out of new
  activities waits in `ap_outbox` (one row each) until the queue is under 40,000.
- Delivered rows are deleted at once (the host's counters keep the history). Failed rows are kept 7
  days for the dashboard, then deleted by `housekeep`. `ap_outbox` rows go when they have no delivery
  left and are older than 7 days.

### 8.6 Shutdown

On SIGTERM, workers stop claiming; a request in flight has the 5 s `srv.Shutdown` already allows;
whatever does not finish keeps its lease and is sent again after it ends. Receivers dedupe by activity
id, so a second send is harmless. `TestDeliveryShutdown` (**new**).

### 8.7 The queue and `./deploy.sh --rollback`

The tables are additive. An older build without the workers leaves them untouched; going forward
again resumes the queue, and rows past 48 hours are given up at the first claim. Nothing is lost that
a 48-hour outage would not lose anyway.

## 9. Data model

### 9.1 Tables and columns (all **new**; one migration per pull request that needs it, appended, additive)

```sql
-- instance settings that change at runtime, and the recorded identity
CREATE TABLE instance_settings (
  key TEXT PRIMARY KEY,               -- origin, first_federated_at, policy, authorized_fetch, registration, rules, description, contact, retention_days, nodeinfo_version
  value JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by BIGINT REFERENCES users(id) ON DELETE SET NULL
);

-- remote accounts are users rows (F5); '' is local
ALTER TABLE users ADD COLUMN IF NOT EXISTS domain TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS federates BOOLEAN NOT NULL DEFAULT false;   -- the member's opt-in (F2)
ALTER TABLE users ADD COLUMN IF NOT EXISTS locked BOOLEAN NOT NULL DEFAULT false;      -- approves followers by hand
CREATE INDEX IF NOT EXISTS users_local ON users (id) WHERE domain = '';

CREATE TABLE retired_usernames (username TEXT PRIMARY KEY, retired_at TIMESTAMPTZ NOT NULL DEFAULT now());

-- the cached document and key of a remote actor
CREATE TABLE ap_actors (
  user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  ap_id TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL,                  -- Person, Service, Application, Group
  inbox TEXT NOT NULL,
  shared_inbox TEXT NOT NULL DEFAULT '',
  url TEXT NOT NULL DEFAULT '',
  key_id TEXT NOT NULL,
  public_key TEXT NOT NULL,            -- PEM
  followers_url TEXT NOT NULL DEFAULT '',
  moved_to TEXT NOT NULL DEFAULT '',
  gone BOOLEAN NOT NULL DEFAULT false,
  fetched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL      -- fetched_at + 24 hours
);
CREATE INDEX ap_actors_key ON ap_actors (key_id);

-- key pairs of local actors; user_id NULL is the instance actor
CREATE TABLE ap_keys (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  public_key TEXT NOT NULL,
  private_sealed BYTEA NOT NULL,       -- AES-256-GCM under FLOWA_KEY_SECRET (F7)
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX ap_keys_instance ON ap_keys ((user_id IS NULL)) WHERE user_id IS NULL;

-- follows in both directions, local and remote
CREATE TABLE follows (
  follower_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  followee_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  state TEXT NOT NULL CHECK (state IN ('pending', 'accepted')),
  ap_id TEXT NOT NULL DEFAULT '',       -- the Follow activity's id, for Accept and Undo
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (follower_id, followee_id)
);
CREATE INDEX follows_followee ON follows (followee_id, state);

-- member-level blocks, of accounts and of domains
CREATE TABLE blocks (user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE, target_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY (user_id, target_id));
CREATE TABLE domain_blocks_member (user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE, domain TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY (user_id, domain));

-- remote posts and comments are rows of posts and comments (F5)
ALTER TABLE posts ADD COLUMN IF NOT EXISTS ap_id TEXT;          -- NULL for local
ALTER TABLE posts ADD COLUMN IF NOT EXISTS audience TEXT NOT NULL DEFAULT 'local';  -- local, public, unlisted, followers
ALTER TABLE posts ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ;
CREATE UNIQUE INDEX posts_ap_id ON posts (ap_id) WHERE ap_id IS NOT NULL;
CREATE INDEX posts_local ON posts (id DESC) WHERE ap_id IS NULL;
ALTER TABLE comments ADD COLUMN IF NOT EXISTS ap_id TEXT;
CREATE UNIQUE INDEX comments_ap_id ON comments (ap_id) WHERE ap_id IS NOT NULL;

-- outbound activities and their deliveries
CREATE TABLE ap_outbox (
  id BIGSERIAL PRIMARY KEY,
  ap_id TEXT NOT NULL UNIQUE,
  actor_id BIGINT REFERENCES users(id) ON DELETE CASCADE,   -- NULL: the instance actor
  body JSONB NOT NULL,                                      -- signed at send time, not stored signed
  fanned_out BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE ap_deliveries (
  id BIGSERIAL PRIMARY KEY,
  outbox_id BIGINT NOT NULL REFERENCES ap_outbox(id) ON DELETE CASCADE,
  inbox TEXT NOT NULL,
  host TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'failed')),
  attempts INT NOT NULL DEFAULT 0,
  next_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  locked_until TIMESTAMPTZ,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (outbox_id, inbox)
);
CREATE INDEX ap_deliveries_due ON ap_deliveries (next_at) WHERE state = 'queued';
CREATE INDEX ap_deliveries_host ON ap_deliveries (host, state);

-- inbound activities: the queue and the record of ids seen
CREATE TABLE ap_inbox (
  id BIGSERIAL PRIMARY KEY,
  activity_id TEXT NOT NULL UNIQUE,
  actor_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  body JSONB,                          -- NULL once processed
  state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'done', 'failed')),
  attempts INT NOT NULL DEFAULT 0,
  received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ap_inbox_queued ON ap_inbox (id) WHERE state = 'queued';

-- one row per remote host: health, circuit, signature form, flood block
CREATE TABLE ap_hosts (
  host TEXT PRIMARY KEY,
  state TEXT NOT NULL DEFAULT 'ok' CHECK (state IN ('ok', 'open', 'unavailable')),
  failures INT NOT NULL DEFAULT 0,
  last_success TIMESTAMPTZ,
  last_failure TIMESTAMPTZ,
  until TIMESTAMPTZ,                   -- circuit open or flood block until
  sig TEXT NOT NULL DEFAULT 'cavage',  -- or 'rfc9421' (section 7.1)
  software TEXT NOT NULL DEFAULT '',
  delivered BIGINT NOT NULL DEFAULT 0,
  dropped BIGINT NOT NULL DEFAULT 0
);

-- the admin's policy per domain
CREATE TABLE domain_policies (
  domain TEXT PRIMARY KEY,
  policy TEXT NOT NULL CHECK (policy IN ('allow', 'limit', 'suspend')),
  reject_media BOOLEAN NOT NULL DEFAULT false,
  reject_reports BOOLEAN NOT NULL DEFAULT false,
  public_comment TEXT NOT NULL DEFAULT '',
  private_note TEXT NOT NULL DEFAULT '',
  created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- the picture cache's index; the bytes are files under -data/media
CREATE TABLE ap_media (
  url_hash TEXT PRIMARY KEY,            -- hex SHA-256 of the remote URL
  url TEXT NOT NULL,
  content_type TEXT NOT NULL,
  bytes INT NOT NULL,
  width INT NOT NULL DEFAULT 0,
  height INT NOT NULL DEFAULT 0,
  fetched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  used_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ap_media_used ON ap_media (used_at);

ALTER TABLE reports ADD COLUMN IF NOT EXISTS forwarded_at TIMESTAMPTZ;
```

Every column added to an existing table has a constant default, so Postgres adds it without
rewriting the table, and an older build that does not name it is unaffected.

### 9.2 Why remote content joins the existing tables (F5)

Remote accounts become `users` rows with `domain` set, `username` of the form `bo@social.example`
(which `usernameRE` can never produce, so no local name can collide), `pw_hash` `!` (which
`checkPassword` can never match), role `user`, never an owner. Remote posts and comments are rows of
`posts` and `comments` with `ap_id` set.

For it: `likes`, `comment_likes`, `comments`, `notifications`, `reports`, `bookmarks`, `mod_actions`,
`targetOwner`, `handleModAction` (mute and suspend become "limit" and "suspend" of a remote account),
`usersByID` and the page's rendering of a post all work unchanged. With separate tables each of those
would need a remote twin, which is the "built twice" this plan avoids.

Against it, and what holds each:

- Every query that lists members must say local only: `peopleSQL`, `dashTotalsSQL`, `dashActiveSQL`,
  `handleUsers`, `seedAdmin`, `ensureOwner`, `handleSetRole`, `handleSetOwner`, the sign-up name check.
  `TestDBRemoteRowsStayRemote` (**new**) inserts remote rows and checks each of those surfaces.
- `feedSQL` (the Community timeline) gains `AND p.ap_id IS NULL`, served by `posts_local`. Home is a
  new query (below).
- The plans: `TestDBQueryPlans` is extended with 2,000 remote accounts, 20,000 remote posts and
  20,000 follows, and holds the people list, the Community timeline, Home, a profile and the
  dashboard to index scans or to the row counts it already allows.
- An older build after a rollback shows remote rows as local (section 14.4).

Home:

```sql
SELECT ... FROM posts p
WHERE (p.user_id = $1 OR p.user_id IN (SELECT followee_id FROM follows WHERE follower_id = $1 AND state = 'accepted'))
  AND (p.audience <> 'followers' OR EXISTS (SELECT 1 FROM follows f WHERE f.follower_id = $1 AND f.followee_id = p.user_id AND f.state = 'accepted'))
  AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id = $1 AND b.target_id = p.user_id)
  AND ($2 = 0 OR p.id < $2) AND p.hidden_at IS NULL
ORDER BY p.id DESC LIMIT 21
```

served by `posts_user (user_id, id DESC)` per followed account; with up to 500 followed accounts
this is a merge of index scans the plan test holds.

### 9.3 Retention

| What | Kept |
| --- | --- |
| Remote posts nobody local liked, commented, bookmarked or reported | 30 days (instance setting `retention_days`, 7 to 365) |
| Remote posts someone interacted with | while the interaction exists |
| Remote accounts nobody follows, with no content left | 30 days after their last content |
| Cached actor documents | refreshed after 24 hours on use |
| Pictures in the cache | until the cache passes 200 MB, then least recently used first, down to 160 MB |
| `ap_inbox` rows | 7 days |
| Failed deliveries | 7 days |

`housekeep` gains these steps, each counted in its log line, and `TestDBHousekeeping` gains a case
for each.

### 9.4 Pictures (F6)

Remote pictures are fetched on first view by `GET /api/fed/media/{hash}` (**new**, needs a session,
like `handlePreviewImage`), shrunk to 1280 px and 200 KB JPEG (avatars 256 px and 40 KB) with the
code of `shrinkThumb`, written under `-data/media/` (the only writable path in the read-only
container), and indexed in `ap_media`. The browser only ever loads from Flowa, so `img-src 'self'
data: blob:` stays as it is and no visitor's address reaches another server. Files are not in the
daily `pg_dump` backup, which keeps backups small; losing them costs only a refetch. A domain with
`reject_media` has its pictures shown as links.

## 10. Administration, with Mastodon as the reference

Mastodon's admin features read as a checklist. Adopt: built as Mastodon has it. Adapt: built in
Flowa's own shape. Later: wanted, not in these epics. Never: with the reason.

| Mastodon feature | Decision | Why, and where it lives in Flowa |
| --- | --- | --- |
| Registration open, closed | adopt | the `signups` flag already; becomes the `registration` instance setting with the flag kept as its switch (INS) |
| Registration with approval | adopt | new accounts wait in Accounts for an admin; the sign-up page says so (INS) |
| Invites | later | useful for a household; not needed to federate |
| Server rules shown at sign-up | adopt | instance setting `rules`, shown on sign-up and on the about page; edited in Admin, audited (INS) |
| Instance description, contact, about page | adopt | instance settings, the NodeInfo `nodeName`, an about page for visitors (INS) |
| Announcements | adapt | one admin notice shown at the top of the timeline, a notification kind `announcement` (**new**) (FMD) |
| Federation modes: open, allowlist, off | adopt | instance setting `policy` (F8); off is the `federation` flag or `FLOWA_MODE=local` (FMD) |
| Domain blocks: limit (silence) | adopt | `domain_policies.policy = 'limit'`: their content is shown only to members who follow the account; not in search (FMD) |
| Domain blocks: suspend | adopt | `suspend`: nothing in or out, existing content and follows purged (FMD) |
| Reject media, reject reports | adopt | the two columns (FMD) |
| Domain allows | adopt | `policy = 'allow'`, the list in allowlist mode (FMD) |
| Public and private comment on a block | adopt | the two text columns; the public one on the about page (FMD) |
| Import and export of blocklists | later | CSV in the same format as Mastodon's when an operator asks |
| Remote account: limit | adapt | `mute` in `handleModAction` on a remote row means limit (FMD) |
| Remote account: suspend | adapt | `suspend` on a remote row: their content hidden, their follows removed, nothing delivered to them (FMD) |
| Remote account: delete data | adopt | a new action `purge_remote` (**new**) (FMD) |
| Reports about remote content | adopt | the existing reports and queue; remote targets are local rows (FMD) |
| Forwarding a report (Flag) | adopt | a checkbox on resolve; sent by the instance actor without the reporter's name (FMD) |
| Receiving reports | adopt | Flag in, as a report from the remote instance (FMD) |
| Authorized fetch | adopt | instance setting, off by default (F11) (FMD) |
| Content retention for remote posts and media | adopt | instance setting `retention_days`, the 200 MB cache (FMD) |
| Custom emoji | never | pictures from every server for decoration; a Pi's disk and a moderation surface for little |
| Relays | never | a relay floods a small instance with posts nobody follows |
| Trends, explore, public federated timeline | never | Flowa is not a discovery service; the Home timeline is what a member chose |
| Hashtag following | later | once there are hashtags beyond props |
| User blocks and mutes of accounts | adopt blocks, later mutes | blocks are needed for safety at once (INS for local, FWR for remote); mutes are a convenience |
| User blocks of domains | adopt | `domain_blocks_member` (FMD) |
| Follow requests for locked accounts | adopt | `users.locked`, `manuallyApprovesFollowers` (FWR) |
| Account migration (Move), alsoKnownAs | adopt | PRT |
| Account export (posts, follows, blocks) | adopt | PRT; useful in local mode too |
| Full-text search of remote content | never | the brief says handle only; indexing remote text costs disk and is a privacy question |
| Email notifications, password reset by email | never | "Not planned" in the roadmap: no email is stored |
| Direct messages | never | "Not planned" in the roadmap |
| Polls | later | shown as text (6.6) |
| Admin roles with custom permissions | adapt | the existing three roles; two new permissions in `rolePerms`: `federation.read` (moderators and admins) and `federation.manage` (admins) |
| Audit log | adapt | every federation admin action through `audit` into `mod_actions` |
| Sidekiq dashboard | adapt | the federation tile and page on the admin dashboard (section 14.2) |
| IP blocks, email domain blocks | never | no email; IP blocks belong to the operator's proxy |

## 11. The member's side

- **Opting in** (F2): a switch in Settings, "Share my profile and public posts beyond this server",
  with a sentence on what goes out (profile, Public posts, comments on Public posts, likes of remote
  posts) and what never does (section 14.5). Turning it off stops all delivery and makes the actor
  and its posts answer 404 to other servers; it sends nothing. Asking other servers to forget the
  account is a separate button, "Remove me from other servers", which sends Delete(Person); the name
  stays the member's, and turning the switch on again later makes new keys. A server that processed
  the Delete may refuse the same address for good (believed of Mastodon, not checked), and the page
  says so before the button sends anything.
- **Audience per post:** Public or This server, defaulting to Public for members who opted in.
- **Following:** a Follow button on profiles (local and remote); "people elsewhere": a search box
  that takes a handle (`@bo@social.example`) or a profile URL only, resolves it by WebFinger, and
  shows the profile with its public posts from the outbox's first page. No full-text search.
- **Follow requests:** a member can approve followers by hand (`users.locked`); requests come as
  notifications with Approve and Decline.
- **Home timeline** beside Community (F4). Remote posts show the handle (`@bo@social.example`), the
  server's name, and "Open on its server".
- **Replies and likes:** remote replies appear under a local post like local ones, with the handle;
  remote likes count with local ones; notifications for follows, follow requests, mentions, replies,
  likes and shares.
- **Blocks:** a member blocks an account or a domain; blocked content disappears for them, and the
  blocked account cannot follow, reply or like (refused in the inbox).
- **The handle on the profile:** `@ana@flowa.example.org`, with a copy button, when the member
  federates.
- **Privacy defaults:** opt-in off, `discoverable` off, followers and following lists hidden,
  approve-by-hand off, audience Public only after opting in.
- **A signed-out visitor from another server** who opens `https://flowa.example.org/users/ana/posts/812`
  in a browser sees the post (text, picture, chips as text, the author) in a small page with "Open in
  Flowa"; for a This server post or a non-federating member, a 404 page. In local mode, as today.

## 12. Budgets

Measured on the Pi 4 (4 GB) where it says Pi, on the CI runner otherwise; a benchmark's threshold is
the budget, and the first measured value is written into the pull request that adds it.

| Budget | Value | Held by |
| --- | --- | --- |
| Extra memory with federation on, at peak | 32 MB of Go heap with 50,000 queued rows, 4 workers sending and the inbox at its concurrency | `TestFederationMemory` (**new**): `runtime.ReadMemStats` after the load, `HeapInuse` growth under the budget |
| Container memory | `mem_limit: 384m` unchanged | the `federation` CI suite reads `docker stats` after its scenario and fails above 300 MB |
| Inbox request, key cached | under 2 ms CPU on the runner, under 10 ms on the Pi; at most 400 allocations | `BenchmarkInboxVerify` (**new**, `-benchmem`) |
| Inbox request, key not cached | under 5 s wall, bounded by the fetch timeout | `TestInboxRefusals` (**new**) with a slow stub |
| Signing one delivery | under 15 ms on the Pi | `BenchmarkSignDelivery` (**new**) |
| Claiming a row | under 2 ms with 50,000 queued rows | `BenchmarkClaimDelivery` (**new**, database) |
| Deliveries out | at most 10 a second, 1 at a time per host, 4 workers | `TestDBDeliveryQueue` (**new**) with a stub that counts concurrent requests |
| Database connections held by federation | at most 3 of 8 | `TestDBDeliveryQueue` reads `db.Stats().InUse` during a burst |
| Queue rows | 50,000 in all, 2,000 per host | `TestDBDeliveryBackPressure` (**new**) |
| Inbound queue | 5,000 rows; 503 above | `TestInboxRefusals` |
| Remote actors cached | 20,000 rows, about 2 KB each | `TestDBRemoteActorCache` (**new**) |
| Picture cache | 200 MB on disk | `TestMediaCacheBudget` (**new**) with a 2 MB budget |
| Replay memory | 100,000 signatures, about 6 MB | `TestReplayWindow` (**new**) |
| What a small instance serves within these | 1,000 remote followers on 300 hosts: a post fans out in about 30 s; 20 verified inbound activities a second | measured once on the Pi in FWR's queue item and written into the todo |

Nothing a remote server controls is unbounded: bodies, depth, arrays, fetch sizes and times,
redirects, rows per host, rows in all, key fetches, retries and their horizon all have a number above.

## 13. How interoperability is shown without a live Mastodon

This sandbox cannot reach other fediverse servers, and could not open the specifications either
(see Sources). Interoperability is shown in four ways, in rising order of trust, and the last is the
owner's:

1. **Specification vectors.** The signature test keys and examples from draft-cavage-12 and RFC 9421
   Appendix B, in `testdata/federation/vectors/`, checked by `TestSignatureVectors`. Copied from the
   texts when the item is built where they can be opened.
2. **Recorded fixtures.** Real actor documents, WebFinger answers, NodeInfo and activities from
   Mastodon, GoToSocial, Pixelfed, Misskey and Akkoma, recorded by `test/federation-record.sh`
   (**new**: fetches with curl, strips nothing but `Date` headers, writes
   `testdata/federation/<software>/<what>.json` with where and when it was recorded), run by the owner
   or by a session that can reach those servers. The pull request that adds them says which were
   recorded and which were written from documentation examples. `TestFixturesParse` (**new**) reads
   every fixture through the inbox's parser and the actor cache.
3. **Two Flowa instances.** In Go, `TestTwoInstances` (**new**) starts two apps on `httptest` TLS
   servers with their own databases and runs follow, accept, post, like, reply, edit, delete, block,
   unfollow and account deletion between them. In CI, the `federation` suite (**new**,
   `test/federation.sh`, in `ALL` in `test/ci-plan.sh` and its self-test) runs two containers from
   the built image on a Docker network with `FLOWA_FEDERATION_ALLOW_PRIVATE`, drives them through
   their APIs, and checks each step arrived.
4. **A real server, by the owner.** FWR's last item: the owner's stage instance against a Mastodon
   account, following the checklist in that item, with the results written into the todo. Federated
   mode stays marked experimental (in the Admin page and the README) until it passes.

## 14. Rollout, order of pull requests and rollback

### 14.1 Behind the mode and the flags

Everything sits behind `FLOWA_MODE` (default `local`) first, and the flags `federation` and `follows`
second, both **new** and both **off by default**, unlike the existing flags, so that adding them
changes no running instance.

### 14.2 What `/healthz` and the dashboard show

- `/healthz` for a local probe gains `federation`: `{"mode": "federated", "queued": 312,
  "oldestQueuedSeconds": 41, "failingHosts": 2}` (**new** fields), only when the mode federates. The
  public answer stays `{"ok": true}`. An unhealthy queue does not make the instance unhealthy: a full
  queue is a warning, not a reason for `deploy.sh` to roll back.
- The admin dashboard gains a federation tile (queued, oldest undelivered, delivered and failed in 24
  hours, hosts open or unavailable) and a page per host with a retry button and the last errors.

### 14.3 Order of pull requests

| # | Title | Epic item | Notes |
| --- | --- | --- | --- |
| 1 | `docs(server): design and plan for activitypub federation` | INS 1 | this page |
| 2 | `feat(server): an instance mode setting that changes nothing by default` | INS 2 | `FLOWA_MODE`, `instance_settings`, schema 7 fixture, `TestDBLocalModeUnchanged` |
| 3 | `feat(server): member blocks` | INS 3 | useful locally, needed for federation |
| 4 | `feat(server): follows and a home timeline behind a flag` | INS 4 | `follows`, the `follows` flag off |
| 5 | `feat(server): instance information, rules and sign-up approval` | INS 5, 6 | may be two pull requests |
| 6 | `feat(server): single-profile mode` | INS 7 | closed sign-up, root page, public profile page |
| 7 | `feat(server): public origin, key secret and keys` | FRD 1 | refuses without them |
| 8 | `refactor(server): one guarded fetcher for previews and federation` | FRD 2 | `TestFetcherRefusals` unchanged |
| 9 | `feat(server): http signatures with the specification vectors` | FRD 3 | sign and verify, both forms |
| 10 | `feat(server): webfinger and nodeinfo` | FRD 4 | |
| 11 | `feat(server): actor documents and public pages` | FRD 5 | content negotiation, picture URLs |
| 12 | `feat(server): outbox and collections` | FRD 6 | |
| 13 | `feat(server): remote actors by signed fetch, and search by handle` | FRD 7, 8 | fixtures land here |
| 14 | `feat(server): the inbox and its queue` | FWR 1 | guard exemption, limits |
| 15 | `feat(server): a delivery queue in postgres` | FWR 2 | workers, backoff, hosts |
| 16 | `feat(server): follows across servers` | FWR 3 | |
| 17 | `feat(server): public posts go out as notes` | FWR 4 | alt text field |
| 18 | `feat(server): posts from followed accounts in the home timeline` | FWR 5 | picture cache, `htmlToText`, mention fix |
| 19 | `feat(server): replies and likes across servers` | FWR 6 | |
| 20 | `feat(server): account deletion across servers` | FWR 7 | retired names |
| 21 | `ci: two instances federating` | FWR 8 | the `federation` suite |
| 22 | owner's trial | FWR 9 | no pull request; results in the todo |
| 23 onward | FMD items, then PRT | | |

### 14.4 What `./deploy.sh --rollback` can and cannot go back past

- **How it works.** `rollback()` in `deploy.sh` restarts `nunchaku:previous` on the database as it
  is; `checkHealth` reports healthy while `Schema >= Migrations`.
- **Can: every pull request above.** Every migration adds tables, or columns with constant defaults,
  and an older build ignores them.
- **But, from pull request 18 on, an older build shows remote rows as local.** It does not know
  `domain` or `ap_id`, so the people list would list remote accounts and the community timeline
  would show remote posts. Pull request 18 says this in its description and adds
  `/nunchaku -purge-remote` (**new**), which deletes every remote account and its content (the
  follows of local members to remote accounts go with them) and is the step to run before going back
  past it. The README's rollback section says the same.
- **Social facts cannot be rolled back.** Activities delivered stay delivered; an actor id announced
  stays announced (section 4.3). A rollback to a build without federation leaves remote servers
  retrying for their own horizon, then giving up.
- **One step only.** `nunchaku:previous` is one tag; further back is `./deploy.sh vX.Y.Z`.

### 14.5 What federation sends out and what never leaves

Sent, only for a member who opted in: the profile (name, bio, avatar, the props they practise, their
links), posts with audience Public, comments on Public posts, likes of remote posts, follows, and
reports a moderator chooses to forward (without the reporter's name). Never sent: practice sessions,
levels, skill progress, notes, progress documents, combos that were not attached to a Public post,
stats, XP, streaks and badges (F12), milestone posts (F13), bookmarks, notifications, This server
posts, anything of a member who did not opt in. The roadmap's principle gains a line saying so.

### 14.6 How TRU and federation touch

TRU makes the numbers on a profile something the server can vouch for. Until it has switched, a
federated profile sends no numbers at all (F12) and milestone posts stay local (F13). After TRU's
switch, the owner may decide to add level and streak as profile fields; the actor document is the one
place to change. TRU's Q8 (rewording the principle about the user's own document) and the line this
plan adds are compatible: both say nothing leaves without the member's action.

## 15. Risks

- **Interoperability is unproven until the owner's trial.** Field names, the signature component sets
  and the clock window are stated from memory (see Sources). The fixtures and the two-instance test
  prove Flowa agrees with itself and with recorded documents, not that Mastodon accepts what Flowa
  sends. Federated mode is marked experimental until the trial.
- **Exposure.** Federation makes member content public for the first time. F2's opt-in, F16's small
  pages and F8's allowlist first are the answer; a mistake here is a privacy incident, so
  `TestDBRemoteRowsStayRemote` and `TestDBPublicSurfaces` (**new**: every new public route refuses
  This server posts and non-federating members) run on every pull request.
- **Load from outside.** A popular remote thread can send hundreds of likes and replies. Every limit in
  section 7.5 answers it, and a server that ignores 429 is blocked for an hour (7.7).
- **Disk.** The picture cache is bounded at 200 MB; the database grows by remote posts, bounded by the
  30-day retention. The dashboard shows both.
- **Rollback shows remote rows** (14.4); the purge flag is the remedy, and the pull request says so.
- **One file.** `main.go` grows by an estimated 3,000 to 4,000 lines over these epics (signatures about
  300, the queue about 400, the inbox handlers about 1,200, the rest pages and admin). One file is the
  repository's choice; a second package is not proposed.
- **Key secret lost.** New keys for everyone and a burst of failed verifications on remote servers
  until they refetch. The README says to keep the secret with the backups' key, not in the backups.
- **Clock.** A Pi without a working clock fails every signature. The dashboard shows the difference
  between the server's clock and the `Date` of the last valid inbound request.

## Sources

The specifications were to be opened for this note. From the session that wrote it, the network
refused `docs.joinmastodon.org` and `www.rfc-editor.org` (name not resolved), and further hosts were
not tried, as the sandbox's tool advises after a refusal. So every document below is stated from the
author's knowledge, **to be checked against the text when the item is built**:

| Document | State |
| --- | --- |
| W3C ActivityPub, https://www.w3.org/TR/activitypub/ | not reachable from where this was written; stated from the author's knowledge, to be checked against the text when the item is built |
| W3C Activity Streams 2.0 Core, https://www.w3.org/TR/activitystreams-core/ | not reachable from where this was written; stated from the author's knowledge, to be checked against the text when the item is built |
| W3C Activity Vocabulary, https://www.w3.org/TR/activitystreams-vocabulary/ | not reachable from where this was written; stated from the author's knowledge, to be checked against the text when the item is built |
| RFC 7033 WebFinger, https://www.rfc-editor.org/rfc/rfc7033 | not reachable from where this was written (refused); stated from the author's knowledge, to be checked against the text when the item is built |
| RFC 9421 HTTP Message Signatures, https://www.rfc-editor.org/rfc/rfc9421 | not reachable from where this was written (refused); stated from the author's knowledge, to be checked against the text when the item is built |
| draft-cavage-http-signatures-12, https://datatracker.ietf.org/doc/html/draft-cavage-http-signatures-12 | not reachable from where this was written; stated from the author's knowledge, to be checked against the text when the item is built |
| NodeInfo protocol, https://nodeinfo.diaspora.software/protocol.html | not reachable from where this was written; stated from the author's knowledge, to be checked against the text when the item is built |
| Mastodon ActivityPub, https://docs.joinmastodon.org/spec/activitypub/ | not reachable from where this was written (refused); stated from the author's knowledge, to be checked against the text when the item is built |
| Mastodon security, https://docs.joinmastodon.org/spec/security/ | not reachable from where this was written (refused); stated from the author's knowledge, to be checked against the text when the item is built |
| Mastodon WebFinger, https://docs.joinmastodon.org/spec/webfinger/ | not reachable from where this was written; stated from the author's knowledge, to be checked against the text when the item is built |
| Mastodon configuration, https://docs.joinmastodon.org/admin/config/ | not reachable from where this was written; stated from the author's knowledge, to be checked against the text when the item is built |
| Mastodon moderation, https://docs.joinmastodon.org/admin/moderation/ | not reachable from where this was written; stated from the author's knowledge, to be checked against the text when the item is built |

Details in the body that matter for interoperability and could not be checked, each to be verified by
the pull request that builds it: the actor fields Mastodon requires (`preferredUsername`, `inbox`,
`publicKey.publicKeyPem`, `endpoints.sharedInbox`, `manuallyApprovesFollowers`, `discoverable`,
`featured`) and the `toot:` IRI (5.5); the instance actor's WebFinger form (5.3); the cavage header
sets and `hs2019` (7.1); the RFC 9421 components Mastodon sends and accepts (7.1); the 12-hour and
1-hour date window (7.1); the HTML Mastodon keeps and the `h-card` mention form (6.5); the hashtag
`href` with a fragment (6.5); the three forms of the public collection (6.7); Mastodon's retry horizon
(F9); plain-text private keys in Mastodon (F7).
