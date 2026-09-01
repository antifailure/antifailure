# security

`install.sh` verified the download on the happy path and passed on every unhappy
one. A `checksums.txt` that did not download printed a warning and installed
anyway; an archive not named inside one printed nothing at all and installed
anyway; a machine with no `shasum` or `sha256sum` printed a warning and
installed anyway. Only a mismatch stopped. Each of those now refuses, naming
what was missing, and `openssl` is accepted as a third hashing tool so refusing
costs almost no machine anything.

Placement was worse than a fail open. `install -m 0755 ... || { cp ... && chmod
...; }` is an AND-OR list, and `set -e` does not apply to one, so an archive
assembled without `af` in it printed `cp: No such file or directory`, then
printed `Installed <version> to <dir>/af`, wrote the PATH line, and exited 0.
The archive is now checked for `af`, the runner entry point, the runner
`package.json` and the runner `package-lock.json` before anything is placed, an
archive that will not unpack is reported as damaged rather than as `gzip:
unexpected end of file`, and every placement step reports its own failure.
