# fixed

A live provider test's readiness gate polled `pg_isready` over the unix socket,
which the postgres entrypoint's temporary initialisation server answers while
listening on no TCP port. The gate was satisfied by a server that was about to
shut down, so the assertion after it landed in the gap. It reddened the `engine`
context on two main commits, and cd's gate then refused to deploy anything on
either of them.

Internal: a test and its helper. Nothing in the product changes.
