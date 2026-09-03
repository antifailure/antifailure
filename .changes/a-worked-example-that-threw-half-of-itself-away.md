# fixed

The worked GitHub Actions example silently discarded half its inputs.

`examples/github-workflow.yml` declared `seed` and `concurrency` twice each in
its `workflow_dispatch` inputs. YAML keeps the last definition, so the first
pair's descriptions were text nobody would ever see, in the one file this
product hands a new user as the example of how to wire it into CI. Nothing
failed. The workflow parsed, GitHub accepted it, the example rendered, and two
descriptions were simply gone.

`tools/keycheck` now refuses any YAML file in the repository that defines the
same key twice in one mapping, and `just keycheck` runs it locally.

Helm charts are rendered through three valid profiles before they are read. A
chart template is Go template source that no YAML parser can take, so the first
version of the gate reported twelve unreadable files, checked the other forty
three, and printed a summary naming fifty five. Nothing else covers them
either: `helm lint`, pointed at a chart whose `service.yaml` defined `type`
twice, returned "0 chart(s) failed". A duplicate survives rendering intact, so
`helm template` output is what the gate parses. With helm missing the command
fails rather than skipping, because skipping would restore the same silence one
step further away.

The inline, external secret and sparse profiles reach every conditional
resource and optional body. Helm's source markers must account for every
authored YAML template, so a resource disabled by the defaults cannot disappear
from the gate.
