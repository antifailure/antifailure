# fixed

The organization rule requiring a column to be masked was checked against a list
the engine never filled.

`extension.EnvironmentRequest` carried `MaskedColumns`, the enterprise policy
read it, and no code anywhere wrote to it. So `required_masked_columns` was
evaluated against an empty list on every environment. An organization that
configured the rule got a licence that granted the feature, a startup line that
printed the rule, and no refusal on any repository ever. The only thing that
kept it from refusing everything instead is that a policy naming no columns
returns before it looks.

It could not be filled where it was read. The policy hook is asked before
anything is created, deliberately, so that a refusal leaves nothing behind, and
what it has at that point is a manifest. A manifest enumerates services, not
columns. Expanding a pattern like `*.email` against the tables a manifest
happens to name would have refused every repository whose masking rules reach a
table the manifest does not mention, which is most of them.

So there is now a second socket, `extension.MaskingHook`, asked during a golden
refresh: after the engine has read the database catalogue and assigned its rules
to it, and before the executor rewrites the first row. It carries the columns the
plan will rewrite and the whole catalogue they were read from. A refusal there
means the golden is never published, and an unverified golden cannot be branched,
so no environment can hold data the policy refused.

Having the catalogue also makes the rule mean what it says. `*.email` is now
satisfied only when every email column in the database is masked; before, one
match anywhere was enough, so a plan that masked `users.email` and left
`contacts.email` readable passed a policy written to stop exactly that.

A golden published before a policy was tightened is not re-examined. Refresh it,
and the new rule decides whether it may be published.
