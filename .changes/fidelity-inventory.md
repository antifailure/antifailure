# added

`af fidelity` takes an inventory of what an environment reproduces and what it
does not, one component at a time, across six dimensions: the services, the
data, the third party hosts, the personas, the runtime and the traffic. Every
line comes from something the engine already knew and was not telling anybody.
The runtime says what is running. The database provider says which golden the
branch came from and whether its signed attestation still matches its own
signature. The branch says how many tables and rows it holds and whether each
declared persona actually has a row in it. The manifest says which hosts the
policy names, and `internal/mockpack` says which pack answers for each one in
mock mode and whether that pack keeps state.

Nothing is estimated. A component whose state could not be determined is
reported as unmeasured, excluded from the headline, and named with the reason,
which is the same discipline the insights report applies when it says what it
could not read. A dimension the manifest never asked for is excluded whole,
because an environment that sends no traffic has not reproduced traffic
perfectly. When nothing could be measured there is no score, and that is never
rendered as nought percent.

The headline is defined every time it is printed: how many of the measured
components are production's own thing rather than a substitution, a refusal or
an absence. The per dimension verdict comes first, because the one dimension a
change touches is exactly what an average hides.

`fidelity.require` in the manifest names dimensions that must be fully
reproduced. A required dimension that was measured and found wanting exits 6
with `AF-FID-001`; one that could not be measured exits 1 with `AF-FID-002`,
because a gap in what we can see is not the same result as a fact about the
environment, and reporting the first as the second is how a check stops being
believed.

# changed

`internal/mockpack` answers two questions it always knew and never exposed:
which pack would answer for a host, and whether a pack keeps what was created.
The second is the difference between a mock of a provider and a list of canned
answers, and the inventory reports them differently rather than calling both
"mocked".

The reason a golden could not be identified no longer says "the migrations were
not rehearsed" regardless of who asked. Two callers want that fact for
different things, and the rehearsal's wording read as nonsense under a database
heading in the inventory.
