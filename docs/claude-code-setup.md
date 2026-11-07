# Working on Flowa with Claude Code

What the repository provides, and one file to add by hand.

## In the repository

| Path | What it is |
| --- | --- |
| `CLAUDE.md` | Project memory loaded in every session: how work flows, the programming principles, the quick commands. |
| `.claude/rules/*.md` | Rules that apply by path (`paths:` in the front matter): Go server, web UI, demo physics, 3D figure, CI and tests. |
| `.claude/agents/*.md` | Scoped subagents: `ci-screenshots`, `demo-physics`, `figure-3d`, `web-ui`, `catalog-research`, `reviewer`. Each knows its files, its verification steps and what it must not do. |
| `.claude/skills/*/SKILL.md` | Slash commands for the recurring workflows: `/pr-checks`, `/ship`, `/new-demo`, `/screenshots`, `/new-feature`. |

Example: "add support for screenshots of the Plan page in CI" is a job for the `ci-screenshots`
agent; it adds a `page(...)` line to `test/browser/pages.py`, runs it locally, pushes, watches the
run and looks at the picture that CI posts.

## To add by hand: `.claude/settings.json`

Claude Code does not let an agent write its own permission settings, so this file is not in the
repository. Create it (or merge it into your user settings) to stop the routine checks from
prompting, make anything that touches the Pi or the remote ask first, and keep secrets unread:

```json
{
  "permissions": {
    "allow": [
      "Read(./**)",
      "Bash(gofmt -l *)", "Bash(go vet ./...)", "Bash(go build *)", "Bash(go test *)",
      "Bash(node --check *)", "Bash(node test/*)", "Bash(node sim/bake.js)", "Bash(npm run build)",
      "Bash(python3 -m py_compile *)", "Bash(test/browser/run.sh *)",
      "Bash(git status *)", "Bash(git diff *)", "Bash(git log *)",
      "Bash(gh pr view *)", "Bash(gh pr diff *)", "Bash(gh pr checks *)",
      "Bash(gh run list *)", "Bash(gh run view *)", "Bash(gh run watch *)",
      "Bash(docker rm -f flowa-test-db)"
    ],
    "ask": [
      "Bash(./deploy.sh*)", "Bash(docker compose *)",
      "Bash(git push *)", "Bash(gh pr create *)", "Bash(gh pr merge *)"
    ],
    "deny": [
      "Read(./.env)", "Read(./data/**)", "Bash(docker exec nunchaku-db *)"
    ]
  }
}
```

Optional hook, in the same file, to keep Go formatted after every edit:

```json
"hooks": {
  "PostToolUse": [{
    "matcher": "Edit|Write",
    "hooks": [{ "type": "command", "command": "f=$(python3 -c 'import json,sys;print(json.load(sys.stdin).get(\"tool_input\",{}).get(\"file_path\",\"\"))'); case \"$f\" in *.go) gofmt -w \"$f\";; esac" }]
  }]
}
```

`.claude/settings.local.json` is the place for anything personal; it is ignored by git.
