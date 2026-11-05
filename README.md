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

- Postgres holds users, sessions, progress (one JSON document per user and prop) and flags.
- Passwords are stored as PBKDF2-SHA256 hashes (200,000 rounds); session tokens are stored hashed.
- `data/backups/progress-DATE.json` is written before the first change each day, kept 14 days.
- Full database dump: `docker exec nunchaku-db pg_dump -U flowa flowa > flowa.sql`
- The files from before accounts existed were imported into the admin account and renamed `*.imported`.

The app is plain HTTP, so passwords cross the LAN unencrypted. Keep it LAN-only.

## Feature flags

Three features are behind flags, stored in Postgres and changed at runtime by an admin
on the Admin page. No restart is needed. `signups` opens or closes sign-up, and `tour` turns the guided tour on.

| Flag | What it adds to a skill's panel |
| --- | --- |
| `details` | Steps, what to watch for, a drill and a foam warning (`web/details.js`) |
| `demos` | Animated path schematic with 3/4, front and side views, hand and speed controls, and GIF download (`web/demo.js`) |
| `references` | Source pages and videos |


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

39 of the 411 skills have a demo (`web/demo.js`), on all five props. See
[docs/demo-coverage.md](docs/demo-coverage.md) for the list and for why the rest have none.

- **Simulated** (nunchaku and most poi clips): the prop moves only by gravity, drag and cord or
  tether tension, driven by a hand model that follows the prop.
- **Throws** (nunchaku aerials, staff and double staff tosses): on release the prop is a free
  body. Both nunchaku sticks fly joined by the cord; a staff keeps its spin on a ballistic arc.
  The catching hand goes to where the prop arrives. Nunchaku release settings come from
  `node test/search-throws.js`, which searches for a throw that turns once and returns within reach.
- **Contact:** the body is a set of capsules, and props bounce off it and off each other.
  No clip may pass through the body. See [docs/contact-review.md](docs/contact-review.md)
  for what is handled and where it is still weak.
- **Drawn** (poi flowers, staff, double staff, buugeng): the path that the move's definition
  fixes, for rigid props that go where the hand puts them.

`node test/physics.js` checks every clip: lengths, slack, a seamless loop, and that the clip
matches its move's definition (direction, timing, petal count, the fixed point of an isolation).
For throws it also checks that the flight is free fall, that the prop really leaves the hand,
and that the catching hand stays within reach.
A move is only listed once it passes. `node test/coverage.js` regenerates the coverage report.
Not modelled: tethers and cords touching the body (so no wraps or rolls), rolling contact,
grip and hand changes, wrist limits.

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

- Postgres holds users, sessions, progress (one JSON document per user and prop) and flags.
- Passwords are stored as PBKDF2-SHA256 hashes (200,000 rounds); session tokens are stored hashed.
- `data/backups/progress-DATE.json` is written before the first change each day, kept 14 days.
- Full database dump: `docker exec nunchaku-db pg_dump -U flowa flowa > flowa.sql`
- The files from before accounts existed were imported into the admin account and renamed `*.imported`.

The app is plain HTTP, so passwords cross the LAN unencrypted. Keep it LAN-only.

## Feature flags

Three features are behind flags, stored in Postgres and changed at runtime by an admin
on the Admin page. No restart is needed. `signups` opens or closes sign-up, and `tour` turns the guided tour on.

| Flag | What it adds to a skill's panel |
| --- | --- |
| `details` | Steps, what to watch for, a drill and a foam warning (`web/details.js`) |
| `demos` | Animated path schematic with 3/4, front and side views, hand and speed controls, and GIF download (`web/demo.js`) |
| `references` | Source pages and videos |


## Guided tour

New users get a guided tour built with [React Joyride](https://react-joyride.com): one after
choosing props, and a short one on the first visit to each section. It is behind the `tour`
flag; when the flag is off, a short note is shown instead. Finished tours are remembered on
the account, and Profile has a "Replay the tour" button.

The rest of the UI is plain JS, so the tour is a separate React bundle, `web/tour.js`, loaded
only when a tour starts. Its source is `tour/src/tour.jsx`, and the steps are in `TOURS` in
`web/account.js`. `./deploy.sh` rebuilds the bundle when `tour/node_modules` exists
(`cd tour && npm install` once).

Demos are simulated in the browser (`web/demo.js`): the striker is a rigid rod on a cord
that can go slack, moved by gravity, drag and cord tension, and driven by a wrist model that
follows the stick. They cover F3 to F7, D4 and D5. `node test/physics.js` checks every clip:
stick length, cord length, a seamless loop, and that nothing goes through the floor. A move is
only listed once it passes. Not modelled: body collisions, grip changes, releases, wrist limits.

Search with `Ctrl K` or `/`. The UI is plain HTML, CSS and JS with no CDN. Only the guided tour has a build step.
