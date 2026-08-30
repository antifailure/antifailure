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
#   render.sh <out-dir> <new-readings.jsonl>
#
# <out-dir>/history.json is read if it exists and created if it does not, so
# the first run starts empty rather than failing. Both files this writes,
# history.json and index.html, belong in the same directory: the page is
# nothing but a rendering of the history sitting next to it.

set -euo pipefail

OUT="${1:?usage: render.sh <out-dir> <new-readings.jsonl>}"
READINGS="${2:?usage: render.sh <out-dir> <new-readings.jsonl>}"
HISTORY="$OUT/history.json"

# How many checks to keep per target. At the workflow's five minute interval
# this is a little over seven days, which is enough to show a real incident's
# shape without the file growing without bound.
KEEP=2016

mkdir -p "$OUT"
[ -f "$HISTORY" ] || echo '[]' > "$HISTORY"

new="$(jq -s '.' "$READINGS")"

merged="$(jq -n --slurpfile old "$HISTORY" --argjson new "$new" --argjson keep "$KEEP" '
  ($old[0] + $new) as $all
  | ($all | group_by(.name) | map(.[0].name)) as $names
  | [ $names[] as $n
      | ($all | map(select(.name == $n)) | sort_by(.checked_at) | .[-$keep:])
      | .[]
    ]
')"
echo "$merged" > "$HISTORY"

# One current line per target, most recently checked last-in-wins, for the
# summary at the top of the page.
current="$(jq '
  group_by(.name) | map(sort_by(.checked_at) | .[-1])
' <<<"$merged")"

badge() {
  # $1: ready (true/false as text)
  if [ "$1" = "true" ]; then
    printf 'ok'
  else
    printf 'down'
  fi
}

rows=""
while IFS= read -r entry; do
  name="$(jq -r '.name' <<<"$entry")"
  url="$(jq -r '.url' <<<"$entry")"
  ready="$(jq -r '.ready' <<<"$entry")"
  checked_at="$(jq -r '.checked_at' <<<"$entry")"
  commit="$(jq -r '.commit // ""' <<<"$entry")"
  state="$(badge "$ready")"

  # The last KEEP checks for this target, oldest first, as a run of narrow bars.
  bars=""
  while IFS= read -r r; do
    if [ "$r" = "true" ]; then
      bars="${bars}<span class=\"bar ok\"></span>"
    else
      bars="${bars}<span class=\"bar down\"></span>"
    fi
  done < <(jq -r --arg n "$name" '[.[] | select(.name == $n)] | sort_by(.checked_at) | .[-288:][] | .ready' <<<"$merged")

  # Built as its own assignment rather than inline in the one below. A
  # command substitution whose last command is skipped by `&&` short circuit
  # exits nonzero, and set -e treats the assignment that embeds it as having
  # failed too, which took a working-looking loop down silently mid-render the
  # first time this was written, on exactly the entry with no commit yet.
  commit_note=""
  if [ -n "$commit" ]; then
    commit_note=" &middot; commit ${commit:0:12}"
  fi

  rows="${rows}
<section class=\"target\">
  <h2><span class=\"dot ${state}\"></span> ${name} <span class=\"state\">${state}</span></h2>
  <p class=\"meta\">${url} &middot; last checked ${checked_at} UTC${commit_note}</p>
  <div class=\"bars\">${bars}</div>
  <p class=\"caption\">last 24 hours, oldest to newest, one bar per check</p>
</section>"
done < <(jq -c '.[]' <<<"$current")

overall="ok"
if jq -e 'map(.ready) | any(. == false)' <<<"$current" >/dev/null; then
  overall="down"
fi

cat > "$OUT/index.html" <<HTML
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Antifailure control plane status</title>
<style>
  :root {
    color-scheme: light dark;
    --bg: #ffffff; --fg: #1a1d21; --muted: #5b6470; --border: #e2e5e9;
    --ok: #157a3d; --ok-bg: #e6f4ea; --down: #a4262c; --down-bg: #fdeceb;
  }
  @media (prefers-color-scheme: dark) {
    :root { --bg: #0f1115; --fg: #e7e9ec; --muted: #9aa4b2; --border: #262b33;
      --ok: #3fb96c; --ok-bg: #10261a; --down: #e56b6f; --down-bg: #2a1517; }
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; padding: 2.5rem 1.25rem 4rem; background: var(--bg); color: var(--fg);
    font: 16px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
  }
  main { max-width: 40rem; margin: 0 auto; }
  h1 { font-size: 1.375rem; margin: 0 0 0.25rem; }
  .lede { color: var(--muted); margin: 0 0 2rem; font-size: 0.9375rem; }
  .banner {
    display: flex; align-items: center; gap: 0.6rem; padding: 0.9rem 1.1rem;
    border-radius: 0.5rem; margin-bottom: 2rem; font-weight: 600;
  }
  .banner.ok { background: var(--ok-bg); color: var(--ok); }
  .banner.down { background: var(--down-bg); color: var(--down); }
  .target { padding: 1.25rem 0; border-top: 1px solid var(--border); }
  .target:first-of-type { border-top: none; }
  h2 { font-size: 1.0625rem; margin: 0 0 0.35rem; display: flex; align-items: center; gap: 0.5rem; }
  .state { margin-left: auto; font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.04em;
    color: var(--muted); font-weight: 600; }
  .dot { width: 0.6rem; height: 0.6rem; border-radius: 50%; flex: none; }
  .dot.ok { background: var(--ok); }
  .dot.down { background: var(--down); }
  .meta { color: var(--muted); font-size: 0.8125rem; margin: 0 0 0.75rem; }
  .bars { display: flex; gap: 1px; height: 1.75rem; align-items: stretch; }
  .bar { flex: 1 1 auto; min-width: 2px; border-radius: 1px; background: var(--ok); }
  .bar.down { background: var(--down); }
  .caption { color: var(--muted); font-size: 0.75rem; margin: 0.4rem 0 0; }
  footer { margin-top: 2.5rem; color: var(--muted); font-size: 0.8125rem; }
  footer a { color: inherit; }
</style>
</head>
<body>
<main>
  <h1>Antifailure control plane</h1>
  <p class="lede">Checked from GitHub Actions, not from the control plane itself, so an outage of the control plane cannot also take down the page that reports it.</p>
  <div class="banner ${overall}">$( [ "$overall" = "ok" ] && echo "All systems answering" || echo "One or more systems not answering" )</div>
  ${rows}
  <footer>Generated $(date -u +%Y-%m-%dT%H:%M:%SZ). Source and history: <a href="https://github.com/antifailure/antifailure/tree/status-data">github.com/antifailure/antifailure</a>, branch <code>status-data</code>.</footer>
</main>
</body>
</html>
HTML
