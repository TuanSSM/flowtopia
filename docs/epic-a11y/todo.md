# A11Y: Usable by keyboard, screen reader and a shaky hand

Why, scope and risks: [epics.md](../epics.md#a11y-usable-by-keyboard-screen-reader-and-a-shaky-hand). Order: [roadmap.md](../roadmap.md).
Category: improvement. Size: S. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#36](https://github.com/TuanSSM/flowtopia/issues/36)). 4 of 5 items done.

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
- [x] **Reduced motion.** With `prefers-reduced-motion`, demos open on a still with a Play button,
  the tour does not animate, the heatmap does not fade in. DoD: `pages.py` with the media feature
  emulated.
  Done: `FLOWA_MOTION=reduce test/browser/run.sh pages.py <dir>` starts Firefox with the pref
  `ui.prefersReducedMotion` set to 1 (`h.py`) and takes a second, shorter set: 7 pictures named
  `still-*.png`, added to the same `index.json`, and 36 checks. The screenshots job runs it after the
  first set, so the pictures are in the pull request's comment. The demo on the landing page, in a
  skill guide and in the combo player, and the avatar in its editor, each open as a still with a Play
  button (two readings of the canvas 0.4 s apart are the same), and Play starts them. A move that
  repeats waits at its start with its whole path drawn; one that does not waits half way through. A
  demo started by hand stops when its page is left, and the next one opens paused again. Every
  element of each page, and what is drawn before and after it, is asked for its computed style: no
  animation, no transition longer than 0 s, no smooth scrolling, and the browser runs no animation.
  That covers the skill panel (it slid in), the achievement card (it dropped in), the menu of a narrow
  window, the bars, and the guided tour's steps and the dark sheet around its highlight.
  What was there before: the stylesheet already switched animations and transitions off, the player
  already started paused and the tour already scrolled without gliding. What was missing, and the
  checks found on the tree before (6 of the first 16 failed): once Play had been pressed, every later
  demo played by itself; the setting was read once, when the page loaded; a paused demo of a move that
  does not repeat showed its first frame with no path; the avatar in its editor and on a profile
  always played, with no button; smooth scrolling and the styles of `::before` and `::after` were
  not covered. The setting is now followed while the page is open, by the player and by the tour.
  The heatmap has no fade in this tree; the check keeps it so. The 3D figure has no motion of its
  own: it is drawn only when a player draws a frame.
  Seen and left: a demo or a combo that a person starts plays round and round until they pause it,
  as it does for everyone. The avatar's canvas is still labelled "turning slowly", which it does not
  do. Not run here: the Firefox pref itself. The suite was run on Chromium with the media feature
  emulated; Firefox reads the pref in CI.
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
- [x] **Forms.** Labels bound to inputs, errors announced, the composer's limits announced. DoD:
  the lint script checks label binding.
  Done: `test/browser/a11y.py` checks it at the same 68 stops, and fails on any. Like Names it is the
  browser suite and not a step of lint: read from the source, 6 of the 67 fields in `web/*.js` cannot
  be tied to a label (the skill map editor builds its fields in one helper and wraps them in another),
  so a reading of the source would fail on a tree that is right. The rule for a field is narrower than
  the rule for a name: a wrapping `<label>`, `for`, `aria-label` or `aria-labelledby`, never a title or
  a placeholder, and a radio button in a named group. What `aria-labelledby`, `aria-describedby` and
  `for` point at must be on the page, and no id may be used twice. The suite proves the rules on 21
  made-up fields first. Before the fixes, of 212 fields 3 had no label (the list in the trick picker,
  at 3 stops), no pointer led nowhere and no id was used twice; now none.
  Errors: the place for an error under a form is a `role="alert"` that is drawn with the form and
  stays, empty and out of the flow, so its words are said when they are put in. The fields an error is
  about get `aria-invalid` and point at it with `aria-describedby`; both go at the next thing typed
  that could be sent. The suite sends three forms wrong (a wrong password, a short one, a post of 2001
  characters) and checks each step, and that two passwords that differ mark the second.
  Limits: a text box with a limit (a post, a comment, a reply, a comment being edited, the bio, the
  note on a report, the reason of a moderation action) points at a counter that shows from 100
  characters left. The count is said at 20 left, at none and when the text is too long, through `#say`,
  a live region for what is said and not shown. A trick tagged or untagged says how many of the 10 the
  post carries, and a picture says it was attached and whether it was made smaller. The note under the
  menu ("Saving", "Not saved, retrying") is a live region that is no longer redrawn with the menu.
  Seen and left: every save says "Saving" and then "All changes saved", which is a lot to hear while
  marking levels; whether that should be only the failure and the recovery is for someone who listens
  with a screen reader to say. In the skill map editor a problem is written inside the field's label,
  so it is read as part of the field's name (the field is now marked invalid too), not as its
  description. An error the server words without naming a field (too many tries, no connection) marks
  no field. Not done: listening with a real screen reader.
