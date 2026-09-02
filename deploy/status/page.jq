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

# ------------------------------------------------------- the metric series
#
# The only metric this page measures is how long each check took, so that is
# the only metric it plots. There is no CPU, no queue depth and no throughput
# here, because nothing in this design observes any of those, and a chart of a
# number nobody measured is the worst thing a status page can contain.
#
# It is labelled as measured from a GitHub Actions runner, which is the honest
# framing: it includes that runner's own network path and tells a reader about
# reachability rather than about what their users feel.
#
# Readings are bucketed into a fixed number of slots across the window and
# averaged inside each. A slot with no readings does not get an interpolated
# value: it ends the current line segment and the next slot with data starts a
# new one. A line drawn across a gap is a line drawn through data that does
# not exist.
| def segments:
    reduce .[] as $b ([];
      if (length > 0) and (.[length - 1][-1].i == ($b.i - 1))
      then .[0:length - 1] + [ .[length - 1] + [$b] ]
      else . + [[$b]] end);

  def niceMax:
    if . <= 0 then 1
    else (. | log10 | floor) as $e
      | pow(10; $e) as $p
      | (. / $p) as $m
      | (if $m <= 1 then 1 elif $m <= 2 then 2 elif $m <= 5 then 5 else 10 end) * $p
    end;

  60 as $slots
| [ {k: "day", label: "Day", secs: 86400},
    {k: "week", label: "Week", secs: 604800},
    {k: "month", label: "Month", secs: 2592000} ] as $mwins

| ($components | map(
    . as $c
    | ($byId[$c.id] // []) as $reads
    | $c + { series: ($mwins | map(
        . as $w
        | ($nowS - $w.secs) as $from
        | ($w.secs / $slots) as $step
        | ($reads
            | map(select((.checked_at | iso) != null
                         and (.checked_at | iso) > $from
                         and ((.duration_ms // 0) > 0)))) as $pts
        | ($pts
            | map(. + {_i: (((((.checked_at | iso) - $from) / $step) | floor)
                            | if . > ($slots - 1) then $slots - 1 elif . < 0 then 0 else . end)})
            | group_by(._i)
            | map({ i: .[0]._i,
                    v: (((map(.duration_ms) | add) / length) | round),
                    n: length,
                    at: (map(.checked_at | iso) | max) })
            | sort_by(.i)) as $buckets
        | { k: $w.k, label: $w.label, secs: $w.secs, from: $from,
            count: ($pts | length),
            buckets: $buckets,
            segs: ($buckets | segments),
            top: (($buckets | map(.v) | max // 0) | niceMax) })) } )) as $components

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

| ($days | last) as $lastDay
| ([range(0; 14) | ($nowS - (. * 86400)) | dayKey]) as $pastDays
| ($allIncidents | map(select((.started_at | iso) > ($nowS - (14 * 86400))))) as $pastWindow

| def esc2: esc;

  def stamp:
    # "Sep 1, 2026 - 04:12 UTC". Built from parts rather than one format
    # string, because %-d is a GNU extension and this has to print the same on
    # a BSD userland.
    (strftime("%b") + " " + (strftime("%d") | sub("^0"; "")) + ", " + strftime("%Y"))
    + " - " + strftime("%H:%M") + " UTC";

  def dayStamp:
    (strftime("%b") + " " + (strftime("%d") | sub("^0"; "")) + ", " + strftime("%Y"));

  def dayStampKey: (. + "T00:00:00Z") | fromdateiso8601 | dayStamp;

  # Operational, Degraded Performance and an outage word, exactly as a reader
  # of any status page expects them, plus the two states most status pages do
  # not have a word for and quietly render as green.
  def statusWord($c):
    if $c.state == "unchecked" then "No Data"
    elif $c.state == "stale" then "No Recent Data"
    elif $c.state == "down" then (if $c.day1ok > 0 then "Partial Outage" else "Major Outage" end)
    elif $c.state == "recovered" then "Degraded Performance"
    else "Operational" end;

  def statusClass($c):
    if $c.state == "unchecked" or $c.state == "stale" then "s-none"
    elif $c.state == "down" then "s-down"
    elif $c.state == "recovered" then "s-warn"
    else "s-ok" end;

  # One bar per day. A bar's colour comes from that day's readings and from
  # nothing else, and a day with no readings is drawn in the neutral, never in
  # green. With a few days of history most of this strip is neutral for a
  # while, which is the honest picture and is what it should look like.
  #
  # The failed share sits on top, sized by the share that failed, and a day
  # containing any failure also carries a near black cap. That cap is not
  # decoration. Measured with the palette validator, the amber and the red are
  # 0.7 apart in OKLab under deuteranopia and the green and the red are 4.0
  # apart, which is to say all three bars are one bar to a red-green colour
  # blind reader and on a greyscale printout. The cap and the fill proportion
  # are what survive that.
  def bar($d):
    ($d.day | dayStampKey) as $label
    | if ($d.known | not)
      then "<i class=\"b b-none\" title=\"\($label): no readings\"></i>"
      elif $d.ok == $d.checks
      then "<i class=\"b b-up\" title=\"\($label): \(plural($d.checks; "check"; "checks")), all passed\"></i>"
      elif $d.ok == 0
      then "<i class=\"b b-out\" title=\"\($label): every one of \(plural($d.checks; "check"; "checks")) failed\"></i>"
      else ((($d.share * 100) | floor) | if . < 8 then 8 elif . > 92 then 92 else . end) as $h
        | "<i class=\"b b-part\" title=\"\($label): \($d.checks - $d.ok) of \(plural($d.checks; "check"; "checks")) failed\"><s style=\"height:\($h)%\"></s></i>"
      end;

  def chart($c; $s):
    ($s.top) as $top
    | if $s.count == 0
      then "<p class=\"nodata\">No readings in this window.</p>"
      else
        "<div class=\"plot\"><svg viewBox=\"0 0 600 78\" preserveAspectRatio=\"none\" role=\"img\" aria-label=\"\($c.name | esc) response time, \(plural($s.count; "reading"; "readings")) over the last \($s.label | ascii_downcase), highest point \($top) milliseconds.\">"
        + ([0, 33.5, 67] | map("<line class=\"g\" x1=\"0\" x2=\"600\" y1=\"\(. + 5)\" y2=\"\(. + 5)\"/>") | join(""))
        + ($s.segs | map(
            # A segment of one point is the normal case here, not an edge case:
            # the probe lands a few times a day, so most slots in a window have
            # a neighbour with nothing in it. A polyline with a single point
            # draws precisely nothing, which is how the first version of this
            # produced seven empty charts that each looked like a styling bug.
            # Repeating the point and letting a round line cap close it renders
            # a dot, at whatever stroke width the class carries, and it cannot
            # distort when the plot scales to the column because the stroke
            # does not scale.
            (if length == 1 then "ln dot" else "ln" end) as $cls
            | (map("\(((.i + 0.5) / 60 * 592 + 4) * 100 | round / 100),\((72 - (.v / $top * 67)) * 100 | round / 100)")) as $pts
            | "<polyline class=\"\($cls)\" points=\"" + ($pts + (if ($pts | length) == 1 then $pts else [] end) | join(" ")) + "\"/>") | join(""))
        + ($s.buckets | map(
            "<rect x=\"\((.i / 60 * 592 + 4) * 100 | round / 100)\" y=\"0\" width=\"9.86\" height=\"78\" fill=\"transparent\"><title>\(.at | stamp | esc): \(.v) ms\((if .n > 1 then " mean of \(.n) checks" else "" end))</title></rect>") | join(""))
        + "</svg>"
        + "<span class=\"ylab yt\">\($top)</span><span class=\"ylab ym\">\((($top / 2) * 10 | round) / 10)</span><span class=\"ylab yb\">0</span>"
        + "</div>"
        + "<div class=\"xlab\"><span>\($s.from | stamp | esc)</span><span>now</span></div>"
      end;

  def updates($i):
    "<div class=\"upds\">"
    + ($i.updates | sort_by(.at) | reverse | map(
        "<div class=\"upd\"><p class=\"upd-l\"><b>\(.status | split(" ") | map((.[0:1] | ascii_upcase) + .[1:]) | join(" ") | esc)</b> - \(.body | esc)</p>"
        + "<p class=\"ts\">\(.at | iso | stamp | esc)</p></div>") | join(""))
    + "</div>";

  def banner($i):
    ($i.components | map(. as $id | ($components | map(select(.id == $id)) | .[0].name) // $id)) as $names
    | (if $i.type == "maintenance" then "maint"
       elif ($i.severity // "") == "critical" then "crit"
       elif ($i.severity // "") == "major" then "major"
       else "minor" end) as $tone
    | "<div class=\"ban ban-\($tone)\"><span class=\"ban-t\">\($i.title | esc)</span>"
    + "<a class=\"ban-s\" href=\"feed.xml\">Subscribe</a></div>"
    + "<div class=\"ban-b\"><p class=\"ban-m\">"
    + (if $i.type == "maintenance" then "Maintenance" else "\($i.severity // "incident" | esc) incident" end)
    + " affecting \($names | map(esc) | join(", "))"
    + (if (($i.started_at | iso) > $nowS)
       then ", scheduled for \($i.started_at | iso | stamp | esc)"
       else ", since \($i.started_at | iso | stamp | esc)" end)
    + "</p>" + updates($i) + "</div>";

  def component($c):
    ($c.cells | map(select(.known))) as $known
    | ($known | map(.checks) | add // 0) as $ck
    | ($known | map(.ok) | add // 0) as $okc
    | "<article class=\"comp\">"
    + "<div class=\"comp-h\"><span class=\"comp-n\">\($c.name | esc)"
    + "<a class=\"what\" href=\"https://antifailure.dev/docs/self-hosting/status-page/#what-is-watched-and-why-each-one-separately\" title=\"\($c.name | esc): \($c.description | esc)\" aria-label=\"What \($c.name | esc) is: \($c.description | esc)\">?</a>"
    + "</span><span class=\"comp-r\"><span class=\"comp-s \(statusClass($c))\">\(statusWord($c) | esc)</span>"
    # The status word and the time it was earned travel together, always, on
    # the same line. "Operational" beside a four hour old check is a weaker
    # claim than a reader will take it for, and the reader can only discount
    # it if the age is in front of them rather than in a paragraph at the
    # bottom of the page. GitHub delivers this five minute cron every three to
    # six hours in practice, so this is the normal case and not an edge one.
    + "<span class=\"comp-t\">"
    + (if $c.latestAt == null then "never checked"
       else "checked \(($nowS - $c.latestAt) | humanSecs) ago" end)
    + "</span></span></div>"
    + "<div class=\"strip\" role=\"img\" aria-label=\"\($stripDays) days to \($lastDay | dayStampKey): \(($known | length)) with readings, \(($known | map(select(.ok < .checks)) | length)) with a failed check, \(($stripDays - ($known | length))) with no readings.\">"
    + ($c.cells | map(bar(.)) | join("")) + "</div>"
    + "<div class=\"foot\"><span class=\"f-l\">\($stripDays) days ago</span><span class=\"f-l f-sm\">30 days ago</span><i class=\"hr\"></i>"
    + "<span class=\"f-p\">"
    + (if $ck == 0 then "no readings yet"
       else "\(availability($okc; $ck)) uptime"
         + (if ($known | length) < $stripDays
            then " over \(plural(($known | length); "day"; "days")) recorded"
            else "" end)
       end)
    + "</span><i class=\"hr\"></i><span class=\"f-r\">Today</span></div>"
    + "</article>";

# ------------------------------------------------------------------ the page

  "<!doctype html>
<html lang=\"en\">
<head>
<meta charset=\"utf-8\">
<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">
<title>Antifailure Status</title>
<meta name=\"description\" content=\"Whether Antifailure is answering, checked from outside the infrastructure it reports on.\">
<meta name=\"color-scheme\" content=\"light\">
<link rel=\"alternate\" type=\"application/atom+xml\" title=\"Antifailure status updates\" href=\"feed.xml\">
<style>
/*
 * Plain on purpose. No card inside a card, no shadow, no gradient, no icon
 * set, nothing rounded that does not need to be. The page should read as a
 * document, because a person arriving here is trying to find one fact quickly
 * while something else is going wrong.
 *
 * Self contained on purpose too. No font file, no stylesheet, no script, no
 * image and no request of any kind leaves this document, because the one
 * moment it has to render correctly is the moment something else is broken. A
 * web font from a CDN is a second origin that can be down.
 *
 * That rules out the site's Inter and Geist, so the type is the reader's own
 * system stack with the site's tracking over it. Every colour is Antifailure's,
 * copied by value from console/app/globals.css and measured on both grounds.
 */
:root {
  color-scheme: light;
  --paper: #f7f7f5;
  --card: #ffffff;
  --ink: #101010;
  /* On white: ink 19.0:1, pass 5.4:1, fail 6.5:1, warn 5.9:1, muted 7.3:1,
     dim 5.0:1. Every one of them clears 4.5:1 as body text. */
  --pass: #1e7a3a;
  --fail: #b3261e;
  --warn: #8a5a00;
  --muted: #575752;
  --dim: #70706b;
  --rule: rgba(16, 16, 16, 0.11);
  --rule-2: rgba(16, 16, 16, 0.2);
  /* Bar fills, which are non-text and need 3:1 against the white row. Green
     5.4:1, amber 4.4:1, red 6.5:1. The neutral is deliberately far below that
     because it is an absence rather than a status, and it is achromatic, which
     is the one thing no form of colour blindness can confuse with the other
     three. */
  --b-up: #1e7a3a;
  --b-part: #a86a00;
  --b-out: #b3261e;
  --b-none: #d4d4cf;
  /* Incident banner grounds, each measured with white on it: minor 5.2:1,
     major 6.5:1, critical 19.0:1, maintenance 8.9:1. */
  --ban-minor: #a15c00;
  --ban-major: #b3261e;
  --ban-crit: #101010;
  --ban-maint: #4a4a45;
  --sans: ui-sans-serif, -apple-system, BlinkMacSystemFont, \"Segoe UI\", Roboto, Helvetica, Arial, sans-serif;
  --mono: ui-monospace, SFMono-Regular, \"SF Mono\", Menlo, Consolas, \"Liberation Mono\", monospace;
}

* { box-sizing: border-box; }

body {
  margin: 0;
  padding: 0 20px 64px;
  background: var(--paper);
  color: var(--ink);
  font-family: var(--sans);
  font-size: 16px;
  line-height: 1.5;
  letter-spacing: -0.011em;
  -webkit-font-smoothing: antialiased;
  min-width: 320px;
}
main { max-width: 56rem; margin: 0 auto; }

a { color: inherit; }
:where(a, label, [tabindex]):focus-visible {
  outline: 2px solid var(--ink);
  outline-offset: 2px;
  border-radius: 3px;
}

/* -------------------------------------------------------------- masthead */

.top {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 26px 0 24px;
}
.wm { font-size: 19px; font-weight: 600; letter-spacing: -0.03em; text-decoration: none; }
.wm span { font-weight: 400; color: var(--muted); }
.sub {
  margin-left: auto;
  flex: none;
  display: inline-flex;
  align-items: center;
  min-height: 34px;
  padding: 0 14px;
  border: 1px solid var(--rule-2);
  border-radius: 3px;
  background: var(--card);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  text-decoration: none;
  white-space: nowrap;
}
.sub:hover { border-color: var(--ink); }

/* ------------------------------------------------------- active incidents */

.ban {
  display: flex;
  align-items: baseline;
  gap: 16px;
  padding: 13px 18px;
  color: #fff;
}
.ban-minor { background: var(--ban-minor); }
.ban-major { background: var(--ban-major); }
.ban-crit  { background: var(--ban-crit); }
.ban-maint { background: var(--ban-maint); }
.ban-t { font-size: 17px; font-weight: 500; letter-spacing: -0.02em; }
.ban-s { margin-left: auto; flex: none; font-size: 13px; color: #fff; text-decoration: underline; text-underline-offset: 3px; }
.ban-b { padding: 16px 18px 20px; background: var(--card); border: 1px solid var(--rule); border-top: 0; }
.ban-m { margin: 0 0 12px; font-size: 13px; color: var(--muted); }
.active + .active { margin-top: 18px; }
.active { margin-bottom: 18px; }

.upd + .upd { margin-top: 15px; }
.upd-l { margin: 0; font-size: 15px; }
.upd-l b { font-weight: 600; }
.ts { margin: 1px 0 0; font-size: 12px; color: var(--dim); font-variant-numeric: tabular-nums; }

/* ------------------------------------------------------------ components */

.sec-note {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 6px;
  margin: 22px 0 8px;
  font-size: 12px;
  color: var(--dim);
}
.sec-note a { color: var(--dim); }
.gloss { max-width: 62ch; }

.comps { border: 1px solid var(--rule); background: var(--card); }
/* Rows share edges rather than each carrying its own border, which is the
   difference between a list and a stack of cards. */
.comp { padding: 16px 18px 14px; border-top: 1px solid var(--rule); }
.comp:first-child { border-top: 0; }

.comp-h { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; }
.comp-n { font-size: 14px; font-weight: 600; letter-spacing: -0.012em; }
.what {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 15px;
  height: 15px;
  margin-left: 5px;
  border: 1px solid var(--rule-2);
  border-radius: 50%;
  font-size: 10px;
  font-weight: 600;
  color: var(--dim);
  text-decoration: none;
  vertical-align: 1px;
}
.what:hover { border-color: var(--ink); color: var(--ink); }
.comp-r { margin-left: auto; display: flex; align-items: baseline; gap: 9px; }
.comp-s { font-size: 13px; font-weight: 500; }
.comp-t { font-size: 12px; color: var(--dim); font-variant-numeric: tabular-nums; }
.s-ok { color: var(--pass); }
.s-warn { color: var(--warn); }
.s-down { color: var(--fail); }
.s-none { color: var(--dim); }

.strip { display: flex; gap: 2px; height: 34px; margin: 11px 0 0; align-items: stretch; }
.b { position: relative; flex: 1 1 0; min-width: 0; align-self: stretch; }
.b-up { background: var(--b-up); }
.b-out { background: var(--b-out); box-shadow: inset 0 2px 0 var(--ink); }
.b-part { background: var(--b-up); }
/* The failed share of the day, from the top, at its true size with a floor so
   one failed check inside a busy day cannot vanish, capped in near black. */
.b-part > s { position: absolute; inset: 0 0 auto 0; display: block; background: var(--b-part); box-shadow: inset 0 2px 0 var(--ink); }
.b-none { background: var(--b-none); }

.foot { display: flex; align-items: center; gap: 10px; margin: 7px 0 0; font-size: 12px; color: var(--dim); }
.hr { flex: 1 1 auto; height: 1px; background: var(--rule); }
.f-p { flex: none; font-variant-numeric: tabular-nums; }
.f-l, .f-r { flex: none; }
.f-sm { display: none; }

/* --------------------------------------------------------------- metrics */

.msec { margin-top: 30px; }
.mhead { display: flex; align-items: center; gap: 14px; flex-wrap: wrap; }
h2 { margin: 0; font-size: 17px; font-weight: 600; letter-spacing: -0.022em; }
.seg { margin-left: auto; display: inline-flex; border: 1px solid var(--rule-2); border-radius: 3px; overflow: hidden; }
.seg label {
  display: inline-flex;
  align-items: center;
  min-height: 30px;
  padding: 0 13px;
  font-size: 12px;
  font-weight: 500;
  color: var(--muted);
  cursor: pointer;
  background: var(--card);
}
.seg label + label { border-left: 1px solid var(--rule-2); }
/* A three way toggle with no script at all: three radios, hidden but still
   focusable and still in the tab order, and sibling selectors below. A page
   that has to render from a cold cache during an outage cannot afford a
   toggle that depends on JavaScript arriving. */
.wsel { position: absolute; opacity: 0; width: 1px; height: 1px; }
/* Focus rings the whole group, not one label, because arrow keys move focus
   within a radio group and the selected label is what the :checked rule below
   already marks. Ringing a single label would be right one time in three. */
.wsel:focus-visible ~ .mhead .seg { outline: 2px solid var(--ink); outline-offset: 2px; }
#w-day:checked ~ .mhead label[for=\"w-day\"],
#w-week:checked ~ .mhead label[for=\"w-week\"],
#w-month:checked ~ .mhead label[for=\"w-month\"] { background: var(--ink); color: #fff; }
.pane { display: none; }
#w-day:checked ~ .mlist .p-day,
#w-week:checked ~ .mlist .p-week,
#w-month:checked ~ .mlist .p-month { display: block; }

.mnote { margin: 6px 0 0; font-size: 12px; color: var(--dim); max-width: 62ch; }
.mlist { margin-top: 14px; border: 1px solid var(--rule); background: var(--card); }
.metric { padding: 16px 18px 12px; border-top: 1px solid var(--rule); }
.metric:first-child { border-top: 0; }
.m-h { display: flex; align-items: baseline; gap: 12px; }
.m-n { font-size: 14px; font-weight: 600; letter-spacing: -0.012em; }
.m-v { margin-left: auto; font-size: 24px; font-weight: 500; letter-spacing: -0.03em; font-variant-numeric: tabular-nums; }
.m-v em { font-style: normal; font-size: 13px; font-weight: 400; color: var(--dim); margin-left: 2px; }

.plot { position: relative; margin-top: 8px; padding-right: 38px; }
.plot svg { display: block; width: 100%; height: 78px; overflow: visible; }
.g { stroke: var(--rule); stroke-width: 1; vector-effect: non-scaling-stroke; }
.ln { fill: none; stroke: var(--pass); stroke-width: 2; stroke-linejoin: round; stroke-linecap: round; vector-effect: non-scaling-stroke; }
.dot { stroke-width: 5; }
/* Axis text is HTML rather than SVG text, because the plot scales
   non-uniformly to the column width and a <text> inside it would stretch. */
.ylab { position: absolute; right: 0; transform: translateY(-50%); font-size: 11px; color: var(--dim); font-variant-numeric: tabular-nums; }
.yt { top: 5px; } .ym { top: 38.5px; } .yb { top: 72px; }
.xlab { display: flex; justify-content: space-between; margin: 2px 38px 0 0; font-size: 11px; color: var(--dim); font-variant-numeric: tabular-nums; }
.nodata { margin: 10px 0 4px; font-size: 13px; color: var(--dim); }

/* --------------------------------------------------------- past incidents */

.past { margin-top: 34px; }
.day { padding: 14px 0; border-bottom: 1px solid var(--rule); }
.day:first-of-type { border-top: 1px solid var(--rule); }
h3 { margin: 0 0 4px; font-size: 13px; font-weight: 600; letter-spacing: -0.01em; }
.none { margin: 0; font-size: 14px; color: var(--dim); }
.pinc + .pinc { margin-top: 16px; }
.pinc-t { margin: 8px 0 2px; font-size: 15px; font-weight: 500; color: var(--fail); letter-spacing: -0.015em; }
.pinc-t.t-maint { color: var(--muted); }
.pinc-m { margin: 0 0 8px; font-size: 12px; color: var(--dim); }

/* ---------------------------------------------------------------- closing */

.method { margin-top: 34px; font-size: 14px; color: var(--muted); max-width: 68ch; }
.method p { margin: 0 0 11px; }
.method p:last-child { margin-bottom: 0; }
.method b { color: var(--ink); font-weight: 500; }
.warnbox { margin: 0 0 14px; padding: 12px 16px; background: #faf0dc; font-size: 13px; color: var(--ink); }
footer { margin-top: 28px; padding-top: 16px; border-top: 1px solid var(--rule); font-size: 12px; color: var(--dim); }

/* -------------------------------------------------------------- narrower */

@media (max-width: 640px) {
  body { padding: 0 14px 48px; }
  /* Thirty bars rather than ninety on a phone: ninety inside 292px of content
     is under two pixels each once the gaps are taken out, which is a texture
     and not a chart. The oldest sixty are hidden rather than never drawn, so
     one page serves both widths with no second render and no script. */
  .strip .b:nth-child(-n + 60) { display: none; }
  .f-l:not(.f-sm) { display: none; }
  .f-sm { display: inline; }
  .comp, .metric { padding-left: 14px; padding-right: 14px; }
  .ban, .ban-b { padding-left: 14px; padding-right: 14px; }
  .comp-n, .m-n { font-size: 15px; }
  .comp-s, .none, .ts, .mnote, .sec-note, .foot { font-size: 13px; }
  .comp-t { font-size: 13px; }
  .upd-l { font-size: 15px; }
  .top { padding: 20px 0 18px; }
  /* The name and the status on two lines, always, rather than on whichever
     number of lines the name's length happens to produce.

     .comp-h wraps, so at 390px a row was 24px tall when the name was short
     enough to leave room for the status and the time beside it and 54px when
     it was not: five of seven rows one height and two of them the other, down
     a list whose whole job is to be scanned. Nothing about a component
     decides which it gets, so the rhythm read as an accident, which is what
     it was. Stacking every row costs 30px each and buys a list with one
     shape. Above 640px they all fit on one line and this does not apply. */
  .comp-h { display: block; }
  .comp-r { margin-left: 0; margin-top: 3px; }

  /* Touch targets. WCAG 2.5.8 asks 24 by 24 as the floor, and the two
     controls a thumb actually aims at, the subscribe button and the metric
     window, both get the full 44. The metric window was 40, which is neither
     the floor nor the target and was the only control on the page sitting
     between them. The help circles stay at the 24 floor deliberately: they
     sit on a heading line beside the component name, and 44 would either
     push that name off its baseline or reach into the row above. */
  .sub { min-height: 44px; }
  .what { width: 24px; height: 24px; font-size: 12px; }
  .seg label { min-height: 44px; padding: 0 16px; font-size: 13px; }
  .m-v { font-size: 21px; }
}

/* Windows high contrast forces every fill to the user's own palette, which
   would flatten the strip into one block. Borders survive it, so the states
   keep a shape when the colours are gone. */
@media (forced-colors: active) {
  .b { border: 1px solid CanvasText; }
  .b-none { border-style: dotted; }
  .b-out, .b-part > s { border-bottom: 2px solid CanvasText; }
}

/* Nothing on this page animates, so there is nothing to gate for reduced
   motion. There is deliberately no live indicator: a pulsing dot says nothing
   a timestamp does not say better, and it says it forever. */
@media print {
  body { background: #fff; }
  .comps, .mlist, .ban-b { border-color: #000; }
}
</style>
</head>
<body>
<main>

<div class=\"top\">
  <a class=\"wm\" href=\"https://antifailure.dev\">Antifailure <span>Status</span></a>
  <a class=\"sub\" href=\"feed.xml\">Subscribe to updates</a>
</div>
"

+ (($openIncidents + $openMaint) | map("<section class=\"active\">" + banner(.) + "</section>") | join(""))

+ "<p class=\"sec-note\"><span class=\"gloss\">Operational means the most recent check passed, not that a component is up right now. Between two checks this page knows nothing, so read every status with the time beside it.</span><span>"
+ (if $recordStart != null and $recordStart <= ($nowS - ($stripDays * 86400))
   then "Uptime over the past \($stripDays) days."
   elif $recordStart != null
   then "Uptime since \($recordStart | dayStamp | esc), which is all the record there is."
   else "No uptime recorded yet." end)
+ "</span><a href=\"https://github.com/antifailure/antifailure/tree/status-data\">Every reading</a></p>"

+ "<div class=\"comps\">"
+ (($targets | reduce .[] as $t ([]; if (. | index($t.group)) then . else . + [$t.group] end)) as $order
   | $components | group_by(.group) | sort_by(.[0].group as $g | $order | index($g))
   | map(map(component(.)) | join("")) | join(""))
+ "</div>"

+ "<section class=\"msec\">
  <input class=\"wsel\" type=\"radio\" name=\"w\" id=\"w-day\" checked>
  <input class=\"wsel\" type=\"radio\" name=\"w\" id=\"w-week\">
  <input class=\"wsel\" type=\"radio\" name=\"w\" id=\"w-month\">
  <div class=\"mhead\"><h2>System metrics</h2>
    <div class=\"seg\">"
+ ($mwins | map("<label for=\"w-\(.k)\">\(.label)</label>") | join(""))
+ "</div></div>
  <p class=\"mnote\">How long each check took, measured from a GitHub Actions runner over the public internet. It includes that runner's own network path, so it says whether a surface is reachable rather than what one of your users would feel.</p>
  <div class=\"mlist\">"
+ ($components | map(
    . as $c
    | "<div class=\"metric\"><div class=\"m-h\"><span class=\"m-n\">\($c.name | esc)</span>"
    + "<span class=\"m-v\">"
    + (if $c.latest != null and (($c.latest.duration_ms // 0) > 0)
       then "\($c.latest.duration_ms)<em>ms</em>" else "&mdash;" end)
    + "</span></div>"
    + ($c.series | map("<div class=\"pane p-\(.k)\">" + chart($c; .) + "</div>") | join(""))
    + "</div>") | join(""))
+ "</div></section>"

+ "<section class=\"past\"><h2>Past incidents</h2>"
+ (if (($incidents.unreadable // []) | length) > 0
   then "<p class=\"warnbox\">\(plural((($incidents.unreadable // []) | length); "incident file"; "incident files")) could not be read and \(if (($incidents.unreadable // []) | length) == 1 then "is" else "are" end) not shown: \((($incidents.unreadable // []) | map(esc) | join(", ")))." + "</p>"
   else "" end)
+ ($pastDays | map(
    . as $d
    | ($pastWindow | map(select((.started_at | iso | dayKey) == $d))) as $on
    | "<div class=\"day\"><h3>\($d | dayStampKey | esc)</h3>"
    + (if ($on | length) == 0
       then "<p class=\"none\">No incidents reported\(if $d == $lastDay then " today" else "" end).</p>"
       else ($on | map(
           . as $i
           | ($i.components | map(. as $id | ($components | map(select(.id == $id)) | .[0].name) // $id)) as $names
           | "<div class=\"pinc\"><p class=\"pinc-t\(if $i.type == "maintenance" then " t-maint" else "" end)\">\($i.title | esc)</p>"
           + "<p class=\"pinc-m\">\(if $i.type == "maintenance" then "Maintenance" else "\($i.severity // "incident" | esc) incident" end)"
           + " affecting \($names | map(esc) | join(", "))"
           + (if ($i.ended_at // null) != null
              then ", resolved after \((($i.ended_at | iso) - ($i.started_at | iso)) | humanSecs | esc)"
              else ", still open" end)
           + "</p>" + updates($i) + "</div>") | join(""))
       end)
    + "</div>") | join(""))
+ "</section>"

+ "<div class=\"method\">
    <p>Every component above is checked over the public internet from a GitHub Actions runner, <b>not from Antifailure's own infrastructure</b>. A probe that lived inside the control plane would go quiet during exactly the outage it exists to report, and a page served from the same Azure region as the product would go down alongside it. This page is generated on GitHub and served from GitHub.</p>
    <p>The control plane checks answer <code>/readyz</code>, which runs a real database query. <code>/health</code> is a static literal that answers even when the database is unreachable, so a page built on it would report an outage as healthy. The static surfaces are checked for a marker in the body as well as a 200, because this site has twice been published broken behind a 200.</p>
    <p>The percentages are <b>the share of checks that passed</b>, not measured uptime. Between two checks this page knows nothing, and an outage shorter than the gap can pass unrecorded. A day with no readings is drawn in the neutral and is never counted as a day that was up.</p>"
+ (if $lastSeen != null
   then "<p>The last check landed <b>\($lastSeen | stamp | esc)</b>, \(($nowS - $lastSeen) | humanSecs) ago"
     + (if $interval == null then "." else ", and checks have been arriving about every \($interval | humanSecs)." end)
     + " That interval is measured from the readings rather than taken from the schedule that asks for them.</p>"
   else "<p>No check has been recorded yet.</p>" end)
+ "  </div>"

+ "<footer>Generated \($generated | esc). "
+ (if $dropped > 0 then "\(plural($dropped; "reading was"; "readings were")) unreadable and skipped. " else "" end)
+ "Every reading behind this page is in the <a href=\"https://github.com/antifailure/antifailure/tree/status-data\">status-data</a> branch, and the probe that wrote them is <a href=\"https://github.com/antifailure/antifailure/blob/main/deploy/status/probe.sh\">deploy/status/probe.sh</a>. <a href=\"feed.xml\">Atom feed</a>.</footer>
</main>
</body>
</html>
"
