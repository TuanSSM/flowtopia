# Epics

Each epic is a user outcome large enough to need several pull requests. This page says why each
one matters, what is in and out of scope, how success is measured, and what can go wrong. The
order and the reasons for it are in [roadmap.md](roadmap.md). The working checklist for each epic,
with a definition of done on every item, is in `docs/epic-<code>/todo.md`.

The first cycle's five epics are below and have all shipped. The second cycle, planned on
10 November 2026 from the debts and wishes recorded across `docs/`, follows them under
[Second cycle](#second-cycle); each of its epics is also a GitHub issue labelled `epic`.

## First cycle (shipped)

| Code | Epic | Size | Flag | Checklist |
| --- | --- | --- | --- | --- |
| RPL | Replies and threaded comments | S | `community` | [epic-rpl/todo.md](epic-rpl/todo.md) |
| MOD | Moderators, admin panels and the skill map editor | L | `moderation`, `catalogEditor` | [epic-mod/todo.md](epic-mod/todo.md) |
| LNK | Instagram, TikTok and YouTube links in posts | M | `linkCards`, `videoEmbeds` | [epic-lnk/todo.md](epic-lnk/todo.md) |
| GEO | Next-trick suggestions from the geometry of moves | L | `suggest` | [epic-geo/todo.md](epic-geo/todo.md) |
| AVT | Customisable avatars in demos, combos and stickers | L | `avatars` | [epic-avt/todo.md](epic-avt/todo.md) |

---

## RPL: Replies and threaded comments

**Problem.** Comments are a flat list. When two people answer each other under a post, the
exchange is lost among other comments, and the person being answered is told nothing unless they
were @mentioned by hand.

**Who it is for.** Anyone who comments: a learner asking how a move is done, the person who posted
answering, a friend cheering.

**Outcome.** A comment can be answered in place. Replies sit under the comment they answer, the
author of that comment is told, and long threads fold away so a post stays readable.

**In scope.**
- One level of threading, as Instagram and TikTok do it: replies to a comment sit under it; a
  reply to a reply joins the same thread and starts with an @mention of the person answered.
- Likes on comments and replies, and sorting a thread by time.
- Folding: the two newest replies are shown, the rest behind "View N more replies".
- A reply notification, and a deleted comment with replies kept as "Comment deleted" so the
  thread still reads.
- Comments loaded per post on demand, instead of every comment with every page of posts.

**Out of scope.** Nested trees deeper than one level, editing after 15 minutes, rich text,
reactions other than a like.

**Success.** Comments and replies per post rise; the share of comments that get an answer rises;
no page slows down: a post with 200 comments opens as fast as one with 2.

**Risks.** Loading comments lazily changes the feed's JSON; older clients still expect
`comments` on each post, so the field stays (with the first few) until every client is updated.

---

## MOD: Moderators, admin panels and the skill map editor

**Problem.** There is one admin and no way for anyone else to help keep the timeline healthy.
Nobody can report a post. Fixing a wrong prerequisite or a typo in a skill map needs a code
change and a redeploy. Achievements and stats are self-reported and nobody can correct them.

**Who it is for.** The admin who runs the Pi; trusted members who will moderate; everyone who
wants a timeline without spam or abuse; anyone who finds a mistake in a skill map.

**Outcome.** Three roles: user, moderator and admin, each with a clear set of powers. Users can
report posts, comments and people. Moderators work a queue, hide content with a reason, mute or
suspend accounts for a time, and every action is logged. Admins get a dashboard, account and role
management, and an editor for the skill maps that validates every change, shows its effect on
people's progress, and can be rolled back.

**In scope.**
- Roles and a permission table enforced on the server; the UI only hides what the server refuses.
- Reports with a reason, a moderation queue, hide and restore, mute and suspend with an end time,
  an audit log of every moderation and admin action, and notifications to the people affected.
- Admin dashboard: active users, sessions logged, posts and reports per week, from data already
  in Postgres.
- Skill map editor: catalogs moved into the database with revisions, drafts, a diff, publish,
  roll back; the same validation rules as `test/catalog.js`, implemented once in Go and run by
  both the server and CI; skill ids that are never reused, with renames kept as aliases.
- Moderator tools for self-reported data: revoke a badge, reset reported stats.

**Out of scope.** Automatic content filtering, appeals by email, per-prop moderators, editing the
demo engine's clips from the browser.

**Success.** Reports resolved within 48 hours; zero skill-map fixes that need a redeploy after the
editor ships; zero catalog changes that orphan progress.

**Risks.**
- **Two validators drifting.** The Go validator becomes the only one; `test/catalog.js` either
  calls it or is retired, and both are run against the same fixtures until then.
- **Orphaned progress.** Retiring or renaming a skill must never lose levels. The editor shows how
  many users have progress on a skill before it can be retired, and renames keep the old id.
- **Moderator overreach.** Every action is logged with who, what, why and when, and is visible
  to admins.

---

## LNK: Instagram, TikTok and YouTube links in posts

**Problem.** People film their practice on Instagram, TikTok and YouTube. Pasting a link into a
Flowa post gives a bare URL with no picture, so nobody clicks it.

**Who it is for.** Anyone who posts their videos elsewhere and wants their Flowa friends to see
them; anyone scrolling the timeline.

**Outcome.** Pasting a link to an Instagram post or reel, a TikTok video or a YouTube video or
short turns into a card with the thumbnail, title and author, without the visitor's browser
talking to those sites until they choose to. YouTube videos can play in place when the admin
allows it.

**In scope.**
- Recognising links from the three sites, normalising them to a canonical URL, and storing them on
  the post (up to 4 per post), alongside the trick and combo chips.
- Server-side previews: the Go server fetches the site's public oEmbed or page metadata from an
  allowlist of hosts, with short timeouts and size limits, and stores the title, author and a
  shrunk thumbnail itself. Visitors load the thumbnail from Flowa, so no request leaks to the site.
- A plain card when no preview is available (Instagram's oEmbed needs a Meta app token, so its
  cards may be title-less at first), with the site's name and the link.
- Click-to-play YouTube through `youtube-nocookie.com`, behind its own flag, which needs one
  `frame-src` entry in the CSP. Instagram and TikTok open in a new tab: their embeds need their
  own scripts, which the CSP does not allow and this epic will not add.
- Linking a post on another site from a profile's social link, for "my latest video".

**Out of scope.** Uploading video to Flowa, importing a whole feed, any login to the three sites.

**Success.** Share of posts with a link card; click-through on cards; zero requests to the three
sites from a visitor's browser unless they press play or open the link.

**Risks.**
- **Server-side request forgery.** Only fixed hosts and fixed endpoints are fetched, redirects are
  followed only within the allowlist, and responses are capped in size and time.
- **Sites change their markup or APIs.** Previews degrade to the plain card, never to an error.
- **CSP.** The one `frame-src` entry for YouTube is named in the PR and sits behind its own flag.

---

## GEO: Next-trick suggestions from the geometry of moves

**Problem.** Building a combo means knowing which trick can follow the last one: same plane or a
plane the hand can reach, same direction of spin or a clean reversal, the prop ending where the
next trick starts. Today the combo builder offers every skill in a long list, and people find out
what flows only by trying.

**Who it is for.** Anyone building a combo, from a beginner linking two figure-8s to someone
shaping a 90-second routine.

**Outcome.** The combo builder suggests the next trick, ranked by how well it flows out of the last
one and whether the user can do it, with a reason in plain words ("same wall plane, same direction,
the striker is already on the outside"). When nothing links directly, it suggests a bridge: the
shortest chain of known tricks that gets there.

**In scope.**
- **Measuring every clip at build time** (`sim/`): the path of each prop's tip, its length,
  speed, curvature and torsion computed with discrete differential geometry; a rotation-minimising
  (parallel transport) frame instead of the Frenet frame, which flips at inflections; the plane of
  motion as a best-fit normal on the sphere; the sense of rotation from angular momentum about that
  normal; the winding number per cycle; and the state at entry and exit: hand position, prop
  orientation in SO(3), angular velocity and grip.
- **Comparing moves**: geodesic angle between plane normals, the angle between orientations on
  SO(3), the change in angular velocity, the distance the hand must travel, and the shape of whole
  cycles with elastic shape analysis (square-root velocity functions), which compares curves
  independently of speed and starting point.
- **A transition score** combining these, with weights fitted at build time against the
  transitions people have marked Works, Needs bridge or Blocked, using only anonymous counts that
  users opt into sharing.
- **Skills without clips** (most of the 411) get structured fields in their catalog: plane,
  direction, hand, grip, entry and exit, maintained in MOD's editor, and marked unknown where no
  source says.
- **In the combo builder**: a ranked list of next tricks, the reason for each, a bridge when there
  is no direct link, and a way to say a suggestion was wrong.

**Out of scope.** Analysing the user's own videos, pose estimation from a camera, machine-learned
models that run in the browser.

**Success.** Offline, the score separates Works from Blocked transitions with an AUC of at least
0.75 before any suggestion is shown. In use, over a quarter of suggestions are added to a combo
and over 60% of those are later marked Works.

**Risks.**
- **Coverage.** Only 104 of 411 skills have clips. Suggestions rely on catalog fields for the rest,
  and say so when they do.
- **Too few labels** to fit weights. Until there are enough, fixed weights from the physics are
  used, and the offline check decides whether suggestions are shown at all.
- **Wrong confidence.** A suggestion with weak evidence says so, and "Blocked" feedback lowers it
  for everyone who opted in.

---

## AVT: Customisable avatars in demos, combos and stickers

**Problem.** Every demo is played by the same grey mannequin. People would rather see themselves:
their build, their style, their prop colours. Profiles and the streak overlay use a photo or
initials, which many people do not want to share.

**Who it is for.** Everyone; most of all people who do not want to post photos of themselves.

**Outcome.** Each user builds an avatar, as with Snapchat's Bitmoji: skin tone, hair, face, outfit,
accessories and prop colours, plus a body preset. Their avatar plays their demos and their combos,
appears on their profile and comments, and poses on stickers: the streak overlay, badge unlocks,
and milestone posts.

**In scope.**
- **An avatar editor** with live 3D preview: skin tones, hair styles and colours, eyes, brows and
  mouth from a set, tops, bottoms and shoes with colours, hats, glasses and wristbands, and prop
  colours per prop.
- **Modular parts** skinned to the same Mixamo skeleton the figure already uses, so a part is one
  more mesh and no animation changes. Flat materials and vertex colours, so no textures are needed.
- **Body presets** (for example short, average and tall, and lean or broad): each preset's bone
  lengths are baked through the demo engine and checked by `node test/physics.js` like any clip.
  Free sliders are not offered, because a body nobody has checked can put a prop through a leg.
- **Avatar in demos and the combo player**, falling back to the standard figure where WebGL is
  missing or the `avatars` flag is off.
- **Stickers**: the avatar rendered in a set of poses taken from the rig (celebrate, salute, the
  last frame of a trick) as a transparent PNG, for the streak overlay templates, badge unlock
  cards and the profile picture.
- **Storage**: a small validated JSON on the user (about 1 KB), public on the profile.

**Out of scope.** Photo-to-avatar generation, face scanning, paid items, user-made parts.

**Success.** Over half of active users customise an avatar within a month; demo pages are no
slower: a frame under 33 ms in Firefox on the Pi and under 16 ms on a mid-range phone.

**Risks.**
- **Performance.** The figure is skinned on the CPU (shader skinning drew nothing in software
  WebGL), so each extra mesh costs time on every frame. Parts are merged per avatar into one mesh
  when it is built, and a frame budget is checked in CI.
- **Licensing.** Parts must be our own or CC0. The X Bot model's licence is checked before parts
  are fitted to it; if it does not allow derivatives, a CC0 base is used instead.
- **Physics honesty.** A cosmetic part never changes the clip. Only checked body presets do.

---

# Second cycle

Planned from what the docs already record: the stage audit ([audit-plan.md](audit-plan.md)), the
hardening review's "left for later" ([hardening-plan.md](hardening-plan.md)), the demo engine's
weak points ([demo-engine.md](demo-engine.md), [contact-review.md](contact-review.md)), the
suggestion limits ([suggestions.md](suggestions.md)), the avatar costs and the open licence
question ([avatars.md](avatars.md)), the multi-prop items still open
([multi-prop-architecture.md](multi-prop-architecture.md)) and the coverage table
([demo-coverage.md](demo-coverage.md)). Nothing here comes from a guess about what people want;
every epic names the document that raised it.

Each epic has one category, which is also its label on GitHub and the type its pull requests will
mostly carry:

| Category | Means | Commit type |
| --- | --- | --- |
| fix | Something is wrong today and a person can meet it | `fix` |
| perf | Faster or lighter with the same behaviour | `perf` |
| improvement | Debt, structure or quality: better without being new | `refactor`, `test`, `build`, `docs` |
| feature | Something new a person can use | `feat` |

| Code | Epic | Category | Size | Issue | Raised by |
| --- | --- | --- | --- | --- | --- |
| AUD | [Close the stage audit](#aud-close-the-stage-audit) | fix | M | [#29](https://github.com/TuanSSM/flowtopia/issues/29) | audit-plan.md |
| LIC | [Settle the base model's licence](#lic-settle-the-base-models-licence) | fix | S | [#30](https://github.com/TuanSSM/flowtopia/issues/30) | avatars.md |
| FPS | [Demo frames inside the budget](#fps-demo-frames-inside-the-budget) | perf | M | [#31](https://github.com/TuanSSM/flowtopia/issues/31) | avatars.md, epic-avt |
| LOAD | [First paint, bytes and queries](#load-first-paint-bytes-and-queries) | perf | M | [#32](https://github.com/TuanSSM/flowtopia/issues/32) | avatars.md, hardening-plan.md |
| TST | [Tests that wait for what they check](#tst-tests-that-wait-for-what-they-check) | improvement | S | [#33](https://github.com/TuanSSM/flowtopia/issues/33) | hardening-plan.md |
| SHP | [One shape for the app](#shp-one-shape-for-the-app) | improvement | M | [#34](https://github.com/TuanSSM/flowtopia/issues/34) | audit-plan.md (structure) |
| OPS | [Safe beyond the LAN](#ops-safe-beyond-the-lan) | improvement | M | [#35](https://github.com/TuanSSM/flowtopia/issues/35) | hardening-plan.md, multi-prop-architecture.md |
| A11Y | [Usable by keyboard, screen reader and a shaky hand](#a11y-usable-by-keyboard-screen-reader-and-a-shaky-hand) | improvement | S | [#36](https://github.com/TuanSSM/flowtopia/issues/36) | audit-plan.md (phones) |
| CAT | [Skill maps, second research pass](#cat-skill-maps-second-research-pass) | improvement | M | [#37](https://github.com/TuanSSM/flowtopia/issues/37) | demo-coverage.md, suggestions.md |
| TRU | [Progress the server can vouch for](#tru-progress-the-server-can-vouch-for) | improvement | L | [#38](https://github.com/TuanSSM/flowtopia/issues/38) | hardening-plan.md, multi-prop-architecture.md |
| LBL | [Open the suggestion gate](#lbl-open-the-suggestion-gate) | feature | S | [#39](https://github.com/TuanSSM/flowtopia/issues/39) | suggestions.md |
| SIM | [Wraps, weaves and passes](#sim-wraps-weaves-and-passes) | feature | L | [#40](https://github.com/TuanSSM/flowtopia/issues/40) | demo-engine.md, contact-review.md |
| XPR | [Across props](#xpr-across-props) | feature | M | [#41](https://github.com/TuanSSM/flowtopia/issues/41) | multi-prop-architecture.md |
| OFF | [Practice without a signal](#off-practice-without-a-signal) | feature | M | [#42](https://github.com/TuanSSM/flowtopia/issues/42) | roadmap principles, audit-plan.md (UN-02, UN-03) |

---

## AUD: Close the stage audit

**Category:** fix. **Size:** M. **Checklist:** [epic-aud/todo.md](epic-aud/todo.md).

**Problem.** The stage audit of 8 November found 46 items. One is severe: the container has no
root certificates, so every link preview fails in production. The rest lose people's drafts, lock
accounts out, count limits against the wrong thing, show stale admin state, and put tap targets
under 44 px on phones.

**Outcome.** Every item is fixed in the six pull requests [audit-plan.md](audit-plan.md) already
specifies, with the test named there, in that order, and the audit's four journeys run again on
stage with each item marked fixed or reopened.

**In scope.** PRs 1 to 6 of the plan exactly; nothing is added to them. **Out of scope.** The four
structure questions (SHP).

**Success.** 46 of 46 marked fixed on stage; a YouTube link posted on production shows a card.

**Risks.** PR 4 triples sign-in time on the Pi (600 000 PBKDF2 rounds); the hash slots bound it,
and the dashboard's sign-in timing is watched for a week. Migration 5 adds columns; it runs under
the advisory lock like the others.

---

## LIC: Settle the base model's licence

**Category:** fix. **Size:** S. **Checklist:** [epic-lic/todo.md](epic-lic/todo.md).

**Problem.** `web/assets/human.glb` is Adobe's Mixamo "X Bot". Serving the raw file to every
visitor may not be allowed by Mixamo's terms ([avatars.md](avatars.md), Licences). The question has
been open since the avatars epic.

**Outcome.** Either written confirmation that serving it is allowed, recorded in `docs/avatars.md`,
or the model replaced by a CC0 humanoid on a Mixamo-compatible skeleton, with every figure test
passing on it.

**In scope.** The question to Adobe; a shortlist of CC0 candidates with the same bone names; the
swap behind the existing tests (`node test/body3d.js`, `test/browser/avatar.py`, the physics
suite, the contact sheets), including refitting the rig capsules and the measured head.
**Out of scope.** New parts or outfits.

**Success.** The licence line in `docs/avatars.md` has no open question; bundle size stays under
1.5 MB; the rig fit stays within 25 mm.

**Risks.** A new mesh changes the drawn body, so the capsules and the head measurements move and
some clips may need re-tuning; the same tests that found the last fit catch it.

---

## FPS: Demo frames inside the budget

**Category:** perf. **Size:** M. **Checklist:** [epic-fps/todo.md](epic-fps/todo.md).

**Problem.** One demo frame takes about 58 ms on the Pi's Firefox (17 frames a second) against a
33 ms budget in CI and a 16 ms target on a mid-range phone ([avatars.md](avatars.md), Cost). The
figure is skinned on the CPU every frame because shader skinning drew nothing in software WebGL,
and that path is taken on real GPUs too.

**Outcome.** A demo plays at the browser's frame rate on a phone and at 30 frames a second on the
Pi, with the same picture.

**In scope.** Shader skinning on GPUs that render it (detected by drawing one frame to a small
canvas and reading a pixel back), the CPU path kept for software WebGL and CI; skinning only when
the pose changed; a lighter mesh for the body (decimated once at build time, checked by the body
tests); no allocations per frame; the frame time measured on the Pi in the browser suite and
reported in the PR comment, not only asserted. **Out of scope.** Changing how clips are computed.

**Success.** Under 33 ms on the Pi and under 16 ms on a phone for the heaviest avatar; contact
sheets before and after match within a pixel tolerance.

**Risks.** Two skinning paths can drift; the browser suite renders one frame on each and compares
them. The GPU detection can be wrong on an odd driver; the fallback is the CPU path, never a blank.

---

## LOAD: First paint, bytes and queries

**Category:** perf. **Size:** M. **Checklist:** [epic-load/todo.md](epic-load/todo.md).

**Problem.** The 3D figure bundle and model are 1.36 MB compressed and load with the app, whether
or not a demo is opened; static files are compressed on each request; the clip index covers every
prop; the feed and dashboard queries have never been read with `EXPLAIN`.

**Outcome.** The app shell paints in under a second on a phone on a slow connection; a demo's
bytes are fetched when a demo is first opened, and then cached for good.

**In scope.** The figure bundle and model loaded on first demo; assets with a hash in their name,
precompressed at build time and served with a long cache life; the clip index split per prop; a
service worker that caches the shell and the figure (the base OFF builds on); a timing log line per
API request and an `EXPLAIN` pass over the feed, people, dashboard and notification queries with
the indexes they need. **Out of scope.** Offline writes (OFF).

**Success.** Bytes before first paint under 150 KB compressed; time to first demo frame no worse
than today on the Pi; every listed query uses an index on a database with 10 000 posts.

**Risks.** Hashed names and a service worker can serve a stale app after a deploy; the worker
checks the version the server reports (`VERSION`, on the dashboard since #26) and reloads.

---

## TST: Tests that wait for what they check

**Category:** improvement. **Size:** S. **Checklist:** [epic-tst/todo.md](epic-tst/todo.md).

**Problem.** The browser suites contain about 150 fixed sleeps. They pass on a quiet machine and
flake under load; a sign-in timing flake cost a rerun this week. The hardening review left this
as a separate cleanup ([hardening-plan.md](hardening-plan.md)).

**Outcome.** Every wait names the condition it waits for, with a timeout and a message that says
what did not happen. A failing check fails once, for a reason.

**In scope.** `wait_for(condition, what)` in `test/browser/h.py`; every `time.sleep` replaced or
justified in a comment; a lint that fails the suite on a bare sleep; the time per check reported
in the PR comment so slow checks stand out; Go handler tests for the routes the browser suites
cover only incidentally. **Out of scope.** New product checks.

**Success.** Zero reruns for flakes over a month of PRs; suite wall time down by the sum of the
sleeps that were longer than needed.

**Risks.** A wait that polls the DOM too often slows Firefox on the Pi; the helper polls at 50 ms
with a backoff.

---

## SHP: One shape for the app

**Category:** improvement. **Size:** M. **Checklist:** [epic-shp/todo.md](epic-shp/todo.md).

**Problem.** The audit raised four structure questions ([audit-plan.md](audit-plan.md), Structure):
the current prop is a hidden global mode changed from five places; level, streak and challenges
repeat on up to five pages; account settings are split over four pages; the maps have five entry
points and the prop tree dominates the sidebar and the phone drawer.

**Outcome.** The app has one shape a new person can draw from memory: the prop is in the URL, one
Progress card, Profile is who you are and Settings is how Flowa behaves, one Skill maps entry.

**In scope.** The recommended option of each question, as the audit wrote them: (a) for MAP-01,
the prop in the route for every prop-scoped page; one Progress card on the dashboard and a one
line summary elsewhere (MAP-05); Profile and Settings as described (MAP-06); one Skill maps entry
with the prop chosen on the page and the tree only for the current prop (MAP-07). Old routes
redirect. **Out of scope.** Visual redesign; new pages.

**Success.** No page changes the current prop as a side effect (a browser check browses another
map and asserts Practice still acts on the member's prop); phone drawer fits the screen; the
screenshots in `pages.py` are the spec.

**Risks.** This is a product decision as much as code. The owner confirms the four options before
the first PR; the todo lists them as the first item.

---

## OPS: Safe beyond the LAN

**Category:** improvement. **Size:** M. **Checklist:** [epic-ops/todo.md](epic-ops/todo.md).

**Problem.** Opening Flowa past the LAN is blocked by things the hardening review left to the owner:
TLS and a reverse proxy, binding to one address, Postgres pinned by digest
([hardening-plan.md](hardening-plan.md), Left for later; [multi-prop-architecture.md](multi-prop-architecture.md),
item 8). The image is built on the Pi at deploy time; a backup is taken but never restored anywhere.

**Outcome.** A documented, tested path from a tag to a running instance anyone can reach over
HTTPS, with a restore drill that proves the backup.

**In scope.** A reverse proxy recipe in `compose.yml` (Caddy, automatic certificates) behind a
profile, with `FLOWA_TRUSTED_PROXIES` set for it; the app bound to loopback by default; Postgres
pinned by digest with a documented update step; the arm64 image built by the release workflow and
pulled by `deploy.sh`, so the Pi stops building; a weekly CI job that restores the latest backup
format into a scratch database and runs the Go tests against it; `GET /healthz` with the schema
version and whether TLS roots are present; one structured log line per request. **Out of scope.** Multi-instance,
metrics dashboards, email alerts.

**Success.** A fresh machine goes from `git clone` to HTTPS in the README's steps; the restore
drill is green; a deploy takes under a minute on the Pi.

**Risks.** Building in CI changes the bytes deployed; the image is built from the tag by the same
Dockerfile and its digest is recorded in the release notes.

---

## A11Y: Usable by keyboard, screen reader and a shaky hand

**Category:** improvement. **Size:** S. **Checklist:** [epic-a11y/todo.md](epic-a11y/todo.md).

**Problem.** The audit measured tap targets under 44 px (fixed in AUD PR 5), and nothing checks
keyboard order, focus visibility, names on icon buttons, reduced motion for demos, or contrast
outside the avatar palettes.

**Outcome.** Every page works with the keyboard alone and reads sensibly in a screen reader; a
person who asks for reduced motion gets a still of each demo with a Play button.

**In scope.** Focus order and visible focus on every control; `aria-label` on icon buttons and the
figure canvas's text alternative (the caption); `prefers-reduced-motion` honoured by the player
and the tour; contrast of text and controls checked in both themes by a script in lint (the
avatar colour test already exists); a keyboard pass of the command palette, the composer and the
editor in `pages.py`. **Out of scope.** Sign language or audio descriptions of moves.

**Success.** The checks pass; a keyboard-only run through sign in, log a session, publish a combo
and reply to a comment is a browser test.

**Risks.** Few. The canvas player is the hard part; its controls are already HTML.

---

## CAT: Skill maps, second research pass

**Category:** improvement. **Size:** M. **Checklist:** [epic-cat/todo.md](epic-cat/todo.md).

**Problem.** Of 411 skills, 94 are unconfirmed or not described by a source that was opened
([demo-coverage.md](demo-coverage.md)); most `motion` fields are `unknown`, so suggestions for
skills without a clip say little ([suggestions.md](suggestions.md), Limits); naming varies between
communities and the catalogs have few aliases.

**Outcome.** Every skill either cites an opened source for its name and mechanics or says plainly
that it is unconfirmed, and its motion fields are filled wherever a source settles them.

**In scope.** One prop per pull request through the catalog editor's revisions, starting with
buugeng (35 of 56 unconfirmed) and staff; `star` cleared only with a reference that was opened;
`motion` from the skill's own text; aliases for names used elsewhere; `node sim/motion.js`
extended to report what each revision changed. **Out of scope.** Inventing mechanics; general
knowledge as a source.

**Success.** Unconfirmed skills under 40; `motion.plane` known for over half of the skills without
a clip; no orphaned progress (the editor's own check).

**Risks.** Sources may simply not exist for some moves; those stay starred, which is the honest
result, and the count is reported rather than forced down.

---

## TRU: Progress the server can vouch for

**Category:** improvement. **Size:** L. **Checklist:** [epic-tru/todo.md](epic-tru/todo.md).

**Problem.** Progress is a JSON document the browser owns; XP, streaks, badges and stats are
reported by the client, so a member can claim anything, and anything social that reads them
(badges on the timeline, challenges, a future coach view) trusts the claim. The hardening review
called changing this an epic of its own; the multi-prop note says to split levels and sessions
into tables before sharing or statistics.

**Outcome.** Sessions and skill levels are rows the server wrote from actions it saw; XP, streak
and badges are computed by the server from them; the document remains for the member's private
notes and settings. Nothing a person sees about another person is self-declared.

**In scope.** Tables for sessions and levels keyed by (user, prop, skill); writes through the API
with the existing `rev` conflict flow; server-side XP, streak and badge rules (the page's rules
moved to Go, with a test that keeps them equal until the page's copy is removed); a one-time
migration of every document with a report per member; the client keeps working offline through
OFF's queue. **Out of scope.** Changing what earns XP.

**Success.** No badge or stat is written by the client; migration report shows zero members with a
changed level or streak; the feed renders no self-reported number.

**Risks.** The biggest change since accounts. It ships behind `progressServer`, member by member
(the flag plus an allowlist), with the document kept in sync both ways until the flag is on for all.
The design, and the decisions it leaves to the owner, are in [progress-server.md](progress-server.md).

---

## LBL: Open the suggestion gate

**Category:** feature. **Size:** S. **Checklist:** [epic-lbl/todo.md](epic-lbl/todo.md).

**Problem.** Next-trick suggestions use fixed weights and say they are early, because fewer than 30
shared transition pairs exist to fit against ([suggestions.md](suggestions.md), Fitting). Marking a
transition Works or Blocked is possible but nothing asks for it.

**Outcome.** After a combo is played or logged, the member is asked once per new transition how it
went; the dashboard shows how far the community is from the gate; when it is reached, the fit runs
and the weights ship by pull request.

**In scope.** The one-tap prompt (Works, Needs a bridge, Blocked, Skip) after a run, only for
members who opted in to sharing, at most once per transition; the count toward 30 on the admin
dashboard; a CI job that fetches the counts from an instance given a token, runs
`sim/fit-suggest.js` and opens the pull request with the AUC in its body. **Out of scope.** Any
change to the score itself.

**Success.** The gate opens; the fitted weights beat the baseline; the "early" label disappears.

**Risks.** Prompts annoy; one per transition, dismissable for good, and off without consent.

---

## SIM: Wraps, weaves and passes

**Category:** feature. **Size:** L. **Checklist:** [epic-sim/todo.md](epic-sim/todo.md).

**Problem.** 214 described skills have no demo because the engine cannot show them
([demo-coverage.md](demo-coverage.md)): tethers and cords are not colliders, so no wraps or wrist
rolls; nothing rolls on the body, so no contact staff; the upper body cannot turn against the hips,
which blocks poi weaves; nothing changes hands except by a throw ([demo-engine.md](demo-engine.md),
Where it is still weak; [contact-review.md](contact-review.md)).

**Outcome.** The four largest missing groups play on the figure, each checked like every clip:
wraps on a tether modelled as a chain, poi weaves with a turning upper body, hand passes, and
rolling contact for the simplest contact staff moves.

**In scope.** In this order, each its own PR with its physics test first: (1) a chest that turns
against the pelvis in the rig and the 3D figure, then the poi weave; (2) the tether and cord as a
chain of points with contact and friction, then the arm wrap and the nunchaku wrist roll; (3) a
pass as a constrained hand-over with the receiving hand steered to the handle; (4) rolling contact
without slip for the halo. The caption keeps saying what is not simulated. **Out of scope.**
Finger physics; travel steps; everything the sources do not describe.

**Success.** At least 60 more skills with a demo; the physics suite still under 4 minutes on the
Pi; no tolerance loosened.

**Risks.** A chain tether makes the solver stiffer and slower; it is simulated at build time only,
and the bake stays cached per clip. Rolling contact may not converge; it is last and may ship
partly.

---

## XPR: Across props

**Category:** feature. **Size:** M. **Checklist:** [epic-xpr/todo.md](epic-xpr/todo.md).

**Problem.** A member with two props has two of everything: two streaks, two plans, two dashboards,
and a combo can hold one prop ([multi-prop-architecture.md](multi-prop-architecture.md), items 4
and 7). Theory that is shared (planes, timing, weaves) is written once per catalog and never
linked.

**Outcome.** One dashboard for a person, not a prop; combos that change prop mid-routine; a skill
that says "you already know this from poi" when the shared concept is clean there.

**In scope.** A combined Progress card (after SHP), total practice and an all-props streak that
AUD already labels; mixed-prop combos in the builder and player with the figure swapping the prop
at the boundary; a small `concepts` list in the catalogs (plane, timing and direction, weave,
isolation) that skills reference, used for a hint and never for unlocking; suggestions that cross
props only through those concepts. **Out of scope.** Transfer claims without a source; cross-prop
unlocks.

**Success.** Members with two or more props open one dashboard; mixed combos are published; the
hint is shown only where both catalogs name the concept.

**Risks.** Wrong transfer claims are worse than none (the architecture note says so); concepts are
curated in the editor, not inferred.

---

## OFF: Practice without a signal

**Category:** feature. **Size:** M. **Checklist:** [epic-off/todo.md](epic-off/todo.md).

**Problem.** Practice happens in parks and gyms. The app installs as a PWA but has no service
worker; without a connection nothing opens, and a session logged on a flaky connection can be
lost (the audit's UN-02 and UN-03 are the online version of that problem).

**Outcome.** The maps, the log, the plan and the demos the member has opened work offline; a
session logged offline is saved when the signal returns, through the same `rev` conflict flow,
and the member sees what is still waiting.

**In scope.** The service worker from LOAD extended with the catalogs, the member's documents and
opened clips; a write queue in IndexedDB replayed in order; a "waiting to sync" line in the header
with a retry; the composer kept out of it (posting needs the server). **Out of scope.** Offline
sign-in; the timeline; link cards.

**Success.** Airplane mode: open the app, log a session, play a demo opened before, reconnect, the
session is on the server; a browser test does exactly that with the network blocked.

**Risks.** Two devices offline at once produce conflicts; the existing 409 flow resolves a
different skill and asks about the same one.
