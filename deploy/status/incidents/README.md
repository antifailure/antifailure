# Incidents and scheduled maintenance

One JSON file per incident, named `<id>.json`, where the id is also the `id`
field inside it. Add a file, open a pull request, merge it. The next probe
picks it up and publishes it, at most one probe interval later.

These live here, on `main`, rather than on the `status-data` branch the probe
writes. A note written during an outage is the highest stakes prose this
project publishes, and it is written by a tired person at an unsociable hour.
Here it gets a diff, a review and a history. On `status-data` it would be a
hand edit of an orphan branch a machine pushes to, where the likely outcome of
a mistake is a force push over the probe's own history.

`deploy/status/incidents.sh check deploy/status/incidents deploy/status/targets.json`
validates every file and names every problem. The `validate` job in
`.github/workflows/status.yml` runs it on any pull request that touches this
directory, so a malformed file is a red check rather than a hole in the page.

## The fields

    {
      "id":          "2026-09-01-control-plane-503",   same as the file name
      "title":       "Sign-in returned 503 for 52 minutes",
      "type":        "incident",                       or "maintenance"
      "severity":    "major",                          minor | major | critical
      "components":  ["control-plane-api", "console"], ids from targets.json
      "started_at":  "2026-09-01T04:10:00Z",           UTC, seconds, trailing Z
      "ended_at":    "2026-09-01T05:02:00Z",           omit while it is open
      "updates": [
        { "at": "2026-09-01T04:18:00Z", "status": "investigating",
          "body": "Sign-in is returning 503. We are looking at it." },
        { "at": "2026-09-01T05:02:00Z", "status": "resolved",
          "body": "A migration held a lock long enough to exhaust the pool." }
      ]
    }

`severity` is required on an incident and ignored on maintenance. An update's
`status` is one of `investigating`, `identified`, `update`, `monitoring` or
`resolved` for an incident, or `scheduled`, `in progress` or `completed` for
maintenance. The list is closed and short on purpose: the whole point of the
bold word at the head of an update is that a reader learns the state without
reading the sentence after it.

An incident with no `ended_at` renders as still open, with its most recent
update at the top of the page above everything else. Add `ended_at` and a
final update to close it.

## Writing the note

Say what a customer could observe, when it started, and what they should do.
Say what you know and mark what you do not. Never write a cause you have not
confirmed: an update saying "we are still identifying the cause" is worth more
than a wrong one, because the correction costs more trust than the delay.

Timestamps are UTC with a trailing `Z`, because the page renders them as
written and a reader in another timezone needs one unambiguous reference.
