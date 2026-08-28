#!/usr/bin/env bash
#
# The G8 forbidden token scan.
#
# Documentation is a product surface, and the tokens below are the ones that
# say a page was never finished: a note to the author, a slot nobody filled, a
# name that belongs to a person or a customer rather than to the product, an
# address that only resolves inside somebody's network, or an identifier that
# names a real cloud tenant.
#
# Usage:
#   tools/docs/forbidden.sh [path ...]
#
# With no paths it scans the documentation site and the repository's front
# door. Exit 0 means zero hits.

set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
# Overridable so the scan's own tests can drive it with their own files. The
# defaults are the repository's, which is what every real invocation uses.
exemptions="${AF_FORBIDDEN_EXEMPTIONS:-$root/tools/docs/forbidden-exemptions.tsv}"
extra="${AF_FORBIDDEN_EXTRA:-$root/tools/docs/forbidden-extra.txt}"

if [ "$#" -gt 0 ]; then
  targets=("$@")
else
  # The pages a user reads, and nothing else. docs/plan is the build log for
  # this repository rather than product documentation: it exists to record what
  # is unfinished, so scanning it for the word for unfinished would be a gate
  # that can only be passed by lying in it.
  targets=(
    "$root/docs/src/content/docs"
    "$root/examples"
    "$root/README.md"
    "$root/CONTRIBUTING.md"
    "$root/SECURITY.md"
    "$root/CODE_OF_CONDUCT.md"
  )
fi

present=()
for t in "${targets[@]}"; do
  [ -e "$t" ] && present+=("$t")
done
if [ "${#present[@]}" -eq 0 ]; then
  echo "forbidden: none of the scan targets exist, so this check is looking in the wrong place" >&2
  exit 1
fi

# Each rule is a name and an extended regular expression. Matching is case
# sensitive, because TODO and WIP are markers rather than words and a case
# insensitive scan would fire on "wip" inside an identifier. The prose words
# carry their sentence initial form explicitly instead, which is how "Lorem
# ipsum" is caught: it is always written that way and a bare lowercase rule
# missed it.
#
# Word boundaries are explicit: "hack" must not fire on "hackathon", and "xxx" must not fire on a
# hexadecimal digest that happens to contain it.
#
# The last three are classes rather than words. An address ending .internal,
# .corp, .lan or .local resolves on somebody's private network and nowhere
# else; a bare GUID in documentation is almost always a real subscription or
# tenant identifier that was pasted from a console.
#
# Two of the plan's tokens are scoped to their marker sense rather than matched
# as bare words, and the reason is that both are also product vocabulary here.
# The sandbox really does hand a container a placeholder credential, and a
# migration really does run against a temporary server. A gate that fired on
# those would be answered by rewording accurate documentation, which is the
# gate making the product worse. What is caught instead is the slot nobody
# filled and the note that says this is not the real thing yet.
rules=(
  'unfinished note|\<(TODO|TBD|FIXME|WIP)\>'
  'filler text|\<[Ll]orem\>'
  'a promise instead of a page|[Cc]oming soon'
  'an unfilled slot|(\[insert|\[placeholder|<placeholder|PLACEHOLDER|\{\{[A-Z_]{2,}\}\}|\<xxx\>|\[Object Object\])'
  'work marked as not the real thing|(\<[Hh]ack(y|s)?\>|[Tt]emporary (workaround|solution|fix|measure|shim|stub|answer)|[Ff]or the time being)'
  'an address that resolves only inside a private network|[a-z0-9-]+\.(internal|corp|lan|local)\>'
  'a subscription or tenant identifier|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}'
)

# Names of people and customers cannot be pattern matched, so they are listed.
# The file is the mechanism; what goes in it is a decision for whoever knows
# which names must never appear.
if [ -f "$extra" ]; then
  while IFS= read -r line; do
    case "$line" in ''|'#'*) continue ;; esac
    rules+=("a name that is not the product's|\\<${line}\\>")
  done < "$extra"
fi

hits=0
used=""
report() {
  local file=$1 line=$2 why=$3 text=$4
  local rel=${file#"$root"/}

  # An exemption is a path, the rule it excuses, and a reason. All three, so
  # that reading the file tells you why a hit was allowed rather than only
  # that somebody allowed it.
  if [ -f "$exemptions" ]; then
    while IFS=$'\t' read -r epath ewhy _; do
      case "$epath" in ''|'#'*) continue ;; esac
      if [ "$epath" = "$rel" ] && [ "$ewhy" = "$why" ]; then
        used="$used$epath	$ewhy
"
        return 0
      fi
    done < "$exemptions"
  fi

  printf '%s:%s: %s\n    %s\n' "$rel" "$line" "$why" "$text" >&2
  hits=$((hits + 1))
}

files=$(find "${present[@]}" -type f \( -name '*.md' -o -name '*.mdx' \) | sort)
count=$(printf '%s\n' "$files" | grep -c . || true)

for rule in "${rules[@]}"; do
  why=${rule%%|*}
  pattern=${rule#*|}
  while IFS= read -r hit; do
    [ -z "$hit" ] && continue
    file=${hit%%:*}
    rest=${hit#*:}
    line=${rest%%:*}
    text=${rest#*:}
    report "$file" "$line" "$why" "$text"
  done < <(printf '%s\n' "$files" | tr '\n' '\0' | xargs -0 grep -nE "$pattern" 2>/dev/null || true)
done

# An exemption that excuses nothing is a claim about the tree that stopped
# being true. Left alone it becomes a licence nobody granted.
if [ -f "$exemptions" ]; then
  stale=0
  while IFS=$'\t' read -r epath ewhy ereason; do
    case "$epath" in ''|'#'*) continue ;; esac
    if [ -z "$ereason" ]; then
      echo "forbidden: the exemption for $epath has no reason" >&2
      stale=$((stale + 1))
      continue
    fi
    case "$used" in
      *"$epath	$ewhy"*) ;;
      *)
        echo "forbidden: the exemption for $epath ($ewhy) matches nothing and can be deleted" >&2
        stale=$((stale + 1))
        ;;
    esac
  done < "$exemptions"
  hits=$((hits + stale))
fi

if [ "$hits" -gt 0 ]; then
  echo "forbidden: $count documents, $hits problems" >&2
  exit 1
fi
echo "forbidden: $count documents, 0 forbidden tokens"
