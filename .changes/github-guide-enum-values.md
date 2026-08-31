# fixed

The GitHub guide documented three values the manifest schema does not accept:
`fork_policy: none` and `all` where the schema has `never` and `always`, and
`teardown_on: [closed, merged]` where it has `close`, `merge` and `ttl`.
Copying the example produced a manifest that disagreed with the schema the
reference page is generated from.
