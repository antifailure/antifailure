# added

The status page reports on the surfaces customers use, and says what it does
not know.

It watched one target, staging, which is the one environment no customer
touches, and rendered it as a run of bars with no uptime, no components, no
incidents and no notion of not knowing. It now watches seven components across
production, the public site and staging, each one a separate line because each
one has its own way of failing while its neighbours are fine: the console is a
static export inside the control plane's own image and answers 503 when that
directory is empty, the installer is placed by the site assembly and has been
missing from a publish, and the waitlist API is a managed function that can be
present and refuse every request, as it did for two days behind a green deploy
each time.

Every static check now asserts a marker in the body as well as the 200, since
each of those three failures would have read as healthy to a check that looked
only at the status line. The markers are build output paths rather than copy,
so a prose edit cannot produce a false outage.

Every figure is computed from the record. Percentages are described as the
share of checks that passed rather than as uptime, a window is offered only
once the record reaches back across it, nothing rounds up, and the page prints
the interval the checks have actually been arriving at rather than the one the
schedule asks for. Incidents and scheduled maintenance are one file each,
written by hand and reviewed in a pull request.

State never reaches a reader as colour alone: pass and fail are four units
apart in OKLab under deuteranopia, so a day containing a failure is capped in
near black and sized by the share that failed, and every component carries a
shape and a word beside its colour. Nothing on the page animates.

The page is still served from GitHub rather than from the infrastructure it
reports on, and it is still self contained, with no font, stylesheet, script
or image request leaving the document, because the one moment it has to render
correctly is the moment something else is down.
