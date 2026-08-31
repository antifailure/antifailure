# fixed

Every full Go suite in the justfile and CI now runs with `-count=1`, because Go's
test cache does not notice when the subject lives outside the package.

Two proven cases. `tools/gatecheck` shells out to `git check-ignore`, and a
subprocess's file reads are invisible to the cache: with `/npmaudit` deleted from
.gitignore, the exact tree that turned main red, `go test ./gatecheck` reported
`ok (cached)`. And `engine/cmd/af-proxy` compares its model defaults against
`runner/src/model.ts`: with that file changed to a different model, which is the
drift the test exists to catch, it also reported `ok (cached)`.

Both fail correctly with `-count=1`, so the tests were right and only the
invocation was wrong. CI restores the Go build cache through actions/setup-go,
so this was not only a local effect.
