# fixed

The dogfood harness read its event stream from `.antifailure/events`, which is
where `attachEventLog` used to write it. That function is gone and telemetry's
FileSink writes the stream to `.antifailure/logs` now, so the reader was never
moved with the writer and found no log on any run in CI. Everything downstream
of the event stream was dead while the run was still recorded green: the per
phase budgets, the `env.destroyed` assertion, the `resource.leaked` count, and
the leak sweep, which is scoped to the environment the event stream names and
so swept nothing at all. The three constants the harness copies out of the
engine, which it cannot import across module boundaries, are now checked
against the engine's own source.

The harness also never asked what the workflows did. `af ci` exits zero when
every workflow is blocked, which is right for the build and left the record
saying green about a run in which the agents drove nothing. Two runs went by
that way. A run that reaches a verdict on no workflow now carries a finding
saying so, which is what keeps it out of a streak; it is still not a failure,
because blocked is the environment rather than the change.
