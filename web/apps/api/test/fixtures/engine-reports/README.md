# Real bytes, from a real engine

Every file here is one `workload.finished` payload exactly as an engine puts it
on the wire: `json.Marshal` of `workload.Result`, `native` deleted,
`workload_run_id` and `outcome` added. Nothing was written by hand. Each was
produced by running the engine's own `workload.Execute` over a runner's results
and then the engine's own `hostedPayload`, so every name in them comes from a Go
struct tag rather than from somebody's memory of one.

They exist because the two suites either side of this wire were both green over
a message neither had ever sent the other. `decodeReport` read the engine's
NATIVE load result (`sent`, `rate`, percentiles under `overall`) instead of the
result document (`requests`, `achieved_rate`, flat), and a run that sent twelve
hundred requests recorded as having sent none. See the header of
`web/apps/api/src/workloads/results.ts`.

## Regenerating them

    engine version: 0.0.0-dev, commit bb1579d9 (w-studio-engine)

They are checked in rather than generated at test time on purpose: the control
plane's test suite must not need a Go toolchain, and a fixture that regenerates
itself from the code under test proves nothing. The other half of the guard is
on the engine side, where `engine/internal/controlplane/report_shape_test.go`
reads `aggregateFor` and asserts every field name it reaches for is one the
engine's `workload.Measured` struct tags actually emit. That one fails the day
the engine renames a field; these fail the day the decoder stops reading one.

To refresh them after an engine change, write a test in
`engine/internal/cli` that builds a `*workload.Result` through
`workload.Execute` with a fake `workload.Runner`, calls `hostedPayload`, and
writes `json.MarshalIndent` of the result to these paths. Keep every kind: two
of them carried the defect and the others are what proves the fix did not
simply move it.

`sql-workload.json` is the first one written that way, and its generator is a
CHECK rather than only a writer.
`TestTheSQLWorkloadWireFixtureIsWhatThisEngineWouldSend` in
`engine/internal/cli/workload_fixture_test.go` produces the document and
requires the checked in bytes to equal it, so the day a field in
`workload.Measured` is renamed the engine's suite goes red in the same commit.
Regenerate it with:

    go test ./internal/cli \
      -run TestTheSQLWorkloadWireFixtureIsWhatThisEngineWouldSend \
      -update-wire-fixture

The other four are deliberately left alone. They carry timestamps, an engine
commit and numbers from the run that produced them, and rewriting them from a
fixture somebody invented would replace real bytes with synthetic ones, which
is the property this file exists to protect.
