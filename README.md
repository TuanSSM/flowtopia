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
- **Dashboard, Practice, Combos, Plan:** per user and per prop.
- **Skill maps:** one per prop, with dependency graphs. Progress is tracked per hand or per direction where the skill calls for it.
- **Profiles:** a picture, a bio and Instagram, TikTok and YouTube links, shown on a public profile page
  (`#/u/<username>`) with the user's level, streak, badges and posts.
- **Timeline:** posts with a picture, tagged tricks from any of your props and an attached combo, likes,
  comments with replies (one level of threading, folded past the two newest, with likes and a 15-minute
  edit window; a deleted comment with replies stays as "Comment deleted") and @mentions; new badges post themselves as milestones (each user can turn that off). A people
  list ranks everyone by XP. The composer's picker searches every skill map and your combos, and suggests
  the tricks from today's session and this week's level-ups; a combo's Publish button brings it to the composer.
- **Notifications:** a bell in the header for likes, comments, replies, comment likes, mentions and new
  badges (a reply opens its thread, scrolled to it), plus a warning
  when today's practice would end a streak.
- **Gamification:** XP and levels, a practice streak across props, weekly challenges, achievements, and
  the Plan as quests. See [Gamification](#gamification).

Search with `Ctrl K` or `/`. The UI is plain HTML, CSS and JS with no CDN. Two parts have a build step: the
guided tour (`tour/`) and the 3D figure (`figure/`).

**What comes next** is in [docs/roadmap.md](docs/roadmap.md): replies, moderators and a skill map editor,
link cards for Instagram, TikTok and YouTube, next-trick suggestions from the geometry of moves, and
customisable avatars. Each epic is described in [docs/epics.md](docs/epics.md) and tracked in a checklist
under `docs/epic-<code>/todo.md`.

## Run

```sh
./deploy.sh                 # builds the binary on the host, rebuilds the image, recreates the containers
docker logs nunchaku
```

Two containers (`compose.yml`): `nunchaku` (the app) and `nunchaku-db` (Postgres 16, data in the
`pgdata` Docker volume, not exposed outside Docker). Secrets are in `.env` (mode 600):
the database password, and the admin name and password used to create the admin on first start.
Changing the admin password in `.env` later does nothing; change it on the Profile page.

## Data

- Postgres holds users, sessions, progress (one JSON document per user and prop), flags, posts,
  likes, comments, notifications, achievements, reports, the audit log and catalog revisions.
- Passwords are stored as PBKDF2-SHA256 hashes (200,000 rounds); session tokens are stored hashed.
- `data/backups/progress-DATE.json` is written before the first change each day, kept 14 days.
  Posts and comments are not in it; the database dump below covers them.
- Full database dump: `docker exec nunchaku-db pg_dump -U flowa flowa > flowa.sql`
- The files from before accounts existed were imported into the admin account and renamed `*.imported`.

The app is plain HTTP, so passwords cross the LAN unencrypted. Keep it LAN-only.

## Feature flags

Several features are behind flags, stored in Postgres and changed at runtime by an admin
on the Admin page. No restart is needed. `signups` opens or closes sign-up, and `tour` turns the guided tour on.
`community` (timeline, public profiles, people list, notifications) and `game` (XP, levels, streak sharing,
challenges, achievements, quests) are on by default and can be turned off if they get in the way.
`moderation` (reports, the queue, hiding, mutes and suspensions) and `catalogEditor` (the skill map editor)
are off until an admin turns them on.

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
- **Plan as quests.** Each phase shows its skills as chips, the skill XP earned of what it holds, and
  the XP its gate pays; the page opens with the prop's XP, the week's challenges and a focus skill.

## Community API

All of these need a signed-in user. `GET /api/users` (people, most XP first), `GET /api/users/{name}`
(profile with achievements), `GET /api/posts?before=&user=`, `POST /api/posts` (text, picture, up to 10 `tags` of prop and skill id, one `combo`), `GET|DELETE
/api/posts/{id}`, `POST /api/posts/{id}/like` (toggle), `GET /api/posts/{id}/comments?after=` (20 top-level
comments, each with its two newest replies), `POST /api/posts/{id}/comments` (with `parentId` for a reply),
`PUT|DELETE /api/comments/{id}`, `GET /api/comments/{id}/replies?after=` (50 at a time),
`GET /api/comments/{id}/thread`, `POST /api/comments/{id}/like` (toggle), `POST /api/achievements`, `GET /api/notifications`,
`PUT /api/notifications/read`. Profile fields (`bio`, `links`, `prefs`, `stats`) go through
`PUT /api/me`; links are stored as handles, whether a handle or a URL was pasted.

## Moderation, roles and the skill map editor

Handlers ask for permissions, never roles; the table is `rolePerms` in `main.go`, and `GET /api/me`
returns the signed-in user's permissions so the interface shows only what the server allows.

| Role | May |
| --- | --- |
| Member | Post, comment, like, report |
| Moderator | Also: delete and hide others' content, mute and suspend members, work the report queue, revoke badges and hide reported stats, see the dashboard and accounts, draft skill map changes |
| Admin | Also: change roles, feature flags, reset passwords, delete accounts, read the audit log, publish and restore skill maps |

Nobody acts on someone of their own role or higher, or on themself, and Flowa always keeps one admin.

- **Reports** (behind `moderation`): on a post, comment or profile, with a reason (spam, harassment, unsafe
  advice, not about flow arts, other) and a note; one open report per person and target, ten a day.
- **The queue** groups open reports by what they are about, with the content, its author, their status
  and earlier actions. Moderators hide or restore (with a reason the author sees), mute (no posting or
  commenting) or suspend (signed out, no sign-in) until a chosen time within a year, then resolve or
  dismiss; the reporters are thanked. Hidden content is filtered in every query and seen only by its
  author and moderators.
- **Audit log:** every moderation and admin action (including flags, roles, account deletion, password
  resets and catalog publishing) is written by one helper in the same transaction, and admins can filter it.
- **Dashboard:** members, people active in 7 and 30 days, open reports, and 12 weeks of practice sessions,
  posts, comments and reports as small charts with a table view.
- **Skill map editor** (behind `catalogEditor`): edit skills, branches and phases, or the whole catalog as
  JSON, in a draft. Every field shows the validator's messages as you type (`web/catalog-check.js`; the
  server runs the same rules in Go, `validateCatalog`, and both must agree on `test/catalog-fixtures`).
  The review lists each change and how many people have progress on it. Publishing (admins) makes a new
  revision live for everyone; earlier revisions can be exported as catalog files or restored.
  Ids are never reused: renaming a published skill keeps its old id as an alias, and progress moves to the
  new id when it next loads; retired skills leave the maps but keep their progress. Publishing or restoring
  anything that would leave progress with no skill to belong to is refused.

Admin API (permission in brackets): `PUT /api/admin/users/{id}/role` (user.role), `GET /api/admin/audit`
(audit.read), `GET /api/admin/dashboard` (dashboard.read), `POST /api/reports`, `GET /api/mod/queue`,
`POST /api/mod/resolve` (report.resolve), `POST /api/mod/actions` (the action's own permission),
`GET|PUT|DELETE /api/admin/catalog/{prop}[/draft]`, `GET .../diff`, `POST .../publish` (catalog.publish),
`POST .../shipped`, `GET .../revisions/{rev}`, `POST .../revisions/{rev}/restore` (catalog.publish).

## Guided tour

New users get a guided tour built with [React Joyride](https://react-joyride.com): one after
choosing props, and a short one on the first visit to each section. It is behind the `tour`
flag; when the flag is off, a short note is shown instead. Finished tours are remembered on
the account, and Profile has a "Replay the tour" button.

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

## Pull requests

`main` is protected: changes arrive by pull request, and `.github/workflows/process_pr.yml`
runs on every one so the checks do not have to run on the Pi. Its jobs:

| Job | What it runs |
| --- | --- |
| `changes` | decides whether the PR touches code; a PR of Markdown, `docs/`, `.claude/` or `.gitignore` only runs just `docs`, and the required check accepts the skipped jobs in that case, so it can auto-merge |
| `docs` | `test/docs.sh`: Markdown links point at files that exist; `.claude` agents, skills and rules have their front matter |
| `lint` | `gofmt -l`, `go vet`, staticcheck; `node --check` on every script; `test/catalog.js`; Python syntax; actionlint |
| `unit tests` | `go build`, `go test -race`, `node test/physics.js` (every clip), and that `web/tour.js` and `web/figure3d.js` are the bundles their sources build |
| `integration tests` | the real server against a Postgres service, driven through headless Firefox (`test/browser/combo.py`, `demos.py`, `social.py`) |
| `screenshots` | `test/browser/pages.py`: the main pages at desktop and phone width and a sheet of demos, pushed to the `ci-screenshots` branch (one folder per PR, replaced each run) |
| `PR summary` | one comment on the PR, kept up to date, with the results, the screenshots inline, and the size of the change |
| `process_pr_result` | the one required check: passes only when every job above passed |

Working on the repository with Claude Code: see [docs/claude-code-setup.md](docs/claude-code-setup.md)
(`CLAUDE.md`, path rules, scoped agents and skills in `.claude/`).

A push to the PR cancels the run for the previous commit. Before opening a PR the same checks can
be run locally with `gofmt -l . && go vet ./... && go test ./... && node test/physics.js` (Go tests that need Postgres run when
`TEST_DATABASE_URL` is set, as in CI's integration job) and `test/browser/run.sh combo.py social.py`.
