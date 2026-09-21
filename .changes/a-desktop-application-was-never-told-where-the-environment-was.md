# fixed

A desktop application under `af test` was never told where the environment it
was being rehearsed against was.

Terminal workflows are started with `AF_BASE_URL` set to the environment's
address, so a command line client talks to the rehearsal copy and not to
whatever the developer's shell points at. Electron applications got nothing, so
a desktop client of a service could only reach its own configured address,
which is either nothing or production.

Electron applications are now launched with `AF_BASE_URL` set to the
environment's address, merged over the runner's own environment and written
last, so it wins over a stale value exported in the shell. Native macOS
applications are unchanged: Launch Services starts them with the session's
environment, so a variable set by the runner would be dropped rather than
received.
