# added

A provider written outside this repository could be implemented and never
selected.

`engine/pkg/provider` is documented as the main extension point, meant to be
written by people outside this repository, with a conformance suite that
decides whether an implementation is conformant. All of that was true and none
of it was reachable. The engine chose its database provider from a switch in
`engine/internal/env`, its runtime from another, and its golden store from a
third, and each ended at a default that refused. Nothing consulted anything a
build had registered, because there was nothing to register into: the only way
to add a provider was to edit an internal package, which the Go toolchain
refuses to let another module import. "Extensible" meant "send us a patch".

`engine/pkg/extension` now carries five sockets beside the four hooks it
already had: `DatabaseProvider`, `DatastoreProvider`, `RuntimeProvider`,
`GoldenStore` and `Emulator`. The database, runtime and golden store switches
consult the registry after their own cases and never before them, so a
registration adds a choice and can never take one over, and each refusal now
names every provider the build has rather than only the ones that ship. A
registration under a built in name is refused at the first command instead of
being silently unused, which is the failure that would otherwise be discovered
as a build somebody believed replaced the Docker provider.

Two of the five have no lifecycle behind them yet, and each says so in its own
documentation rather than leaving somebody to find out. `tools/socketcheck` is
what keeps that honest: every socket is either consulted by the engine or
listed there with the reason, both directions fail, and the entry cannot
outlive the gap. It was written after finding that `Registry.Audit` has no
caller anywhere in the engine, so `audit_stream` would forward nothing even
once somebody wrote the sinks.
