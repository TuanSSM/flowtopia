# A11Y: Usable by keyboard, screen reader and a shaky hand

Why, scope and risks: [epics.md](../epics.md#a11y-usable-by-keyboard-screen-reader-and-a-shaky-hand). Order: [roadmap.md](../roadmap.md).
Category: improvement. Size: S. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#36](https://github.com/TuanSSM/flowtopia/issues/36)). 2 of 5 items done.

- [ ] **Focus.** Every control reachable by Tab in reading order, a visible focus ring in both
  themes, dialogs trap focus and return it. DoD: a keyboard-only browser test signs in, logs a
  session, publishes a combo and replies to a comment.
- [x] **Names.** `aria-label` on every icon-only button; the figure canvas carries the caption as
  its text alternative; live regions for toasts. DoD: a script in lint lists controls without a
  name and fails on any.
  Done: `test/browser/a11y.py` lists them and fails on any, over 68 stops. It is a browser suite
  (`integration (a11y)`) and not a step of the lint job, because a control's name is only known
  from the page as drawn and lint has no browser; the plan runs it on every change to the page,
  the clips or the figure, and `process_pr_result` needs it. Before the fixes it found 8 controls
  without a name (the comment box at 7 stops, the sheet behind the narrow menu) and 1 picture (the
  streak overlay preview); now none. The demo and combo canvases are named by their label and the
  caption, and the toast, the achievement card and the hint are live regions that stay on the page.
  Seen and left for the items they belong to: the segmented buttons (theme, hand, graph or list)
  show the current choice by colour only, the admin tabs have no `aria-current`, the hidden file
  inputs behind "Picture" and "Import JSON" cannot be reached by keyboard (Focus), form errors are
  hidden alerts until shown and the "Saving" note is not a live region (Forms). Not done: listening
  with a real screen reader.
- [ ] **Reduced motion.** With `prefers-reduced-motion`, demos open on a still with a Play button,
  the tour does not animate, the heatmap does not fade in. DoD: `pages.py` with the media feature
  emulated.
- [x] **Contrast.** Text and controls at 4.5:1 (3:1 for large text and icons) in both themes,
  checked by a script over the computed styles of each page. DoD: in lint next to
  `test/avatar-colors.js`.
  Done in two layers, because the computed styles of a page need a browser and lint has none.
  `node test/contrast.js` is a step of the lint job (`test/avatar-colors.js` itself runs in the unit
  job). It reads `web/app.css`: the light theme written for the system setting and the one for the
  switch say the same; each of the 7 colours used for words reaches 4.5:1 on each of the 12 surfaces
  words are laid on, and on 3 tinted boxes for the colours written in them; a rule with a colour
  and a background of its own is checked as written, hover included; field edges, focus rings and
  the ring around a chosen option reach 3:1. A rule whose colour, background, fade, filter or state
  ring it cannot place fails, and so does a colour in a template that is not a colour for words.
  485 checks.
  `test/browser/contrast.py` is the `integration (contrast)` suite. It first proves its arithmetic
  on pieces with known answers (black on white 21:1, #777 on white 4.48:1, half white over black,
  the size where text counts as large, a background from an ancestor, a faded parent), then walks
  55 stops in each theme, the tour opened once in each, and judges every visible text, `svg.i` icon
  and field (typed words, placeholder, edge) against what the computed styles put behind it: about
  20,300 pairs of colours.
  Before the fixes the stylesheet check failed 135 of 481 and the walk found 476 pairs below their
  contrast on 109 of 110 views, 27 different pairs of colours. The worst: the edge of a field at
  1.19:1 in the light theme and 1.30:1 in the dark one, of 3:1; the tick on a practised day 1.78:1
  of 3:1; white on the red count 3.23:1 and on the primary button 3.64:1 (dark) and 4.42:1 (light);
  the small grey line on a challenge that is done 3.62:1 (light) and 3.80:1 (dark); amber notes
  3.81:1 (light); the tour's Next button 3.90:1; the browser's own grey for placeholders 4.00:1.
  Now both find none. What moved: `--ink-3`, `--accent` with `--l3` and `--bad` in both themes,
  `--good` and `--warn` in the light one, each by the smallest step that passes; a new `--edge` for
  fields and the chosen segment; placeholders in `--ink-3`; the words on red and green pills in
  `--bg`; a dark tick on a practised day; the primary button darkens under the pointer; a quest
  that is done is no longer faded.
  Left out by the standard, each listed with its reason in the script: controls that are switched
  off; the flame and the badge emblem, which stand beside words that say the same; borders that
  only tint a box or only change under the pointer; the outline of a button, which its words mark;
  checkboxes, radio buttons and sliders, which the browser draws. Out of scope and counted, not
  passed: text and icons over a picture, a gradient or the demo canvas (about 100 in the walk), and
  the shapes of charts, the heatmap and progress bars (14 rules).
  The walk was written against Chromium; its first run on Firefox was the pull request's CI, where
  it passed (123 passed, 0 failed, 8 skipped). Seen
  and left: the search box in the palette shows no focus ring (Focus); the tour's Next button is
  put right by a rule in `app.css` over the colours the tour is handed, where the tour itself
  should ask for the right one.
- [ ] **Forms.** Labels bound to inputs, errors announced, the composer's limits announced. DoD:
  the lint script checks label binding.
