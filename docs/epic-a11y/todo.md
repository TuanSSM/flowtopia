# A11Y: Usable by keyboard, screen reader and a shaky hand

Why, scope and risks: [epics.md](../epics.md#a11y-usable-by-keyboard-screen-reader-and-a-shaky-hand). Order: [roadmap.md](../roadmap.md).
Category: improvement. Size: S. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** not started (issue [#36](https://github.com/TuanSSM/flowtopia/issues/36)). 0 of 5 items done.

- [ ] **Focus.** Every control reachable by Tab in reading order, a visible focus ring in both
  themes, dialogs trap focus and return it. DoD: a keyboard-only browser test signs in, logs a
  session, publishes a combo and replies to a comment.
- [ ] **Names.** `aria-label` on every icon-only button; the figure canvas carries the caption as
  its text alternative; live regions for toasts. DoD: a script in lint lists controls without a
  name and fails on any.
- [ ] **Reduced motion.** With `prefers-reduced-motion`, demos open on a still with a Play button,
  the tour does not animate, the heatmap does not fade in. DoD: `pages.py` with the media feature
  emulated.
- [ ] **Contrast.** Text and controls at 4.5:1 (3:1 for large text and icons) in both themes,
  checked by a script over the computed styles of each page. DoD: in lint next to
  `test/avatar-colors.js`.
- [ ] **Forms.** Labels bound to inputs, errors announced, the composer's limits announced. DoD:
  the lint script checks label binding.
