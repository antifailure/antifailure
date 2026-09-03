# changed

The Helm values and the Terraform variables are stable, and a gate holds them.

Version 1.0.0 listed both as free to change in a minor release. The cost of
that sentence falls entirely on the operator: a self hoster's values file and
tfvars file are their configuration, kept in their repository and applied by
their pipeline, and nobody outside this repository could know whether `pool_max`
still existed in the version they were about to take. Every upgrade was a hand
migration that could not be automated.

It also failed quietly rather than loudly, which is what made it worth a gate
rather than a rule. Helm accepts a values key no template reads. Terraform only
warns about a variable nothing declares. A rename does not stop an apply, it
removes a setting while the apply reports success.

The promise is the name, the type, and whether an input is required, for every
key in the chart's values file and every variable and output in the Terraform.
Outputs are named because a runbook reads them: the Azure guide pipes
`backend_hcl` into a backend configuration and the rotation runbook scopes a
role assignment with `key_vault_id`. Defaults are deliberately excluded and
`image_tag` is why, since it names the release being cut and `tools/tagsync`
exists to make sure it moves.

`tools/inputcheck` reads every one of them and compares against a snapshot
taken at v1.0.0. A removal, a rename, a changed type, an optional input becoming
required, and a new input arriving without a default each fail and say which
one they are. Adding an optional input is compatible and only asks for the
snapshot to record it. It was watched refusing each of those before it was
believed, including on the five renames in this release.

Five variables were renamed first, because freezing a name that is wrong means
living with it until a 2.0. The foundation module's `name` is
`resource_group_name`, which is what its own description called it and what the
stack already passed to it, while `name` on the control plane module means a
four character resource prefix. Its `log_analytics` is `log_analytics_enabled`,
a switch that sat one line from an output called `log_analytics_id`. The
alerting module's `connection_percent` is `database_connection_percent`,
beside the two database thresholds it belongs with. `golden_replication` and
`golden_soft_delete_days` are `goldens_replication` and
`goldens_soft_delete_days`, so one family carries one prefix.

The chart is version 1.0.0. A chart at 0.x says in the only language its
ecosystem has that its values may be rearranged at any time.
