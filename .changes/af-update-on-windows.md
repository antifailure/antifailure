# added

`af update` works on Windows. It used to refuse the platform outright, and it
could not have worked there as written: it replaced the binary by renaming the
new one over it, and Windows refuses to overwrite the image of a running
process, which during an update is always the process doing the update.

On Windows the update now fetches the release's zip, checks it against the
published checksum exactly as the tarball is checked elsewhere, and applies
the same refusals to every entry: no path outside the release, no links, no
duplicates, no entry larger than it declares, and a checksum failure in any
entry, including ones the update does not install. It then moves the running
`af.exe` aside, which Windows does allow, puts the new one in its place, and
puts both the binary and the runner source back if the second step fails. The
next `af` to start deletes the copy that was moved aside; one still running,
such as an MCP server an editor keeps open, is left until it exits.

The installation lock uses LockFileEx on Windows, and recovery after an
interrupted update closes its journal before removing it, which Windows
requires and which had made every recovery there fail at its last step.
