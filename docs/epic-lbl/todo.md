# LBL: Open the suggestion gate

Why, scope and risks: [epics.md](../epics.md#lbl-open-the-suggestion-gate). Order: [roadmap.md](../roadmap.md).
Category: feature. Size: S. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** not started (issue [#39](https://github.com/TuanSSM/flowtopia/issues/39)). 0 of 4 items done.

- [ ] **The prompt.** After a combo run is logged, for each transition the member has not marked:
  "How did <a> into <b> go?" Works, Needs a bridge, Blocked, Skip; once per transition; only with
  sharing on. DoD: `suggest.py` logs a run and answers; the mark lands in the combo and the counts.
- [ ] **Progress to the gate.** The admin dashboard shows shared pairs toward 30 and the date the
  gate can open. DoD: `TestDBDashboard` has the number.
- [ ] **The fit by workflow.** `fit_suggest.yml`, run by hand with an instance URL and a read token
  in a secret: fetches `GET /api/suggest/counts`, runs `sim/fit-suggest.js --write`, opens a PR with
  the AUC and the baseline in the body. DoD: run once on stage data.
- [ ] **The label goes.** When `test/suggest-gate.js` sees fitted weights that pass, the "early"
  label is removed from the builder. DoD: the browser check for the label flips with the weights.
