#!/usr/bin/env bash
# The states this page is actually going to be in.
#
# A status page spends almost all of its life in one state and matters in the
# others, so the ones nobody builds are the ones tested here: no history at
# all, one reading, a gap in the middle, a component that has never been
# probed, a probe that stopped running, and a reading the renderer cannot
# parse. Each case asserts the page SAYS the right thing, not merely that the
# renderer exited zero, because a renderer that swallows a case and emits a
# confident page is the failure mode.
#
# Every case also asserts something that must NOT be on the page. An assertion
# that only checks for presence passes against a page that says everything.
#
#   render_test.sh
#
# Needs jq and bash. Writes only under a temporary directory.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

pass=0
fail=0
current=""

case_start() { current="$1"; echo; echo "  $current"; }

expect() { # expect <description> <file> <needle>
  if grep -qF -- "$3" "$2"; then
    pass=$((pass + 1)); echo "    ok   $1"
  else
    fail=$((fail + 1)); echo "    FAIL $1: the page does not contain \"$3\""
  fi
}

refute() { # refute <description> <file> <needle>
  if grep -qF -- "$3" "$2"; then
    fail=$((fail + 1)); echo "    FAIL $1: the page contains \"$3\" and must not"
  else
    pass=$((pass + 1)); echo "    ok   $1"
  fi
}

expect_exit() { # expect_exit <description> <expected> <actual>
  if [ "$2" = "$3" ]; then
    pass=$((pass + 1)); echo "    ok   $1"
  else
    fail=$((fail + 1)); echo "    FAIL $1: expected exit $2, got $3"
  fi
}

# A scripts directory the test controls, so a case can supply its own targets
# and its own incident files without touching the real ones.
scripts() { # scripts <name> -> prints the path
  local dir="$WORK/scripts-$1"
  mkdir -p "$dir/incidents"
  cp "$HERE/page.jq" "$HERE/incidents.sh" "$dir/"
  cp "$HERE/targets.json" "$dir/targets.json"
  chmod +x "$dir/incidents.sh"
  echo "$dir"
}

run() { # run <out-dir> <readings> <scripts-dir>
  "$HERE/render.sh" "$1" "$2" "$3" > "$1/render.log" 2>&1
}

iso() { # iso <seconds-ago>
  local ago="$1"
  if date -u -r 0 > /dev/null 2>&1; then
    date -u -r "$(( $(date -u +%s) - ago ))" +%Y-%m-%dT%H:%M:%SZ   # BSD
  else
    date -u -d "@$(( $(date -u +%s) - ago ))" +%Y-%m-%dT%H:%M:%SZ  # GNU
  fi
}

reading() { # reading <id> <seconds-ago> <ok>
  jq -nc --arg id "$1" --arg at "$(iso "$2")" --argjson ok "$3" \
    '{checked_at: $at, id: $id, name: $id, group: "g", url: "https://example.invalid",
      check: "http", http_status: (if $ok then 200 else 503 end), ok: $ok, ready: null,
      duration_ms: 120, commit: "", detail: (if $ok then "" else "HTTP 503" end)}'
}

echo "render.sh over the states this page will actually be in"

# ---------------------------------------------------------------------------
case_start "no history at all, and no readings: the first run ever"
d="$WORK/empty"; mkdir -p "$d"; s="$(scripts empty)"
: > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
expect_exit "renders rather than failing on an absent history" 0 "$?"
expect "says nothing has been checked" "$d/index.html" "Nothing has been checked yet."
expect "does not claim a component is operational" "$d/index.html" "Not yet checked"
refute "not one day is drawn as a passing day" "$d/index.html" 'class="cell cell--ok"'
expect "every day is drawn as unknown" "$d/index.html" 'class="cell cell--unknown"'
expect "the shape signal that carries state without colour is present" "$d/index.html" "box-shadow: inset 0 2px 0 var(--ink);"
expect "creates an empty history" "$d/history.json" "[]"
refute "states no availability figure it cannot support" "$d/index.html" "100%"
refute "no window is claimed" "$d/index.html" ">90d<"

# ---------------------------------------------------------------------------
case_start "exactly one reading: no interval can be measured from a single point"
d="$WORK/one"; mkdir -p "$d"; s="$(scripts one)"
reading control-plane-api 60 true > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
expect_exit "renders" 0 "$?"
expect "reports the one component it checked" "$d/index.html" "Everything checked so far is answering."
expect "says the other components have never been checked" "$d/index.html" "components have never been checked."
refute "does not invent a check interval from one timestamp" "$d/index.html" "checks arriving about every"
expect "shows the single check as the whole record" "$d/index.html" "1 of 1 check"

# ---------------------------------------------------------------------------
case_start "a gap in the middle of the record"
d="$WORK/gap"; mkdir -p "$d"; s="$(scripts gap)"
{ for ago in 1728000 1641600 1555200 259200 172800 86400 3600; do
    reading control-plane-api "$ago" true
  done; } > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
expect_exit "renders" 0 "$?"
expect "the days with no reading carry the class that draws them grey" "$d/index.html" 'class="cell cell--unknown"'
expect "the days with readings carry the passing class" "$d/index.html" 'class="cell cell--ok"'
expect "and the hover text agrees with the class" "$d/index.html" "no check recorded"
expect "the strip's label counts the days it does not know about" "$d/index.html" "with no check recorded."

# ---------------------------------------------------------------------------
case_start "a component that has never been probed, beside ones that have"
d="$WORK/never"; mkdir -p "$d"; s="$(scripts never)"
{ reading control-plane-api 3600 true; reading console 3600 true; } > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
expect "names the state rather than showing an empty row" "$d/index.html" "Not yet checked"
expect "says so in words on the row" "$d/index.html" "No check has been recorded for this component yet."
refute "never claims an unprobed component is operational" "$d/index.html" "Everything is answering."

# ---------------------------------------------------------------------------
case_start "a malformed line in the readings, and a malformed element in the history"
d="$WORK/bad"; mkdir -p "$d"; s="$(scripts bad)"
jq -n '[ {checked_at: "not a timestamp", id: "console", ok: true},
         "a bare string where a reading belongs",
         null,
         {id: "console", ok: true},
         {checked_at: "2026-08-30T00:00:00Z", name: "staging", ready: true} ]' > "$d/history.json"
{ reading control-plane-api 3600 true
  printf '{ this line is not json\n'
  reading console 3600 true; } > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
expect_exit "one bad line does not fail the run" 0 "$?"
expect "the good readings survived" "$d/index.html" "Control plane API"
expect "the legacy reading with name and ready survived" "$d/index.html" "Control plane, staging"
expect "the page says how many it could not read" "$d/index.html" "unreadable and skipped."
kept="$(jq 'length' "$d/history.json")"
expect_exit "exactly the three usable readings are kept" 3 "$kept"

# ---------------------------------------------------------------------------
case_start "a history file that will not parse at all"
d="$WORK/corrupt"; mkdir -p "$d"; s="$(scripts corrupt)"
printf 'this is not json at all' > "$d/history.json"
before="$(cat "$d/history.json")"
: > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
rc="$?"
expect_exit "fails loudly rather than starting a fresh record" 1 "$rc"
if [ "$before" = "$(cat "$d/history.json")" ]; then
  pass=$((pass + 1)); echo "    ok   the unreadable record is left exactly as it was"
else
  fail=$((fail + 1)); echo "    FAIL the unreadable record was overwritten"
fi

# ---------------------------------------------------------------------------
case_start "a rollup file in the wrong shape is refused, not replaced"
d="$WORK/rollshape"; mkdir -p "$d"; s="$(scripts rollshape)"
jq -n '[{id: "control-plane-api", day: "2026-01-05", checks: 288, ok: 280}]' > "$d/daily.json"
before="$(cat "$d/daily.json")"
reading control-plane-api 600 true > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
expect_exit "fails loudly" 1 "$?"
if [ "$before" = "$(cat "$d/daily.json")" ]; then
  pass=$((pass + 1)); echo "    ok   the rollup file is left exactly as it was"
else
  fail=$((fail + 1)); echo "    FAIL the rollup file was overwritten"
fi

# ---------------------------------------------------------------------------
case_start "an outage: one component failing its most recent check"
d="$WORK/down"; mkdir -p "$d"; s="$(scripts down)"
{ for ago in 10800 7200 3600 600; do reading console "$ago" true; done
  reading control-plane-api 600 false
  reading control-plane-api 3600 true; } > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
expect "the headline names the component" "$d/index.html" "Control plane API is not answering."
expect "the verdict block escalates" "$d/index.html" 'class="verdict is-down"'
expect "the row says which failure it was" "$d/index.html" "HTTP 503"
expect "the day is drawn as partly failed" "$d/index.html" "cell cell--bad"
refute "does not also say everything is answering" "$d/index.html" "Everything is answering."

# ---------------------------------------------------------------------------
case_start "two components down at once"
d="$WORK/down2"; mkdir -p "$d"; s="$(scripts down2)"
{ reading console 600 false; reading control-plane-api 600 false; } > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
expect "counts them rather than naming one" "$d/index.html" "2 components are not answering."

# ---------------------------------------------------------------------------
case_start "recovered: the latest check passed and an earlier one did not"
d="$WORK/recovered"; mkdir -p "$d"; s="$(scripts recovered)"
{ reading control-plane-api 7200 false
  reading control-plane-api 3600 true
  reading control-plane-api 600 true; } > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
expect "the verdict says it is answering now" "$d/index.html" "Everything is answering now."
expect "the row is marked recovered" "$d/index.html" "Recovered"
expect "the row counts the failures" "$d/index.html" "of 3 checks failed in the last 24 hours."
refute "does not call a recovered component down" "$d/index.html" "is not answering."

# ---------------------------------------------------------------------------
case_start "stale: the probe stopped running"
d="$WORK/stale"; mkdir -p "$d"; s="$(scripts stale)"
{ for ago in 604800 601200 597600 594000; do reading control-plane-api "$ago" true; done; } > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
expect "says the checks stopped rather than showing the last one as current" "$d/index.html" "Not checked recently"
expect "the verdict reflects it" "$d/index.html" "Some components have not been checked recently."
expect "the row says how long it has been" "$d/index.html" "which is longer than the interval this probe has been keeping."

# ---------------------------------------------------------------------------
case_start "incidents: one open, one closed, one scheduled, one unreadable"
d="$WORK/inc"; mkdir -p "$d"; s="$(scripts inc)"
cat > "$s/incidents/2026-08-20-closed.json" <<'JSON'
{ "id": "2026-08-20-closed", "title": "Reports were delayed by up to nine minutes",
  "type": "incident", "severity": "minor", "components": ["control-plane-api"],
  "started_at": "2026-08-20T09:00:00Z", "ended_at": "2026-08-20T09:41:00Z",
  "updates": [ { "at": "2026-08-20T09:41:00Z", "status": "resolved", "body": "A backlog cleared." } ] }
JSON
cat > "$s/incidents/2026-08-31-open.json" <<'JSON'
{ "id": "2026-08-31-open", "title": "Sign-in is returning 503",
  "type": "incident", "severity": "major", "components": ["control-plane-api", "console"],
  "started_at": "2026-08-31T04:10:00Z",
  "updates": [ { "at": "2026-08-31T04:18:00Z", "status": "investigating", "body": "We are looking at it." } ] }
JSON
cat > "$s/incidents/2026-09-20-maint.json" <<'JSON'
{ "id": "2026-09-20-maint", "title": "Database upgrade, up to twenty minutes of read-only time",
  "type": "maintenance", "components": ["control-plane-api"],
  "started_at": "2026-09-20T01:00:00Z",
  "updates": [ { "at": "2026-09-01T00:00:00Z", "status": "scheduled", "body": "Planned." } ] }
JSON
printf '{ nope' > "$s/incidents/broken.json"
reading control-plane-api 600 true > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
expect "the open incident is above the components" "$d/index.html" "Open incident"
expect "the open incident's title shows" "$d/index.html" "Sign-in is returning 503"
expect "it is marked still open" "$d/index.html" "still open"
expect "scheduled maintenance has its own block" "$d/index.html" "Scheduled maintenance"
expect "the closed incident is in the history" "$d/index.html" "Reports were delayed by up to nine minutes"
expect "the closed one shows how long it lasted" "$d/index.html" "after 41 minutes"
expect "the unreadable file is named rather than dropped silently" "$d/index.html" "broken.json"
refute "the empty-history sentence is gone once there are incidents" "$d/index.html" "No incident has been recorded"
refute "and the closed-record sentence too, since one has closed" "$d/index.html" "Nothing has closed in the"
# The open incident is shown in full above the components. Rendering it again
# verbatim in the history, on the same screen, is not a second piece of
# information, and it was doing exactly that.
expect_exit "the open incident appears once, not twice" 1 "$(grep -oc "Sign-in is returning 503" "$d/index.html")"
expect_exit "the closed one appears once" 1 "$(grep -oc "Reports were delayed by up to nine minutes" "$d/index.html")"
# A maintenance window next month has not started and is not open.
expect "future maintenance is scheduled, not started" "$d/index.html" "scheduled for 20 Sep 2026"
refute "future maintenance is not described as open" "$d/index.html" "still open</b></p><ol class=\"updates\"><li><p class=\"update-head\"><b>scheduled"

# ---------------------------------------------------------------------------
case_start "an incident is open and none has ever closed"
d="$WORK/onlyopen"; mkdir -p "$d"; s="$(scripts onlyopen)"
cat > "$s/incidents/2026-08-31-open.json" <<'JSON'
{ "id": "2026-08-31-open", "title": "Sign-in is returning 503",
  "type": "incident", "severity": "major", "components": ["control-plane-api"],
  "started_at": "2026-08-31T04:10:00Z",
  "updates": [ { "at": "2026-08-31T04:18:00Z", "status": "investigating", "body": "Looking at it." } ] }
JSON
reading control-plane-api 600 true > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
expect "the history reports on the closed record" "$d/index.html" "Nothing has closed in the"
refute "and does not contradict the open incident above it" "$d/index.html" "No incident has been recorded"

# ---------------------------------------------------------------------------
case_start "no incidents at all: the state this page is in almost always"
d="$WORK/noinc"; mkdir -p "$d"; s="$(scripts noinc)"
reading control-plane-api 600 true > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
expect "says so, and says what it is a statement about" "$d/index.html" "No incident has been recorded"
expect "does not present the absence as a measurement" "$d/index.html" "That is a statement about the record"
refute "no unreadable warning when nothing is unreadable" "$d/index.html" "could not be read"

# ---------------------------------------------------------------------------
# This case exists because the suite did not have it and a mutation proved it.
# Replacing the availability formula with a bare "100%" passed all fifty other
# assertions: every other case is either all-passing or so short that any
# rounding lands on the same number. A page that rounds its way to a round
# number is the exact defect this project's own field guide names, so it gets
# an assertion of its own with a denominator big enough that only the sign of
# the rounding decides the answer.
case_start "799 of 800 checks passed: 99.875% must print as 99.8 and never as 99.9 or 100"
d="$WORK/round"; mkdir -p "$d"; s="$(scripts round)"
jq -nr --arg now "$(date -u +%s)" '
  [ range(0; 800)
    | { checked_at: (($now | tonumber) - 14400 + (. * 7) | todate),
        id: "control-plane-api", name: "control-plane-api", group: "g",
        url: "https://example.invalid", check: "http", http_status: 200,
        ok: (. != 400), ready: null, duration_ms: 10, commit: "", detail: "" } ]
  | .[] | tojson' > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
expect "states the figure it actually measured" "$d/index.html" "99.8%"
refute "does not round the failed check half away" "$d/index.html" "99.9%"
refute "never rounds a failed check away into a clean 100%" "$d/index.html" "100%"
expect "shows the denominator beside it" "$d/index.html" "799 of 800 checks"

# ---------------------------------------------------------------------------
# Raw readings are pruned to a few weeks and the rollups are not, so a window
# check written against the raw history would refuse the ninety day figure
# forever on a page whose record holds a year. It did exactly that.
case_start "a rollup reaching back a year shows the ninety day figure, with no raw that old"
d="$WORK/window"; mkdir -p "$d"; s="$(scripts window)"
jq -n --arg now "$(date -u +%s)" '
  ($now | tonumber) as $n
  | { counted_through: {},
      days: [ range(1; 200) | { id: "control-plane-api",
                                day: (($n - (. * 86400)) | strftime("%Y-%m-%d")),
                                checks: 288, ok: 288 } ] }' > "$d/daily.json"
reading control-plane-api 600 true > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
expect "the ninety day window is offered" "$d/index.html" ">90d<"
expect "and the thirty day one" "$d/index.html" ">30d<"
expect "over the checks the rollups actually hold" "$d/index.html" "of 25633 checks"

# ---------------------------------------------------------------------------
case_start "the raw retention cap holds, and the rollup keeps the count it truncated"
d="$WORK/cap"; mkdir -p "$d"; s="$(scripts cap)"
jq -nr --arg now "$(date -u +%s)" '
  [ range(0; 1400)
    | { checked_at: (($now | tonumber) - 12600 + (. * 9) | todate),
        id: "control-plane-api", name: "control-plane-api", group: "g",
        url: "https://example.invalid", check: "http", http_status: 200,
        ok: true, ready: null, duration_ms: 10, commit: "", detail: "" } ]
  | .[] | tojson' > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
expect_exit "raw readings are capped" 1000 "$(jq 'length' "$d/history.json")"
expect_exit "the rollup still counts every check that arrived" 1400 "$(jq '[.days[] | select(.id == "control-plane-api") | .checks] | add' "$d/daily.json")"

# Running the same probe twice is not hypothetical: the workflow can be
# dispatched by hand over a run that already happened, and a rollup that
# counted forward without a watermark would double every one of those checks.
run "$d" "$d/readings.jsonl" "$s"
expect_exit "a re-run of the same readings counts nothing twice" 1400 "$(jq '[.days[] | select(.id == "control-plane-api") | .checks] | add' "$d/daily.json")"

# ---------------------------------------------------------------------------
case_start "a rollup for a day whose raw readings have aged out is not overwritten"
d="$WORK/roll"; mkdir -p "$d"; s="$(scripts roll)"
jq -n '{counted_through: {"control-plane-api": "2026-01-05T23:59:00Z"},
        days: [{id: "control-plane-api", day: "2026-01-05", checks: 288, ok: 280}]}' > "$d/daily.json"
reading control-plane-api 600 true > "$d/readings.jsonl"
run "$d" "$d/readings.jsonl" "$s"
expect_exit "the run itself succeeded, so this assertion is about the merge and not about a skipped run" 0 "$?"
kept="$(jq -r '[.days[] | select(.day == "2026-01-05")] | .[0].checks // 0' "$d/daily.json")"
expect_exit "the stored rollup survives a run that has no raw readings for that day" 288 "$kept"

# ---------------------------------------------------------------------------
echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
