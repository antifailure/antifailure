# fixed

Abandoned GitHub sign-ins were kept for good.

`oauth_states` holds one row per sign-in that has been started and not
finished. The row is unusable ten minutes after it is written, and it was
deleted only when somebody came back and redeemed it. Nobody swept the table.
So every person who pressed "Continue with GitHub" and closed the tab left a
row behind permanently, and so did the site deploy gate, which probes that
route on every publish. The table had gone without a sweeper since the first
migration, while sessions, device authorizations and sign-in links each got
one.

The volume was never the problem. An unbounded, security relevant table on an
unauthenticated path is.

The control plane now removes states a day past their expiry, from the same
housekeeping interval that sweeps the other three. It is housekeeping and not
enforcement: expiry is still decided when a callback is redeemed, so a sweep
that is late costs table size and cannot end a sign-in that is in flight.

This one needed no new policy, unlike the two sweepers before it. The policy on
this table already admits the application role to every row, because a
handshake has no tenant and no user to key on, and that is now measured by a
test rather than assumed.
