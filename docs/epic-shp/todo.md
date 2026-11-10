# SHP: One shape for the app

Why, scope and risks: [epics.md](../epics.md#shp-one-shape-for-the-app). Order: [roadmap.md](../roadmap.md).
Category: improvement. Size: M. Each item is one pull request or one commit of one, with a Conventional
Commits title of the category's type. An item is checked only when its DoD holds and the
[roadmap's definition of done](../roadmap.md#definition-of-done-for-any-epic-item) is met.

**Status:** not started (issue [#34](https://github.com/TuanSSM/flowtopia/issues/34)). 0 of 6 items done.

The four questions are in [audit-plan.md](../audit-plan.md), Structure. The recommended options are
listed here for the owner's decision before the first PR.

- [ ] **Decide.** Owner confirms or changes: MAP-01 (a) the prop in the route; MAP-05 one Progress
  card; MAP-06 Profile is who you are, Settings is how Flowa behaves; MAP-07 one Skill maps entry,
  the tree for the current prop only. DoD: the decision recorded here with the date.
- [ ] **The prop in the URL.** `#/<prop>/practice`, `#/<prop>/combos`, `#/<prop>/plan`,
  `#/<prop>/map`; old routes redirect to the member's prop; browsing another map changes nothing
  else. DoD: `combo.py` opens the poi map and asserts Practice still logs nunchaku; links in posts
  and notifications carry the prop.
- [ ] **One Progress card.** Level, streak and the week's challenges on the dashboard; a one line
  summary in the header of Practice, Combos and Plan; Plan opens on the current phase. DoD:
  `pages.py` finds the full widgets once; phone screenshot of Plan shows the current phase first.
- [ ] **Profile and Settings.** Profile: name, bio, links, avatar, picture, public preview. Settings:
  theme, dominant hand, sharing, props, data, sign out everywhere. The public profile is a view.
  DoD: every setting reachable from exactly one page; `account.py` or `pages.py` checks the split.
- [ ] **One Skill maps entry.** Sidebar: Skill maps, with the prop chosen on the page; the tree
  for the current prop, collapsed by default on phones; the drawer fits a 640 px tall screen. DoD:
  phone screenshot with the drawer open has no scroll.
- [ ] **Docs and tour.** README, the tour's steps and `docs/audit-plan.md`'s structure section
  updated to say what was decided and done. DoD: `test/docs.sh` green; tour runs on the new routes.
