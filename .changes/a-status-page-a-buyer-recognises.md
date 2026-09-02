# added

A status page that reads like a document, and a Subscribe control that is real.

It watched one target, staging, which is the one environment no customer
touches, and rendered it as a run of bars with no components, no uptime, no
incidents and no notion of not knowing. It now watches seven components across
production, the public site and staging, each one a separate line because each
one has its own way of failing while its neighbours are fine: the console is a
static export inside the control plane's own image and answers 503 when that
directory is empty, the installer is placed by the site assembly and has been
missing from a publish, and the waitlist API is a managed function that can be
present and refuse every request, as it did for two days behind a green deploy
each time. Every static check now asserts a marker in the body as well as the
200, since each of those failures would have read as healthy to a check that
looked only at the status line.

The page itself is plain on purpose: an incident banner when there is one,
then a component per row with its status in a word and its last ninety days,
then the response times behind those checks, then the incident history day by
day. No card inside a card, nothing that animates, and no font, stylesheet,
script or image request leaving the document, because the one moment it has to
render correctly is the moment something else is down.

Every figure is computed from the record. Percentages are described as the
share of checks that passed rather than as uptime, a ninety day figure is only
called that once the record reaches back ninety days, nothing rounds up, a day
with no readings is drawn in the neutral and never counted as a day that was
up, and an isolated reading is drawn as a dot rather than joined by a line to
one hours away.

State never reaches a reader as colour alone. Amber and red are 0.7 apart in
OKLab under deuteranopia and green and red are 4.0 apart, so every component
states its status in a word, a day containing a failure is capped in near
black and sized by the share that failed, and the neutral is achromatic.

Subscribe is an Atom feed generated from the same data, carrying one entry per
incident update and one per run of failed checks the probe detected. A button
that did nothing would have been worse than no button.
