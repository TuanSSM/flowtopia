# CAT: Skill maps, second research pass

Why, scope and risks: [epics.md](../epics.md#cat-skill-maps-second-research-pass). Order: [roadmap.md](../roadmap.md).
Category: improvement. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** not started (issue [#37](https://github.com/TuanSSM/flowtopia/issues/37)). 0 of 6 items done.

Today: 94 of 411 skills unconfirmed or not described; most `motion` fields unknown
([demo-coverage.md](../demo-coverage.md), [suggestions.md](../suggestions.md)).

- [ ] **The method.** A page in `docs/` on how a skill is confirmed: which sources count, how a
  reference is recorded, when `star` is cleared, how `motion` is read from the text. DoD: linked
  from `catalog-format.md` and the `catalog-research` agent.
- [ ] **Buugeng.** 35 of 56 unconfirmed. One revision through the editor. DoD: the count after, and
  the sources opened, in the PR body; `node test/catalog.js` and the Go validator agree.
- [ ] **Staff.** 18 unconfirmed; `motion` for the described moves. DoD: as above.
- [ ] **Poi and double staff.** 27 unconfirmed between them. DoD: as above.
- [ ] **Nunchaku.** 14 unconfirmed; aliases for the names used by the main communities. DoD: as
  above, plus aliases resolve in the search palette.
- [ ] **Coverage report.** `node test/coverage.js` and `node sim/motion.js` report what each pass
  changed; `docs/demo-coverage.md` regenerated. DoD: unconfirmed under 40 overall or the reason per
  remaining skill recorded.
