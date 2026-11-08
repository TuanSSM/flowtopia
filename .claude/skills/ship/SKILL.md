---
name: ship
description: Take the current work from a dirty tree to a merge-ready pull request - branch, commit, push, open the PR, watch process_pr, read failures, and fetch the CI screenshots. Use when asked to "create a PR", "commit and push" or "ship it".
---
`main` is protected; everything goes through a PR. Steps:

1. `git status --short` and `git diff --stat`. Make sure nothing secret or accidental is staged
   (`.env`, `__pycache__`, `/tmp` paths, the real admin password).
2. If on `main`, create a branch named for the change (`type/short-name`, like `fix/proxy-addresses`);
   otherwise stay.
3. Run `/pr-checks` unless the user said to skip it.
4. Commit with a Conventional Commits title under 70 characters (`type(scope): summary`, types in
   CLAUDE.md) and a body in plain prose: what changed, why, what was verified. Breaking changes get
   `!` and a `BREAKING CHANGE:` line. No model names, no emoji. End with the Co-Authored-By line
   from the session.
5. `git push -u origin <branch>`; on a network error retry with 2, 4, 8, 16 s waits.
6. `gh pr create` with a Conventional Commits title (it becomes the squash commit on `main`, which
   sets the next version) and a body that has: Why, What (grouped by area), how it was verified, and
   anything the reviewer must do after merging (for example a ruleset change). End with the
   "Generated with Claude Code" line.
7. Watch: `id=$(gh run list --branch <branch> --workflow process_pr --limit 1 --json databaseId -q '.[0].databaseId'); gh run watch "$id" --exit-status`.
   On failure: `gh run view "$id" --json jobs -q '.jobs[] | "\(.conclusion)\t\(.name)"'`, then the
   failing job's log filtered for `FAIL|Error|Traceback|##\[error\]`. Fix, push, watch again.
8. When green, fetch one screenshot from the `ci-screenshots` branch (`pr-<n>/<sha7>/demos.png`)
   with `curl -H "Authorization: token $(gh auth token)"` and look at it.
9. Report: PR link, job results, what the screenshots showed, and anything the user must do.
   Do not merge unless asked.
