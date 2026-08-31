# fixed

The comment on `events.Event` said the envelope is identical across the engine,
the runner and the control plane. Four of the eight names differ and two have no
counterpart at all, and nothing outside Go reads that schema. It now says which
type the control plane actually receives and where the translation happens.
