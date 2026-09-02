# The Atom feed behind the Subscribe control.
#
# It exists because the control has to be real. A status page with a Subscribe
# button that does nothing is worse than one with no button: it tells a
# customer they will be told, and then does not tell them. There is no mailing
# list here and building a fake one would be the same lie in more code, so the
# button is a feed, which costs almost nothing, works in every reader, and is
# genuinely what a person watching a vendor's status wants.
#
# Two kinds of entry, and both are real.
#
#   Incident updates, one entry per update, so a subscriber sees each note as
#   it is written rather than one entry per incident that silently changes.
#
#   Detected outages, one entry per run of consecutive failed checks for a
#   component, computed from the readings. Without these the feed would be
#   empty until somebody hand wrote an incident, and the most common real
#   outage is the one nobody had time to write up. A run is reported with its
#   first and last failing check and whether it has recovered, which is
#   exactly what the readings support and nothing more.
#
# Entry ids are tag URIs and are stable: a reader must not show an entry twice
# because the page was regenerated. The id of an outage entry is keyed on its
# first failing check, which does not move as the run grows.
#
# Inputs mirror page.jq's, plus $base, the absolute URL the feed is served
# from. Atom wants absolute links and a relative one is resolved differently
# by different readers, so it is one variable in render.sh rather than a guess
# repeated in four places.

def esc: if . == null then "" else (tostring | @html) end;
def iso: try fromdateiso8601 catch null;
def rfc: strftime("%Y-%m-%dT%H:%M:%SZ");
def stamp:
  (strftime("%b") + " " + (strftime("%d") | sub("^0"; "")) + ", " + strftime("%Y"))
  + " - " + strftime("%H:%M") + " UTC";
def plural($n; $one; $many): "\($n) " + (if $n == 1 then $one else $many end);
def readingOk: if has("ok") and ((.ok | type) == "boolean") then .ok
               elif has("ready") and ((.ready | type) == "boolean") then .ready
               else false end;
def readingId: if has("id") and ((.id | type) == "string") and (.id | length) > 0 then .id
               elif has("name") and ((.name | type) == "string") then .name
               else null end;

($targets[0]) as $targets
| ($history[0]) as $history
| ($incidents[0]) as $incidents
| ($now | floor) as $nowS
| ($targets | map({key: .id, value: .name}) | from_entries) as $names

# One entry per run of consecutive failing checks. reduce over the readings in
# order, opening a run on the first failure and closing it on the first pass
# after one, so a component that fails twice with a pass between them is two
# outages and not one, which is what actually happened.
| [ $targets[] | .id as $id
    | ($history | map(select(readingId == $id and (.checked_at | iso) != null)) | sort_by(.checked_at)) as $reads
    | ($reads | reduce .[] as $r ({runs: [], open: null};
        if ($r | readingOk)
        then (if .open == null then . else {runs: (.runs + [.open]), open: null} end)
        else { runs: .runs,
               open: (if .open == null
                      then {id: $id, from: $r.checked_at, to: $r.checked_at, n: 1,
                            detail: ($r.detail // "")}
                      else (.open + {to: $r.checked_at, n: (.open.n + 1)}) end) }
        end)) as $acc
    | (($acc.runs + (if $acc.open == null then [] else [$acc.open + {still: true}] end))[]) ] as $outages

| [ ( $incidents.incidents[]? as $i
      | $i.updates[]?
      | { at: .at,
          id: "tag:antifailure.dev,2026:status/incident/\($i.id)/\(.at)",
          title: "\(.status | split(" ") | map((.[0:1] | ascii_upcase) + .[1:]) | join(" ")): \($i.title)",
          body: .body,
          extra: ((if $i.type == "maintenance" then "Maintenance" else "\($i.severity // "incident") incident" end)
                  + " affecting " + ($i.components | map($names[.] // .) | join(", "))) } ),
    ( $outages[]
      | { at: .to,
          id: "tag:antifailure.dev,2026:status/outage/\(.id)/\(.from)",
          title: "\($names[.id] // .id): \(plural(.n; "failed check"; "failed checks"))\(if (.still // false) then ", still failing" else ", recovered" end)",
          body: ("The probe recorded \(plural(.n; "consecutive failed check"; "consecutive failed checks")) for "
                 + "\($names[.id] // .id), from \(.from | iso | stamp) to \(.to | iso | stamp)."
                 + (if ((.detail // "") | length) > 0 then " The first failure was: \(.detail)." else "" end)
                 + (if (.still // false) then " It has not passed a check since."
                    else " A later check passed." end)
                 + " This entry is generated from the readings and is not a written incident report."),
          extra: "Detected from the readings" } ) ]
  | sort_by(.at) | reverse | .[0:50] as $entries

| "<?xml version=\"1.0\" encoding=\"utf-8\"?>
<feed xmlns=\"http://www.w3.org/2005/Atom\">
  <title>Antifailure status</title>
  <subtitle>Incident updates, and outages detected by the probe.</subtitle>
  <id>tag:antifailure.dev,2026:status</id>
  <link rel=\"self\" type=\"application/atom+xml\" href=\"\($base)feed.xml\"/>
  <link rel=\"alternate\" type=\"text/html\" href=\"\($base)\"/>
  <updated>\(if ($entries | length) > 0 then ($entries[0].at | iso | rfc) else ($nowS | rfc) end)</updated>
  <author><name>Antifailure</name></author>
"
+ ($entries | map(
    "  <entry>
    <title>\(.title | esc)</title>
    <id>\(.id | esc)</id>
    <link rel=\"alternate\" type=\"text/html\" href=\"\($base)\"/>
    <updated>\(.at | iso | rfc)</updated>
    <summary type=\"text\">\(.body | esc)</summary>
    <content type=\"html\">&lt;p&gt;\(.body | esc)&lt;/p&gt;&lt;p&gt;\(.extra | esc)&lt;/p&gt;</content>
  </entry>
") | join(""))
+ "</feed>
"
