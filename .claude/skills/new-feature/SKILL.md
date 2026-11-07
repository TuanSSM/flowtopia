---
name: new-feature
description: Plan and deliver a feature that crosses areas (server, UI, demos, figure, CI) by splitting it between the scoped agents and the checks each must pass. Use for a request like "add support for X" when X touches more than one directory.
---
1. Say in two sentences what the user will be able to do afterwards and how it will be verified.
2. Map the work to areas and their owners:
   - server, API, database: the rules in `.claude/rules/go-server.md` (do it yourself)
   - pages, controls, copy: the `web-ui` agent
   - demos and physics: the `demo-physics` agent
   - the figure's look or motion: the `figure-3d` agent
   - checks, PR summary, screenshots: the `ci-screenshots` agent
   - new skills or source questions: the `catalog-research` agent
   Give each agent the exact files, the acceptance check, and what the others are doing; run
   independent parts in parallel; do not redo their work.
3. Every feature ships with: its check (a browser test, an expectation, or a CI step), a line in
   `README.md` if a user would look for it, a feature flag if it can break a page, and a
   screenshot in `test/browser/pages.py` if it has a page.
4. Before the PR, run `/pr-checks`, then ask the `reviewer` agent for a read of the diff and fix
   what it finds. Then `/ship`.
