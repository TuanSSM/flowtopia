# XPR: Across props

Why, scope and risks: [epics.md](../epics.md#xpr-across-props). Order: [roadmap.md](../roadmap.md).
Category: feature. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#41](https://github.com/TuanSSM/flowtopia/issues/41)). 2 of 5 items done.

- [x] **Concepts in the catalogs.** A `concepts` list (plane, timing and direction, weave,
  isolation, extension) and a `concepts` field on skills, validated by both validators; curated in
  the editor. DoD: fixtures for both validators; no inference from text.
  Done: the five concepts are a fixed list in both validators (`CONCEPTS` in `web/catalog-check.js`,
  `catalogConcepts` in `main.go`). A catalog may name the ones it teaches, each with a sentence on
  what it means for that prop; a skill may list up to three of its catalog's concepts. Twelve
  fixtures (two good, ten bad, each failing for its own reason) are judged the same way by both
  validators. In the editor the map's concepts are written under Branches and the skill form has a
  tick box per concept the map names; the review now also names what changed in the map itself, so
  a concept can be published before any skill lists it (`editor.py` and `TestDBCatalogs` cover it).
  Nothing fills the field in: there is no script for it, the method page says when a person may
  tick one, and the five shipped catalogs name no concept yet. Curating them is content work for a
  pass that can open sources ([CAT](../epic-cat/todo.md)); the hint below shows nothing until then.
- [x] **The hint.** A skill whose concept is clean on another of the member's props says "you know
  the <concept> from <prop>". Never an unlock. DoD: `combo.py` shows the hint only when both
  catalogs name the concept and the level is clean.
  Done: the skill guide of a signed-in member says "You know the plane from poi (<skill>, clean).
  Here: <this map's sentence on the concept>", one line per concept, naming every other prop of
  theirs where a skill that lists the concept is Clean or better, with a link to the best one
  (`knownFrom` in `web/app.js`). `combo.py` publishes the concept on two maps and shows: no hint
  while the other map does not name it, none while the level there is below Clean, the hint when
  both hold, none on a skill that does not list the concept, and the "prerequisites are not Clean"
  notice still there beside the hint. With the shipped maps nobody sees a hint yet, because they
  name no concept (see the item above).
- [ ] **One dashboard.** After SHP: one Progress card across props, total practice, the all-props
  streak, per-prop rows beneath. DoD: `pages.py` screenshot; a member with one prop sees no change.
- [ ] **Mixed combos.** A step can name a prop; the player swaps the prop at the boundary; publish
  and chips carry both. DoD: `combo.py` builds and publishes a poi-then-staff combo; the physics
  checks run per step.
- [ ] **Suggestions across props.** A next trick from another prop offered only through a shared
  concept, labelled as such. DoD: `test/suggest.js` case.
