# fixed

The console read its data from the browser cache, so a write did not move the
screen that made it.

`query` in `console/lib/api.ts` used `fetch` with the default cache mode, and
the control plane sends no `cache-control` on `/trpc`, so a repeated GET could
be served from the browser's own copy. Nothing noticed while no screen both
listed something and changed it. The operator portal does: suspend an account
from the panel beside the list, the write commits, the audit entry is written,
the list reloads, and the row still says active. An operator reads that as the
button having done nothing and presses it again.

Reads now say `no-store`. There is nothing to lose on this side: every response
is live state behind a session, none of them is shared between people, and none
is large enough for a cache to be worth showing somebody last minute's answer
about a customer they are in the middle of changing. Measured before and after
by watching the requests: one refetch after the write in both cases, and only
the second one returns the new row.
