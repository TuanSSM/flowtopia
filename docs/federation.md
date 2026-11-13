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
this plan changes `compose.yml`'s ports, adds a reverse proxy or ends TLS in the app. (`compose.yml`
passes each setting to the app by name, so every new setting adds one `NAME: ${NAME:-}` line under
`environment`, where empty means the default; nothing else in that file is touched.) HTTPS and the
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

- (a) Stored as plain PEM in Postgres. (What Mastodon does is not settled: its configuration page
  says only that since 4.3.0 it encrypts "some database attributes" with Rails' Active Record
  Encryption, without naming them.)
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
  (not in Mastodon's documentation, and its code could not be opened; see Sources). ActivityPub
  leaves the horizon to the sender (7.5 suggests dropping a follower unreachable for "perhaps six
  months"). Longer costs rows on a Pi; shorter loses posts to a server that is
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
   `application/activity+json` or `application/ld+json; profile="https://www.w3.org/ns/activitystreams"`
   (ActivityPub 7: a POST MUST use the second, and servers SHOULD treat the first as the same).
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
   function of `newFetcher`) is the right place for federation's guard, but it was written for nine
   known hosts: with any host allowed it must also hold the port and two more address ranges. The
   host allowlist and the method do not fit. Section 7.8.
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
    dot or a dash (`ana.`, `bo-`). The SWICG report "ActivityPub and WebFinger" ("Current
    implementation rules") says Mastodon allows a dot or a dash only in the middle of a username
    (`/[a-z0-9_]+([a-z0-9_.-]+[a-z0-9_]+)?/i`), and recommends that new software use only letters,
    digits and underscores for now. So a Flowa member named `ana.` can federate (the actor and
    WebFinger work regardless) but cannot be mentioned or found by handle from Mastodon. FRD's
    WebFinger item warns such a member in Settings when they opt in; the report is a snapshot, so
    the owner's trial (FWR's last item) records how a current Mastodon behaves.
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
- Almost every community route is `social(...)`, which is `flagged("community", authed("", h))`: a
  session is required, and the `community` flag turns the API off. Three are not: bookmarks and
  achievements have flags of their own (`bookmarks`, `game`), and a member's avatar needs a session
  but no flag. Every one of them needs a session, so a signed-out visitor sees nothing.
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
or `single`. Any other value stops the server at start with a message naming the three, and so does a mode
this version does not build yet: an operator who asks for a single-profile instance must not get an
open community that only says "single". Capitals and spaces around the word are forgiven. Every
setting is read before anything is written, so a refused start leaves the database as it was. In single mode
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
- **Frozen once federated.** An actor id has been announced to other servers and cannot change
  (Mastodon's configuration page says the same of its `LOCAL_DOMAIN`: changing it makes remote
  servers "confuse your existing accounts with entirely new ones"). So the public origin and every username that has federated are frozen. If `FLOWA_ORIGIN` later
  differs from the recorded origin, the server starts, serves the app as in local mode, logs
  "federation refused: the origin was https://a and is now https://b; actor ids cannot move", shows
  it on the dashboard, and federates nothing. Moving a whole instance to a new name is not supported
  (members move one by one with PRT's Move).
- `federated` back to `local`: allowed, but the remote copies stay where they are. In local mode
  after federating, the actor URLs answer **410 Gone** with a Tombstone (ActivityPub 6.4: 410 when a
  Tombstone is the body) and WebFinger answers 404 (RFC 7033 4.2 asks for 404 when the server has no
  information); these are the only federation answers local mode gives, and only on an instance that
  recorded `first_federated_at`, so remote servers stop retrying. An admin who wants remote servers to forget the accounts first runs "Leave the
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

What Mastodon checks in the answer (Mastodon WebFinger page, "Mastodon's requirements for
WebFinger"): `subject` is present; `links` holds a link with `rel` `self` and `type`
`application/activity+json` or `application/ld+json; profile="https://www.w3.org/ns/activitystreams"`;
its `href` resolves to an actor. It then goes back the other way: from the actor's
`preferredUsername` and the host of the actor's id it builds `acct:ana@flowa.example.org`, asks
WebFinger again, and expects the same `subject`. So the handle's host must be the host of
`FLOWA_ORIGIN`, which it is by construction. `aliases` and the profile-page link are optional
(RFC 7033 4.4; the SWICG report "ActivityPub and WebFinger" lists the profile page among the common
extra links). Since Mastodon 4.6.0 an actor may also name its handle in a `webfinger` property
(FEP-2c59); Flowa does not need it, since `preferredUsername` and the host already give the handle.

Edge cases, each a row of `TestWebFinger` (**new**): `acct:` with any case, a leading `@`, the actor
URL as the resource, the host compared with the origin's host case-insensitively and without a
trailing dot, an unknown name, a member who has not opted in, a retired name (all three 404: RFC
7033 4.2 asks for 404 when the server has no information, and one answer for all three does not say
whether an account exists or existed), the instance actor (`acct:flowa.example.org@flowa.example.org`
resolves to `/actor`; this form is not in Mastodon's documentation, see Sources), a missing or
repeated `resource` (400: RFC 7033 4.2 asks for 400 when it is absent or malformed, and for it
exactly once), a resource over 300 bytes (400), one or more `rel` parameters (only the matching links
returned, the rest of the answer unchanged: RFC 7033 4.3 says SHOULD support `rel`, and a server
that does not MUST ignore it). Every answer, including an error, carries
`Access-Control-Allow-Origin: *` (RFC 7033 5: servers MUST send the header and SHOULD allow any
origin). Answering only for members who opted in (F2) is also what RFC 7033 9.2 asks: personal data
goes out through WebFinger only when the person has authorised it.

**host-meta** (`/.well-known/host-meta`, RFC 6415): later, only if a server is found that needs it
to find WebFinger. Mastodon's WebFinger page describes the lookup going straight to
`/.well-known/webfinger` and does not mention host-meta; whether Mastodon falls back to it when
WebFinger fails is not in its documentation.

### 5.4 NodeInfo 2.x

`GET /.well-known/nodeinfo` (**new**) answers a JRD with one link,
`{"links": [{"rel": "http://nodeinfo.diaspora.software/ns/schema/2.1", "href": "https://flowa.example.org/nodeinfo/2.1"}]}`,
as `application/json` (NodeInfo protocol, "Discovery": the relation names the schema version, and
media types other than `application/json` are left unspecified). `/nodeinfo/2.1` (**new**) answers
with `Content-Type: application/json; profile="http://nodeinfo.diaspora.software/ns/schema/2.1#"`
(NodeInfo protocol, "Retrieval") and:

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

Checked against the NodeInfo 2.1 schema: `version`, `software`, `protocols`, `services`,
`openRegistrations`, `usage` and `metadata` are all required, and no other top-level member is
allowed; `software.name` must match `^[a-z0-9-]+$` and `software.version` is a required string;
`protocols` needs at least one entry from a fixed list (`activitypub` is one); `services.inbound`
and `services.outbound` are required arrays and may be empty; `usage.users` is a required object
whose members (`total`, `activeMonth`, `activeHalfyear`) are each optional; `metadata` is free form.
The example above is valid.

The counts mean what the schema says they mean: `total` is "the total amount of on this server
registered users", the active counts are users who "signed in at least once" in the last 30 and 180
days (Flowa reads `last_seen`), and `localPosts` is every post made by local users. (An earlier draft
of this note counted only members who opted in; that would publish smaller numbers under the
schema's names.) A count is not personal data, but if the owner prefers not to publish them, the
schema allows `"users": {}` and no `localPosts`. `openRegistrations` ("whether this server allows
open self-registration") is true only when the registration mode is open without approval, and
always false in single mode. The version string follows `buildVersion`; an admin can choose to send
`"version": ""` (instance setting `nodeinfo_version`, **new**; the schema requires the member but not
a value), since which build runs helps an attacker, the reason `handleHealthz` keeps it private.

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
      "indexable": "toot:indexable",
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
  "indexable": false,
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

Checked against the texts. ActivityPub 4.1: an actor MUST have `id`, `type`, `inbox` and `outbox`
(both OrderedCollections), SHOULD have `followers` and `following`, and MAY have
`preferredUsername` and `endpoints.sharedInbox`. Mastodon's ActivityPub page ("Profile federation",
"Properties used") reads `preferredUsername` (for the WebFinger lookup; "must be unique on the
domain"), `name`, `summary`, `type` (`Application` or `Service` is shown as a bot), `url`, `icon`,
`image`, `manuallyApprovesFollowers` (shown as locked), `discoverable`, `indexable`, `publicKey`
("required for signatures"), `featured`, `attachment` (profile fields), `alsoKnownAs` (required for
Move), `published`, `memorial` and `suspended`. Its "JSON-LD Contexts and Extensions" section gives
the IRIs used in the context above: `toot:` is `http://joinmastodon.org/ns#` (`discoverable`,
`indexable`, `featured`, `suspended`, `blurhash`, `focalPoint`), `manuallyApprovesFollowers`,
`movedTo`, `sensitive` and `Hashtag` are `as:` terms, and `schema:` is `http://schema.org#`, which
the page itself calls incorrect (it should be `https://schema.org/`) but which is what Mastodon
expects, so Flowa writes it that way on purpose. Profile fields are `PropertyValue` objects with
`name` and an HTML `value` ("Profile metadata"); the key is `publicKey` with `id`, `owner` and
`publicKeyPem` under the `https://w3id.org/security/v1` context ("Public key"). When FRD's
fixtures land, the golden file is still compared with a recorded Mastodon actor. `discoverable` is false by default
(no directory listing); a member can turn it on. `indexable` (whether other servers may put the
member's posts in full-text search; the page recommends that indexing tools respect it) is false
and is not a setting yet, to match F2's "nothing shared without an action". No stats, levels or
badges (F12).

The instance actor `/actor` is `"type": "Application"`, `preferredUsername` the host, with its own
key, an inbox and an empty outbox (ActivityPub 4.1 requires both properties). It signs fetches that
are not on behalf of a member (key fetches, fetches under authorized fetch) and receives Flags.
Mastodon's ActivityPub page ("Secure mode") says Mastodon uses "a dedicated system actor" for the
same purpose, and sends its own Flags from it ("Reporting profiles and posts"); the SWICG report on
HTTP signatures ("Instance actor") explains why: it breaks the loop of two servers each fetching the
other's signing actor with a signed request.

### 5.6 Content negotiation on the same URL

`/users/{name}` and `/users/{name}/posts/{id}` answer JSON when `Accept` names
`application/activity+json` or `application/ld+json` (with or without the profile parameter), and
otherwise a small HTML page the server writes from a Go `html/template` (no script, CSP
`default-src 'none'; img-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none';
form-action 'none'`, and the other headers `secure` sets) with a link into the app (`/#/u/ana`,
`/#/post/812`). ActivityPub 3.2 allows exactly this: a server MUST answer the ActivityStreams
object to `application/ld+json; profile="https://www.w3.org/ns/activitystreams"`, SHOULD to
`application/activity+json`, and MAY use the same URL for HTML. Every answer carries `Vary: Accept`,
and `Vary: Accept, Signature` while authorized fetch is on (the SWICG report on HTTP signatures,
"Compatibility with HTTP caching": an answer that depends on the request's signature must say so,
or a cache hands a signed reader's answer to an unsigned one). JSON answers carry
`Content-Type: application/activity+json` (Activity Streams 2.0 Core 8.1 registers it, and says to
treat the `ld+json` form as the same) and `Cache-Control: public, max-age=180`, or `private` with
authorized fetch on. `TestContentNegotiation` (**new**) covers each `Accept` form, `*/*`, a browser's
header, and a missing one (HTML).

### 5.7 Keys

- RSA 2048, generated for a member when they opt in and for the instance actor when federation first
  starts, in the background, one at a time (key generation takes about a second or more on the Pi;
  measured in FRD's keys item), so no request waits for it. RSA because Mastodon verifies nothing
  else before 4.7.0 (its security page), and 2048 bits because the SWICG report on HTTP signatures
  asks for "a 2048-bit or larger RSA keypair". The public key is served as an SPKI
  (`BEGIN PUBLIC KEY`) PEM, the form in Mastodon's examples.
- Stored in `ap_keys` (**new**): the public key as PEM, the private key sealed with AES-256-GCM under
  `FLOWA_KEY_SECRET` (F7), with the key id and creation time. The secret never goes into the database,
  the log or a backup.
- Rotation: `/nunchaku -rotate-keys` (**new**, a flag like `-healthcheck`) makes new keys for every
  local actor and sends Update(Person) to followers; remote servers that fail to verify with the old
  key refetch the actor (section 7.4). This is what the SWICG report calls a "signed" rotation ("Key
  rotation"), and its verification steps refetch the key once on a failure, as 7.4 does. Also used
  after a lost secret.
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
| `POST /users/{name}/inbox`, `POST /inbox`, `POST /actor/inbox` | inboxes (section 7); a GET answers 405, because Flowa has no client-to-server side to show an inbox to (ActivityPub 5.2 makes the inbox an OrderedCollection mainly for its owner's client) |
| `GET /users/{name}/outbox` | OrderedCollection of the member's Create activities for Public posts, `totalItems`, `first`, pages of 20 newest first by post id (`?page=true&max_id=812`); ActivityPub 5.1 requires an OrderedCollection, 5 requires reverse chronological order and names an increasing integer id as a fair key, and 5.1 says an unauthenticated reader gets the Public posts |
| `GET /users/{name}/followers`, `/following` | OrderedCollection with `totalItems` only; `first` and pages only for the account itself (F10); ActivityPub 5.3 and 5.4 allow either collection type and filtering by who asks |
| `GET /users/{name}/featured` | an empty OrderedCollection at first (pinned posts do not exist yet); cheap, and Mastodon reads `featured` as the pinned posts it fetches for a profile (its ActivityPub page, "Featured collection") |
| `GET /users/{name}/posts/{id}`, `/comments/{id}` | the Note, or HTML; 404 for audience This server, 410 with a Tombstone for a deleted one (ActivityPub 6.4: 410 when a Tombstone is the body, 404 otherwise; 3.2 allows 404 for an object whose existence is private) |
| `GET /users/{name}/posts/{id}/picture.jpg`, `GET /users/{name}/avatar.jpg` | the stored picture, public only for federating members and Public posts |
| `GET /api/fed/...` | the page's own endpoints for follows, search by handle, Home (section 11), behind `authed` and the limits. Blocks of accounts are not here: they are `PUT /api/users/{name}/block`, `DELETE` of the same and `GET /api/blocks`, which every instance has since INS, behind the `community` flag |

### 6.3 Activities in

**One decoder for the shapes JSON-LD allows.** Flowa runs no JSON-LD processor, so the same field
arrives in several shapes and each must be read: one value or a list of them (`to`, `cc`, `type`,
`attributedTo`, `tag`, `attachment`, `url`, `icon`, and `publicKey`, where the SWICG report on HTTP
signatures says to pick the entry whose `id` is the signature's key id), and an id as a string or an embedded object
carrying it (`actor`, `object`, `attributedTo`, `publicKey`, `inReplyTo`). Two small types do this
for every field (`oneOrMany`, `idOrObject`, **new**), nothing reads these fields any other way, and
the fixtures hold each shape. `@context` is not interpreted: a property is recognised by its short
name, and by its full `https://www.w3.org/ns/activitystreams#` form where a server sends that.
`TestDecodeShapes` (**new**).

**What Flowa reads from a remote actor:** `id`, `type`, `preferredUsername`, `name`, `summary`
(through `htmlToText`), `url`, `icon`, `inbox`, `endpoints.sharedInbox`, `followers`, `publicKey`,
`manuallyApprovesFollowers` (a Follow to it shows as requested until Accept), `movedTo` and
`alsoKnownAs` (PRT), and `suspended`: Mastodon sets it on an account its server suspended and then
serves "empty data" for it (Mastodon ActivityPub page, "Suspended flag"), so Flowa marks such an
actor `gone` in `ap_actors` and stores nothing new from it until an Update clears the flag.
`discoverable` and `indexable` concern directories and full-text search, which Flowa does not have
(section 10), so Flowa does not read them. Everything else is ignored.

The inbox verifies (section 7), stores the activity in `ap_inbox` and answers 202; a worker handles
it. "Actor" below is the verified signer's actor.

| Activity | Accepted when | Effect | Edge cases |
| --- | --- | --- | --- |
| Follow (object: a local actor) | the object is a local federating member; the actor is not blocked by the member or the domain | `follows` row `pending`; Accept sent at once unless the member approves followers by hand, then a follow request notification | a repeat Follow while accepted: Accept sent again (the remote may have lost it); Follow of the instance actor: Reject; Follow of a member who opted out since: Reject |
| Accept (object: our Follow, by id or embedded) | the actor is the target of that Follow | our `follows` row becomes `accepted` | Accept of an unknown or already accepted Follow: ignored; embedded Follow whose actor is not ours: refused |
| Reject (object: our Follow) | as Accept | row deleted, the member told once | Reject after Accept means "removed you as a follower": row deleted |
| Undo (object: Follow, Like, Announce, Block, or Accept of our Follow) | the inner activity's actor is the signer (ActivityPub 6.10: both MUST have the same actor) | the row it made is removed; Undo of an Accept of our Follow is read as a Reject | Undo of something never seen: ignored, 202; the inner object by id only is matched by (actor, type, object). Mastodon's ActivityPub page lists Undo of Like and Announce for posts, and of Follow, "Accept Follow" and Block for profiles |
| Create (object: Note) | the Note's `attributedTo` and the activity's actor are the signer, same origin as the Note's id; and either the actor is followed by a local member, or the Note replies to a local post or comment, or mentions a local member | stored as a post (or a comment when `inReplyTo` is a known post or comment) | a Note already stored: ignored; `inReplyTo` unknown and nobody follows: dropped; direct (no Public, no followers collection in `to`/`cc`): dropped (F3); over 2000 characters: cut with a link to the original (section 6.6); Question, Article, Page and the other types Mastodon converts: kept as text with a link, see 6.6 |
| Update (object: Note) | same origin and author as the stored Note (ActivityPub 7.3: at minimum, the Update and its object of the same origin) | the whole object replaced, not merged (ActivityPub 7.3); `updated` kept | Update of an unknown Note: fetched and stored only if it would have been accepted as a Create; Update without `updated`, or older than the stored `updated`: ignored (Mastodon edits a post only "when the updated timestamp is present", its ActivityPub page says) |
| Update (object: Person) | object id is the signer | cached actor replaced, key replaced | a different `publicKey` is a rotation: accepted, because the request was verified with the key it replaces or after a refetch |
| Delete (object: Note or Tombstone, by id or embedded) | the object's id has the signer's origin, and the stored Note's author is the signer | post or comment removed with its likes and replies as a local delete does | Delete of an unknown id: ignored, 202, and the id remembered for 7 days so a late Create is refused |
| Delete (object: the actor itself) | the object is the signer | the remote account's posts, comments, likes and follows purged; the row kept as a tombstone so the name shows as "deleted account" | the key cannot be fetched any more (the actor is gone): accepted when the signature verifies against the cached key; refused otherwise. This is what Mastodon's security page says receivers do with the Delete it sends when a server shuts down ("validating it against the locally cached actor key"); the SWICG report on HTTP signatures ("Handling Deletes of actors") also never processes one whose key fetch answers 404 |
| Like (object: a local post or comment) | the object is ours and visible to the public | `likes` or `comment_likes` row for the remote account; notification as for a local like | Like of a hidden or This server post: ignored; Like with content (an emoji reaction, as Misskey sends): counted as a like |
| Announce (object: a local post) | the object is ours and Public | a notification "shared your post" (**new** kind `share`); no timeline entry | Announce of someone else's post: dropped in the first cut (Flowa has no boosts); the inner object is never trusted inline |
| Block (object: a local actor) | object is ours | our follows in both directions with that actor removed; the remote account's content hidden from that member | never told to the member. Mastodon sends Block when its user blocks a remote one, though ActivityPub 6.9 says servers SHOULD NOT deliver it, and reads one as "hide the actor's profile and posts from the local user" (Mastodon ActivityPub page, "Remote blocking"); its user guide ("Blocking") lists what the blocked person experiences and no notice among it |
| Flag (object: local actors and posts) | the domain does not have "reject reports" | a report in the queue, reporter the remote instance's account, the Flag's `content` as the report's note | objects that are not ours are dropped; more than 20 objects: cut to 20. Mastodon sends a Flag from its instance actor, with the reported account and the reported posts in `object`, the comment in `content`, and `to` the reported account (Mastodon ActivityPub page, "Reporting profiles and posts") |
| Move (object: the actor, target: a new actor) | PRT: the target's `alsoKnownAs` names the actor (Mastodon ActivityPub page, "Account migration") | local followers re-follow the target | until PRT ships: ignored |
| Anything else (Add, Remove, Read, View, EmojiReact, QuoteRequest, ...) | | 202, dropped, counted per type on the dashboard | Mastodon uses Add and Remove for pinned posts and QuoteRequest for quote posts (its ActivityPub page); Flowa has neither |

Mastodon's ActivityPub page ("Supported activities for statuses" and "for profiles") lists Create,
Delete, Like, Announce, Update, Undo, Flag and QuoteRequest for posts, and Follow, Accept, Reject,
Add, Remove, Update, Delete, Undo, Block, Flag and Move for profiles; every one of them has a row
above.

### 6.4 Activities out

| Activity | When | Sent to |
| --- | --- | --- |
| Create(Note) | a federating member posts with audience Public | followers' inboxes (shared inbox per host; a follower whose actor names no shared inbox gets its own inbox, as ActivityPub 7.1.3 requires), plus mentioned remote actors |
| Create(Note) for a comment | a federating member comments on a Public post, local or remote | the commenter's followers, the post's author (and, for a local post, the author's followers), mentioned actors |
| Update(Note) | a comment is edited inside the 15-minute window | as the Create; the Note carries `updated`, without which Mastodon does not apply an edit (its ActivityPub page, Update) |
| Forward of an inbound activity | a remote activity Flowa sees for the first time is addressed to a collection Flowa owns (a local member's followers) and replies to, or is about, an object Flowa owns: typically a remote reply to a local Public post that `cc`s the author's followers | the members of that collection on other servers, with the body as received. ActivityPub 7.1.2 makes this a MUST, to avoid "ghost replies" that only some followers see. Flowa signs the forward with the instance actor and does not add Linked Data signatures, so a receiver that does not take a signer other than the actor fetches the object from its origin by id, as Flowa does itself (7.3); how Mastodon treats such a forward is not in its documentation |
| Delete(Tombstone) | a federated post or comment is deleted by its author or by a moderator | everyone it was sent to (recorded in `ap_deliveries`) |
| Update(Person) | profile name, bio, avatar or links change, or keys rotate | followers; at most one in five minutes per member, the newest wins |
| Delete(Person) | a federating account is deleted | followers and every host it ever delivered to (bounded at 2,000 hosts) |
| Follow, Undo(Follow) | a member follows or unfollows a remote account | that actor's inbox |
| Accept, Reject | a follow request is answered (or answered at once for open accounts) | the follower's inbox |
| Like, Undo(Like) | a member likes or unlikes a remote post | the post author's inbox |
| Flag | a moderator chooses to forward a report about remote content | the remote instance's shared inbox, signed by the instance actor, with no reporter named; in Mastodon's shape: `actor` the instance actor, `object` the reported account then the reported posts, `content` the moderator's comment, `to` the reported account |
| Move | PRT | followers |
| Block | not sent: the block removes follows with Undo and Reject, which says less about the member (F3 follows Flowa's privacy defaults), and ActivityPub 6.9 says servers SHOULD NOT deliver a Block to its object. Mastodon does send one ("Remote blocking"), so Flowa receives Blocks (6.3) but does not send them | |
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
      {"type": "Image", "mediaType": "image/jpeg", "url": "https://flowa.example.org/users/ana/posts/812/picture.jpg", "name": "", "summary": "", "width": 1280, "height": 960}
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
  of local and remote members as the `h-card` form above. Only `p`, `br`, `a` and `span` are written.
  Mastodon's ActivityPub page ("HTML sanitization") keeps `p`, `span` (with `class`), `br` and `a`
  (with `href`, `rel` and `class`) in every version, more tags since 4.2, and classes that start with
  `h-`, `p-`, `u-`, `dt-` or `e-` or are `mention`, `hashtag`, `ellipsis` or `invisible`; links keep
  their `href` only for `http`, `https` and a few other schemes. So everything Flowa writes survives.
  Its Microformats page says `h-card` is "also attached to mentions" and names `u-url`, `mention`
  and `hashtag`; the exact nesting above (`h-card` span, `u-url mention` link, the name in its own
  span) is Mastodon's own output as remembered, and a recorded Mastodon post confirms it in FRD.
- **The plain-text fallback** for chips and combos is always in `content` ("Tricks: ...", "Combo:
  Name: A, B, C"), so any server shows something sensible; another Flowa reads `flowa:tricks` and
  `flowa:combo` back into chips when the skill exists in its catalog, and shows the text otherwise.
  Skill names come from the latest published catalog (`latestCatalog`), so a renamed skill federates
  under its new name.
- **One Hashtag per prop** in the chips, so a post can be found by `#poi` on servers that index tags.
  The tag is `{"type": "Hashtag", "name": "#poi", "href": ...}` as in Mastodon's ActivityPub page
  ("Hashtags"), which also says Mastodon folds a tag's name to lower-case ASCII and drops characters
  it does not allow; the five prop names (`buugeng`, `doublestaff`, `nunchaku`, `poi`, `staff`) pass
  unchanged. The `href` is the prop's skill map; the page's example `href` is a plain path, and
  whether Mastodon uses or keeps an `href` with a fragment is not in its documentation.
- **Mentions** are `{"type": "Mention", "href": <actor id>, "name": "@bo@social.example"}` and the
  mentioned actor is also in `to` or `cc`: Mastodon needs the tag to notify and the address to
  deliver (its ActivityPub page, "Mentions for addressing and notifications"; `tag[].name` is "the
  plain-text WebFinger address").
- **summary** is a content note; Flowa has none today, so it is left out. Incoming summaries are
  shown as a fold label (Mastodon's ActivityPub page: `summary` is the content warning, and
  `sensitive` hides the media).
- **attachment** is the post's picture as a public URL with its size; alt text is empty until the
  composer gets an alt text field (FWR, posts out: a field, at most 1,500 characters). The alt text
  is written twice, as `name` and as `summary`: Mastodon's ActivityPub page names
  `attachment[].summary` as "used as media description", while `name` is the Activity Vocabulary's
  title for an object and is what many servers read. Coming in, `summary` is read first, then
  `name`. Which of the two a current Mastodon writes is not in its documentation; a recorded
  Mastodon post with alt text settles it in FRD. An empty alt text is allowed but makes the picture
  inaccessible on other servers; the composer asks for it. `blurhash` and `focalPoint` (Mastodon
  extensions) are not sent.
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
`richText`, which escapes everything and links URLs and mentions, exactly as for a local post. A mention needs care: Mastodon writes a remote mention as a link whose text is the short `@bo`, which
`richText` would link to the local member `bo`. So `htmlToText` rewrites each link that a `Mention`
entry of the object's `tag` names into the full `@bo@social.example`, and who is notified locally is
decided from the `href`s of those entries alone, never by running `notifyMentions` over the text. No
HTML sanitiser is needed, and none is added: `golang.org/x/net/html` would replace about 80 lines with a
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
| Article, Page, and Image, Audio, Video or Event as the object itself | the `name` as a title, the summary or the first 500 characters, and the link. Mastodon converts the same six types "as best as possible": `content`, or `name` without it, as the text, the `url` appended, `summary` as the content warning (its ActivityPub page, "Payloads") |
| Announce of others | dropped (6.3) |
| Unknown properties (`blurhash`, `focalPoint`, `memorial`, quote properties, ...) | ignored |

### 6.7 Addressing

The public collection is accepted in three forms: `https://www.w3.org/ns/activitystreams#Public`,
`as:Public` and `Public`, and sent only in the first. ActivityPub 5.6 names the first as the
identifier and says implementations that do not run JSON-LD "should be prepared to accept all
three"; it also says never to deliver to it. Which form Mastodon sends is not in its documentation,
and does not matter since all three are read. Incoming visibility is read as Mastodon's ActivityPub
page ("Mentions for addressing and notifications") defines it: Public in `to` is public; Public in
`cc` only is unlisted (shown in Home, like public); the actor's followers collection in `to` or `cc`
without Public is followers-only (F3); actors only, whether all mentioned ("direct") or not
("limited"), is not shown and dropped.

## 7. Security

### 7.1 HTTP signatures

- **Sent, draft-cavage-http-signatures-12 (the default):** `algorithm="rsa-sha256"`,
  `headers="(request-target) host date digest"` on POST and `"(request-target) host date"` on GET,
  `Digest: SHA-256=<base64 of the body's SHA-256>`, and `keyId` the actor's key id. The signing
  string is one `name: value` line per entry of `headers`, in that order, names in lower case,
  `(request-target)` as the lower-case method, a space and the path with its query, joined by `\n`
  with no newline at the end (cavage-12 2.3); it is signed with RSASSA-PKCS1-v1_5 and SHA-256.
  Mastodon's security page asks for exactly this: signed `(request-target)`, `host` and `date`
  ("Creating HTTP signatures"), and on a POST a `Digest` header that is itself signed ("Signing POST
  requests and the Digest header"). On the `algorithm` value the texts disagree with practice:
  cavage-12 2.1.3 says it MUST NOT be a name its registry marks deprecated, and the registry (E.2)
  marks `rsa-sha256` deprecated; the one active name, `hs2019`, is defined with SHA-512 and
  recommends RSASSA-PSS for RSA keys, which is not what anyone in the fediverse signs. The SWICG
  report on HTTP signatures advises sending `hs2019` and reading it as `rsa-sha256`; Mastodon's own
  example sends no `algorithm` at all. Flowa sends `rsa-sha256` because it names exactly what is
  signed and is what the draft's test vectors use (Appendix C); whether Mastodon reads the field is
  not in its documentation, and the owner's trial would show a refusal. `Digest` follows RFC 3230
  as the draft's examples write it (`SHA-256=`, base64); Mastodon's page writes `sha-256=` in one
  example and a bare hex value in another, so the page is not relied on for the format, and Flowa
  reads the algorithm name in any letter case (RFC 3230 4.1.1: "digest algorithm values are
  case-insensitive"). RFC 9530 has since obsoleted `Digest`, but cavage signatures
  still need it.
- **Sent, RFC 9421 (to a host marked for it, POST only):**
  `Content-Digest: sha-256=:<base64 of the body's SHA-256>:` (RFC 9530 2: a structured-field
  dictionary whose key is the algorithm and whose value is a byte sequence between colons),
  `Signature-Input: sig1=("@method" "@target-uri" "content-digest");created=<Unix seconds>;keyid="<key id>"`
  and `Signature: sig1=:<base64>:`, signed with RSASSA-PKCS1-v1_5 and SHA-256 (RFC 9421 3.3.2), and
  no `alg` parameter, as in Mastodon's example (RFC 9421 3.2 lets the key decide the algorithm).
  The signature base is one line per covered component, `"name": value`, then the
  `"@signature-params"` line, joined by `\n` with none at the end (RFC 9421 2.5); `@target-uri` is
  the full `https://` URL with its query (2.2.2). This is Mastodon's list exactly (Mastodon security
  page, "HTTP Message Signatures (RFC9421)"): only a single signature; `created` MUST be present;
  `@method` and `@target-uri` MUST be signed; a `Content-Digest` header MUST be present and signed;
  the only algorithm is RSASSA-PKCS1-v1_5 with SHA-256. Mastodon validates such signatures since
  4.4.0 behind its `http_message_signatures` flag and by default since 4.5.0; 4.7.0, not released
  when this was read, adds ed25519 validation. Fetches (GET) are always signed with cavage: Mastodon's
  list requires `Content-Digest` with no exception for a GET, and whether it wants the digest of an
  empty body there is not stated.
- **Accepted:** cavage as sent, also with `algorithm="hs2019"` or no `algorithm`: whatever the field
  says, an RSA key is verified as RSASSA-PKCS1-v1_5 with SHA-256 (cavage-12 2.5: the verifier MUST
  take the algorithm from the key, not from the field; the SWICG report: "If it's hs2019, assume
  that means rsa-sha256"). A missing `headers` parameter covers no request line (the draft says it
  then means `(created)`, Mastodon's page that it assumes `date`), so it is refused. `(created)` and
  `(expires)` are accepted only with `hs2019`, since cavage-12 2.3 makes them an error with an `rsa`
  algorithm, and a `created` in the future is refused (2.1.4). RFC 9421: one signature, as Mastodon
  accepts it; `created` required; covered, `@method` and `@target-uri`, or `@method`, `@authority`,
  `@path` and (when the request has a query) `@query`, and on POST `content-digest`; an `alg`, when
  present, must be `rsa-v1_5-sha256` (RFC 9421 3.2, step 6.5: an algorithm stated in two places must
  agree); an `expires` in the past is refused (3.2.1).
- **Which form is sent to a host:** cavage by default. A 400 or a 401 to a signed delivery is
  answered by sending it once more at once, in the other form: what the SWICG report calls
  "double-knocking", and what Mastodon 4.7.0 (unreleased) does itself, falling back to RFC 9421 for
  "requests that fail with 400 or 401 with cavage draft signatures" (Mastodon security page, version
  history). If the other form is accepted, `ap_hosts.sig` (**new**) records it and the host gets that
  form first from then on; if it is refused too, the row follows section 8.3. A host that sends Flowa
  a valid RFC 9421 signature is marked the same way. So a host gets two requests for one delivery
  only on a refusal. Flowa answers a bad signature with 401 (below), so Mastodon's own fallback works
  when Mastodon is the sender.
- A request is refused (401, with a short reason in the log, never the header) when: the signature
  does not cover the request line (`(request-target)`; in RFC 9421 as above), `host` (cavage; in RFC
  9421 the authority is part of `@target-uri` or `@authority`), `date` (or `created`) and, on POST,
  the digest; the digest does not match the body (`Digest` for cavage, `Content-Digest` for RFC
  9421); the date (or `created`) is more than 12 hours old or more than 1 hour ahead; the key cannot
  be fetched; the signature does not verify after one refetch of the key (the SWICG report's
  verification steps refetch once, for a rotated key). The 12 hours are Mastodon's ("Verifying HTTP
  signatures": "made within the past 12 hours"). Mastodon's page says nothing about a date in the
  future; the hour ahead is Flowa's own, close to the SWICG report's "an hour plus a few minutes
  buffer in either direction".

Example of a delivery Flowa sends with cavage (the digest shown is of the body `{"hello": "world"}`
used in both texts' examples, so it can be checked by hand):

```http
POST /users/bo/inbox HTTP/1.1
Host: social.example
Date: Thu, 12 Nov 2026 18:04:06 GMT
Content-Type: application/ld+json; profile="https://www.w3.org/ns/activitystreams"
Digest: SHA-256=X48E9qOokqqrvdts8nOJRJN3OWDUoyWxBf7kbu9DBPE=
User-Agent: Flowa/v1.4.0 (+https://flowa.example.org/)
Signature: keyId="https://flowa.example.org/users/ana#main-key",algorithm="rsa-sha256",headers="(request-target) host date digest",signature="kO3...8w=="
```

and the string it signs:

```text
(request-target): post /users/bo/inbox
host: social.example
date: Thu, 12 Nov 2026 18:04:06 GMT
digest: SHA-256=X48E9qOokqqrvdts8nOJRJN3OWDUoyWxBf7kbu9DBPE=
```

The same delivery in RFC 9421 form carries `Content-Digest:
sha-256=:X48E9qOokqqrvdts8nOJRJN3OWDUoyWxBf7kbu9DBPE=:` and signs this base:

```text
"@method": POST
"@target-uri": https://social.example/users/bo/inbox
"content-digest": sha-256=:X48E9qOokqqrvdts8nOJRJN3OWDUoyWxBf7kbu9DBPE=:
"@signature-params": ("@method" "@target-uri" "content-digest");created=1794506646;keyid="https://flowa.example.org/users/ana#main-key"
```

The `Content-Type` is the `ld+json` form because ActivityPub 7 says a POST MUST use it (and that
servers SHOULD treat `application/activity+json` as the same); Mastodon's own example uses it too.

The verifier uses the origin of `FLOWA_ORIGIN` for `host`, `@authority` and `@target-uri`, and the
path and query as received for `(request-target)`, `@path` and `@query` (finding 4).
`TestSignatureVectors` (**new**) copies these published vectors into
`testdata/federation/vectors/` (**new**), each with the section it came from:

- **cavage-12 Appendix C.1 (Default) and C.2 (Basic):** `rsa-sha256`, that is RSASSA-PKCS1-v1_5
  with SHA-256, on the draft's test key. Signing reproduces the published signature byte for byte
  (PKCS#1 v1.5 is deterministic) and verifying accepts it; both were checked with Python's
  `cryptography` package while writing this. Two warnings go with them: the appendix calls its own vectors "old
  and possibly wrong", and the key it calls 2048-bit is 1024-bit.
- **cavage-12 C.3 (All Headers) is not copied.** Its signature verifies only against the string it
  prints, which leaves out the `(created)` and `(expires)` lines its own `headers` list names, and
  section 2.3 makes those two an error with `rsa-sha256` anyway.
- **RFC 9421 Section 4.3, the `proxy_sig` signature:** the RFC's only `rsa-v1_5-sha256` example,
  with `test-key-rsa` from Appendix B.1.1 (2048-bit, a PKCS#1 `RSA PUBLIC KEY` PEM, so it also tests
  that form of key). Deterministic like the cavage ones, so the test signs and verifies; it too was
  checked the same way while writing this.
- **RFC 9421 Section 2.5 and Appendix B.2.1 to B.2.3:** for building the signature base only. Their
  signatures are RSASSA-PSS with SHA-512 (`test-key-rsa-pss`), which Flowa does not implement, so
  the test compares the base it builds with the printed one and does not verify those signatures.
- **Digests:** SHA-256 of `{"hello": "world"}` is `X48E9qOokqqrvdts8nOJRJN3OWDUoyWxBf7kbu9DBPE=`
  (cavage-12 3.1 and Appendix C) and SHA-512 is the `sha-512=:WZDPaVn...:` value of RFC 9421 B.2;
  both match a recomputation.

### 7.2 Replay

- An activity id is processed once (ActivityPub 5.2: a server MUST de-duplicate by `id`):
  `ap_inbox.activity_id` is unique; processed rows lose their body
  at once and are kept 7 days. **The id must have the actor's origin before it is written**, or a
  hostile server could send activities carrying ids another server will use and have the real ones
  dropped as repeats. An activity with no id (ActivityPub 3.1 and 7 allow it for one "intentionally
  transient") is processed and not deduplicated by id; the signature rule below still stops a
  replay of the same request.
- A signature is accepted once: the SHA-256 of the `Signature` header is kept in memory for the 13
  hours a date can be valid, in a map of at most 100,000 entries (about 6 MB); when full, the oldest
  half is dropped, and a request older than the oldest kept is refused as stale.

### 7.3 Origin checks

- The key's `owner` must be the activity's `actor`, and the key id must have the actor's origin.
  **Origins are compared whole**: scheme, host and port, after the fragment is removed, never the
  host alone. When the key id is not a fragment of the actor's URL (some servers keep the key at a
  path of its own), the key document is fetched, then the actor it names as `owner`, and the actor's
  `publicKey.id` must be that key id: a key vouches for an actor only when the actor names the key.
  This is Mastodon's rule (security page, the footnote on `keyId`: for `actor#key`
  "the owner points back to the same document"; for a key at its own path "the owner points back to
  some actor, and that actor should point back to the same key"), and the SWICG report's ("How to
  obtain a signature's public key", which also follows `controller` where `owner` is missing).
  `publicKeyPem` may hold either a PKCS#1 (`RSA PUBLIC KEY`) or an SPKI (`PUBLIC KEY`) PEM (the SWICG
  report, same section), so both are parsed.
- A fetched document's `id` must be the URL it was fetched from, compared after the fragment is
  removed. (ActivityPub 3 asks a server to check at least "that the object appears as received at
  its origin"; 3.1 requires ids whose authority belongs to their originating server.)
- The object's id, when the object is embedded, must have the actor's origin, except: Accept and
  Reject (the object is our Follow, on our origin), Undo (the object is the actor's own activity),
  Like, Announce, Flag and Block (the object is someone else's, by id, never trusted inline), and
  Create of a Note whose id has the actor's origin. An embedded object of another origin is fetched
  from its own origin by id instead, and that copy is used (ActivityPub 3 gives this very case, a
  Like carrying a forged Note, and says to dereference the id).
- Linked Data signatures (for activities forwarded by a third server) are not verified: a forwarded
  activity whose signer is not its actor is answered 202 and its object fetched from its origin by id.
  Mastodon's security page advises against implementing them ("it is not advised to implement
  support for LD Signatures": Mastodon's `RsaSignature2017` predates the final drafts, which Data
  Integrity has since replaced); Mastodon uses them for relays and for the Deletes it sends when a
  server shuts down, and 4.7.0 (unreleased) adds FEP-8b32 Object Integrity Proofs, which Flowa does
  not read either.
- **Remote ids never decide local authority.** Who may delete or edit a post is decided by the stored
  author's `users.id` after the signer is mapped to it, never by comparing strings a remote document
  supplied. Only the origin may delete or update its objects.

### 7.4 Keys of remote actors

- Fetched with a signed GET by the instance actor (so authorized-fetch servers answer), through the
  guarded fetcher, cached in `ap_actors` with the document.
- A failed verification triggers one refetch of the key, at most once per actor per 10 minutes; a
  verification that then succeeds is a rotation. A failed fetch is cached negatively for 10 minutes,
  and a 410 marks the actor gone.
- **A Delete from an actor never seen is answered 202 and dropped, with no fetch.** Mastodon sends
  Delete "to all known peers" when a server self-destructs (its security page, "Linked Data
  Signatures"), and is remembered to do the same for each deleted account, which its documentation
  does not say; fetching the key of each would end in a 410 and cost a request apiece. The check is
  one indexed lookup in `ap_actors` before anything else.
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
| Fetched documents (actors, objects) | 1 MB, `application/activity+json` or `application/ld+json` only (WebFinger: `application/jrd+json`, the type RFC 7033 4.2 names, or plain `application/json`; NodeInfo: `application/json`) |
| Fetched pictures | 8 MB in, 4096 px a side checked with `image.DecodeConfig` before decoding, as `shrinkThumb` does; **one picture decoded at a time** (a mutex), because a 4096 by 4096 picture is 64 MB once decoded and the container has 384 MB |
| Inbox requests per remote host | 300 in 5 minutes in a `limiter` keyed by host, 429 above |
| Inbox handling at once | 4, 8 more may wait 2 s, then 503 with `Retry-After: 30` |
| Inbound queue | 5,000 unprocessed rows; above it the inbox answers 503 with `Retry-After: 120` |

### 7.6 Authorized fetch

An admin option (F11) in `instance_settings`. When on, every federation GET except WebFinger,
NodeInfo, the instance actor and the public key part of an actor needs a valid signature, and the
signer's domain must not be blocked; answers are `Cache-Control: private` with `Vary: Accept,
Signature` (5.6). `TestDBAuthorizedFetch` (**new**).

This matches Mastodon's secure mode (configuration page, `AUTHORIZED_FETCH`): signatures required
on the ActivityPub form of public posts and profiles, profiles answering "only barebones technical
information" without one, off by default because "not all software in the fediverse can support it
fully" and because no caching is possible. The same page notes that secure mode "does not hide HTML
representations", which is true of Flowa's small pages too (F16). Mastodon's allowlist
(`LIMITED_FEDERATION_MODE`) "is based on secure mode": with F8's allowlist and F11 off, Flowa
sends to and accepts from only the listed servers, but any server can still read the public actors
and posts with an unsigned GET. That is a consequence of the two recommendations as they stand, not
a change to them; the owner may want authorized fetch on whenever the allowlist is.

### 7.7 Blocking a server that floods

A host that is refused by its allowance 5 times in an hour is blocked for an hour (`ap_hosts.until`),
and the dashboard says so. An admin's domain block is permanent until removed. Requests from a blocked
host are answered 403 before the body is read.

### 7.8 One way out

Every outbound request goes through one fetcher (`fetcher.do`, **new**, generalising `fetcher.get`):

- The dial guard of `newFetcher` stays: a connection to a loopback, private, link-local or
  reserved address is refused at connect time, after DNS, so a name that resolves to `10.0.0.1` is
  refused. `TestFetcherRefusals` keeps passing unchanged. Two things are added, because the guard
  was written for nine known hosts and now meets any host: **only port 443** is dialled (any port
  with the test setting of 5.1), and `reservedNets` gains the ranges that carry an IPv4 address
  inside an IPv6 one, 6to4 (`2002::/16`) and Teredo (`2001::/32`), with cases in
  `TestPublicIPReserved`.
- Hosts: the preview allowlist for link cards as today; for federation any host the domain policy
  allows (section 10). Only `https`.
- Redirects: at most 3, each re-checked against the scheme, the policy and the dial guard; a redirect
  to another origin is followed only for media and for WebFinger, never for an actor or an object
  (whose id must match where it was fetched from). WebFinger is the exception because RFC 7033 4.2
  lets a WebFinger resource redirect (to `https` only), and Mastodon's `WEB_DOMAIN` setting depends on
  it: the handle's domain answers WebFinger with a 301 to the server's own domain (Mastodon
  configuration page). A WebFinger lookup is never retried over `http` (RFC 7033 4.2: MUST NOT).
- Headers: a GET for a document sends
  `Accept: application/ld+json; profile="https://www.w3.org/ns/activitystreams", application/activity+json`
  (ActivityPub 3.2 and 7: the client MUST ask with the first); a delivery sends the
  `Content-Type` of 7.1; a NodeInfo GET asks for `application/json` (NodeInfo protocol,
  "Retrieval").
- Time: 5 s for a document or a delivery (the delivery is retried later), 10 s for a picture.
- Size: as in 7.5. Content types: as in 7.5, others refused before reading the body. Mastodon guards
  its own fetches the same way, refusing private address ranges unless an operator lists them in
  `ALLOWED_PRIVATE_ADDRESSES` (configuration page).
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
  `ap_deliveries`, with followers on the same host collapsed into that host's shared inbox. The
  unique (activity, inbox) pair is the de-duplication ActivityPub 7.1 requires of the final
  recipient list, and the actor itself is never a recipient (same section). Shared inboxes are
  allowed by 7.1.3, which also requires that an addressee without one still gets the activity.
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
is more than 48 hours old (F9) is given up as `failed`. A 400 or a 401 is first answered by one
immediate attempt in the other signature form (7.1), which does not count as a retry; this is the
same pair of codes on which Mastodon 4.7.0 (unreleased) will switch from cavage to RFC 9421 (its
security page). If that attempt is refused too, a 401 is retried like a failure (the receiver may
not have reached our key yet, or our clock may be wrong), and a 400 gives up. Any other 4xx except
408 and 429 gives up at once (the remote refused it; retrying will not help); 410 for an inbox
removes that follower. A 429 uses its `Retry-After` when it is between 1 minute and 12 hours.
ActivityPub 7.1 asks only that delivery be asynchronous and retried on network errors; the
schedule is Flowa's.

**Nothing is signed while the clock cannot be trusted.** A Raspberry Pi has no battery clock and
starts on the time it was last shut down at, until the network sets it; a signature dated then is
refused everywhere. Workers do not claim rows while the time is earlier than the build's own date or
than the newest `updated_at` the database holds, the dashboard says "waiting for the clock", and
inbound signatures are answered 503 with `Retry-After` in that state instead of 401.
`TestClockGuard` (**new**).

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

Today `main` returns as soon as `ListenAndServe` does, which is the moment `srv.Shutdown` begins:
the 5 s it is given are never waited for, and nothing waits for background work at all. The delivery
item changes that: `main` waits for `Shutdown` to return, the workers have their own context and a
`sync.WaitGroup`, and on SIGTERM they stop claiming and are given 5 s for the request in flight;
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

-- member-level blocks, of accounts and of domains. blocks is migration 8 (INS), local accounts only so
-- far: user_id blocks target_id, never itself, and blocks_target answers "who blocks me". A block hides
-- each side's posts and comments from the other (a moderator blocked by a member still sees that
-- member's), refuses comments, replies and likes across it (taking back one's own like still works),
-- tells nobody of a mention across it, and removes the notifications between the two; the reads ask
-- through blockFree in main.go, one primary key look per row.
CREATE TABLE blocks (user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE, target_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY (user_id, target_id), CONSTRAINT blocks_not_self CHECK (user_id <> target_id));
CREATE INDEX blocks_target ON blocks (target_id, user_id);
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
  AND <blockFree($1, p.user_id)>  -- both directions, as the Community timeline asks it since INS
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

The rows were checked against Mastodon's "Moderation actions" page (docs.joinmastodon.org/admin/moderation/),
its configuration page (/admin/config/) and its user guide "Dealing with unwanted content"
(/user/moderating/). What those pages describe is quoted in the rows. Three rows name features those
pages do not describe: "reject reports" and the public and private comments on a domain block are
remembered from Mastodon's admin screens, and "Sidekiq dashboard" from its admin menu; they are
marked so. Mastodon also has features the table leaves out because Flowa has no use for them yet:
marking an account "sensitive", freezing a local account, appeals of moderation decisions, and
keyword filters.

| Mastodon feature | Decision | Why, and where it lives in Flowa |
| --- | --- | --- |
| Registration open, closed | adopt | the `signups` flag already; becomes the `registration` instance setting with the flag kept as its switch (INS) |
| Registration with approval | adopt | new accounts wait in Accounts for an admin; the sign-up page says so (INS) |
| Invites | later | useful for a household; not needed to federate |
| Server rules shown at sign-up | adopt | instance setting `rules`, shown on sign-up and on the about page; edited in Admin, audited (INS) |
| Instance description, contact, about page | adopt | instance settings, the NodeInfo `nodeName`, an about page for visitors (INS) |
| Announcements | adapt | one admin notice shown at the top of the timeline, a notification kind `announcement` (**new**) (FMD); Mastodon's are published or scheduled from the admin area and can also be emailed |
| Federation modes: open, allowlist, off | adopt | instance setting `policy` (F8); off is the `federation` flag or `FLOWA_MODE=local` (FMD). Mastodon's allowlist is `LIMITED_FEDERATION_MODE`, which also turns on secure mode and turns off public pages (configuration page); see 7.6 for what Flowa's allowlist does without authorized fetch |
| Domain blocks: limit (silence) | adopt | `domain_policies.policy = 'limit'`: their content is shown only to members who follow the account; not in search; a follow from that domain becomes a follow request even to a member who does not approve by hand, as Mastodon's domain Limit "will automatically turn all follows from the limited server into follow requests" (FMD) |
| Domain blocks: suspend | adopt | `suspend`: nothing in or out, existing content and follows purged (FMD). Mastodon: "No content from the server will be stored locally except for usernames", and follows are removed and not restored |
| Reject media, reject reports | adopt | the two columns (FMD). Reject media is on Mastodon's moderation page (no avatars, headers, emoji or attachments from that server); reject reports is not on the pages read (see above) |
| Domain allows | adopt | `policy = 'allow'`, the list in allowlist mode (FMD) |
| Public and private comment on a block | adopt | the two text columns; the public one on the about page (FMD); not on the pages read (see above) |
| Import and export of blocklists | later | CSV in the same format as Mastodon's when an operator asks (Mastodon imports and exports them as `.csv`; the columns are not given on its moderation page) |
| Remote account: limit | adapt | `mute` in `handleModAction` on a remote row means limit (FMD): as Mastodon's account Limit, hidden from everyone but its followers, and its follows of local members become follow requests |
| Remote account: suspend | adapt | `suspend` on a remote row: their content hidden, their follows removed, nothing delivered to them (FMD). Mastodon: suspending a remote account makes it unfollow every local account, and the follows are not restored on unsuspend |
| Remote account: delete data | adopt | a new action `purge_remote` (**new**) (FMD) |
| Reports about remote content | adopt | the existing reports and queue; remote targets are local rows (FMD) |
| Forwarding a report (Flag) | adapt | a checkbox on resolve; sent by the instance actor without the reporter's name (FMD). In Mastodon the reporter chooses to forward, in the report dialog (user guide, "Reporting problematic content"); in Flowa a moderator decides, so a member's report never leaves the server without a moderator's look |
| Receiving reports | adopt | Flag in, as a report from the remote instance (FMD) |
| Authorized fetch | adopt | instance setting, off by default (F11) (FMD); Mastodon's `AUTHORIZED_FETCH`, also off by default (7.6) |
| Content retention for remote posts and media | adopt | instance setting `retention_days`, the 200 MB cache (FMD) |
| Custom emoji | never | pictures from every server for decoration; a Pi's disk and a moderation surface for little |
| Relays | never | a relay floods a small instance with posts nobody follows |
| Trends, explore, public federated timeline | never | Flowa is not a discovery service; the Home timeline is what a member chose |
| Hashtag following | later | once there are hashtags beyond props |
| User blocks and mutes of accounts | adopt blocks, later mutes | blocks are needed for safety at once (INS for local, FWR for remote); mutes are a convenience |
| User blocks of domains | adopt | `domain_blocks_member` (FMD); as in Mastodon ("Hiding an entire server"), the member also loses their followers on that server |
| Follow requests for locked accounts | adopt | `users.locked`, `manuallyApprovesFollowers` (FWR) |
| Account migration (Move), alsoKnownAs | adopt | PRT |
| Account export (posts, follows, blocks) | adopt | PRT; useful in local mode too |
| Full-text search of remote content | never | the brief says handle only; indexing remote text costs disk and is a privacy question |
| Email notifications, password reset by email | never | "Not planned" in the roadmap: no email is stored |
| Direct messages | never | "Not planned" in the roadmap |
| Polls | later | shown as text (6.6) |
| Admin roles with custom permissions | adapt | the existing three roles; two new permissions in `rolePerms`: `federation.read` (moderators and admins) and `federation.manage` (admins) |
| Audit log | adapt | every federation admin action through `audit` into `mod_actions` |
| Sidekiq dashboard | adapt | the federation tile and page on the admin dashboard (section 14.2); not on the pages read (see above) |
| IP blocks, email domain blocks | never | no email; IP blocks belong to the operator's proxy (Mastodon has both: "IP Rules" and e-mail domain blocks on its moderation page) |
| Single-user mode | adopt | `FLOWA_MODE=single` (INS). Mastodon's `SINGLE_USER_MODE` redirects the front page to the first profile and turns registrations off (configuration page), as section 4.2 has it |

## 11. The member's side

- **Opting in** (F2): a switch in Settings, "Share my profile and public posts beyond this server",
  with a sentence on what goes out (profile, Public posts, comments on Public posts, likes of remote
  posts) and what never does (section 14.5). Turning it off stops all delivery and makes the actor
  and its posts answer 404 to other servers; it sends nothing. Asking other servers to forget the
  account is a separate button, "Remove me from other servers", which sends Delete(Person); the name
  stays the member's, and turning the switch on again later makes new keys. A server that processed
  the Delete may refuse the same address for good (believed of Mastodon; its ActivityPub page says
  only that a Delete of an account removes it "from the database, as well as all of their
  statuses", and what happens when the address comes back is in its code, which could not be
  opened), and the page says so before the button sends anything.
- **Audience per post:** Public or This server, defaulting to Public for members who opted in.
- **Following:** a Follow button on profiles (local and remote); "people elsewhere": a search box
  that takes a handle (`@bo@social.example`) or a profile URL only, resolves it by WebFinger, and
  shows the profile with its public posts from the outbox's first page. No full-text search. A
  handle is resolved as Mastodon's WebFinger page describes: `acct:bo@social.example` asked of
  `social.example` over HTTPS only, the `self` link of an ActivityStreams type followed to the actor,
  and the actor accepted only if WebFinger for its own `preferredUsername` and host gives the same
  actor back (the actor may live on another host, such as `ap.social.example`, when that host's
  WebFinger names the handle as `subject`). The host is compared without regard to case; the name
  is kept as the remote server answered it, because the SWICG WebFinger report says implementers
  "SHOULD NOT assume case insensitivity for external usernames", even though Mastodon's are.
- **Follow requests:** a member can approve followers by hand (`users.locked`); requests come as
  notifications with Approve and Decline.
- **Home timeline** beside Community (F4). Remote posts show the handle (`@bo@social.example`), the
  server's name, and "Open on its server".
- **Replies and likes:** remote replies appear under a local post like local ones, with the handle;
  remote likes count with local ones; notifications for follows, follow requests, mentions, replies,
  likes and shares.
- **Blocks:** a member blocks an account or a domain; blocked content disappears for them, and the
  blocked account cannot follow, reply or like (refused in the inbox). Blocking a domain also removes
  the member's followers there, as Mastodon's "Hiding an entire server" does.
- **The handle on the profile:** `@ana@flowa.example.org`, with a copy button, when the member
  federates.
- **Privacy defaults:** opt-in off, `discoverable` and `indexable` off, followers and following lists hidden,
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

This sandbox cannot reach other fediverse servers. The specifications and Mastodon's documentation
were opened on 12 November 2026 (see Sources), but documentation is not a running server.
Interoperability is shown in four ways, in rising order of trust, and the last is the owner's:

1. **Specification vectors.** The signature test keys and examples named in 7.1 (cavage-12 C.1 and
   C.2, RFC 9421 4.3 with the key of B.1.1, the signature bases of RFC 9421 2.5 and B.2.1 to B.2.3),
   in `testdata/federation/vectors/`, checked by `TestSignatureVectors`. Copied from the texts by
   FRD's signatures item; this note only names them.
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
| 2 | `feat(server): an instance mode setting that changes nothing by default` | INS 2 | `FLOWA_MODE`, the route list, `TestDBLocalModeUnchanged`; no table yet |
| 3 | `feat(server): member blocks` | INS 3 | useful locally, needed for federation; the first migration of the plan, so the schema 7 fixture lands here |
| 4 | `feat(server): follows and a home timeline behind a flag` | INS 4 | `follows`, the `follows` flag off |
| 5 | `feat(server): instance information, rules and sign-up approval` | INS 5, 6 | `instance_settings` lands here, with its first reader; may be two pull requests |
| 6 | `feat(server): single-profile mode` | INS 7 | closed sign-up, root page, public profile page |
| 7 | `feat(server): public origin, key secret and keys` | FRD 1 | refuses without them |
| 8 | `refactor(server): one guarded fetcher for previews and federation` | FRD 2 | `TestFetcherRefusals` unchanged |
| 9 | `feat(server): http signatures with the specification vectors` | FRD 3 | sign and verify, both forms |
| 10 | `feat(server): webfinger and nodeinfo` | FRD 4 | |
| 11 | `feat(server): actor documents and public pages` | FRD 5 | content negotiation, picture URLs, retired names (a name is public from here on) |
| 12 | `feat(server): outbox and collections` | FRD 6 | |
| 13 | `feat(server): remote actors by signed fetch, and search by handle` | FRD 7, 8 | fixtures land here; the first remote rows, so `-purge-remote` and the rollback warning too |
| 14 | `feat(server): the inbox and its queue` | FWR 1, FMD 1 | guard exemption, limits, domain policies and the allowlist |
| 15 | `feat(server): a delivery queue in postgres` | FWR 2 | workers, backoff, hosts |
| 16 | `feat(server): follows across servers` | FWR 3 | |
| 17 | `feat(server): public posts go out as notes` | FWR 4 | alt text field, `@name@host` in `mentionRE` and `richText` |
| 18 | `feat(server): posts from followed accounts in the home timeline` | FWR 5 | picture cache, `htmlToText` |
| 19 | `feat(server): replies and likes across servers` | FWR 6 | |
| 20 | `feat(server): account deletion across servers` | FWR 7 | Delete(Person) both ways |
| 21 | `ci: two instances federating` | FWR 8 | the `federation` suite |
| 22 | owner's trial | FWR 9 | no pull request; results in the todo |
| 23 onward | FMD items 2 to 8, then PRT | | |

### 14.4 What `./deploy.sh --rollback` can and cannot go back past

- **How it works.** `rollback()` in `deploy.sh` restarts `nunchaku:previous` on the database as it
  is; `checkHealth` reports healthy while `Schema >= Migrations`.
- **Can: every pull request above.** Every migration adds tables, or columns with constant defaults,
  and an older build ignores them.
- **But, from pull request 13 on, an older build shows remote rows as local.** It does not know
  `domain` or `ap_id`, so the people list would list remote accounts (from 13) and the community
  timeline would show remote posts (from 18). Pull request 13 says this in its description and adds
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
  and the clock window are now taken from the specifications and Mastodon's documentation (see
  Sources), but documentation can lag the code, and a few details are only in Mastodon's code, which
  could not be opened (listed in Sources). The fixtures and the two-instance test prove Flowa agrees
  with itself and with recorded documents, not that Mastodon accepts what Flowa sends. Federated mode
  is marked experimental until the trial.
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

The first version of this note was written where no specification could be opened, and said so.
On 12 November 2026 every text below was downloaded and read, and each statement in the note and in
the five checklists that depends on one was compared with it: the ones the texts confirm now name
their source and section, the ones they contradict were corrected, and the ones no text settles are
listed at the end. Mastodon's documentation describes Mastodon as of its "last updated" date, not
every running version; that date is given where the page shows one.

| Document | Address | Read | Checked against it |
| --- | --- | --- | --- |
| W3C ActivityPub (Recommendation, 23 January 2018) | https://www.w3.org/TR/activitypub/ | 12 November 2026, sections 3 to 7 and the headings of B | ids and transient activities (3.1), retrieval and `Accept` (3.2), actor properties (4.1), OrderedCollections and order (5), outbox, inbox and de-duplication (5.1, 5.2), followers (5.3), the public collection and its three forms (5.6), Tombstone, 410 and 404 (6.4), Block not delivered (6.9), Undo by the same actor (6.10), content types for POST and GET (7), delivery targets, de-duplication and retries (7.1), forwarding from the inbox (7.1.2), shared inbox (7.1.3), Update as replacement (7.3), Delete (7.4), Follow, Accept and Reject (7.5 to 7.7) |
| W3C Activity Streams 2.0 Core | https://www.w3.org/TR/activitystreams-core/ | 12 November 2026, the media type and date sections | `application/activity+json` and its equivalence with the `ld+json` profile (8.1), date-time format |
| W3C Activity Vocabulary | https://www.w3.org/TR/activitystreams-vocabulary/ | 12 November 2026, audience targeting (5.1) and the type list | `to`, `cc`, `bto`, `bcc`; Tombstone, Mention and Image exist as types; `Hashtag` and `sensitive` are not in it (they are the `as:` extensions Mastodon's page lists) |
| RFC 7033 WebFinger | https://datatracker.ietf.org/doc/html/rfc7033 | 12 November 2026, sections 4, 5 and 9 | HTTPS only (4, 4.2, 9.1), `resource` exactly once, 400 and 404 (4.2), redirects (4.2), `application/jrd+json` (4.2, 10.2), `rel` (4.3), `subject` SHOULD be present (4.4.1), CORS (5), personal data only when authorised (9.2) |
| RFC 6415 Host Metadata | https://datatracker.ietf.org/doc/html/rfc6415 | 12 November 2026, the document's form only | what host-meta is, for 5.3's "later" |
| RFC 9421 HTTP Message Signatures | https://datatracker.ietf.org/doc/html/rfc9421 | 12 November 2026, sections 2.2, 2.3, 2.5, 3.1 to 3.3, 4.3 and Appendix B | `@target-uri` and `@authority` (2.2.2, 2.2.3), signature parameters (2.3), the signature base (2.5), verifying and the algorithm rules (3.2, 3.2.1), RSASSA-PKCS1-v1_5 with SHA-256 (3.3.2), multiple signatures and the `rsa-v1_5-sha256` example (4.3), the test keys and cases (B.1, B.2) |
| RFC 9530 Digest Fields | https://datatracker.ietf.org/doc/html/rfc9530 | 12 November 2026, sections 1, 2, 5 and Appendix E | `Content-Digest` as a dictionary of byte sequences (2), algorithm keys (5), `Digest` obsoleted (1, Appendix E) |
| RFC 3230 Instance Digests | https://datatracker.ietf.org/doc/html/rfc3230 | 12 November 2026, 4.1.1 only | digest algorithm names are case-insensitive |
| draft-cavage-http-signatures-12 | https://datatracker.ietf.org/doc/html/draft-cavage-http-signatures-12 | 12 November 2026, sections 2 and 3.1, Appendix C and E.2 | parameters (2.1), `algorithm` and its registry (2.1.3, E.2), `created` in the future (2.1.4), the default `headers` (2.1.6), the signing string (2.3), verifying (2.5), the test values (Appendix C, re-run while writing this) |
| SWICG report "ActivityPub and HTTP Signatures" | https://swicg.github.io/activitypub-http-signature/ | 12 November 2026, whole | `hs2019` read as `rsa-sha256`, how to obtain and match a key, the time window in practice, instance actor, Deletes of actors, `Vary: Signature`, double-knocking between versions |
| SWICG report "ActivityPub and WebFinger" | https://swicg.github.io/activitypub-webfinger/ | 12 November 2026, whole | the round trip from handle to actor and back, the `self` link, Mastodon's username rules, case of remote usernames, redirects |
| NodeInfo protocol and the 2.1 schema | https://nodeinfo.diaspora.software/protocol.html and https://nodeinfo.diaspora.software/ns/schema/2.1 | 12 November 2026, whole | the well-known JRD and its relation names, schema versions 1.0 to 2.1, retrieval and content type, every required field and its meaning |
| Mastodon: ActivityPub | https://docs.joinmastodon.org/spec/activitypub/ | 12 November 2026 (page last updated 30 March 2026) | the activities and object types it handles, conversion of other types, the HTML it keeps, the properties it reads on posts and profiles, the `toot:`, `as:` and `schema:` terms, Block, Flag, Move, polls, visibility from addressing, public key, featured, profile fields, the discoverable, indexable, suspended and memorial flags, hashtags, emoji, focal points, blurhash, sensitive, secure mode |
| Mastodon: Security | https://docs.joinmastodon.org/spec/security/ | 12 November 2026 (page last updated 20 July 2026) | the cavage form it sends and verifies, `Digest` on POST, the 12-hour window, the `keyId` footnote, RFC 9421 as it accepts it and its version history, Linked Data signatures and Object Integrity Proofs |
| Mastodon: WebFinger | https://docs.joinmastodon.org/spec/webfinger/ | 12 November 2026 (page last updated 30 March 2026) | what it asks and checks, the reverse lookup, FEP-2c59 |
| Mastodon: Microformats | https://docs.joinmastodon.org/spec/microformats/ | 12 November 2026 (page last updated 14 January 2026) | `h-card` on mentions, `u-url`, `mention`, `hashtag` |
| Mastodon: Configuring your environment | https://docs.joinmastodon.org/admin/config/ | 12 November 2026 (page last updated 8 October 2026), the federation and encryption settings | `LOCAL_DOMAIN`, `WEB_DOMAIN`, `ALLOWED_PRIVATE_ADDRESSES`, `AUTHORIZED_FETCH`, `LIMITED_FEDERATION_MODE`, `SINGLE_USER_MODE`, Active Record Encryption |
| Mastodon: Moderation actions | https://docs.joinmastodon.org/admin/moderation/ | 12 November 2026 (page last updated 15 January 2026) | account and domain Limit and Suspend, reject media, blocklists, announcements, IP and e-mail blocks |
| Mastodon: Dealing with unwanted content | https://docs.joinmastodon.org/user/moderating/ | 12 November 2026 (page last updated 21 November 2025) | blocking, muting, hiding a server, forwarding a report |
| Mastodon: Moving or leaving accounts | https://docs.joinmastodon.org/user/moving/ | 12 November 2026 (page last updated 27 November 2025) | aliases before Move, export and import as CSV, the post archive |

Not opened: Mastodon's source code (github.com cannot be reached from here except for this
repository), FEP-2c59, FEP-8b32 and FEP-e2ce (codeberg.org is not reachable from here), RFC 7565 (the
`acct` scheme; the SWICG WebFinger report quotes its grammar), and any other server's documentation.

**Still unverified, and what would settle each:**

- The instance actor's WebFinger form, `acct:flowa.example.org@flowa.example.org` (5.3): not in
  Mastodon's documentation. Settled by Mastodon's WebFinger controller or by a WebFinger answer
  recorded from a Mastodon server for its own host.
- Which property a current Mastodon writes alt text in, `name` or `summary` (6.5): its page names
  only `summary`. Settled by a recorded Mastodon post with alt text (FRD's fixtures).
- The exact nesting of a mention's `h-card` (6.5) and whether a hashtag `href` with a fragment is used
  as is (6.5): the pages give the classes, not the nesting, and a plain path in the example. Settled
  by a recorded Mastodon post and the owner's trial.
- Whether Mastodon reads the cavage `algorithm` field, and so whether `rsa-sha256` or `hs2019` makes a
  difference to it (7.1); how far into the future it lets a `Date` be; whether it wants
  `Content-Digest` on an RFC 9421 GET: none is on its security page. Settled by Mastodon's
  `signature_verification.rb` or by the owner's trial.
- How Mastodon treats an activity forwarded by a server that is not its actor and carries no
  Linked Data signature (6.4, forwarding): not on its pages. Settled by its inbox code or the owner's
  trial.
- Mastodon's delivery retry horizon (F9), how it stores private keys (F7), whether it sends a
  Delete to every known server for each deleted account (7.4), whether it refuses an address that
  returns after a Delete (11), and whether it falls back to host-meta (5.3): all in its code only.
- Mastodon's username rules (finding 11): from the SWICG WebFinger report, which calls itself a
  snapshot. Settled by the owner's trial with a name ending in a dot.
- "Reject reports", the public and private comments on a domain block, and the Sidekiq dashboard
  (section 10): not on the moderation or configuration pages. Settled by Mastodon's admin screens or
  its code.
- The column layout of Mastodon's CSV files for blocklists and following lists (section 10, PRT): the
  pages say only "CSV". Settled by a real export.
- That Misskey sends emoji reactions as a Like with content (6.3): no Misskey text was opened.
  Settled by a recorded Misskey activity.
