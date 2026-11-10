# Progress the server can vouch for: design

Today the browser owns a member's progress. It keeps one JSON document per prop, works out XP,
level, streak and badges from it, and tells the server the results, which the server records and
shows to other people. This page is the design note the TRU epic asks for as its first item
([epics.md](epics.md#tru-progress-the-server-can-vouch-for), checklist
[epic-tru/todo.md](epic-tru/todo.md), issue [#38](https://github.com/TuanSSM/flowtopia/issues/38)):
the tables, the rules moved to the server, the migration, the flag and allowlist, and how the
document and the tables stay in step during the transition.

**It is a proposal waiting for the owner's review.** Nothing in it is built, no code changed with
it. Of its fourteen questions thirteen are open (Q4 was a bug and has been fixed since, in #71).
They come first, because the rest depends on them.

The first half (sections 1 to 5) describes the code as it is. Code is named by file and by
function, constant or SQL name, never by line, so the note does not go stale with the next edit.
Every name was looked up with grep on `main` on 10 November 2026 (commit `71f0e51`: 7 migrations,
15 flags, 31 fixed badge keys). The second half (section 6) is the proposal.

## Decisions the owner is asked for

Each has the options and a recommendation. Later sections refer to them as Q1 to Q14.

**Q1. Gates, combo runs and notes.** They feed XP, seven fixed badges and the roadmap badge of each
prop, but they are not in the epic's two tables (finding 1, section 2.1).

- (a) Add two small tables, `plan_gates(user, prop, gate, day)` and
  `combo_runs(user, prop, combo, runs, clean)`, written by their own rate-limited actions.
- (b) Leave them in the document and let the Go rules read them from there.
- (c) Stop counting them in public numbers.
- Recommended: (a), as a seventh todo item after "Behind the flag". Until then (b), with this note
  saying plainly that the gates and combos parts of XP are still declared. With (b) alone the epic's
  "nothing a person sees about another person is self-declared" is not met: 1,000 ready combos with
  20 clean runs each fit in one document and are 350,000 XP.

**Q2. The 300-entry history cap makes XP fall** (finding 3). Keep the cap in the Go rules for good,
or lift it at the switch?

- Recommended: rows are never cut; the rules look at the newest 300 until pull request 7, so Go
  equals JS; lift the cap there and say so in the release notes. It is a rule change, so it needs
  the owner's yes (the epic says "Out of scope: changing what earns XP").

**Q3. The Reverse variant counts as the weak hand** for a left-dominant member (section 2.4). Copy
the quirk into Go, or fix it in the page first?

- Recommended: fix it in pull request 2 (count a weak-hand level-up only on `LR` skills), with the
  report listing who loses a challenge.

**Q4. Per-prop badges** (finding 2, section 3.2). Should the server award them, as the README says?

- Settled: yes. It was a bug against what the README promises, so it was fixed without waiting for
  this review ([#71](https://github.com/TuanSSM/flowtopia/pull/71)). Members who had already
  completed a branch or a roadmap get those badges, and one milestone post, late. The question
  keeps its number so that the others keep theirs.

**Q5. Bounds on what a member may log.** Today the server has none.

- Options: only what the form already asks for (1 to 600 minutes, not in the future); plus a day
  total of 1,440 minutes; plus a limit on backdating.
- Recommended: the first two, and no limit on backdating, but show moderators when a session was
  logged (`created_at`).

**Q6. The `share-streak` badge** is earned by downloading a picture, which the server never sees.

- Options: retire it for new members; award it only when the streak is posted to the Flowa timeline;
  keep a small "I shared" action.
- Recommended: the second. Members who hold it keep it.

**Q7. The streak post** carries a number in free text and in a picture the page draws
(`shareCaption()` and `shareStreak()` in `web/game.js`), and the last todo item says "the feed
renders no self-declared number".

- Recommended: a post kind `streak` whose number the server fills in and the page draws as a card.
  Nothing can stop a member typing a number into an ordinary post.

**Q8. The roadmap principle** "Progress stays in the user's own document. The server stores only
what other people need to see" ([roadmap.md](roadmap.md), "The person owns their practice"). This
epic contradicts it. Reword it, or keep session notes out of the table?

- Recommended: reword it to what stays true (nothing is shown to others without an action from the
  member). The notes are in the same database either way. This pull request leaves the roadmap as it
  is until the owner has answered.

**Q9. Endpoint paths.** The todo's flat `POST /api/sessions` and `PUT /api/levels`, or nested under
`/api/progress/{prop}/`?

- Recommended: nested, because `rev` is per prop.

**Q10. Time zone.** Store an IANA zone per member and embed Go's `time/tzdata` (about 450 KB in the
binary, by the package's own documentation), or use one zone for the whole instance?

- Recommended: per member, with an instance default `FLOWA_TZ` as the fallback.

**Q11. Import and reset.** Should imported sessions and levels count toward public numbers?

- Recommended: yes, marked `source = 'import'` and visible to moderators. Refusing them would change
  what earns XP.

**Q12. Removing the page's rules entirely** means that offline (epic OFF) the page shows the last
numbers the server gave it, not live ones. Acceptable, or keep a display-only copy?

- Recommended: accept it. A second copy of the rules is what this epic removes.

**Q13. Who may see the per-member report and switch a member's mode:** admins only, or moderators
too (they already hold `badge.revoke` and `dashboard.read`)?

- Recommended: admins only.

**Q14. When to do pull request 8**, the one step with no way back: with the switch, or a release
later?

- Recommended: at least one release later, after a restore drill on the new shape.

## What was found on the way

Seven things change the design and are not in the epic or its checklist.

1. **The epic's two tables do not cover what feeds XP and badges.** Gates, combos and notes stay in
   the document and feed XP (`propXP()` in `web/game.js`), seven fixed badges (`notes-10`,
   `gates-1`, `gates-5`, `combo-1`, `combo-ready`, `runs-10`, `runs-50`) and the roadmap badge of
   each prop. One member can still declare any XP through combos alone: 350 XP per combo, and no cap
   on the number of combos. See section 2.1 and Q1.
2. **Per-prop badges were refused by the server** (fixed in #71). `propBadges()` in `web/game.js`
   makes the keys `gates:<prop>` and `branch:<prop>:<k>`. They were not in `badgeKeys` in `main.go`,
   so `handleAchievements` answered 400, and `test/badges.js` could not see them, because it read
   quoted keys only. The README promises them ("plus one per branch and one per roadmap for each of
   the user's props"). See section 3.2 and Q4.
3. **XP can fall with no action by the member.** The level history is cut to 300 entries per prop
   (`setLevel()` in `web/app.js`, `progress()` in `web/merge.js`), and `challengeXP()` in
   `web/game.js` recomputes challenge XP from that history for every past week. See Q2.
4. **The profile streak is stale by construction.** `syncGame()` in `web/game.js` writes the stats
   only while the owner's own page is open, so a streak that ended still shows on the profile.
5. **The server has no time zone data.** The image is `FROM scratch` (`Dockerfile`), and `main.go`
   has neither `time.LoadLocation` nor an import of `time/tzdata`. Every streak and week rule uses
   the browser's local zone. See Q10.
6. **`PUT /api/me` and `PUT /api/progress/{prop}` have no rate limit.** Neither `handlePutMe` nor
   `handlePutProgress` in `main.go` asks a limiter.
7. **`testdata/backups` holds `flowa-schema5.dump` and `flowa-schema6.dump` only**, and `migrations`
   in `main.go` has 7 entries. The next migration needs `flowa-schema7.dump` first: the rule in
   `.claude/rules/go-server.md` is to keep a backup of the schema before yours
   (`test/restore.sh --fixture` on the commit before the change).

### How the two main claims were checked

**Finding 2, by reading the code and by a script; not reproduced in a browser.** The lines below
describe the tree before the fix in #71.

- `allBadges()` in `web/game.js` is the fixed list `ACH` followed by `propBadges()`, and
  `syncGame()` sends every newly met key of both through `grant()` to `POST /api/achievements`.
- `handleAchievements` in `main.go` walks the keys in order and answers 400 ("There is no badge
  called ...") at the first key for which `badgeKeys[k]` is false. `badgeKeys` is a fixed map of 31
  keys, and none of them contains a colon.
- A short script read `badgeKeys` and the pattern `achKeyRE` out of `main.go` and tried the keys the
  page makes: `gates:poi` is not in the list; `branch:poi:F` is not in the list and does not match
  the pattern either, because the branch letter is a capital. The five shipped catalogs give the
  page 51 such keys (5 roadmaps and 46 branches).
- `node test/badges.js` passes all the same. Its one line of output on this tree:
  `badges: 31 keys, the same in web/game.js and main.go`. It finds keys written as
  `{ key: '...' }` or through `achTiers('...')`, and the per-prop keys are template strings.
- No Go test and no browser test sends a per-prop key (`db_test.go` sends `streak-100`,
  `sessions-1`, `sessions-10` and `made-up-badge`).

**Finding 6, by listing every use of the limiter in `main.go`.** `allowed(...)` is called by
`handleTransitionLabels`, `handleSuggestUsage`, `handleAchievements`, `handleCreatePost`,
`handleLike`, `handleBookmark`, `handleComment` and `handleLikeComment`; the `a.lim.*` allowances
are used directly by `handleSignup`, `handleSignin` and `handlePassword`. `handlePutMe` and
`handlePutProgress` are not in either list, and the wrappers their routes pass through (`authed`,
`guard`, `secure`) check the session, the origin and the host, not a rate. The body size is capped
(200 KB for `PUT /api/me`, `maxProgress` for a progress document); the number of requests is not.

## 1. The progress document today

### 1.1 Shape as the page writes it

`blank()` in `web/app.js`:

```js
const blank = () => ({ version: 1, skills: {}, sessions: [], combos: [], gates: {}, events: [], settings: { dominant: 'R', trackStart: '', event: '', rulesChecked: '' } });
```

`normalize()` in `web/app.js` fills missing parts and forces `sessions`, `combos` and `events` to
arrays and `skills` and `gates` to objects. `version` and `settings.rulesChecked` are written and
never read: `blank()` is the only place either name appears in the page.

| Field | Shape | Written by |
| --- | --- | --- |
| `skills` | `{ [skillId]: { <variant>: 0..4, updated: ISO string, notes: string } }` | `setLevel()`; notes by the `note` case of the `change` listener, both in `web/app.js` |
| `events` (the level history; not called `history`) | newest first, `{ t: ISO, id, side, from, to }`, `side` is `''` for the single variant; cut to 300 | `setLevel()`; cut again by `progress()` in `web/merge.js` |
| `sessions` | `{ id: uid(), date: 'YYYY-MM-DD', minutes: int, focus: [skillId], notes: string }` | the `log-form` submit handler (append) and the `session-del` action (delete by id) in `web/app.js` |
| `combos` | `{ id, name, role, seq: [], reps: [], tr: { 'A>B': 'works'/'bridge'/'blocked'/'untested' }, runs, clean, suggested?: ['A>B'] }` | the `combo-form` submit handler and the `combo-*` actions in `web/app.js`; `nt-add` and `nt-bridge` in `web/nexttrick.js` |
| `gates` | `{ [phaseOrWeekId]: 'YYYY-MM-DD' }`, deleted when unticked | the `gate` case of the `change` listener in `web/app.js` |
| `settings` | `dominant` 'L' or 'R', `trackStart`, `event`, `rulesChecked` | the `dominant` action and the `setting` case in `web/app.js` |
| `noFit` (optional) | `{ 'A>B': true }`, suggestions the member hid | the `nt-no` action in `web/nexttrick.js` |
| `shared` (optional) | `{ 'A>B': mark }`, the marks last sent to the shared counts | `ntSync()` in `web/nexttrick.js` |

- The epic's column names differ from the document's: `focus` (epic: `skills`) and `notes` (epic:
  `note`).
- Ids come from `uid()` in `web/app.js`: `Date.now().toString(36)` plus four characters from
  `Math.random()`.
- The session form (`pagePractice()` in `web/app.js`) bounds minutes to 1 to 600 and the date to
  today or earlier, in HTML only. Nothing on the server checks either.

### 1.2 Levels per variant

- `TRACKS` in `web/catalogs.js`:
  `{ one: [['O','Overall']], LR: [['L','Left hand'],['R','Right hand']], FR: [['F','Forward'],['R','Reverse']] }`.
  An unknown track falls back to `one` (`variants()`).
- **`R` means Right on an `LR` skill and Reverse on an `FR` skill.** Only the catalog's `track`
  tells them apart.
- Readers: `lv`, `levels`, `best`, `worst` in `web/app.js`. The level names 0 to 4 are `LEVELS` in
  `web/data.js`.
- `!` in `needs` and in a phase's `req`: `needMet()` in `web/app.js` asks `worst(id) >= 2` with
  `!`, else `best(id) >= 2`.
- Prerequisites are display only. A locked skill can still be levelled (the skill panel says "They
  are a suggested order, so you can still log progress"), so no rule the server must own reads
  `needs`.
- Shipped catalogs, counted from `web/catalog/*.json`: nunchaku 109 skills (70 `LR`, 39 `one`), poi
  90 (50 `one`, 30 `LR`, 10 `FR`), staff 84, double staff 72, buugeng 56. Only nunchaku has `weeks`
  (7). No skill is retired and there are no aliases yet.

### 1.3 `state`, `commit()`, `save()`

- Globals in `web/app.js`: `state, rev, dirty, saving, saveTimer, syncState, loaded`. Per-prop
  copies are `docs[prop] = { rev, state }`, and `synced[prop]` is the JSON the server last had, the
  merge base (both in `web/account.js`).
- `commit()` sets `dirty` and saves after 350 ms.
- `save()` sends `PUT api/progress/<prop>` with `{"rev":n,"state":{...}}`, the whole document every
  time. A 401 signs out; a 409 merges (section 4); a network error retries every 5 s. After a clean
  save it calls `syncGame()` and `ntSyncAll(false)`.
- Load: `loadSession()` in `web/account.js` fetches `GET api/me`, then one `GET api/progress/<prop>`
  per chosen prop, and applies catalog aliases in the browser (`applyAliases()` in
  `web/catalogs.js`), saving the result back.
- The `visibilitychange` listener in `web/app.js` reloads everything when nothing is unsaved.
- Whole-document operations, all in the listeners of `web/app.js`: `export`, `import` (replaces
  `state`) and `reset` (`state = blank()`).

### 1.4 Server storage

- Table `progress(user_id, prop, rev BIGINT, state JSONB, updated_at, PRIMARY KEY (user_id, prop))`,
  in the `schema` constant of `main.go`.
- `GET /api/progress/{prop}` (`handleGetProgress`) returns `{rev, state}`, or `{rev:0, state:{}}`
  when there is none (`getProgress`).
- `PUT /api/progress/{prop}` (`handlePutProgress`):
  - Needs a session (`authed`) and a known prop (`propOf`). No flag, no limiter.
  - The whole body is capped at `maxProgress`, 512 KB, by `readJSON`.
  - The only validation is that `state` is an object.
  - `rev == 0` inserts with `ON CONFLICT DO NOTHING`; otherwise
    `UPDATE progress SET rev = rev + 1 ... WHERE ... AND rev = $4`.
  - No row changed means 409 with the current `{rev, state}`. Success returns `{rev}`.
- Daily backup: `backup()` runs before every PUT and writes `data/backups/progress-DATE.json` once a
  day, documents only, keeping 14 (`keepBackups`). The date is the container's, which is UTC.
- `backup.sh` takes the full `pg_dump`.
- Tests: the over-size case is in `TestDBHardening`. The 409 flow is tested only in the browser (the
  two-device check in `test/browser/combo.py`), not in Go.

### 1.5 The name `sessions` is taken

`sessions(token_hash, user_id, created_at, expires_at)` is the sign-in table (`schema` in
`main.go`), plus `last_seen` from migration 3. It is read by `housekeep`, the dashboard's active
count (`dashActiveSQL`), the accounts list (`handleUsers`), the `suspend` action of
`handleModAction`, and migration 5.

**Proposal: `practice_sessions`.** Also `skill_levels` and not `levels`, because "level" already
means the XP level in `users.stats.level`.

## 2. Rules the page computes that the server would have to own

All in `web/game.js` unless said. No rule uses randomness, tiers, clip data or feature flags. The
`game` flag only decides whether `syncGame()` runs (`save()` and `load()` in `web/app.js`) and
whether the endpoint exists (`flagged("game", ...)` in `socialRoutes`).

The catalog parts the rules need are tracks, live (not retired) skills, the branch letter (the first
character of the id, set in `addCatalog()` in `web/catalogs.js`), phase ids and aliases. The server
already has them: published revisions in `catalog_revisions`, read by `latestCatalog` and seeded at
start by `seedCatalogs`.

### 2.1 XP

Constants: `XP_SKILL = [0,10,40,100,200]` and
`XP = { session:10, minuteCap:120, gate:150, week:40, comboReady:50, cleanRun:15, cleanRunCap:20, challenge:40 }`.

`propXP()`, per prop:

| Part | Rule | Reads |
| --- | --- | --- |
| skills | `XP_SKILL[level]` per variant of each live skill | `state.skills`, catalog tracks |
| practice | per session, `10 + min(Number(minutes) or 0, 120)` | `state.sessions` |
| gates | per key in `state.gates`: 150 if it is a phase id, else 40 | `state.gates`, catalog phases |
| combos | per combo, 50 if ready, plus `15 * min(clean, 20)` | `state.combos` |

`xpAll()` sums over `myProps()` and adds challenge XP.

Things the server would inherit or have to decide:

- `myProps()` in `web/account.js` is the props chosen in the profile. Removing a prop from the
  profile removes its XP.
- A session of 0 minutes still earns 10 XP. Negative or non-numeric minutes in a crafted document
  pass straight through.
- Any junk key in `gates` earns 40 XP.
- A fractional level such as 2.5 earns no skill XP but counts as clean (`best() >= 2`).
- There is no cap on sessions per day or on the number of combos.
- "Ready" is `comboReady()` in `web/app.js`: two or more skills, every transition marked `works`.

### 2.2 Levels

`levelNeed = n => 50*(n-1)*n`, `levelInfo()`, and the titles in `LEVEL_TITLES` and `levelTitle()`.
A pure function of XP.

### 2.3 Streak

- `streak(m)` in `web/app.js`: start at the browser's today; if today has no minutes, start at
  yesterday; count back while a day has minutes.
- The only grace is that a day not yet logged does not break the streak. There is no freeze.
- `streakInfo()`: minutes per `date` across every prop; `best` is the longest run of consecutive
  days with more than 0 minutes; `atRisk = current > 0 && !today`; `week` is seven dots from Monday.
- A day is the string the member put in the date field, with no zone attached. "Today" and "this
  week" come from the browser's clock and zone (`dayKey()` in `web/app.js`, `weekStart()` and
  `new Date()` in `streakInfo()`).
- Sessions can be backdated without limit, so a streak can be filled in afterwards.

### 2.4 Weekly challenges

- `CHALLENGES`: practise on 4 days, log 120 minutes, raise 3 skill levels, level up a weak-hand
  skill.
- `weekStats()`:
  - sessions are bucketed by the Monday of `date`, only when it matches `^\d{4}-\d\d-\d\d$` and has
    more than 0 minutes;
  - events are bucketed by the Monday of `new Date(e.t)` in the browser's zone;
  - an "up" is any event with `to > from`.
- `challengeXP()` gives 40 XP per challenge done in every week ever, and counts the weeks with all
  four done.

Quirks the Go port must copy or the owner must fix:

- The weak hand is taken from the current `settings.dominant` (`allEvents()`), not from the setting
  at the time of the event.
- For a left-dominant member the weak side is `'R'`, and `setLevel()` records the Reverse variant of
  an `FR` skill with side `'R'` too, so levelling it counts as a weak-hand level-up (Q3).
- Raising and lowering the same skill repeatedly counts as several ups.
- The 300-entry cap silently removes old weeks' challenge XP (finding 3).

### 2.5 Badges

The registry `ACH` has 31 fixed keys; `propBadges()` adds the per-prop ones. The numbers they are
measured against come from `gameContext()`.

| Keys | Condition |
| --- | --- |
| `sessions-1/10/50/200` | number of sessions on the member's props, 0-minute ones included |
| `hours-10/50/100` | total minutes / 60 |
| `props-2` | distinct props with a session of more than 0 minutes |
| `streak-3/7/30/100` | `best` streak |
| `week-5` | most practice days in one Monday week |
| `challenges-1/4` | weeks with all four challenges done |
| `clean-1/10/50/100` | live skills with `best >= 2` |
| `perf-1` | a live skill with `best >= 4` |
| `both-5/20` | live `LR` skills with `worst >= 2` |
| `notes-10` | live skills with a non-empty note |
| `gates-1/5` | phase gates ticked |
| `combo-1` | a combo of 2 or more skills |
| `combo-ready` | a ready combo |
| `runs-10/50` | sum of `clean` over combos |
| `first-post`, `share-streak` | manual, granted by an action: the post form in `web/social.js`, `shareStreak()` in `web/game.js` |
| `gates:<prop>` | every phase of that prop ticked |
| `branch:<prop>:<k>` | every skill in use of the branch clean. Until #71 the target counted retired skills and the count did not, so a branch with a retired skill could never complete. |

- `badgeStates()` shows a badge as on when it is recorded or its number is met.
- `syncGame()` posts newly met keys and then the stats.
- `share-streak` is granted for downloading a PNG, which the server cannot see (Q6).

### 2.6 `test/badges.js`

It reads `web/game.js` and `main.go` as text and fails when the fixed keys in `ACH` differ from
`badgeKeys` in Go, or when fewer than 20 are found. Run on this tree it prints
`badges: 31 keys, the same in web/game.js and main.go`. It checks keys only, not conditions. It was
blind to the per-prop keys (finding 2); since #71 it also compares the kinds of per-prop key the
page makes (`gates`, `branch`) with the kinds `propBadge` in `main.go` takes. CI runs it in the `lint` job of
`.github/workflows/process_pr.yml`.

### 2.7 Browser clock and zone, in one list

In `web/app.js`: `dayKey()` and `parseDay()`, `streak()`, `weekMinutes()`, the stamp `setLevel()`
puts on a level change, the date the `gate` case writes, and the plan week in `pagePlan()`. In
`web/game.js`: `weekStart()`, `streakInfo()`, `weekStats()`, `thisWeekKey()`. In `web/social.js`:
`suggestedTags()`.

## 3. What the server accepts as self-reported

| Endpoint | Handler | Validates | Limit | A dishonest client can |
| --- | --- | --- | --- | --- |
| `PUT /api/me` with `stats` | `handlePutMe` | `validStats`: at most 2000 bytes, 40 keys, key length 30, values numbers or booleans. No ranges, no relation between `level` and `xp`. Stats do not move the profile `rev` (`profileRev`). | none | claim any XP, level, streak, best, clean, minutes, sessions and badge count, and top the people list |
| `POST /api/achievements` | `handleAchievements`, behind `flagged("game", ...)` | refuses muted members (`muted`); the key must be in `badgeKeys`; at most 60 keys (`maxAchKeys`); skips keys in `badge_blocks` | `a.lim.badges`, 60 an hour | take all 31 badges in one request, each with a notification, plus one milestone post when `autoPost` and `community` are on |
| `PUT /api/progress/{prop}` | `handlePutProgress` | size, and "is an object" | none | write any levels, sessions, gates and combos; `cleanSkills` shows other members up to 200 "clean" skills per prop from it |
| `POST /api/suggest/labels` | `handleTransitionLabels` | known prop, at most 500 deltas, id format, consent | `a.lim.marks`, 120 an hour | add 1 to one pair 500 times in a request; marks are never checked against the member's own document. Outside TRU's scope, but the same pattern. |

### 3.1 Where a self-declared number is rendered

- People list: `peopleCard()` in `web/social.js` (level, streak, XP), ordered by `stats->xp` in
  `peopleSQL` and sorted again in `handleUserList`.
- Profile: `pageUser()` in `web/social.js` (level, XP, streak and best, clean skills, and the badge
  list).
- Milestone posts: the `milestone` branch of `postCard()` in `web/social.js`. `milestone` is the
  only post kind besides the ordinary post (`handleAchievements` inserts it; `handleCreatePost`
  leaves the kind at its default). There is no level-up post.
- Bell: the `achievement` notification (`notifText()` in `web/social.js`).
- Streak share to the timeline: an ordinary post whose text and picture carry the streak number,
  both made in the browser (`shareCaption()` and `shareStreak()` in `web/game.js`).
- The [README](../README.md) states the model in its Gamification section, and the comment that
  opens the community section of `main.go` says the same: "achievements and the stats shown on
  profiles are reported by the client and only recorded here".

### 3.2 Bug, fixed in #71: per-prop badges were refused

This is how it stood on the tree the note was written from. Once a member completed a branch or a
roadmap, `syncGame()` put `branch:...` or `gates:...` in the batch. Reading the code (how this was
checked is under finding 2 above):

- `handleAchievements` inserts the fixed keys that come before it, each with its notification, then
  answers 400 at the unknown key.
- No milestone post is made for that batch.
- `grant()` in `web/game.js` returns on `!r.ok`, so the page does not record the answer and sends
  the batch again after every save.
- `achKeyRE` (`^[a-z0-9][a-z0-9:_-]{0,60}$`) also rejects the capital branch letter, so the
  `revoke_badge` action could not revoke a branch badge either.

It was not reproduced in a browser before the fix, and no test covered it.

The fix ([#71](https://github.com/TuanSSM/flowtopia/pull/71)): `propBadge` in `main.go` takes
`gates:<prop>` for a known prop whose map has a roadmap and `branch:<prop>:<K>` for a branch with a
skill in use; every key of a batch is checked before any is recorded; `achKeyRE` allows the capital
letter. `TestDBHardening` and `TestDBModeration` cover it, and a browser run showed the batch
answered 200 with both badges on the account.

### 3.3 Moderation hooks that exist

- Permission `badge.revoke` (`permBadgeRevoke`), held by moderators and admins (`rolePerms`).
- `handleModAction`:
  - `revoke_badge` deletes the achievement and inserts into `badge_blocks`. A reason is required.
  - `hide_stats` and `show_stats` flip `users.stats_hidden`.
  - Every action checks `outranks`, is audited (`a.audit`) and notifies the member (`a.notify`).
- Hidden stats are blanked in `public()`, ranked as 0 by `peopleSQL`, and the clean list is withheld
  in `handleUserProfile`.
- Tests: `TestDBModeration` (revoke, block, hide) and `TestDBHardening` (an invented key, a muted
  member).
- What is missing:
  - No "reset stats" action exists.
  - Nothing removes a row from `badge_blocks`.
  - `revoke_badge` has no button; it appears only as an audit label (`ACTION_NAMES` in
    `web/mod.js`). Hide stats and Show stats do have one (`adminAccounts()` in `web/mod.js`).

## 4. Merge and conflict machinery

### 4.1 What exists

- **Progress `rev`**: one counter per (user, prop), compare-and-swap in the `UPDATE` of
  `handlePutProgress`.
- **The 409 path** in `save()` (`web/app.js`): `theirs` is the server's copy, `base` is
  `synced[prop]`, `mine` is the local state; it calls `progressMerge.progress(base, mine, theirs)`
  and saves again after 50 ms.
  - After three clashes in a row, or with no base, the local edit is dropped with a toast. That is
    a path on which data is lost.
  - Another prop's document is merged in the background and saved when that prop is opened again.
- **`web/merge.js`**, a three-way merge:
  - objects merge key by key (`merge3`);
  - a deletion holds unless the other side changed the thing;
  - lists of objects with ids merge by id (sessions, combos), lists without ids merge by content
    (the level history) (`keyer`, `list`);
  - plain lists such as a combo's `seq` are replaced;
  - the same field changed on both sides keeps this device's value;
  - events are sorted again and cut to 300 (`progress`).
  - `test/merge.js` has the cases: "one skill, same hand" keeps mine, "sessions added on both", the
    "session deleted ..." cases, and "history cap".
- **Profile `rev`**: 16 hex characters of SHA-256 over `[bio, links, prefs]` (`profileRev`,
  `hashOf`). It is optional in `PUT /api/me`; a mismatch is a 409 with the current account. Stats
  and `seen` are left out on purpose (the comment on `profileRev`; `TestDBProfileRev`).
- **Idempotency keys**: `once` in `main.go`, a key per member and path, kept in memory for 10
  minutes (`onceWindow`), at most 5000 (`maxOnce`), forgotten at restart. Used by
  `handleCreatePost` and `handleComment`; the page side is `onceKey()` in `web/social.js`. Progress
  saves do not use it.
- **Two devices today**: each device saves the whole document; the one that saves second gets a
  409 and merges in the page. Covered by the two-device check in `test/browser/combo.py`.
- `ntSyncAll()` in `web/nexttrick.js` saves other props' documents directly and ignores a 409.

### 4.2 What changes with rows

- A session is identified by its id, so adding one on two devices is never a conflict. Replays need
  idempotency that outlives a restart and the 10-minute window, which `once` cannot give. OFF
  replays hours later ([epic-off/todo.md](epic-off/todo.md), "The write queue").
- A level change is a compare-and-set on (skill, variant). "Same skill on both devices" becomes
  something the server detects and can name, which OFF's "asks about the same one" needs
  ([epic-off/todo.md](epic-off/todo.md), "Two devices").
- The page's merge keeps working for what stays in the document (notes, combos, gates, settings,
  `noFit`, `shared`). For sessions, levels and history the server's copy wins and the page applies
  its unsent operations on top.
- Deleting a session needs a tombstone, or a replayed add brings it back.
- Import, reset and alias moves are whole-document changes today and need a row path.

## 5. Consumers that read progress for something other than the owner's pages

| Consumer | Where | Reads today | New source |
| --- | --- | --- | --- |
| Dashboard weekly session counts | `dashWeeksSQL` unpacks `state->'sessions'`; `TestDBDashboard` inserts documents by SQL | document | `practice_sessions` by `day`, with an index; the test must write rows |
| Dashboard "active" and accounts "last active" | `dashActiveSQL`, `handleUsers` | `progress.updated_at` | unchanged if every row write also touches `progress` |
| People list by XP | `peopleSQL`, `handleUserList`; plan checked by `TestDBQueryPlans` | `users.stats` (client) | `users.stats` written by the server only |
| Profile numbers and badges | `handleUserProfile`; `pageUser()` in `web/social.js` | `users.stats`, `achievements` | same columns, written by the server |
| Clean skills for the avatar on a profile | `cleanSkills` | `state->'skills'` | `skill_levels WHERE level >= 2` |
| Catalog publish safety and diff | `progressOn`, used by `safeToPublish` and `handleCatalogDiff` | keys of `state->'skills'`, which include records that hold only a note | `skill_levels`, plus the document for notes |
| Catalog aliases | the page, at load (`applyAliases()` in `web/catalogs.js`) | document | rows must move when a catalog with a new alias is published (`publishCatalog`) |
| Milestone posts and badge notifications | `handleAchievements` | keys the page sent | keys the Go rules award |
| Streak share post, overlay, "at risk" bell and card | `streakCanvas()` to `shareStreak()` in `web/game.js`; `pageTimeline()` and `bellHTML()` in `web/social.js` | page rules | server summary; depends on the member's zone |
| Composer's suggested tags | `suggestedTags()` in `web/social.js` | `allSessions()`, `allEvents()` | the page's read copy of the rows |
| Next-trick suggestions | `readiness()`, `myMarks()`, `ntSync()` in `web/nexttrick.js` | levels for readiness; `combos[].tr`, `noFit`, `shared` | levels from the read copy; the rest stays in the document |
| Combo bookmarks | the ref is the combo id (`bookmarkRef`, `comboIDRE`); resolved in the page (`pageBookmarks()` in `web/social.js`) | `state.combos` | unaffected: combos stay in the document |
| Combo attached to a post | the `postCombo` type | copied by the page | unaffected |
| Export, import, reset | the `export`, `import` and `reset` cases in `web/app.js` | `state` | export assembled from document plus rows; import and reset through the row path |
| Daily backup made by the app | `backup()`; the README's backups section | documents only | must include the rows once documents stop carrying them |
| Restore drill | `test/restore.sh`, steps 1 and 4 | a progress document | must also write and compare rows |
| Account deletion | `handleDeleteUser` | cascade | new tables need `ON DELETE CASCADE` |
| Plan page XP per phase, "How XP works" | `pagePlan()` in `web/app.js`, `pageAchievements()` in `web/game.js` | page constants | server summary once the page's rules go |

There are no leaderboards beyond the people list, and no challenge between members: weekly
challenges are per member.

## 6. Proposed design

### 6.1 Principles

1. One revision counter per (user, prop), the existing `progress.rev`, orders every write, whether
   to the document or to rows.
2. The server keeps the document and the tables in step, inside one transaction per write. The page
   never has to make two requests succeed.
3. Until the last pull request the stored document always holds a complete copy in the old shape.
   So an old tab, an old image after `./deploy.sh --rollback`, the JS rules and every consumer in
   section 5 keep working.
4. The self-report endpoints become the comparison probe during the transition, then go.

### 6.2 Tables (migration 8, additive only)

```sql
CREATE TABLE IF NOT EXISTS practice_sessions (
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  prop TEXT NOT NULL,
  id TEXT NOT NULL,                 -- the page's uid, or made by the server; also the replay key
  day DATE NOT NULL,                -- the practice day as the member wrote it: a calendar day, no zone
  minutes INT NOT NULL CHECK (minutes BETWEEN 0 AND 1440),
  skills TEXT[] NOT NULL DEFAULT '{}',
  note TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT 'page' CHECK (source IN ('page','document','import')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),   -- when the server saw it
  deleted_at TIMESTAMPTZ,                          -- a tombstone, so a replay cannot bring it back
  PRIMARY KEY (user_id, prop, id));
CREATE INDEX IF NOT EXISTS practice_sessions_day ON practice_sessions (day) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS practice_sessions_user_day ON practice_sessions (user_id, day) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS skill_levels (
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  prop TEXT NOT NULL, skill TEXT NOT NULL,
  variant TEXT NOT NULL CHECK (variant IN ('O','L','R','F')),
  level SMALLINT NOT NULL CHECK (level BETWEEN 0 AND 4),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, prop, skill, variant));
CREATE INDEX IF NOT EXISTS skill_levels_skill ON skill_levels (prop, skill);

CREATE TABLE IF NOT EXISTS level_events (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  prop TEXT NOT NULL, skill TEXT NOT NULL, variant TEXT NOT NULL,
  from_level SMALLINT NOT NULL, to_level SMALLINT NOT NULL,
  at TIMESTAMPTZ NOT NULL,          -- when it happened
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  source TEXT NOT NULL DEFAULT 'page',
  UNIQUE (user_id, prop, skill, variant, at, from_level, to_level));
CREATE INDEX IF NOT EXISTS level_events_user_at ON level_events (user_id, at DESC);

ALTER TABLE progress ADD COLUMN IF NOT EXISTS tables_rev BIGINT NOT NULL DEFAULT 0; -- the rev at which the tables last agreed with the document
ALTER TABLE users ADD COLUMN IF NOT EXISTS progress_server BOOLEAN NOT NULL DEFAULT false; -- the allowlist
ALTER TABLE users ADD COLUMN IF NOT EXISTS tz TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS stats_server JSONB NOT NULL DEFAULT '{}';
CREATE TABLE IF NOT EXISTS progress_reports (
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  prop TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL,   -- conversion, mirror, rules
  at TIMESTAMPTZ NOT NULL DEFAULT now(), same BOOLEAN NOT NULL, detail JSONB NOT NULL DEFAULT '{}',
  PRIMARY KEY (user_id, prop, kind));
```

Why these choices:

- **A history table beside the current levels.** The epic's
  `levels(user, prop, skill, variant, level, at)` cannot answer the weekly challenges, which count
  ups by week (section 2.4).
- **The `UNIQUE` on `level_events`** is how `web/merge.js` already identifies history entries (by
  content), and it makes conversion and replays idempotent.
- **Variant letters are stored as the document has them**, the two meanings of `R` included. Never
  read a hand from `R` without the catalog's track.
- **`minutes` is wider in the table (0 to 1440) than in the endpoint (1 to 600)**, so old rows fit.
- Before this migration: run `test/restore.sh --fixture` on the commit before, and commit
  `testdata/backups/flowa-schema7.dump` (finding 7).

### 6.3 Endpoints

Nested under the existing progress route, so `propOf` and the per-prop `rev` apply. This departs
from the todo's flat `POST /api/sessions` and `PUT /api/levels` (Q9).

**`POST /api/progress/{prop}/sessions`**

```json
{ "rev": 41, "sessions": [ { "id": "mhb3k2a9x1b2", "date": "2026-11-10", "minutes": 25, "skills": ["F5","F7"], "note": "left hand drops" } ] }
```

- 200: `{ "rev": 42, "sessions": [...stored rows...], "replayed": [], "unlocked": ["sessions-10"], "game": {...} }`
- 1 to 50 sessions per request; 50 also serves import.
- Checks: the id matches `^[a-z0-9_-]{1,64}$` (as `comboIDRE` in `main.go`); the date is
  `YYYY-MM-DD` and not later than tomorrow in UTC; minutes 1 to 600; at most 12 skill ids matching
  `skillIDRE`; a note of at most 2000 characters through `visibleText`.
- An id that already exists, deleted or not, with the same content is a replay and changes nothing.
  With different content it is a 409 carrying the stored row.

**`DELETE /api/progress/{prop}/sessions/{id}`** with `rev`. Sets `deleted_at`. Needed because the
page deletes sessions today (the `session-del` action in `web/app.js`). Idempotent.

**`PUT /api/progress/{prop}/levels`**

```json
{ "rev": 42, "changes": [ { "skill": "F5", "variant": "L", "from": 1, "to": 2, "at": "2026-11-10T18:03:11.120Z" } ] }
```

- 200: `{ "rev": 43, "levels": [ { "skill": "F5", "variant": "L", "level": 2, "updatedAt": "..." } ], "unlocked": [], "game": {...} }`
- 1 to 300 changes, all or nothing.
- Checks: the skill is in the published catalog (an alias resolves; a retired skill is allowed);
  the variant belongs to the skill's track; `from` and `to` are 0 to 4.
- `at` is the server's clock unless the page sends one between 30 days ago and 5 minutes ahead (for
  OFF replays).
- A stored level that differs from `from` is a 409:
  `{ "error": "...", "rev": 43, "state": {...}, "conflicts": [ { "skill": "F5", "variant": "L", "level": 3 } ] }`.
  During the transition the page sends again with the current `from`, which is today's "this device
  wins". OFF can ask the member.

**How `rev` applies, in all three:**

- The first statement is the document's own check,
  `UPDATE progress SET rev = rev + 1, updated_at = now(), tables_rev = rev + 1 WHERE user_id = $1 AND prop = $2 AND rev = $n`.
  With `rev: 0` it inserts, as `handlePutProgress` does.
- No row changed means 409 with the current `{rev, state}`, exactly as `PUT /api/progress/{prop}`
  answers.
- The page then takes the server's copy, applies its unsent operations again and sends them with
  the new rev.
- A response lost on the way is safe: the retry gets a 409, the page finds its session id already
  present, and drops the operation.

**Limits and gating:**

- New entries in `limits`: `a.lim.practice` (60 session writes an hour) and `a.lim.levels` (240 an
  hour), through `allowed`, as `.claude/rules/go-server.md` asks of every new write endpoint.
- Also put `PUT /api/progress/{prop}` under a limiter. It has none (finding 6).
- A muted member can still log; muting is about the community.
- Both endpoints sit behind `flagged("progressServer", ...)` (404 while the flag is off) and answer
  403 to a member who is not listed.

**`GET /api/game`** (own numbers only) returns the summary the page shows:

- XP, split by part and per prop, the level, the streak with its seven days, this week's challenges;
- every badge with `n`, `of`, `on`, `at`;
- per-phase XP earned and possible;
- the constants.

It is the read path once the page's rules are removed.

### 6.4 Rules in Go

A new section in `main.go` (one file on purpose, by `.claude/rules/go-server.md`), as pure functions
with no database and no clock:

- `gameSummary(in gameInput) summary`.
- `gameInput` holds: `Now`; a `*time.Location`; the member's props; per prop the catalog, levels,
  sessions and events, and from the document the gates, combos, note presence and `dominant`; the
  recorded badges.
- Constants are copied from `XP_SKILL`, `XP`, `LEVEL_TITLES` and `levelNeed` in `web/game.js`.
- `award(u, keys, prop)` is the body of `handleAchievements` turned into a function: blocks
  respected, a notification each, one milestone post per batch when `autoPost` and `community` are
  on, no post for a muted member.
- It is called after every committed write, and its result is returned as `unlocked`. Badges are
  never taken back when a number falls, as today.

Time zone (Q10):

- `users.tz` holds an IANA name the page sends from
  `Intl.DateTimeFormat().resolvedOptions().timeZone`, checked with `time.LoadLocation`. The fallback
  is an instance default, `FLOWA_TZ`, else UTC.
- The binary must import `_ "time/tzdata"`, because the image has no zone files. This is the
  standard library, not a new dependency; the precedent is the import of
  `golang.org/x/crypto/x509roots/fallback` in `main.go`, there for the same reason.

`users.stats`:

- It stays the cache the people list sorts on, with the same keys the page writes (the `stats`
  object in `syncGame()`) plus `streakDay`.
- A read reports the streak only when `streakDay` is today or yesterday in the member's zone. That
  fixes finding 4 without a nightly job.
- The hourly `housekeeping` loop recomputes members whose catalog revision moved.

### 6.5 The Go-versus-JS equality test

The repository's pattern is one committed fixture file checked by both sides:
`test/links-fixtures.json` with `node test/links.js` and `TestParseLink`; `test/catalog-fixtures`
with `test/catalog-fixtures/check.js` and `TestCatalogFixtures`. `go test` never starts node.
Follow it.

1. **Make the rules runnable in node first.** `web/game.js` cannot be loaded there: it reads
   `localStorage` at load (the `share` constant), adds DOM listeners at the end of the file, and its
   rules use globals from `app.js`, `catalogs.js` and `account.js` (`lv`, `best`, `worst`,
   `comboReady`, `streak`, `dayKey`, `variants`, `myProps`, `inProp`, `docs`).
   - Move the rules into `web/rules.js`: pure functions of
     `({ props: [{ id, catalog, state }], chosen, now, recorded })`.
   - End it with `if (typeof module !== 'undefined') module.exports = Rules;`, as `web/merge.js`,
     `web/links.js` and `web/suggest.js` do.
   - Load it in `web/index.html` after `catalogs.js`.
   - `game.js` and `app.js` call it. No behaviour change.
2. **`test/game-fixtures.js`** builds 100 members from a seeded generator and writes
   `test/game-fixtures.json`. Without `--write` it builds them again in memory and fails if the
   committed file differs.
   - The file embeds its own small catalogs: `one`, `LR` and `FR` skills, a retired skill, an alias,
     phases, weeks. Then editing the real maps (epic CAT) does not move the expected numbers.
3. **A fixture member:**

   ```json
   { "name": "left-fr-weak", "tz": "Pacific/Auckland", "now": "2026-11-09T11:30:00Z",
     "chosen": ["poi", "nunchaku"],
     "docs": { "poi": { "skills": {"F3": {"F": 2, "R": 1, "updated": "..."}}, "sessions": [{"id": "a1", "date": "2026-11-09", "minutes": 25, "focus": ["F3"], "notes": ""}],
                        "events": [{"t": "2026-11-08T22:10:00.000Z", "id": "F3", "side": "R", "from": 0, "to": 1}],
                        "combos": [], "gates": {"p1": "2026-10-01"}, "settings": {"dominant": "L"} } },
     "recorded": ["first-post"],
     "want": { "xp": {"skills": 50, "practice": 35, "gates": 150, "combos": 0, "challenges": 40, "total": 275, "byProp": {"poi": 235}},
               "level": 2, "streak": {"current": 1, "best": 1, "today": false, "atRisk": true, "week": [true,false,false,false,false,false,false]},
               "challenges": {"days": 1, "minutes": 25, "levels": 1, "weak": 1}, "challengeWeeks": 0,
               "earned": ["sessions-1", "clean-1", "gates-1"],
               "stats": {"xp": 275, "level": 2, "streak": 1, "best": 1, "clean": 1, "minutes": 25, "sessions": 1, "badges": 4} } }
   ```

   The numbers are an illustration, worked out by hand from the rules in `web/game.js`: in Auckland
   `now` is half past midnight on Tuesday 10 November, so the session of the 9th is yesterday's,
   the streak is at risk, and the level-up of Sunday evening in UTC falls in this week. 275 XP is
   level 2, and `badges` counts the one recorded and the three earned, as `syncGame()` does after
   `grant()`. The generator's run of the page's rules is what writes `want`.

4. **Members are built to cover, by construction:**
   - an empty document;
   - sessions with 0 minutes, with 600 minutes, and several on one day;
   - a streak ending today, yesterday and two days ago;
   - a Sunday-to-Monday week edge, a year edge, daylight-saving days, zones at UTC+13 and UTC-10,
     and an event at 23:59 on a Sunday, local time;
   - each track; a left-dominant member with an `FR` level-up;
   - more than 300 events;
   - junk gate keys;
   - combos with `clean` above 20 and with fewer than 2 skills;
   - levels on a retired skill and under an alias;
   - a prop with a document but not chosen;
   - "dirty" documents: a malformed date, negative minutes, a fractional level.
5. **`node test/game.js`** sets `process.env.TZ` per member, runs `Rules` on the documents and
   compares with `want`. It goes into the `lint` job beside `test/merge.js`.
6. **`TestGameRulesMatchPage` in `main_test.go`** reads the same file and, for each member:
   - runs the Go converter (document to rows, in memory);
   - runs `gameSummary` with `time.LoadLocation(tz)` and `now`;
   - compares field by field, naming the member and the field;
   - refuses a file with fewer than 100 members (the "fixtures look cut short" guard in
     `TestParseLink` is the model).
7. **`TestDBProgressServer` in `db_test.go`** pushes a few fixture members through the real
   endpoints both ways (document, then rows) and reads `GET /api/game`.
8. **The statement that is tested:** for every member, Go on the converted rows equals JS on the
   document. For dirty members, where what JS does depends on values the tables cannot hold, the
   fixture lists what the converter must report in their place.
9. When the page's copy is removed, `test/game.js` and the rule half of `web/rules.js` go, and the
   fixture stays as the Go regression. `test/badges.js` stays, pointed at the page's display
   registry (names and icons still live in the page).

### 6.6 The migration

One Go function does all of it: `syncFromDocument(tx, user, prop, state)`, a port of `normalize()`
(`web/app.js`) and `applyAliases()` (`web/catalogs.js`) followed by "make the three tables agree
with these three sections":

- sessions by id; a session with no id gets one made from its content;
- levels by skill and variant;
- events by content, including removal of the ones no longer in the document (so that Go on the
  tables equals JS on the document while the document is in charge);
- it ends with `tables_rev = rev`.

It is used in three places:

1. **Once for everyone, at start, under the migration lock.**
   - `migrations` in `main.go` becomes a slice of steps that are either SQL or a Go function, so
     step 9 runs inside `migrate()` while advisory lock 7130651 is held and commits with its record
     in `schema_migrations`.
   - Entries 1 to 8 keep their text and order. `TestDBSchemaTwice` and `TestDBMigrations` need the
     small matching change.
   - It reads the list of (user, prop) first, then one document at a time, because lib/pq cannot
     interleave a streaming read and writes on one connection.
2. **On every document save, for every member**, from the migration pull request on, so the tables
   never go stale between "converted once" and "switched on".
3. **Whenever `tables_rev <> rev` is found**: a build that did not know the tables wrote the
   document, for instance after a rollback. The document wins, once.

**Idempotent:** the primary keys, the `UNIQUE` on events and `tables_rev` make a second run change
nothing.

**Per-member report**, rows in `progress_reports` with kind `conversion`:

- sessions, minutes, level cells and clean skills before (from the document) and after (from the
  tables);
- every item that could not be converted, with the reason;
- XP, level and best streak as the page reported them (`users.stats`) against the Go rules.

It is served as JSON and CSV by `GET /api/admin/progress/report` (admins only, Q13). The export from
stage is attached to the pull request. One log line at start says how many documents were
converted, how many differed and how long it took.

**Two honest limits:**

- **`users.stats` has no date**, so the page's current streak cannot be compared exactly. Ship, one
  release earlier, a page change that adds `day` (a number, `yyyymmdd`) to the stats it sends and
  `tz` to `PUT /api/me`; `validStats` takes numbers and booleans only, which is why the day is a
  number. Then the report compares the current streak for every member seen since, and the best
  streak for all.
- **Timing.** The conversion runs before the server answers `/healthz`, and `healthy()` in
  `deploy.sh` waits 90 seconds before it goes back by itself. Measure the conversion on stage with
  real data and print the time.

### 6.7 Flag and allowlist

**Flag `progressServer`:**

- In `flagDefaults` in `main.go`, default `false`, with its row in `FLAGS` in `web/app.js`;
  `test/flags.js` fails without both.
- Stored in the `flags` table, changed by admins on Admin, Features (`handlePutFlags`, permission
  `flags.edit`), audited.
- The README's "Every flag is on by default" needs the exception.

**Allowlist: `users.progress_server`:**

- Changed by admins on Admin, Accounts through `PUT /api/admin/users/{id}/progress` (permission
  `user.manage`, registered in `routes()` beside the other `/api/admin/users/{id}/...` routes).
- Audited with `a.audit` (action `progress_mode`, added to `ACTION_NAMES` in `web/mod.js`).
- Turning a member on is refused unless `tables_rev = rev` and their last comparison was the same.
  Turning off is always allowed.
- The effective mode is `flag AND column`, worked out by the server and sent as
  `user.progressServer` in `GET /api/me` and in the accounts list (`handleUsers`). That is the
  todo's "the dashboard shows the mode per member".

### 6.8 "The page writes both ways", precisely

- **Member not listed (document in charge).** The page behaves as today. The server mirrors each
  saved document into the tables in the same transaction. Stats and badges still come from the page.
- **Member listed (tables in charge).**
  - The page sends sessions and level changes to the row endpoints and keeps saving the document for
    notes, combos, gates and settings, with `"rows": "server"` beside `rev` and `state`.
  - On a row write the server writes the rows and, in the same transaction, rewrites the document's
    `sessions`, level values and `events` from the tables.
  - On a document save with the marker it keeps the tables and overwrites those three sections with
    their projection.
  - On a document save without the marker (an old tab, or a rolled-back page), the rev check has
    passed, so the difference is the member's intent and is applied to the rows with
    `source = 'document'`.
  - `GET /api/progress/{prop}` returns the old shape either way.
  - For a listed member the server ignores what the page sends to `PUT /api/me` as stats and to
    `POST /api/achievements` as keys, except to compare.
- **In the page:** an operation queue flushed before each document save (the same queue OFF will
  later keep in IndexedDB). `web/merge.js` gets one case: on a 409 the three table-owned sections
  come from the server and the unsent operations are applied on top. `test/merge.js` covers it.

### 6.9 Drift: how it is found and reported

1. **Structure, constant time:** `SELECT count(*) FROM progress WHERE tables_rev <> rev`. Not zero
   means a write path forgot the other store; it is repaired by rule 3 of section 6.6 and counted.
2. **Content, hourly** in `housekeep`: project the tables and compare with the document's three
   sections (after aliases), per (user, prop). The result goes to `progress_reports`, kind `mirror`.
3. **Rules, at every stats report from a page:** when `PUT /api/me` carries `stats`, the server
   works out its own for the same day and zone, stores them in `users.stats_server` and records same
   or different with both values (kind `rules`). The same happens for the keys sent to
   `POST /api/achievements`.

The "two weeks with no difference" evidence for the owner's account is zero `rules` and `mirror`
rows marked different.

It is reported in three places: a tile on the admin dashboard (members on the server path,
differences open); the mode and the last check per member in Accounts; one log line per difference
with the member's number and the field names. Never the session notes.

### 6.10 Order of pull requests

| # | Title | Todo item | Notes |
| --- | --- | --- | --- |
| 0 | `fix(server): take the roadmap and branch badges the page awards` | none | Finding 2. Merged as #71, so the fixture's badge sets are not built on a bug. |
| 1 | `docs(server): design note for progress the server can vouch for` | 1 | This page, linked from the README. The box is ticked when the owner has reviewed it. |
| 2 | `refactor(ui): gather the game rules in a file node can run` | prepares 3 | `web/rules.js`, the fixture generator, `node test/game.js`; the page dates its stats and sends `tz`. |
| 3 | `feat(server): tables and endpoints for sessions and levels` | 2 | The schema 7 fixture, migration 8, endpoints behind the flag (off), limits, `db_test.go` cases. |
| 4 | `feat(server): work out xp, streak and badges in go` | 3 | Rules, `TestGameRulesMatchPage`, `GET /api/game`, the comparison at stats writes, `time/tzdata`. |
| 5 | `feat(server): mirror every progress document into the tables` | 4 | Step 9, the mirror on every save, the report endpoint; the report from stage attached. |
| 6 | `feat(ui): write sessions and levels through the server for listed members` | 5 | Allowlist, mode in Accounts, the queue, a browser suite added to `ALL` in `test/ci-plan.sh`, the two-device check in `combo.py` extended. |
| 7 | `feat(server)!: the server alone awards badges and stats` | 6, first half | Everyone listed; `POST /api/achievements` takes no keys; `stats` dropped from `PUT /api/me`; the page reads `GET /api/game`; the page's rules removed; section 5's consumers moved to the tables. Documents still carry the projection. |
| 8 | `refactor(server)!: keep sessions and levels out of the document` | 6, second half | The one step that cannot be rolled back past. After a soak (Q14); export, the app's daily backup and the restore drill moved first. |

### 6.11 What `./deploy.sh --rollback` can and cannot go back past

- **How rollback works.** `rollback()` in `deploy.sh` restarts the single kept image
  `nunchaku:previous` on the database as it is. An older build is healthy on a newer schema because
  `checkHealth` in `main.go` sets `OK = Database && Schema >= Migrations`. The comment at the top of
  `deploy.sh` says going back is safe while migrations only add.
- **Can: pull requests 0 to 7.** Every schema change is an added table or column, and step 9 only
  inserts rows. An older build ignores them.
  - After going back to a build without the mirror, documents move alone and `tables_rev` falls
    behind. Going forward again repairs it (the document wins).
  - Listed members lose nothing, because every row write was also written into their document.
- **One step only.** `nunchaku:previous` is one tag. Further back is `./deploy.sh vX.Y.Z`.
- **Cannot: past pull request 8.** Once documents stop carrying sessions, levels and history, an
  older build shows empty logs and its page would save documents without them. That pull request
  carries `!`, a `BREAKING CHANGE:` line and "take `./backup.sh` first; going back needs that dump",
  as `CLAUDE.md` ("Commit types") and `.claude/rules/go-server.md` ask.
- **Pull request 7 is breaking for clients**, not for data: an old tab's writes to
  `POST /api/achievements` and its stats are answered 200 and ignored.

### 6.12 Risks

- **Time zones.** A wrong or missing `tz` moves "today" and the week edge. Sessions are safe
  (calendar days); the current streak and the week of a level-up are not. The fixture covers the
  edges, the fallback is stated, and the page sends `tz` again when it changes.
- **Clock skew.** Level times come from the server, except bounded times from the page for offline
  replays. A page clock in the future is clamped.
- **Backdating.** Unlimited today, and the server cannot know when someone practised. `created_at`
  beside `day` makes it visible; a bound is the owner's call (Q5).
- **Offline writes (OFF).** The contract OFF needs is here: ids made by the page, replays that
  survive a restart, 409 with the current copy, conflicts named per skill. OFF's todo says "combo
  edits" too; those stay document saves.
- **Document size.** While the projection is kept, documents hold the same as today (the 512 KB
  cap, `maxProgress`). The history stays cut to 300 in the projection. Pull request 8 removes the
  part that grows.
- **Catalog changes.** Retiring a skill changes XP with no action by the member, and a new alias
  must move rows at publish. The two meanings of `R` mean that a track change from `LR` to `FR`
  would reinterpret stored levels; nothing checks that today (`safeToPublish`).
- **Privacy.** Nothing new is shown to anyone: the profile keeps its four numbers, the badges and
  the clean list. New in storage, visible to nobody but the database: the time zone, and when each
  session and level change was logged. Session notes move from JSON to a column in the same
  database. The report and the logs carry numbers, never notes. But the roadmap's principle says
  progress stays in the user's own document, which this epic contradicts (Q8).
- **One file.** `main.go` grows by roughly a thousand lines; one file is the repository's choice.
