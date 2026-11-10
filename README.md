# Flowa

A progress tracker for flow arts props, on port 3169. Five props have researched skill maps:

| Prop | Skills | Branches | Unconfirmed | Catalog |
| --- | --- | --- | --- | --- |
| Nunchaku | 109 | 11 | 15 | `web/catalog/nunchaku.json` |
| Poi | 90 | 10 | 3 | `web/catalog/poi.json` |
| Staff | 84 | 9 | 4 | `web/catalog/staff.json` |
| Double staff | 72 | 9 | 6 | `web/catalog/doublestaff.json` |
| Buugeng | 56 | 7 | 7 | `web/catalog/buugeng.json` |

Catalogs follow [docs/catalog-format.md](docs/catalog-format.md) and are checked with
`node test/catalog.js web/catalog/*.json`. The files in `web/catalog` seed the catalogs; once running,
Flowa serves each prop's latest published revision from Postgres, and admins correct maps in the
skill map editor without a redeploy (see [Moderation, roles and the skill map editor](#moderation-roles-and-the-skill-map-editor)). Names and definitions come from the sources listed
on each skill; prerequisites, tiers and plans are coaching proposals. Skills whose name or
mechanics no opened source confirms are marked unconfirmed, and guides say so when a move is
named but not described.

- **Accounts:** sign up with a username and password (no email). Each user picks their props
  and has their own progress. Three roles: member, moderator and admin.
- **Without an account:** browse the skill maps, guides, demos and references.
- **Dashboard, Practice, Combos, Plan:** per user and per prop. With `suggest` on, the combo builder
  suggests what can follow the last trick, from how the moves travel, with a bridge when nothing links
  directly ([docs/suggestions.md](docs/suggestions.md)).
- **Skill maps:** one per prop, with dependency graphs. Progress is tracked per hand or per direction where the skill calls for it.
- **Avatars:** with `avatars` on, members build an avatar (face, hair, outfit, accessories, prop colours
  and a body preset) that plays their demos and combos, shows on their profile, and appears on badge,
  milestone and streak stickers ([docs/avatars.md](docs/avatars.md)).
- **Profiles:** a picture, a bio and Instagram, TikTok and YouTube links, shown on a public profile page
  (`#/u/<username>`) with the user's level, streak, badges and posts.
- **Timeline:** posts with a picture, tagged tricks from any of your props and an attached combo, likes,
  comments with replies (one level of threading, folded past the two newest, with likes and a 15-minute
  edit window; a deleted comment with replies stays as "Comment deleted") and @mentions; new badges post themselves as milestones (each user can turn that off). A people
  list ranks everyone by XP. The composer's picker searches every skill map and your combos, and suggests
  the tricks from today's session and this week's level-ups; a combo's Publish button brings it to the composer.
- **Link cards:** a pasted Instagram post or reel, TikTok video or YouTube video or short is offered as a card
  (up to 4 per post). The server fetches the title, author and thumbnail from the site's public oEmbed or page
  tags and serves its own small JPEG of the thumbnail, so visitors never contact those sites until they open
  a link. YouTube can play in place, from youtube-nocookie.com, when an admin turns that on. See
  [Link cards](#link-cards).
- **Notifications:** a bell in the header for likes, comments, replies, comment likes, mentions and new
  badges (a reply opens its thread, scrolled to it), plus a warning
  when today's practice would end a streak.
- **Gamification:** XP and levels, a practice streak across props, weekly challenges, achievements, and
  XP on the phases of the Plan. See [Gamification](#gamification).

Search with `Ctrl K`, `/` or the Search button. It covers the pages and the skills of every prop you chose
(every prop for a visitor), says so under the results, and names the prop beside each skill when there is
more than one. Staff also find Moderation, Admin and each admin section they may open. A visitor's menu
and search list what opens without an account (Welcome, Skill maps, Sign in, Settings); a page that
needs an account says what signing in is for. The landing page plays one demo, the prop's signature
move, and shows the skill map in small.

On a touch screen (`pointer: coarse`) every control is at least 44 x 44 px, by its smallest size and its
padding; with a mouse the layouts are unchanged. The words follow: a touch screen is told about the Search
button, not `Ctrl K`. `test/browser/touch.py` measures every control on each page at 390 px wide with a touch
pointer and checks that no page scrolls sideways, the avatar editor on every tab at 360 px too.

The UI is plain HTML, CSS and JS with no CDN. Two parts have a build step: the guided tour (`tour/`) and the
3D figure (`figure/`).

**What comes next** is in [docs/roadmap.md](docs/roadmap.md): replies, moderators and a skill map editor,
link cards for Instagram, TikTok and YouTube, next-trick suggestions from the geometry of moves, and
customisable avatars. Each epic is described in [docs/epics.md](docs/epics.md) and tracked in a checklist
under `docs/epic-<code>/todo.md`.

## Run

```sh
./deploy.sh                 # runs the Go tests, builds the binary, rebuilds the image, recreates the containers
./deploy.sh --rollback      # back to the image that ran before the last deploy
./backup.sh                 # a full database dump to data/backups (see Data)
docker logs nunchaku
```

The image holds the binary and nothing else, so the root certificates that link previews need are
compiled into the binary (Mozilla's bundle, pinned in `go.mod`; update it with
`go get golang.org/x/crypto/x509roots/fallback@latest`). The log says `TLS roots: present` at start.

`deploy.sh` keeps the image that was running as `nunchaku:previous` and goes back to it by itself if
the new container does not become healthy within 90 seconds. Both containers have CPU, memory and
process limits in `compose.yml`, so the app cannot starve the printer stack on the same Pi.

Healthy means `GET /healthz` answers 200: the database is reachable and its schema is not older than
the build needs; otherwise it answers 503. Anyone may ask and is told only that, as `{"ok":true}`. A
probe from the machine the app runs on (in Docker: from inside the container) also reads the schema
version, the number of migrations the build knows, whether TLS roots are present, and `VERSION`:

```sh
docker exec nunchaku /nunchaku -healthcheck
# {"ok":true,"database":true,"schema":5,"migrations":5,"tlsRoots":true,"version":"main.abc1234"}
```

That is the probe the image's health check runs and the one `deploy.sh` waits on. The database is asked
at most once a second, however many probes arrive.

Two containers (`compose.yml`): `nunchaku` (the app) and `nunchaku-db` (Postgres 16, data in the
`pgdata` Docker volume, not exposed outside Docker). Secrets are in `.env` (mode 600):
the database password, and the admin name and password used to create the admin on first start.
Changing the admin password in `.env` later does nothing; change it on the Profile page. Once the
admin exists, remove `ADMIN_PASSWORD` from `.env`: it is otherwise kept in the container's
environment, where `docker inspect` shows it.

Port 3169 is published on the machine itself only (`127.0.0.1`), so after a first `docker compose up`
nothing else on the network can open the app: a reverse proxy on the same machine can, and so can
`curl http://127.0.0.1:3169/healthz`. That is the default because a port Docker publishes is opened
ahead of the host's firewall (ufw and firewalld do not see it), so publishing on every address would
put the app on every network the machine is on, whatever the firewall says. To open it to other
devices, set `FLOWA_BIND` in `.env` and run `./deploy.sh` (or `docker compose up -d`):

```sh
FLOWA_BIND=192.168.1.20    # one address of the machine: only that network reaches the app
FLOWA_BIND=0.0.0.0         # every IPv4 address the machine has
```

A reverse proxy that runs in another container on the same machine reaches the app through the
Docker bridge, not through loopback: give `FLOWA_BIND` the bridge's address (`172.17.0.1` for the
default bridge). A value that is not an address stops `docker compose` with `invalid ip address`.
`deploy.sh` ends by saying where the port is published, and says so when it is this machine only.
The database is never published.

### HTTPS

`compose.yml` has a reverse proxy behind a profile, for opening Flowa to people outside the house:
[Caddy](https://caddyserver.com), which gets a certificate for your name by itself, renews it, sends
plain HTTP on to HTTPS and tells browsers to keep using HTTPS for a year (HSTS). The app's own port
stays on the machine. From a machine that has Docker with the compose plugin, Go and Node, and
nothing else:

1. Point a DNS record for a name you own (say `flowa.example.org`) at the machine, and let ports 80
   and 443 reach it from outside (on a home router: forward both, and 443 over UDP too for HTTP/3).
   The certificate comes from Let's Encrypt, which has to reach the machine under that name.
2. Get the code and the settings:

   ```sh
   git clone https://github.com/TuanSSM/flowtopia.git && cd flowtopia
   cp .env.example .env && chmod 600 .env
   ```

3. In `.env`, choose `POSTGRES_PASSWORD` and `ADMIN_PASSWORD`, and add the name and the profile:

   ```sh
   FLOWA_DOMAIN=flowa.example.org
   COMPOSE_PROFILES=proxy
   ```

4. `./deploy.sh`. It ends with `proxy: https://flowa.example.org`; a first certificate can take a
   minute, and `docker logs nunchaku-proxy` says how it went.
5. Open `https://flowa.example.org`, sign in as the admin, then remove `ADMIN_PASSWORD` from `.env`.

What the two settings do: the proxy (`nunchaku-proxy`, configured by the `Caddyfile`) is started and
publishes 80 and 443; the app answers to the name without listing it in `FLOWA_HOSTS`; and the app
believes what a proxy says about a visitor (their address, and that they came over HTTPS) from the
proxy's own network alone, `10.169.31.0/29`, where before it believed any Docker network and the
machine itself. That is why the session cookie is marked Secure and a write from an `http://` page is
refused, and why a visitor cannot pick their address by sending `X-Forwarded-For` or `X-Real-IP`.
If you set `FLOWA_TRUSTED_PROXIES` yourself, keep that range in it, or the proxy is not believed. If
the range is taken on your machine (Docker says `Pool overlaps`), move it with `FLOWA_PROXY_NET`.

A name that only resolves at home (`flowa.home.arpa`, `raspi.local`) works too: Caddy then signs the
certificate with an authority of its own, which each device has to be told to trust once
(`docker compose cp proxy:/data/caddy/pki/authorities/local/root.crt .`). Without `FLOWA_DOMAIN` the
proxy stops and says so in its log. Certificates and their keys live in the `caddy_data` volume.

### Pinned images

Postgres and Caddy are named in `compose.yml` by version and digest
(`postgres:16.15-alpine@sha256:...`), so a deploy starts the bytes that were tested and never a newer
image by itself. Dependabot looks once a week (`.github/dependabot.yml`) and opens a pull request when
a version or a digest has moved. That pull request runs every integration suite with the database
started from the image it names, because the checks read the image from `compose.yml`
(`test/db-image.sh`), and so does `test/browser/run.sh` on the Pi. To take an update: merge the pull
request, pull, `./deploy.sh`. To do it by hand:

```sh
docker buildx imagetools inspect postgres:16-alpine | grep -m1 Digest   # the digest of the newest 16
# write the version (docker run --rm postgres:16-alpine postgres --version) and that digest into
# compose.yml, open a pull request, deploy after it merges
```

A minor version of Postgres (16.15 to 16.16) starts on the same data. A major one (16 to 17) does
not: `test/db-image.sh` refuses it and Dependabot does not offer it, because that move is a full
dump with `./backup.sh`, a new empty volume, and a restore.

The app answers only to IP addresses, `localhost`, single-label names such as `raspi`, and private
names (`.local`, `.lan`, `.home`, `.home.arpa`, `.internal`, `.localdomain`, `.ts.net`). To reach it
by any other name, list it in `FLOWA_HOSTS` in `.env` (comma-separated). This stops a website from
pointing its own name at the Pi and talking to Flowa from a visitor's browser (DNS rebinding).

`FLOWA_HOME_URL` in `.env` is optional: an `http` or `https` address of the start page of the machine
Flowa runs on, like `http://raspi.local:8080/`. Admins then get a "Server home" link in the Staff
group of the menu; nobody else is told the address. It is empty by default, which means no link, and
a value that is not such an address stops the server at start with a message that says so.

Behind a reverse proxy of your own instead (nginx, Traefik, a Cloudflare tunnel), every connection comes from the
proxy, so the sign-in and sign-up limits need each visitor's own address. Flowa reads it from
`X-Forwarded-For` (or `X-Real-IP`), but only when the connection comes from a trusted proxy, and it reads
the header from the right, so a visitor cannot choose their address by sending it. Trusted by default:
loopback and Docker networks (`127.0.0.0/8`, `::1`, `172.16.0.0/12`), which covers a proxy on the same
host or in Docker. Set `FLOWA_TRUSTED_PROXIES` in `.env` (addresses or ranges, comma-separated) for any
other setup: for example add Cloudflare's ranges when Cloudflare sits in front of your proxy, or `none`
when visitors connect directly. The proxy must send `X-Forwarded-For` (Traefik, Caddy and Cloudflare do;
in nginx, `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`). A proxy that ends TLS should
also send `X-Forwarded-Proto: https` (the same three do; in nginx,
`proxy_set_header X-Forwarded-Proto $scheme;`): Flowa then marks its cookie Secure and refuses a write
that an `http://` page tries to make to the HTTPS site.

Admin, Overview shows the running version, from `VERSION` in the container's environment.
`deploy.sh` sets it to `main.<short commit hash>` of the checkout it deploys (the Pi is the staging
host); production passes the release tag, such as `v1.2.0`. Unset, it reads `dev`.

## Data

- Postgres holds users, sessions, progress (one JSON document per user and prop), flags, posts,
  likes, comments, notifications, achievements, reports, the audit log and catalog revisions.
- Passwords are stored as PBKDF2-SHA256 hashes with 600,000 rounds; the count is stored in each hash,
  so an older hash of 200,000 rounds still checks and is replaced the next time its owner signs in.
  One hash takes about 1.7 s on the Pi, and the server works out at most two at a time. A password is
  8 characters or more, is not the username, and is not one of the 1,000 most used passwords of that
  length (the UK NCSC list, embedded in `main.go`, compared without regard to case). Session tokens
  are stored hashed.
- `users.last_seen` is the last time a person was seen, written at sign-in and at most hourly after
  that. The accounts page and the dashboard read it, so signing someone out (a suspension, a password
  reset) does not make them read "Never".
- `data/backups/progress-DATE.json` is written by the app before the first change each day, kept
  14 days. It holds progress documents only.
- `./backup.sh` writes a full, compressed dump of the database (accounts, posts, comments, progress,
  catalogs, everything) to `data/backups/flowa-DATE.dump`, checks it can be read back, and keeps 14.
  Run it daily from cron: `17 3 * * * /home/pi/nunchaku/backup.sh >> /home/pi/nunchaku/data/backups/backup.log 2>&1`.
  Restore into an empty database with the app stopped:
  `docker exec -i nunchaku-db pg_restore -U flowa -d flowa --clean --if-exists < data/backups/flowa-DATE.dump`
- The schema changes through numbered migrations (`migrations` in `main.go`, recorded in
  `schema_migrations`), each applied once at start. An hourly job removes expired or unused
  sessions, notifications read more than 180 days ago, and link previews no post uses.

The app itself is plain HTTP, so on its own port passwords cross the network unencrypted: keep that
port to the machine or the LAN, and open Flowa to anyone else through the proxy profile
([HTTPS](#https)), which encrypts every connection. Pages are served with a
Content-Security-Policy of `'self'` only (plus youtube-nocookie.com frames while `videoEmbeds` is on); the only
outbound requests the server makes are link previews to Instagram, TikTok and YouTube. Every response
forbids framing by other sites and MIME sniffing and sends no referrer.

Limits: at most two password hashes run at once; failed sign-ins are counted per address and username
(8 in 10 minutes), per address (30) and per account (20); 10 sign-ups per address per hour; 5 wrong
current passwords per member in 10 minutes; and per member per hour, 30 posts, 120 comments, 600 likes,
60 badge updates and 120 suggestion updates. A post or comment notifies at most 10 mentioned people.
What is counted is what costs something. A sign-up form the server turns down is free and a taken name
costs a tenth of a sign-up; a post, comment or like the server turns down costs a tenth of one that goes
through. An IPv6 visitor is counted by their /64, which is what one home or phone gets, not by each of
its addresses. An account with 20 wrong guesses against it is not locked: a wrong password is told to
wait, the right one signs in and clears the count, so nobody can lock a member or an admin out by
guessing. Each limiter remembers at most 20 000 visitors and forgets the oldest when full.

Refusals are logged, one line per kind and visitor per minute and at most 120 lines a minute, such as
`refused: too many sign-in attempts from 203.0.113.7` or `refused: too many posts by member 12`. A line
never holds a username or a password. `docker logs nunchaku 2>&1 | grep refused:` shows them.

Every request also gets one line of JSON in the same log, with the method, the route, the status, the
time it took and the bytes answered:

```
{"time":"2026-11-10T19:05:24.123Z","method":"POST","route":"/api/posts/{id}/comments","status":200,"ms":12.4,"bytes":310}
```

The route is the pattern the request matched, never the address asked for, so a line holds no id, no
name and no query string, and no visitor's address or member either. The one exception is a request a
limit or the sign-in refused: its line also carries `refused` with the same words as the refusal line
above. Two kinds of request are left out because their lines would say nothing: a `/healthz` probe
answered 200 (Docker sends one every half minute) and a page file served without an error. At most 240
request lines are written in a minute; what came after is counted in one line
(`{"time":...,"unlisted":1234,"since":...}`). `docker logs nunchaku 2>&1 | grep '^{' | jq 'select(.status >= 500)'`
shows the requests that failed. The app keeps 10 log files of 10 MB, which is a day of the most both
kinds of line can write, and months of ordinary use.
Pictures are decoded and encoded again on upload, which keeps only the pixels (no camera location),
and other people's pictures are fetched by URL rather than carried in every list. Sessions end after
30 days, or after 14 days unused; the profile can sign out every other device. The review behind these
is [docs/hardening-plan.md](docs/hardening-plan.md).

## Feature flags

Several features are behind flags, stored in Postgres and changed at runtime by an admin
on the Admin page. No restart is needed. Every flag is on by default; an admin turns off what gets in the
way, and a flag an admin has set keeps that setting. A flag that is off turns off the feature's API as
well as its pages: with `community` off the timeline, comments, likes, people and notifications answer
404, and with `game` off so do badges. `signups` opens or closes sign-up, `tour` runs the
guided tour, `community` is the timeline, public profiles, people list and notifications, `game` is XP,
levels, streak sharing, challenges, achievements and XP on the Plan's phases, `moderation` is reports, the queue, hiding,
mutes and suspensions, `catalogEditor` is the skill map editor, `linkCards` turns Instagram, TikTok and
YouTube links into cards, `videoEmbeds` plays YouTube in place (it adds youtube-nocookie.com frames to
the page security policy), `suggest` is next-trick suggestions, and `avatars` is avatars, body presets
and stickers.

| Flag | What it adds to a skill's panel |
| --- | --- |
| `details` | Steps, what to watch for, a drill and a foam warning (from each prop's catalog) |
| `demos` | Demos on a jointed figure and labelled pictures, with 3/4, front and side views, hand and speed controls, and GIF download (`web/demo.js`, clips in `web/clips`) |
| `references` | Source pages and videos |


## Gamification

Everything is worked out in the browser from the progress documents (`web/game.js`); the server
only records which achievements were unlocked and when, and the level, XP, streak and totals each
user reports for their profile and the people list (the community section of `main.go`).

- **XP.** A skill variant earns 10, 40, 100 or 200 XP at levels 1 to 4. A session is 10 XP plus one
  per minute up to 120. A phase gate is 150 XP and a track week 40. A ready combo is 50 XP and each
  clean run 15, up to 20 runs. Each weekly challenge done is 40 XP. XP from every prop adds up to one
  level: reaching level n takes 50 * (n - 1) * n XP, with titles from Beginner to Legend.
- **Streak.** Consecutive days with a logged session on any prop; the dashboard, Achievements page and
  bell say when today's session is still missing. **Share** renders a transparent PNG overlay
  (story 1080 by 1920 or square 1080) with the flame, the count, the week and the user's name, in one
  of five templates (classic, minimal, card for busy photos, bold, week) placed at the top, middle or
  bottom, to lay over one's own photo or video in Instagram, TikTok or any other app; it can be downloaded,
  shared through the Web Share API where the browser offers it, or posted to the timeline with a
  background.
- **Weekly challenges.** Monday to Sunday, across props: practise on 4 days, log 120 minutes, raise
  3 skill levels, level up a weak-hand skill.
- **Achievements.** Badges for sessions, hours, streaks, clean skills, both hands, gates, combos and
  the community, plus one per branch and one per roadmap for each of the user's props. New badges show
  an unlock card, a notification and one milestone post per batch.
- **Plan with XP.** Each phase shows its skills as chips, the skill XP earned of what it holds, and
  the XP its gate pays; the page opens with the prop's XP, the week's challenges and a focus skill.

## Community API

All of these need a signed-in user. `GET /api/users` (people, most XP first), `GET /api/users/{name}`
(profile with achievements), `GET /api/posts?before=&user=`, `POST /api/posts` (text, picture, up to 10 `tags` of prop and skill id, one `combo`, up to 4 `links`), `GET|DELETE
/api/posts/{id}`, `POST /api/posts/{id}/like` (toggle), `GET /api/posts/{id}/comments?after=` (20 top-level
comments, each with its two newest replies), `POST /api/posts/{id}/comments` (with `parentId` for a reply),
`PUT|DELETE /api/comments/{id}`, `GET /api/comments/{id}/replies?after=` (50 at a time),
`GET /api/comments/{id}/thread`, `POST /api/comments/{id}/like` (toggle), `POST /api/achievements`, `GET /api/notifications`,
`PUT /api/notifications/read`. Profile fields (`bio`, `links`, `prefs`, `stats`) go through
`PUT /api/me`; links are stored as handles, whether a handle or a URL was pasted. `PUT /api/me` writes
only the fields, links and preferences that are in the body. With `rev` (from the user in `GET /api/me`)
a save made on an older copy of the profile is answered 409 with the current user, and the About form
keeps what was typed, shows what changed elsewhere and saves on top at the next press.

`POST /api/posts` and `POST /api/posts/{id}/comments` take an `Idempotency-Key` header. A second request
from the same member with the same key within 10 minutes gets the first answer again, with
`Idempotency-Replayed: true`, and makes nothing new; the page sends one per draft, so a tap repeated
after a lost answer does not post twice.

Progress is one document per prop, saved with the `rev` it was read at. When another device saved in
between, the page merges the two against the copy they both started from (`web/merge.js`: per skill,
session, combo and setting; the same field changed on both sides keeps this device's value) and saves
again. `node test/merge.js` checks the merge, and `node test/links.js` checks that the page reads a
pasted link the way the server does, on the fixtures the Go test uses (`test/links-fixtures.json`).

## Link cards

`parseLink` in `main.go` recognises Instagram posts and reels (also after an account name), TikTok videos and
short links (`vm.`, `vt.`, `/t/`), and YouTube videos, shorts, live and `youtu.be` links, and stores each in a
canonical form without tracking parameters. Anything else is refused.

Previews are fetched in the background after a post is made, so posting never waits for another site. The
fetcher only talks to those sites and their image hosts, follows redirects only between them, refuses private
and loopback addresses, gives up after 3 s, and reads at most 256 KB of an answer and 2 MB of a picture. Short
links are followed to their video. A preview is fetched once per link and refreshed at most weekly; a failure
leaves a plain card with the site's name and the link. Instagram answers without a login only sometimes, so
its cards may have no title or picture. Thumbnails are scaled to 640 px and re-encoded as JPEG by the server,
and `/api/previews/<id>.jpg` serves one only while a post the viewer may see links to it.

With `videoEmbeds` on, YouTube cards get a play button that swaps in a frame from
`https://www.youtube-nocookie.com`; that host is added as `frame-src` to the Content-Security-Policy only while
the flag is on (`csp()` in `main.go`). Instagram and TikTok always open in a new tab: their embeds need their
own scripts, which the policy does not allow.

Tests never reach the real sites: `FLOWA_PREVIEW_BASE` points the fetcher at the stand-in in
`test/browser/stub.py` (Go tests use an `httptest` server), and `FLOWA_PREVIEW_FETCH=off` turns fetching off.
Neither is set in production.

## Moderation, roles and the skill map editor

Handlers ask for permissions, never roles; the table is `rolePerms` in `main.go`, and `GET /api/me`
returns the signed-in user's permissions so the interface shows only what the server allows.

| Role | May |
| --- | --- |
| Member | Post, comment, like, report |
| Moderator | Also: delete and hide others' content, mute and suspend members, work the report queue, revoke badges and hide reported stats, see the dashboard and accounts, draft skill map changes |
| Admin | Also: change roles, feature flags, reset passwords, delete accounts, read the audit log, publish and restore skill maps |

Nobody acts on someone of their own role or higher, or on themself, and Flowa always keeps one admin.
That covers deleting someone's posts and comments: a moderator cannot delete an admin's or another
moderator's. No admin can reset another admin's password or delete their account, and an admin changes
their own password on the Profile page, with the current one.

One admin is the **owner**: the first admin, marked at start if nobody is (`users.owner`; the database
allows one, and only on an admin). Any admin may make someone a moderator or an admin, but only the
owner changes an admin's role, so one admin cannot demote another and then delete them. The owner
cannot be demoted or deleted, and hands ownership to another admin from Admin > Accounts
(`PUT /api/admin/users/{id}/owner`), which is audited and tells the new owner. A role chosen on the
accounts page is asked about first ("Make @name a moderator?") and sent on Apply.

- **Reports** (behind `moderation`): on a post, comment or profile, with a reason (spam, harassment, unsafe
  advice, not about flow arts, other) and a note; one open report per person and target, ten a day.
- **The queue** groups open reports by what they are about, with the content, its author, their status
  and earlier actions. Moderators hide or restore (with a reason the author sees), mute (no posting or
  commenting) or suspend (signed out, no sign-in) until a chosen time within a year, then resolve or
  dismiss; the reporters are thanked. Hidden content is filtered in every query and seen only by its
  author and moderators.
- **Audit log:** every moderation and admin action (including flags, roles, account deletion, password
  resets and catalog publishing) is written by one helper in the same transaction, and admins can filter it.
- **Overview** (the first admin tab): members, people active in 7 and 30 days, open reports, and 12 weeks of practice sessions,
  posts, comments and reports as small charts with a table view.
- **Skill map editor** (the Catalogs tab, behind `catalogEditor`): edit skills, branches and phases, or the whole catalog as
  JSON, in a draft. Every field shows the validator's messages as you type (`web/catalog-check.js`; the
  server runs the same rules in Go, `validateCatalog`, and both must agree on `test/catalog-fixtures`).
  The review lists each change and how many people have progress on it. Publishing (admins) makes a new
  revision live for everyone; earlier revisions can be exported as catalog files or restored.
  Ids are never reused: renaming a published skill keeps its old id as an alias, and progress moves to the
  new id when it next loads; retired skills leave the maps but keep their progress. Publishing or restoring
  anything that would leave progress with no skill to belong to is refused.

Admin API (permission in brackets): `PUT /api/admin/users/{id}/role` (user.role),
`PUT /api/admin/users/{id}/owner` (the owner only), `GET /api/admin/audit`
(audit.read), `GET /api/admin/dashboard` (dashboard.read), `POST /api/reports`, `GET /api/mod/queue`,
`POST /api/mod/resolve` (report.resolve), `POST /api/mod/actions` (the action's own permission),
`GET|PUT|DELETE /api/admin/catalog/{prop}[/draft]`, `GET .../diff`, `POST .../publish` (catalog.publish),
`POST .../shipped`, `GET .../revisions/{rev}`, `POST .../revisions/{rev}/restore` (catalog.publish).

## Guided tour

New users get a guided tour built with [React Joyride](https://react-joyride.com): one after
choosing props, and a short one on the first visit to each section. It is behind the `tour`
flag; when the flag is off, a short note is shown instead. Finished tours are remembered on
the account, and Profile has a "Replay the tour" button. A tour belongs to its section: going Back or
following a link out of the section closes it and counts it as seen, so it never sits over the next page.

Until a new member has chosen their props the menu lists only what opens: their profile, the chooser and
Settings.

The rest of the UI is plain JS, so the tour is a separate React bundle, `web/tour.js`, loaded
only when a tour starts. Its source is `tour/src/tour.jsx`, and the steps are in `TOURS` in
`web/account.js`. `./deploy.sh` rebuilds the bundle when `tour/node_modules` exists
(`cd tour && npm install` once).

## Demos

104 of the 411 skills have a demo, on all five props: 168 clips and pictures. See
[docs/demo-coverage.md](docs/demo-coverage.md) for the list and for why the rest have none, and
[docs/demo-engine.md](docs/demo-engine.md) for how they are made and where they are weak.

The motion is worked out at build time (`sim/`, run by `./deploy.sh` through `node sim/bake.js`)
and stored in `web/clips`. The browser (`web/demo.js`) only plays it, on a jointed figure.

- **Simulated:** nunchaku and poi moved by gravity, drag, cord or tether tension and contact with
  the body, including throws, a bounce, and swings carried by a body that shifts or bends.
- **Solved:** poi flowers. The head follows its defined path and the hand is placed exactly where
  gravity and tether tension produce it.
- **Hand-driven:** staff, double staff and buugeng, tracing the path the definition gives.
- **Rolling:** nunchaku rolls. The hand lets go and the nunchaku turns about the hand, wrist,
  finger, forearm or neck as a balanced rotor, at half the rate of the swing, until a stick is caught.
- **Pictures:** labelled stills for grips, lengths and planes.

`node test/physics.js` checks every clip against its physics, its definition (`test/expect.js`)
and the body: reach, limbs clear of the body and each other, hand speed, legs, feet and balance.
Contact details are in [docs/contact-review.md](docs/contact-review.md).

Not modelled: wraps and rolls, rolling contact, a wrist or fingers, poi weaves and turns.

The figure is a rigged 3D model (`web/assets/human.glb`, drawn by `web/figure3d.js`, built from
`figure/` with three.js). It is posed from the same joints as the drawn figure and skinned on the
CPU, so it does not need float textures. The model is the "X Bot" mannequin from the three.js
examples (a Mixamo character); any model with a Mixamo skeleton can replace the file. The `model3d`
feature flag turns it off, and the drawn figure is used wherever WebGL is missing. Close-up grip
pictures stand the model behind the picture and cut it away beyond the forearm, so the hand shown
is the model's, with its thumb and fingers round the prop.

The model is posed as a body moves, not only placed: the upper arm and thigh roll so that the elbow
and knee are hinges that bend toward the forearm and shin (the knee points where the toes point);
the forearm pronates or supinates to follow the palm, within about 100 degrees, and only the rest is
left to the wrist; the collarbone lifts as the hand goes up; the head stays nearer upright than a
leaning trunk; the fingers close less tightly round a thick handle.

The Combos page plays the open combo on a stage beside its timeline. Each skill plays whole cycles
of its demo, as many as its repetition count (1 to 9, saved with the combo); skills can be moved
earlier; a skill with no demo shows as a name card; the links between skills are not animated.

What the sources say about each nunchaku skill that had no demo is in
[docs/nunchaku-research.json](docs/nunchaku-research.json) (8 fully described, 44 partly, 38 not at all).
Browser tests live in `test/browser` (`test/browser/run.sh combo.py`); they use a throwaway database.

## Avatars

The avatar editor (`#/avatar`, `web/avatar.js`) has a live 3D preview that turns with a drag and a tab
each for face, hair, outfit, accessories, props and body; "Surprise me" picks a random avatar and Undo
steps back. New members are offered it, with a random avatar, right after choosing their props, with one
pair of buttons: Keep this avatar, or Skip for now. On a phone the rows of colours and options wrap. A
picture of the avatar's head can become the profile picture. On someone's profile, their avatar plays a
skill they have marked Clean.

Parts are made in code in the figure bundle, with no model files or textures: outfits colour the body
per vertex, and head parts and wristbands are fixed to a bone; the head is measured from the mesh, so
face parts and glasses sit on the skin. A frame (neutral, feminine or masculine) shapes the trunk, the
arms and, for the feminine frame, a bust. Body presets (3 heights by 3 builds) are checked like any clip: `node test/physics.js` runs the body checks on every clip for every preset,
`node sim/bake.js` records which clips fail where in `web/clips/presets.json`, and such a clip plays on
the standard body with a note. `node test/presets.js`, `node test/stickers.js`,
`node test/avatar-colors.js` and `node test/bundle-size.js` cover shapes across presets, sticker poses,
palette contrast and download size; `node test/body3d.js` poses the 3D figure without a browser and
checks that no prop is drawn into the body on any clip and that every avatar part shows; `test/browser/avatar.py`
covers the pages, parts and the frame budget. Details, sources for the preset sizes, and licences: [docs/avatars.md](docs/avatars.md).

## Pull requests

`main` is protected: changes arrive by pull request, and `.github/workflows/process_pr.yml`
runs on every one so the checks do not have to run on the Pi. Its jobs:

| Job | What it runs |
| --- | --- |
| `changes` | checks that the PR title is a Conventional Commits title; turns the changed files into a plan with `test/ci-plan.sh`: which of `lint`, `unit tests` and `screenshots` run, and which integration suites. A PR of Markdown, `docs/`, `.claude/` or `.gitignore` runs just `docs`; a change to the release or deploy files runs `lint`; a change to `compose.yml` runs `lint` and every integration suite, since it names the Postgres they all start; a change to the `Caddyfile` or the `Dockerfile` runs `lint` and the `compose` suite; a change to one browser test runs that suite; a change to `main.go`, to `process_pr.yml`, or to a path no rule knows runs everything |
| `docs` | `test/docs.sh`: Markdown links point at files that exist; `.claude` agents, skills and rules have their front matter |
| `lint` | `gofmt -l`, `go vet`, staticcheck; `node --check` on every script; `test/catalog.js`; `test/badges.js`, `test/links.js`, `test/merge.js` and `test/flags.js`; Python syntax; `test/sleeps.sh` (a browser test's sleep says why its time is fixed); `test/compose.sh` (what `compose.yml` resolves to: the app on loopback unless `FLOWA_BIND` is set, never the database, and with `FLOWA_DOMAIN` the name answered and only the proxy's network believed); shellcheck; actionlint; and a preview of the next release's version and notes (git-cliff) in the job summary |
| `unit tests` | `go build`, `go test -race`, `node test/physics.js` (every clip), and that `web/tour.js` and `web/figure3d.js` are the bundles their sources build |
| `integration (<suite>)` | one job per suite, in parallel, each with its own Postgres service, started from the image `compose.yml` pins: `db` (the Go tests that need Postgres), `compose` (`test/compose.sh --up`: the two containers of `compose.yml` built and started on the runner, the app asked by loopback, where it must answer, and by the runner's other addresses, where it must not until `FLOWA_BIND=0.0.0.0`; then the proxy profile for a private name, asked over HTTPS for its certificate, the way from HTTP, HSTS, the Secure cookie, a write from an HTTP page and an address the visitor made up) and each `test/browser` script driven through headless Firefox, plus `demos` and `combo` again with avatars on. `touch` runs Firefox as a phone: a touch pointer, and the page zoomed until the window holds 390 px of it, because a Firefox window is never narrower than 450 px |
| `screenshots` | `test/browser/pages.py`: the landing page, the main pages at desktop and phone width, a new member's first screen and a sheet of demos, pushed to the `ci-screenshots` branch (one folder per PR, replaced each run) |
| `PR summary` | one comment on the PR, kept up to date, with the results, the five slowest checks, the screenshots inline, and the size of the change |
| `process_pr_result` | the one required check: passes only when every job the plan needed passed |

## Releases

`.github/workflows/release_tag.yml` tags a release of `main` and publishes its notes as a GitHub
release. It runs only when started by hand: Actions, `release_tag`, Run workflow, or

```sh
gh workflow run release_tag -f bump=auto          # or patch, minor, major
gh workflow run release_tag -f version=v1.4.0     # an exact version
gh workflow run release_tag -f dry_run=true       # show the version and the notes, tag nothing
```

That tag is the `VERSION` to pass to the production container (the Pi shows `main.<commit>` instead).
The version follows semantic versioning from the commits since the last `vX.Y.Z` tag, read by
[git-cliff](https://git-cliff.org) with [cliff.toml](cliff.toml). Commits follow
[Conventional Commits](https://www.conventionalcommits.org) (types in [CLAUDE.md](CLAUDE.md)): a breaking
change (`feat!: ...`, or a `BREAKING CHANGE:` line) bumps major, `feat` minor, and every other type patch.
Pull requests are squash-merged, so the PR title is the commit on `main`; the `changes` job fails a PR
whose title is not a Conventional Commits title. Commits from before the convention appear in the notes
as earlier changes. The first release is `v1.0.0`. The workflow refuses to run from another branch, to
release when nothing changed, to reuse a tag, or to go backwards; nothing is committed, so protected
`main` needs no push. Every pull request's lint job shows the version and notes a release would get.

Working on the repository with Claude Code: see [docs/claude-code-setup.md](docs/claude-code-setup.md)
(`CLAUDE.md`, path rules, scoped agents and skills in `.claude/`).

A push to the PR cancels the run for the previous commit, and a cancelled run stops at once (no
step uses `always()`, which would keep starting suites after the cancel). Before opening a PR the same checks can
be run locally with `gofmt -l . && go vet ./... && go test ./... && node test/physics.js` (Go tests that need Postgres run when
`TEST_DATABASE_URL` is set, as in CI's integration job) and `test/browser/run.sh combo.py social.py`.
