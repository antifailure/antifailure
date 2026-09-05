# added

The MCP server served four tools out of a command surface of eighty one, so an
agent could rehearse a migration and read the egress log and could not bring an
environment up, drive it, load it, explore it, look at it, read its output or
remove it. Everything past the two rehearsals was reachable only by a person
typing a command.

Seven more tools now cover the environment's whole working life.
`start_environment` creates the environment for the checked out branch and
`teardown_environment` destroys it. `describe_environment` says what is running
and `read_service_logs` says what it wrote. `run_load_test` sends production's
weighted mix, a short smoke of it, or the declared journeys, chosen by one
enum rather than by three near identical tools. `run_browser_workflows` drives
the declared workflows and asks the manifest's invariants of the rows they
leave. `explore_for_friction` sends agents at a goal with no script.

The division of authority is unchanged and is a property of the schemas rather
than a convention. There is no argument on any of these that names a branch, a
base URL, a database, a golden, a safe route, a threshold or a runner
executable: the environment comes from the checkout, the limits come from the
manifest's policy block, and unknown fields are refused. `teardown_environment`
is the one tool that destroys, so it is not marked read only, it says so in the
first word of its description, and it requires the caller to name the branch,
which is then checked against the checkout. There is no wildcard and no way to
reach another branch's environment.

What a run reports is bounded and stripped of what it does not control. The
rows behind a violated invariant are counted and never quoted, because they
come out of a branch of a masked copy of production. A verdict word this engine
cannot read is blocked rather than repeated. Page text, control names, route
names, scenario names and service logs are all output the application or the
candidate branch wrote, so every one of them is neutralised and clipped. A
container id and an artifact path name this host and are reported as present or
absent instead.

The verdicts come from the evaluator `af ci` uses, so a tool call and a pull
request check cannot disagree. A load run that sent nothing, a threshold that
was in force and measured nothing, an exploration that did not cover its goals,
and a workflow run in which nothing reached a verdict are all INCONCLUSIVE
rather than clean, because an experiment that did not happen says nothing about
the change.
