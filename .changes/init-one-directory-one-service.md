# fixed

`af init` reported one application as two services and then refused to write
anything, which stopped people at the third command of the install path.

A Dockerfile beside a `package.json` whose name is not the directory name is
the median containerised Node repository. Detection keyed services by name and
every source spells the name differently, so `dashboard/` produced a service
called `dashboard` from the Dockerfile and one called `bonfire-dashboard` from
the package, both on port 3100. The manifest validator correctly refused two
services claiming one port, and the refusal told the user to fix a line in a
file `af init` had declined to write. Running it again could not help.

A service is now identified by the directory it is built and run from plus its
role, and the evidence from every source in that directory merges into it. One
source declaring two services in a directory still means two services, so a
compose file with a web and an admin container on one build context is left
alone.

Four errors on the same path were dead ends and are not any more.

- `af init --non-interactive` answered a question that has no default with
  AF-MAN-004, whose next step is to pass `--non-interactive`. AF-DET-004 names
  the question and the exact `--answer` flag that settles it.
- A draft that fails validation now returns AF-DET-005, which says plainly that
  nothing was written, rather than the validator's own AF-MAN-002 pointing at a
  file that does not exist.
- `--answer` only ever reached a question, so the override AF-DET-005 tells you
  to reach for did nothing when detection had read the value with confidence.
  It now overrides a detected value too, and an id that names nothing is
  refused with the ids that would have worked.
- `af init < /dev/null` asked every question into a stream nobody was reading
  and took the defaults in silence, because the terminal test read the
  character device bit, which `/dev/null` has.

Separately, `af init` wrote `target: build` for the common multi stage
Dockerfile whose builder is named and whose final stage is not, so `af up`
would have built the stage that compiles the application instead of the one
that runs it. That one is worth reading twice, because nothing failed: the
manifest validated, `af init` succeeded, `af up` succeeded, and the
environment quietly ran the build stage.

Two service names change as a result of the fold, and only for a repository
being set up for the first time. A service name is a hostname inside the
environment and it also prefixes the environment name, so it is what you see
in `af env list` and `af down`, not just an internal identifier. Nobody with a
committed `antifailure.yaml` is renamed under them: `af init` refuses to touch
an existing manifest without `--force`.

`AF-DET-003` is deleted. It was reserved for a port collision and never
emitted by anything. The sentence it would have printed is produced by the
manifest validator, which reaches the user as AF-MAN-002 from `af doctor` and
`af up`, and as the detail inside AF-DET-005 from `af init`.
