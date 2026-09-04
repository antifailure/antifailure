# added

A producer for every event in the analytics catalog, and a gate that fails when
one loses its call site. An event nothing emits is a row on a dashboard that
reads zero forever, which is indistinguishable from a quiet week.

The marketing site sends page views, waitlist submissions and waitlist dialog
opens. It normalizes the referrer into a bounded channel in the browser, so the
raw referrer, the URL and the query string never cross the network at all. It
sets no cookie, uses no third party, keeps its session identifier in
sessionStorage so it dies with the tab, and turns itself off for a reader who
has set Global Privacy Control or Do Not Track.
