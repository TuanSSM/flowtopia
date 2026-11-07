#!/usr/bin/env bash
# Checks the documentation and the Claude Code files, which have no other test:
#   relative links in Markdown point at files that exist;
#   agents and skills in .claude have front matter with a name and a description;
#   rules in .claude/rules have a paths list.
# Runs in a few seconds and is the whole check for a docs-only pull request.
#   test/docs.sh
cd "$(dirname "$0")/.."
bad=0
say() { echo "$1"; bad=$((bad + 1)); }

# Links: ](path) and ](path#anchor), skipping URLs and mailto. Anchors are not checked.
while IFS=$'\t' read -r file link; do
  target=${link%%#*}; [ -z "$target" ] && continue
  case "$target" in http://*|https://*|mailto:*) continue;; esac
  [ -e "$(dirname "$file")/$target" ] || [ -e "$target" ] || say "$file: link to missing file $target"
done < <(grep -rnoE --include='*.md' --exclude-dir=node_modules --exclude-dir=.git '\]\([^)]+\)' . | sed -E 's/^\.\/([^:]+):[0-9]+:\]\((.*)\)$/\1\t\2/')

front() { # file key: 1 if the front matter has "key:"
  awk -v k="$2" 'NR==1 && $0!="---" {exit 1} NR>1 && $0=="---" {exit (found?0:1)} NR>1 && index($0, k":")==1 {found=1}' "$1"
}
for f in .claude/agents/*.md; do
  [ -e "$f" ] || continue
  front "$f" name || say "$f: no name in front matter"
  front "$f" description || say "$f: no description in front matter"
done
for f in .claude/skills/*/SKILL.md; do
  [ -e "$f" ] || continue
  front "$f" name || say "$f: no name in front matter"
  front "$f" description || say "$f: no description in front matter"
done
for f in .claude/rules/*.md; do
  [ -e "$f" ] || continue
  front "$f" paths || say "$f: no paths list in front matter"
done

n=$(find . -name '*.md' -not -path './*/node_modules/*' -not -path './.git/*' | wc -l)
echo "docs: $n Markdown files, $bad problems"
exit $((bad > 0))
