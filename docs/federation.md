# Federation: design and plan

The owner asked for ActivityPub, so that Flowa can run as a single-profile instance, as a federated
community, or as a local-only community as it does today, with what an operator expects from
Mastodon's administration in mind. This page is the design note for that work: what the code has
today, what federation needs, and the order of pull requests. The epics are
[INS](epics.md#ins-instance-modes-and-settings), [FRD](epics.md#frd-federation-read-side),
[FWR](epics.md#fwr-federation-write-side), [FMD](epics.md#fmd-federation-moderation-and-administration)
and [PRT](epics.md#prt-portability), with checklists under `docs/epic-<code>/todo.md`; their order
is in [roadmap.md](roadmap.md#third-cycle).

**It is a proposal waiting for the owner's review.** Nothing in it was built when it was written; what has
been built since is marked "As built" in its section and listed in the epics' todos (as of FRD 8: the modes,
the origin, the keys, the guarded fetcher, HTTP signatures, discovery, the actor documents and public
pages with the post audience and retired names, the outboxes and collections, remote accounts with the
cache of remote actors, and the search by handle that calls it; a federating instance can be read, its members
can look up people elsewhere; and as of FWR 1 with FMD 1's domain policies, an inbox that takes deliveries and
acts only on an account deleting itself or changing its profile, 7.10; as of FWR 2 a delivery queue; as of FWR 3
follows across servers, in and out, with approval by hand, 6.4; and as of FWR 4 Public posts sent as notes to followers
elsewhere, with their deletions and profile changes, 6.5). The decisions come first, because the rest depends on them. Until the owner answers one, the plan
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
   `@name@host` form before the local one (FWR, posts in). Built in FWR 4 (6.5, "Public posts out, as built"):
   `parseMentions` and the page's `mentionsIn`, held to one set of cases.
10. **Usernames can be reused.** `handleDeleteUser` deletes the row and nothing keeps the name, so a
    new sign-up could take it and inherit another person's identity on every server that knew it.
    Federation keeps a list of retired names (section 9; built in FRD 5, 5.2).
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
16. **Behind Docker's userland proxy a client chooses its own address** (found in the second review of #111; older
    than federation, and the same for every allowance; not changed here, for the owner to decide). The condition:
    `FLOWA_TRUSTED_PROXIES` is unset, so its default trusts `172.16.0.0/12`, and the connection reaches the
    container through Docker's userland proxy (`docker-proxy`, which handles a published port when Docker does not
    rewrite the address with iptables: with `"userland-proxy": true` and iptables off, or for connections the host
    makes to its own published port). The connection then comes from the bridge's gateway (`172.17.0.1` or the
    like), which is trusted, and `docker-proxy` passes the client's bytes unchanged, so `clientIP` believes the
    `X-Forwarded-For` the client wrote itself: a client can name any address per request and step around every
    per-address allowance (sign-in and sign-up limits, discovery, public pages, the inbox). With Docker's usual
    iptables forwarding the container sees the client's own address and this does not happen; behind a reverse
    proxy that sets `X-Forwarded-For` itself, the proxy's entry is the one read.

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

As built (FRD 4): all three modes start. `FLOWA_FEDERATE` (default `off`) is read beside the mode, before
the database is opened: in single mode unset, empty or `off` (any case, spaces around forgiven) is off, `on` is on, and any
other value stops the start with a message naming the setting; in local and federated mode it is ignored
whatever it says. "The mode federates" means federated mode, or single mode with `FLOWA_FEDERATE=on`, and
only then are `FLOWA_ORIGIN` and `FLOWA_KEY_SECRET` read (a message from them names the mode and, in single
mode, the setting). **What a federating instance gets in this version is discovery only**: WebFinger and
NodeInfo (5.3, 5.4), and nothing else: no actor documents, no inbox, nothing is delivered. The start says so
once in the log ("federation: this version answers discovery only (WebFinger and NodeInfo, for the members
who opted in): no actor documents, no inbox, nothing is delivered"), and the README and the admin
dashboard's mode line say the same.

As built (FRD 5): what a federating instance does is now one sentence, `fedBuilt` in `main.go`, which the start
logs once ("federation: this version answers discovery (WebFinger and NodeInfo) and serves the profiles and
Public posts of the members who opted in, to other servers and to people in a browser; it has no inbox, so
nobody can follow anyone yet, and nothing is delivered"), the dashboard shows as the server sends it, and
NodeInfo's `metadata.federationBuilt` lists as parts (`webfinger`, `nodeinfo`, `actors`, `notes`). The sentence
of FRD 4 and its two constants are gone, so there is one copy.

As built (FRD 6): the sentence now adds the outboxes and the counts ("..., to other servers and to people in a
browser, with each member's outbox of Public posts and the counts of their followers and follows; it has no
inbox, so nobody can follow anyone yet, and nothing is delivered"), and the parts add `outbox` and
`collections`.

As built (FRD 8): the sentence adds the search by handle ("..., and lets members look up people on other servers
by their handle and read their profiles and latest public posts; it has no inbox, ..."), and the parts add
`lookup`.

As built (FWR 1): the sentence now ends "..., and takes deliveries from other servers at its inboxes (only from the
servers the admin's domain policy lets in, and only while the Federation switch is on) but acts only on an account
deleting itself or changing its profile: follows, posts, replies and likes sent to it are counted and dropped, so
nobody can follow anyone yet, and nothing is delivered", and the parts add `inbox` (7.10).

As built (FWR 3): the sentence now ends "..., and takes deliveries from other servers at its inboxes (only from the
servers the admin's domain policy lets in, and only while the Federation switch is on) and acts on follows and their
answers and on an account deleting itself or changing its profile, so members can follow people on other servers and
be followed from them (approving each follower by hand when they choose), and sends those follows, answers and
unfollows; posts, replies and likes sent to it are counted and dropped, and no post is sent yet", and the parts add
`follows` (6.4, "Follows across servers, as built").

As built (FWR 4): the sentence now ends "..., and sends those follows, answers and unfollows, and sends the Public posts of
members who share their profile to their followers on other servers, with a post's deletion and a profile's changes;
posts, replies and likes sent to it are counted and dropped", and the parts add `posts` (6.5, "Public posts out, as
built").

As built (FWR 5): the sentence now ends "..., with a post's deletion and a profile's changes, and keeps the posts of
the accounts its members follow on other servers for those members' Home (their pictures copied into its own bounded
cache, a member told when one mentions them or shares their post), while replies and likes sent to it are counted and
dropped", and the parts add `home` (6.6, "Remote content in, as built").

As built (FWR 6): the sentence now ends "..., a member told when one mentions them or shares their post), and takes
replies and likes from other servers to its members' Public posts and comments, and sends its members' comments on
Public posts and their likes and comments on public posts of other servers, with an edit, a deletion and an unlike",
and the parts add `replies` (6.4, "Replies and likes across servers, as built").

### 4.2 What each mode means for every surface

| Surface | `local` (default) | `federated` | `single` |
| --- | --- | --- | --- |
| Port, addresses, `compose.yml` ports | as today | as today | as today |
| Sign-up | the `signups` flag, and by approval once an admin chooses it (INS 6; nothing chosen is as before) | as local | closed for good: `handleSignup` answers 403 whatever the flag says |
| Accounts | many | many | exactly one local account, the owner (F15) |
| Root page for a signed-out visitor | as today (Welcome) | as today | the owner's profile (F16) |
| Timelines | Community | Community (local posts) and Home (followed accounts) | Home (followed accounts) and the owner's posts |
| People list | as today | local members only; remote accounts by handle search | the owner's following |
| Follows | off unless the `follows` flag is on (local only) | local and remote | the owner follows; others follow the owner |
| Federation endpoints (WebFinger, NodeInfo, actors, inboxes, outboxes) | none: 404, as for any unknown path | answer (as built in FRD 6: WebFinger, NodeInfo, actors, posts, outboxes and collections; no inbox) | answer only with `FLOWA_FEDERATE=on` (the same) |
| Outbound connections | link previews only, as today | link previews and federation | as federated, or link previews only |
| Public pages for signed-out visitors | none, but the about page once an admin has saved a description (INS 5) | as local, plus profiles and Public posts of members who opted in | as local, plus the owner's profile at `/` (INS 7); Public posts once posts have an audience (FRD) |
| Feature flags | as today | `federation` (pause), `follows` | as federated |

**Single mode as built (INS 7).** The owner's profile at `/` is a small page the server writes from a Go
`html/template` with the CSP of 5.6: display name, username, bio, links, the profile picture (at
`/profile-picture`, the one public picture address, for the owner alone) and the instance information. It
shows no post without `FLOWA_FEDERATE=on`. As built (FRD 5): with it on, the page also lists the owner's newest
20 Public posts (the post's text with its links, its chips and combo as text, its picture at the public
picture address) and links the profile page `/users/{name}` for the rest; the page is made again when the owner
posts or deletes a post, after any moderator's action, and when the opt-in changes (`TestDBVisitorPageFollows`;
nobody can moderate the owner, who is the one account and an admin, but the page would follow if anyone could). Without it the page is byte for byte as before, and the owner's posts are seen by the
owner alone, signed in. It shows no number
(14.6). The People list is hidden, and Follows and Home stay behind their flag with nobody to follow until
FWR. `FLOWA_FEDERATE` is read since FRD 4: with it on, the instance answers WebFinger for the owner (whose
opt-in, F2, is that setting: the `federates` column is not written for them, so going back to local mode
leaves no opt-in behind, and Settings shows the handle without a switch) and NodeInfo, and the owner's key is
asked for at start after the instance actor's (5.7). Its routes are those of single mode and the three of
`testdata/routes-federated.txt`. A signed-in owner gets the app at `/`; signed out, the app stays at
`/index.html`. Sign-up, the routes for requests to join, and deleting, demoting or handing over the one
account answer 403. The start refuses more than one account, no account without `ADMIN_USER`, an
`ADMIN_USER` that would make a second one, and a sole account that is suspended; a sole member is made the
admin and the owner.

**Local means today.** In local mode the federation routes are not registered on the mux at all, the
queue workers are not started, and no key is generated. `TestDBLocalModeUnchanged` (**new**) builds
the route table with no new setting and compares its patterns with a list checked in today, and runs
the start-up with a fetcher that fails the test on any dial.

### 4.3 Moving between modes, and what cannot be undone

- `local` to `single`: allowed when one local account exists (F15). Nothing is announced unless
  `FLOWA_FEDERATE=on`.
- `local` or `single` to `federated`: allowed. The first time the server federates (it answers a
  WebFinger request, or delivers anything), it records the origin and the time in `instance_settings`
  (`origin`, `first_federated_at`). As built (FRD 4, which moved it from FRD 1's first key): the two rows are
  written in one transaction (`recordOrigin`, from `ensureOrigin`) just before the first answer that names
  a member's actor id to another server: in this version a WebFinger 200 for a member, to a GET (a HEAD gets
  the same headers and no body, so it records nothing). The instance actor's
  WebFinger answer does not freeze it: anyone can ask for `acct:host@host` (a scanner, the operator's own
  check), and that must not lock a mistyped origin in. As built (FRD 5): a member's actor document and a
  Public post's Note call it too, before the first byte goes out, and a moved origin turns either into the 404;
  the instance actor's own document does not, by the reasoning of its WebFinger answer (it names no member, and
  anyone may ask for it), and neither do the HTML pages, which are for people and name no id to a server that
  stores it. The first request the instance actor signs (FRD 7, a signed fetch names `/actor#main-key` to
  another server) and the first delivery will call the same function when they come. As built (FRD 7): the
  actor cache calls it before it signs its first fetch (`actorCache.get`), so the first actor fetched freezes
  the origin if no member's id has; a refused federation fetches nothing. Making keys records nothing:
  a key holds no origin (`ap_keys` keeps the part of the key id after the origin, and the sealing is bound to
  the key id and the owner), so a key made under one origin serves under another. NodeInfo is no
  announcement: its discovery document names the origin in an `href`, but no actor id, and nothing another
  server stores as an identity changes when the origin does. Until the first announcement a start with a
  different `FLOWA_ORIGIN` simply takes it, and every start says in one log line that the origin is not
  announced yet; the dashboard says "not announced yet" until then and the date after. Races: two first
  answers at once in one process take turns on a mutex, and the second finds the origin recorded; a first
  answer while another transaction writes the rows waits for its commit (`INSERT ... ON CONFLICT DO
  NOTHING`) and reads its row. One process runs per database, so an origin recorded by another process after
  this one started is not a case Flowa sets up; if two did overlap with two origins, the second to record
  finds the other origin and refuses federation, before any id goes out under it. A database that a
  development build of FRD 4 before this rule started in a mode that federates has its origin recorded
  already (that build recorded it with the instance actor's first key), and its dashboard shows "first
  announced" with that date; no released build could start in such a mode, so only a development
  database can be in that state.
- **Frozen once federated.** An actor id has been announced to other servers and cannot change
  (Mastodon's configuration page says the same of its `LOCAL_DOMAIN`: changing it makes remote
  servers "confuse your existing accounts with entirely new ones"). So the public origin and every username that has federated are frozen. If `FLOWA_ORIGIN` later
  differs from the recorded origin, the server starts, serves the app as in local mode, logs
  "federation refused: the origin was https://a and is now https://b; actor ids cannot move", shows
  it on the dashboard, and federates nothing. Moving a whole instance to a new name is not supported
  (members move one by one with PRT's Move). As built (FRD 4), the instance actor's own id is the one
  exception until its first signed request freezes the origin (built in FRD 7, above): before a member is
  announced or an actor fetched, an instance answers `acct:host@host` under whatever `FLOWA_ORIGIN` it was
  started with.
- `federated` back to `local`: allowed, but the remote copies stay where they are. In local mode
  after federating, the actor URLs answer **410 Gone** with a Tombstone (ActivityPub 6.4: 410 when a
  Tombstone is the body) and WebFinger answers 404 (RFC 7033 4.2 asks for 404 when the server has no
  information); these are the only federation answers local mode gives, and only on an instance that
  recorded `first_federated_at`, so remote servers stop retrying. (Not built yet: in local mode the
  discovery addresses are unknown addresses, a 404 without the CORS header, and so are the actor and public
  page addresses of FRD 5: these answers of local mode are left for the item that builds them, since FRD 5
  serves only a mode that federates. While federation is refused in a mode that federates, as built in FRD 4
  and 5, the discovery, actor and public addresses stay registered and answer the 404 of an unknown name to
  everything, discovery with the CORS header.) An admin who wants remote servers to forget the accounts first runs "Leave the
  fediverse" in federated mode (**new**, FMD), which sends Delete for each actor and waits for the
  queue to drain.
- Retired usernames stay retired in every mode.

## 5. Identity and discovery

### 5.1 Public origin

`FLOWA_ORIGIN` (**new**): `https://` plus a host and nothing else (no path, no query, no user, no
port unless needed). Without it, or with `http://`, federation is refused at start with a message.
Its host is added to the names `allowedHost` accepts. As built (FRD 1): an IP address, `localhost` and
single-label or private names are refused, a host in another script must be in its `xn--` form, a trailing
dot is refused, and capitals, one trailing slash and `:443` are forgiven and written away, so one origin has
one written form (actor ids are compared as text). It can change freely until it is first announced, and
is frozen from then on (4.3, as built in FRD 4; the instance actor's id follows it until then). A test-only setting,
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

Correction (FRD 6): the Create of a post is not a random activity id but the post's full address with
`/activity` after it (`https://flowa.example.org/users/ana/posts/812/activity`), as Mastodon names a status's Create
(`/users/{name}/statuses/{id}/activity`). It is made from the post whenever it is needed (the outbox now, the
deliveries of FWR 4 later), so it is the same every time with nothing stored, and it answers at that address
(6.2, "As built (FRD 6)"). The random form stays for activities that are rows of their own (a Follow, a Like,
an Update). Featured is at `/users/ana/featured` as the table says, not under Mastodon's `/collections/`:
readers follow the actor's `featured` link, never a guessed path.

As built (FRD 5): `retired_usernames` (migration 14, with `flowa-schema13.dump` for the restore drill). A
name is retired when an admin deletes an account that had opted in or ever had a key (a key is made only on
opting in, and for the owner of a single-profile instance with `FLOWA_FEDERATE=on`, whose opt-in leaves no
column; it stays when the member opts out), in the delete's own transaction and before the row goes, in
every mode (4.3: retired names stay retired whatever the mode). A name of an account that never opted in is
free again, as before. A retired name is refused at sign-up, at a request to join, at approving a request
that waited under it, and for `ADMIN_USER` (the start goes on without that account while others exist, and
stops with the reason when there would be none), always in the words of a taken name ("That username is
taken."), so the answer does not say whether an account once had it. The transaction that retires a name and each
that makes an account (sign-up, approving a request, `ADMIN_USER`) first take a lock on that one name
(`nameLockSQL`, an advisory transaction lock keyed by the name): without it an insert that waited on the
unique index for a delete went ahead, once the delete committed, with a snapshot from before the
retirement. Under READ COMMITTED each statement takes a new snapshot when it starts, so an insert sent after
the lock is granted sees the retirement (`TestDBRetireRace`). The password is hashed before the lock is
taken, so sign-up waits only for another transaction on the same name. The actor of a retired name answers
410 with a Tombstone, an accepted disclosure that an account under it once shared its profile (5.5, "As built
(FRD 5)", says why). No endpoint renames an account, so a
name that has federated cannot move (4.3); `TestDBRetiredNames` holds that `PUT /api/me` takes no name. The
instance's own host is taken the same way in a mode that federates (5.3 found that a member so named is
shadowed by the instance actor in WebFinger): it is refused at every door above, and a member who already has
that name keeps the account but is refused the opt-in, with the reason. A member of that name who had opted
in before this version stays opted in, and can be reached only by their actor id.

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

As built (FRD 4), checked against RFC 7033 and Mastodon's WebFinger page, with these decisions where
neither says, and one correction:

- **Correction (FRD 5):** the profile-page link is now in every answer, `{"rel":
  "http://webfinger.net/rel/profile-page", "type": "text/html", "href": <the id>}`, since `/users/{name}` and
  `/actor` answer a browser with a page (5.6); `rel` keeps only the links asked for. The two paragraphs below
  are as FRD 4 left them.
- **Only the `self` link, and the id as the one alias.** The profile-page link of the example above points
  at a page this version does not serve (the HTML profile is FRD 5), so it is left out until then; RFC 7033
  4.4 makes every link optional and Mastodon needs only `self`. The `self` link points at `/users/{name}`,
  which **this version does not serve either** (actor documents are FRD 5): a server that follows it gets the
  app's 404 until then, so Mastodon cannot resolve a Flowa account yet. The JRD is written from typed structs;
  `links` is `[]`, never absent, when `rel` leaves none.
- **The resource.** Exactly one `resource` (absent, empty or repeated: 400); at most 300 bytes once decoded
  and a query of at most 2,048 bytes (400 beyond); a space, a control or a non-ASCII byte is 400 (a URI is
  ASCII, RFC 3986 2); a query that does not parse (a bad `%` escape, a `;`) is 400. Accepted: `acct:` in any
  case (RFC 3986 3.1); the handle without the scheme, as `name@host` or `@name@host`, which is how Mastodon
  writes handles for people, with one leading `@` forgiven in every form; the name in any case (Flowa's names
  are lower case, so `Ana` can only mean `ana`; the subject is the name as stored, which RFC 7033 4.4.1
  allows); the host compared with the origin's host without case and with one trailing dot taken off. An
  `acct:` host with a port (`acct:ana@flowa.example.org:443`) names nobody (404): RFC 7565 section 7 gives
  the syntax as `acctURI = "acct" ":" userpart "@" host`, with RFC 3986's `host` rule, which has no port (the
  port belongs to `authority`). RFC 7565 section 4 asks that two acct URIs be compared after case and
  percent-encoding normalization (RFC 3986 6.2.2.1 and 6.2.2.2), so `acct:%61na@...` is `ana`, and the URL
  form also takes an unreserved character percent-encoded (`/users/%61na`), but not an encoded slash. The actor URL form is compared as an id is written: `https`, the path exactly
  `/users/{name}` or `/actor`, no query, fragment, user or percent-encoding, the name as stored; its port may
  be written when it is the origin's (`:443` for an origin without one: RFC 3986 6.2.3 makes the default port
  the same URI). Any other scheme (`mailto:`, `http:`) is a well formed URI this server has no information
  about: 404. A handle with no `@`, more than one after the leading one, or an empty name or host: 400.
- **The instance actor** is `acct:host@host`. `usernameRE` allows dots, so a member whose username is the
  origin's host can exist when the host has 3 to 24 characters (`flowa.example.org` can be a username). The
  instance actor is decided before any member is looked up, so it always wins, and that member can be
  found only by the URL form of their id. FRD 5 reserves that name (5.2, "As built (FRD 5)").
- **The owner only, in single mode.** The implied opt-in of single mode is the owner's (`owner` in the
  lookup), not every account's, though `checkSingle` keeps one account there anyway.
- **One answer for nobody.** An unknown name, a member who has not opted in, a suspended member and a deleted
  one (and later a retired name) all run the same one statement on the unique username index
  (`webFingerSQL`) and get the same 404, body and headers; a name `usernameRE` refuses cannot exist and costs
  no query.
- **Methods, caching, limits.** GET and HEAD; any other method gets 405 with `Allow: GET, HEAD` and the CORS
  header, answered before the write guard (which would have said 403 or 415). A found answer is sent with
  `Cache-Control: public, max-age=300`, so turning the switch off reaches other servers' caches within five
  minutes (Mastodon's own answer is kept far longer); a 404 with `max-age=60`; 400, 405 and 429 with
  `no-store`. Every address has 600 discovery requests an hour (`limits.discovery`, RFC 7033 9.3 recommends
  limiting by address against harvesting), counted before the query is read, apart from every allowance
  members use: a remote server over it gets 429 with `Retry-After` and the CORS header, and a member at the
  same address still uses the app.
- `BenchmarkWebFinger` (the whole server chain, `-benchmem`) is in FRD's todo.

**host-meta** (`/.well-known/host-meta`, RFC 6415): later, only if a server is found that needs it
to find WebFinger. Mastodon's WebFinger page describes the lookup going straight to
`/.well-known/webfinger` and does not mention host-meta; whether Mastodon falls back to it when
WebFinger fails is not in its documentation.

**As built (FRD 8), the other direction:** Flowa as the server that asks, for a member's search by handle
(section 11, `findRemote` in `main.go`), checked against Mastodon's WebFinger page and the SWICG report
"ActivityPub and WebFinger" ("Forward discovery", "Reverse discovery"):

- **Forward.** `acct:name@host` (the name percent-encoded where RFC 7565 7 asks, its case kept) is asked of
  `https://host/.well-known/webfinger` through the fetcher's WebFinger kind (5 s, 64 KB, `application/jrd+json`
  or `application/json`, up to 3 redirects to any https host, as RFC 7033 4.2 allows and Mastodon's
  `WEB_DOMAIN` relies on, each checked again by the fetcher's rules and its dial guard). The answer is read
  strictly (`parseJRD`: the decoder's scan first, so no member name twice and valid UTF-8; members by their
  exact names): a `subject` is required (Mastodon checks it is present), and the first link whose `rel` is
  `self` and whose type is `application/activity+json` or `application/ld+json` with the Activity Streams
  profile, whose `href` passes `apIDOK` (the SWICG report: "Take the first matching entry").
- **The actor** is fetched through `remoteActor` (7.4, as built in FRD 7: signed, same-origin redirects only,
  its id where it came from). A gone actor is told as gone, a suspended one as suspended, and neither is shown.
- **Reverse.** From the actor's `preferredUsername` and the host of its id, `acct:pref@host` is asked of that
  host; its `self` link must be the actor. When its `subject` is another account (Mastodon: "if the subject
  contains a different canonical account URI, then Mastodon will perform an additional WebFinger request for
  that canonical account URI"; the SWICG report makes the same step optional), that account is asked of its own
  host and must give the same actor and the same subject. A canonical account on this instance's own host is
  refused unasked. A reverse lookup that finds nothing (404, 410) is "not an account": the actor was found,
  its handle does not check out.
- **The rule when the handle's host and the actor's host differ** (decided here; neither text says what to do
  with the query's own subject): the handle shown is the one the reverse check proves (the canonical account),
  and the subject of the query's own WebFinger answer must be that same account. So `alice@example.com`
  answered by `example.com` (or by the host it redirects to) with subject `acct:alice@example.com` and an actor
  on `ap.example.com` whose own host names `acct:alice@example.com` back is accepted and shown as
  `alice@example.com`; a server that answers for `eve@its.host` with someone else's actor is refused (its
  subject is not the account the actor's server proves), and one that answers with that account's own handle
  as subject is simply shown that account. Accounts are compared as RFC 7565 section 4 asks: scheme and host
  without case (and without a trailing dot), percent-encoding decoded, the name with its case (the SWICG
  report: "SHOULD NOT assume case insensitivity for external usernames").
- **A profile address** is first asked as an actor (Mastodon's page: the actor "may be provided directly,
  without the initial WebFinger request"); one that redirects on its origin to the actor's id is that actor,
  and the cache keeps it as one of the actor's names (7.4, as built after FRD 7's review), so asking it again
  sends no request for the actor; an address that answers no actor Flowa can use (another type, not
  an actor, a document of another id, a redirect to another origin) is asked of its host's WebFinger with the
  address itself as the resource (RFC 7033 4.1 takes any URI; Mastodon's and Flowa's own WebFinger answer for a
  profile's URL), and goes on as a handle. A handle or an address on this instance's own host is a local member,
  found in the database with no connection.
- **A WebFinger request already answered in the same lookup is not sent again** (the reverse lookup of a
  handle asked exactly as the actor's server writes it is the forward one), so a plain lookup sends two requests
  before the outbox.

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

Checked against the NodeInfo 2.1 schema (and, for FRD 4, against the schema file itself, kept as
`testdata/federation/nodeinfo-2.1.schema.json`, which `TestNodeInfo` validates the answer with: it agrees
with this paragraph, and adds that `software`, `services`, `usage` and `usage.users` allow no other members
either, that `software` may also have `repository` and `homepage` and `usage` may have `localComments`, and
that every count has a minimum of 0): `version`, `software`, `protocols`, `services`,
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
always false in single mode. The version string is `""` by default (as built in FRD 4); an admin will be
able to choose to send the build's version instead, or to keep `"version": ""` (instance setting `nodeinfo_version`, **new**; the schema requires the member but not
a value), since which build runs helps an attacker, the reason `handleHealthz` keeps it private.

As built (FRD 4): the two documents as above, from typed structs, the discovery one as `application/json`
and the 2.1 one with the profile content type, both with the CORS header, the cache and limits of 5.3 and
405 for other methods. `software.version` is `""`: the schema file requires the member and gives it only
`"type": "string"` (no pattern, no minimum length), so the empty string is valid and says nothing about the
build. That makes the version private by default, as in `/healthz`; publishing it becomes an admin's choice
with `nodeinfo_version`, below. `protocols` is
`["activitypub"]` because the schema needs one entry from its list, though only discovery is built; so that
nobody takes that for the whole protocol, `metadata` says `{"mode": "...", "federationBuilt": ["webfinger",
"nodeinfo"]}` (since FRD 6 `["webfinger", "nodeinfo", "actors", "notes", "outbox", "collections"]`, read from the one
sentence of 4.1) instead of the example's `nodeName` (Flowa keeps no instance name). The counts are one query of
four counts, kept for five minutes; `openRegistrations` is read from the flag and registration caches the
pages use. **The instance setting `nodeinfo_version` is not built**: it needs the admin's instance editor to
learn a new field, which is its own change; until it lands, NodeInfo sends the empty string, so the default
is the private one and the setting will only add the choice to publish the version.

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

As built (FRD 5), checked against `TestActorDocument`'s golden files (`testdata/federation/actor-ana.json`,
`actor-instance.json`), with these decisions:

- **The context and the properties are those above**, from typed structs, each name once, every id https:
  `id`, `type` `Person`, `preferredUsername`, `name` (left out when the member has no display name), `summary`
  (the bio as the HTML of 6.5: escaped, paragraphs, links), `url` (the id), `inbox`, `outbox`,
  `manuallyApprovesFollowers`, `discoverable` and `indexable` all false, `published` (the account's creation),
  `icon` (`/users/{name}/avatar.jpg` or `.png` by the picture's type, `?v=` the first 12 hex characters of an
  MD5 of the stored picture, so a new picture is a new address), `attachment` (`Props` and one field per link,
  each link `rel="me nofollow noopener"`), `publicKey` from `ap_keys`. No number (F12).
- **Addresses this version does not serve.** `inbox` and `outbox` are named although nothing answers them
  yet, because ActivityPub 4.1 says an actor MUST have both and a reader that checks the shape (Mastodon among
  them) refuses an actor without an inbox; a delivery there fails, which is true: nobody can follow yet.
  `followers`, `following`, `featured` and `endpoints.sharedInbox` are left out until they answer (FRD 6, FWR
  1): ActivityPub makes them SHOULD and MAY, and naming an address that answers 404 says more than is built.
  As built (FRD 6): the outbox answers, and a member's actor names `followers`, `following` and `featured`,
  which answer too (6.2); `endpoints.sharedInbox` is still left out, and the inbox still takes nothing (405,
  below). The instance actor names its inbox and its outbox (`/actor/outbox`, empty) and nothing else.
- **As built (FWR 1), replacing the next paragraph:** the inboxes take deliveries (7.10), a member's actor names
  `endpoints.sharedInbox` (`/inbox`, ActivityPub 4.1 and 7.1.3; `actor-ana.json` changed), and any method but POST
  at an inbox is 405 with `Allow: POST` (`TestDBInboxMethods` replaces `TestDBInboxNotYet`). The instance actor
  still names its inbox and outbox and nothing else.
- **The inboxes the actors name** (`/users/{name}/inbox`, `/actor/inbox`) answer every method with 405 and an
  empty `Allow`, whatever the name, before the write guard (which said 415 or 403), counted against the public
  allowance. RFC 9110 15.5.6 makes 405 the answer for a method the target does not support, and 10.2.1 an
  empty `Allow` the way a resource temporarily disabled by configuration says it takes none; a 4xx tells a
  sender not to keep trying. 404 would say the address does not exist, which the actor contradicts, and 501
  is for a method the server supports nowhere (15.6.2). Mastodon's pages on disk do not say how it treats
  each status. So someone on Mastodon can press Follow on a Flowa account now; the Follow is refused, and it
  stays "requested" there until FWR builds the inbox. Local and plain single mode answer these paths as any
  unknown address (`TestDBDefaultsUnchanged`); `TestDBInboxNotYet` (replaced in FWR 1, above).
- **An opted-in member whose key is still being made** (opting in asks for it; it takes a moment, 5.7) gets
  503 with `Retry-After: 60`, never a document without a key. The instance actor likewise until its key exists.
- **Kept in memory.** A member's document is made once and kept (at most 1,000, all dropped when full, each
  remade after ten minutes as a net for a hand edit) and dropped when the member saves their profile (name,
  bio, links, props, picture, the opt-in) and when their key is made or rotated; who may be shown is asked of
  the database on every request, so a suspension or an opt-out hides a kept document at once.
  `TestDBActorCache` changes each.
- **The instance actor** is `Application`, `preferredUsername` the host, `url` the id, inbox and outbox named,
  its key; nothing else.
- **A deleted member who had opted in** (a retired name, 5.2): the actor answers 410 with
  `{"@context": "https://www.w3.org/ns/activitystreams", "id": ..., "type": "Tombstone", "formerType":
  "Person"}` (ActivityPub 6.4), and a browser a page that says the account was deleted, so other servers stop
  asking. Any other name nobody shows (never opted in, opted out, suspended, deleted without having opted in)
  is the one 404. **This tells something, on purpose:** that an account under the name once shared its
  profile beyond the server (opted in, or had a key: also one that opted out before it was deleted), where
  WebFinger's one 404 (5.3) tells nothing. It is accepted because the actor id was public already (other
  servers may have stored it, which is why the name is retired), and other servers that hold it must learn it
  is gone, or they keep showing and asking for an account that no longer exists; ActivityPub 6.4 gives 410 and
  a Tombstone for that, and the SWICG report on HTTP signatures reads a 410 on a key fetch as the actor
  deleted. An account that never opted in had no public id, and its deletion stays the one 404.

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

As built (FRD 5), decided against RFC 9110 (5.3, 5.6.2, 5.6.4, 5.6.6, 12.4.2, 12.5.1) where the paragraph
above is silent:

- **The reader** (`parseAccept`): several `Accept` lines are one list; an element is `type/subtype` (case
  ignored) with parameters in any order and case, a value a token or a quoted string with its escapes; `q` is 0
  to 1 with at most three decimals, and what follows it is an extension, not the type's. An element that does
  not parse is skipped (a profile written without quotes is one: a URI is no token). A header over 4,096 bytes
  or with more than 64 elements is read as no header, so a hostile one costs bounded work.
- **The choice** (`wantsActivity`): for each of the two answers, the most specific range that matches gives
  its weight (the ld+json profile, then the type, then `type/*`, then `*/*`). The document when a JSON type
  weighs above zero and above `text/html`, or the same and more specific; otherwise the page. So a missing
  header, `*/*` alone and a browser's header get the page; `text/html;q=0, application/activity+json` gets the
  document; `application/ld+json` with a profile that does not name ActivityStreams counts for nothing, and a
  profile parameter on `application/activity+json` changes nothing. A request that accepts neither gets the
  page rather than 406: RFC 9110 12.5.1 lets a server disregard the header, and a person who gets a page
  reads why.
- **Headers.** `Vary: Accept` on every answer of the two negotiated addresses and of `/actor`, the 404, 410,
  429, 503 and 500 included (`Signature` joins it with authorized fetch, 7.6). Documents are
  `application/activity+json`; found answers `Cache-Control: public, max-age=180` with an `ETag` (and 304 to
  `If-None-Match`), a 404 `public, max-age=60`, a 405, 429 or 503 `no-store`. HEAD answers as GET without the
  body; any other method 405 with `Allow: GET, HEAD`, counted against the allowance and answered before the
  write guard. No CORS header: the note asks it of WebFinger only (5.3).
- **The pages** are one `html/template` (`pubTmpl`) with the policy above, no script and no event attribute,
  text escaped by the template (as the app's own pages escape it; links are the post's http and https URLs),
  links out `rel="nofollow noopener ugc"`, pictures only from this server, a Sign in button, and `<meta
  name="robots" content="noindex">`: the note is silent on search engines, and `indexable` false (5.5) and
  "nothing shared without an action" (F2) make not being indexed the careful default; an owner who wants to be
  found can be offered a switch later. A profile page lists 20 Public posts newest first and links older ones
  by `?before=<post id>` (a cursor, so a page does not shift when someone posts); a value that is not one
  positive id is the 404.
- **Limits.** Every public address (the two above, `/actor` and the pictures) counts against one allowance
  of 2,400 requests an hour per address (`limits.public`), before anything is read and apart from every
  allowance members use; past it 429 with `Retry-After`.

### 5.7 Keys

- RSA 2048, generated for a member when they opt in and for the instance actor when federation first
  starts, in the background, one at a time (key generation takes about a second or more on the Pi;
  measured in FRD's keys item), so no request waits for it. RSA because Mastodon verifies nothing
  else before 4.7.0 (its security page), and 2048 bits because the SWICG report on HTTP signatures
  asks for "a 2048-bit or larger RSA keypair". The public key is served as an SPKI
  (`BEGIN PUBLIC KEY`) PEM, the form in Mastodon's examples.
- Stored in `ap_keys` (**new**): the public key as PEM, the private key sealed with AES-256-GCM under
  `FLOWA_KEY_SECRET` (F7), with the key id and creation time. The secret never goes into the database,
  the log or a backup. As built (FRD 1): the AES key is made from the secret with HKDF-SHA256 and an info
  string naming its purpose, never the secret itself, so a second purpose later gets an unrelated key;
  each sealed key carries a fresh 12-byte nonce, names its scheme (`v1.`), and has its key id and owner
  authenticated with it, so a sealed key copied onto another row does not open; the key id is stored
  without the origin (`/actor#main-key`), which is recorded once in `instance_settings` when it is first
  announced (4.3; making a key records nothing). A secret that does
  not open the instance actor's key (checked at every start) stops federation, not the server, as a moved
  origin does (4.3); while federation is refused, for either reason, no key is made for any owner, so none
  replaces a key that cannot be opened and none is sealed under a second secret. The secret must be 64
  hexadecimal characters and not made by hand: at least 8 different bytes, no repeating block, no
  arithmetic run, not every byte printable ASCII (a random secret is refused with a chance under 2^-45).
  `-rotate-keys` skips a key another rotation changed meanwhile, says how many, and exits non-zero.
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
| `GET /users/{name}/outbox` | OrderedCollection of the member's Create activities for Public posts, `totalItems`, `first`, pages of 20 newest first by post id (`?page=true&before=812`, the profile page's cursor; corrected in FRD 6 from `max_id`); ActivityPub 5.1 requires an OrderedCollection, 5 requires reverse chronological order and names an increasing integer id as a fair key, and 5.1 says an unauthenticated reader gets the Public posts |
| `GET /users/{name}/followers`, `/following` | OrderedCollection with `totalItems` only; `first` and pages only for the account itself (F10); ActivityPub 5.3 and 5.4 allow either collection type and filtering by who asks |
| `GET /users/{name}/featured` | an empty OrderedCollection at first (pinned posts do not exist yet); cheap, and Mastodon reads `featured` as the pinned posts it fetches for a profile (its ActivityPub page, "Featured collection") |
| `GET /users/{name}/posts/{id}`, `/comments/{id}` | the Note, or HTML; 404 for audience This server, 410 with a Tombstone for a deleted one (ActivityPub 6.4: 410 when a Tombstone is the body, 404 otherwise; 3.2 allows 404 for an object whose existence is private). As built (FRD 5): posts only; a deleted post is the 404, since nothing keeps a deleted post's id to answer with a Tombstone (6.4 makes replacing an object with one a MAY); comments are FWR's |
| `GET /users/{name}/posts/{id}/picture.jpg`, `GET /users/{name}/avatar.jpg` | the stored picture, public only for federating members and Public posts. As built (FRD 5): `.png` too, the extension the picture's type has (the other one is the 404) |
| `GET /api/fed/...` | the page's own endpoints for search by handle and remote accounts (section 11), behind `authed` and the limits. Follows and Home are not here: since INS every instance has `PUT` and `DELETE /api/users/{name}/follow`, `GET /api/home`, `GET /api/following` and `GET /api/followers`, behind the `follows` flag (off by default) and the `community` flag. Blocks of accounts are not here: they are `PUT /api/users/{name}/block`, `DELETE` of the same and `GET /api/blocks`, which every instance has since INS, behind the `community` flag |
| `GET /users/{name}/posts/{id}/activity`, `GET /actor/outbox` | as built (FRD 6): a Public post's Create (5.2's correction), and the instance actor's empty outbox |

As built (FRD 6), checked against ActivityPub 5, 5.1, 5.3 and 5.4, Activity Streams 2.0 Core 2.3 (collections
and paging) and Mastodon's ActivityPub page ("Featured collection"), with these decisions where the table is
silent:

- **One function for the Create.** `createOf` makes the Create of a Public post from the post (`noteOf` its
  Note), for a page of the outbox, for the Create's own address, and for FWR 4's deliveries when they come:
  `id` the post's full address with `/activity` after it (`https://flowa.example.org/users/ana/posts/812/activity`),
  `type` `Create`, `actor` the author, `published` the post's, `to` the
  public collection and `cc` the author's followers collection (6.4, 6.7), the same as the Note's own `to` and
  `cc`; `object` the Note without a `@context`. The Note at its own address gains the same `cc` (its golden file
  changed), since the followers collection now answers.
- **The outbox** (`/users/{name}/outbox`): `{"@context", "id", "type": "OrderedCollection", "totalItems",
  "first": ".../outbox?page=true"}`, `first` even when the count is 0 (the first page then has `orderedItems`
  `[]`). A page is `OrderedCollectionPage` with `partOf`, 20 Creates (`pubPage`, the profile page's size; no
  size parameter) newest first, and `next` (`?page=true&before=<the last post id on the page>`) while there are
  older posts. There is no `prev`: Activity Streams 2.0 Core 2.3 allows a singly linked list and tells a reader
  to walk from `first` by `next`, and a newer page would need a second cursor. Exactly three queries answer,
  compared as written (`docQuery`, on the raw query, so nothing is dropped unseen as `url.ParseQuery` drops a
  pair with a semicolon): none, `page=true`, and `page=true&before=<id>` with the id as a plain decimal (no
  sign, no leading zero, no escape). Any other (`page=1`, `max_id`, `limit`, `before=007`, `before=%2B7`, the
  parameters in another order, a repeated, unknown or semicolon-joined one) is the 404, so each page answers at
  exactly the address its `id` names. Followers, following, featured, a Create and the instance actor's outbox
  answer with no query at all, by the same function. The cursor only bounds a statement that is already held to
  the one member's Public posts (the profile page's `profilePageSQL`, which the outbox reuses), so a cursor that
  names another member's post, a This server post or no post reads that member's own Public posts older than
  the id, and nothing else (`TestDBOutboxPaging`). A cursor past 2^31 answered 500 on the profile page (the
  statement took it for an integer): this item found it, and the fix (a bigint) merged with FRD 5 (#107).
  `totalItems` counts what the pages list (`publicPost`: Public, not hidden, not a milestone) in the same
  statement as the gate (`outboxSQL`), through `posts_public` for the one member: it reads the member's Public
  posts and nothing else (about 6 ms for a member with 22,000 of them, on a 2.1 GHz Xeon). That is linear; an
  index-only count would need `kind = 'post'` in `posts_public`'s condition, which is the owner's call (FRD's
  todo). A page is one statement, the Creates made after its rows are read.
- **Followers and following** answer `{"@context", "id", "type": "OrderedCollection", "totalItems"}` and never
  `first`, a page or an item. F10 shows the lists only to "the account itself", and nobody who asks at this
  address can be that account: nothing here reads a session, and a signed request, once there is one (7.6), is
  signed by another server's actor. A member reads their own lists in the app (`GET /api/followers`, `GET
  /api/following`, since INS), which is that other surface. **The count says no more than the app does, and
  less.** It is given only while the app shows counts at all, the `community` and `follows` flags both on
  (`followCountsShown`, which the app's profile asks too); otherwise the collection has no `totalItems`, which
  Activity Streams allows. And it counts only the follows whose other side is itself a member who may be shown
  beyond this server (opted in, not suspended: `publicMember`, the gate's own condition) and no follow across
  a block (`publicFollowCountsSQL`). The app shows its signed-in members every local follow, but a follow made
  by, or of, a member who never chose to federate, and when it changed, is theirs to keep inside the app: a
  public count of all of them would tell the internet something those members never shared. A member who opts
  out or is suspended leaves the counts at the next request (other servers' copies within the three minutes of
  `Cache-Control`). No member can hide the counts (section 11 hides the lists, which are hidden from everyone
  here). Remote followers do not exist yet (FWR 3). **Open for the owner (F10):** whether a public count
  should count every local follower, or, as built, only those who federate.
- **Featured** (`/users/{name}/featured`, 5.2): `totalItems` 0 and `orderedItems` `[]`, since posts cannot be
  pinned. **The instance actor's outbox** (`/actor/outbox`, which 5.5 names): the same, empty; like the instance
  actor's document it records no origin.
- **The rules of the public addresses apply unchanged, through the same code**: the gate of `publicMember` (the
  actor's `actorGateSQL` for the collections), one indistinguishable 404 per representation for an unknown, not
  opted in, opted out, suspended or deleted member and for any spelling that is not an id, `limits.public`
  before any database work, HEAD, 405 with `Allow` (`publicPaths` knows the new addresses), `Vary: Accept` on
  every answer, `Cache-Control: public, max-age=180` with an `ETag` (and 304), so a member who opts out leaves
  readers' caches within three minutes as with the actor, `ensureOrigin` before a document naming a member's id,
  no CORS header. **A retired name** is the 404 at these addresses, as at its posts: the Tombstone at the actor
  (5.5) is what tells other servers to stop asking.
- **A browser** at the outbox, a collection or a Create is sent with 303 See Other to the page that shows the
  same thing: the profile page (`?before=` kept for an outbox page), the post's page, or `/actor`. These are
  documents for servers (Mastodon serves no page at them either), and a person who opens one should land on
  something readable (5.6). The redirect reads nothing, so it is the same for every name; the page it leads to
  answers for itself.
- `TestDBOutboxPaging` (empty, one page, many pages, a deleted and a hidden post, This server posts and a
  milestone absent and not counted, every cursor, every query that names no page, the browser's redirect),
  `TestDBCreateActivity`, `TestDBCollections`, `TestDBCollectionSurfaces`, `TestDBOwnDocumentsDecode` (the
  outbox, its pages, the Creates and the collections through `decodeCollection` and `decodeActivity`),
  `TestDBOneConnection` and `TestDBPublicPool` (the new addresses), `TestDBNamesFailingRead` (a failing read of
  the skill names costs one statement per prop for 10 s, `namesRetry`, not one per name on a page),
  `TestDBQueryPlans` (`outboxSQL`, `publicFollowCountsSQL`), `TestDBDefaultsUnchanged` (the new addresses in
  local and plain single mode) and `BenchmarkOutboxPage` are in FRD's todo.

### 6.3 Activities in

**One decoder for the shapes JSON-LD allows** (built, with no caller yet: the section "federation:
the shapes activities arrive in" of `main.go`). Flowa runs no JSON-LD processor, so the same property
arrives in several shapes, and this is the one place they are read. Every document first passes one scan
(`apScan`), linear in its length and without recursion: well-formed JSON, valid UTF-8 (AS2 Core 2 requires
it) with no escaped half of a surrogate pair, the limits of 7.5, and no member name twice in one object,
however it is spelled (an escaped letter does not make another name). Left to itself, `encoding/json` keeps
the last of two equal names, turns invalid UTF-8 into U+FFFD and matches struct fields without regard to
case, so two parsers could read two documents from one body; after the scan the decoder walks the bytes
itself and reads members by their exact names, never through structs (`FuzzDecodeNames` holds that it sees
the same names and values `encoding/json` does).

**The rule for refusing:** a document is refused only for what Flowa would act on wrongly; what it merely
cannot use is dropped, and the readers count what they dropped so that the caller can log it. An actor
refused is an account whose every later activity fails its key lookup, so the bar for refusing one is high.

- **Refused**, because acting on a second reading would be wrong: an `id`; the `actor`; every
  `attributedTo` entry (an author Flowa cannot name must not vanish from "is this post by the signer");
  the `object` of an activity Flowa acts on; `inbox`, `followers`, `endpoints.sharedInbox`, `movedTo`,
  `first`, `next` and collection items; a `summary` or `summaryMap` that cannot be read (dropping a
  content warning would show the post unfolded); more than one value where Flowa acts on one (`actor`,
  `target`, `inReplyTo`, `movedTo`, `endpoints`, and `object` for Create, Update, Delete, Like, Announce,
  Follow, Accept, Reject, Undo, Block and Move: a table in the decoder, so no caller has to remember; a
  Flag names several, as Mastodon's example does, and an unknown type keeps its list since the inbox
  drops it); two public keys with one id; two vocabulary types in one `type`; and everything the scan
  refuses. A list inside a list is refused in a property Flowa reads ("JSON-LD forbids arrays of
  arrays", AS2 Core 5) and passed over elsewhere, as consumers "MUST NOT stop processing" for an
  extension (Mastodon's `focalPoint` is a list).
- **Dropped and counted:** recipients (`to`, `cc`, `bto`, `bcc`, `audience`: `apAudience.Dropped`),
  `alsoKnownAs` entries, Mentions whose `href` Flowa cannot use or that have none (`TagsDropped`: nobody
  is notified for them), `publicKey` entries that cannot be read (a `did:key` beside an https key:
  `KeysSkipped`; the key a signature names must be readable, or its lookup fails), and a Flag's objects
  that cannot be local. Dropping a recipient can only narrow what Flowa takes the audience to be: the
  public collection is recognised before the id check and never dropped, and the followers collection
  that makes a post followers-only (6.7) is the actor's own, which passed the check when the actor was
  read, so a dropped entry cannot be it.
- **Left out, on the careful side:** `url`, `icon`, an attachment's `url` and a Hashtag's `href` that
  Flowa cannot use; a media type that is not text of at most 255 bytes (the attachment stays, without
  one); a `published`, `updated` or `deleted` that is not an AS2 Core 2.3 date-time (absent, with
  `BadDate` set, since an Update without `updated` is ignored); a `totalItems` that is not a whole number
  from 0 to 2^53 (any JSON number with such a value is taken, so `5.0` is 5); a `name` that cannot be
  read; `sensitive`, `manuallyApprovesFollowers` and `suspended` read as true when they are anything but
  `false` or null. An `inReplyTo` Flowa cannot address (an id on another port, say) is neither refused
  nor dropped: `apNote.ReplyToUnknown` says the post is a reply to a post Flowa does not know, which the
  table below drops unless the author is followed and never shows as a new thread.

How the shapes are read:

- **One value or many** (`oneOrMany`, **new**): `type`, the five addressing properties, `attributedTo`,
  `object`, `tag`, `attachment`, `url`, `icon`, `publicKey`, `alsoKnownAs`, `items`. `null`, an empty
  list and absence are the same (AS2 Core 2); a `null` inside a list is skipped.
- **An id or the object** (`idOrObject`, **new**): a string, or an embedded object carrying `id` (a Link's
  `href`; an attachment that is a Link keeps its `href` as its address), in any of the properties above
  and in `actor`, `target`, `inReplyTo`, `first`, `next`, `endpoints` and `movedTo`. `@id` and `@type`
  in an object Flowa reads are refused: the normative context aliases them to `id` and `type`, AS2 Core
  2.1 requires the compacted form, which writes the aliases, and no text shows a server sending the
  keywords. Inside a property Flowa does not read they are left alone.
- **Names:** a property is recognised by its short name, its compact name and its full IRI: `as:` and
  `https://www.w3.org/ns/activitystreams#` for the Activity Streams terms (`inbox` is `ldp:inbox` in the
  normative context, read on 13 November 2026), `sec:` and `https://w3id.org/security#` for `publicKey`,
  `publicKeyPem`, `owner` and `controller`, `toot:` and `http://joinmastodon.org/ns#` for `suspended`. Two
  spellings of one property in one object are refused as a duplicate; so are `items` and `orderedItems`
  together, which the context maps to one property.
- **`@context` is never interpreted**, in any shape. So a term a context adds is not seen either: with
  `{"author": "as:attributedTo"}` in its context, `{"attributedTo": A, "author": B}` has two authors to a
  JSON-LD processor and one to Flowa, and `"as:to": "Public"` alone makes a post public to Flowa. That is
  acceptable because Flowa acts for the signer, whose identity it checks (7.3), never for a name a
  context made up, and because servers that read the plain names, Mastodon among them by its page, read
  what Flowa reads; the inbox needs nothing more (`TestDecodeContextTerms`).
- **Languages:** `contentMap`, `summaryMap` and `nameMap` are the same properties as `content`,
  `summary` and `name` in a language map (AS2 Core 4.7). The text taken is the plain property when it has
  text, otherwise the map's `und` entry, otherwise the entry whose language tag sorts first: Flowa has no
  reader's language to prefer, and this choice is the same every time. A post keeps `contentMap` whole
  beside `content` for the converter; `summary` and `name` (of a post, an actor and an attachment) are
  read by that rule. `content`, `summary`, `name` and each map entry are cut at 64 KB (`Cut` says so).
- **Types:** `type` is one name or a list. The object's kind is the one Activity Vocabulary type among
  them (and `Hashtag`), written short, `as:Note` or in full; other names, including `as:` names that are
  not vocabulary types, are extensions and ignored; `Object` or `Link` beside a more specific type is
  ignored too (a Note is an Object); two specific vocabulary types in one list are refused. "Is a Note"
  is kind `Note`; an actor is one of the five Actor Types of the Activity Vocabulary (3.2): Application,
  Group, Organization, Person, Service.
- **Ids** are absolute https URLs the fetcher would ask (`apIDOK` runs the fetcher's own check: port 443,
  no user name, a public DNS name), at most 2,048 bytes, compared as written (AS2 Core 2.2). The
  instance's refused hosts and its own origin are not known to the decoder: the fetcher applies them on
  every request, and FMD's domain policy to what comes in. `FuzzDecodeIDs` holds that every id, address
  and link a reader returns passes `apIDOK`.
- **Readers**, for exactly what this note uses: an actor (below), an activity's envelope (`id`, required
  because the inbox removes duplicates by it, `type`, `actor`, `object`, `target`, the five addressing
  properties with the public collection's three forms read as one, `published`, `updated`), a post as 6.5
  and 6.6 read it (`attributedTo`, `inReplyTo`, `name`, `summary`, `content` and `contentMap`,
  `sensitive`, `tag` with Mention and Hashtag, `attachment` with both `name` and `summary`, the latter
  first as 6.5 says, `url`, `published`, `updated`), a Tombstone, and a collection or page
  (`totalItems`, `first`, `next`, `items` or `orderedItems`). A `publicKey` entry is chosen by its id,
  fragment included, and its `publicKeyPem` goes to `parsePublicKeyPEM` exactly as the JSON string holds
  it. 7.3's origin checks need nothing new: `sameOrigin` compares an activity's actor with its key id or
  its object's id, and a post is by its actor when `attributedTo` holds that actor alone.

Every refusal is a fixed phrase, what was being read ("actor refused", "post refused", ...) and the path
of the property it is about (`object.tag.href`), all safe to log. The documents in
`testdata/federation/shapes` are examples copied from ActivityPub, Activity Streams 2.0 Core and Mastodon's
ActivityPub page, and the two of this note; none is recorded (their README says from where).
`TestDecodeShapes`, `TestDecodeFixtures`, `TestDecodeActor`, `TestDecodeNote`, `TestDecodeActivity`,
`TestDecodeCollection`, `TestDecodeRefusals`, `TestDecodeDrops`, `TestDecodeLanguageMaps` and four fuzz
targets hold them.

**What Flowa reads from a remote actor:** `id`, `type`, `preferredUsername`, `name`, `summary`
(through `htmlToText`), `url`, `icon`, `inbox`, `endpoints.sharedInbox`, `followers`, `publicKey`,
`manuallyApprovesFollowers` (a Follow to it shows as requested until Accept), `movedTo` and
`alsoKnownAs` (PRT), and `suspended`: Mastodon sets it on an account its server suspended and then
serves "empty data" for it (Mastodon ActivityPub page, "Suspended flag"), so Flowa marks such an
actor `gone` in `ap_actors` and stores nothing new from it until an Update clears the flag.
`discoverable` and `indexable` concern directories and full-text search, which Flowa does not have
(section 10), so Flowa does not read them. Everything else is ignored.

As built (FRD 7), where each is kept (`store`, "federation: remote actors" in `main.go`): `name` in
`users.display_name` as plain text (AS2 Core 4.7: no markup), with what `visibleText` takes out of a local
name taken out, a name that turns the text's direction dropped whole, at most 64 characters, the plain
`preferredUsername` standing in when there is none; `preferredUsername` as written, case kept, in
`ap_actors.preferred_username` (empty when over 255 bytes or holding NUL), and in the account's username
(9.2); `summary` as the HTML received, NUL removed, cut at 2,048 bytes on a character boundary, in
`ap_actors.summary`, for FWR's `htmlToText` (FWR 5 in the order of 14.3), which no item before it needs, so no
page shows it until then; the first usable `url` and `icon` as addresses (the picture itself comes through
F6's cache, FWR 5); `inbox`, `endpoints.sharedInbox`, `followers` and `movedTo` as addresses the decoder
already holds to `apIDOK`; `manuallyApprovesFollowers` in `ap_actors.locked` (not `users.locked`, as 9.1
had it: it describes the remote actor, and a local member's choice comes with FWR 3's follows); one key
(7.3, 7.4); `alsoKnownAs` not at all, until PRT. **Correction:** a suspended actor is not marked `gone`:
`ap_actors.state` is `suspended`, because a suspension can be lifted and `gone` (410) is final. A known
actor that says it is suspended keeps what was stored and gets only its state and times; one never seen is
stored with its id, kind, name and key, and no display name, summary, `url` or `icon`; a later fetch that no
longer says so is read in full again (the document is the actor's own word, as an Update would be).

The inbox verifies (section 7), stores the activity in `ap_inbox` and answers 202; a worker handles
it. "Actor" below is the verified signer's actor. What FWR 1 does with each row of this table, until the items
that own them, is in 7.10.

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

As built (FRD 6): nothing is sent yet, but the Create(Note) of the first row is built (`createOf`) and read in
the outbox and at its id (`/users/{name}/posts/{id}/activity`, 5.2's correction): to the public collection, cc
the author's followers, as 6.7 says; FWR 4 adds the mentioned actors to `cc` of the Create and its Note
together, in that one function. As built (FWR 4): the first row (Create(Note), to the followers one per shared inbox
and the remote accounts mentioned), Delete(Tombstone) on a deletion and on a moderator's hide, to the inboxes the Create
went to, and Update(Person) at most every five minutes, the newest state winning (6.5, "Public posts out, as built"). As built
(FWR 6): the second row (a comment, to the commenter's followers, the post's author and, for a post here, its author's
followers, and the accounts it mentions), the third (Update(Note) with `updated`), the forward of the fourth (signed by the
instance actor, never to the sending server), and Like and Undo(Like) of a post of another server (below, "Replies and
likes across servers, as built").

#### Follows across servers, as built (FWR 3)

"federation: follows across servers" in `main.go`, migration 20 (with `flowa-schema19.dump` for the restore drill).
What was built, and the decisions where 6.3, 6.4, 9.1 and 11 were silent:

- **The data** (9.1, 9.2, F5): a remote account is a `users` row, so a follow across servers is a row of `follows`
  as a local one is, with `state` (`pending` or `accepted`, default `accepted`, so every local row there was is
  accepted with no rewrite; the check `follows_state` is added `NOT VALID`) and `ap_id`, the Follow's id.
  `users.locked` is a member's choice to approve followers from other servers by hand. Indexes: `follows_pending
  (followee_id, created_at) WHERE state = 'pending'` (a member's requests) and `users_remote_domain (domain) WHERE
  domain <> ''` (a sending host's accounts, for its caps). An older build after `./deploy.sh --rollback` reads a
  pending remote follow as a follow (14.4); nothing is rewritten, so the rollback is not blocked.
- **One rule for counts and lists** (F10, as built in FRD 6): every count and list of follows (`followCountsSQL`,
  `followersSQL`, `followingSQL`, `homeSQL`, `publicFollowCountsSQL`) counts accepted follows only; a request that
  waits is in none of them. The public count of the followers and following collections counts the follows whose
  other side is itself public beyond this server (`publicSide`): a member who may be shown (`publicMember`'s
  condition), or a remote account no moderator suspended here, since its own server already shows its follows.
  The app's counts are unchanged for local follows.
- **Approval by hand applies to followers from other servers only.** The design does not say whether a local
  member's follow becomes a request; follows between members are kept exactly as they were in every mode (FWR 3's
  rule), so `users.locked` holds back remote Follows alone, and Settings says so beside the switch ("Members of this
  Flowa follow you as before"). It is shown, and `PUT /api/me` takes `locked`, only in a mode that federates; `/api/me`
  says it inside `federation`, so local mode's key set is unchanged and the field is ignored there
  (`TestDBLockedOnlyFederating`). The actor's `manuallyApprovesFollowers` says it.
- **In.** The inbox (7.10) stores Follow, Accept, Reject, Undo and Block only after the origin rule and only when the
  activity alone shows it can be about a follow here (`followShaped`): a Follow or Block of an actor id of this
  instance, compared as written; an Accept or Reject whose embedded Follow's actor is our member and whose object (when
  given) is the signer, or given by an id on our origin; an Undo by id, or embedding the signer's own Follow of an actor
  here or its own Accept (ActivityPub 6.10). Everything else of those types is dropped and counted, as before. The
  decoder now lets an Accept or a Reject embed a Follow with no id (Mastodon's id may be its own or missing; 6.3), and
  nothing else. The worker (no fetch, one statement per turn, `followIn`):
  - **Follow** of a member who may be shown and with no block either way: accepted and answered with Accept, or, with
    `locked`, held `pending` with a `follow_request` notification. Answers otherwise, so the sender learns no more than
    "refused": a member who does not federate (any more) and has a key, Reject; one with no key, nothing (no key could
    sign the answer); a Follow across a block, Reject; of the instance actor, Reject signed by the instance actor; of a
    name nobody has or had, nothing. A repeat while accepted is answered with Accept again and keeps one row (the row
    takes the newest id); a repeat while pending changes nothing. A follower whose inbox is missing or not on its own
    origin is not taken (no answer could reach it, below).
  - **Accept** of our pending Follow: accepted. Matched by actor and object, the embedded Follow's id ignored, or by the
    id we sent alone; an Accept by anyone but the account followed, of a Follow by someone not ours, or of a follow
    already accepted or unknown changes nothing.
  - **Reject**, and **Undo of an Accept** (read as a Reject, as Mastodon lists "Accept Follow" among what it undoes):
    our follow, pending or accepted, is removed and the member told once (`follow_rejected`).
  - **Undo of a Follow**: the signer's follow of that member goes, with its request notification; by id, only the
    signer's own Follow of that id. **Block**: the follows between the two go both ways, nobody is told.
- **Out.** `PUT /api/fed/accounts/{id}/follow` (the remote account by its id here, which the search by handle now
  returns as `account`, with `view`: none, requested or following, `followsYou`, `asks`, `blocked` and `cannot`) writes
  the follow `pending` with a new activity id (`/users/{name}/activities/<16 random bytes in base32>`, 5.2; these ids
  are not served, as Mastodon's Follow ids need not be) and enqueues the Follow; it stays "Requested" until the Accept.
  `DELETE` sends the Undo of that Follow, embedded with its id. (Found in FWR 4's review: the Undo could reach the other
  server before a Follow that met one error and waited for its next attempt, leaving a follow there that is gone here.
  The Undo now takes back its Follow, 8.8 "Taking back": a Follow never tried is given up and no Undo goes, one that met
  an error (it may have arrived) is given up and the Undo goes, one in flight holds the Undo until its lease ends. An Accept and a later Reject of the same Follow, after a block, can overtake each
  other the same way; the Accept's id is not kept with the follow, so this is left open.) `PUT` and `DELETE /api/fed/requests/{id}` approve and
  decline a request (Accept or Reject, embedding the Follow as it came), `GET /api/fed/requests` lists them (200 at
  most), and `PUT`/`DELETE /api/fed/accounts/{id}/block` block a remote account: the follows go both ways in the
  block's statement, then a Reject for its follow of the member and an Undo for the member's follow of it; no Block is
  sent (6.4). `GET /api/fed/accounts/{id}` is the remote profile page's data, from the cache alone (no fetch). The
  profile, blocks and requests behind the `community` flag, following and requests behind `follows` too, as local
  follows are; all only in a mode that federates (`testdata/routes-federated.txt`).
- **Who may follow out**: a member who shares their profile beyond this server (`publicMember`), since the other server
  verifies the member's signature against their actor, which answers only for them; not across a block; not an account
  gone or suspended; not on a server the domain policy refuses (in allowlist mode, every server not allowed: the page
  says so in `lookupNotAllowed`'s words); and not an account whose inbox is not on its own origin.
- **Where it is sent.** Every Follow, Undo, Accept and Reject goes to the other actor's own inbox (6.4), never its
  shared inbox, through `enqueue` (the only call is `sendFollow`), which applies the domain policy and the caps, and is
  signed at the send with the member's key (the instance actor's for its own Reject), each recipient named by its cached
  row's verified id (`ap_actors.ap_id`), as `enqueue` requires since FWR 2's review. The inbox must be on the actor's
  own origin (`inboxOwn`: `sameOrigin`, scheme, host and port whole, the rule the cache holds a key to, 7.3), so that
  what a delivery meets there (a redirect, a 5xx, a time out, which count towards a host's circuit, 8.4) is the actor's
  own server's; an actor naming an inbox elsewhere cannot be followed, and its Follow is not taken. That is at least as
  strict as `enqueue`'s own rule (the inbox on the actor's host, 8.8), which would refuse such a recipient anyway.
- **Bounds against a hostile server** (section 12): at most 200 requests wait for one member (`followRequestsMax`), 100
  from the accounts of one host (`followRequestsPerHost`), and 2,000 follows of local members come from one host's
  accounts in any state (`followsPerHost`); past each a Follow is dropped unanswered and counted in the row's outcome.
  The counts are in the statement that writes, each stopping at its cap (`followInSQL`), so two workers may pass one by
  at most one row each. Per host, not per sender group: the inbox's per-group rate (3,000 deliveries in 5 minutes) bounds
  how fast a wildcard name's hosts can add more. A member follows at most `maxFollows` (1,000) accounts, local and remote
  together, and may follow, unfollow, approve and decline across servers 100 times an hour (`limits.fedFollows`); blocks
  count against `limits.blocks`.
- **410** (8.3): `goneFollower`, the delivery queue's `goneInbox`, removes the follows of local members by the remote
  accounts whose own inbox answered 410, and their requests; a shared inbox answering 410 names no one account and removes
  nothing. It reads `ap_actors` by inbox (at most 20,000 rows, on a 410 only).
- **The actor cache's bound** never removes an account a follow points at either way (`follows` is in `userRefs`, both
  columns; `TestDBEvictionCoversReferences` and `TestDBFollowsAcross`).
- **The pages** (11): the remote profile page `#/fed/{id}` (its name in a `bdi`, its handle only when `handleNameOK`
  passes its `preferredUsername` and otherwise its host alone, its bio as text, a link to its own server, Follow with its
  three states, a request it made with Approve and Decline, Block, and, where its posts would be, the sentence that they
  do not show in Flowa yet), the Follow button in "People elsewhere", remote accounts in the followers, following and
  blocks lists and in the bell (`remote` in the user object, `markRemote`, only when an account of another server is
  among them, so a page with none costs what it did), the request notification with Approve and Decline beside it (not
  inside its link), the requests in "Following and followers", and in Home the sentence that posts of people followed on
  other servers do not show yet.
- **Behind the follows switch** (after the review of FWR 3; 14.1 puts follows behind `federation` and `follows`). With
  the `follows` flag off (its default, or `community` off) nothing adds or accepts a follow: the inbox counts and drops
  Follow and Accept (202, no row, nothing sent, nobody told), and the worker drops one stored before the flag went off;
  the member's routes answer 404 as the local ones do, so no Follow goes out; Settings hides the switch for approving by
  hand. **What only removes a follow is still acted on** (a Reject, an Undo of a Follow or of an Accept, a Block):
  removing is always safe, sends nothing, and keeps this server in step with the other, which has already ended it. The
  actor says `manuallyApprovesFollowers: true` while follows are off: a Follow is then not taken at all and stays
  "requested" on the sender's server, which is what a locked account shows there, and `false` would promise an Accept
  that never comes; the kept actor documents are dropped when a flag changes. The rows there were stay as they were
  (accepted follows hidden from counts and lists as local follows are while the flag is off; requests wait, and can be
  answered when it is on again), and turning the flag on replays nothing (`TestDBFollowsFlagOff`). **A switch that
  cannot be read is not "off"** (after the second review): `flagsKnown` tells a failed read apart, and then the inbox
  answers a Follow or an Accept 503 with `Retry-After` (`inFlags`), the worker tries the row again, and an actor
  document made meanwhile is answered but not kept (`TestDBFollowsSwitchUnread`). The `federation` switch's own reader,
  the pause, already reads a failed query as paused, which answers 503 and drops nothing; it was left as it was.
  Settings shows the switch for approving by hand only while both `follows` and `community` are on, as the server reads
  them.
- **What a Follow and its Undo leave behind** (after the review): nothing. An Undo of a Follow, a Block and a 410 take
  the `follow` and `follow_request` notifications of that account with the follow. What accounts of other servers leave
  in one member's notifications is bounded, the oldest dropped: the newest 50 from one server (`remoteNotesPerHost`) and
  500 from all of them (`remoteNotesMax`), so a server that floods pushes out only remote notifications, never a local
  member's. The trim (`remoteNotesTrimSQL`) reads only the kinds an account of another server can leave (`follow`,
  `follow_request`, `follow_rejected`) through `notifications_follows` (migration 20, not shipped, edited in place),
  so a member with 20,000 notifications from members here costs what their follow notifications cost (2,286 rows read
  for 300 in `TestDBQueryPlans`), and it spares a `follow_request` whose request still waits, already bounded by the
  200 waiting requests (`TestDBTrimKeepsLocalAndRequests`). A notification does not keep a remote account in the cache (`userRefsGoing`, apart from
  `userRefs`): it goes with the account when the bound removes one nothing else points at, so a server cycling Follow and
  Undo through new accounts cannot hold the cache above `remoteActorsMax` once the inbox's 7 days of de-duplication are
  over (`TestDBFollowUndoLeavesNothing`: 50 cycles leave no follow, no notification and no account the bound cannot remove).
- **A repeat is answered at most once an hour** per follower and followee (`followAnswerEvery`, in memory, 10,000 pairs,
  on the delivery queue's clock): a Follow repeated while accepted, and a Follow refused again (across a block, of a
  member who stopped federating, of the instance actor). Inside the hour it is taken with 202 and not answered; each
  answer is a row to send. Only a true repeat is silenced: the memory keeps what was answered and why, so another
  answer (a Reject after an Accept, after a block) goes out at once, and an Undo or a Block forgets the pair. A new
  follow is always answered.
- **Suspending a domain** (Admin, Federation) now ends the follows between members here and its accounts and its
  subdomains', both ways, with their notifications, in the policy's own transaction; nothing is sent (section 10:
  Mastodon removes them and does not restore them), and the audit row says how many follows it ended
  (`followsEnded`). Lifting the suspension does not bring them back.
- **A 410 is matched by the queue's own address** (`inboxAddress`, shared with `collect`: scheme, host and port whole,
  the host in lower case, no `:443`), so a 410 never removes the followers whose inbox is on another port of the host.
- **A member who stops sharing** their profile beyond this server keeps their remote followers' rows, and sends nothing
  (11: turning it off sends nothing). Their actor then answers 404, so remote followers' servers see the account as not
  found when they next fetch it, and new Follows of them are answered with Reject. Deleting an account goes out as
  Delete(Person) with FWR 7; until then the rows go with the account and nothing is sent.
- **Not built** (later items, or the owner's): posts from followed accounts (FWR 5), the domain policy `limit` turning
  follows into requests (FMD), Move (PRT), Delete(Person) out on account deletion (FWR 7), removing a follower without
  blocking it, and approving pending requests when a member turns approval by hand off (they stay requests until
  answered). Recorded Mastodon fixtures: the ones in
  `testdata/federation/follows` are written from documentation (its README); recorded ones wait for the owner's trial.
- Tests: `TestDBFollowsAcross`, `TestDBTwoFlowaFollow` (two instances over the real fetcher, signer, inbox and delivery
  queue: follow, accept and unfollow both ways, and approval by hand; FWR 8's container suite is the DoD's
  `TestTwoInstances`), `TestDBLockedOnlyFederating`, `TestDBFollowsOneConnection`, `TestDBQueryPlans` (a Follow in at
  845 and 258 rows read, a member's requests at 1,400), `TestDBPurgeRemote` and `TestDBInboxHandlers` updated; the
  `federated` browser suite (the switch, the remote profile, the button's states, a request with Approve and Decline).

#### Replies and likes across servers, as built (FWR 6)

"federation: replies and likes across servers" in `main.go`, migration 23 (with `flowa-schema22.dump` for the restore
drill). What was built, and the decisions where 6.3, 6.4, 9.1 and 11 left room:

- **The data** (9.1, 9.2, F5). A remote reply is a `comments` row with `ap_id` its id on its server (unique,
  `comments_ap_id`), `ap_updated` the `updated` of the version kept and `ap_extra` what is shown beside its text (the
  link to the original, a content warning, its attachments as links). A remote like is a `likes` or `comment_likes`
  row of the remote account, with `ap_id` the Like's id, so an Undo naming it by id finds it; a member's like of a
  post of another server keeps the id of the Like sent in the same column, so its Undo can name it. `ap_comment_out`
  is a member's comment's way out, as `ap_post_out` is a post's. `notifications.fed` marks what an account of another
  server left (the backfill marks the rows FWR 3 and 5 wrote), `notifications_remote` serves FWR 3's trim through it
  and replaces `notifications_fed` (migration 22): the trim now covers comments, replies and likes without reading
  the notifications members here left. `comments_remote (post_id, id)` gives a post's newest remote replies and counts
  them for its cap; its newest local comments come through `comments_post`, past at most its 300 remote replies (the
  index of local comments alone that an earlier draft added cost a write for every comment here, also in local mode,
  and was dropped after the review of #116).
- **In: a reply** (`repliedIn`). A Create whose Note passes the origin rule on the object (`noteShaped`, as for any
  post) and whose `inReplyTo` is a post of this server (`localPostOf`), a member's comment (`localCommentOf`, the id a
  comment goes out with, `/users/{name}/comments/{id}`), or a remote reply kept here (by `ap_id`). The post must be
  Public, not hidden, not a milestone, of a member who shares their profile (`publicPost` and `publicMember`); the
  comment answered shown (not deleted, not hidden); the account well; the switches a follow needs on (`followsState`:
  off drops, unread is 503 and a retry); no block, whichever blocked whom, between the account and the post's author,
  the member answered or the thread's first author. **Decided: only a public or unlisted reply is kept**: one
  addressed to its author's followers or to some accounts alone would be shown under a post anyone may read, which
  widens it. Its text is `htmlToText` cut at 1,000 characters (the comment's length here) with "Read the rest on its
  server" and the link; a content warning folds it; attachments are links only (no picture of a reply is copied). A
  reply to a reply joins the thread of the top comment, its text starting with a mention of the person answered when it
  does not already, as a member's reply does (RPL); the top comment is the `parent_id`, as for a member's. **A reply
  to a post of another server stays FWR 5's remote post** (kept when its author is followed or it names a member here,
  else dropped): it answers nothing here.
- **Update and Delete of a reply.** Only by the same account, decided by the row's `user_id` (7.3), and only with an
  `updated` newer than the one kept: the text replaced, the comment marked edited, nobody told. Another account's
  Update or Delete is refused. A Delete removes the reply, or keeps it as a placeholder with no text while it has
  answers, and removes a placeholder left with none, as a member's deletion does (`deleteCommentTx`), in FWR 5's
  transaction for a Delete, under the same lock on the object's id (`noteLock`, which a reply's Create takes too). A
  Delete of an id never kept is remembered by FWR 5's rule (`noteRememberSQL`, whoever follows the account), and a reply
  deleted here by the post's author or a moderator is remembered as a moderator's delete of a remote post is
  (`ap_deleted.by_staff`, a year): the statement that keeps a reply asks that memory for its id and account, so a late
  Create or the author's next edit does not bring it back. An edit of a reply never kept is taken as new only when the
  reply was published within 7 days (FWR 5's rule); an edit that addresses a kept reply to fewer people than Public takes
  it out, only when newer than the version kept. A reply is dated by its own `published`, never later than the inbox
  took it.
- **In: a like** (`likedIn`). A Like of a Public post of a member who shares, or of a member's comment shown under
  one, from an account with no block between it and the author: one row per account and post (or comment), a repeat
  or an emoji reaction (a Like with content) counted as the like it is. The notification is told once per post (or
  comment) and account, so liking, unliking and liking again tells the member once, as a local like does. An Undo of a
  Like (embedded, its actor the signer, matched by the Like's id or its object; or by id alone, `undoByID` looking for
  a follow first) removes the row; the notification stays, as a member's unlike leaves it. The inbox takes a Like only
  of a post or comment of this server (`likeShaped`) and with the switches on; an Undo of a Like whatever the switches
  (it only removes, as an Undo of a Follow).
- **Notifications.** `comment` to the post's author, `reply` to the member answered, `mention` to the members the
  Note's Mention tags name (local actor ids, never the text, at most 10), `like` and `comment_like`; each once, never
  across a block, marked `fed` and under FWR 3's bound (50 from one server, 500 from all, per member). **A remote
  account is told nothing here** (decided): `notifyAt` writes nothing for an account of another server in a mode that
  federates, so a member answering, liking or hiding a remote reply leaves no row nobody reads.
- **Forward** (6.4, ActivityPub 7.1.2). A reply kept for the first time (the statement that keeps it wrote a row; a
  repeat, at the inbox by its activity id or at the worker by its Note's id, forwards nothing) whose addressing names
  the followers collection of the post's author or of the member answered goes to that collection's followers on
  other servers (the post's recipients' statement, `postRecipientsSQL`, one per shared inbox), **never to the sending
  server's host**, with the body as received, signed by the instance actor, no Linked Data signature (6.4 says so; a
  receiver that wants the actor's own signature fetches the reply from its origin). A collection the reply names that
  is neither of those (another member's followers) is not forwarded to. One activity per reply: its id here is made
  from the reply's (`/actor/forwards/<hash>`), and `ap_outbox` is unique by id, so it is sent once. At most 60 replies
  forwarded an hour per sending server (`forwardsPerHostHour`); after the review of #116 also 30 an hour per member whose
  followers it goes to and 300 in all, at most 1,000 addresses and 64 KB each, never a body addressed to anyone in `bto`
  or `bcc`, and none while the delivery queue is past its resume mark or an activity waits for its fan-out, so the
  members' own posts and comments always come first (`TestDBForwardBounds`). A forward that fails (a full queue) is
  said in the log and not tried again; the reply is kept either way.

  **What a forward achieves is not shown.** Flowa's own inbox answers a delivery signed by anyone but the activity's
  actor as a forward and does not act on it (7.10, `inForward`, 202), so a forward between two Flowa servers is taken
  and dropped. For Mastodon, the reading of its documentation recorded in Sources (12 November 2026) is that its
  security page advises against implementing Linked Data signatures and uses them for relays and for the Deletes of a
  server that shuts down, and that how it treats an activity forwarded by a server that is not its actor and carries no
  Linked Data signature is not on its pages; that is not verified. docs.joinmastodon.org could not be read again from the
  session that built this (its host is not among the session's allowed hosts). If such forwards are dropped by the
  servers they are for, forwarding costs deliveries and achieves nothing; keeping it or removing it is the owner's. The Update and
  Delete of a reply are not forwarded (decided: 7.1.2 is about ghost replies; an edit or a deletion that reaches only
  some followers leaves the version they saw).
- **Out: a member's comment** (`commentOut`, the delivery queue's producer after posts). A comment of a member who
  shares their profile beyond this server, on a Public post here, made while the switches a post needs are on: its
  record is written in the comment's transaction, and a worker makes the Create, as for a post (FWR 4's rules on
  switches, a full queue and a crash hold). Its Note: id `/users/{name}/comments/{id}`, `inReplyTo` the top comment of
  its thread (a member's by that id, a remote reply by its own id) else the post, `to` the public collection, `cc` the
  commenter's followers, the post's author and the accounts it mentions (resolved from the cache alone, as a post's);
  the HTML of 6.5 (`noteHTMLWith`). Sent to the commenter's followers on other servers, the post's author's followers
  when the author is a member here (so the thread reaches those who have the post, as a forward would), and the remote
  accounts named (the author of a post or reply of another server answered among them). An edit inside the 15 minutes
  turns the record to `update` (`gen` counts edits, so one made while the Update is made is sent too) and goes as
  Update(Note) with `updated` (Mastodon applies an edit only with it) to the inboxes the Create went to, with a new
  activity id. A deletion, a moderator's hide, the post's deletion (here, or by its author's server for a post of
  another server) and housekeeping finding a sent comment gone (removed with its post by retention, say) turn it to
  `delete`: Delete(Tombstone) to the same inboxes, after its Create (`ap_outbox.undoes`, 8.8's "Taking back": a Create
  never tried is given up and nothing goes). **A comment of a member who does not share stays here** (decided: 11 says
  comments on Public posts go out for members who opted in). **Comments are not served at their ids** in this version
  (decided: the Note is embedded in each activity, which Mastodon takes from its signer's origin; an id that answers
  404 is said under Open).
- **Out: likes and comments on a post of another server.** A public or unlisted post of another server that the member
  may read (they follow its author, or it names them) can be liked and answered by a member who shares their profile;
  one who does not is told why ("Only members who share their profile beyond this server can like or answer posts
  from other servers"), so no like or answer stays here unseen by its author. The Like goes to the author's own inbox
  (6.4) with the like written in the same transaction (`enqueueThen`); the unlike is Undo(Like), embedding the Like,
  taking it back (`enqueueUndo`: a Like never tried is given up and nothing goes), with the like removed in the same
  transaction; a full queue answers 503 and changes nothing. A comment there goes as above, to the author and the
  member's followers. **A followers-only post of another server is neither liked nor answered** (decided: the answer
  would go to more people than its author addressed), and the page says so; a server the domain policy refuses or an
  author whose inbox is not on its own origin is "cannot be reached from here".
- **Bounds** (section 12). Replies: 300 from other servers under one post, 200 from one account, 2,000 from one
  server's accounts, each counted in the statement that keeps one and stopping at its cap (the server's through
  `users_remote_domain` and `comments_user`, never every remote reply); 300 new an hour from one server (in memory);
  50,000 in all, from housekeeping's hourly count (`talkCountSQL`). Likes: 2,000 from other servers for one post or
  comment, 2,000 from one account and 10,000 from one server (of posts and of comments each), 600 new an hour from one
  server, 200,000 in all (counting every like with an activity id). **Retention: a remote reply or like is kept while
  the post it is about exists**, as a member's comment is (decided: removing replies after a time would cut threads
  members answered), bounded by the caps; `-purge-remote` removes them with their accounts, the members' comments and
  likes under remote posts and remote replies with those, and every `ap_comment_out` record.
- **A flood under one post** does not push the members' comments out of the preview: `feedCommentsSQL` takes each
  post's newest comments of this server and newest remote replies through their own indexes, each at most the
  preview's size, local ones first (`TestDBQueryPlans`: 37 to 57 rows for a post holding 3,000 remote replies, ten
  times the cap). The post's counts (its comments, its likes) are read per post and grow with them, bounded by the
  caps: about 8,000 rows for the page with that post, under 1,000 at the cap.
- **Following FWR 5's review.** A member is told of a mention, and a member's comment is answered or liked, only when
  that member shares their profile and is not suspended (`publicMember`): a member who never opted in gets nothing
  from other servers. An account that deletes itself (`actorGoneSQL`) or whose domain is suspended loses, in the
  statement that removes its posts, its replies (each removed, or kept as a placeholder where a member here answered
  it, so the answer stays), its likes and the comment, reply, mention, share and like notifications it left
  (`remoteTalkGone`, `remoteTalkKinds`). The statements that keep a reply or a like ask the stored domain policy, as
  FWR 5's insert does. A reply of an account its server says is suspended is not listed (`actorShown` in
  `commentBlockFree`; a placeholder stays). In local mode no remote reply is shown, as no remote post is.
- **Privacy and authority.** The origin rule on activity and object; a Note attributed to a member here or with an id on
  this server is dropped at the inbox; a remote account cannot comment under, or like, a This server, hidden or
  milestone post, a post of a member who does not share, or a comment it cannot see; it cannot edit or delete another
  account's reply. A member who cannot read a post of another server cannot read its comments either (`commentShown`,
  `targetOwner` hold remote posts to `remoteSeen`). **Local mode**: no inbox takes one; in an instance that federated
  before, the rows stay until `-purge-remote` and `queryComments` leaves remote replies out of every list of comments
  (their counts still count them).
- **Moderation.** A remote reply is reported, hidden, restored and deleted by staff as a member's comment is, and the
  post's author deletes one under their post; the queue shows a remote post's or reply's text as text (no `@name`
  linked, fixing the same for FWR 5's remote posts). Nothing is sent for it: it is its server's.
- **Pre-existing queries that can now meet a remote reply or like**, and what was done: the timeline's and Home's
  comment counts and like counts (`postCols`): counted, as 11 says ("remote likes count with local ones"); a post's
  comments, replies and thread (`queryComments` and its queries): shown in a mode that federates, left out in local
  mode; the preview (`feedCommentsSQL`): local first; `commentShown`, `commentRow`, `handleLikeComment`: a member may
  like a remote reply, counted here and sent nowhere; `handleDeleteComment`: the author's and the post's author's
  right as before; the profile's "likes" count (likes of the member's posts): counts remote likes, as 11 says; the
  admin dashboard's weeks: narrowed to comments of this server (`c.ap_id IS NULL`), as FWR 5 narrowed posts; NodeInfo:
  counts users and local posts only, unchanged; the moderation queue and reports (`targetOwner`, `fillQueueItem`):
  remote replies reportable, shown as text; the bell (`notificationsSQL`, `unreadSQL`): shown, with the account's handle
  and host; FWR 5's remote post retention (`remoteUntouched`): a member's like or comment keeps a remote post, as 9.3
  says; the actor cache's bound (`userRefs`): comments, likes and `ap_comment_out` keep their accounts; `purgeCounts`:
  counts them. There is no export of comments or likes to change.
- **Pages.** `web/social.js` draws a remote reply with its author's name isolated and handle and host apart, its text
  through `remoteText` (no `@name` linked), its content warning as a fold, its attachments as links, "Open on its
  server", and no Edit; a public or unlisted post of another server gets Like and Comment, a followers-only one the
  sentence that it cannot be liked or answered from here. Nothing changes in local mode.
- **Rollback (14.4).** An older build after `./deploy.sh --rollback` would show remote replies as comments of local
  accounts: run `-purge-remote` first. The migration drops `notifications_fed`, which an older build only reads
  through (it would be slower, not wrong).
- Tests: `TestDBRemoteReplies` (the reply kept and shown, the forward once, only for the first sight and only to the
  collection named, never to the sending server, a reply to a reply and to a member's comment, edits by the same
  account only with a newer `updated`, a forged author, what is not answered, blocks, the switches, the caps, staff,
  the post's author, deletions with placeholders, local mode), `TestDBRemoteLikes`, `TestDBCommentsOut`,
  `TestDBTalkToRemotePosts`, `TestDBTwoFlowaReplies` (two instances over the real fetcher, signer, inbox and queue:
  reply, edit, like, unlike and deletion; FWR 8's container suite is the DoD's `TestTwoInstances`),
  `TestDBRepliesOneConnection`, `TestDBRemoteTalkNotesBounded`, `TestDBRemoteTalkFollowsItem5`, `TestDBQueryPlans` (a post holding 3,000 remote
  replies and 2,000 remote likes), `TestDBPurgeRemote`, `TestDBInboxHandlers` and `TestDBRemoteNotes` updated; the
  `federated` browser suite (`federated-remote-reply`).
- **After the review of #116** (each with a test that fails without it):
  - **A comment's activities stay in order at each inbox, and nothing deleted goes out after its Delete**
    (`TestDBCommentOrders`; reworked after the second review, below). Updates have ids the record names
    (`<comment>#update-<generation>`, kept in `ap_comment_out.last_ap_id`), and each comes after the activity before it
    (its Create, or the Update before), so the Delete takes back the comment's whole line. Posts send no Update(Note)
    (they are not edited), so they have no such case.
  - **An edit while the Create is being made is not lost** (`TestDBCommentEditWhileProduced`): every edit moves the
    record's generation, and the Create's step checks the generation it read, so it rolls back and the next pass sends the
    text as edited.
  - **A full queue holds a comment, never drops it** (`TestDBCommentQueueFull`): a Create or an Update the full queue
    refuses waits on the delivery schedule, as a post's Delete does, and is given up, said in the log and counted, only
    past its last wait.
  - **Nothing names a member who does not share** (`TestDBCommentsNameNoNonSharer`): a comment answering a member who
    does not share their profile, or in a thread one of them began, or answering a hidden comment, stays here, as the
    comments of members who do not share do, so neither the comment's id nor the name added before an answer leaves;
    the producer asks again about the thread's first author and about the member answered, whose name opens the comment,
    before each Create and Update (after the second review). A name of such a member in the text stays
    text: `resolveMentions` finds only members who share, so it is no Mention tag and no link to their actor.
  - **A notification is never lost to a failure** (`TestDBRemoteTalkNotifiedAfterFailure`): the notifications of a
    remote reply or like are written in the transaction that keeps it (`notifyFedTx`), so a failure rolls both back and
    the worker's retry keeps the reply and tells the member, where before the retry found a repeat and told nobody.
  - **Local mode on a database that federated lists and counts alike** (`TestDBRemoteHiddenInLocalMode`): a start in
    local mode asks once whether rows of other servers are kept (`remoteKept`); when they are, the statements over posts
    and comments leave remote replies and likes out before their LIMIT and out of the counts (`hideRemote`, fixed
    fragments of `postCols`, `commentCols` and `commentBlockFree`), so a page of comments is full and a post's counts
    agree with what is listed. A database that never federated runs the statements as they always were. In a mode that
    federates, a reply of an account its server suspended is left out of the counts as of the lists (`actorShown`);
    its likes still count, as there is no list of likes to agree with.
  - **Forwards are bounded and come last** (`TestDBForwardBounds`, the bullet on the forward above), and the note says
    plainly what one is known to achieve.
  - **Small points** (`TestDBRemoteTalkSmallPoints`). A removed remote reply (its author's deletion, its account's, its
    domain's suspension) is a placeholder that names nobody: the page gets no account and no link for it. A member's
    comment on an unlisted post of another server is addressed as that post is, the public collection in `cc` only. An
    id stays the kind of thing it was first kept as: an Update that adds `inReplyTo` to a kept post leaves it a post, and
    a Delete removes whatever carries its id. Housekeeping's probe for comments gone without a Delete walks the records a
    batch a pass, on from where it stopped (`orphansAfter`), so its cost is the batch, not the table, which keeps a
    record while its comment exists. The followers of a post's author that a comment goes to leave out the accounts
    with a block either way with the commenter (`postFollowersApartSQL`), as a post's mentions do; a shared inbox still
    serves every follower on its server.
  - **The caps are counted without a lock.** Each count (per post, per account, per server) is read in the statement
    that writes, under READ COMMITTED, so the two inbox workers can each pass a cap by one at the same moment: a cap is
    exceeded by at most one row (`inboxWorkers` minus one). The hourly allowances are counted in memory under a lock
    and hold exactly; the caps in all are read by housekeeping each hour, so they can be passed by what one hour lets in
    (at most the hourly allowance of each sending server).
  - **Switches that could not be read hold a comment's way out** (`TestDBCommentSwitchesUnread`): the record is written
    for a member who shares, and the producer waits for the switches to read, as a post's way out does; only a read that
    said off keeps the comment here.
- **After the second review of #116** (each with a test that fails without it):
  - **A restart forgets no comment** (`TestDBCommentRecordsAfterRestart`): the start marks the comments' records due, as
    it does the posts', and starts the workers when one waits, in each state a record waits in: a Create, a Create held
    while the switches could not be read, and an Update or a Delete the full queue held back. The other flags this item
    keeps in memory need nothing at a start: housekeeping counts the caps in all (`repliesFull`, `likesFull`) in its first
    pass, which runs at the start, and the orphan probe's place (`orphansAfter`) starts again from the first record.
  - **One slow server holds nobody else's edits** (`TestDBCommentOrders`, `TestDBLikeAgainAfterUndo`). An Update is
    enqueued at once, naming the activity it comes after (`ap_outbox.comes_after`), and the queue holds its row at an
    inbox while that activity, or one before it in the comment's line, is still queued at the same inbox, in flight or
    waiting for another attempt (`afterHeldSQL`, asked at the send as `heldBackSQL` is, the row given back until the last
    of them is due or its lease ends; no worker sleeps, and the one before is never given up for it). At every other inbox
    it goes at once. The queue fans out in order, so the one before always has its rows first; one that a Delete drops
    before its fan-out is marked, and nothing waits for it. Where the one before is given up for good at an inbox (its 11
    attempts or 48 hours spent, the domain policy, the host's cap that dropped its row), the Update is sent there after it
    all the same: it carries the whole Note as edited, so a receiver either ignores an Update of an object it never had
    (Mastodon does) or takes the edited text, which is the comment as it is; holding it longer would hold it for nothing,
    since the one before will not come. The Delete takes back the comment's line whole at each inbox
    (`ap_outbox.undoes_chain`, `takeBackSQL`): every row of the line queued there and not claimed is given up; the Delete's
    own row goes only where the Create itself was among them untried, none had an attempt and none was left queued
    (counted, so a claim made after the statement's snapshot keeps the Delete), since then nothing of the comment ever
    left for that inbox; a row in flight holds the Delete past its lease, and the send asks again over the whole line. A
    Delete made while an Update still waits for its fan-out drops it and fans out at once, not one pass later. A like
    again after an unlike comes after that Undo the same way: `ap_like_out` keeps the Undo a member's unlike enqueued last
    (it goes with the Undo's row of `ap_outbox`), and the next Like of that post waits behind it at the author's inbox,
    where it would otherwise be taken back by an Undo that arrived after it. Each step down a line is one lookup through
    `ap_outbox`'s unique id index, one per Update the comment had; a comment is edited only in its first 15 minutes, and
    edits made while its producer is busy fold into one Update.
  - **What is not sent is shown** (`TestDBForwardBounds`). The Federation tab's "Sending to other servers" says, beside
    the posts and comments whose Delete could not be taken back (`notTakenBack`, which counts comments too and now says
    so), the comments and edits the full queue refused past their last wait (`commentsNotSent`) and the replies not
    forwarded for a bound (`forwardsSkipped`), each when it is not zero. All three are counted in memory: a restart
    counts from nothing, and the tab says "since the last start". A forward asks the queue first, so one the queue
    refuses takes nothing from the hourly allowances.
  - **An answer that could not go is refused, not kept unseen** (`TestDBRemoteAnswerThatWouldStay`). Under a Public
    post here, an answer to a hidden comment or to a member who does not share stays here, which is right: the post is
    here too. Under a post of another server it would stay here where none of that post's people see it, so it is
    refused with the reason (`talkStaysHere`, 403), as `talkGate` refuses a member who does not share.
- **Open.** A comment's id is not served (a receiver that fetches it by id gets 404; one that takes the embedded Note,
  as Mastodon does from the actor's origin, is not affected); an Update can no longer reach an inbox before the
  Create it changes (above), but at an inbox where the Create failed for good the Update arrives alone, and what a
  receiver does with an Update of an object it never had is its own (FWR 5 here takes one as new only when recent); a
  member's like of a remote reply stays here; a member who stops sharing after a comment that answers or names them
  went out gets nothing taken back (the comment stays on the servers it reached; only its later edits stay here); a
  remote reply to a member's comment
  under a post of another server is not kept (it answers no post here); the likes of an account its server says is
  suspended still count; recorded Mastodon replies and likes to hold this to (the owner's trial, FWR 9).

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

As built (FRD 5), the Note a Public post answers at its id (`testdata/federation/note-ana.json`), as much of the
above as this version can stand behind: `@context` as above, `id` and `url` the post's address, `type`
`Note`, `attributedTo`, `published`, `to` the public collection alone (6.7), `sensitive` false, `content` by
the rules above (`p`, `br` and `a` only; the chips as "Tricks: name (Prop), ..." and the combo as "Combo:
Name: A, B" in paragraphs of their own, names from the latest published map), and the picture as an `Image`
attachment with its type, size, and empty `name` and `summary`. Left for the items that serve or send them:
the followers collection in `cc` (it does not answer until FRD 6), mentions and the `h-card` form and the
`Mention` and `Hashtag` tags (FWR 4, which also teaches `mentionRE` the remote form; until then a mention is
plain text), and `flowa:tricks`. A reader that knows only ActivityStreams sees the same post either way. No
activity wraps it: the Create is FWR's. As built (FRD 6): `cc` is the author's followers collection, which now
answers, and the Create that wraps the Note is built (`createOf`, 6.2's "As built (FRD 6)"), served in the
outbox and at its own id; FWR 4 sends the same Create.

#### Public posts out, as built (FWR 4)

"federation: public posts out" in `main.go`, migration 21 (with `flowa-schema20.dump` for the restore drill). What was
built, and the decisions where 6.4, 6.5, 6.7 and 14.5 were silent:

- **What is sent, and when** (`postsGoOut`). A post of kind `post`, audience Public, by a member who shares their profile
  beyond this server (F2; `postAudience` gives Public to no one else), made while the mode federates, federation is not
  refused, and the `federation` and `follows` flags are both on (14.1). Milestones (F13), This server posts and every
  other audience send nothing: the API offers only Public and This server (F3), and the worker asks `publicPost` again
  (Public, not hidden, kind `post`), so a milestone made Public by hand is still not sent. **A post made while either
  flag is off is never sent later** (decided here: the switches are the admin's pause, 8.8 lets the queue hold rows
  while paused, but a backlog of posts going out when a switch comes back on would surprise the member who posted under
  it, and burst the queue).
- **Durable from the first statement; nothing waits on the network.** The post and its record (`ap_post_out`, state
  `create`) are written by one statement (a `WITH ... INSERT`), so neither exists without the other. A worker of the
  delivery queue (its producer, `sendOut`, asked before each claim and only when something may wait) reads the
  oldest record, makes the Create with `createBody`, finds the recipients, and enqueues it with the record turned to
  `sent` and the inboxes stored in `targets`, **in the same transaction** (`enqueueThen`, the queue's `enqueue` with a
  step of the caller's before the commit). A crash between the post and the worker leaves the record, which the next
  start finds and sends; a crash inside the worker rolls back both the activity and the record's change. Two workers
  (two processes during a deploy) may make it at once: `ap_outbox` is unique by the activity's id, so one writes and
  the other's update of the record finds no row in state `create` and rolls back. A full queue (`errDeliveryFull`)
  lets a Create go unsent, as an outage would (8.8), said in the log; nothing left, so its Delete is not needed later.
  **A Delete the full queue refuses is kept** (after FWR 4's review: it was dropped too, and the post stayed on the
  servers it went to): its record is held for the delivery schedule's next wait (8.3: 1 minute, then 5, 15, ...;
  `ap_post_out.tries` and `due_at`) and tried again then, and given up, said in the log, after the schedule's last
  wait (about 40 hours), counted on the Federation tab since the last start ("could not be taken back"). A worker's step
  asks nothing meanwhile (the producer knows when the first held record is due).
  **The switches are asked again at the send**: a Create or an Update(Person) found with the `federation`, `follows`
  or `community` flag off is dropped, and turning one of them off drops what waits at once, rather than sending it when
  the switch comes back on; a Delete stays, since it only takes back what already left. **Switches that could not be
  read are not "off"** (as FWR 3's follows read them since its second review, `flagsKnown`): nothing is dropped or sent,
  the record waits, and the worker looks again after its pause; an actor document made then (its
  `manuallyApprovesFollowers` a guess) is neither kept nor sent in an Update (`TestDBOutSwitchesUnread`). The worker
  claims no record with a lease: it reads one and enqueues its activity with the record's change in one transaction,
  so a stop in the middle leaves the record as it was, for the next pass.
- **Recipients** (`postRecipientsSQL`, one statement, at most 10,001 rows): the accepted followers whose cached actor is
  well, has an inbox, and whose account no moderator suspended here, and the remote accounts the post mentions (none
  across a block either way), **one row per delivery address**: the shared inbox when it is on the actor's own host,
  else the actor's own inbox, the rule `enqueue` applies (8.8), which also leaves out the domains the policy refuses,
  hosts marked unavailable and this instance. The follower's account and actor are looked up by key in a `LATERAL`
  subquery per follow, never by joining `ap_actors` whole (`TestDBQueryPlans`: 1,000 followers on 300 servers, about 12
  rows counted over the plan's steps per follower, none for other members' follows). **A member followed at more than
  10,000 addresses** (the queue's bound for one activity, 8.8, after the shared inboxes) has an activity sent to the
  first 10,000 in the order of their addresses, one line in the log says so, and the Federation tab counts such
  activities since the last start ("reached only the first 10,000 addresses", after FWR 4's review); batching beyond it would need a
  second activity id for one post, which receivers would read as a second post. Section 12's instance (1,000 followers
  on 300 servers) is far below it. The Create is sent with `application/ld+json; profile=...` (ActivityPub 7, the
  delivery kind of 7.8), signed with the member's key at the send.
- **The record** (`ap_post_out`): the inboxes the Create was enqueued for, after shared inboxes, as JSON, at most 10,000
  per post, kept while the post exists. A deletion or a hide marks it `delete` in its own statement; the worker then
  enqueues the Delete for exactly those inboxes and removes the record in the same transaction. So a Delete reaches the
  servers the Create went to, also a follower's who left since, and not one who arrived since. The record has no
  foreign key to `posts` (it outlives the post until its Delete is queued); its member's deletion takes it (FWR 7 sends
  Delete(Person) then). `-purge-remote` removes every record, since each names other servers.
- **Delete(Tombstone)** on a deletion by the author or a moderator, and on a moderator's hide (6.5): id the post's
  address with `#delete` (as Mastodon names a status's Delete), `object` `{"id": <post>, "type": "Tombstone"}`, to the
  public collection and cc the followers. A restore sends nothing (6.5), and a deletion after a hide sends nothing
  more. A post deleted or hidden before its Create was made sends nothing at all. **A post deleted or hidden while the
  server ran in local mode** (where nothing marks its record, since no federation runs) is still on the servers it went
  to: every start that federates marks such records for a Delete, which then goes (after FWR 4's review;
  `TestDBDeleteAfterLocalMode`). **A Delete is sent even when the
  member has stopped sharing** (decided here: 11 says turning sharing off sends nothing, but a Delete only takes back
  what already left, and a moderator's hide must reach other servers whatever the member did since). The Note's
  address then answers 404, as FRD 5 decided (nothing keeps a deleted post's id for a Tombstone; ActivityPub 6.4 makes
  replacing one a MAY). **No audience narrowing and no Update(Note):** posts are not edited (1.2) and no route changes a
  post's audience (11), so neither exists; a remote reader sees a post as it was first sent until its Delete.
- **The Note** (`noteOf`, the one function behind the Note's address, the outbox, the Create's address and the body
  sent, held by `TestNoteFromPost` to `note-full.json` and `create-ana.json`, the object sent compared byte for byte
  with the Note served): `to` the public collection, `cc` the followers and every actor mentioned, `content` as below,
  `attachment` the picture with its alt text as both `name` and `summary`, `tag` the Mentions then one Hashtag per prop,
  `flowa:tricks` and `flowa:combo`. `testdata/federation/note-ana.json` gained its Hashtag, tricks and combo.
- **The HTML** (`noteHTMLWith`). Mastodon's ActivityPub page ("HTML sanitization", `lib/sanitize_ext/sanitize_config.rb`,
  read on 14 November 2026) keeps `<p>`, `<span>` (class), `<br>` and `<a>` (href, rel, class) in every version, keeps
  classes that begin with `h-`, `p-`, `u-`, `dt-`, `e-` or are `mention`, `hashtag`, `ellipsis` or `invisible`, and
  keeps a link's protocol only for http, https, dat, dweb, ipfs, ipns, ssb, gopher and xmpp. Flowa writes only `p`,
  `br`, `a` and `span`; `a` only with `href` and either `rel="nofollow noopener"` (a link in the text), `class="u-url
  mention"` (a mention, inside `<span class="h-card">`, the name in a `<span>`), or `class="mention hashtag" rel="tag"` (a
  hashtag); `span` only bare or with `class="h-card"`. Every character of the member's text goes through
  `html.EscapeString` (`<`, `>`, `&`, `'` and `"`); only `http` and `https` text becomes a link (`javascript:` and
  `data:` stay text); a mention or a `#word` inside a link stays part of the link; control characters (but a line break)
  and the marks that embed, override or isolate a direction are dropped from what is written, after ending any name or link they stand in (`markText`, `escText`: "@ann", a mark, "a" mentions ann; a lone carriage return is a space; the app's own page still shows the
  text as stored); a mention's address is a link only when it is https with nothing that could end an attribute
  (`linkable`), else the actor's id is, else the mention stays text. (So a mention's link is always https; a link in the
  text may be http or https, as 6.5 says and Mastodon's sanitiser keeps both.) `TestNoteContentEscaping` and `FuzzNoteContent`
  (30 s, about 214,000 runs, no failure) hold every output to that subset with no `<` of the input left unescaped.
- **Mentions** (`parseMentions`, finding 9). `@name` is a member here as before (up to 24 characters, the first a letter
  or digit); `@name@host`, the host an ASCII DNS name of two labels or more whose last label holds a letter (a full stop
  after it ends the sentence), ending before a space or punctuation (never before another `@`, an underscore or a
  letter, digit or mark), is an account of another server. **Anything shaped `@name@...` is never a member here**: when
  its host is not such a name (a host in another script, `h.exa_mple`, two dots, a dash at a label's end, more than 253
  characters, nothing at all) it is plain text, whole (after FWR 4's review: these used to tell the local member). A
  host in another script stays text because hosts are kept in their `xn--` form (9.2) and converting needs
  `golang.org/x/net/idna`, which is not a dependency; its `xn--` form is read. On this instance's own host
  (`@anna@flowa.example.org`) it is the member here, on the server and on the page alike (the page reads its host from
  the member's handle, which `/api/me` gives only in a mode that federates; in local mode neither side has a host of its
  own, so such a handle is text on both). **One reading of a text** (`bodyMentions`): mentions are only looked for in the
  runs between links, as the content shows them, and the text is read as written (names compared in lower case, as
  usernames are), so what is notified, what is linked, what is addressed and what is delivered to are the same: an
  account named inside a link (`https://x.example/@anna`) is none of them. **This changes two things in local mode**,
  both small and correct: a name inside a link no longer tells that member (the page never linked it), and
  `@anna@host` with a host that is not one no longer tells anna. The page reads text the same way (`mentionsIn` in `web/links.js`, which
  `richText` uses, showing another server's handle as text), held to the cases of `test/mentions-fixtures.json` by
  `TestMentions` and `node test/mentions.js` (in the lint job). Notifications use it, so `@anna@social.example` no longer
  tells the local anna. When a Public post goes out, its mentions are resolved **from the cache alone, with no fetch**
  (`mentionLookupSQL`, one statement, at most 10 mentions, `maxMentions`): a member here who shares their profile, or
  a remote account whose cached actor is well and not suspended here, neither the author nor across a block. **A handle
  the cache does not hold stays plain text**: no Mention tag, no address in `cc`, no delivery, and nobody there is
  told; the member can look the account up first (11, people elsewhere), which fills the cache. What was resolved is
  stored with the post (`posts.ap_mentions`), so the Note is the same bytes every time it is made, whatever the cache
  holds later. A mention's link goes to the actor's profile page when it names an https one (Mastodon's own form, 6.6's
  correction), its tag's `href` is the actor id and its `name` the handle with its `@`.
- **Hashtags**: one per prop of the chips and of the combo (decided here: 6.5 says "in the chips", and a combo's prop is
  as much the post's), each once, `{"type": "Hashtag", "href": "<origin>/#/prop/<prop>", "name": "#<prop>"}`, and as
  links at the end of the last line of the text fallback, as 6.5's example puts it.
- **`flowa:tricks` and `flowa:combo`** (F14's namespace): each chip as `{"type": "flowa:Trick", "flowa:prop",
  "flowa:skill", "name"}`, the name from the latest published map; the combo as `{"type": "flowa:Combo", "name",
  "flowa:prop", "flowa:steps": [Trick, ...], "flowa:reps"}` (repeats only when the combo has them). The combo's role
  (reliable, signature, stretch) is the member's own measure of their progress and its id names their progress
  document: neither is sent (14.5, F12). The text fallback ("Tricks: ...", "Combo: ...") is in `content` as before.
- **Alt text** (`posts.alt`): a field "Describe the picture (optional)" under the picture in the composer, **in every
  mode** (decided here: it is an accessibility gain for members of a local instance as much as for readers elsewhere,
  and nothing in the design ties it to federation). It appears only once a picture is attached, so a composer without
  a picture is as it was; a post without a picture sends no `alt`, and the API answers `alt` only for a post that has
  one, so every answer about a post without alt text is byte for byte as before; routes and headers are unchanged. At
  most 1,500 characters (`maxAlt`, counted as sent: the characters of the trimmed text, before anything is taken out, as the composer counts them), with what `visibleText` takes out of a bio taken out, line breaks joined into
  spaces, and a direction override refused with the reason; a description sent without a picture is dropped. It is the
  picture's `alt` on the post in the app and on the public pages (else the words there were), and `name` and `summary`
  in the Note. Posts are not edited (1.2), so it is written once, with the post. The field has a counter as the post's
  text has, counting characters as the server does (an emoji is one; a browser's `maxlength` counts UTF-16 units, so the
  field has none, and a description that is too long is refused before it is sent, with the count). The moderation
  queue shows a reported post's picture with its description as written (after FWR 4's review). `a11y.py`, `keys.py`
  and `social.py` hold the field's name, its counter, its keyboard path and the post's alt.
- **Update(Person)** (6.4): a profile saved with a change the actor shows (the name, the bio, the picture, the props, the
  links, approving by hand), by a member who shares, with both flags on, marks the member's row of `ap_person_out` due
  in the profile's own transaction: now when none went in the last 5 minutes (`personUpdateEvery`), else 5 minutes
  after the last one; a row already due keeps its time. The worker makes the actor document as it is then (`actorOf`,
  the function the actor's address serves), so the newest state wins; `gen` counts the changes, so one made while an
  Update is being made leaves the row due again. The coalescing is in the database, on the delivery queue's clock, and
  survives a restart; no timer per member exists. Turning sharing on again sends no Update, and a member who stopped
  sharing gets none. To the followers by the same statement as a post, with a new activity id, the document without its
  own `@context` (the activity carries the actor's).
- **Bounds**: a post's text at most 2,000 characters as before, at most 10 mentions resolved and 10 chips (so at most 5
  hashtags), one activity to at most 10,000 addresses, a Create of more than 256 KB not sent (none can be: the text and
  its fallback are a few KB, the picture is an address), one record per post, one row per member for profiles.
- **What leaves** is what 14.5 lists: `TestDBNothingPrivateLeaves` sets every private field (practice and its notes,
  progress, stats, preferences, the password hash and sessions, bookmarks, a This server post, a hidden post, a
  combo's role and id) and finds none of them in the bytes enqueued for a Create, an Update and a Delete, and a post that
  is not Public makes no record and no activity.
- **Cost on the hot paths.** Local mode: `POST /api/posts` sends Postgres 8 statements, as on the commit before
  (`TestDBPostLocalStatements`, a driver that counts every statement; the alt text is a column of the same insert).
  In a mode that federates: a Public post that goes out is the same insert as one statement with its record, plus one
  statement when its text holds a mention; a deletion is one statement as before; a hide adds one statement to its
  transaction; a profile save that changes what the actor shows adds one. The worker's statements take the federation's
  turns (8.8), one at a time, and none is held across another wait.
- **What the admin sees**: the Federation tab's counts are the queue's (a post's deliveries are rows like a follow's),
  with one more when it is not zero: the activities that reached only the first 10,000 addresses since the last start;
  its subtitle now says what is sent. The dashboard's sentence and `fedBuilt` say posts go out; NodeInfo's
  parts add `posts`.
- Tests: `TestNoteFromPost`, `TestNoteContentEscaping`, `FuzzNoteContent`, `TestMentions` (and `node test/mentions.js`),
  `TestDBPostFanOut` (600 followers on 300 servers give 300 deliveries, a server with no shared inbox one per follower,
  a suspended domain nothing, a gone actor, a pending follow and a suspended account nothing, a mentioned account one, a
  This server post, a post naming no audience, the audiences not offered, a milestone (also one made Public by hand),
  each flag off, a member who stops sharing and federation refused nothing; a Create made twice is one activity),
  `TestDBPostDeleteAndHide` (the Delete to the recorded inboxes after followers changed, a hide, a restore, a deletion
  after a hide, a post deleted or hidden before its Create, a record that cannot be written leaves no post),
  `TestDBPersonUpdateCoalesced` (on the test's clock), `TestDBNothingPrivateLeaves`, `TestDBTwoFlowaNotes` (two Flowa
  instances over the real fetcher, signer, inbox and queue: the other one's inbox verifies the Create and the Delete
  and counts and drops them, notes in being FWR 5), `TestDBNotesOneConnection`, `TestDBPostLocalStatements`,
  `TestDBQueryPlans`, `TestDBPurgeRemote`, `TestDBEvictionCoversReferences` (the two new tables in `userRefs`),
  `TestDBLocalModeUnchanged` extended, with `-race -count=3` on two cores.
- **Open** (later items, or the owner's): a recorded Mastodon post confirming the h-card nesting and which of `name` and
  `summary` it writes for alt text (the owner's trial, FWR 9); Update(Person) after `-rotate-keys` (5.7 asks for it;
  the rotation is a command run beside the server and does not mark the rows yet); a Delete when a member is suspended
  (the public addresses hide their posts at once, nothing is sent); a remote handle that is not in the cache is not
  fetched while posting (a lookup first fills it); a moderator's hide of a suspended member's post sends a Delete
signed with a key whose actor document then answers 404: Mastodon checks such a Delete against the key it already holds
(its security page, on a server shutting down, and 6.3), and how other software answers is not checked; the Federation
tab has no count of the records waiting for their
  worker (they wait for a step, seconds at most); the fan-out time on the Pi (FWR 2's benchmark covers it).

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

As built (FRD 8), for the search by handle, which needed it before FWR 5 (the bio and posts of a remote account
are shown; the order of 14.3 had it with the Home timeline): `htmlToText` as this section specifies, with these
decisions and one correction. `<br>` and `</li>` are a line break and `</p>` a blank line, so paragraphs stay
apart; a link whose text is its address already (Mastodon writes the whole URL with parts in hidden spans), or
the address without its scheme or last slash, keeps its text alone, any other link gets its address after it in
brackets, and only an http or https href is written; a link a Hashtag names keeps its text (`#poi`); comments,
CDATA, `<!...>` and `<?...?>` are dropped; the marks that embed, override or isolate a text's direction
(U+202A to U+202E, U+2066 to U+2069) and control characters are removed, and the page shows each text isolated
(`dir="auto"`). Whatever is left open at the end (a tag, a quoted attribute, a comment, a script) is dropped,
and for a text the decoder cut (`Cut`) an entity without its semicolon too. **Correction:** Mastodon's mention
link goes to the profile page (`https://social.example/@bo`) while its Mention's `href` is the actor id
(`https://social.example/users/bo`), as this note's own example of 6.5 shows, so matching links to Mentions by
`href` alone finds none: a link is a Mention's when its `href` is the Mention's, or when its text is the short
`@name` of the Mention's handle and its `href` is on the host of the Mention's `href`; it becomes the
Mention's name when that carries a host, else the name with that host. `TestRemoteHTMLToText`,
`TestRemoteHTMLFixtures` (every post and actor under `testdata/federation`, none recorded), `FuzzHTMLToText`
(30 s, no failure) and `BenchmarkHTMLToText` (64 KB of each kind of markup in under 2 ms on a 2.1 GHz Xeon) hold
it. Nothing converted is stored: the search shows and forgets it. Remote text is shown escaped as plain text,
not through `richText`, which would still read `@bo@social.example` as the local `bo` until FWR 4 (finding 9).

#### Remote content in, as built (FWR 5)

Posts from followed accounts in Home ("federation: posts from followed accounts" in `main.go`; migration 22 with
`flowa-schema21.dump`). What, and why where the note left room:

- **Where a remote post is kept.** A `posts` row (F5, 9.2) with `ap_id` its id, `ap_updated` the `updated` of the
  version kept, `ap_to` the members here who may open it from a notification, and `ap_extra` what is shown beside the
  text (the link to the original, a long post's title, the content warning, whether its pictures are sensitive, its
  `published`, the post it answers). Its audience is `public`, `unlisted` or `followers` as 6.7 reads it. Indexes:
  `posts_ap_id` (unique), `posts_local (id DESC) WHERE ap_id IS NULL` for the Community timeline, `posts_remote
  (created_at, id)` for retention and the cap. `ap_media` is the picture cache's index (one row per attachment, removed
  with its post), `ap_deleted` the 7 days' memory of deletions that came before their post, `notifications_fed` the
  trim of what other servers leave in the bell, `reports_target` the question "is this post reported" one post at a time.
- **What the inbox takes.** A Create or an Update whose object is embedded, of a kind 6.6 shows (Note, Question,
  Article, Page, Image, Audio, Video, Event), with its id on the actor's origin and attributed to the actor alone
  (`noteShaped`: 7.3 on the object, not only on the activity); one given by id only is dropped and counted, since
  fetching it from its origin is not built. A Delete of an object on the actor's own origin. An Announce whose object
  is a post of this server. Create, Update and Announce need the switches a follow needs (`followsState`): off drops
  and counts, switches that cannot be read answer 503 and a worker tries a stored row again later. A Delete only
  removes, so it is always acted on. Every type keeps the inbox's own allowances and caps (7.10).
- **When it is kept** (`noteIn`). The author is followed by a member here (accepted), or the post's Mention tags name a
  member here (a `href` that is a local actor id) whom no block separates from the author; for a followers-only post
  that member must also be in its addressing, so no audience is ever widened. **Decided:** a post that mentions a
  member and is from an account nobody follows is kept (6.3's row says so), shown only to the members it names, from
  the bell, never in a Home. A direct or limited post (neither the public collection nor the author's followers
  collection) is dropped (F3). An account marked suspended or gone stores nothing new. A Create of an id kept or
  remembered as deleted is a repeat.
- **Update.** Only of a post the same account wrote, decided by the row's `user_id` (7.3: never a string a remote
  document supplied), only with an `updated` newer than the one kept (Mastodon's rule, 6.3): the whole post is
  replaced, its pictures with it. An Update of a post never kept is taken as its Create would be, from the embedded
  object (no fetch). **Decided:** an edit notifies nobody, so a server cannot repeat a mention by editing.
- **Delete.** Of a post the same account wrote: the post goes with its notifications, its pictures' rows (`ON DELETE
  CASCADE`) and their files. Of another account's post: refused. Of a post never kept: remembered 7 days with the
  account that deleted it, whoever follows that account (a post naming a member is kept without a follow, so a
  mention deleted at once must not tell anyone when its Create comes late), so that account's Create arriving after
  it, or its Update taken as a Create, is refused (6.3); another account of the same server cannot block a post by
  deleting its id ahead. Bounded: 100 remembered per account (past it a Delete is acted on and not remembered), 600 an
  hour per sending host, 20,000 in all (housekeeping drops the oldest). Corrected after the review of #115: before, a
  Delete was remembered only for a followed account, by id alone, without a cap.
- **Text** (`remotePost`, with `htmlToText`, which now also ends a line at the close of a heading, a quote, a list, a
  `div` or a `pre`, for long posts). The first 2,000 characters, then "…" and "Read the rest on its server:" with the
  link; a text the decoder cut at 64 KB inside a tag, an attribute, a comment or an entity loses the unterminated tail
  and still ends with the link. The link is the post's first `url` on the author's own host, else its id: a `url`
  elsewhere would send a reader where the author chose under this server's name (the lookup's rule, 11). The content
  warning (at most 500 characters) is a fold to open (a `details` element: a button, no script); a poll is its
  question, each choice on a line (at most 20, 200 characters each; `oneOf`, else `anyOf`, which the decoder now
  reads) and "This is a poll; vote on its server:" with the link; an Article, Page, Image, Audio, Video or Event is its
  `name` as a title (200 characters), its summary or its first 500 characters, and the link. Custom emoji stay
  `:shortcode:` text; quote fields and other unknown properties are ignored (a quote's `RE:` paragraph is text).
- **Mentions.** A short `@bo` linked as a Mention of the post becomes `@bo@social.example` (the matching of FRD 8);
  the page shows a remote post's text escaped and links only web addresses in it (`remoteText`), never an `@name`, so
  a plain `@anna` in a remote post neither links nor notifies the member anna here. Who is told is decided from the
  Mention tags' `href`s that are local actor ids, at most 10, never from the text.
- **Announce** of a member's Public post (of a member who shares their profile): a `share` notification to its author,
  once per post and account, never across a block; of anything else, dropped. The bell's notifications from other
  servers stay bounded by FWR 3's trim (50 per server, 500 in all, per member), which now reads shares and mentions too.
- **Home and everything else.** Home (`homeSQL`) reads a remote account followed as one more account, a page at a
  time through `posts_user`, and since the review of #115 leaves out an account whose server says it is suspended or
  gone (`actorShown`) and, outside a mode that federates, every remote post (`$5`); it orders by arrival (the row's id). `feedSQL` (the Community timeline, a member's page and the bookmarks) reads
  local posts only through `posts_local`; a post opened alone (`postOneSQL`) is a remote one only for a follower of its
  author, a member it names (`ap_to`), or staff; `postShown` (likes, comments, bookmarks) refuses remote posts until
  FWR 6; a report of a remote post needs the same right to read it; `publicPost` (the public pages, the outbox, the
  Notes served) and NodeInfo's `localPosts` count this server's posts only, and the dashboard's weeks count local
  posts. The page draws a remote post with the author's name isolated and its handle and host apart, the time, "Open
  on its server", and no like, comment or bookmark ("Replies and likes for posts from other servers are not built
  yet"); Report and a moderator's Delete and hide stay. A block hides the account's posts (blockFree) and ends the
  follow, so nothing more is kept unless another member follows it.
- **Bounds.** 200 posts kept per account (a Create past it drops the account's oldest, 50 at most per Create), 600 new
  posts an hour per sending host (in memory, then dropped and counted), 50,000 in all and 30 days for a post nobody
  here liked, commented on or reported (9.3; housekeeping, 1,000 rows a statement, through `posts_remote`). An account
  unfollowed by its last follower here keeps its posts until those 30 days, read by nobody (no Home shows it), and the
  actor cache never evicts an account with posts (`posts` is in `userRefs`). `TestDBQueryPlans` holds it with 20,000
  remote posts: the Community timeline 284 rows read, Home of 600 accounts about 36,900, a post alone 1 to 35, a
  batch of retention about 11,300 and of the cap about 25,300, the worker's claim 5.
- **Pictures (F6, 9.4), with one correction.** 9.4 fetched a picture on its first view, in the request of the member
  who viewed it; built instead: a worker (`mediaCache`) fetches the pictures of a post when it is kept, one at a time
  in all (so one per host), through the guarded fetcher (`fetchPicture`: no private address, 8 MB and 10 s, at most 3
  redirects, each checked again), holding no connection or turn while it waits; so no member's request ever waits on
  another server and nothing is fetched while a page is drawn. The bytes decide the type (`image.DecodeConfig`: JPEG,
  PNG or GIF, whatever the header said, which only lets an image type through first), the size is read before
  decoding (4,096 px a side, the bound `shrinkPicture` is given), one picture is decoded at a time with link cards'
  thumbnails (`pictureMu`; a member's own upload is decoded in its own request, `cleanImage`, as before), the first
  frame of a GIF, and the copy is
  a JPEG of at most 1,280 px and 200 KB made from the pixels alone. Each is written once, whole, to a temporary file
  renamed into place under `-data/media`, and its last use is written at most once an hour, for an SD card's sake. At
  most 200 MB and 4,000 files: past either, the least recently used are marked evicted (the row first, then the file)
  until 160 MB and 3,200 files; at most 2,000 wait to be fetched (past that a new one is a link). A picture not copied
  yet, failed or evicted shows as a link to the original, and an evicted one is wanted again when a post that shows it
  is read. `GET /api/fed/media/{id}.jpg` serves the copy to a member who may read the post, with the headers of a
  picture (`private, max-age=86400`, `nosniff`); the CSP's `img-src 'self' data: blob:` is unchanged. A time out, a
  refused connection or a server error is tried again after 1 minute, 10 minutes and 1 hour; anything else fails at
  once. Housekeeping removes files no row names, older than 10 minutes. Not built: the domain policy's `reject_media`
  (FMD's), avatars and headers of remote accounts.
- **Rollback (14.4).** This is the first version to keep remote posts: an older build after `./deploy.sh --rollback`
  shows them as local, in the Community timeline, so `/nunchaku -purge-remote` must run first, with the Federation
  switch off (the inbox then answers 503 and its workers take no row, so nothing is stored between the purge and the
  rollback: a post naming a member needs no follow). It now also counts and removes the pictures' rows and the
  remembered deletions, and empties `-data/media`.
- **After the review of #115** (each with a test that fails without it):
  - An account its server says is suspended (`ap_actors.state`) is left out of Home, of a post opened alone and of
    the picture route (`actorShown`, one primary key look per account) until its server lifts it; an account that
    deletes itself loses its posts here (their pictures with them) and its follows both ways in the statement that
    marks it gone (`actorGoneSQL`). Suspending a domain removes, in the statement that ends its follows, the posts its
    accounts left (kept for a follow or a mention), their pictures and the mention and share notifications they sent;
    the picture route asks the domain policy too.
  - A post may name only a member who shares their profile and is not suspended (`publicMember`, as an Announce
    asks); a member who never opted in gets nothing from other servers.
  - Posts kept only for a mention (`posts.ap_mention_only`) have a cap of their own, 5,000, so servers nobody here
    follows cannot push followed accounts' posts out; the 50,000 counts the others.
  - A Create and a Delete of one post take turns on its id (`pg_advisory_xact_lock` inside their transaction, never
    across a fetch), so two workers cannot both miss each other.
  - A moderator's delete of a remote post is remembered like its author's own, so an edit from its server does not
    bring it back. An edit that makes a kept post direct, or takes out the only mention that kept an unfollowed
    author's post, removes it (only when newer than the version kept).
  - The picture worker starts at a start when a picture waits; a picture a reader asks for again after its
    eviction is asked for at most once an hour, and only while fewer than 2,000 wait (the soft bound of the rows
    written at the same moment by the two inbox workers apart); a write that fails (a full disk) holds the worker off
    for a minute.
  - An undecodable Delete stored since migration 22 is not acted on (only older rows are read as an actor's own).
  - Links: the original is on the author's host (`remoteLink`); a reply's `inReplyTo` and an attachment's address are
    https addresses on any host, by nature (a reply may answer anyone; Mastodon serves files from a host of its own),
    and the page names the host beside each. A `published` later than the time received is shown as that time.
  - **After the second review.** A moderator's delete is remembered a year (`ap_deleted.by_staff`), outside the 7
    days and the 20,000 of the others, and goes with its account or by `-purge-remote`; an edit of a post never kept is
    taken as new only when the post was published within 7 days; a post is dated by its own `published`, never later
    than the inbox took it (`received_at`), which retention counts from, while Home lists by arrival. An edit that
    would remove a post compares the version kept inside the transaction that holds the post's lock. A self-deleted
    account's shares go with it. Posts kept for a mention have a share per host of 500 (a tenth of the 5,000), trimmed
    first, and stop counting as kept for a mention when a follow of their account is accepted. Staff see the pictures of
    the posts they may open, a suspended account's too. A Create checks the stored domain policy in the statement that
    keeps it, so a domain suspended while a worker holds the post keeps nothing. A page asks again for an evicted
    picture only when one is due, with no statement otherwise, and wakes the worker only when one changed. A named
    member reads the post only while they share their profile (or own the instance) and are not suspended. The cap in
    all reads `posts_remote_followed`. `-purge-remote` says when the Federation switch is still on: turn it off, wait 5
    seconds, and run it.
  - **After the last review** (with FWR 6). The accept of a follow clears the mark of the posts kept for a mention in
    the same statement (`followAnswered`), so a failure cannot leave the follow accepted and the mark set for good. The
    mark is not set back when a follow ends: a post kept for a follow stays counted as one until its next Update, which
    reads who follows the account then. An Update without `published` of a post never kept is dropped, as one published
    longer ago than 7 days is (an edit is taken as new only for a recent post). A Create of a post published longer ago
    than the time remote posts are kept (30 days) is dropped at intake and counted ("published before the time posts are
    kept here"): stored, it would have spent the host's hourly allowance and told the members it names, then gone with
    housekeeping within the hour. The check of the stored domain policy in the statement that keeps a post covers a
    suspension only: a domain taken off the allowlist while a worker holds a post is still kept, as the policy in memory
    said when the inbox took it.
  - **Local mode on a database that federated.** The remote accounts stay and the follow lists show them as before
    (FWR 3), but their posts are not shown (`homeSQL`'s `$5`, `postOneSQL`'s `$4`), the picture route is not there and
    housekeeping does not touch them; they show again when the mode federates again, and `-purge-remote` removes
    them. Local mode is otherwise as it has always been.
- **Tests.** `TestRemoteHTMLToText` (the hostile corpus: script, style, event attributes, `javascript:` and `data:`
  links, entities numeric and overlong, comments, CDATA, direction marks, a megabyte of nesting, a text cut inside a
  tag, an attribute, a comment, an entity), `TestRemotePostFixtures` (`testdata/federation/notes`, written from
  documentation: a content warning, a poll, attachments of five kinds, a quote with unknown fields, a hashtag and a
  custom emoji, a long Article, followers-only, direct, short mentions), `FuzzRemotePost` (30 s), `TestDBRemoteNotes`,
  `TestDBRemoteNotesBounds`, `TestMediaCacheBudget`, `TestDBMediaCacheBudget`, `TestDBRemoteNotesOneConnection`,
  `TestDBQueryPlans`, `TestDBPurgeRemote`, `TestDBTwoFlowaNotes` (one Flowa's post through the real fan-out reaches the
  other's follower's Home, and its Delete removes it), and the `federated` browser suite (`federated-remote-post`).
  After the first review of #115: `TestDBRemoteActorSuspendedOrGone`, `TestDBRemoteDomainSuspended`,
  `TestDBRemoteDeletedMemory`, `TestDBMediaWorkerAtStart`, `TestDBRemoteNotesWhoReads`, `TestDBRemoteMentionOnlyCap`,
  `TestDBRemoteCreateDeleteRace`, `TestDBRemoteNotesTakenAway`, `TestDBMediaWriteFails`, `TestDBRemoteNotesEdges`,
  `TestDBRemoteNotesInLocalMode`. After the second: `TestDBRemoteStaffDeleteHolds`, `TestDBRemoteRemoveLosesToNewer`,
  `TestDBRemoteGoneSharesAndStaffPictures`, `TestDBRemoteMentionShare`, `TestDBRemoteSuspendedWhileStoring`,
  `TestDBRemoteReadsCostAndRights`, `TestDBPurgeRemoteSwitchOn`.

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
  says, an RSA key is verified as RSASSA-PKCS1-v1_5 with SHA-256 (cavage-12 2.5: the verifier "MUST
  derive the digital signature algorithm from the metadata associated with the keyId and MUST NOT use
  the value of algorithm"; the SWICG report: "If it's hs2019, assume that means rsa-sha256"). Any
  other value is refused, unknown or naming a kind of key the key is not (2.1.3: when it "differs from
  the key metadata ... an implementation MUST produce an error"). A missing `headers` parameter covers
  no request line, so it is refused: the draft says it then means `(created)` (2.1.6), its own
  Appendix C.1 signs `date` alone, and Mastodon's page says it assumes `date`. `(created)` and
  `(expires)` are accepted only with `hs2019` or no `algorithm` (2.3, rules 2 and 3: an error when the
  algorithm "starts with rsa, hmac, or ecdsa"; this note said `hs2019` only). A `created` in the future
  is refused with no allowance at all (2.1.4: it "MUST NOT be processed"), unlike `Date`, and an
  `expires` in the past too (2.1.5). Parameter names are read in any letter case (RFC 7235 2.1), a
  known parameter given twice refuses the header and an unknown one is skipped (cavage-12 2.2); the
  names in `headers` are lower-cased (2.3, rule 4), and one named twice is refused (the draft is
  silent; RFC 9421 2.5 step 2.1 makes it an error). RFC 9421: one signature, as Mastodon accepts it
  ("Only a single signature is supported"; a second label is refused, not chosen among, so Flowa never
  accepts what Mastodon refuses); `created` required; covered, `@method` and `@target-uri`, or
  `@method`, `@authority`, `@path` and (when the request has a query) `@query`, and on a request with a
  body `content-digest`; `@request-target` does not count as the request line (2.2.5: "NOT
  RECOMMENDED" where a version other than HTTP/1.1 might be in use); an `alg`, when present, must be
  `rsa-v1_5-sha256` (3.2, step 6.5: an algorithm stated in two places must agree), and without one the
  key decides (step 6.3); an `expires` in the past is refused (3.2.1). The `@signature-params` line is
  the `Signature-Input` member serialized again with its components and parameters in the order and of
  the types received (3.2, step 7; 4.1: "list order and parameter order have to be preserved"), never
  rebuilt in an order of Flowa's. Refused as well (2.5: "If the derived component name is unknown ...
  produce an error", "If the component identifier has a parameter that is not understood, produce an
  error"): a derived component not in 2.2, `@status` (2.2.9: not in a request), a covered
  `@signature-params` (2.3), a header field name in capitals (2.1: "MUST use lowercased field names")
  and any parameter on a header field (`sf`, `key`, `bs`, `req`, `tr`: no fediverse server sends one).
  `Signature-Input`, `Signature` and `Content-Digest` are parsed as strict Structured Fields (RFC 8941
  1.1), with at most 16 dictionary members, 16 parameters and 32 covered components (3.1.1 and 3.1.2
  let a field definition constrain these), and a key or parameter given twice is refused where RFC
  8941 4.2.2 keeps the last (RFC 9421 4.1: labels "MUST be unique").
- **Both forms on one request.** They share the field name `Signature`: a line that reads as a cavage
  header is one, and when `Signature-Input` is present the other lines are RFC 9421's dictionary (the
  SWICG report: `Signature-Input` "was only added in the later versions"). Both must verify, with the
  same key id: two keys vouching for one request give it no single signer, so that is refused. RFC 9421
  is then the form recorded for the host. `Authorization: Signature` is not read: Mastodon sends and
  reads the `Signature` header.
- **Header values** as both texts canonicalize them (cavage-12 2.3, rule 4; RFC 9421 2.1): each line
  trimmed, the lines joined by ", " in the order received. Go's HTTP/1.1 reader already replaces
  obsolete line folding with a space, which RFC 9112 5.2 allows and both texts' examples show; a CR, LF
  or NUL still in a value is refused. A signed `Date` must be one line, since two joined are not a date
  (a singleton field: RFC 9110 5.5 and 6.6.1). When both `Date` and `created` are signed, both must be inside the window (RFC 9421
  B.2.3 leaves it "up to the verifier").
- **Digests.** `Digest` (RFC 3230 4.3.2; names in any letter case, 4.1.1) and `Content-Digest` (RFC
  9530 2) may each carry several algorithms. Flowa computes SHA-256 and SHA-512, needs at least one of
  them, and checks every one that is there, in constant time: checking only one leaves the downgrade RFC
  9530 6.6 and 6.7 describe. Others (md5, sha, unixsum and the Deprecated ones of RFC 9530 5) are
  ignored, and a header with only those is refused. The digest is of the body exactly as received.
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
  9421); the date (or `created`) is more than 12 hours old or more than 1 hour ahead; the signature
  was accepted before (7.2); the key cannot be fetched; the signature does not verify after one
  refetch of the key (the SWICG report's verification steps refetch once, for a rotated key). While
  this server's clock is behind its build (8.3) the verifier says so instead (`sigClock`), and the
  inbox answers 503. The 12 hours are Mastodon's ("Verifying HTTP
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
path and query exactly as received (`r.RequestURI`, never Go's decoded path) for
`(request-target)`, `@path` and `@query` (finding 4); a request target in a proxy's absolute form is
refused. The signer sets the `Host` it sends to the authority it signs (the host in lower case, no
`:443`: RFC 9421 2.2.3) and signs the request line Go writes (`req.URL.RequestURI()`: `/` for an empty
path, the path's own percent-encoding and capitals, the query as given; RFC 9421 2.2.6 and 2.2.7, and
cavage-12 2.3, whose lower-casing "only applies to the :method"). It runs again for the URL a redirect
leads to, as the fetcher's `signer` hook does.

**Built (FRD's third item), with no caller yet** (`main.go`, "federation: HTTP signatures"): the pure
parsers and builders (`parseCavage`, `cavageString`, `sfDictionary`, `parseSignatureInput`,
`rfc9421Base`, `parseDigest`, `parseContentDigest`, `parsePublicKeyPEM`), the policy
(`sigVerifier.verify`, with a typed `sigReason` for every refusal, each a fixed phrase safe to log,
and the key function's error wrapped for the caller), `verifyRequest` for a request that arrived and
`httpSigner` for the fetcher. Neither reads the database: the verifier is handed a function from key
id to public key, the signer the private key and its id. The verifier takes the body bytes the caller
already read within its cap and reads no body itself. Caps: 8 KB for each of `Signature`,
`Signature-Input`, `Digest` and `Content-Digest` (8 lines each), 32 covered components, 16 parameters,
16 KB for a covered value, 64 KB for a signing string or base, 16 KB for a PEM. On the CI-class runner
(Xeon 2.3 GHz) `BenchmarkInboxVerify` (2 KB body, RSA 2048) is about 35 µs, 4.4 to 5.6 KB and 51 to 56
allocations, and `BenchmarkSignDelivery` about 0.8 ms; the fuzz targets `FuzzParseSignature`,
`FuzzParseSignatureInput` and `FuzzVerifyRequest` ran 30 s each with no failure.

`TestSignatureVectors` (**new**) copies these published vectors into
`testdata/federation/vectors/` (**new**), each with the section it came from, the date it was read
and how it was unwrapped (RFC 8792 7.2.2 for the RFC's "\" line wrapping):

- **cavage-12 Appendix C.1 (Default) and C.2 (Basic):** `rsa-sha256`, that is RSASSA-PKCS1-v1_5
  with SHA-256, on the draft's test key. Signing reproduces the published signature byte for byte
  (PKCS#1 v1.5 is deterministic) and verifying accepts it. Two warnings go with them: the appendix
  calls its own vectors "old and possibly wrong", and the key it calls 2048-bit is 1024-bit, so the
  test parses it with a lowered minimum (a parameter of `parsePublicKeyPEM`, not a setting). C.1 has
  no `headers` and signs `date` alone, which section 2.1.6 contradicts (see Accepted).
- **cavage-12 C.3 (All Headers) is kept as a documented negative.** Its signature verifies only
  against the string it prints, which leaves out the `(created)` and `(expires)` lines its own
  `headers` list names, and section 2.3 makes those two an error with `rsa-sha256` anyway; the test
  shows all three.
- **RFC 9421 Section 4.3, the `proxy_sig` signature:** the RFC's only `rsa-v1_5-sha256` example,
  with `test-key-rsa` from Appendix B.1.1 (2048-bit, a PKCS#1 `RSA PUBLIC KEY` PEM, so it also tests
  that form of key). The base Flowa builds is the printed one, the signature verifies, and signing
  reproduces it. Its parameters come in the order `created`, `keyid`, `alg`, `expires`, not Flowa's.
- **RFC 9421 Section 2.5 and Appendix B.2.1 to B.2.3:** for building the signature base only, which
  matches the printed base byte for byte. Their signatures are RSASSA-PSS with SHA-512
  (`test-key-rsa-pss`, an SPKI PEM), which Flowa does not implement; the test checks them with Go's
  RSA-PSS over the printed base only to show the copy is exact. B.2.2 covers a named query parameter
  (2.2.8), so `@query-param` is built too.
- **Digests:** SHA-256 of `{"hello": "world"}` is `X48E9qOokqqrvdts8nOJRJN3OWDUoyWxBf7kbu9DBPE=`
  (cavage-12 3.1 and Appendix C) and SHA-512 is the `sha-512=:WZDPaVn...:` value of RFC 9421 B.2.
  RFC 9530's own examples are of the object followed by a line feed (its Appendix A, 19 bytes):
  `sha-256=:RK/0qy...:` (B.1) and `sha-512=:YMAam5...:` (section 2). All four match a recomputation.
  Two slips in RFC 9530 were found and are not used: B.5 and B.6 print the sha-256 with one `=` too
  many, and the `sha-256=:d435Qo...:` that section 2 pairs with `YMAam5...` is the digest of a Brotli
  encoding (B.4), not of the same bytes.

### 7.2 Replay

- An activity id is processed once (ActivityPub 5.2: a server MUST de-duplicate by `id`):
  `ap_inbox.activity_id` is unique (as built, FWR 1: per signer's actor, 7.10); processed rows lose their body
  at once and are kept 7 days. **The id must have the actor's origin before it is written**, or a
  hostile server could send activities carrying ids another server will use and have the real ones
  dropped as repeats. An activity with no id (ActivityPub 3.1 and 7 allow it for one "intentionally
  transient") is processed and not deduplicated by id; the signature rule below still stops a
  replay of the same request.
- A signature is accepted once on a request that changes something, that is any method but GET and
  HEAD: a replayed signed GET within the window reads only what the signer could read with its own
  signature anyway, and remembering every signed fetch would crowd out the deliveries. The key is the
  first 128 bits of the SHA-256 of the decoded signature bytes, not of the header, so the same signature
  written in another base64 (padding left out, other pad bits) is the same entry; only signatures that
  verified are remembered, so a stranger cannot choose what goes in, and a collision would take about
  2^64 valid signatures. An entry lives as long as the time window could accept its signature: until
  12 hours after the time it vouches for, through the last second, which is the second the window starts
  to refuse it (`TestReplayWindow` shows the two meet). A signature dated an hour ahead is kept 13 hours,
  one dated 12 hours back hardly at all. The memory keeps its own clock, the latest it was given, so a
  system clock stepped back by up to an hour cannot bring a forgotten signature back into the window.
- **What the memory promises, and what replay safety also rests on.** The memory alone promises that a
  signature is never accepted twice under the same sender group (below) while the window would take it.
  For RFC 9421 that is enough: `keyid` is in the signature base, so the same signature under another key
  id does not verify. For the draft form it is not: a cavage signature does not cover its `keyId`, so once
  it has been evicted from its group (below), the same bytes sent under a key id of another domain that
  serves a copy of the sender's public key (public keys are public) pass the memory and verify. That
  delivery is refused by the origin rule of 7.3, applied by the inbox to every delivery: the body, bound by
  the digest, still names the first sender's actor, and the key id is not on that actor's origin. **The
  origin rule is therefore part of replay safety for the draft form, not only of authorship**
  (`TestReplayAcrossDomains` writes both halves down). No grouping closes it inside the memory: keyed by
  the key's fingerprint, whoever claimed a fingerprint first would own the victim's floor, and one floor
  for everyone is the blocker the first rule was.
- **When the memory is full, the sender that filled it pays, never a bystander.** It is bounded and the
  number of valid signatures in 12 hours is not, so some valid new signature must sometimes be refused;
  a replay never is. Entries are grouped by the **sender group** of their key id (`senderGroup`): the
  last two labels of its host in lower case, scheme and port ignored, so `a.b.evil.example:8443` and
  `evil.example` are both `evil.example`; an IP literal is a group of its own, and a key id on a host
  of one label is refused before the memory sees it. An entry is dropped only by expiry or by eviction, and an evicted entry is the oldest of its group, whose own
  **floor** rises to its time: from then on a new signature from that group dated at or before its
  floor is refused as stale, since it might be the one dropped. A signature with exactly the floor's
  time is refused too (by time alone it cannot be told from the dropped one); this only happens to an
  group that was itself evicted from. One group's floor never touches another's, and a floor is
  forgotten when the window would refuse its time anyway. Who is evicted from:
  - a group holds at most 10,000 entries, a tenth of the memory (a group at the inbox's allowance,
    300 requests in 5 minutes, fills that in under 3 hours, so a sender that dates its deliveries when
    it sends them loses none); at that cap it evicts its own oldest, whatever dates it claims, so a flood
    from one group costs that group only;
  - when all 100,000 are taken, the oldest entry of the group holding the most is evicted, or the
    inserting group's own when it already holds an equal share (the memory divided by the groups in
    it). The heaviest pays because it is the one over its share: a group at or under an equal share
    never loses an entry to another's traffic;
  - at most 2,000 groups are kept: a signature from a new group then is refused with its own reason
    (`sigFull`), which the inbox answers with 503 and `Retry-After`, since a group is never forgotten
    while it may still have a floor or an entry.

  **The worst case** is a flood of valid signatures from very many groups, each needing a domain the
  domain policy lets in, a key that verifies, and its host held to the inbox's allowance. Subdomains and
  ports share their domain's group, so one wildcard DNS name fills one group's share, not the 2,000
  groups. An honest group already in the memory keeps at least an equal share of its newest entries,
  50 at the full 2,000 groups, and loses only a delivery dated no later than the ones evicted, one
  overtaken by that many of its own later deliveries. An honest group not yet in the memory gets 503 for
  as long as the flood is kept up, or until the flooding domains are blocked: holding all 2,000 groups
  takes 2,000 registered names that the domain policy lets in, each serving a fetchable key and sending a
  signed request at least every 13 hours (an entry dated an hour ahead lasts 13). Either way the sender
  retries, with a fresh date, and is accepted. A shared overflow group for the groups beyond the cap was
  considered and not built: a sender could be in the overflow group at one time and in its own group
  later, and a floor protects only if a signature always maps to the same group. `TestReplayModel` checks the rule against a reference that remembers everything, over
  thousands of random operations.

  **What grouping by the last two labels costs.** It is a rough stand-in for the registrable domain,
  and safe: a floor kept per group refuses at least everything a floor per origin would, so no replay
  gets through. The cost is fairness under pressure only: servers that share their last two labels
  (every server under `co.uk`, or every customer of a host that gives out subdomains) share one
  group's share of the memory and one floor, so a flood from one of them can cost its group-mates a
  delivery dated at or before the group's floor, which the sender retries with a fresh date
  (`TestReplaySharedFloor` writes the trade down). Grouping by origin instead let one wildcard name
  have every server not yet seen refused for as long as it kept the flood up (`TestReplaySubdomainFlood`). The list of
  public suffixes would draw the line better; it is not taken as a dependency.

  Each check is O(log n): a heap of entries by time for each group, and heaps of groups by count and
  by oldest time, so expired entries are found without a scan. Every call forgets up to 8 expired entries
  of any group, then the named group's own, at most 64 in all, so a request not yet verified costs at
  most 64 heap removals; `sweep` is there for the housekeeping pass. Entries share one pool, so
  memory at the cap is fixed: 52 to 56 bytes an entry measured with 50 and 2,000 groups, 5.2 to 5.6 MB
  for 100,000, about 7 MB at most with the slack a shrinking group's heap may keep
  (`TestReplayExpiry`, `BenchmarkReplayAdd`). This replaces a first rule, one floor for the whole
  memory raised to the latest time among the oldest half dropped by arrival, under which one key
  sending deliveries dated 59 minutes ahead could make the server refuse everyone else's for an hour,
  and one sender with a fast clock could do the same by accident.
- The memory is lost on a restart: a replay within the 12 hours after one then rests on the
  de-duplication by activity id above, and an activity with no id is not protected across a restart.
  **A clock stepped back by more than an hour is treated the same way.** A Raspberry Pi has no clock of
  its own and takes its time from the network after it starts; a clock that jumped ahead and was then
  corrected would otherwise leave the memory's clock ahead, and every honest signature older than the
  size of the step would be refused as stale, all of them once the step passes 12 hours, until real
  time caught up. So when the wall clock reads more than an hour behind the memory's clock, the memory
  forgets everything, as a restart does, takes the wall clock, and says so once for the caller to log
  one line (`TestReplayClockStep`). An hour is the tolerance ahead the window already gives: a step back
  within it leaves the memory's clock at most an hour ahead, which refuses as stale only signatures
  dated more than 11 hours back, and keeps a forgotten signature from coming back.

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
  report, same section), so both are parsed (`parsePublicKeyPEM`): one block with nothing before or
  after it and no PEM headers, an RSA key of 2048 bits (the SWICG report: "a 2048-bit or larger RSA
  keypair") to 8192 bits (a cost limit on verifying). An Ed25519 key is refused with its own reason:
  Mastodon validates Ed25519 only from 4.7.0, not released when this was read; accepting it would be one
  more case there and a second algorithm chosen by the key. `sameOrigin` compares a key id and an actor
  id this way; fetching the key and following `owner` are 7.4's item.
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

As built (FRD 7), for fetched actors (`actorCache.lead`, `keyDocument`, `actorKey`): the document's `id` must
be the address it came from after redirects (`sameID`; the document kind follows same-origin redirects only,
so it is on the origin asked), or nothing is stored and the failure is remembered (`actorWrongID`). A key is
kept only when the actor names it, its `owner` (or, without one, `controller`, as the SWICG report follows)
is the actor, its id is on the actor's origin (`sameOrigin`, whole origins) and its PEM is an RSA key of 2,048
to 8,192 bits; otherwise the actor is stored with no key, and a lookup by that key id fails (`actorNoKey`).
A key at a path of its own is the SWICG report's case ("How to obtain a signature's public key", step 5: "If
it's a raw Key object, use its controller or owner property as the new key id ... and repeat"): the document
at the key id (its fragment removed) that is not an actor is read as a key, whose id must be the key id
asked for and where it came from, and whose owner must be on its origin; the owner is then fetched (once
more, whatever its row's age, since only its document can say whether it names the key) and must name the
key id back, embedded or by id only (the report: "It may be a compacted id"), when the key document's PEM is
taken. An owner is never followed further than one key document.

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
- **The verifier calls the key lookup for whatever https key id a request names, before any
  cryptography** (`sigVerifier.verify`; the replay memory and the time window are the only checks
  before it). So the inbox must give that lookup the fetch cache above, the negative cache and a limit
  of key fetches per host before it wires a lookup that fetches; otherwise one unsigned request per key
  id a stranger invents costs a fetch. `parsePublicKeyPEM` refuses keys no RSA key has (an exponent
  under 3 or even, an even modulus), so none of those is cached as good.

**As built (FRD 7)** ("federation: remote actors" in `main.go`; `TestDBRemoteActorCache`, `TestDBRemoteOneAnswer`,
`TestDBRemoteKeyOfItsOwn`, `TestDBRemoteKeyLoops`, `TestDBRemoteActorTurns`, `TestDBRemoteActorWaiters`,
`TestDBRemoteActorFlight`, `TestDBRemoteActorBound`, `TestDBEvictionCoversReferences`,
`TestDBRemoteActorNotReady`, `TestDBRemoteActorStop`, `TestDBRemoteActorStaleBound`):

- **One way in for now:** `federation.remoteActor(ctx, id)`, which FRD 8's search by handle calls (as built, 11, and
  nothing else does) with the id a WebFinger answer gave (or a profile address: an address that redirects to the actor's id answers with
  that actor). `remoteKey(ctx, keyID, use)` and `remoteKeyFailed(ctx, keyID)` are the two lookups FWR's inbox
  gives its verifier; nothing calls any of them in this version, no route reaches them, and the cache exists
  only in a mode that federates (`federation.actors`), with no goroutine until a fetch starts.
- **One answer per actor, however it is asked for.** Every way in goes through one function (`lookup`). The
  address fetched is the string asked for without its fragment (the SWICG report: "fetch it, discarding its
  fragment first if necessary"). A string that led to an actor whose id it is not (an id with a fragment, an
  address that redirects to the actor's own) is remembered as that actor's **name** for as long as the row is
  fresh, so asking it again is asking the actor; an actor gets at most 8 names (an attacker's server can
  answer every path with a redirect to one actor), and a ninth address is refused and that refusal
  remembered. A key id is a key only in the document of the actor that owns it (7.3, and the SWICG report:
  "Check that this key object's id matches the original keyId"): a key id under a redirecting address, or one
  the actor does not name, is refused, and the refusal is remembered under the key id. So N lookups of any one
  string cost at most one fetch in 10 minutes for a failure or a key, and one in 24 hours for a fresh actor:
  5 lookups of a redirecting id cost 2 requests (the redirect and the document, once; the first version paid
  10), 5 of an id with a fragment of a cached actor none (5), 5 of a key id under a redirecting path 2 (10), 5
  of a key the actor does not name none within 10 minutes of its fetch and one after.
- **Signed by the instance actor**, with draft-cavage as every GET (7.1), through `fetcher.do` with the
  document kind; the origin is frozen first (4.3); the instance actor's key is read before a turn is taken.
  Before the instance actor has its key nothing is fetched (`actorNotReady`), nor while the clock is earlier
  than the build (`actorClock`, as 8.3), federation is refused, or the server is stopping. The refused hosts
  come from a hook (`actorCache.refused`), empty until FMD's domain policies fill it; the instance's own
  origin is refused by name before anything (`actorLocal`), as the fetcher refuses it again, and
  `fetcher.deny` holds at connect time.
- **Keys:** a key the actor names by id only is followed to its own document at **every** fetch of the actor,
  one request more, never further (`followKey`), so an actor whose key sits at a path of its own keeps it
  through refetches and a rotation there is taken (the first version lost the key at the first refetch). A
  key document whose owner is itself is refused at once; a key document's owner is fetched at depth 1, a
  fetch at depth 1 never follows a key document and never waits for another fetch, so no two fetches can
  wait for each other (the first version waited on itself for 15 s). An actor keeps one key (9.1): an actor
  that names two RSA keys is served for the one asked last, and asking for the other within 10 minutes of a
  fetch fails and is remembered; Mastodon names one.
- **Expiry and the 10 minute rule** are decided on the cache's own clock (`actorCache.now`, which the tests
  move): a row younger than 24 hours is the answer; `remoteKeyFailed`, and a lookup that needs a key the row
  does not keep, fetch again only when the row was fetched 10 minutes ago or more. A changed key in a fetched
  document replaces the kept one: the document comes from the actor's origin by its id, which is the
  vouching a rotation has (5.7).
- **What a caller gets, and what a key may be used for.** `remoteActor` answers with the row when there is
  one, saying how fresh it is: `Stale` with `StaleReason` when its refetch failed (any reason, a busy turn
  included), and `State` `gone` or `suspended`. A stale row is answered for at most 30 days after its last
  fetch (9.3's retention), then the failure is the answer. **A key verifies a signed request only when its
  row is fresh and the actor neither gone nor suspended**: `remoteKey(ctx, keyID, keyToVerify)` and
  `remoteKeyFailed` refuse the others with their reason (`actorStale`, `actorGone`, `actorSuspended`), and
  `remoteActor.publicKey(keyToVerify)` does the same. The one exception 6.3 allows is the actor's own Delete
  verified against the cached key, which asks with `keyForOwnDelete`. So a leaked key that its owner rotated
  cannot keep verifying past its 24 hours by keeping this server's turns busy (`TestDBRemoteActorTurns`).
- **Failures remembered, by kind:** the remote server's answers (refused, unreachable or another status,
  timed out, 404, too large, not an Activity Streams type, a redirect to another origin, a document that does
  not decode as an actor, an id that is not where it came from, a key document that vouches for no actor, an
  address beyond an actor's 8 names) for 10 minutes; 410 for 24 hours for an id never stored, and for good for
  a stored actor, whose row becomes `gone` and is kept. This server's own state (no key yet, a busy turn, a
  caller that gave up, an unset clock, a refused or stopping federation) is never remembered. Failures and
  names share one memory of at most 10,000 strings, by 16 bytes of the SHA-256 of each; when full the expired
  go, then about a tenth of the rest, so a flood of distinct strings costs refetches, not memory.
- **One fetch per address at a time; callers wait.** The first caller starts the fetch on its own context (the
  federation's, so a stop ends it, and the stop waits for it: 8.6) with 15 s for the fetch and its writes, and
  every caller of the same address waits for it as long as its own context allows; one that gives up does not
  fail the others. A caller whose question the shared fetch did not answer (a key it did not keep) asks once
  more with a fetch of its own, so two keys of one actor asked at once both verify. At most 2 fetches run at
  once and **at most one per host**: a lookup takes its host's turn before one of the two, within 5 s in all
  (then `actorBusy`, which FWR's inbox answers with 503), so a slow host holds one of them and every other host
  keeps the other. No database connection and no lock is held while the network is waited on.
- **The bound:** 20,000 cached actors (section 12). The count is read from the table at the first new actor
  and after each removal; one over the bound removes the least recently fetched and a hundredth of the bound
  more, never the actor just stored, never one a moderator muted or suspended, and **never one that any
  table points at**: the removal asks every column that references `users` (`userRefs`), each through an
  index that starts with it, and `TestDBEvictionCoversReferences` reads the foreign keys to `users` from the
  catalog and fails when one is missing from `userRefs` or has no such index. Migration 15 adds the nine such
  indexes that were missing (comments, notifications by actor, reports by reporter and by resolver,
  `mod_actions` by actor and by target, catalog revisions by author, instance settings by who saved them,
  sessions). So a later item that adds a table referencing `users` cannot forget it. The bound is soft only if
  more than 20,000 remote accounts are each kept by something.
- **An actor that moved host** is a new id, so a new actor; the old row keeps `moved_to`, which PRT reads.
- **As built (FWR 1):** the cache of parsed keys and the limit of key fetches per sender group, as the next
  paragraph asks (7.10).
- **Not built here:** the in-memory cache of parsed public keys (1,000, least recently used out) is the
  verifier's, and comes with FWR's inbox, which is the first to verify; so does the limit of key fetches per
  host the paragraph above asks for, which the inbox needs because the verifier fetches for any key id. The
  inbox's requirements on this cache are written into its item (`docs/epic-fwr/todo.md`).

### 7.5 Limits on what comes in

| Limit | Value |
| --- | --- |
| Inbox body | 256 KB (`http.MaxBytesReader`), 413 above |
| JSON depth | 24 levels of objects and lists, checked by the decoder's scan before anything is decoded (6.3), 400 above |
| Array length | 1,000 entries in every list and 1,000 members in every object, whatever property holds it (a remote server chooses them all); longer is 400 |
| Recipients | 1,000 in `to`, `cc`, `bto`, `bcc` and `audience` together, counted before any is dropped (6.3); more is 400 |
| Member names | each once in an object, in any spelling (an escaped letter, a compact or a full IRI name for the same property); a second is 400 |
| A string field | 2,048 bytes for an id or URL (`maxURL`); 64 KB for `preferredUsername`; `content`, `summary`, `name` and each entry of their language maps are cut to 64 KB on a character boundary, before conversion; a media type or a language tag over 255 bytes is dropped (6.3) |
| Memory of one decode | at most 4 MB allocated for a 256 KB inbox body, all of it garbage once the decode returns. Measured on the costliest inputs found: a thousand Hashtags each carrying thirty members the decoder reads elsewhere, 3.4 MB (13 times the body); a thousand Mentions, 1.7 MB; every other input under 1 MB, nesting, distinct and escaped names and empty objects by the thousand among them. It grows linearly with the body, so a 1 MB fetched document may take four times as much. `TestDecodeMemory` holds the 4 MB; FWR's inbox decodes at most as many bodies at once as it handles (4, above), so 16 MB at worst |
| Fetched documents (actors, objects) | 1 MB, `application/activity+json` or `application/ld+json` only, the latter with no `profile` or with the Activity Streams one among its profiles (WebFinger: `application/jrd+json`, the type RFC 7033 4.2 names, or plain `application/json`; NodeInfo: `application/json`, any parameters; both 64 KB, as built in FRD 2). Plain `application/json` is refused for an actor or object: ActivityPub 3.2 names only the two types, and no recording shows a server Flowa needs serving it; a fixture that does reopens this. The type is checked before the body is read, and the cap counts the inflated bytes of a compressed answer |
| Fetched pictures | 8 MB in, 4096 px a side checked with `image.DecodeConfig` before decoding, as `shrinkThumb` does; **one picture decoded at a time** (a mutex), because a 4096 by 4096 picture is 64 MB once decoded and the container has 384 MB |
| Inbox requests per remote host | 300 in 5 minutes in a `limiter` keyed by host, 429 above |
| Replay memory (7.2) | 100,000 signatures, at most 10,000 of one sender group (the last two labels of the key id's host), at most 2,000 groups; a signature from a new group beyond that is answered 503 with `Retry-After` |
| Inbox handling at once | 4, 8 more may wait 2 s, then 503 with `Retry-After: 30` |
| Inbound queue | 5,000 unprocessed rows; above it the inbox answers 503 with `Retry-After: 120` |

### 7.6 Authorized fetch

An admin option (F11) in `instance_settings`. When on, every federation GET except WebFinger,
NodeInfo, the instance actor and the public key part of an actor needs a valid signature, and the
signer's domain must not be blocked; answers are `Cache-Control: private` with `Vary: Accept,
Signature` (5.6). `TestDBAuthorizedFetch` (**new**). The followers and following collections give no list
with it on either (6.2, "As built (FRD 6)"): a signature names another server's actor, never the account
itself, so F10's "only to the account itself" is still nobody at that address.

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
  `TestPublicIPReserved`. As built (FRD 2), the list was checked against what a careful guard refuses:
  already refused were loopback, RFC 1918, link-local (with `169.254.169.254`), `fc00::/7`, `fe80::/10`,
  `::1`, `::`, multicast, `0.0.0.0/8`, `100.64.0.0/10`, `192.0.0.0/24`, `198.18.0.0/15`, `240.0.0.0/4`,
  NAT64 (`64:ff9b::/96`, `64:ff9b:1::/48`) and IPv4-mapped addresses (`::ffff:a.b.c.d`, checked as the IPv4
  they carry); added with 6to4 and Teredo were IPv4-compatible (`::/96`) and IPv4-translated
  (`::ffff:0:0:0/96`) addresses, the documentation ranges (`192.0.2.0/24`, `198.51.100.0/24`,
  `203.0.113.0/24`, `2001:db8::/32`, `3fff::/20`), IPv6 benchmarking (`2001:2::/48`), the never-routed ORCHID (`2001:10::/28`, `2001:20::/28`) and SRv6
  segment id (`5f00::/16`) blocks, the old 6to4 relay
  (`192.88.99.0/24`), discard-only (`100::/64`) and site-local (`fec0::/10`). The guard runs in the dialer's
  `Control` function on the address being connected, for each address of a DNS answer and each redirect,
  so a name cannot answer one address to a check and another to the dial: there is no earlier check.
  The test reaches its local stub through a field the test sets (`dialOverride`), which lets only that one
  address through; no setting does.
- Hosts before any connection, for federation: no IP address written as the host (with the inet_aton forms
  such as `2130706433` and `0x7f.1`), no name without a dot, no `localhost`, no private suffix, nothing but
  ASCII letters, digits, dots and dashes (Go would turn another script into its `xn--` form after the refused
  list was compared), no user name, no URL over 2,048 bytes, and never the instance's own origin unless the
  caller says so. Until FMD's domain policies exist, the caller passes the refused hosts (each with its
  subdomains, any case, a trailing dot ignored). **A refused host must be written in ASCII**: a name in
  another script is stored in its `xn--` form, because the standard library cannot convert
  `bücher.example` to the `xn--bcher-kva.example` a URL carries; a list with any other entry makes every
  request refused (`refusedHosts`), so FMD's admin page must convert or refuse such a name when it is
  saved. The link card stub (`FLOWA_PREVIEW_BASE`) refuses every federation request, since it lets
  private addresses through.
- **The own-origin rule is by name only.** A stranger's name whose DNS points at the instance's own
  public address, or at the home router's WAN address, passes every check: the address is public, so
  the dial guard lets it through, and the request comes back in to this server or to the router. What an
  operator can do now: a firewall rule on the router that drops connections from the Pi to its own WAN
  address (most routers' hairpin NAT is what makes this reach in). The fetcher already has the hook for
  the rest (`fetcher.deny`, address prefixes refused beside `reservedNets`, empty by default); the
  federation settings item gives it a setting where the operator lists the instance's own addresses.
- One request at a time per host is counted at the host each request is actually sent to, the name
  lower case and without a trailing dot: a redirect hands the request's turn over to the new host (one
  turn held at a time, so requests redirected across each other cannot wait on each other). Waiting for a
  turn lasts at most the kind's time, then fails as busy (`failBusy`, this server's queue, retried like
  any temporary failure). There is no limit across different hosts: callers bound their own concurrency
  (the delivery queue and the picture cache have their budgets in section 12).
- Hosts: the preview allowlist for link cards as today; for federation any host the domain policy
  allows (section 10). Only `https`.
- Redirects: at most 3, each re-checked against the scheme, the policy and the dial guard; a redirect
  to another origin is followed only for media and for WebFinger, never for an actor or an object
  (whose id must match where it was fetched from). WebFinger is the exception because RFC 7033 4.2
  lets a WebFinger resource redirect (to `https` only), and Mastodon's `WEB_DOMAIN` setting depends on
  it: the handle's domain answers WebFinger with a 301 to the server's own domain (Mastodon
  configuration page). A WebFinger lookup is never retried over `http` (RFC 7033 4.2: MUST NOT).
  A NodeInfo document follows the same-origin rule of documents. **A POST is never redirected** (as
  built): a delivery answered 3xx fails and the queue tries again later, because following would send
  the signed body to an address its signature did not name. Each request after a redirect is built
  afresh, so no `Authorization`, `Cookie` or signature header is carried over (Go 1.26's client drops only
  `Authorization`, `WWW-Authenticate`, `Cookie`, `Cookie2` and the two proxy authentication headers, only
  to a host that is not the first one or under it, and adds a `Referer`, so it is not used to follow
  redirects), and the signer runs again for the new URL. The final URL is returned for the
  id check of 7.3 (`sameID`).
- Headers: a GET for a document sends
  `Accept: application/ld+json; profile="https://www.w3.org/ns/activitystreams", application/activity+json`
  (ActivityPub 3.2 and 7: the client MUST ask with the first); a delivery sends the
  `Content-Type` of 7.1; a NodeInfo GET asks for `application/json` (NodeInfo protocol,
  "Retrieval").
- Time: 5 s for a document or a delivery (the delivery is retried later), 10 s for a picture, one
  deadline over the name lookup, the connection, TLS, every redirect and the whole body. Success is 200
  for a GET and 200, 201, 202 or 204 for a delivery; 404 and 410 are told apart, other statuses carry
  their code and `Retry-After` (bounded to 12 hours), and no more than 4 KB of an error's body is read.
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

### 7.10 The inbox, as built (FWR 1)

`POST /users/{name}/inbox`, `POST /inbox` and `POST /actor/inbox` (`handleInbox`, "federation: the inbox and its
queue" in `main.go`), only in a mode that federates (`testdata/routes-federated.txt`), with these decisions where
the note was silent or is corrected:

- **One inbox.** The three addresses are one handler, and no name is looked up: the activity's own fields say
  what it is about. So no answer can tell whether a local name exists, is opted in, suspended or retired (a
  delivery to `/users/nobody/inbox` is answered as one to `/inbox`). Mastodon's pages on disk say nothing of how
  it answers a personal inbox of an unknown name; answering alike is the careful choice 5.3 made for WebFinger.
- **The one exemption from `guard`.** `fedMethods` sends a POST to an inbox address to the mux past `guard`;
  `handleInbox` checks the type itself (`application/activity+json`, or `application/ld+json` with no profile or
  the Activity Streams one, as the fetcher takes them: 7.5), so plain JSON and anything else is 415. Every other
  address keeps `guard` unchanged (`TestDBInboxMethods`).
- **Order and statuses, and who pays** (corrected after the review of #111: before, a sender group's allowances
  were counted before its signature was checked, so forged requests could spend a real server's). The rule:
  nothing a sender has not proven is charged to anyone but the address it came from. In order, each pinned by
  `TestInboxStageOrder` (each refusal reached with every earlier stage passed and no later stage's work done):
  1. Before the body is read, at the cost of a few counters: federation refused, 404 (as every federation address
     then); 3,600 deliveries an hour per address (the server's client-address rule, so a trusted proxy's
     forwarded address), 429; the type, 415; no signature or one that does not parse, 401; the key id's domain
     suspended, or not allowed in allowlist mode, 403 (7.7, section 10); the `federation` flag off, 503 with
     `Retry-After` (F1's pause switch, off by default, a flag only in a mode that federates); a `Content-Length`
     over 256 KB, 413; no read turn, 503 at once: 8 bodies being read at once, at most 2 for one address, so no
     address can hold them all. A read turn ends when the body has arrived (after the second review of #111: it
     was held through the whole delivery, so one address had 2 deliveries in flight in all;
     `TestInboxReadTurnFreed`).
  2. The body within 256 KB (413) and 10 s (408): 256 KB at about 200 kbit/s, a slow mobile link. The
     connection's read deadline is set, and the read runs beside a timer as well; on any failure the read turn is
     given back before the answer (`TestInboxSlowBodies`: two from one address, the rest of it 503 at once and a
     real delivery taken meanwhile; eight addresses holding every turn until their time runs out).
  3. The signature (401; 503 for `sigClock`, `sigFull`, and a key lookup that was busy, past an allowance, or found
     both key turns taken). A key not kept is looked up under one of 2 key turns; the request's address pays for
     each fetch (60 an hour per address), and the key id's sender group is charged only for a fetch whose key
     then verified (120 an hour per group; while it is used up nothing more is fetched for that group). Forged
     requests therefore never spend a group's allowance, and what a flood of distinct made-up key ids from many
     addresses costs a peer is bounded by the cache's pace: one request at a time per host, 2 at once in all, 5 s
     each, every key id that leads to no key remembered 10 minutes (`spend`'s comment, `TestInboxForgedSpendsGroup`,
     `TestInboxKeyFetchBound`).
  4. Verified, so charged to the sender: 300 deliveries in 5 minutes per host of the key id (7.5's "per remote
     host") and 3,000 per sender group, the last two labels of the host as the replay memory groups them, so a
     wildcard name gets ten hosts' worth, and other servers under `co.uk` or `github.io` are touched only by ten
     times one server's verified traffic; 429.
  5. The decode under one of 4 decode turns (7.5's "handling at once": 8 more wait 2 s, then 503), so at most 4
     bodies are decoded at once, each verified first (400 when it does not decode); the origin rule.
  6. The queue: 503 at 5,000 rows waiting, 429 at 500 of one sender group (8.5's per-host share, decided here as a
     tenth of the queue).
  Every refusal has a fixed phrase, logged through `logRefusal` with the host or the address and nothing the sender
  wrote, and counted.
- **Keys (7.4).** `inboxKeyFor` gives the verifier the key: from `keyMemo` (at most 1,000 parsed keys, least
  recently used out, each kept no longer than its row is fresh and dropped whenever the cache writes the actor,
  so it is always a key `remoteKey` would give to verify with), else `remoteKey`, with the fetches paid for as
  above, on top of the cache's 2 at once and one per host, and the 10 minute memory of failures. A signature that
  does not verify is tried once more after `remoteKeyFailed`. A stale, gone or suspended actor's key verifies
  nothing; an actor's own Delete asks with `keyForOwnDelete`. A restart empties the replay memory and `keyMemo`; a
  replay within 12 hours of one rests on the activity ids stored.
- **Keys not kept have turns of their own.** A key not in `keyMemo` is looked up (and perhaps fetched) under one of
  2 key turns (`inboxKeyAtOnce`, the cache's own 2 fetches at once), taken at once or not at all (503): a flood of
  deliveries naming keys nobody has holds at most these 2, and no read or decode turn, so deliveries whose keys
  are kept go on (`TestDBInboxFlood`).
- **Behind a proxy, "per address" is per what the proxy says.** The address the allowances count is `clientIP`'s:
  the forwarded address only when the connection comes from `FLOWA_TRUSTED_PROXIES` (default loopback and Docker's
  `172.16.0.0/12`). When the proxy passes none, or is not trusted, every sender shares that one address's
  allowances (3,600 deliveries and 60 key fetches an hour, 2 bodies being read), for the whole fediverse. Decided:
  the inbox logs this once per process when a delivery comes straight from a trusted proxy with no
  `X-Forwarded-For` or `X-Real-IP` (`forwardMissing`, `TestInboxUnforwardedSaidOnce`), because that state is a
  misconfiguration an operator cannot see otherwise and one line costs nothing; an untrusted proxy cannot be told
  from a sender, so it is not logged. `clientIP` and its defaults are unchanged.
- **An own Delete nobody can verify is answered 202** (after the review of #111). Before, a garbage signature on a
  body shaped as an actor's own Delete was answered 202 when Flowa had never seen the actor and 401 when it had it
  cached, so a stranger could learn whether a member here had looked a given person up. Now the peek takes only an
  actor on the key id's origin (any other cannot pass the origin rule anyway), and every such Delete whose
  signature fails, whatever the reason but this server's unset clock (the same for every sender), is answered 202
  with an empty body and nothing done, as one from an actor never seen is (`TestInboxPeekOracle`). **Except this
  server's own failures** that any sender can meet, cached actor or not: the replay memory full, every key turn
  taken, the database (each checked before the peek or by the one lookup both kinds run): those keep their 503 and
  the sender retries. A key fetch that cannot be made (no fetch turn in 5 s, the address's allowance of fetches
  used up, a canceled fetch) is answered 202 too, since only a cached actor's Delete reaches a fetch (an unknown
  actor's is dropped before), and a 503 there would bring the question back (`ownFailure`). **The cost:** a
  real own Delete from a cached actor whose signature fails for another reason (its clock, a proxy that changed the
  body) is answered 202, so its sender does not retry, and the Delete is lost. That is accepted: the actor is still
  marked gone at its next fetch, which answers 410 (7.4), and Mastodon's own pages on disk do not say what it
  answers a Delete it cannot verify; the SWICG report says only "discard the Delete and do not process it". **What
  stays distinguishable:** the time. An unknown actor's Delete costs one indexed lookup. A cached actor's costs a
  row read and an RSA check besides (about 1 ms on the runner), and, the first time in each 10 minutes that a key id
  it does not keep is named for it, a refetch of the actor: a request to its server, bounded by the fetch's 5 s and
  the address's 60 fetches an hour. So a sender who measures can tell a cached actor apart, sharply once per actor
  in 10 minutes, faintly otherwise; an own Delete signed by another actor is that actor's forward (`TestInboxOwnDeleteByAnother`).
- **The one reading before the signature.** `peekOwnDelete` tells an actor's own Delete before the signature, which
  7.4 needs (a Delete from an actor never seen costs no fetch) and 6.3 too (a gone actor's own Delete verifies with
  its cached key). Its bounds, written as its comment: only for a key not kept, so only after every check before the
  body; on at most the 256 KB already read; through the decoder's linear scan (no recursion, the limits of 7.5) and
  then the top-level members alone (`type`, `actor`, `object`'s id); under a key turn, never a decode turn; with no
  database work but one indexed lookup, and only for an own Delete; and its answer chooses only between dropping
  with 202, verifying with the cached key (`keyForOwnDelete`, after which the decoded activity must be that own
  Delete, or 401) and going on as usual. Nothing it reads is stored, logged or acted on. An unsigned request is
  refused before its body is read at all (`TestDBInboxFlood` counts the bytes read: none).
- **The origin rule (7.3).** The activity's actor must be the actor whose key signed it (the cache keeps only a
  key its actor names, owns and has on its own origin, so the key id is on the actor's origin too). A delivery
  signed by anyone else is a forward: 202, not stored, not acted on, as 7.3 says ("answered 202 and its object
  fetched from its origin by id"; ActivityPub 7.1.2 makes forwarding the forwarder's duty and leaves the receiver to
  check it; fetching the object is FWR 5's and 6's). Its sender group's allowances are the signer's: the domain
  policy, the delivery allowance and the key fetches are all decided by the key id that signed it, and the named
  actor's domain is never counted or fetched from (`TestDBInboxForwardPays`). This is what refuses a cavage signature
  replayed under another domain's key id (7.2). The activity's id must be on the actor's origin before it is
  written (7.2): 400. **Correction (7.2):** an activity with no id is refused (400), not "processed and not
  deduplicated": the decoder requires `id` (6.3), and accepting one would leave it to the replay memory alone,
  which a restart empties.
- **What is stored and handled** (6.3's table until the items that own each row): an actor's own Delete (its
  object the actor) and an Update whose object is the actor's own document, embedded and of an actor type, are
  stored; a worker marks the first's actor `gone` (FWR 7 purges what it left) and fetches the second's actor
  again within the 10 minute rule, which takes a new key or lifts a suspension (the embedded document is not
  stored as received: 7.3). As built (FWR 3): Follow, Accept, Reject, Undo and Block are stored too when they can be
  about a follow here (6.4, "Follows across servers, as built"). Everything else is answered 202, counted per type (the vocabulary's
  types and Mastodon's extensions by name, any other as `other`) and dropped with no row. (Before FWR 3 nothing was
  answered back; since, a Follow is answered with Accept or Reject as 6.4's "as built" says.)
- **`ap_inbox`** (migration 18, with `flowa-schema17.dump`): as 9.1 with these **corrections**: unique by the
  verified signer's actor and the activity id together, not by the id alone (7.2 holds an id only to the actor's
  origin, so another account of the same server could send its own activity under the first one's id, Mastodon's
  `<actor>#delete` among them, and have the real one refused as a repeat for the 7 days a row is kept;
  `TestInboxIDSquat`; a repeat by the same actor is still refused); `body` is `TEXT`,
  not `JSONB`, so the bytes the signature covered are kept as received (the worker decodes them again; a forward,
  7.1.2, sends them as received); `host` (the sender group) for the per-group share; `kind`, `next_at`,
  `locked_until`, `outcome` and `handled_at` for the claim, the retries and the retention; `actor_id` indexed and in
  `userRefs`; and indexes for the claim (`ap_inbox_due`), the share (`ap_inbox_host`) and the retention
  (`ap_inbox_handled`). Rows are claimed with `FOR UPDATE SKIP LOCKED` and a 60 s lease on the queue's clock (8.1);
  a failure of this server's own (a busy fetch, the database) is retried after 1 min, 5 min, 30 min, 2 h and 6 h,
  then the row is failed (the note gives no schedule for inbound rows); a handled or failed row loses its body
  at once and is removed by housekeeping after 7 days (9.3). Two workers (`inboxWorkers`) start with the first
  row stored, or at start when rows wait; they wait on a signal or 5 s, stop with the federation's context and
  wait group (8.6), and a row claimed when they stop is given back at once (its lease cleared, the attempt not
  counted); a row whose process died is claimed again when its lease ends.
- **Kept keys follow their rows, and the workers their limits** (after the review of #111). A key in `keyMemo` is
  forgotten whenever its actor's row changes in this process: the cache writing the actor (a refetch, a changed
  key, a suspension), a 410, the worker marking it gone, and the cache's bound removing actors (which forgets every
  kept key, since it does not list the ids it removed); a lookup that read the row before such a drop does not keep
  its key after it (the memo's generation, compared and the key put under one lock), and a key read from an older
  row than the one the cache last wrote for that actor is not kept either (the row's `fetched_at`, recorded by the
  write: the write itself does not move the generation, since the lookup that waited for it reads that very row
  back; after the second review of #111, `TestKeyMemoRaces`). A row removed by `-purge-remote` beside the server is met as a foreign
  key refusal when the delivery is stored: the key is forgotten and the sender told 503, to try again
  (`TestDBInboxKeyFollowsRow`). A row claimed more often than its attempts allow (its handling kept ending its
  worker or outlasting its lease) is failed at the claim, not handled again; a row whose actor's domain was
  suspended after it was taken is dropped and counted when a worker reaches it, as an admin who suspends means
  nothing more from it to be acted on (section 10; `TestDBInboxWorkerLimits`). **The caps are soft by a little:**
  the queue's 5,000 and a group's 500 are counted in the statement that inserts, under READ COMMITTED, so deliveries
  stored at the same moment can pass them by at most 3 rows (one fewer than the 4 decode turns, under which alone a
  delivery is stored), and the 10,000 domain policies by the saves running at once.
- **Connections (section 12).** Every statement federation makes for its own work takes one of `fedConnsMax` (3)
  turns (`dbTurn`), never across a fetch and never while taking another: the inbox (the indexed lookup, the row),
  the workers (claim, handling, finishing, giving a row back), the actor cache (reads, the write of an actor, a 410,
  the bound), the keys (`keyIDOf`, `ensureKey`, `privateKey`), `ensureOrigin`, the policy's load, the housekeeping
  step, and the search by handle's one statement (a local member), whose remote reads go through the actor cache's
  turns per statement and whose outbox and pages through the fetcher with no turn and no connection. So federation
  holds at most 3 of the pool's 8 (`TestDBInboxOneConnection`, `TestDBFedConnsBound`). Outside the turns, on
  purpose: the answers to readers (WebFinger, NodeInfo, the actors, the public pages and the outboxes), which are
  page requests held to `limits.public` and `limits.discovery` like any page, and the admin pages' reads; and the
  start (`startFederation`) and `-rotate-keys`, which run before or apart from any of it.
- **Housekeeping** (only in a mode that federates): the rows above, the replay memory's `sweep`, and one line with
  the readers' drop counts of the last pass (recipients and objects dropped, dates that were not dates). The
  replay memory's clock step is logged once (`stepped`). `ReplyToUnknown` and the note readers' counts come with
  the first item that decodes a post (FWR 5).
- **The numbers (14.2).** The Federation tab of the Admin page shows what waits (and how old the oldest is), what
  was given up in 7 days, and over the last 24 hours the deliveries taken, dropped per type, refused per reason and
  handled. Those counts are in memory: the tab says "kept in memory since the last start, <time>: a restart begins
  them again", and the dashboard's mode line, which says whether deliveries are paused and what waits, names the
  same time, so a restart is not read as a quiet day.
- **Not built** (FWR 2 has since built the first two, 8.8)**:** the `/healthz` fields (FWR 2's DoD), `TestFederationMemory` (it needs deliveries out: 50,000
  queued deliveries and 4 workers sending, FWR 2; the inbox's own share, 4 decodes of 4 MB at most, is held by
  `TestDecodeMemory` and the 4 turns), the flood block of 7.7 (FMD 7), the forward of 7.1.2 (FWR 6).
- **Measured** (2.1 GHz Xeon, 4 cores, local Postgres in Docker): `BenchmarkInboxRequest` (the whole server chain)
  with the key kept, 66 to 75 µs, 17.7 KB and 173 allocations for a delivery that is dropped, and 1.4 to 2.1 ms wall,
  26 KB and 219 allocations for one that is stored, the commit of its row included (the wall time is the commit's
  wait; the CPU is the dropped case's); an unsigned request with a 256 KB body, 6.2 to 6.6 µs, 3.1 KB and 41
  allocations (refused before the body is read); a 256 KB Delete from an actor never seen under a key nobody keeps,
  1.7 to 1.8 ms, 640 KB and 173 allocations (the body read and digested, `peekOwnDelete`, one indexed lookup, no
  fetch); `BenchmarkInboxVerify` (the signature alone) 43 to 48 µs and 59 to 64 allocations, as before.

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
`TestClockGuard` (**new**). Built with the signatures: `clockTrusted(now, built)`, which the signer
checks before every signature (it refuses with `errClockUntrusted`) and the verifier before every
check (`sigClock`, for the 503). The build's date is `builtAt()`: `buildTime` when the linker sets it
(`-X main.buildTime=<Unix seconds>`; nothing sets it yet), else the commit time Go records itself when
it builds in a git checkout (`./deploy.sh --build`), else unknown, which is logged once and skips the
check. `build-image.sh` builds with `-buildvcs=false`, so a release image does not know its date until
it passes `-X main.buildTime=$SOURCE_DATE_EPOCH`, the commit's own time, which keeps a commit building
the same bytes; that one line, with the release suite run to show it, belongs to the item that first
signs. The newest `updated_at` bound is the delivery queue's.

**A clock that jumps forward** (as built, FWR 2, after the second review of #112). Without a rule, a clock set a year
ahead would give up every queued row at its next claim with no request sent, housekeeping would clear what is not
old, and once the clock was set right the rows written meanwhile would be dated a year ahead and never claimed. The
rules, the simplest that close those:

- A queued row older than twice the horizon (96 hours) cannot come of the schedule, whose last attempt falls about 40
  hours after a row starts. Meeting one, the queue doubts the clock: such rows are held (given back for a minute, no
  attempt counted), housekeeping deletes nothing, and the Federation tab, the dashboard and `/healthz` say "waiting for
  the clock" (the tab adds that the clock may have jumped forward). The doubt lasts an hour on the monotonic clock. If
  the wall clock is set back before the moment of doubt, the doubt ends at once (the jump was undone); if the hour
  passes on a wall clock that moved with the monotonic one, the clock is believed (the server was off that long) and
  the rows are given up as 8.7 says. One line in the log says so when the doubt begins.
- Rows dated in the future, once the clock is right again: housekeeping (at the start and every hour) pulls a row's
  `next_at` back to at most the longest wait the schedule can give (12 hours and its 20%: 14.4 hours) and its
  `created_at` back to now, in batches of 1,000, so it is claimed and its 48 hours count from then. A future
  `failing_since` or `down_since` on a host is taken as now when its next failure is counted.
- The floor from the database (above) is believed for the first hour after a start only (8.8).

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

Until FRD 1, `main` returned as soon as `ListenAndServe` did, which is the moment `srv.Shutdown` begins:
the 5 s it is given were never waited for, and nothing waited for background work at all. FRD 1 made
`main` wait for `Shutdown` and for its key worker, 5 s in all (`serve`, `TestServeWaits`,
`TestDBServeStopsWorker`); a second signal during that wait ends the process at once. The delivery
item adds its workers to that wait: the workers have their own context and a
`sync.WaitGroup`, and on SIGTERM they stop claiming and are given 5 s for the request in flight;
whatever does not finish keeps its lease and is sent again after it ends. Receivers dedupe by activity
id, so a second send is harmless. `TestDeliveryShutdown` (**new**).

### 8.7 The queue and `./deploy.sh --rollback`

The tables are additive. An older build without the workers leaves them untouched; going forward
again resumes the queue, and rows past 48 hours are given up at the first claim. Nothing is lost that
a 48-hour outage would not lose anyway.

### 8.8 The delivery queue, as built (FWR 2)

"federation: the delivery queue" in `main.go` (`deliveryQueue`), migration 19 (with `flowa-schema18.dump` for the
restore drill). **Nothing in this version sends an activity**: there is no caller of `enqueue` until follows (FWR
3), and the dashboard's sentence about what this version does still ends "nothing is delivered". The queue is
exercised by the tests alone, and a federated instance starts its workers only when rows wait (none can, until FWR
3) or the first activity is enqueued. What was built, and the decisions where this section was silent or is
corrected:

- **Enqueue** (`enqueue(ctx, actor, apID, body, recipients)`): one transaction writes the activity once to
  `ap_outbox` and one row per inbox to `ap_deliveries`. A recipient's shared inbox replaces its own when its actor
  names one (ActivityPub 7.1.3), so followers on one server get one row; the same inbox is written once (7.1);
  an inbox on this instance's own host, an address that is not `https` on port 443 or not a name another server can
  have (`fedHost`), and a host the domain policy refuses are left out and counted as refused. **Every recipient names
  its actor's id, and its inbox must be on that actor's host** (after the review of #112): otherwise an actor on one
  server could name an address on another (an inbox that redirects, or fails), and this server's failures there
  would open the other server's circuit and count towards its week. `enqueue` checks it itself and refuses a
  mismatch; a shared inbox on another host than the actor's is passed over for the actor's own inbox (ActivityPub
  7.1.3 makes the shared inbox an optimisation, and 7.1 lets a server filter its targets by its own rules). What it
  costs: a server whose actors name a shared inbox on another host gets one request per follower there instead of
  one per post. None of the pages on disk shows such a server: ActivityPub's examples and Mastodon's ActivityPub page
  name no shared inbox on another host, and Mastodon's `WEB_DOMAIN` puts the actor and its inboxes on the same host.
  **For FWR 3:** its callers pass each follower's actor id beside its inbox and shared inbox, as they come from the
  cached actor (`ap_actors.ap_id`, `inbox`, `shared_inbox`). The actor id must be that verified `ap_actors.ap_id`,
  the id of an actor fetched from its own origin (7.3), never one a delivery merely named: the host rule is only as
  good as the id it is checked against. Addresses are compared in one spelling (scheme and host in lower case, no
  `:443`), so `https://A.EXAMPLE/inbox` and `https://a.example/inbox` are one row. The activity's id
  must be on this origin and its body JSON of at most 256 KB, sent as it was given (`body` is `TEXT`, not `JSONB`,
  which would reorder it: a correction to 9.1), and signed at each send. One enqueue or fan-out runs at a time
  (`pg_advisory_xact_lock` on `ap_outbox`), so the caps below are exact, not soft as the inbox's are.
- **Back-pressure** (8.5): 2,000 rows queued per host, counting the rows the same activity writes; the rest are not
  written and are added to the host's `dropped`. 50,000 rows queued in all: when an activity would pass it, or while
  another activity waits, it waits in `ap_outbox` with its inboxes (`targets`, a new column) and is fanned out by a
  worker, oldest first, once fewer than 40,000 rows are queued, against the hosts and the policy as they are then.
  One activity names at most 10,000 inboxes after the shared inboxes, so a waiting activity always fits once the
  queue is under 40,000: 50,000 is a hard bound. At most 1,000 activities wait; past that `enqueue` answers
  `errDeliveryFull` and writes nothing, which tells its caller (FWR 3 onward) to keep the local action and let the
  delivery go, as an outage would.
- **Workers** (8.2): `FLOWA_FEDERATION_WORKERS`, 4 when unset, 1 to 8, read and checked at the start in a mode that
  federates, beside `FLOWA_ORIGIN` (a stray value in local or plain single mode is ignored, as theirs are). A worker
  claims one row at a time (`deliveryClaimSQL`, prepared once: planning it took 1.7 ms, running it 0.6 ms), never a
  row of a host this process is sending to (one request at a time per host, kept in memory, so per process: two
  processes during a deploy may each send one), nor of a host whose circuit is open or that is unavailable. When a
  circuit opens the host's rows are moved to its end, and a row written for an open host is due then, so the claim
  never passes over them (with 25 hosts open and their 4,000 rows at the head of the index a claim took 6.5 ms;
  moved, it takes 0.8 ms). A row
  claimed for a host another worker took between the list and the claim is given back at once. Before the send:
  the row is given up past 48 hours from when it was written (the first attempt is due then: F9's "first attempt");
  the domain policy is asked again (`admits`), so a host suspended, or no longer allowed, since the row was written
  gets nothing and the row is given up; the signer's key is read (`privateKey`; a member with no key yet waits a
  minute, without counting an attempt); and one of the pacer's places is taken: places are a tenth of a second
  apart in all, so at most 10 requests a second, and a place more than a second ahead (a clock that stepped back) is
  not waited for. A row held longer than half its 60 s lease has the lease renewed before the send, and is not sent
  if it lost it: every statement about a claimed row names the lease it holds, so a worker that lost its row
  changes nothing. The request goes through the guarded fetcher (`fetchDelivery`: port 443, the dial guard, 5 s,
  4 KB of the answer read, never redirected), with the policy's `admits` and refused list, signed by `httpSigner`
  with the actor's key (the instance actor's for a row whose `actor_id` is NULL).
- **Answers** (8.3, `classify`): 2xx sent: the row is deleted and the host's row keeps the count, the last success,
  the form that worked, and that it is well. 400 or 401: sent again at once in the other form, with another place
  but no attempt counted; the form that is taken is recorded in `ap_hosts.sig` and sent first from then on (7.1). A
  400 in both forms is given up; a 401 in both is tried again on the schedule and does not count towards the host's
  circuit, since the host answered (any answer that does not count is a sign of life, below). 410: the row and every
  other row queued for that inbox are given up, and
  `goneInbox` is told the inbox. As built (FWR 3): the hook is `goneFollower`, which removes the follows of local
  members by the accounts whose own inbox it was (6.4, "Follows across servers, as built").
  404 and any other 4xx but 408 and 429: given up. 429: tried again after its `Retry-After` when that is between 1
  minute and 12 hours, else on the schedule, not counting towards the circuit. A redirect (a delivery is never
  redirected, 7.8), with or without a `Location`: tried again on the schedule and **not** counted towards the circuit,
  since it is the address's fault, not the host's. 5xx, 408, a refused or failed connection, a time out: tried again
  on the schedule and counted towards the circuit. This server's own hold-ups (its own request to that host holding the fetcher's turn, the
  clock refused by the signer) give the row back for a minute with no attempt counted. The schedule is 8.3's, each
  wait moved by up to 20% either way; after the 11th attempt, or when the next would fall past 48 hours, the row is
  given up. Failed rows keep the reason (`last_error`, the fetcher's fixed phrase: a host and a status, never a body)
  and every failure is logged with the host, the status and the attempt (7.9).
- **Hosts** (8.4): `ap_hosts` as 9.1 with these **corrections**: `failing_since` (when the failures in a row began,
  for "5 within 10 minutes"), `down_since` (when the host's streak of failures with no sign of life began, for
  "unavailable"), `last_error`, `alive_at` (the last sign of life), `probed_at`, `sig_learned` (a send of ours
  learned the form), `created_at`, and the partial index `ap_hosts_held` of the hosts not `ok`, which is all the
  claim reads of the table; `software` and the flood block's use of `until` wait for the items that need them (FMD
  7). 5 failures within 10 minutes open the circuit for 10 minutes. When its time is up the circuit is half-open: one
  row is tried (one request per host does that by itself), a failure that counts opens it again at once, and any
  other answer (taken, or a 4xx, 429 or redirect: the host answered) closes it. One request at a time per host is kept
  per process, so during a deploy, when two processes run, each may try one row. Only a delivery taken or a valid
  delivery from the host resets the failures in a row: a 401, a 429 or a redirect is a sign of life but leaves the
  count, so a host answering 503 and 429 in turn still opens its circuit. A 429 with `Retry-After` holds the whole host
  for that long, at most an hour, as an open circuit does (its rows moved to the end of it). **Unavailable** (corrected after the
  review of #112: it counted the 7 days from the last success, so one 503 after a quiet week marked a host
  unavailable): a host that has failed for 7 days with no sign of life between, and no two failures further apart than
  48 hours (a longer silence says nothing, since every row of the first failure was long given up, so the streak
  starts again). So a host this server writes to less often than about every 88 hours (a row's last attempt falls
  about 40 hours after it starts, and the next row's first failure must come within 48 hours of it) never becomes
  unavailable: harmless, since each of its rows still has its own 11 attempts and 48 hours, and being unavailable
  only saves requests to a host that is written to often. The mark is written only if the host is still due for it
  when the statement runs, so a sign of life written between the counted failure and the mark wins. A sign of life is
  any answer from the host but a failure that counts, a valid delivery from it, or
  an answer to the probe; each ends the streak (`down_since` back to NULL). An unavailable host's queued rows are given
  up and new ones are not written (counted as unavailable). Once a day housekeeping asks
  the NodeInfo discovery document of up to 20 unavailable hosts, one after the other, and an answer marks a host
  available. **Correction:** the probe is an unsigned GET, as the fetcher's NodeInfo kind always is (7.8): NodeInfo is
  public, and a signature would only freeze the origin earlier. A verified delivery from a host, in either form, is a
  sign of life (`heardFrom`), written at most once an hour per host (memory says when it was last written, for at most
  10,000 hosts), and at once when the host is unavailable (it is available again) or first seen signing with RFC 9421,
  which marks it for that form (7.1) unless a send of ours learned the form it takes: what a send learned wins, so a
  server that signs with RFC 9421 and takes only the draft is not flipped back and forth.
- **Nothing signed before the clock is set** (8.3): no row is claimed while the clock is earlier than the build
  (`builtAt`) or than the newest time the database held at the start (the newest of `instance_settings.updated_at`,
  `ap_outbox.created_at` and the hosts' last success and failure, read once at the start); the signer refuses on its
  own as well. The Federation tab, the dashboard's mode line and `/healthz` say "waiting for the clock". The floor is
  believed for the first hour after the start only: a Raspberry Pi's clock is set by the network within minutes, so
  a clock still before the floor an hour later means the floor is wrong (something was written while a clock was
  ahead). It is then dropped, with one line in the log ("... is not believed and deliveries go on"), rather than
  holding every delivery until that time comes. Until then the operator sees "waiting for the clock".
- **The pause and refusal:** with the `federation` flag off, or federation refused, nothing is claimed and every row
  stays as it was; `enqueue` still writes while paused (the pause holds sending, not the members), and refuses while
  federation is refused.
- **Connections** (section 12): every statement takes one of the federation's 3 turns and gives it back before the
  next, a transaction (the enqueue, a fan-out) holds one turn for its statements, and none is held across a request or
  a wait for a place: a worker waiting on a slow host holds no connection, no turn and no lock.
- **Shutdown** (8.6), **correction**: the workers have a context and a wait group of their own, which
  `federation.stop` ends and waits for within `serve`'s 5 s. A row claimed and not yet sent (waiting for its place,
  its key or its turn) is given back at once, its attempt not counted, rather than left to its lease; a request
  already sent is let finish within the fetcher's bounds and its answer written on a context of its own; one that
  outlasts the stop keeps its lease and is sent again after it ends, which receivers' de-duplication by id makes
  harmless.
- **Retention** (8.5, 9.3): sent rows go at once; failed rows (`failed_at`, a new column) are removed by housekeeping
  after 7 days, and past the newest 20,000 failed rows the oldest go too, so a run of give-ups (an unavailable host
  alone gives up to 2,000) cannot grow the table without bound; an activity with no row left goes 7 days after it was
  written. Each delete removes at most 1,000 rows, chosen through an index and removed through the primary key
  (`id = ANY(ARRAY(...))`: the `id IN (SELECT ...)` form was planned as a pass over the whole table, 24 to 40 ms a
  batch at 150,000 rows, against 2 ms), and gives its turn back before the next, on the workers' context, so
  a stop ends it between two statements. `-purge-remote` removes every row of
  the three tables (each names another server), and says how many.
- **What it shows** (14.2): the Federation tab's "Sending to other servers" (queued and the oldest, waiting
  activities, sent and given up over the last day from memory, given up in 7 days, hosts paused after failures and
  unavailable; "20,000 or more" when the count of rows given up reaches the 20,000 kept, one constant for both), the dashboard's mode line
  (to send, the oldest, given up, unavailable, or waiting for the clock), and `/healthz` for a local probe (14.2). The
  numbers are asked only for a local probe, and kept 5 seconds: a remote probe, which sees only `{"ok": true}`, runs
  no statement of the queue's.
- **Taking back** (as built in FWR 4, after its review): an activity can name one it takes back (`ap_outbox.undoes`: a
  post's Delete names its Create; FWR 3's Undo names its Follow), and at no inbox does the second reach the server before
  the first's outcome there. The claim orders rows by when they are due, so without a rule a Create that met one 5xx (due
  again in minutes) would be overtaken by its Delete, and the post would stay on that server. The rule, inbox by inbox,
  when the second is fanned out (`takeBackSQL`): where the first is queued, not claimed and **never tried** (`attempts`
  0: no request for it ever left), it is given up ("taken back before it was sent") and the second goes nowhere there;
  where it is queued and not claimed **after an attempt** (a 5xx, a time out, a refusal, a redirect, an open circuit
  holding it), that request may have arrived (a 504 from the other server's proxy after it stored the post, a slow
  server), so the first is given up ("taken back after an attempt that may have arrived") and **the second goes** (after
  the second review of FWR 4: it went nowhere, and the post stayed there for good; for an Undo, a phantom follower); where
  the first is claimed (a request may be in flight), the second is due no earlier than the end of the first's lease;
  where the first is gone (taken, or given up), the second goes as any row. A first that still waits for its own fan-out
  (the queue was full) is dropped with the second, which then goes nowhere at all. `attempts` counts the claims that may
  have sent a request: every path that gives a claimed row back unsent takes its attempt back (`giveBack`: another worker
  sending to the host, the clock doubted, the origin or key not ready, a stop, the hold below, the fetcher's turn at the
  host not had), and the pause, the clock guard, an open circuit and an unavailable host keep a row from being claimed;
  a lease that runs out keeps its attempt (`takeBackSQL`'s comment lists them). At the second's send (`heldBackSQL`, in
  `deliver`), with the first's row locked first, so a claim made by another worker meanwhile is seen as it is now (after
  the second review: it read the row as it had been, and could mark a claimed Create failed and send the Delete beside
  it): if the first is claimed, the second is given back until that lease ends (no attempt counted); if it is queued and
  not claimed (its request failed meanwhile), the first is given up and the second is sent. A second sent to a server
  that never got the first is harmless (a Delete or an Undo of something unknown is ignored).
  `TestDBDeleteNeverOvertakesCreate` (never tried, failed once, in flight, taken, and the hold at the send),
  `TestDBDeleteAfterAmbiguousFailure` (a Create answered 504, then its Delete sent), `TestDBHeldBackSeesLateClaim` and
  `TestDBTakeBackSeesLateClaim` (another worker's claim committed while each statement waits for the row),
  `TestDBDeleteOfWaitingCreate`, `TestDBUndoNeverOvertakesFollow`.
- **Measured** (2.1 GHz Xeon, 4 cores, local Postgres in Docker): `BenchmarkClaimDelivery`, one claim with 50,000
  rows queued on 300 hosts, 4 being sent to and 25 with an open circuit, 0.75 to 0.85 ms, 1.8 KB and 41 allocations
  (budget 2 ms; 0.62 ms with no circuit open; 1.6 to 1.7 ms before the statement was prepared). `TestFederationMemory`:
  the rows live in Postgres, not in the Go heap, so what it measures is the heap of the workers holding what they
  claimed: it grew by 1.6 MB while 4 workers each held a 256 KB activity in a request the stub held, claimed with
  50,000 rows queued; with the inbox's bound added (4
  decodes of at most 3.2 MB measured here, and 8 bodies of 256 KB being read: 14.9 MB) that is 16.5 MB of the 32 MB
  budget. The inbox's share is a bound added, not a measurement taken at the same moment; the Pi's figure and the
  container's (FWR 8's `docker stats`) are not measured. The fan-out of section 12 (1,000 followers on 300 hosts) is
  `BenchmarkDeliveryFanOut`, bound by 10 requests a second to about 30 s anywhere (29.9 s here, the stub answering
  at once); its figure on the Pi is the owner's to write into the todo.

## 9. Data model

### 9.1 Tables and columns (all **new**; one migration per pull request that needs it, appended, additive)

```sql
-- instance settings that change at runtime, and the recorded identity
CREATE TABLE instance_settings (
  key TEXT PRIMARY KEY,               -- origin, first_federated_at, policy, authorized_fetch, registration, rules, description, contact, retention_days, nodeinfo_version
                                      -- (migration 10 made the table; INS 5 writes description, contact and rules, one row each; INS 6 writes registration, "open" or "approval"; the rest come with their items)
  value JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by BIGINT REFERENCES users(id) ON DELETE SET NULL
);

-- remote accounts are users rows (F5); '' is local
ALTER TABLE users ADD COLUMN IF NOT EXISTS domain TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS federates BOOLEAN NOT NULL DEFAULT false;   -- the member's opt-in (F2)
ALTER TABLE users ADD COLUMN IF NOT EXISTS locked BOOLEAN NOT NULL DEFAULT false;      -- approves followers by hand
CREATE INDEX IF NOT EXISTS users_local ON users (id) WHERE domain = '';

CREATE TABLE retired_usernames (username TEXT PRIMARY KEY, retired_at TIMESTAMPTZ NOT NULL DEFAULT now());   -- migration 14, built in FRD 5

-- requests to join while registration is by approval (migration 11, built in INS 6): not users rows, so
-- nothing that reads users (lists, counts, mentions, follows, sessions) meets a person nobody approved
CREATE TABLE join_requests (
  id BIGSERIAL PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,        -- taken while it waits: sign-up checks users and this table
  display_name TEXT NOT NULL,
  pw_hash TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',      -- "Why do you want to join?", at most 500 characters, plain text
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()   -- housekeeping removes a request after 30 days
);

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

-- key pairs of local actors; user_id NULL is the instance actor (migration 12, built in FRD 1)
CREATE TABLE ap_keys (
  key_id TEXT PRIMARY KEY,             -- the part after the origin: /actor#main-key, /users/ana#main-key
                                       -- (the origin is recorded once and frozen, so rows do not repeat it)
  user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
  public_key TEXT NOT NULL,            -- SPKI PEM (BEGIN PUBLIC KEY)
  private_sealed TEXT NOT NULL,        -- "v1." + base64url(nonce, ciphertext, tag): the PKCS#8 key under
                                       -- AES-256-GCM with a key made from FLOWA_KEY_SECRET by HKDF-SHA256,
                                       -- the key id and owner authenticated with it (F7)
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT ap_keys_one_per_owner UNIQUE NULLS NOT DISTINCT (user_id)   -- one key per owner, the NULL too
);

-- follows in both directions, local and remote. follows is migration 9 (INS), local accounts only so
-- far, without state and ap_id: nothing reads them until follows cross servers, so they come with the
-- migration of that item (FWR 3), state defaulting to 'accepted' for the local rows already there. The
-- index for "who follows me" is (followee_id, follower_id) for now; state joins it then. Nobody follows
-- themself (follows_not_self). A block removes the follows between the two, both ways, in the block's
-- own statement, and a follow across a block is refused, either way, with a 403 and one sentence ("You
-- cannot follow this account.") that does not say who blocked whom. It is not a 404: the profile still
-- opens, so "no such user" would be false. The blocked member is not told of the block but can notice
-- it (they stop following and cannot follow again), as on Mastodon; the page says so before a block.
-- Counts, lists and Home leave out a follow across a block that was written at the same moment as it.
CREATE TABLE follows (
  follower_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  followee_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  state TEXT NOT NULL CHECK (state IN ('pending', 'accepted')),   -- FWR 3
  ap_id TEXT NOT NULL DEFAULT '',       -- FWR 3: the Follow activity's id, for Accept and Undo
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (follower_id, followee_id),
  CONSTRAINT follows_not_self CHECK (follower_id <> followee_id)
);
CREATE INDEX follows_followee ON follows (followee_id, follower_id);  -- (followee_id, state) from FWR 3

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
ALTER TABLE posts ADD COLUMN IF NOT EXISTS audience TEXT NOT NULL DEFAULT 'local';  -- local, public, unlisted, followers (migration 14, built in FRD 5:
                                                                                     -- the check posts_audience NOT VALID, and posts_public (user_id, id DESC)
                                                                                     -- WHERE audience = 'public' AND hidden_at IS NULL for the public pages)
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
  domain TEXT PRIMARY KEY,             -- plain ASCII, lower case, a name in another script in its xn-- form (7.8)
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

As built (FRD 7, migration 15, with `flowa-schema14.dump` for the restore drill): `users.domain` and
`users_local` as above, and a check, `users_remote_inert`, that keeps a remote row inert whatever writes it
(this build, or an older one after a rollback): `domain = '' OR (pw_hash = '!' AND role = 'user' AND NOT
owner AND NOT federates AND` the username ending in `@` and the domain`)`. Every existing row has `domain`
'', so every existing account is local and the check holds for it; adding the column and the check reads
`users` once. `ap_actors` differs from the block above: **corrections** found while building it:
`preferred_username`, `summary` (HTML as received, at most 2,048 bytes, for `htmlToText`) and `icon` are
added, since 6.3 reads them and no other table holds them (`name` goes in `users.display_name`);
`locked` (manuallyApprovesFollowers) is here, not on `users` (`users.locked` is not added: a local member's
choice comes with FWR 3); `gone` is `state`, one of `ok`, `suspended` or `gone` (6.3, as built); `kind` is
checked against the five actor types; `inbox` may be empty (a suspended actor may have none); `key_id` and
`public_key` may be empty (an actor naming no usable key is still cached, and a key lookup then fails);
`ap_actors_key` holds only rows with a key; and `ap_actors_fetched (fetched_at, user_id)` serves the removal
of the least recently fetched. The migration also adds an index for each column referencing `users` that had
none (`comments_user`, `notifications_actor`, `reports_reporter`, `reports_resolver`, `mod_actions_actor`,
`mod_actions_target`, `catalog_revisions_author`, `instance_settings_by`, `sessions_user`), so the removal can
ask whether anything points at an account one row at a time (7.4); each reads its table once when it is made. `fetched_at` and `expires_at` have no default: the cache writes them from its
own clock.

As built (FRD 8, migration 16, with `flowa-schema15.dump` for the restore drill): `ap_actors.outbox TEXT NOT
NULL DEFAULT ''`, the outbox the actor names, which the search by handle reads (11); the decoder reads `outbox`
(in each spelling of its name) and leaves out one it cannot use rather than refusing the actor (6.3), and a
suspended actor keeps none, as it keeps no other address.

As built (FWR 2, migration 19, with `flowa-schema18.dump` for the restore drill): `ap_outbox`, `ap_deliveries` and
`ap_hosts` as 8.8 says, with these **corrections** to the block above: `ap_outbox.body` is `TEXT` (the bytes sent),
`targets TEXT[]` holds the inboxes of an activity whose fan-out waits, `created_at` has no default (the queue's clock
writes it), and `actor_id` is indexed for the actor cache's bound (`userRefs`); `ap_deliveries` gains `failed_at`, its
`created_at` has no default, and its indexes are `ap_deliveries_due (next_at, id)`, `ap_deliveries_host (host)`,
`ap_deliveries_age (created_at)`, all `WHERE state = 'queued'`, and `ap_deliveries_failed (failed_at) WHERE state =
'failed'`; `ap_hosts` gains `failing_since`, `down_since`, `last_error`, `alive_at`, `probed_at`, `sig_learned` and `created_at`,
checks `host` lower case and `sig`, leaves `software` to a later item, and has `ap_hosts_held (host) WHERE state <> 'ok'`.

### 9.2 Why remote content joins the existing tables (F5)

Remote accounts become `users` rows with `domain` set, `username` of the form `bo@social.example`
(which `usernameRE` can never produce, so no local name can collide), `pw_hash` `!` (which
`checkPassword` can never match), role `user`, never an owner. Remote posts and comments are rows of
`posts` and `comments` with `ap_id` set.

As built (FRD 7): the one unique index on `username` stays and holds local and remote names apart, since every
remote name holds an `@` and the check `users_remote_inert` requires it to end in `@` and its domain;
`(username, domain)` needs no index of its own. A name is not an identity (the SWICG WebFinger report:
implementers "SHOULD use the actor id in any references to an actor"), so `ap_actors.ap_id` identifies the
row, and the username is a key that is written once and never renamed (`remoteUsername`): the
`preferredUsername` as written, **case kept** (the same report: "SHOULD NOT assume case insensitivity for
external usernames"), when it is 1 to 64 bytes of the characters RFC 3986 leaves unreserved, which hold
every name Mastodon allows; otherwise, or when another actor of the host holds the name already, it gets
`+` and twelve hex digits of the SHA-256 of the actor id, which no plain name has. So a local `ana` and a
remote `ana@social.example` are two names, as are `Bo@social.example` and `bo@social.example` if a server has
both, and a name in another script or with an `@` of its own is kept in `ap_actors.preferred_username` and
shown by later items from there. Hosts are compared and kept in ASCII: an id in another script arrives in its
`xn--` form (the decoder takes nothing else, 6.3), and it is kept and shown in that form, which also shows a
name that imitates another in a different script for what it is; turning it back into Unicode would need
`golang.org/x/net/idna`, which is not a dependency.

What reads `users` (every statement in `main.go`, gone through for this item) and where it stands with
remote rows, `TestDBRemoteRowsStayRemote` holding each surface byte for byte with and without them:

- **Local only, by `domain = ''` in the statement:** sign-in (`signinSQL`, both halves, so a remote name costs
  the same dummy hash as an unknown one), sessions (`currentUser`; `startSession` writes none for a remote
  row), the people list (`peopleSQL`, now through `users_local`), a profile (`handleUserProfile`), a member's
  posts by name (`handleGetPosts`), mentions (`notifyNames`), follows and blocks by name and by id
  (`memberNamed`, `handleBlock`, `handleFollow`), a profile picture (`handleAvatar`), the admin's list
  (`handleUsers`), deleting an account, resetting its password, its role and handing over the owner
  (`handleDeleteUser`, `handleResetPassword`, `handleSetRole`, `handleSetOwner`: a remote id is "No such
  user"; FMD acts on remote accounts its own way), the dashboard's totals and active count (`dashTotalsSQL`,
  `dashActiveSQL`), NodeInfo's counts (`nodeCountsSQL`), a key's owner (`keyIDOf`, so no remote row gets a
  key) and the members missing one (`missingKeysSQL`), the operator's account (`seedAdmin`), and every count
  of single mode (`checkSingle`, `soleAdmin`, `makeVisitorPage`).
- **Local by the check, with no change:** WebFinger (`webFingerSQL`) and every public address (the actor,
  posts, pictures, outbox and collections, through `publicMember`) need `federates` or the owner, which a remote
  row never has; `ensureOwner` and the last-admin checks need the admin role, which it never has. Sign-up, a
  request to join and its approval check a name `usernameRE` allowed, which has no `@`.
- **Local by construction:** the follow, block and follower lists read the rows of `follows` and `blocks`,
  which only the local-only statements above write; the reads by the session's own id (Settings, password,
  progress, the progress backup) are a local account's.
- **Open to remote rows on purpose (F5, as above):** `usersByID` (how a post's author is shown), the
  moderation queue and the audit log's names, `targetOwner` and `handleModAction` (moderating a remote
  account, once one can be met), and `retireSQL` (a remote row never federates nor has a key, so it is never
  retired). Nothing in this version shows a remote row through them, since nothing points at one.


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
  dashboard to index scans or to the row counts it already allows. As built (FRD 7): with the 2,000 remote
  accounts (each with its cached actor and key, the most XP, signed in just now) every plan it held holds,
  the people list and the dashboard's totals and active count now reading local members through
  `users_local` rather than `users` whole (700, 501 and 7,501 rows read); the lookups by id and by key id read
  one row each, and removing the 200 least recently fetched about 1,200. The remote posts and follows come with
  FWR, whose items extend it.
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

served by `posts_user (user_id, id DESC)` per followed account. As built in INS (`homeSQL`, without
the audience and state conditions, which come with federation), Postgres cannot merge the newest posts of
accounts it learns only while the query runs, so each followed account gives its own newest page
through a `LATERAL` subquery and the newest 21 of those are kept; blockFree is asked once per account.
A page reads at most 21 posts per account followed, whatever the size of `posts`, and a member follows
at most 1,000 accounts (`maxFollows`). `TestDBQueryPlans` holds it for a reader who follows 500 of
1,000 accounts with 30,000 posts: about 33,700 rows counted over the plan's steps (44,200 as a
moderator, who also sees hidden posts), under a bound of 50,000.

### 9.3 Retention

| What | Kept |
| --- | --- |
| Remote posts nobody local liked, commented, bookmarked or reported | 30 days (instance setting `retention_days`, 7 to 365) |
| Remote posts someone interacted with | while the interaction exists |
| Remote accounts nobody follows, with no content left | 30 days after their last content |
| Cached actor documents | refreshed after 24 hours on use; at most 20,000, the least recently fetched removed first, never one something points at (as built in FRD 7, 7.4) |
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

As built (FRD 5), the other direction, Flowa's own pictures for other servers and for the public pages:
`GET /users/{name}/avatar.{jpg,png}` and `GET /users/{name}/posts/{id}/picture.{jpg,png}`. Each statement asks
for the picture by the member's name, and the post's id with it, under the same conditions as the documents
(the member shown, the post Public, not hidden, not a milestone), so no other picture can be reached through
them, whatever name, id or extension is tried; the wrong extension is the 404 too. The bytes are the stored
picture, which `cleanImage` already made again; found answers are kept three minutes (`public, max-age=180`,
with an `ETag`), because a picture withdrawn by an opt-out or a hide should leave caches soon, and the avatar's
`?v=` changes with the picture. The app's own `GET /api/users/{id}/avatar` and `GET /api/previews/{file}` keep
their session.

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
| Registration open, closed | adopt | the `signups` flag already, kept as the one switch that closes sign-up; the `registration` instance setting says how people join while it is on, `open` (what nothing saved means) or `approval`, so it has no third value "closed" that could disagree with the flag (INS). Built in every mode, local included |
| Registration with approval | adopt | built: a sign-up is a request in `join_requests` (9.1), not an account, with an optional reason ("Why do you want to join?", as Mastodon asks); admins with `user.manage` approve or decline it in Accounts, both audited; the sign-up page says so before the first field; the person is not told (no email) and learns it by signing in, and only the right password hears that the request waits; at most 200 wait, and housekeeping lets a request go after 30 days (INS) |
| Invites | later | useful for a household; not needed to federate |
| Server rules shown at sign-up | adopt | instance setting `rules`, shown on sign-up and on the about page; edited in Admin, audited (INS) |
| Instance description, contact, about page | adopt | instance settings, the NodeInfo `nodeName`, an about page (INS). Built: the about page is in the app for every member in every mode, and opens to a signed-out visitor only while a description is saved, so an instance that saves nothing answers `GET /api/about` as an unknown address; the contact is an email address (letters, digits and `. _ + -` before the `@`), an http or https link whose host is in plain letters (a look-alike host is written in its `xn--` form) or a short text, never another kind of link; a save that meets another admin's newer one is refused (409), and the admin chooses between loading theirs and saving over it |
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

As built (FMD 1, the part the inbox cannot be safe without; shipped with FWR 1, as 14.3 row 14 pairs them):
`domain_policies` as in 9.1 (migration 17, with `flowa-schema16.dump`), with `created_by` indexed and in
`userRefs`; the policy `allowlist` or `open` in `instance_settings` under the key `federation_policy` (not `policy`:
the table holds every runtime setting, and a bare `policy` would not say whose), where nothing saved means the
allowlist (F8); the permissions `federation.read` (moderators and admins) and `federation.manage` (admins) in a table
of their own, `fedPerms`, listed in `/api/me` only in a mode that federates, so a local instance lists the
permissions it always did; `GET /api/admin/federation`, `PUT /api/admin/federation/policy`, `PUT` and `DELETE
/api/admin/federation/domains/{domain}` (in `testdata/routes-federated.txt`), each change audited in its own
transaction (`domain_policy`, `domain_policy_remove`, `federation_policy`) and held to `limits.domains` (300 an hour
per admin); and the Admin page's Federation tab. A domain is matched with its subdomains, and a suspension anywhere
above a host wins over an allow (`fedPolicy.decide`). The domain is written as the fetcher compares it: lower case
ASCII, a name in another script in its `xn--` form (7.8), at least two labels, never this instance's own. The rows are
read into memory at start and after each change, so a check costs no query; at most 10,000 rows. Applied: the inbox
refuses a suspended domain, and in allowlist mode an unlisted one, with 403 before the body is read or a key fetched
(7.7); and the fetcher refuses, on the first URL and every redirect, every host the policy does not let in
(`fetchReq.admits`, the actor cache's `admits` hook): F8 defines the allowlist as "talk only to servers an admin
listed", and 7.6 reads it as "sends to and accepts from only the listed servers", so in allowlist mode no key, actor or
WebFinger request leaves for an unlisted host, and a member's search by handle for one is told so before any connection
(`lookupNotAllowed`, 403, most of its allowance given back; a redirect to an unlisted host mid-lookup is the one "not
found" of FRD 8's review). A fresh federating instance therefore reaches no other server until an admin allows a domain
or chooses open, which the README and the Admin page say; a change of policy forgets the cache's remembered refusals at
once. As 7.6 says, neither mode makes the public pages private. **Not built yet** (FMD's todo): the effect of `limit` (follows across servers are FWR 3; the API takes allow and suspend only), reject media and
reject reports (no remote media or Flag is taken yet), the public comment on the about page, and the sentence the
page owes on authorized fetch (7.6) beyond the one it says now (that any server can still read public pages).

## 11. The member's side

- **As built (FRD 4):** `users.federates` (migration 13, off for everyone) and the switch in Settings, in a
  card "Beyond this server" shown only in a mode that federates. Its switch is labelled for what is built, "Let
  other servers find me by my handle", and the card shows the member's handle and the server's sentence on what
  this version does (only the handle can be found, nothing is sent, practice, progress, notes, stats and badges
  never leave); the label and the sentence come from the server (`fedView`), so the page cannot say more than
  is built. While federation is refused the card says sharing is unavailable, gives staff the reason, and has
  no switch. `PUT /api/me` takes `federates` only in a mode that federates; outside one it ignores the field as
  any unknown field (so a deployment that sets nothing new answers as before), and in single mode with
  `FLOWA_FEDERATE=on` it refuses it with 400 and the reason (that mode is new, nothing relied on the field
  there, and the owner should learn the setting decides). `/api/me` says `federation` (handle, `optedIn`,
  `label`, `built`, `implied` in single mode, `warning`, `unavailable`, and `refused` for staff) only in a mode
  that federates, and has no such key otherwise. Every start asks for the keys of members who opted in and
  have none (a request dropped from a full queue of 64, or held back by a refusal), as many as the queue holds. The opt-in is not part of the user object other
  answers carry. Opting in asks for the member's key (5.7); opting out keeps it. A name that ends with a
  dot or a dash gets the warning of finding 11 beside the switch once it is on.
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
- **As built (FRD 5), the switch:** the card's switch is now F2's "Share my profile and Public posts beyond this
  server", and the server's sentence says what this version does (found by the handle; the profile and the
  posts marked Public read by anyone, on other servers or in a browser, without signing in; nobody can follow
  yet and nothing is sent), with a link to the member's public page once on. The composer's sentence under
  Public says the same ("Anyone can read it, on other servers or in a browser, without signing in. It is not
  sent to anyone.").
- **As built (FRD 5):** `posts.audience` (migration 14), `local` (This server) for every post there was and
  for every post made outside a mode that federates; the check allows the four audiences of 9.1 and is added
  `NOT VALID`, so no row is read to add it. The composer shows "Who can read it", a labelled list with Public
  first and chosen, and This server, only where the server federates, the member has opted in (or is the owner
  of a single-profile instance with `FLOWA_FEDERATE=on`) and federation is not refused; its labels and the
  sentence under it are the server's (`fedView`'s `audiences`). The server never gives Public by a default:
  a post that names no audience is This server, so a page from before audiences cannot publish by leaving the
  field out; Public is refused with the reason to a member who has not opted in and while federation is
  refused; outside a mode that federates the field is ignored like any unknown field. The author sees "Public"
  or "This server" beside the post's time; nobody else's answer carries the audience, and outside a mode that
  federates no answer does. Milestone posts are This server (F13). Posts are not edited (1.2), so the
  audience is decided once: a Public post that another server may have read is not made private by a change
  of a column, and taking it back is deleting it, which FWR's Delete then sends where it went. Comments have
  no audience of their own: section 14.5 sends comments on Public posts only, and comments are not on the
  public pages of this version (FWR serves them as Notes).
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
- **As built (FRD 8), people elsewhere:** `POST /api/fed/lookup` (`handleLookup`, only in a mode that
  federates, `testdata/routes-federated.txt`), and the card "People elsewhere" in the timeline's side column
  (and in single mode with `FLOWA_FEDERATE=on`, where People is hidden), shown while `/api/me`'s `federation`
  says `lookup`, which it does for every member while federation is not refused and the `community` flag is on
  (the route answers only then, as the timeline does); looking someone up shares nothing of the member, so it
  does not wait for the opt-in, **an open question for the owner** beside the unlisted posts below. An actor
  whose server does not answer is shown as the cache kept it (7.4: at most 30 days), with "Its server did not
  answer just now" and the date it was last fetched; a gone or suspended actor is told as such, with nothing
  of it shown. It takes `@name@host`, `name@host`,
  `acct:name@host` or an https address (`parseLookup`, strict: RFC 7565's userpart, an ASCII host with no
  port, `http://` told that only https is asked); resolves it as 5.3's "As built (FRD 8)" says; shows the name,
  the proven handle, the bio as plain text (`htmlToText`), whether the account approves followers by hand, a link
  to its profile on its own server, and up to 20 of its latest posts from the first page of its outbox (the
  outbox and the page on the actor's origin and each where it came from; each item a Create by the actor, the
  Create and its Note on the actor's origin, the Note attributed to the actor alone and addressed to the public
  collection in `to` or `cc`, which 6.7 calls public and unlisted, both posts their author let anyone read, as
  Mastodon shows both on a profile, **an open question for the owner**; an item given by id only is left out rather than fetched; a Question,
  Article or other type is left out: "only Notes" for now), each as text cut at 2,000 characters with a link to
  the rest (6.6), its content warning folding it, its pictures counted and not shown. Nothing of the posts is
  stored. **No remote picture is shown** (F6 and 9.4: the picture cache is FWR 5's, and hot-linking would tell
  other servers each member's address and need `img-src` opened); **no Follow is offered**: the server's notice
  says that following is not built because there is no inbox, and that pictures are not shown yet. The limits:
  `limits.lookups` (30 an hour per member) counted before anything is read; a mistyped query and the answers
  that cost no connection (a local member, this server's own state) give most of it back, a lookup that asked
  another server and a query naming an address the fetcher refuses do not; at most 4 lookups at once on the
  server (a fifth is told to try again), one at a time per member; at most 8 requests through the fetcher
  (section 12), 15 s in all. When the member's request ends, the lookup stops waiting and sends nothing more;
  an actor fetch already started is the cache's, shared with other callers, and runs on to its own 15 s and is
  kept (7.4). A member is told one of eight fixed reasons and nothing else: not a handle, refused (only what
  the typed text alone shows, before any DNS lookup: an IP address, localhost, a private suffix, another port),
  not found (every failure once the name was handed to the resolver: no such name, a private address refused
  at connect time, a connection refused, TLS that fails, no answer in time, a status that is not a success,
  answered no sooner than one second after the lookup began, `lookupFloor`, so neither the words nor the time
  tell a public name that resolves to a private address from one that does not resolve), not an account, gone,
  suspended, busy, unavailable.
  **As built after its review:** a handle's name part, in the query, in every WebFinger subject and built from
  an actor, is taken only when `handleNameOK` passes it: after decoding, letters, decimal digits, marks after
  them, and "-", ".", "_", "~" (RFC 7565's userpart narrowed to what servers use: Mastodon's rule in the SWICG
  report, and RFC 7565 4's PRECIS class, which holds no control, format or space character), with no control,
  format, private-use, separator, unseen or variation character (`shownRune`, which a bio and a post go through
  too); anything else is "not an account". The name shown falls back to the proven handle's name, never the raw
  `preferredUsername`; the page shows the name in a `bdi` and the handle as an isolated name and, apart from it,
  the host. A link is offered only on the actor's origin or the proven handle's host (else the actor's or the
  post's id), with the host beside it. The outbox and its page are fetched through the fetcher alone, signed,
  not through the actor cache's turns: what a lookup can cost the inbox is one actor fetch at a time (at most
  two in one lookup, the address of a profile and then its actor, each with its key's document), never the
  posts. The actor fetch counts two requests (the document and, when the actor names its key by id at an
  address of its own, the cache's `followKey`), so the true bound is 8. No remote server's text, status or address
  reaches the member or the log. Staff are answered as anyone else; visitors and suspended members have no
  session; a page of another site cannot start one (POST behind `guard`, JSON only). No database connection or
  lock is held while the network is waited on (`TestDBOneConnection`).
- **Follow requests:** a member can approve followers by hand (`users.locked`); requests come as
  notifications with Approve and Decline.
- **Home timeline** beside Community (F4). Remote posts show the handle (`@bo@social.example`), the
  server's name, and "Open on its server".
- **Replies and likes:** remote replies appear under a local post like local ones, with the handle;
  remote likes count with local ones; notifications for follows, follow requests, mentions, replies,
  likes and shares.
- **Blocks:** a member blocks an account or a domain; blocked content disappears for them, and the
  blocked account cannot follow, reply or like (refused in the inbox). The blocked account is not
  told, but it can notice: its follow ends and a new one is refused (locally with "You cannot follow
  this account.", the same words whoever blocked whom). Blocking a domain also removes the member's
  followers there, as Mastodon's "Hiding an entire server" does.
- **The handle on the profile:** `@ana@flowa.example.org`, with a copy button, when the member
  federates.
- **Privacy defaults:** opt-in off, `discoverable` and `indexable` off, followers and following lists hidden,
  approve-by-hand off, audience Public only after opting in. As built (FRD 6): the lists are hidden from everyone
  at the collection addresses and shown to the member in the app. The counts there are given only while the app
  shows counts (the `community` and `follows` flags on), and count only follows whose other side also federates
  (opted in, not suspended), so a follow by or of a member who never opted in stays inside the app; whether a
  public count should include every local follower is the owner's question under F10 (6.2).
- **A signed-out visitor from another server** who opens `https://flowa.example.org/users/ana/posts/812`
  in a browser sees the post (text, picture, chips as text, the author) in a small page with "Open in
  Flowa"; for a This server post or a non-federating member, a 404 page. In local mode, as today. Built in
  FRD 5 (5.6, "As built").

## 12. Budgets

Measured on the Pi 4 (4 GB) where it says Pi, on the CI runner otherwise; a benchmark's threshold is
the budget, and the first measured value is written into the pull request that adds it.

| Budget | Value | Held by |
| --- | --- | --- |
| Extra memory with federation on, at peak | 32 MB of Go heap with 50,000 queued rows, 4 workers sending and the inbox at its concurrency | `TestFederationMemory` (**new**): `runtime.ReadMemStats` after the load, `HeapInuse` growth under the budget |
| Container memory | `mem_limit: 384m` unchanged | the `federation` CI suite reads `docker stats` after its scenario and fails above 300 MB |
| Inbox request, key cached | under 2 ms CPU on the runner, under 10 ms on the Pi; at most 400 allocations | `BenchmarkInboxVerify` (`-benchmem`; about 35 µs and 56 allocations on the runner, the signature check alone) |
| Inbox request, key not cached | under 5 s wall, bounded by the fetch timeout | `TestInboxRefusals` (**new**) with a slow stub |
| Signing one delivery | under 15 ms on the Pi | `BenchmarkSignDelivery` (about 0.8 ms on the runner; the Pi not measured yet) |
| Claiming a row | under 2 ms with 50,000 queued rows | `BenchmarkClaimDelivery` (**new**, database) |
| Deliveries out | at most 10 a second, 1 at a time per host, 4 workers | `TestDBDeliveryQueue` (**new**) with a stub that counts concurrent requests |
| Database connections held by federation | at most 3 of 8 | `TestDBDeliveryQueue` reads `db.Stats().InUse` during a burst |
| Queue rows | 50,000 in all, 2,000 per host | `TestDBDeliveryBackPressure` (**new**) |
| Inbound queue | 5,000 rows; 503 above | `TestInboxRefusals` |
| A search by handle | at most 8 requests through the fetcher, each with at most 3 redirects (32 requests at most), 15 s in all; 30 an hour per member, one at a time per member, 4 at once on the server, none of the actor cache's turns for the posts | `TestDBResolveHandle` counts the requests each case sends at the stub (4 for a plain handle, 6 for a handle domain apart from its server or a profile page that answers no actor, 5 for a profile address that redirects to its actor and 3 when it is asked again, 13 for a lookup that takes every step with redirects, 8 for the longest way of all: a profile page that answers no actor, its WebFinger, the actor and its key at an address of its own, the reverse and canonical WebFinger, the outbox and its page), and `spend` refuses the ninth; `TestDBLookupRoute` the allowance, `TestDBLookupKeepsKeyTurns` the turns |
| Remote actors cached | 20,000 rows, about 2 KB each | `TestDBRemoteActorCache` and `TestDBRemoteActorBound` (as built in FRD 7: 858 bytes for an actor with a 2048-bit key and short addresses, both rows, indexes apart; at most about 18 KB for one with a 2,048-byte summary and seven addresses of 2,048 bytes; `BenchmarkRemoteActorCached` about 0.8 ms, 6.2 KB and 79 allocations a lookup the cache answers, one query to a local Postgres; `BenchmarkRemoteActorFetch` about 7 ms, 50 KB and 586 allocations a fetch of an expired actor, signing, the local TLS stub and five queries counted, on a 2.1 GHz Xeon) |
| Picture cache | 200 MB on disk | `TestMediaCacheBudget` (**new**) with a 2 MB budget |
| Replay memory | 100,000 signatures, about 6 MB (5.2 to 5.6 MB measured: 52 to 56 bytes each; at most about 7 MB) | `TestReplayExpiry` |
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
   every fixture through the inbox's parser and the actor cache. As built (FRD 7): **none is recorded**;
   this sandbox reaches no fediverse server and `test/federation-record.sh` is not written. The 28 documents
   of `testdata/federation/shapes` (copied as printed) and three of `testdata/federation/actors` (written
   from Mastodon's "Public key" example and the SWICG report's key steps, each row of its README saying from
   where) and Flowa's own golden documents are what `TestFixturesParse` reads, through the decoder and the
   cache's choice of a key and a name, with an outcome written for each; `TestDBRemoteFixtures` fetches each
   actor among them through the cache from a stub. The details the Sources section leaves to a recording
   stay open.
3. **Two Flowa instances.** In Go, `TestTwoInstances` (**new**) starts two apps on `httptest` TLS
   servers with their own databases and runs follow, accept, post, like, reply, edit, delete, block,
   unfollow and account deletion between them. In CI, the `federation` suite (**new**,
   `test/federation.sh`, in `ALL` in `test/ci-plan.sh` and its self-test) runs two containers from
   the built image on a Docker network with `FLOWA_FEDERATION_ALLOW_PRIVATE`, drives them through
   their APIs, and checks each step arrived.
   As built (FRD 8): the search by handle is shown against stubs in Go only. `TestDBResolveHandle` serves
   WebFinger answers, actors and outboxes written from Mastodon's WebFinger page and the SWICG WebFinger report
   (none recorded) through the fetcher's test-only `dialOverride`: forward and reverse lookups, a redirect to
   another host, a handle domain apart from its server, a profile address, an actor whose reverse lookup names
   another actor, finds nothing or names a canonical account that leads elsewhere, and every failure a member
   can meet. The `federated` browser suite cannot reach a stub: its server refuses private addresses and no
   setting may change that (`FLOWA_FEDERATION_ALLOW_PRIVATE` of 5.1 is the two-instance suite's, FWR 8, and is
   not built). So it covers what needs no other server: a handle on its own host (answered from the database),
   queries refused or failing at their first connection (a private address, localhost, another port, a name
   that does not resolve), a visitor, another page's write and a GET; and in the browser, the card, its
   keyboard path, a member found and an address refused, each said to a screen reader, at desktop and 420 px.
   A remote profile on the page is not seen by any browser check until FWR 8's two instances.
   As built (FRD 7), the first step of it in Go: `TestDBTwoFlowaActors` starts
   two federated apps on their own databases; one fetches the actor of a member of the other, signed, through
   the real fetcher, over TLS to the other's real routes, which check the signature with `verifyRequest`
   (requiring one is authorized fetch, F11, not built yet), and stores it. The stub is reached through the
   fetcher's `dialOverride`, a field only a test sets: no setting reaches it, `TestDialOverrideTestOnly` holds
   that `main.go` never assigns or makes one, and every address the override gives still passes the dial
   guard.
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
- As built (FWR 2): a local probe of `/healthz` in a mode that federates gets `federation` with `mode`, `queued`,
  `oldestQueuedSeconds` and `failingHosts` (hosts whose circuit is open, that are unavailable, or whose last request
  failed), and `paused` and `clockWaiting` when true; the numbers are left out when the database did not answer
  them, are asked only for a local probe and kept 5 seconds, and none of it makes the instance unhealthy. The public answer is still `{"ok": true}`, and in local and
  plain single mode the answer is byte for byte as before (`TestDBHealthz`, `TestDBDefaultsUnchanged`). The tile
  of the second point is the Federation tab's "Sending to other servers"; the page per host with a retry button is
  not built.
- As built (FRD 4): the dashboard's mode line says, in a mode that federates, the origin, whether it is
  "not announced yet" (so it can still change) or since when it is frozen (4.3), and what this version
  does (since FRD 5 the sentence of 4.1's "As built (FRD 5)": discovery, profiles and Public posts can be read,
  no inbox, nothing delivered); or why federation is refused. `/healthz` is unchanged until the queues exist.

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
| 10 | `feat(server): webfinger and nodeinfo` | FRD 4 | the first answers to other servers, so federated mode and `FLOWA_FEDERATE` become startable; discovery only |
| 11 | `feat(server): actor documents and public pages` | FRD 5 | content negotiation, picture URLs, retired names (a name is public from here on) |
| 12 | `feat(server): outbox and collections` | FRD 6 | |
| 13 | `feat(server): remote actors by signed fetch, and search by handle` | FRD 7, 8 | fixtures land here; the first remote rows, so `-purge-remote` and the rollback warning too. As built: two pull requests, FRD 8's adding `htmlToText` (planned with FWR 5) and the outbox column (migration 16) |
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
  past it. The README's rollback section says the same. As built (FRD 7, `purgeCommand`): one transaction
  that locks the remote rows, counts what hangs off them (cached actors, posts, comments, likes, comment
  likes, follows, blocks, notifications and reports), deletes them (the rest goes by `ON DELETE CASCADE`; an
  audit line keeps its action with the account emptied, `ON DELETE SET NULL`) and prints the counts; run again
  it removes nothing. It needs only `DATABASE_URL` and works in every mode. Like `-rotate-keys` it does not
  check whether the server runs: the image has no shell, so it runs beside the server with `docker exec`
  before `./deploy.sh --rollback`, and a server of this version fetches no actor by itself. A database backup
  restored into an older build is the same case: purge first. As built (FRD 8): a member's search by handle
  fetches and keeps the actor it finds, so from this version an instance in a mode that federates has remote
  rows once anyone has looked someone up, and the purge before a rollback matters in practice. As built (FWR 5):
  the first version to keep remote posts, which an older build after `./deploy.sh --rollback` shows as local, in
  the Community timeline; the purge removes them, their pictures' rows and the files under `-data/media`. Turn the
  Federation switch off before the purge, so that nothing is stored between it and the rollback.
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
| RFC 3230 Instance Digests | https://datatracker.ietf.org/doc/html/rfc3230 | 12 November 2026, 4.1.1; 13 November 2026, 4.3.2 | digest algorithm names are case-insensitive (4.1.1), `Digest` is a list a recipient may partly ignore (4.3.2) |
| RFC 8941 Structured Field Values for HTTP | https://datatracker.ietf.org/doc/html/rfc8941 | 13 November 2026, sections 1.1, 2, 3.1 to 3.3 and 4 | strict parsing, dictionaries, inner lists, parameters, every bare item, the parsing and serializing algorithms, padding of byte sequences (4.2.7) |
| RFC 8792 Handling Long Lines in Content of Internet-Drafts and RFCs | https://datatracker.ietf.org/doc/html/rfc8792 | 13 November 2026, section 7 | unwrapping the "\" line wrapping of RFC 9421's figures and RFC 9530's examples |
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
| Mastodon: Scaling up your server | https://docs.joinmastodon.org/admin/scaling/ | 14 November 2026 (page last updated 20 July 2026), the Sidekiq section | delivery runs in background workers (the `push` queue), each thread holding a database connection; nothing on retries or their schedule (8.8) |
| Mastodon: Using the admin CLI | https://docs.joinmastodon.org/admin/tootctl/ | 14 November 2026 (page last updated 15 April 2026), `accounts cull`, `domains purge` and `domains crawl` | an unreachable domain is reported and its accounts kept; nothing on delivery retries or unavailable domains |
| Mastodon: Troubleshooting errors | https://docs.joinmastodon.org/admin/troubleshooting/ | 14 November 2026 (page last updated 21 November 2025) | nothing on delivery failures |

Not opened: Mastodon's source code (github.com cannot be reached from here except for this
repository), FEP-2c59, FEP-8b32 and FEP-e2ce (codeberg.org is not reachable from here), RFC 7565 (the
`acct` scheme; the SWICG WebFinger report quotes its grammar), and any other server's documentation.

**Still unverified, and what would settle each:**

- Whether Mastodon answers its profile page address (`https://mastodon.social/@Gargron`) with the actor when
  asked for Activity Streams, and with which id, and whether its WebFinger answers that address as a resource
  (11, as built in FRD 8, tries the first and falls back to the second): its pages do not say. Settled by a
  recording or the owner's trial.

- The instance actor's WebFinger form, `acct:flowa.example.org@flowa.example.org` (5.3): not in
  Mastodon's documentation. Settled by Mastodon's WebFinger controller or by a WebFinger answer
  recorded from a Mastodon server for its own host.
- Which property a current Mastodon writes alt text in, `name` or `summary` (6.5): its page names
  only `summary`. Settled by a recorded Mastodon post with alt text (FRD's fixtures). FRD 7, whose item the
  recorded fixtures belong to, could record nothing from here (section 13, as built), so this and the two
  below that wait for a recording stay open.
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
- Mastodon's delivery retry schedule and horizon (F9), which statuses stop its retries, and when it marks a domain
  unavailable (8.3, 8.4, 8.8): the pages read for FWR 2 (ActivityPub, Security, Scaling, the admin CLI,
  Troubleshooting) say nothing of them; only the Security page's 400 and 401 fallback to RFC 9421 (7.1) is relied
  on, with ActivityPub 7.1's "retry delivery to recipients if it fails due to network error". Flowa's schedule, its 7
  days and its statuses are its own, written in 8.3 and 8.4. Settled by Mastodon's delivery worker and its failure
  tracker, or by the owner's trial.
- How Mastodon stores private keys (F7), whether it sends a
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
