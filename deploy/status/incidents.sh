#!/usr/bin/env bash
# The incident record, validated and collected.
#
# Incidents live on `main`, in deploy/status/incidents/, one JSON file each,
# and NOT on the status-data branch the probe writes. Two reasons, and the
# second is the one that decided it.
#
# A note somebody writes during an outage is the highest stakes prose this
# project publishes, and it is written by a tired person at an unsociable
# hour. On main it gets a diff, a review and a history. On status-data it
# would be a hand edit of an orphan branch that a probe pushes to every few
# minutes, where the likely outcome of a mistake is a force push over the
# machine's own history.
#
# The cost is that an incident reaches the page on the next probe rather than
# instantly. That is at most one probe interval, and the alerting stack, not
# this page, is what wakes anybody.
#
# The status words are a closed vocabulary, and a short one, because the point
# of the bold word at the head of each update is that a reader learns the state
# of the incident without reading the sentence after it. A free text status
# would be a second sentence wearing bold.
#
# The lie this format exists to prevent: an incident history that is always
# empty because writing one is hard. So the format is a flat object with no
# tooling, no generator and no schema registry, and a malformed file is
# reported on the page rather than silently dropped.
#
#   incidents.sh check   <incidents-dir> [targets.json]
#   incidents.sh collect <incidents-dir>
#
# `check` is a gate: it exits nonzero and names every problem, and with a
# targets.json it also rejects a component id that does not exist, which is
# the typo that would otherwise attach an incident to nothing at all.
#
# `collect` is what the renderer calls. It never fails on a bad file. It emits
#
#   { "incidents": [ ... ], "unreadable": [ "name.json", ... ] }
#
# so one malformed file cannot blank the incident section, which is the same
# rule that governs a malformed reading: an element that cannot be read is one
# element, and losing the collection with it is a second failure on top of the
# first.

set -uo pipefail

TIMESTAMP='^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$'

# The validation, as one jq program, so `check` and `collect` cannot drift
# into disagreeing about what a valid incident is. It takes the parsed file
# and prints one line per problem, and prints nothing when the file is good.
read -r -d '' PROBLEMS <<'JQ' || true
def ts: type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$");
def nonempty: type == "string" and (. | gsub("\\s"; "") | length) > 0;

[
  (if type != "object" then "the file is not a JSON object" else empty end),

  (if (.id? | nonempty | not) then "id must be a non-empty string"
   elif .id != $stem then "id \"\(.id)\" does not match the file name \"\($stem)\""
   else empty end),

  (if (.title? | nonempty | not) then "title must be a non-empty string" else empty end),

  (if (.type? // "") | IN("incident", "maintenance") | not
   then "type must be \"incident\" or \"maintenance\"" else empty end),

  (if (.type? // "") == "incident" and (((.severity? // "") | IN("minor", "major", "critical")) | not)
   then "severity must be \"minor\", \"major\" or \"critical\" on an incident" else empty end),

  (if (.components? | type) != "array" then "components must be an array of component ids"
   elif (.components | length) == 0 then "components must name at least one component"
   elif (.components | map(nonempty) | all | not) then "every component id must be a non-empty string"
   else ( .components[] | select(($ids | length) > 0 and (IN($ids[]) | not))
          | "component \"\(.)\" is not a component in targets.json" )
   end),

  (if (.started_at? | ts | not)
   then "started_at must be a UTC timestamp like 2026-09-01T04:10:00Z" else empty end),

  (if (has("ended_at") and .ended_at != null and ((.ended_at | ts) | not))
   then "ended_at must be a UTC timestamp like 2026-09-01T05:02:00Z, or absent" else empty end),

  (if (has("ended_at") and .ended_at != null and (.started_at? | ts) and .ended_at < .started_at)
   then "ended_at is before started_at" else empty end),

  (if (.updates? | type) != "array" then "updates must be an array"
   elif (.updates | length) == 0 then "updates must carry at least one entry"
   else ( .updates | to_entries[]
          | .key as $i | .value as $u
          | ( if ($u.at? | ts | not) then "update \($i + 1): at must be a UTC timestamp" else empty end,
              if (($u.status? // "") | IN("investigating", "identified", "update", "monitoring", "resolved", "scheduled", "in progress", "completed") | not)
              then "update \($i + 1): status must be one of investigating, identified, update, monitoring, resolved, scheduled, in progress, completed"
              else empty end,
              if ($u.body? | nonempty | not) then "update \($i + 1): body must be a non-empty string" else empty end )
        )
   end)
] | .[]
JQ

collect_dir() {
  local dir="$1"
  [ -d "$dir" ] || { echo "$dir is not a directory" >&2; return 1; }
  # -print0 and a null-delimited read, because a path is allowed to contain a
  # space and a for-loop over unquoted find output is the classic way to turn
  # one file into two broken ones.
  find "$dir" -maxdepth 1 -type f -name '*.json' -print0 | sort -z
}

problems_for() {
  # $1 file, $2 stem, $3 ids-json
  jq -r --arg stem "$2" --argjson ids "$3" "$PROBLEMS" "$1" 2>/dev/null
}

known_ids() {
  # An empty list means "do not check component ids", which is what `collect`
  # wants: the renderer must show an incident even if a component was renamed
  # out from under it, and `check` is where that gets caught.
  local targets="${1:-}"
  if [ -n "$targets" ] && [ -f "$targets" ]; then
    jq -c '[.[].id]' "$targets"
  else
    echo '[]'
  fi
}

cmd_check() {
  local dir="${1:?usage: incidents.sh check <incidents-dir> [targets.json]}"
  local ids
  ids="$(known_ids "${2:-}")"
  local bad=0 count=0

  while IFS= read -r -d '' file; do
    count=$((count + 1))
    local stem problems
    stem="$(basename "$file" .json)"
    if ! jq -e . "$file" > /dev/null 2>&1; then
      echo "$file: is not valid JSON"
      bad=$((bad + 1))
      continue
    fi
    problems="$(problems_for "$file" "$stem" "$ids")"
    if [ -n "$problems" ]; then
      while IFS= read -r problem; do
        echo "$file: $problem"
      done <<< "$problems"
      bad=$((bad + 1))
    fi
  done < <(collect_dir "$dir")

  if [ "$bad" -gt 0 ]; then
    echo "$bad of $count incident files are unusable" >&2
    return 1
  fi
  echo "$count incident files are well formed"
  return 0
}

cmd_collect() {
  local dir="${1:?usage: incidents.sh collect <incidents-dir>}"
  local good='[]' unreadable='[]'

  if [ -d "$dir" ]; then
    while IFS= read -r -d '' file; do
      local stem name problems
      stem="$(basename "$file" .json)"
      name="$(basename "$file")"
      if jq -e . "$file" > /dev/null 2>&1 && [ -z "$(problems_for "$file" "$stem" '[]')" ]; then
        good="$(jq -c --slurpfile one "$file" '. + $one' <<<"$good")"
      else
        unreadable="$(jq -c --arg n "$name" '. + [$n]' <<<"$unreadable")"
      fi
    done < <(collect_dir "$dir")
  fi

  # Newest first, by the last thing that happened on the incident rather than
  # by when it opened, so a long incident that is still being updated does not
  # sink below a short one that opened after it.
  jq -nc --argjson good "$good" --argjson unreadable "$unreadable" '
    { incidents: ($good | sort_by([(.updates | map(.at) | max), .started_at]) | reverse),
      unreadable: $unreadable }'
}

case "${1:-}" in
  check)   shift; cmd_check "$@" ;;
  collect) shift; cmd_collect "$@" ;;
  *) echo "usage: incidents.sh check <dir> [targets.json] | incidents.sh collect <dir>" >&2; exit 2 ;;
esac
