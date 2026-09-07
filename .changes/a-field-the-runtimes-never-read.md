# fixed

A manifest could ask for three instances of a worker and get one, and nothing
said so. `replicas`, `resources.cpu` and `resources.memory` were in the schema
and in the reference, the normalizer filled all three in, and then nothing read
any of them: the local runtime never mentions replicas, both Kubernetes
Deployments hardcode one, and neither runtime emits a resource requirement at
all. The cost is not the missing feature, it is that a run reproducing a bug
which only appears at more than one instance went green having never started a
second instance. All three are refused at validation now, by name, the way
`load.thresholds.query_count_increase` already was.

`just fieldsweep` counts the population so a fourth cannot accumulate in
silence. Every field in the manifest schema is read by something, refused by
validation, or declared as a label the engine deliberately does not read, and a
label has to carry a description in the schema, because an undocumented label
and a field nobody wired look exactly alike from the outside.
`workflows[].tags` was the field that showed why: it had no description at all.

The inventory could not see the other half of the same defect. There is one
golden, one masking pass and one branch, and all of them are Postgres, so a
ClickHouse or a Redis declared as a service came up empty and no fidelity
dimension mentioned it. An analytics product's twin held masked Postgres
metadata and zero events and scored as faithful. A `datastores` dimension names
every store other than the primary as `unmeasured`, with the reason, so the
report says what it did not measure instead of leaving it out.
