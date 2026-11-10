# XPR: Across props

Why, scope and risks: [epics.md](../epics.md#xpr-across-props). Order: [roadmap.md](../roadmap.md).
Category: feature. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** not started (issue [#41](https://github.com/TuanSSM/flowtopia/issues/41)). 0 of 5 items done.

- [ ] **Concepts in the catalogs.** A `concepts` list (plane, timing and direction, weave,
  isolation, extension) and a `concepts` field on skills, validated by both validators; curated in
  the editor. DoD: fixtures for both validators; no inference from text.
- [ ] **The hint.** A skill whose concept is clean on another of the member's props says "you know
  the <concept> from <prop>". Never an unlock. DoD: `combo.py` shows the hint only when both
  catalogs name the concept and the level is clean.
- [ ] **One dashboard.** After SHP: one Progress card across props, total practice, the all-props
  streak, per-prop rows beneath. DoD: `pages.py` screenshot; a member with one prop sees no change.
- [ ] **Mixed combos.** A step can name a prop; the player swaps the prop at the boundary; publish
  and chips carry both. DoD: `combo.py` builds and publishes a poi-then-staff combo; the physics
  checks run per step.
- [ ] **Suggestions across props.** A next trick from another prop offered only through a shared
  concept, labelled as such. DoD: `test/suggest.js` case.
