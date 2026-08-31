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

When the stream could not be read, the harness said so and stopped, which is
honest about the file and silent about the four assertions that live after the
early return. It now names them: the per phase budgets, the assertion that
something recorded the teardown, the count of leaked resources the engine
reported, and the leak sweep. The teardown assertion is the sharp one, because
it is downstream of the read that failed, so a missing log disabled the guard
whose whole job is to notice that nothing recorded a teardown.

The leak sweep was the one that said nothing at all. It returns nil for an
unnamed environment and nil is also what a clean sweep returns, so `leaked:
null` in the record meant either, and the environment identifier comes only
from the stream. Every run in CI reported a clean teardown it had never looked
for. The record now carries `swept`, so the two are different values.

The harness also never asked what the workflows did. `af ci` exits zero when
every workflow is blocked, which is right for the build and left the record
saying green about a run in which the agents drove nothing. Two runs went by
that way. A run that reaches a verdict on no workflow now carries a finding
saying so, which is what keeps it out of a streak; it is still not a failure,
because blocked is the environment rather than the change.
