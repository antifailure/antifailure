# fixed

The status page was live and nothing linked to it, and the ages on it were
frozen at the moment it was written.

https://antifailure.github.io/antifailure/ answered 200 for days while no
page on antifailure.dev mentioned it, antifailure.dev/status was a 404, and the
only reference was a self-hosting document. A person who hit an outage had no
way to reach the one page written for that moment. The footer's Connect column
now carries a Status link beside GitHub, and /status is a 301 to the page.

The page itself printed "checked 3 seconds ago" beside Operational and kept
printing it for the hours until the next probe landed, because every age was
computed when the page was generated and served unchanged afterwards. It also
said checks arrived "about every 2 seconds": the interval was the median gap
between all readings pooled, and seven components probed inside one run sit
two seconds apart, so the staleness threshold pinned to its floor and no
component could ever read as stale when the page was written. The interval is
now measured between consecutive readings of one component, which is the gap
between runs, every age carries its epoch, and an inline script restates the
ages against the reader's clock and shows how old the page itself is once it
passes the threshold.

The probe also runs when a cd workflow completes, on top of a schedule that
GitHub delivered 25 times in three days where it was asked for 864.
