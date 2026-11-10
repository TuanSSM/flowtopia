# LBL: Open the suggestion gate

Why, scope and risks: [epics.md](../epics.md#lbl-open-the-suggestion-gate). Order: [roadmap.md](../roadmap.md).
Category: feature. Size: S. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#39](https://github.com/TuanSSM/flowtopia/issues/39)). 2 of 4 items done.
The fit by workflow is built and tested; its box waits for its first run on an instance's data.

- [ ] **The prompt.** After a combo run is logged, for each transition the member has not marked:
  "How did <a> into <b> go?" Works, Needs a bridge, Blocked, Skip; once per transition; only with
  sharing on. DoD: `suggest.py` logs a run and answers; the mark lands in the combo and the counts.
- [x] **Progress to the gate.** The admin dashboard shows shared pairs toward 30 and the date the
  gate can open. DoD: `TestDBDashboard` has the number.
  Done: a third tile under Suggestions, "Pairs of skills with a shared mark": the count of pairs
  that someone marked Works or Blocked (what `sim/fit-suggest.js` counts; "needs a bridge" alone is
  not read by the fit), "of 30 needed", and the week 30 would be reached at the pace of the last
  four whole weeks. With no pair first marked in those weeks it says there is no date, and with 30
  it says the fit can run now. For the pace a pair keeps the week it first counted
  (`transition_counts.first_week`, migration 7): a week and not a moment, so it cannot be laid next
  to who was signed in, and gone again when the last mark is withdrawn. `TestDBDashboard` and
  `TestDBTransitionCounts` have the numbers, `TestGate` the date arithmetic and that 30 is the
  `MIN_PAIRS` of the fit, and `mod.py` reads the tile.
- [ ] **The fit by workflow.** `fit_suggest.yml`, run by hand with an instance URL and a read token
  in a secret: fetches `GET /api/suggest/counts`, runs `sim/fit-suggest.js --write`, opens a PR with
  the AUC and the baseline in the body. DoD: run once on stage data.
  Built: the read token is `FLOWA_COUNTS_TOKEN` in the instance's `.env` and in the repository's
  secrets. A request with `Authorization: Bearer` and that token gets the counts of every prop
  without an account and nothing else; a token under 32 characters stops the server at start; a
  wrong one is refused and logged (`TestDBCountsToken`, `TestCountsToken`). The workflow takes an
  `https` address only, checks that the answer is a list of counts before using it, fits, runs the
  gate, and opens a pull request with the pairs, the AUC, the baseline and the gate's line, then
  starts `process_pr` on the branch, because a pull request a workflow opens does not start it. With
  fewer than 30 pairs or weights that fail the gate it says so in the run and opens nothing.
  `dry_run` stops before the pull request. Its steps were run by hand against a local server with 40
  made-up pairs: the fetch, the refusal of a wrong token, the fit and the gate (which turned those
  made-up weights down, as it should).
  Open, and why the box is not ticked: the workflow has not run, here or on stage data. This session
  may not start workflows, stage is not reachable over HTTPS until the proxy recipe is in use (OPS),
  and stage has fewer than 30 pairs. First run: `gh workflow run fit_suggest -f instance=... -f dry_run=true`.
- [x] **The label goes.** When `test/suggest-gate.js` sees fitted weights that pass, the "early"
  label is removed from the builder. DoD: the browser check for the label flips with the weights.
  Done: the builder already took the label from `web/suggest-weights.json` (`early`), and
  `test/suggest-gate.js` already refuses a file that is fitted and still says early, or early and
  not the starting weights. What was missing was the check on the page. `suggest.py` now has both
  halves: with the committed weights the list says "Early suggestions", and when the page is given
  weights that were fitted and measured (the same file with `early` false, as the fit writes it)
  the list is there without the label. Nothing in the page changed. The label itself goes on the
  day the fit's pull request merges, which is the next item.
