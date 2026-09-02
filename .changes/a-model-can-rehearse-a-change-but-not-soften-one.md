# added

A coding agent can now rehearse a change through Antifailure, and cannot make
the rehearsal easier.

`af mcp` serves four tools over the Model Context Protocol: rehearse this
branch's pending migrations against a throwaway branch of a sanitized copy of
production, inspect what the environment is allowed to reach and what it
actually reached, and read or cancel a run that is still going. It is a thin
frontend over the same orchestrator `af ci` and `af insights` drive, so a tool
call and a pull request check cannot disagree about the same change.

The division of authority is a property of the schemas rather than a request.
There is no argument on any tool that disables sanitization, widens the egress
policy, lowers a threshold, names a database or skips the rehearsal, and a
field no schema declares is refused rather than ignored. Thresholds come from
the manifest's own policy block. An agent chooses what to test; it does not
choose how safely the test runs.

Verdicts are PASS, FAIL and INCONCLUSIVE, and INCONCLUSIVE is never a quieter
PASS. A missing golden, an unavailable subsystem, a cancelled run and a server
that stopped mid run all report it, because an experiment that did not finish
says nothing about the change. Every tool requires `project_id`: agents usually
have one of these servers per repository configured at once, and naming the
project turns a misrouted call into a refusal instead of a confident answer
about code nobody asked about.

The candidate branch is treated as what it is, which is input written by
whoever opened the pull request. Statement text never reaches a result;
statements are identified by position and duration, and a name that does not
look like a name is replaced rather than repeated, because stripping the line
breaks out of an instruction leaves the instruction.

`stress_test_pr_branch` is not here. It needs a full environment and load
cycle, and a tool that appears in the list but cannot run is worse than an
absent one, because an agent will call it and spend a cycle finding out.
