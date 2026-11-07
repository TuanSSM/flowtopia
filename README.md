# Flowa

A progress tracker for flow arts props, on port 3169. Five props have researched skill maps:

| Prop | Skills | Branches | Unconfirmed | Catalog |
| --- | --- | --- | --- | --- |
| Nunchaku | 109 | 11 | 15 | `web/data.js`, `web/details.js` |
| Poi | 90 | 10 | 3 | `web/catalog/poi.json` |
| Staff | 84 | 9 | 4 | `web/catalog/staff.json` |
| Double staff | 72 | 9 | 6 | `web/catalog/doublestaff.json` |
| Buugeng | 56 | 7 | 7 | `web/catalog/buugeng.json` |

Catalogs follow [docs/catalog-format.md](docs/catalog-format.md) and are checked with
`node test/catalog.js web/catalog/*.json`. Names and definitions come from the sources listed
on each skill; prerequisites, tiers and plans are coaching proposals. Skills whose name or
mechanics no opened source confirms are marked unconfirmed, and guides say so when a move is
named but not described.

- **Accounts:** sign up with a username and password (no email). Each user picks their props
  and has their own progress. An admin manages features and accounts.
- **Without an account:** browse the skill maps, guides, demos and references.
- **Dashboard, Practice, Combos, Plan:** per user and per prop.
- **Skill maps:** one per prop, with dependency graphs. Progress is tracked per hand or per direction where the skill calls for it.
- **Profiles:** a picture, a bio and Instagram, TikTok and YouTube links, shown on a public profile page
  (`#/u/<username>`) with the user's posts.
- **Timeline:** posts with a picture or a tagged skill, likes, comments and @mentions, and a list of
  everyone here.
- **Notifications:** a bell in the header for likes, comments and mentions.

Search with `Ctrl K` or `/`. The UI is plain HTML, CSS and JS with no CDN. Only the guided tour has a build step.

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
  likes, comments and notifications.
- Passwords are stored as PBKDF2-SHA256 hashes (200,000 rounds); session tokens are stored hashed.
- `data/backups/progress-DATE.json` is written before the first change each day, kept 14 days.
  Posts and comments are not in it; the database dump below covers them.
- Full database dump: `docker exec nunchaku-db pg_dump -U flowa flowa > flowa.sql`
- The files from before accounts existed were imported into the admin account and renamed `*.imported`.

The app is plain HTTP, so passwords cross the LAN unencrypted. Keep it LAN-only.

## Feature flags

Several features are behind flags, stored in Postgres and changed at runtime by an admin
on the Admin page. No restart is needed. `signups` opens or closes sign-up, and `tour` turns the guided tour on.
`community` (timeline, public profiles, people list, notifications) is on by default and can be turned off
if it gets in the way.

| Flag | What it adds to a skill's panel |
| --- | --- |
| `details` | Steps, what to watch for, a drill and a foam warning (`web/details.js`) |
| `demos` | Demos on a jointed figure and labelled pictures, with 3/4, front and side views, hand and speed controls, and GIF download (`web/demo.js`, clips in `web/clips`) |
| `references` | Source pages and videos |


## Community API

All of these need a signed-in user. `GET /api/users` (people), `GET /api/users/{name}` (profile), `GET /api/posts?before=&user=`, `POST /api/posts`, `GET|DELETE
/api/posts/{id}`, `POST /api/posts/{id}/like` (toggle), `POST /api/posts/{id}/comments`,
`DELETE /api/comments/{id}`, `GET /api/notifications`, `PUT /api/notifications/read`.
Profile fields (`bio`, `links`) go through `PUT /api/me`; links are stored as handles, whether a
handle or a URL was pasted.

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
be run locally with `gofmt -l . && go vet ./... && go test ./... && node test/physics.js` and `test/browser/run.sh combo.py social.py`.
