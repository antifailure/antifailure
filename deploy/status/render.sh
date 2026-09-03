#!/usr/bin/env bash
# Folds one probe run into the history and renders the page that shows it.
#
# The history and the page are data, not code: they never live on `main`. The
# workflow that calls this checks out a separate branch for them, so a probe
# every few minutes is a few minutes of commits on a branch nothing else reads,
# rather than a few minutes of commits on the branch that triggers a staging
# deploy on every push. Mixing the two would mean this page's own upkeep
# redeploys the thing it is watching.
#
#   render.sh <out-dir> <new-readings.jsonl> [scripts-dir]
#
# <scripts-dir> defaults to this script's own directory and is the checkout of
# `main` holding targets.json, page.jq and the incident files. It is a separate
# argument because the workflow has two checkouts, the scripts at `main/` and
# the data at `data/`, and the whole point is that they are different trees.
#
# Three files are written into <out-dir>, and all three belong together: the
# page is nothing but a rendering of the record sitting next to it.
#
#   history.json  raw readings, recent, bounded by age and by count
#   daily.json    one rollup per component per UTC day, which is what lets the
#                 page show ninety days without keeping ninety days of raw
#   index.html    the page
#   feed.xml      the Atom feed the page's Subscribe control points at

set -euo pipefail

OUT="${1:?usage: render.sh <out-dir> <new-readings.jsonl> [scripts-dir]}"
READINGS="${2:?usage: render.sh <out-dir> <new-readings.jsonl> [scripts-dir]}"
HERE="${3:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}"

HISTORY="$OUT/history.json"
DAILY="$OUT/daily.json"
TARGETS="$HERE/targets.json"
PAGE_JQ="$HERE/page.jq"
FEED_JQ="$HERE/feed.jq"

# Where this page is served from, used for the absolute links Atom wants.
# Atom resolves a relative link differently in different readers, so this is
# one variable rather than a guess repeated in four places. It is the GitHub
# Pages address for this repository; change it here, and only here, if the
# page ever moves to a custom domain.
STATUS_BASE_URL="${STATUS_BASE_URL:-https://antifailure.github.io/antifailure/}"
INCIDENT_DIR="$HERE/incidents"

# Raw readings are kept for this long and no longer. Everything older survives
# as a daily rollup, which is what the ninety day strip is drawn from, so the
# window here is only about how much per-check detail is worth carrying: the
# latest reading of each component, and enough behind it to answer "what
# happened in the last day". Two bounds rather than one, because a count alone
# grows unboundedly in time when the probe is slow and an age alone grows
# unboundedly in size when it is fast.
KEEP_READINGS=1000
KEEP_READING_DAYS=35
KEEP_DAILY_DAYS=400
STRIP_DAYS=90

# Time, as a dependency, the way the control plane already treats it.
#
# The clock is read exactly once, here, and the number is passed on to every
# program below. Nothing else in this script or in page.jq asks what time it
# is. That matters because almost every claim this page makes is a claim about
# a moment: how long ago a check landed, whether a component has gone quiet,
# which UTC day a reading belongs to, which fourteen days of incidents are
# still shown, how far back the ninety day strip reaches.
#
# A suite that cannot pin the moment cannot test any of them. It has to build
# its fixtures from the wall clock, and then a case saying "three checks on one
# day, one of them failed" means three checks on one day when it runs at noon
# and two days when it runs at half past midnight. It quietly becomes a
# different case rather than failing, and the assertion that goes red is red
# about the hour rather than about the renderer.
#
# So the clock is injectable, and render_test.sh pins it. Nothing in the
# workflow sets this, and unset it is the system clock exactly as before.
NOW_EPOCH="${STATUS_NOW_EPOCH:-$(date -u +%s)}"
case "$NOW_EPOCH" in
  '' | *[!0-9]*)
    echo "render.sh: STATUS_NOW_EPOCH must be whole seconds since the epoch, not \"$NOW_EPOCH\"" >&2
    exit 1
    ;;
esac

for tool in jq date; do
  command -v "$tool" > /dev/null || { echo "render.sh needs $tool" >&2; exit 1; }
done

# Formatted by jq rather than by date, because turning an epoch back into a
# stamp is `date -r` on a BSD userland and `date -d @` on a GNU one, and jq is
# already a hard requirement one line above.
GENERATED="$(jq -rn --argjson now "$NOW_EPOCH" '$now | todate')"

[ -f "$TARGETS" ] || { echo "render.sh: no targets at $TARGETS" >&2; exit 1; }
[ -f "$PAGE_JQ" ] || { echo "render.sh: no page program at $PAGE_JQ" >&2; exit 1; }
[ -f "$FEED_JQ" ] || { echo "render.sh: no feed program at $FEED_JQ" >&2; exit 1; }

# Every intermediate below goes through a file rather than a shell variable.
# `jq --argjson history "$merged"` is the obvious way to write this and it
# fails in production rather than in a test: the whole record travels on the
# command line, and seven components at the retention cap is well over a
# megabyte, which is past ARG_MAX. The symptom is "Argument list too long" and
# a page that silently stops updating once the history is big enough, which is
# to say months after anyone would connect the two.
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$OUT"
[ -f "$HISTORY" ] || echo '[]' > "$HISTORY"
[ -f "$DAILY" ] || echo '{"counted_through":{},"days":[]}' > "$DAILY"

# A history file that will not parse at all is not recoverable element by
# element, and the wrong response is to start a fresh one: that silently
# destroys the record, which is the only thing this branch exists to hold. So
# it fails loudly and writes nothing. The run goes red, the files stay as they
# were, and a person decides.
jq -e 'type == "array"' "$HISTORY" > /dev/null 2>&1 || {
  echo "::error::$HISTORY is not a JSON array. Refusing to overwrite it; the record is not replaceable." >&2
  exit 1
}
jq -e 'type == "object" and (.days | type) == "array"' "$DAILY" > /dev/null 2>&1 || {
  echo "::error::$DAILY is not a rollup object. Refusing to overwrite it; the record is not replaceable." >&2
  exit 1
}

# One bad line must not cost the whole run's readings. `jq -s` over a .jsonl
# with one malformed line parses nothing at all, so each line is read as text
# and parsed on its own, and the ones that do not parse are counted.
jq -R -s 'split("\n") | map(select(length > 0)) | map(fromjson? // empty)' "$READINGS" > "$TMP/new.json"
new_lines="$(grep -c '[^[:space:]]' "$READINGS" || true)"
new_ok="$(jq 'length' "$TMP/new.json")"
dropped_new=$(( new_lines - new_ok ))
[ "$dropped_new" -gt 0 ] && echo "::warning::$dropped_new of $new_lines lines in $READINGS did not parse and were skipped" >&2

# The same rule one level down: an element inside the history that is not a
# usable reading is dropped and counted rather than allowed to fail the fold.
# A reading is usable when it has a timestamp in the shape the probe writes
# and something to identify a component by. Older readings carry `name` where
# an id belongs, and both are accepted, because rewriting the record to fit a
# newer shape is the one thing a record must never do.
readable='
  def usable: type == "object"
    and ((.checked_at? | type) == "string")
    and (.checked_at | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))
    and ((((.id? // .name?) // "") | type) == "string")
    and ((((.id? // .name?) // "") | length) > 0);
  map(select(usable))'

jq -n --slurpfile old "$HISTORY" --slurpfile new "$TMP/new.json" "
  ((\$old[0] + \$new[0]) | $readable) | sort_by(.checked_at)" > "$TMP/usable.json"

jq --argjson keep "$KEEP_READINGS" --argjson days "$KEEP_READING_DAYS" --argjson now "$NOW_EPOCH" '
  . as $all
  | ($now - ($days * 86400)) as $floor
  | [ ($all | group_by(.id // .name))[]
      | map(select((.checked_at | fromdateiso8601) >= $floor))
      | sort_by(.checked_at)
      | .[-$keep:][] ]
  | sort_by(.checked_at)
' "$TMP/usable.json" > "$TMP/merged.json"
dropped_old=$(( $(jq 'length' "$HISTORY") + new_ok - $(jq 'length' "$TMP/usable.json") ))

# The rollups.
#
# These are counted forward from a watermark, not recomputed from the raw
# history, and the difference is a bug this suite caught rather than a
# preference. Recomputing looks obviously right and is wrong for one reason:
# the raw history is pruned, so on any day busier than the cap the recompute
# reads fewer checks than actually happened and writes that smaller number
# down as the day's total. The first version of this did exactly that and
# understated a 1400 check day as 1000.
#
# So each run adds only the readings newer than the last one it counted, per
# component, and moves the watermark. That is idempotent: a re-run of the same
# probe adds nothing, because none of its readings is newer than the watermark
# it just set. It also bootstraps correctly, because an absent watermark sorts
# before every timestamp and the first run therefore counts the whole retained
# history exactly once.
#
# The cost is that a reading arriving out of order, older than the watermark,
# is not counted. The probe writes one batch per run with a single timestamp
# and the workflow serialises its own runs, so that does not happen; it is
# recorded here because it is the assumption that would have to break.
jq -n --slurpfile old "$DAILY" --slurpfile rows "$TMP/usable.json" \
  --argjson days "$KEEP_DAILY_DAYS" --argjson now "$NOW_EPOCH" '
  def ok: if has("ok") and ((.ok | type) == "boolean") then .ok
          elif has("ready") and ((.ready | type) == "boolean") then .ready
          else false end;

  ($rows[0]) as $rows
  | ($old[0] // {}) as $prev
  | ($prev.counted_through // {}) as $mark
  | (($prev.days // []) | map(select(type == "object" and (.id | type) == "string"
        and (.day | type) == "string" and (.checks | type) == "number"
        and (.ok | type) == "number"))) as $stored

  | ($rows | map(. + {_id: (.id // .name)})
           | map(select(.checked_at > ($mark[._id] // "")))) as $fresh

  | ($fresh | group_by([._id, (.checked_at | fromdateiso8601 | strftime("%Y-%m-%d"))])
            | map({ id: .[0]._id,
                    day: (.[0].checked_at | fromdateiso8601 | strftime("%Y-%m-%d")),
                    checks: length,
                    ok: (map(if ok then 1 else 0 end) | add) })) as $added

  | (($stored + $added) | group_by([.id, .day])
       | map({ id: .[0].id, day: .[0].day,
               checks: (map(.checks) | add), ok: (map(.ok) | add) })) as $all

  | (($now - ($days * 86400)) | strftime("%Y-%m-%d")) as $floor
  | { counted_through: ($mark + ($rows | map(. + {_id: (.id // .name)})
                                      | group_by(._id)
                                      | map({key: .[0]._id, value: (map(.checked_at) | max)})
                                      | from_entries
                                | with_entries(.value = ([.value, ($mark[.key] // "")] | max)))),
      days: ($all | map(select(.day >= $floor)) | sort_by([.id, .day])) }
' > "$TMP/daily.json"

"$HERE/incidents.sh" collect "$INCIDENT_DIR" > "$TMP/incidents.json"

cp "$TMP/merged.json" "$HISTORY"
cp "$TMP/daily.json" "$DAILY"
jq -c '.days' "$TMP/daily.json" > "$TMP/days.json"

jq -r -n -f "$PAGE_JQ" \
  --argjson now "$NOW_EPOCH" \
  --slurpfile targets "$TARGETS" \
  --slurpfile history "$TMP/merged.json" \
  --slurpfile daily "$TMP/days.json" \
  --slurpfile incidents "$TMP/incidents.json" \
  --argjson dropped "$(( dropped_new + (dropped_old > 0 ? dropped_old : 0) ))" \
  --argjson stripDays "$STRIP_DAYS" \
  --arg generated "$GENERATED" \
  > "$OUT/index.html"

jq -r -n -f "$FEED_JQ" \
  --argjson now "$NOW_EPOCH" \
  --slurpfile targets "$TARGETS" \
  --slurpfile history "$TMP/merged.json" \
  --slurpfile incidents "$TMP/incidents.json" \
  --arg base "$STATUS_BASE_URL" \
  > "$OUT/feed.xml"

echo "rendered $(jq 'length' "$TMP/merged.json") readings and $(jq '.days | length' "$TMP/daily.json") daily rollups into $OUT/index.html"
