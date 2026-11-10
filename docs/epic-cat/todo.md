# CAT: Skill maps, second research pass

Why, scope and risks: [epics.md](../epics.md#cat-skill-maps-second-research-pass). Order: [roadmap.md](../roadmap.md).
Category: improvement. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#37](https://github.com/TuanSSM/flowtopia/issues/37)). 1 of 6 items done.

Today: 94 of 411 skills unconfirmed or not described; most `motion` fields unknown
([demo-coverage.md](../demo-coverage.md), [suggestions.md](../suggestions.md)).

- [x] **The method.** A page in `docs/` on how a skill is confirmed: which sources count, how a
  reference is recorded, when `star` is cleared, how `motion` is read from the text. DoD: linked
  from `catalog-format.md` and the `catalog-research` agent.
  Done: [catalog-method.md](../catalog-method.md). It names the three states a skill can be in and
  how each is written and counted, ranks four kinds of source and lists what does not count, says
  how a reference and the nunchaku research notes are recorded, gives the two conditions for
  clearing a star and what a skill becomes when only its name is confirmed, lists the words
  `sim/motion.js` reads for each `motion` value, and ends with a pass step by step and what its pull
  request must say. It writes down what the first pass did and the validator and coverage report
  already assume; nothing in a catalog changes with it.
- [ ] **Buugeng.** 35 of 56 unconfirmed. One revision through the editor. DoD: the count after, and
  the sources opened, in the PR body; `node test/catalog.js` and the Go validator agree.
- [ ] **Staff.** 18 unconfirmed; `motion` for the described moves. DoD: as above.
- [ ] **Poi and double staff.** 27 unconfirmed between them. DoD: as above.
- [ ] **Nunchaku.** 14 unconfirmed; aliases for the names used by the main communities. DoD: as
  above, plus aliases resolve in the search palette.
  Found while writing the method: R11, Intercept, is marked unconfirmed and has a demo, built from
  search snippets of a site that is offline. This pass settles it: a source that counts, or no demo.
- [ ] **Coverage report.** `node test/coverage.js` and `node sim/motion.js` report what each pass
  changed; `docs/demo-coverage.md` regenerated. DoD: unconfirmed under 40 overall or the reason per
  remaining skill recorded.
