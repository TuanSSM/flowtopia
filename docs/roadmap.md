# Flowa roadmap

Where Flowa is going, in what order, and why. The epics themselves are described in
[epics.md](epics.md); each has a checklist with a definition of done in `docs/epic-<code>/todo.md`.
How they are built, branch by branch, is in [implementation-plan.md](implementation-plan.md).

Last reviewed: 10 November 2026.

## What Flowa is for

Flowa helps people who spin nunchaku, poi, staff, double staff and buugeng get better on
purpose, and enjoy doing it with others. It does three jobs:

1. **Know what to learn next.** Researched skill maps with prerequisites, guides and demos that
   are physically correct, so the next step is never a guess.
2. **Keep practising.** A log, a streak, XP, challenges and badges that reward showing up and
   levelling up, without rewarding fake progress.
3. **Practise together.** Profiles, a timeline, trick and combo chips, and the means to share a
   streak or a combo wherever people already post.

Flowa runs on a Raspberry Pi for one community at a time. That is a constraint and a feature:
everything must be cheap to run, private by default, and honest about what it knows.

## Product principles

These decide trade-offs when the epics pull in different directions.

- **Correct before clever.** A demo that moves wrongly, or a suggestion that leads into a blocked
  transition, costs more trust than it earns. Ship what can be checked; say plainly what cannot.
- **One tap for the common case.** The obvious action (tag today's trick, publish this combo,
  reply to that comment) takes one tap. Power lives one step further in, never in the way.
- **The person owns their practice.** Progress stays in the user's own document. The server
  stores only what other people need to see. Nothing is shared without an action from the user.
- **Safe to open up.** Every feature that lets people reach each other comes with the tools to
  stop abuse of it, before it is turned on for everyone.
- **Cheap on the Pi.** Heavy work happens at build time or in the browser. A feature that needs a
  bigger server is redesigned, not deployed.
- **Behind a flag.** Anything that can break a page ships behind a feature flag an admin can turn
  off without a redeploy.

## Where we are (shipped)

| Area | State |
| --- | --- |
| Skill maps | Five props, 411 skills, researched and validated (`test/catalog.js`) |
| Demos | 104 skills, 168 clips: simulated, solved, hand-driven, rolling, stills; 3D figure |
| Practice | Log, heatmap, combos with transitions and a player, plan with gates |
| Accounts | Username and password, props per user, one admin role |
| Community | Profiles with bio and social links, timeline, likes, flat comments, mentions, bell |
| Posts | Pictures, up to 10 trick chips, one attached combo, Publish from the Combos page |
| Gamification | XP and levels, streak, weekly challenges, badges, Plan as quests |
| Sharing | Streak overlay PNG in five templates and three positions, Web Share, timeline |
| Delivery | Protected `main`, `process_pr` CI with browser tests and screenshots on every PR |

## The plan

Epics are ordered by value to users against risk and cost, and by what each one unlocks for the
next. Sizes are rough: S is a few PRs, M is a few weeks of PRs, L is a quarter or more.

### First cycle (shipped)

| Order | Code | Epic | Size | Shipped |
| --- | --- | --- | --- | --- |
| 1 | RPL | [Replies and threaded comments](epic-rpl/todo.md) | S | #13 |
| 2 | MOD | [Moderators, admin panels and the skill map editor](epic-mod/todo.md) | L | #14 |
| 3 | LNK | [Instagram, TikTok and YouTube links in posts](epic-lnk/todo.md) | M | #15 |
| 4 | GEO | [Next-trick suggestions from the geometry of moves](epic-geo/todo.md) | L | #16 |
| 5 | AVT | [Customisable avatars in demos, combos and stickers](epic-avt/todo.md) | L | #17 |

Then a security and tech debt review (#18), a release process (#20, #21), a CI that runs only the
jobs a change needs (#22), bookmarks (#24), the version on the dashboard (#26) and the drawn body
fitted to the physics with frames (#27).

### Second cycle

Planned on 10 November 2026 from what the docs recorded as debt, weakness or wish, not from a guess
about demand; each epic names the document that raised it in [epics.md](epics.md#second-cycle).
Every epic has one category, which is its label on GitHub:

- **fix**: wrong today, a person can meet it;
- **perf**: faster or lighter, same behaviour;
- **improvement**: debt, structure or quality, better without being new;
- **feature**: something new a person can use.

The order has three stages. "Now" is what a product should not carry for another month:
correctness in production, a legal question, flaky checks and the frame rate of the signature
experience. "Next" is what makes the app coherent and safe to open up. "Later" is the new ground,
each piece standing on something in Now or Next.

| Stage | Code | Epic | Category | Size | Issue | Why here |
| --- | --- | --- | --- | --- | --- | --- |
| Now | AUD | [Close the stage audit](epic-aud/todo.md) | fix | M | [#29](https://github.com/TuanSSM/flowtopia/issues/29) | 46 known faults, one of them every link card in production. Root certificates first and alone. |
| Now | LIC | [Settle the base model's licence](epic-lic/todo.md) | fix | S | [#30](https://github.com/TuanSSM/flowtopia/issues/30) | An open legal question on a file every visitor downloads. Cheap to ask, bounded to swap. |
| Now | TST | [Tests that wait for what they check](epic-tst/todo.md) | improvement | S | [#33](https://github.com/TuanSSM/flowtopia/issues/33) | About 150 fixed sleeps tax every PR of every other epic. Small, and it pays from the first week. |
| Now | FPS | [Demo frames inside the budget](epic-fps/todo.md) | perf | M | [#31](https://github.com/TuanSSM/flowtopia/issues/31) | 58 ms a frame on the Pi is 17 frames a second for the thing Flowa is known for. |
| Next | SHP | [One shape for the app](epic-shp/todo.md) | improvement | M | [#34](https://github.com/TuanSSM/flowtopia/issues/34) | The four structure questions, decided and done before more pages are added to the wrong shape. |
| Next | LOAD | [First paint, bytes and queries](epic-load/todo.md) | perf | M | [#32](https://github.com/TuanSSM/flowtopia/issues/32) | A 1.4 MB figure on every page load; queries never read with EXPLAIN. Gives OFF its service worker. |
| Next | OPS | [Safe beyond the LAN](epic-ops/todo.md) | improvement | M | [#35](https://github.com/TuanSSM/flowtopia/issues/35) | Everything that has blocked opening the instance up: TLS, pinned Postgres, a restore drill, deploys that pull instead of build. |
| Next | A11Y | [Usable by keyboard, screen reader and a shaky hand](epic-a11y/todo.md) | improvement | S | [#36](https://github.com/TuanSSM/flowtopia/issues/36) | Nothing checks focus, names or reduced motion today; cheap while SHP touches every page. |
| Next | CAT | [Skill maps, second research pass](epic-cat/todo.md) | improvement | M | [#37](https://github.com/TuanSSM/flowtopia/issues/37) | 94 unconfirmed skills and mostly unknown motion fields limit maps and suggestions alike. Content work, runs in parallel. |
| Next | LBL | [Open the suggestion gate](epic-lbl/todo.md) | feature | S | [#39](https://github.com/TuanSSM/flowtopia/issues/39) | Suggestions stay "early" until 30 shared pairs exist; nothing asks for a mark. |
| Later | TRU | [Progress the server can vouch for](epic-tru/todo.md) | improvement | L | [#38](https://github.com/TuanSSM/flowtopia/issues/38) | The largest debt: badges and stats are self-reported. Needed before anything reads one person's progress for another. |
| Later | SIM | [Wraps, weaves and passes](epic-sim/todo.md) | feature | L | [#40](https://github.com/TuanSSM/flowtopia/issues/40) | 214 described skills the engine cannot show; the four biggest groups, physics test first. |
| Later | XPR | [Across props](epic-xpr/todo.md) | feature | M | [#41](https://github.com/TuanSSM/flowtopia/issues/41) | One dashboard per person, mixed combos, shared concepts. Needs SHP's one Progress card. |
| Later | OFF | [Practice without a signal](epic-off/todo.md) | feature | M | [#42](https://github.com/TuanSSM/flowtopia/issues/42) | Practice happens where there is no signal. Needs LOAD's worker and TRU's write path. |

By category: fix 2 (AUD, LIC), perf 2 (FPS, LOAD), improvement 6 (TST, SHP, OPS, A11Y, CAT, TRU),
feature 4 (LBL, SIM, XPR, OFF). The split is deliberate: a second cycle that was mostly features
would build on the faults the first cycle's audit found.

### Where the second cycle stands

On 11 November 2026, after pull requests #44 to #85. An item is counted done only when its
definition of done holds; "waits" names the one thing that cannot be done from a pull request.

| Code | Done | What is in | What waits, and for whom |
| --- | --- | --- | --- |
| AUD | 6 of 7 | every audit fix | the audit run again on stage after a deploy (the owner) |
| LIC | 0 of 4 | the question written down, swap candidates listed | the answer on the base model's licence (the owner asks) |
| TST | 5 of 6 | tests wait for what they check; slowest checks in the PR comment | a month of pull requests without a flaky rerun (time) |
| FPS | 2 of 6 | frame time in the PR comment; an unchanged frame is not drawn again; a frame allocates about 40 KB, not 3 MB | the unchanged frame timed on the Pi (2 ms); the lighter mesh waits for LIC, since the model may be swapped; shader skinning cannot be judged in software WebGL |
| SHP | 0 of 6 | nothing | the four structure decisions (the owner); everything else in SHP follows from them |
| LOAD | 1 of 6 | queries indexed and held to their plans; page files under hashed names, compressed | the slowest request read from the Pi's log; the figure on demand, the clip index per prop, the service worker and the first-paint numbers are not started |
| OPS | 2 of 5 | health, request log; Postgres pinned; image built by the release; restore drill | Dependabot's first pull request, a first release and a deploy of it, the drill's first runs (the owner starts workflows). Loopback by default and the proxy recipe were built and withdrawn (#74) |
| A11Y | 4 of 5 | names, forms, reduced motion, contrast, each with a check that runs on every pull request | Focus (keyboard), in work; listening with a real screen reader has not been done |
| CAT | 1 of 6 | the method for confirming a skill | the five research passes: sources have to be opened, which the session that wrote this could not do |
| LBL | 3 of 4 | the question after a run, progress to the gate, the label check, the fit workflow | 30 marked pairs on an instance and the workflow's first run (the owner) |
| TRU | 0 of 6 | the design note | the owner's review and thirteen decisions at its top |
| SIM | 1 of 7 | a tether as a chain that touches the body, with 70 known answers | the chest twist is in work; the weave, wraps, passes and rolling contact follow in that order |
| XPR | 2 of 5 | shared concepts in the maps and the editor; the hint on a skill | the maps name no concept yet (CAT); one dashboard waits for SHP; mixed combos and suggestions across props are not started |
| OFF | 0 of 5 | nothing | LOAD's service worker and TRU's write path |

Found and fixed on the way, outside any checklist: the roadmap and branch badges the page awards
were refused by the server (#71); a bake gave other clip files on another machine, and nothing
checked (#85).

### Dependencies

- **AUD PR 1 before everything.** Root certificates ship alone, first, because production is wrong
  until they do.
- **TST early.** Every later epic's browser checks are written with the wait helper, not sleeps.
- **SHP before XPR and before new pages.** One Progress card and the prop in the URL are what the
  combined dashboard and mixed combos stand on.
- **LOAD before OFF.** The service worker and hashed assets are LOAD's; OFF adds the queue.
- **TRU before OFF's queue is final, and before any coach or group feature.** The write path OFF
  replays is the server-side one.
- **OPS before the instance is reachable from outside the LAN**, whatever the order otherwise.
- **CAT feeds LBL and SIM.** Confirmed descriptions are what a demo and a motion field come from.
- **LIC may change the mesh;** FPS's decimated body is built from whichever model LIC settles on,
  so LIC's answer is wanted before FPS's mesh item.

### Not scheduled, with the reason

- **Splitting `main.go`.** One file on purpose (`.claude/rules/go-server.md`); the size is a cost
  the tooling pays, not the reader.
- **Finger physics, travel steps, pose from video.** Named as weak in [demo-engine.md](demo-engine.md)
  and still out: each is a research project, not an epic with a known shape.
- **Argon2 or another password hash.** PBKDF2 at 600 000 rounds (AUD PR 4) with the hash slots is
  within OWASP's guidance; a new dependency buys little on the Pi.
- **Coaching, groups, events.** Wanted by the community model but every one of them reads one
  person's progress for another, which TRU must make truthful first. They are the first candidates
  for the third cycle.

## How we will know it works

Measured on the instance, from data the server already has or the epic adds. No third-party
analytics.

| Goal | Measure | Target |
| --- | --- | --- |
| People keep practising | Share of active users with a session in 4 of the last 7 days | Hold or improve with each epic, against the level when gamification shipped |
| People talk to each other | Comments and replies per post | Rises after RPL |
| The timeline stays healthy | Reports resolved within 48 hours; posts hidden per 100 | Over 90%; low and stable |
| Suggestions help | Share of suggested next tricks added to a combo, and later marked Works | Over 25% added; over 60% of those Works |
| Demos feel personal | Share of active users who customised an avatar | Over 50% within a month of AVT |
| Demos run smoothly | Frame time of the heaviest avatar on the Pi, from the browser suite | Under 33 ms (58 ms today) |
| Checks are trusted | Reruns of `process_pr` caused by flakes, per month | Zero after TST |
| The audit is closed | Items marked fixed on stage | 46 of 46 |
| The app is light | Compressed bytes before first paint | Under 150 KB (about 1.5 MB today with the figure) |
| Maps are honest | Skills unconfirmed by an opened source | Under 40 (94 today), the rest saying so |

## Definition of done for any epic item

Every checkbox in an epic's todo is done only when, in addition to its own DoD:

- It is merged into `main` through a PR whose `process_pr_result` passed.
- It has a check that fails when it breaks: a Go test, a browser test (`test/browser/*.py`), a
  physics expectation, or a CI step. A check that cannot run is reported as skipped, never passed.
- A user-visible feature is behind a feature flag, has a line in `README.md`, and, if it has a
  page, a screenshot in `test/browser/pages.py`.
- Copy is plain language, with no long dashes, and works at 420 px wide.
- The CSP is not loosened without the PR saying so and why.
- The README, this roadmap and the epic's todo are updated in the same PR.

## Not planned

- **Email, password reset by email, or social login.** No email is stored on purpose.
- **Direct messages.** Moderating private messages on a household server is not worth the risk.
- **Video hosting.** Videos stay on Instagram, TikTok or YouTube; Flowa links to them (LNK).
- **Ads, tracking pixels or third-party analytics.**
- **A native app.** The web app installs as a PWA; native shells add cost without new value.

## From earlier analysis

[multi-prop-architecture.md](multi-prop-architecture.md) left shared concepts, cross-prop views,
storage growth, scheduled backups and HTTPS open. Backups are taken daily (`backup.sh`, #18);
shared concepts and cross-prop views are XPR; storage and the write path are TRU; HTTPS and the
rest of opening up are OPS. Nothing from that note is left unassigned.
