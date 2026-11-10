# A11Y: Usable by keyboard, screen reader and a shaky hand

Why, scope and risks: [epics.md](../epics.md#a11y-usable-by-keyboard-screen-reader-and-a-shaky-hand). Order: [roadmap.md](../roadmap.md).
Category: improvement. Size: S. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#36](https://github.com/TuanSSM/flowtopia/issues/36)). 1 of 5 items done.

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
- [ ] **Contrast.** Text and controls at 4.5:1 (3:1 for large text and icons) in both themes,
  checked by a script over the computed styles of each page. DoD: in lint next to
  `test/avatar-colors.js`.
- [ ] **Forms.** Labels bound to inputs, errors announced, the composer's limits announced. DoD:
  the lint script checks label binding.
