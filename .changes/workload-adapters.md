# added

`af workload` runs a hosted workload definition through the command that names
it. Four kinds, kept apart because they measure materially different things: an
observed_load mix through `af load run`, an http_scenario through `af load
scenario`, a browser_workflow through `af test`, and an exploration through `af
explore`. Each executes the same orchestrator call the plain command already
makes, so nothing about those commands changes.

Every result carries the plain command that reproduces it, and a knob the plain
command has no flag for is refused rather than dropped: a definition setting
concurrency on an observed_load fails before anything is sent, because `af load
run` has no such flag and honouring it would be a promise the run cannot keep.

A run that measured nothing gets its own exit code, separate from the one a
real failure gets, so a job gating on the exit code can tell "the tests passed"
from "nothing was tested".

`af workload teardown` removes an environment and says what was actually
removed and what is still standing. `af workload promote` compiles an
exploration into a workflow definition and lists, one line each, what the
compilation could not carry over. `af workload compare` differences two results
of the same kind and states what it cannot control.
