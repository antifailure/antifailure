# fixed

`THIRD_PARTY_NOTICES.md` in the repository listed 40 Go modules where the engine
links 89. Its generator ran in exactly one place, the release workflow, which
writes the file into the published artifact rather than comparing it against the
committed copy, so the committed one was free to drift and did, by 49 modules.
It is regenerated, and `just _generated` and the CI job that runs it now
regenerate it on every pull request and fail on a difference.

The generator also answered for whichever platform it happened to run on. A
release publishes darwin and linux archives, and `modernc.org/libc` links
`github.com/ncruces/go-strftime` on darwin and not on linux, so the notice built
on the release runner attributed 88 modules while the two darwin binaries in the
same release linked 89. It now takes the union over the platforms in the release
build matrix, read from the workflow so that adding an architecture cannot leave
the attribution behind.
