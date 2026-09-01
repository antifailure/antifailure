# fixed

A comment in `engine/internal/controlplane/sink.go` asserted that
`environment.queued` is "produced by the scheduler, which runs in the control
plane". There is no such component. `engine/internal/scheduler` exists, runs in
the engine rather than the control plane, and emits no event at all, so a reader
who trusted the comment went looking for something that is not there.

The comment now says plainly that `environment.queued` and `artifact.stored`
are reserved for capabilities that are not built, and names what each is
reserved for.

The gate beside it was certifying a property it did not check.
`TestTheControlPlaneTypesWithNoEngineEventAreTheExpectedOnes` asks whether a
type is MAPPED, not whether anything emits it, so a mapped type that nothing
emits passed it. Five of the eleven mapped types are in exactly that state:
`agent.started`, `agent.finished`, `agent.verdict`, `egress.decision` and
`env.sleeping`. `TestEveryMappedTypeHasSomethingInTheEngineThatEmitsIt` now
scans the engine source for a real emitter of each mapped type and names all
five with the reason each capability is unbuilt. It fails in three directions:
a type gaining an emitter without leaving the list, a type losing its emitter,
and an exemption left behind for a type that no longer exists.

The five are not the same kind of gap and the comment now says which is which,
because reserved and abandoned call for opposite decisions. `env.sleeping` is
reserved for something never built. `egress.decision` is disconnected rather
than unbuilt: the decisions are made, `Orchestrator.Decisions` returns them,
`af net` and `af ci` render them, and the dashboard already classifies the
event as noisy to suppress, so producer and consumer both exist and only the
wire between them is missing. The three agent types are the set where the code
does not say which, and the comment says that rather than guessing.

No behaviour changes. Nothing was emitted before and nothing is emitted now;
what changes is that the codebase says so instead of implying otherwise.
