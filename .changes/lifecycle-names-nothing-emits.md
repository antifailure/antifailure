# fixed

The lifecycle rail on `/product/twins` named five events the engine emits
rather than six database states, two of which are unreachable.

The panel first listed thirteen invented state names. Correcting them to the
six values of the control plane's `environment_state` enum was still one level
of the same mistake: `queued` has no entry in the engine's `controlplane.typeMap`
at all, and `sleeping` maps from `events.EnvSleeping`, which is declared,
described, and emitted by nothing. An environment can be observed in four of
those six.

The rail now names `env.creating`, `env.ready`, `env.destroying`,
`env.destroyed` and `env.failed`, all five of which `internal/env/env.go`
emits, so a reader can run `af up` and `af down` and watch each arrive.

The four phases carry no identifier. Labelling one per phase with an event was
the obvious fix and it was wrong: the Run phase's would have been
`agent.verdict`, and the whole `agent.*` family has no emitter either, so it
would have put a fourth invented identifier on the page while removing thirteen.

The migration lint claim is also rewritten rather than merely uncounted. It said
six rules where seventeen ship, and `cli/gate.go` turns every one of them into a
pull request finding under its own rule name with its fix attached, so the
sentence understated the product by eleven classes of defect.
