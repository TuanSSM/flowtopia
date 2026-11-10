# How a skill is confirmed

The skill maps are only worth following if what they say can be traced. This page is the method for
the research passes over `web/catalog/<prop>.json`: which sources count, how a reference is recorded,
when a skill stops being unconfirmed, and how the `motion` field is read. The file format is in
[catalog-format.md](catalog-format.md); this page is about the evidence behind it. The passes
themselves are the [CAT epic](epic-cat/todo.md).

## Three states of a skill

| State | In the file | Counted by `node test/coverage.js` as |
| --- | --- | --- |
| **Unconfirmed.** No opened source confirms the name, or none confirms what the move is. | `star: true` | unconfirmed or not described |
| **Named, not described.** An opened source uses the name for this move, but none says how it is done. | `star: false`, and the first step is exactly: "The sources I could read name this move but do not describe it, so find a demonstration before training it." | unconfirmed or not described |
| **Confirmed.** An opened source names the move and says what the prop and the body do. | `star: false`, steps written from the source, the source in `refs` | described |

On 10 November 2026 the five maps hold 411 skills: 29 carry a star, 87 open with that sentence
(some do both: a name that is itself in doubt, on a move nobody describes), and 95 are in one or
both of the first two states. The coverage page counts 94, because it sets aside skills that have
a demo, and one of the 95 has one: nunchaku R11, Intercept, drawn from fragments of a site that is
now offline, with a caption that says so. By the rules below those fragments do not count, so the
nunchaku pass has to find a source that does or take the demo out. Otherwise a demo is built only
for a move whose motion its definition fixes ([demo-engine.md](demo-engine.md)), and the `motion`
of a skill in the first two states stays unknown.

## Which sources count

A source counts when it was **opened and read for this skill**, on the day it is cited. In order of
weight:

1. **A page about the move itself** in a reference a community keeps: a wiki article, a glossary
   entry, a federation's or school's syllabus. It confirms the name and, where it says how, the
   mechanics.
2. **A written tutorial by a named teacher or school**, with the move named and its steps described.
3. **A video tutorial by a named teacher**, where the move is named in the title or spoken, and
   shown. It confirms the name and what can be seen: plane, direction, which hand, where the prop
   touches the body. Write only what is said or plainly visible; a guess at what the wrist does off
   screen is not a description. A video that was not watched counts for its title and its written
   description only, which is weight 4: the name, not the steps. The double staff map was built
   that way and says so in its `about`.
4. **A forum thread, a comment or a list of names.** It confirms that a name is in use, and nothing
   more. It can support an alias, never the steps.

What does not count, however right it may be:

- **A page that did not open**: behind a login, a paywall, a dead link, a search result's snippet.
- **Memory and general knowledge**, including a language model's. If it is true, a source says so;
  find the source.
- **Another skill map**, Flowa's own included. A map is a list of claims, not evidence for them.
- **A source for a different prop.** A poi weave does not describe a staff weave, even when the
  names match; say so in the description if the name is borrowed.

When two sources disagree on a name, the skill keeps the name of the heavier source and the other
goes into the description ("also called ..."). When they disagree on the mechanics, the skill is
not confirmed until one of them is shown to describe a different move.

## How a reference is recorded

- Each file has one `refs` map: a short key to a title and a URL,
  `"wikiPoi": ["Wikipedia: Poi (performance art)", "https://en.wikipedia.org/wiki/Poi_(performance_art)"]`.
  The title names the publisher and the page, so a dead link can be searched for. For a video, the
  title names the teacher and the video, and the URL is the video's own address, not a playlist's.
- The skill lists the keys of the sources that confirm **it** in its own `refs`, heaviest first. A
  source read for the whole prop but silent on this move is not listed on the move.
- Every URL was opened successfully on the day of the pass. `node test/catalog.js` checks that a
  key exists; it cannot check that a page says what is claimed, which is why the pull request does
  (below).
- For nunchaku, where the demos lean on the exact mechanics, the reading is also kept in
  [nunchaku-research.json](nunchaku-research.json): per skill, `defined` (`full`, `partial` or
  `none`), the mechanics in the source's own words with quotation marks, the start, the end, the
  contact with the body, and the sources. A demo needs the motion fully fixed by its definition,
  which is `full`.
- Text is never copied into the map. The description and the steps are written in Flowa's words,
  plain sentences a beginner can follow; quotations live in the research file.

## When the star is cleared

`star: true` goes when **both** hold, from sources cited on the skill:

1. the name is used for this move by a source of weight 1 to 3, and
2. what the prop and the body do is stated or shown by a source of weight 1 to 3.

Then, in the same revision:

- `desc` says what the move is in one factual sentence;
- the steps are rewritten from the source, 1 to 5 of them, and the sentence "The sources I could
  read name this move ..." is gone;
- the watch items and the drill are coaching, and may be Flowa's own; they must not add mechanics
  the source does not have.

If only the name is confirmed, the star is cleared and the skill becomes **named, not described**:
the first step is that sentence, word for word (the coverage report looks for it), and no step
invents the rest. If a source describes only part of the move, say the start and the plane, the
steps give that part and the first step still says the rest is not described.

A star is never cleared because a move is well known, because a demo could be made to look right, or
because the prerequisite graph needs it. Prerequisites, tiers, phases and plans are coaching
proposals, not claims about the art; they need consistency, not a source.

## How `motion` is read

`motion` says how a move travels, for next-trick suggestions ([suggestions.md](suggestions.md)):
`plane`, `direction`, `hands`, `grip`, and `source`. It is read from two places only.

- **The skill's clip**, when it has a demo: the plane is measured from the baked clip
  (`web/clips/features.json`), and `source` is `clip`.
- **The skill's own confirmed text**, its name, description and steps, and only words that are
  there: `source` is `description`. `node sim/motion.js` proposes the values for the nunchaku map
  and lists the skills where the clip and the text disagree; each disagreement is settled by reading
  the source again, not by picking one. The words it reads:

  | Field | Value | Words in the text |
  | --- | --- | --- |
  | plane | wall | wall plane, side plane |
  | plane | face | face plane, front plane |
  | plane | table | table plane, horizontal, flat circle |
  | plane | mixed | more than one of the above: variants in several planes |
  | direction | forward | forward, upspin |
  | direction | reverse | reverse, backward, downspin |
  | direction | either | either direction, both directions, or both of the above |
  | hands | both | both hands, two hands, hand to hand, switches hands, a pass (and every double-prop skill) |
  | hands | one | one hand, same hand, single hand |
  | grip | end | tail grip, by the end, end of the stick |
  | grip | middle | middle of the stick |
  | grip | handle | normal grip, by the handle, handle grip |

- Anything neither settles is `unknown`, which is a valid and common answer. A value is never taken
  from how the move is "usually" done. A skill that is unconfirmed or named but not described has
  no text to read, so every field is `unknown` and `source` is `unknown`.
- When a pass rewrites a description from a source, it uses these words where the source supports
  them, so that the field can be read from the text by the same rule the next time.

## Shared concepts

A skill's `concepts` say which of five ideas it is about: plane, timing and direction, weave,
isolation, extension ([catalog-format.md](catalog-format.md)). They exist so that Flowa can one day
say "you know the plane from poi" on a staff skill, and a wrong claim of that kind is worse than
none. So:

- A catalog names a concept only with a sentence, in its own words, on what the idea means for that
  prop, and only when the prop's own sources use the idea. Poi and staff sources both speak of
  planes; that does not make a nunchaku "plane" the same thing until a nunchaku source says so.
- A skill is given a concept by a person, in the editor, when the skill's confirmed text is about
  that idea: it teaches it or cannot be done without it. Mentioning a wall-plane circle in a step
  does not make a skill "about" the plane.
- Nothing is inferred: no script proposes concepts, as `sim/motion.js` does for `motion`, and a
  skill that is unconfirmed or named but not described gets none.
- A concept is a hint and never a prerequisite. It does not unlock anything.

## A pass, step by step

1. List the prop's skills in the first two states: `node test/coverage.js` writes them under "No
   demo because the sources do not confirm or describe the move" in [demo-coverage.md](demo-coverage.md).
2. For each, look for sources in the order above. Keep a note per skill of what was opened and what
   it did and did not say; a search that found nothing is a result and is recorded too.
3. Make the changes as **one draft revision in the skill map editor** (Admin, Catalogs): the
   validator shows its messages as you type, the same rules as `node test/catalog.js` and the
   server. Ids never change; a renamed skill keeps its id.
4. Publish, export the revision, and replace `web/catalog/<prop>.json` with it in a pull request.
5. Run `node test/catalog.js web/catalog/*.json` and `node test/catalog-fixtures/check.js`
   (the Go validator runs the same files in `go test`), `node sim/motion.js` for nunchaku, and
   `node test/coverage.js` to regenerate the coverage page. If an id changed after all, search
   `sim/specs.js`, `test/expect.js` and `test/browser/*.py` for it and run `node test/physics.js`.
6. The pull request says, for the prop: the count in each state before and after, every source
   opened with what it confirmed, and the skills that stay unconfirmed with what was looked for and
   not found. That list is the reason a remaining star is honest.
