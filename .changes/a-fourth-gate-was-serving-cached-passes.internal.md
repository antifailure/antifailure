# fixed

`just keyring` certified this machine's credential store without looking at it.

Its own comment says "The same command the keyring workflow runs, which is what
lets `just gate` and CI agree about it". It was not the same command.
`keyring.yml:68` has always passed `-count=1` and the recipe did not, and the
test reads the operating system's credential store, which is as far outside the
module as a dependency gets, so nothing it touches is anything Go's test cache
watches. Measured rather than argued: 20.677s, then `ok (cached)`.

This is the fourth instance of one class, after the installer under
`just test-tools`, `just docexamples` over the documentation, and now this. So
the whole justfile was swept rather than this one fixed. Every other `go test`
recipe is clean, and two categories that look like the same defect are not,
which is worth writing down so the next sweep does not re-open them:

The fuzz recipes pass `-fuzz`, which never consults the cache.

The `-update-*` generators write files, which makes their packages uncacheable.
That one was measured rather than assumed, because it was the half I would have
got wrong by reasoning: `go test ./internal/cli -update-reference` twice in a
row took 143s and then 121s, both real runs, no cache.
