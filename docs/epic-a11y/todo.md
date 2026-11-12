# A11Y: Usable by keyboard, screen reader and a shaky hand

Why, scope and risks: [epics.md](../epics.md#a11y-usable-by-keyboard-screen-reader-and-a-shaky-hand). Order: [roadmap.md](../roadmap.md).
Category: improvement. Size: S. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** in progress (issue [#36](https://github.com/TuanSSM/flowtopia/issues/36)). 4 of 5 items done; Focus is built but for the controls under a skill's guide beside a wide page.

- [ ] **Focus.** Every control reachable by Tab in reading order, a visible focus ring in both
  themes, dialogs trap focus and return it. DoD: a keyboard-only browser test signs in, logs a
  session, publishes a combo and replies to a comment.
  Open: beside a wide page a skill's guide lies over the right 420 px of it, and 5 Tab stops at
  1440 px (the bell, the search button and what else lies there) take the focus under the guide,
  where it cannot be seen (WCAG 2.2, 2.4.11 Focus Not Obscured). The suite counts them and does not
  fail on them. Making them inert would mean working out from their places, at every size and
  scroll, which controls the guide covers, and the bell and the search button are in the bar at the
  top, which does not scroll, so no scroll margin brings them out; giving the guide room of its own
  changes the layout for everyone and belongs to SHP. The box is ticked when that is done.
  Built: `test/browser/keys.py` is the `integration (keys)` suite. It sends the browser real key presses
  (Tab, Shift+Tab, Enter, Space, Escape, the arrow keys and typed text; `key`, `type_text` and `tab_to`
  in `h.py`) and never focuses or clicks from the page's own JS. With them it signs in, logs a session
  of 40 minutes with a skill, builds a combo of two skills and publishes it, and replies to a comment,
  and reads each back through the API. On the way it presses Tab round 27 pages, the timeline once
  more with Shift+Tab, and 7 dialogs (35 rounds, 1,723 stops) until the focus is back where it began
  and holds each round against the page: every control on show is reached (the list `a11y.py` names;
  1,578 controls; a control drawn again since the round passed it counts as reached by a stop on one
  that looks the same, one stop for one control, so of two Load more buttons each needs its own); nothing hidden, inert, outside
  the window or under something else takes the focus, and no control has a positive `tabindex`; the
  order is reading order (no stop after one that lies more than a row, 24 px, below it, unless it
  starts at or beyond that one's right edge, as the next column of a layout does); each stop shows an
  outline of at least 2 px at 3:1 against what it is drawn on, read from the computed styles in the
  dark and the light theme, and no box cuts the outline off. Ten things that open over or beside the
  page are opened by key: the search, a report, a moderation action, the streak overlay, the menu of a
  narrow window, a skill's guide on a narrow window and beside a wide page, the notifications, the
  trick picker and the guided tour. Each takes the focus when it opens, closes on Escape and hands
  the focus back; the six that cover the page keep Tab and Shift+Tab inside and the page behind them
  is `inert`, and the tour keeps Tab inside too. 24 controls are used and must still have the focus,
  with its ring, when the page has been drawn again; a Remove and a Delete that are used must leave it
  on a control that deletes nothing. With real clicks (`point` in `h.py`) it checks that nothing of
  this moves the focus or scrolls the page for a person with a mouse, and that a playing combo costs
  the page's watcher no work then. 309 of 309 checks pass, none skipped, in headless Firefox 159 (see below).
  Before the fixes (this suite run on `main` at 89d8ed9), 101 of 312 checks failed; the count differs
  because each control Tab cannot reach adds a failed check of its own. No page had a link past the menu.
  3 controls could not be reached (Picture, Upload picture and Import JSON: labels around hidden file
  fields). The search box showed no ring, and the 8 rows of the trick picker and the 6 tabs of the
  avatar editor had theirs cut off by the box that scrolls them. 1 node of the branch graph came out
  of reading order (the graph listed its nodes by number, not by place). None of the 10 did all a
  dialog owes: Tab walked on into the page behind every one of the six that cover it (293 stops under
  a dialog, or in the menu of a narrow window while it was off the screen); the streak overlay, the
  menu and a skill's guide did not take the focus; a report, a moderation action and a skill's guide
  lost it when they closed; the notifications did not close on Escape, and the trick picker only from
  its search box; the tour left the focus on nothing when it ended. All 23 controls that were used
  there (one fewer than now, as one could not be reached) lost the focus when the page was drawn again, the list of skills in a combo a second time when the
  suggestions arrived. Enter on a search result that had the focus opened the result the arrow keys
  had last marked and then the search again. The rows of choices and the tabs said nothing of which
  one was chosen; Remove on a chosen skill and Delete on a comment left the focus on nothing; Escape
  on the search over a report closed both. Now none of these.
  What changed: a link that skips to the content is the first Tab stop. The page is drawn again from
  state after most actions, so what has the focus is written down as what it is (its part of the
  page, its kind and its data) and the control that answers to the same words gets the focus back
  after a redraw; one that is gone gives its place to the next of its kind, a new page to its
  content, and a Delete or Remove that is gone to the nearest control before it that deletes nothing.
  This is done for a person at the keys only: a click or a tap forgets what was written down for the
  keys and nothing is moved, and on a touch screen the focus is not put back into a text box, which
  would bring the keyboard up.
  What lies over the page keeps Tab to itself, is closed by Escape one thing at a press, and what it
  covers is `inert`. The rows of choices (theme, hand, graph or list, the editor's views, the
  overlay's format and position) carry `aria-pressed` and are walked with the arrow keys; the tabs of
  Admin, Moderation and the editor's props carry `aria-current`. The file fields are opened by a
  button. The graph lists its nodes row by row. Rings are drawn inside the control where a box that
  scrolls would cut them off, and a control Tab reaches under the bar at the top of the window is
  moved out from under it.
  For a mouse or a finger two things look different: the search box shows the ring every text box
  shows, and the pointer is a hand over Picture, Upload picture and Import JSON, as over every other
  button. The pictures of `pages.py` are the same before and after, but for the figure in mid-move
  and the clock.
  A person with a mouse walked 50 steps of the same journey on both trees (sign in, a session, the
  theme, a reply, the search, a report, the streak overlay, a guide, the menu of a narrow window): of
  14 pictures of pages left still, all are the same but for the search box's ring and a difference of 1
  in 255 in one colour channel on the corners of a few rounded boxes, which two runs of `main` show
  too. The controls keep their size. What opens over the page after a click hands no focus back when
  it closes, even when it is closed with Escape. `keys.py` checks two such cases with real clicks, each
  with a box typed in before and scrolled out of sight: a click beside the content, the search by its
  key and Escape; and a click on Report that does not focus the button, as in Safari and Firefox on
  macOS, then Escape. In both the focus and the scroll stay where they were. Both pass on `main`.
  A review before the merge changed three things. Typing in a box that was clicked, Enter included, no
  longer counts as working the page by keys: a comment sent with Enter after a click had left the
  focus, with its ring, in the box and scrolled the page 121 px. Escape over a dialog no longer also
  closes the notifications under it. And a change of words alone, such as the clock of a playing combo
  each frame, is not looked at unless the focus has dropped to the page as a whole.
  A second review changed five more, each with a check in `keys.py` that failed before (8 checks).
  The place to hand the focus back to was the last control that had had it, however long before: after
  a click elsewhere, Escape on a dialog or the search opened by the pointer sent the focus to a box
  typed in earlier and scrolled the page to it (1,200 px in the check). Now it is what had the focus
  when the layer opened by key, or nothing, and a click forgets the rest. The watcher did its work on
  every change of words whenever the focus was on the page as a whole, the usual state with a mouse:
  over 5 s of a playing combo after Pause and Play by mouse, 262 and 290 runs, now 1 and 0 (the one is
  a change of elements). One Escape on the search over a dialog closed both; now the search alone. A
  Delete or Remove that was used gave the focus to the next row's, where a second Enter deletes on.
  And a box the focus was put back into, then clicked to type in, was replaced by a redraw from the
  server and lost the focus; a click now marks it as being typed in.
  `a11y.py` no longer counts the page behind an open dialog, which is inert: 4,433 controls, 143
  pictures and 210 fields over the same 68 stops.
  The questions the browser itself asks (Delete this session?) are its own dialogs and are not
  pressed; before a comment is deleted the suite answers yes from outside, as `editor.py` does. The search lists up to 40 results and each
  is a Tab stop. The templates of the streak overlay are radio buttons by role that Tab stops at one
  by one; the arrow keys move between them and do not choose. Link cards are not on the journey (they
  need the stand-in the links suite has); their ring, and those of the skill map editor, the avatar
  editor's tabs and seven pages at 420 px, were looked at once by hand with the same checks, 25 more
  rounds with none failing. The suite was written on Chromium through a stand-in for Marionette. Its
  first runs in real Firefox (the pull request's CI, then headless Firefox 159 here) failed: 21 passed,
  14 failed, then a crash. All three causes were in the test, not in the page. Tab past the last control
  of a page takes the focus out of it to the browser's own bar, where Marionette cannot send keys; the
  keys that followed reached a page without the focus, and Firefox moved its focused element there with
  no focus event and no ring, so every stop of the landing page showed none, the skip link was cut down
  to nothing under the menu, and Tab went no further than two links of the menu. `key()` in `h.py` now
  gives the window its focus back after such a press, as a person's next Tab through the bar does, and
  drops the caret a text box leaves in the page, from which Firefox would start that Tab instead of
  from the top. And the suite scrolled the page in the same frame as its last typed key, before Firefox
  scrolled to what was typed; it now lets the page draw first. A failed sign-in now stops the journey
  with its failures counted, instead of a crash a few steps on. Firefox also stops Tab at every box
  that scrolls, which Chromium does only for a box with no control in it; the suite takes such a stop
  as one and holds it to a ring (1,723 stops in Firefox, 1,699 on Chromium). Not done: a pass with a
  real screen reader.
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
  543 checks.
  `test/browser/contrast.py` is the `integration (contrast)` suite. It first proves its arithmetic
  on pieces with known answers (black on white 21:1, #777 on white 4.48:1, half white over black,
  the size where text counts as large, a background from an ancestor, a faded parent), then walks
  58 stops in each theme, the tour opened once in each, and judges every visible text, icon and
  field (typed words, placeholder, edge) against what the computed styles put behind it: about
  18,500 pairs of colours.
  Before the fixes the stylesheet check failed 135 of 481 and the walk found 476 pairs below their
  contrast on 109 of 110 views, 27 different pairs of colours. The worst: the edge of a field at
  1.19:1 in the light theme and 1.30:1 in the dark one, of 3:1; the tick on a practised day 1.78:1
  of 3:1; white on the red count 3.23:1 and on the primary button 3.64:1 (dark) and 4.42:1 (light);
  the small grey line on a challenge that is done 3.62:1 (light) and 3.80:1 (dark); amber notes
  3.81:1 (light); the tour's Next button 3.90:1; the browser's own grey for placeholders 4.00:1.
  Now both find none. What moved: `--ink-3`, `--accent` and `--bad` in both themes, `--l3`,
  `--good` and `--warn` in the light one, each by the smallest step that passes; a new `--edge` for
  fields and the chosen segment; placeholders in `--ink-3`; the words on red and green pills in
  `--bg`; a dark tick on a practised day; the primary button darkens under the pointer; a quest
  that is done is no longer faded.
  Left out by the standard, each listed with its reason in the script: controls that are switched
  off; the flame and the badge emblem, which stand beside words that say the same; borders that
  only tint a box or only change under the pointer; the outline of a button, which its words mark;
  checkboxes, radio buttons and sliders, which the browser draws. Out of scope and counted, not
  passed: text and icons over a picture, a gradient or the demo canvas (about 90 in the walk, of
  three kinds the suite names; a fourth kind fails it), and the shapes of charts, the heatmap and
  progress bars (18 rules).
  The walk was written against Chromium; its first run on Firefox was the pull request's CI, where
  it passed (123 passed, 0 failed, 8 skipped). Seen
  and left: the search box in the palette shows no focus ring (Focus); the tour's Next button is
  put right by a rule in `app.css` over the colours the tour is handed, where the tour itself
  should ask for the right one.
  After a review, the gaps it found were closed. The stylesheet check now reads `@supports` and
  `@layer`, checks a rule outside and inside each block that changes it, refuses a colour in
  `@keyframes` and an `@import`, reads `color: inherit` on a background as a background for other
  words, reads channels written as percentages, wants every tint listed with what lies under it
  (none lies on `--card-2`, where three colours would fall to 4.1:1 to 4.4:1), and takes a side
  border, an outline colour or an inner ring on a state for a mark, and a class the templates put
  on a field for a field. The walk counts what is hidden under something or dimmed behind a dialog
  instead of judging it, judges the words of `::before` and `::after`, of button inputs and an svg
  that is all a button shows, asserts the message at the bottom while it is up, and edits a
  highlighted comment, where a field stands on a tint. `--edge` moved once more (dark #696c74 to
  #70737a, light #8a8a86 to #868682) to hold 3:1 there and in a row under the pointer. Two looks
  that had moved by accident are back: the third level of the map and the heatmap keeps its blue
  in the dark theme, with a dark number on it, and the drawn 2D figure has greys of its own
  (`--fig-body`, `--fig-arm`) instead of the greys of muted text.
  After a second review the stylesheet check changed its stance: what it cannot read fails with what
  to write instead, and what it wrongly refuses has a table. It fails on a colour it cannot turn into
  numbers wherever it stands (it now reads `hsl()` and `hwb()` too), on a nested rule, on a token set
  outside the three theme blocks or in a style attribute, and on a property that paints and is not
  one it places. It reads every spelling of a border and every shadow in a list, backgrounds in the
  order they are written, a gradient or picture under words as an unknown surface, and each set of
  blocks that can hold together. It no longer refuses sizes in `@keyframes` or in a style attribute,
  a utility class that happens to sit on a field, or a divider between rows; `NOT_A_STATE`,
  `NOT_A_FIELD`, `APART`, `LOCAL_TOKENS` and `OTHER_COLOURS` allow the rest. Its break-it run is 71
  broken copies that fail and 21 sound ones that pass. The walk holds its counts: nothing may lie
  dimmed behind a sheet at a stop that opens no dialog, a stop that opens one must judge what is in
  it, and the two stops at the comment being edited assert where the cursor is.
  What the stylesheet check still cannot see, and the walk is the judge of: which rules meet on one
  element, a state shown by a tint alone, a state marked by a whole border on a rule of one class,
  and a class or colour a script puts on an element.
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
