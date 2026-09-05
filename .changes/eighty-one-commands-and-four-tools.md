# added

The MCP server answered four questions out of an engine that answers eighty one.

An agent connected to `af mcp` could rehearse a migration and inspect the
egress firewall, and that was the whole surface. Everything else the engine
knows, whether the environment is close enough to production to be worth
trusting, what a failure code means, what the project is actually configured to
do, which checks a diff needs, whether the data still holds, whether this change
behaves like the release it replaces, and what masking does, was reachable only
by a person typing flags into a terminal. Eight tools now cover that ground:
`assess_environment_fidelity`, `explain_error`,
`explain_effective_configuration`, `plan_checks_for_change`,
`check_data_invariants`, `compare_with_previous_release`,
`inspect_data_masking` and `apply_data_masking`.

They are named for the question rather than for the command, so a model picks
one without reading a manual, and the two that cost minutes submit a run and are
polled with `get_rehearsal_run` like every other experiment here.

Three of them touch data that is real until masking removes it, and none of them
returns a value. The masking sample reports whether a column CHANGED, not what
it changed from, which is what finds the failure somebody is actually hunting: a
rule that names a column and then does nothing to it. The verification reports
which detector still recognises something and withholds even the redacted
excerpt the scanner keeps, because an excerpt of real data is real data. The
invariants report which invariant broke, how many rows came back and what the
columns are called, and leave the rows themselves to `af invariants`.

`apply_data_masking` is the only one that writes, it is irreversible, and it is a
separate tool from the read only one for that reason alone. It also requires an
acknowledgement with exactly one accepted value, so that a caller which meant to
preview cannot reach it by accident.
