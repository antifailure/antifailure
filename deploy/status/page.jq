# The status page itself: one jq program from the folded history to the HTML.
#
# It is jq rather than shell string concatenation because every value on this
# page comes from a file somebody edits by hand or from a body a remote server
# returned, and `@html` on every one of them is a property of the program here
# rather than a thing to remember at forty call sites. The earlier version
# built rows with `rows="${rows}<section>..."` and interpolated a component
# name straight into markup.
#
# Inputs, all as --argjson except where noted:
#   $now         epoch seconds, the moment the page was generated
#   $targets     deploy/status/targets.json
#   $history     the folded raw readings
#   $daily       per component per UTC day rollups: {id, day, checks, ok}
#   $incidents   {incidents: [...], unreadable: ["name.json"]}
#   $dropped     how many readings were unreadable and skipped
#   $stripDays   how many days the history strip covers
#   $generated   an ISO timestamp, as a string

# ---------------------------------------------------------------- formatting

def esc: if . == null then "" else (tostring | @html) end;

def plural($n; $one; $many): "\($n) " + (if $n == 1 then $one else $many end);

# Never round a percentage up. 99.99 becomes 99.9, and only an unbroken run of
# passing checks is allowed to print 100%. A status page that rounds its way to
# a round number is the exact tell this project's own field guide names.
def availability($ok; $total):
  if $total == 0 then null
  elif $ok == $total then "100%"
  else (($ok * 1000 / $total) | floor) as $tenths
    | if ($tenths % 10) == 0 then "\($tenths / 10 | floor)%" else "\($tenths / 10)%" end
  end;

# %-d is a GNU extension and this has to render the same on a BSD userland, so
# the zero pad comes off with a substitution rather than a format flag.
def stampDate: strftime("%d %b %Y") | sub("^0"; "");
def stampTime: strftime("%H:%M");
def dayKey: strftime("%Y-%m-%d");
def dayLabel: (. + "T00:00:00Z") | fromdateiso8601 | stampDate;

def humanSecs:
  if . == null then "unknown"
  elif . < 90 then plural((. | round); "second"; "seconds")
  elif . < 5400 then plural((. / 60 | round); "minute"; "minutes")
  elif . < 172800 then plural((. / 3600 | round); "hour"; "hours")
  else plural((. / 86400 | round); "day"; "days")
  end;

def iso: try fromdateiso8601 catch null;

# ------------------------------------------------------------- normalisation

# A reading written before components had ids carries `name` where an id
# belongs and `ready` where `ok` belongs. Both are read here rather than
# rewritten in the data, because rewriting history to fit a new shape destroys
# the one thing a status page's history is for.
#
# `.ok // .ready` would be a bug and not a shortcut: jq's alternative operator
# treats `false` as absent, so a reading that recorded a real failure would
# fall through to the next branch and could be read as passing. has() is the
# only correct test for a boolean field.
def readingId: if has("id") and ((.id | type) == "string") and (.id | length) > 0
               then .id
               elif has("name") and ((.name | type) == "string") then .name
               else null end;
def readingOk: if has("ok") and ((.ok | type) == "boolean") then .ok
               elif has("ready") and ((.ready | type) == "boolean") then .ready
               else false end;

# ------------------------------------------------------------------ the data

# Every one of the inputs arrives through --slurpfile, which wraps the file's
# contents in an array, so each is unwrapped exactly once here. They are files
# and not --argjson because the record otherwise travels on the command line,
# and a full history is past ARG_MAX. That failure does not appear until the
# record is big enough, which is months after anyone would connect the two.
($targets[0]) as $targets
| ($history[0]) as $history
| ($daily[0]) as $daily
| ($incidents[0]) as $incidents
| ($now | floor) as $nowS
| ($stripDays - 1) as $back
| [range(0; $stripDays) | ($nowS - (($back - .) * 86400)) | dayKey] as $days
| ($days | last) as $today

| ($history | map(select(readingId != null))) as $rows
| ($rows | group_by(readingId)
        | map({key: .[0] | readingId, value: (sort_by(.checked_at))})
        | from_entries) as $byId
| ($daily | group_by(.id)
        | map({key: .[0].id, value: (INDEX(.day))})
        | from_entries) as $dayIndex

# The observed interval between checks, from the data rather than from the
# cron expression. The workflow asks for every five minutes; GitHub drops
# scheduled runs under load and delivers far fewer, and a page that printed
# the schedule it asked for rather than the cadence it got would be stating a
# freshness it does not have.
| ($rows | map(.checked_at | iso) | map(select(. != null)) | unique) as $stamps
| (if ($stamps | length) < 2 then null
   else ([range(1; $stamps | length) | $stamps[.] - $stamps[. - 1]] | sort) as $gaps
     | $gaps[(($gaps | length) / 2 | floor)]
   end) as $interval
| (if $interval == null then null else ([1800, $interval * 3] | max) end) as $staleAfter
| ($stamps | if length == 0 then null else .[0] end) as $firstSeen
| ($stamps | if length == 0 then null else .[-1] end) as $lastSeen

# Window eligibility is measured against the ROLLUPS, not against the raw
# readings, and the difference is a bug rather than a nicety. Raw is pruned to
# a few weeks, so $firstSeen never reaches back further than that and a check
# written against it would refuse to print the ninety day figure forever, on a
# page whose rollups hold a full year. The rollups are the record of how far
# back this page can see.
| ($daily | map(.day) | if length == 0 then null
    else (min + "T00:00:00Z" | iso) end) as $recordStart

# ------------------------------------------------------------- per component

| [ $targets[]
    | . as $t
    | ($byId[$t.id] // []) as $reads
    | ($reads | if length == 0 then null else .[-1] end) as $latest
    | ($latest | if . == null then null else (.checked_at | iso) end) as $latestAt
    | ($reads | map(select((.checked_at | iso) != null and (.checked_at | iso) > ($nowS - 86400)))) as $day1
    | ($day1 | length) as $day1n
    | ($day1 | map(select(readingOk)) | length) as $day1ok

    | (if $latest == null then "unchecked"
       elif ($latest | readingOk | not) then "down"
       elif $staleAfter != null and $latestAt != null and ($nowS - $latestAt) > $staleAfter then "stale"
       elif $day1n > $day1ok then "recovered"
       else "ok" end) as $state

    | [ $days[]
        | . as $d
        | (($dayIndex[$t.id] // {})[$d]) as $roll
        | if $roll == null or ($roll.checks // 0) == 0
          then {day: $d, known: false, checks: 0, ok: 0, share: 0}
          else {day: $d, known: true, checks: $roll.checks, ok: $roll.ok,
                share: (($roll.checks - $roll.ok) / $roll.checks)}
          end ] as $cells

    | ($cells | map(select(.known))) as $known
    | ($known | map(.checks) | add // 0) as $allChecks
    | ($known | map(.ok) | add // 0) as $allOk

    | { id: $t.id, name: $t.name, group: $t.group, description: $t.description,
        url: $t.url, state: $state, latest: $latest, latestAt: $latestAt,
        cells: $cells, day1n: $day1n, day1ok: $day1ok,
        allChecks: $allChecks, allOk: $allOk,
        windows: (
          ([ (if $day1n > 0 and $recordStart != null and $recordStart <= ($nowS - 86400)
              then {label: "24h", checks: $day1n, ok: $day1ok, text: availability($day1ok; $day1n)}
              else empty end),
             ([ {label: "7d", secs: 604800}, {label: "30d", secs: 2592000}, {label: "90d", secs: 7776000} ][]
              | . as $w
              | (($nowS - $w.secs) | dayKey) as $from
              | ($known | map(select(.day >= $from))) as $in
              | ($in | map(.checks) | add // 0) as $c
              | ($in | map(.ok) | add // 0) as $o
              # A window is only shown once the record actually reaches back
              # across it. Printing "90 days: 100%" over four days of data is
              # the specific lie this page exists not to tell.
              | select($c > 0 and $recordStart != null and $recordStart <= ($nowS - $w.secs))
              | {label: $w.label, checks: $c, ok: $o, text: availability($o; $c)}) ]) as $w
          | if ($w | length) > 0 then $w
            elif $allChecks > 0
            then [ {label: "since \(($known | first).day | dayLabel)", checks: $allChecks, ok: $allOk,
                    text: availability($allOk; $allChecks)} ]
            else [] end)
      } ] as $components

# ------------------------------------------------------------------- verdict

| ($components | map(select(.state == "down"))) as $down
| ($components | map(select(.state == "stale"))) as $stale
| ($components | map(select(.state == "recovered"))) as $recovered
| ($components | map(select(.state == "unchecked"))) as $unchecked

| (if ($components | length) == 0 then "unknown"
   elif ($unchecked | length) == ($components | length) then "unknown"
   elif ($down | length) > 0 then "down"
   elif ($stale | length) > 0 or ($unchecked | length) > 0 or ($recovered | length) > 0 then "warn"
   else "ok" end) as $verdict

| (if $verdict == "unknown" then "Nothing has been checked yet."
   elif ($down | length) == 1 then "\($down[0].name) is not answering."
   elif ($down | length) > 1 then "\($down | length) components are not answering."
   elif ($stale | length) > 0 then "Some components have not been checked recently."
   # An observed failure outranks a component nobody has probed. A reader who
   # arrives after a blip needs to be told about the blip; the subhead is where
   # the unprobed components get named.
   elif ($recovered | length) > 0 then "Everything is answering now."
   elif ($unchecked | length) > 0 then "Everything checked so far is answering."
   else "Everything is answering." end) as $headline

| ([ (if ($down | length) > 0
      then "\(plural(($components | length) - ($down | length); "component"; "components")) of \($components | length) passed the most recent check."
      else empty end),
     (if ($stale | length) > 0
      then "\(plural(($stale | length); "component has"; "components have")) not been checked inside the expected interval."
      else empty end),
     (if ($unchecked | length) > 0 and $verdict != "unknown"
      then "\(plural(($unchecked | length); "component has"; "components have")) never been checked."
      else empty end),
     (if ($recovered | length) > 0
      then "\(plural(($recovered | length); "component"; "components")) failed a check in the last 24 hours and is answering again."
      else empty end),
     (if $verdict == "ok"
      then "All \($components | length) components passed their most recent check."
      else empty end),
     (if $verdict == "unknown"
      then "The probe has not recorded a reading for any component yet."
      else empty end) ] | join(" ")) as $subhead

# ----------------------------------------------------------------- incidents

| ($incidents.incidents // []) as $allIncidents
| ($allIncidents | map(select(.type == "incident"))) as $incidentList
| ($allIncidents | map(select(.type == "maintenance"))) as $maintList
| ($incidentList | map(select((has("ended_at") | not) or (.ended_at == null)))) as $openIncidents
| ($maintList | map(select((has("ended_at") | not) or (.ended_at == null)))) as $openMaint
| ($incidentList
    | map(select((.started_at | iso) > ($nowS - ($stripDays * 86400))))
    # Open incidents are rendered in full above the components. Repeating them
    # here verbatim, on the same screen, is not a second piece of information.
    | map(select((.ended_at // null) != null))) as $recentIncidents

# ------------------------------------------------------------------ renderer

| def glyph($state):
    { ok: "g-ok", recovered: "g-warn", down: "g-down",
      stale: "g-unknown", unchecked: "g-unknown", unknown: "g-unknown",
      warn: "g-warn" }[$state] as $sym
    | "<svg class=\"glyph\" viewBox=\"0 0 14 14\" aria-hidden=\"true\" focusable=\"false\"><use href=\"#\($sym)\"/></svg>";

  def stateWord($state):
    { ok: "Operational", recovered: "Recovered", down: "Not answering",
      stale: "Not checked recently", unchecked: "Not yet checked" }[$state] // "Unknown";

  def cell($c):
    ($c.day | dayLabel) as $label
    | if $c.known | not
      then "<span class=\"cell cell--unknown\" title=\"\($label): no check recorded\"></span>"
      elif $c.ok == $c.checks
      then "<span class=\"cell cell--ok\" title=\"\($label): \(plural($c.checks; "check"; "checks")), all passed\"></span>"
      else ((($c.share * 100) | floor) | if . < 4 then 4 else . end) as $h
        | "<span class=\"cell cell--bad\" title=\"\($label): \($c.checks - $c.ok) of \(plural($c.checks; "check"; "checks")) failed\">"
          + "<i style=\"height:\($h)%\"></i></span>"
      end;

  def row($c):
    ($c.cells | map(select(.known))) as $known
    | ($c.cells | map(select(.known and .ok < .checks)) | length) as $badDays
    | "<article class=\"row s-\($c.state)\">"
    + "<div class=\"row-head\">"
    + glyph($c.state)
    + "<h4 class=\"row-name\">\($c.name | esc)</h4>"
    + "<span class=\"row-state\">\(stateWord($c.state) | esc)</span>"
    + "</div>"
    + "<p class=\"row-desc\">\($c.description | esc)</p>"
    + "<div class=\"strip\" role=\"img\" aria-label=\"\($stripDays) days to \($today | dayLabel): \(($known | length)) with checks recorded, \($badDays) with a failed check, \($stripDays - ($known | length)) with no check recorded.\">"
    + ($c.cells | map(cell(.)) | join(""))
    + "</div>"
    + "<div class=\"axis\"><span class=\"axis-start\">\($days[0] | dayLabel | esc)</span>"
    + "<span class=\"axis-start-sm\">\($days[$stripDays - 30] | dayLabel | esc)</span>"
    + "<span class=\"axis-end\">today</span></div>"
    + (if ($c.windows | length) > 0
       then "<p class=\"figures\">" + ($c.windows | map("<span><b>\(.label)</b> \(.text) <em>\(.ok) of \(plural(.checks; "check"; "checks"))</em></span>") | join("")) + "</p>"
       else "" end)
    + "<p class=\"row-meta\">"
    + (if $c.latest == null
       then "No check has been recorded for this component yet."
       else ((if $c.state == "down"
              then "<b class=\"said\">Failing since the check at \($c.latestAt | stampTime) UTC" + (if (($c.latest.detail // "") | length) > 0 then ": \($c.latest.detail | esc)" else "" end) + ".</b> "
              elif $c.state == "stale"
              then "<b class=\"said\">Last checked \(($nowS - $c.latestAt) | humanSecs) ago, which is longer than the interval this probe has been keeping.</b> "
              elif $c.state == "recovered"
              then "<b class=\"said\">\($c.day1n - $c.day1ok) of \(plural($c.day1n; "check"; "checks")) failed in the last 24 hours.</b> "
              else "" end)
             + "Checked \($c.latestAt | stampDate | esc) at \($c.latestAt | stampTime) UTC"
             + (if ($c.latest.duration_ms // 0) > 0 then ", answered in \($c.latest.duration_ms)&nbsp;ms" else "" end)
             + (if (($c.latest.commit // "") | length) >= 8 then ", running <code>\($c.latest.commit[0:8] | esc)</code>" else "" end)
             + ". <a href=\"\($c.url | esc)\">\($c.url | esc)</a>")
       end)
    + "</p></article>";

  def updateBlock($u):
    "<li><p class=\"update-head\"><b>\($u.status | ascii_downcase | esc)</b>"
    + "<time datetime=\"\($u.at | esc)\">\(($u.at | iso | stampDate) | esc), \(($u.at | iso | stampTime) | esc) UTC</time></p>"
    + "<p class=\"update-body\">\($u.body | esc)</p></li>";

  def incidentBlock($i; $open):
    ($i.components | map(. as $id | ($components | map(select(.id == $id)) | .[0].name) // $id)) as $names
    | (($i.started_at | iso) > $nowS) as $future
    | "<article class=\"incident incident--\(if $open then "open" else "closed" end) sev--\($i.severity // "none" | esc)\">"
    + "<h4 class=\"incident-title\">\($i.title | esc)</h4>"
    + "<p class=\"incident-meta\">"
    + (if $i.type == "maintenance" then "Maintenance" else "\($i.severity // "incident" | esc) incident" end)
    + " &middot; \($names | map(esc) | join(", "))"
    # A window that has not opened yet has not started, and is not open. The
    # first version said "started 8 Sep, still open" about a date next week.
    + " &middot; \(if $future then "scheduled for" else "started" end) \(($i.started_at | iso | stampDate) | esc), \(($i.started_at | iso | stampTime) | esc) UTC"
    + (if ($i.ended_at // null) != null
       then " &middot; ended \(($i.ended_at | iso | stampDate) | esc), \(($i.ended_at | iso | stampTime) | esc) UTC, after \((($i.ended_at | iso) - ($i.started_at | iso)) | humanSecs | esc)"
       elif $future then " &middot; in \(($i.started_at | iso) - $nowS | humanSecs | esc)"
       elif $i.type == "maintenance" then " &middot; <b>in progress</b>"
       else " &middot; <b>still open</b>" end)
    + "</p><ol class=\"updates\">"
    + ($i.updates | sort_by(.at) | reverse | map(updateBlock(.)) | join(""))
    + "</ol></article>";

# ------------------------------------------------------------------ the page

  "<!doctype html>
<html lang=\"en\">
<head>
<meta charset=\"utf-8\">
<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">
<title>Antifailure status</title>
<meta name=\"description\" content=\"Whether Antifailure is answering, checked every few minutes from outside the infrastructure it reports on.\">
<meta name=\"robots\" content=\"index, follow\">
<meta name=\"color-scheme\" content=\"light\">
<style>
/*
 * Self contained on purpose. No font file, no stylesheet, no script, no image
 * and no request of any kind leaves this document, because the one moment it
 * has to render correctly is the moment something else is broken. A web font
 * from a CDN is a second origin that can be down, and a status page that
 * renders unstyled during an outage has failed at the only job it has.
 *
 * That rules out the site's Inter and Geist, so the type is the reader's own
 * system stack with the site's tracking and its type scale applied over it.
 * Everything else is copied from console/app/globals.css by value: the same
 * paper, the same ink, the same measured pass, fail and warn, the same three
 * greys, the same radius vocabulary. A customer arriving here from
 * antifailure.dev should not feel they left.
 *
 * Light only, and painted explicitly. The marketing site and the console are
 * both light only, and a dark theme mechanically inverted from a light one is
 * worse than not having one.
 */
:root {
  color-scheme: light;
  --paper: #f7f7f5;
  --card: #ffffff;
  --ink: #101010;
  /* Measured against both grounds rather than picked by eye. On #f7f7f5:
     ink 17.7:1, pass 5.0:1, fail 6.1:1, warn 5.5:1, muted 6.8:1, dim 4.6:1.
     Every one of them clears 4.5:1 as body text. */
  --pass: #1e7a3a;
  --fail: #b3261e;
  --warn: #8a5a00;
  --muted: #575752;
  --dim: #70706b;
  --rule: rgba(16, 16, 16, 0.1);
  --rule-strong: rgba(16, 16, 16, 0.22);
  /* State grounds, each measured with the text that sits on it:
     fail on #fbeceb is 5.7:1, warn on #faf0dc is 5.2:1. */
  --fail-ground: #fbeceb;
  --warn-ground: #faf0dc;
  --sans: ui-sans-serif, -apple-system, BlinkMacSystemFont, \"Segoe UI\", Roboto, Helvetica, Arial, sans-serif;
  --mono: ui-monospace, SFMono-Regular, \"SF Mono\", Menlo, Consolas, \"Liberation Mono\", monospace;
}

* { box-sizing: border-box; }

body {
  margin: 0;
  padding: 0 20px 72px;
  background: var(--paper);
  color: var(--ink);
  font-family: var(--sans);
  font-size: 16px;
  line-height: 1.55;
  letter-spacing: -0.011em;
  -webkit-font-smoothing: antialiased;
  min-width: 320px;
}

main { max-width: 60rem; margin: 0 auto; }

a { color: var(--ink); text-decoration: underline; text-decoration-color: var(--rule-strong); text-underline-offset: 3px; }
a:hover { text-decoration-color: var(--ink); }

/* One focus ring, defined once, for every interactive element on the page.
   The alternative is what usually happens: a ring on the things somebody
   styled and a browser default on the ones they forgot. */
:where(a, button, [tabindex]):focus-visible {
  outline: 2px solid var(--ink);
  outline-offset: 2px;
  border-radius: 3px;
}

code { font-family: var(--mono); font-size: 0.86em; letter-spacing: 0; }

/* ---------------------------------------------------------------- masthead */

.masthead {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 28px 0 22px;
  border-bottom: 1px solid var(--rule);
}
.wordmark { font-size: 15px; font-weight: 600; letter-spacing: -0.03em; margin: 0; }
.masthead .page-name { font-size: 15px; font-weight: 400; color: var(--muted); margin: 0; letter-spacing: -0.02em; }
.masthead .away { margin-left: auto; font-size: 14px; color: var(--muted); }

/* ----------------------------------------------------------------- verdict */

/* The verdict is quiet when everything is fine and loud when it is not. A
   permanently coloured banner is decoration, and decoration is exactly what
   stops carrying meaning when it matters. */
.verdict { padding: 34px 0 30px; border-bottom: 1px solid var(--rule); }
.verdict.is-warn, .verdict.is-down {
  margin: 22px 0 0;
  padding: 24px 22px;
  border-bottom: 0;
  border-radius: 6px;
}
.verdict.is-warn { background: var(--warn-ground); }
.verdict.is-down { background: var(--fail-ground); }

.verdict-line {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  margin: 0;
  font-size: clamp(26px, 6.4vw, 40px);
  line-height: 1.12;
  font-weight: 500;
  letter-spacing: -0.035em;
  text-wrap: balance;
}
.verdict-line .glyph { width: 18px; height: 18px; flex: none; margin-top: 0.34em; }
.verdict.is-ok .verdict-line .glyph { color: var(--pass); }
.verdict.is-warn .verdict-line { color: var(--warn); }
.verdict.is-warn .verdict-line .glyph { color: var(--warn); }
.verdict.is-down .verdict-line { color: var(--fail); }
.verdict.is-down .verdict-line .glyph { color: var(--fail); }
.verdict.is-unknown .verdict-line .glyph { color: var(--dim); }

.verdict-sub { margin: 14px 0 0; font-size: 17px; color: var(--muted); max-width: 44ch; letter-spacing: -0.014em; }
.verdict.is-warn .verdict-sub, .verdict.is-down .verdict-sub { color: var(--ink); }

/*
 * The line this page is built around.
 *
 * Every status page states a verdict. Almost none of them state how recently
 * they earned the right to. This one prints the moment of the last check, how
 * long ago that was, and the interval the checks have actually been arriving
 * at, measured from the readings rather than copied from the cron expression
 * that asks for them. A reader can tell at a glance whether the green above is
 * four minutes old or four hours old, which is the difference between a
 * status page and a decoration.
 */
.freshness {
  margin: 18px 0 0;
  font-family: var(--mono);
  font-size: 13px;
  line-height: 1.7;
  letter-spacing: 0;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
.freshness b { color: var(--ink); font-weight: 600; }
.freshness .sep { color: var(--muted); padding: 0 8px; }

/* -------------------------------------------------------------- components */

section.block { padding: 34px 0 0; }
h2.block-title {
  margin: 0 0 4px;
  font-size: 12px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.09em;
  color: var(--muted);
}
.block-note { margin: 0 0 20px; font-size: 14px; color: var(--muted); max-width: 62ch; }

.group { margin-top: 26px; }
.group-name {
  margin: 0 0 2px;
  font-size: 15px;
  font-weight: 600;
  letter-spacing: -0.02em;
}
.group-rule { height: 1px; background: var(--ink); opacity: 0.85; margin-bottom: 4px; }

.row { padding: 18px 0; border-bottom: 1px solid var(--rule); }
.row:last-child { border-bottom: 0; }
.row-head { display: flex; align-items: center; gap: 9px; flex-wrap: wrap; }
.row-name { margin: 0; font-size: 17px; font-weight: 500; letter-spacing: -0.022em; }
.row-state {
  margin-left: auto;
  font-size: 13px;
  font-weight: 600;
  letter-spacing: -0.01em;
}
.glyph { width: 13px; height: 13px; flex: none; }
.row-head .glyph { margin-top: 1px; }

/* State reaches the reader three ways at once, and this is not belt and
   braces. Measured with the palette validator, pass and fail are 4.0 apart in
   OKLab under deuteranopia and 1.21:1 apart in luminance, which is to say a
   red cell and a green cell are the same cell to a red-green colour blind
   reader and to anyone printing this in grey. So every state also carries a
   shape and a word. */
.row .glyph { color: var(--dim); }
.row.s-ok .glyph, .row.s-ok .row-state { color: var(--pass); }
.row.s-recovered .glyph, .row.s-recovered .row-state { color: var(--warn); }
.row.s-down .glyph, .row.s-down .row-state { color: var(--fail); }
.row.s-stale .glyph, .row.s-stale .row-state,
.row.s-unchecked .glyph, .row.s-unchecked .row-state { color: var(--dim); }

.row-desc { margin: 3px 0 0; font-size: 14px; color: var(--muted); }

/* ------------------------------------------------------------------- strip */

.strip {
  display: flex;
  gap: 2px;
  height: 32px;
  margin: 14px 0 0;
  align-items: stretch;
}
.cell {
  position: relative;
  flex: 1 1 0;
  min-width: 0;
  border-radius: 1px;
  align-self: stretch;
}
.cell--ok { background: var(--pass); }
.cell--bad { background: var(--pass); }
/*
 * The failed share of a day, drawn from the top, with the true fraction as its
 * height and a floor of 4% so a single failed check inside a busy day cannot
 * disappear. The 2px ink cap is the part that is not colour: near black
 * against both the green and the red, at 17.7:1 on paper, so a day with a
 * failure is distinguishable from a clean day on a greyscale printout and to
 * a reader who cannot separate the two hues at all.
 */
.cell--bad > i {
  position: absolute;
  inset: 0 0 auto 0;
  display: block;
  background: var(--fail);
  border-radius: 1px 1px 0 0;
  box-shadow: inset 0 2px 0 var(--ink);
}
/* A day with no reading is neither good nor bad and must not look like
   either. It is grey, which is achromatic and so cannot be confused with
   pass or fail under any colour vision, and it is a quarter height, which
   reads as an absence rather than as a bar even at three pixels wide. */
.cell--unknown {
  align-self: flex-end;
  height: 25%;
  background: var(--rule-strong);
}

.axis {
  display: flex;
  justify-content: space-between;
  margin: 6px 0 0;
  font-family: var(--mono);
  font-size: 11px;
  letter-spacing: 0;
  color: var(--dim);
}
.axis-start-sm { display: none; }

.figures {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 18px;
  margin: 12px 0 0;
  font-size: 13px;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
.figures span { white-space: nowrap; }
.figures b { font-family: var(--mono); font-size: 11px; letter-spacing: 0.04em; text-transform: uppercase; color: var(--dim); font-weight: 600; }
.figures em { font-style: normal; color: var(--dim); }

.row-meta { margin: 10px 0 0; font-size: 13px; color: var(--muted); }
/* Only the sentence that says what went wrong is coloured. Painting the whole
   line takes the link and the timestamp with it and reads as shouting. */
.row-meta .said { font-weight: 600; }
.row.s-down .row-meta .said { color: var(--fail); }
.row.s-recovered .row-meta .said { color: var(--warn); }
.row.s-stale .row-meta .said { color: var(--ink); }

/* ------------------------------------------------------------------ legend */

.legend {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 22px;
  margin: 22px 0 0;
  padding: 14px 16px;
  background: var(--card);
  border: 1px solid var(--rule);
  border-radius: 6px;
  font-size: 13px;
  color: var(--muted);
}
.legend span { display: flex; align-items: center; gap: 8px; }
.key { width: 12px; height: 20px; flex: none; border-radius: 1px; }
.key--ok { background: var(--pass); }
.key--bad { background: var(--pass); box-shadow: inset 0 2px 0 var(--ink), inset 0 6px 0 var(--fail); }
.key--unknown { height: 5px; align-self: center; background: var(--rule-strong); }

/* --------------------------------------------------------------- incidents */

.incident {
  padding: 18px 0;
  border-bottom: 1px solid var(--rule);
}
.incident:last-child { border-bottom: 0; }
.incident--open {
  margin: 0 0 14px;
  padding: 20px 22px;
  background: var(--fail-ground);
  border: 0;
  border-radius: 6px;
}
.incident--open.sev--none { background: var(--warn-ground); }
.incident-title { margin: 0; font-size: 19px; font-weight: 500; letter-spacing: -0.026em; }
.incident-meta { margin: 5px 0 0; font-size: 13px; color: var(--muted); }
.incident--open .incident-meta { color: var(--ink); }
.incident-meta b { color: var(--fail); }

.updates { margin: 14px 0 0; padding: 0; list-style: none; }
.updates li { padding: 0 0 0 16px; border-left: 1px solid var(--rule-strong); }
.updates li + li { margin-top: 14px; }
.update-head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 10px;
  margin: 0;
  font-size: 12px;
}
.update-head b { text-transform: uppercase; letter-spacing: 0.07em; font-weight: 600; }
.update-head time { font-family: var(--mono); color: var(--muted); letter-spacing: 0; font-variant-numeric: tabular-nums; }
.update-body { margin: 3px 0 0; font-size: 15px; color: var(--muted); max-width: 68ch; }
.incident--open .update-body { color: var(--ink); }

.empty {
  margin: 0;
  padding: 20px 22px;
  background: var(--card);
  border: 1px solid var(--rule);
  border-radius: 6px;
  font-size: 15px;
  color: var(--muted);
  max-width: 68ch;
}
.empty b { color: var(--ink); font-weight: 500; }

.warnbox {
  margin: 0 0 16px;
  padding: 14px 18px;
  background: var(--warn-ground);
  border-radius: 6px;
  font-size: 14px;
  color: var(--ink);
}

/* ------------------------------------------------------------------ method */

.method { margin-top: 6px; font-size: 15px; color: var(--muted); max-width: 68ch; }
.method p { margin: 0 0 12px; }
.method p:last-child { margin-bottom: 0; }
.method b { color: var(--ink); font-weight: 500; }

footer {
  margin-top: 46px;
  padding-top: 20px;
  border-top: 1px solid var(--rule);
  font-size: 13px;
  color: var(--dim);
}

/* ---------------------------------------------------------------- narrower */

@media (max-width: 640px) {
  body { padding: 0 16px 56px; }
  .masthead .away { display: none; }
  /* Thirty days rather than ninety on a phone: ninety cells inside 288px of
     content is three pixels each with the gaps, which is a texture and not a
     chart. The oldest sixty are hidden rather than never drawn, so one page
     serves both widths with no second render and no script. */
  .strip .cell:nth-child(-n + 60) { display: none; }
  .axis-start { display: none; }
  .axis-start-sm { display: inline; }
  .strip { height: 32px; }
  .verdict { padding: 26px 0 24px; }
  .verdict.is-warn, .verdict.is-down { padding: 20px 16px; }
  .incident--open { padding: 18px 16px; }
  .legend { gap: 8px 16px; padding: 12px 14px; font-size: 14px; }
  .row-desc { font-size: 15px; }
  .row-meta, .figures { font-size: 14px; }
  .axis { font-size: 12px; }
  .block-note, footer { font-size: 14px; }
  .figures b { font-size: 12px; }
  .incident-meta { font-size: 14px; }
}

/*
 * Windows high contrast forces every background and colour to the user's own
 * palette, which would flatten the strip into one uniform block. The border
 * survives forced colours, so the states keep a shape when the fills are gone.
 */
@media (forced-colors: active) {
  .cell { border: 1px solid CanvasText; }
  .cell--bad > i { border-bottom: 2px solid CanvasText; }
  .cell--unknown { border-style: dotted; }
  .key { border: 1px solid CanvasText; }
}

/* Nothing on this page animates, so there is nothing to gate here beyond the
   browser's own smooth scrolling. There is deliberately no live indicator: a
   pulsing dot says nothing a timestamp does not say better, and it says it
   forever. */
@media print {
  body { background: #fff; }
  .verdict.is-warn, .verdict.is-down, .incident--open { background: #fff; border: 1px solid #000; }
}
</style>
</head>
<body>
<svg width=\"0\" height=\"0\" style=\"position:absolute\" aria-hidden=\"true\" focusable=\"false\">
  <symbol id=\"g-ok\" viewBox=\"0 0 14 14\"><rect x=\"2\" y=\"2\" width=\"10\" height=\"10\" rx=\"1\" fill=\"currentColor\"/></symbol>
  <symbol id=\"g-warn\" viewBox=\"0 0 14 14\"><path d=\"M7 1.5 13 12.5H1z\" fill=\"currentColor\"/></symbol>
  <symbol id=\"g-down\" viewBox=\"0 0 14 14\"><path d=\"M2.6 1.2 7 5.6l4.4-4.4 1.4 1.4L8.4 7l4.4 4.4-1.4 1.4L7 8.4l-4.4 4.4-1.4-1.4L5.6 7 1.2 2.6z\" fill=\"currentColor\"/></symbol>
  <symbol id=\"g-unknown\" viewBox=\"0 0 14 14\"><rect x=\"2.1\" y=\"2.1\" width=\"9.8\" height=\"9.8\" rx=\"1\" fill=\"none\" stroke=\"currentColor\" stroke-width=\"2\"/></symbol>
</svg>
<main>

<header class=\"masthead\">
  <p class=\"wordmark\">Antifailure</p>
  <p class=\"page-name\">Status</p>
  <p class=\"away\"><a href=\"https://antifailure.dev\">antifailure.dev</a></p>
</header>

<section class=\"verdict is-\($verdict)\">
  <p class=\"verdict-line\">\(glyph($verdict)) \($headline | esc)</p>
  <p class=\"verdict-sub\">\($subhead | esc)</p>
  <p class=\"freshness\">"
+ (if $lastSeen == null
   then "no check recorded yet"
   else "last check <b>\($lastSeen | stampDate | esc) \($lastSeen | stampTime | esc) UTC</b>"
     + "<span class=\"sep\">|</span>\(($nowS - $lastSeen) | humanSecs) ago"
     + (if $interval == null then ""
        else "<span class=\"sep\">|</span>checks arriving about every \($interval | humanSecs)"
        end)
   end)
+ "</p>
</section>
"

# Anything open goes above the components, because a reader who arrives during
# an incident came for the note and not for the strip.
+ (if ($openIncidents | length) > 0
   then "<section class=\"block\"><h2 class=\"block-title\">Open incident"
     + (if ($openIncidents | length) > 1 then "s" else "" end) + "</h2>"
     + ($openIncidents | map(incidentBlock(.; true)) | join(""))
     + "</section>"
   else "" end)

+ (if ($openMaint | length) > 0
   then "<section class=\"block\"><h2 class=\"block-title\">Scheduled maintenance</h2>"
     + ($openMaint | map(incidentBlock(.; true)) | join(""))
     + "</section>"
   else "" end)

+ "<section class=\"block\">
  <h2 class=\"block-title\">Components</h2>
  <p class=\"block-note\">A component is operational when its most recent check passed. Recovered means the most recent check passed and an earlier one inside the last 24 hours did not.</p>"
+ (($targets | reduce .[] as $t ([]; if (. | index($t.group)) then . else . + [$t.group] end)) as $order
   | $components | group_by(.group) | sort_by(.[0].group as $g | $order | index($g)) | map(
     "<div class=\"group\"><h3 class=\"group-name\">\(.[0].group | esc)</h3><div class=\"group-rule\"></div>"
     + (map(row(.)) | join(""))
     + "</div>") | join(""))
+ "
  <div class=\"legend\">
    <span><i class=\"key key--ok\"></i>every check that day passed</span>
    <span><i class=\"key key--bad\"></i>at least one failed, capped in black, sized by the share that failed</span>
    <span><i class=\"key key--unknown\"></i>no check recorded that day</span>
  </div>
</section>"

+ "<section class=\"block\">
  <h2 class=\"block-title\">Incident history</h2>
  <p class=\"block-note\">Written by hand, reviewed in a pull request, and published by the next probe. Nothing here is generated from the readings above.</p>"
+ (if (($incidents.unreadable // []) | length) > 0
   then "<p class=\"warnbox\">\(plural((($incidents.unreadable // []) | length); "incident file"; "incident files")) could not be read and \(if (($incidents.unreadable // []) | length) == 1 then "is" else "are" end) not shown here: \((($incidents.unreadable // []) | map(esc) | join(", ")))." + "</p>"
   else "" end)
+ (if ($recentIncidents | length) > 0
   then ($recentIncidents | map(incidentBlock(.; false)) | join(""))
   # With an incident open, "no incident has been recorded" is a sentence that
   # contradicts the block directly above it. The closed record is what this
   # section holds, so that is what it reports on.
   elif ($openIncidents | length) > 0 or ($openMaint | length) > 0
   then "<p class=\"empty\"><b>Nothing has closed in the \($stripDays) days to \($today | dayLabel | esc).</b> What is open is above.</p>"
   else "<p class=\"empty\"><b>No incident has been recorded"
     + (if $firstSeen == null then "." else " in the \($stripDays) days to \($today | dayLabel | esc)." end)
     + "</b> That is a statement about the record, which is written by hand, and not a measurement. What the readings measure is the strip above.</p>"
   end)
+ "</section>"

+ "<section class=\"block\">
  <h2 class=\"block-title\">How this page is measured</h2>
  <div class=\"method\">
    <p>Every component above is checked over the public internet from a GitHub Actions runner, <b>not from Antifailure's own infrastructure</b>. A probe that lived inside the control plane would go quiet during exactly the outage it exists to report, and a page served from the same Azure region as the product would go down alongside it. This page is generated on GitHub and served from GitHub, so an Azure event that takes the product down does not take its status page with it.</p>
    <p>The control plane checks answer <code>/readyz</code>, which runs a real database query. <code>/health</code> is a static literal that answers even when the database is unreachable, so a page built on it would report an outage as healthy. The static surfaces are checked for a marker in the body as well as a 200, because this site has twice been published broken behind a 200.</p>
    <p>The percentages are <b>the share of checks that passed</b>, not measured uptime. Between two checks this page knows nothing, and an outage shorter than the gap between checks can pass unrecorded. A window is only shown once the record actually reaches back across it, which is why a fresh page shows fewer of them than an old one.</p>
    <p>This page is not the pager. Alerting wakes an engineer far sooner than a five minute sample can move a bar here. They watch the same system from different distances.</p>
  </div>
</section>"

+ "<footer>Generated \($generated | esc). "
+ (if $dropped > 0 then "\(plural($dropped; "reading was"; "readings were")) unreadable and skipped. " else "" end)
+ "Every reading behind this page is in the <a href=\"https://github.com/antifailure/antifailure/tree/status-data\">status-data</a> branch, and the probe that wrote them is <a href=\"https://github.com/antifailure/antifailure/blob/main/deploy/status/probe.sh\">deploy/status/probe.sh</a>.</footer>
</main>
</body>
</html>
"
