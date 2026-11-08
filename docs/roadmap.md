# Flowa roadmap

Where Flowa is going, in what order, and why. The epics themselves are described in
[epics.md](epics.md); each has a checklist with a definition of done in `docs/epic-<code>/todo.md`.
How they are built, branch by branch, is in [implementation-plan.md](implementation-plan.md).

Last reviewed: 8 November 2026.

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

| Order | Code | Epic | Size | Why now |
| --- | --- | --- | --- | --- |
| 1 | RPL | [Replies and threaded comments](epic-rpl/todo.md) | S | Conversations already happen in flat comments and lose their thread. Cheapest engagement win. |
| 2 | MOD | [Moderators, admin panels and the skill map editor](epic-mod/todo.md) | L | Must land before the community grows or links arrive. Also unblocks fixing skill maps without a redeploy. |
| 3 | LNK | [Instagram, TikTok and YouTube links in posts](epic-lnk/todo.md) | M | People already film their practice elsewhere. Needs reporting and hiding from MOD first. |
| 4 | GEO | [Next-trick suggestions from the geometry of moves](epic-geo/todo.md) | L | Flowa's distinctive feature: the combo builder knows which trick flows out of the last one. |
| 5 | AVT | [Customisable avatars in demos, combos and stickers](epic-avt/todo.md) | L | The most delightful and the most expensive. Builds on GEO's work on clips and on a stable figure. |

### Now: RPL, then the first half of MOD

Replies are small and self-contained. The moderator role, reports and the moderation queue follow
straight after, because LNK and any wider opening of sign-ups depend on them.

### Next: the skill map editor (MOD), LNK and GEO's feature extraction

The editor turns skill-map fixes from a code change into an admin task. LNK adds link cards with
privacy-preserving previews. GEO starts at build time, measuring every baked clip, so its numbers
can be checked long before a suggestion is shown to anyone.

### Later: GEO in the combo builder, then AVT

Suggestions ship only once they beat a simple baseline on the transitions people have already
marked. Avatars start with cosmetic parts (no physics impact), then body presets that the physics
suite has checked.

### Dependencies

- **MOD before LNK.** Link posts bring outside content in; reporting and hiding must exist first.
- **RPL before MOD's queue is complete.** Replies are a reportable object, so the report model
  covers posts, comments and replies from the start.
- **MOD's editor before GEO's catalog fields.** GEO needs plane, direction and entry and exit
  states on skills without clips; the editor is where that content is maintained.
- **GEO's clip features before AVT's body presets.** Both read the same baked clips; the feature
  extractor is the tool that shows a body preset still moves the prop the same way.

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

## Still open from earlier analysis

From [multi-prop-architecture.md](multi-prop-architecture.md): shared concepts across props
(partly covered by GEO), cross-prop combos, storage growth, scheduled `pg_dump` backups and
HTTPS before the app is reachable from outside the LAN. These are not scheduled; they become
blockers the moment Flowa is opened beyond the LAN.
