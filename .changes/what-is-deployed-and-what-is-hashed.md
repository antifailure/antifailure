# fixed

Six more claims the site made that the running system does not.

`api_key` was drawn as deleted. The default rule for a key-shaped column is
`hash_hex`, not `nullify`: the value is replaced by a keyed hash of the same
length. Only `session_token` is nullified. The free-text panel showed surgical
redaction inside a sentence; `free_text` replaces the whole field with synthetic
prose of the same length, which is safer and is now what the panel shows. The
masked addresses were at invented domains that anybody could register, where the
transform always writes `example.test`, which is reserved.

Subsetting was described as the default on `/product/safe-state` and as a built
cost control "by default" on `/product/architecture`. `subset.enabled` defaults
to false and `af explain` prints "subset off, the whole database is masked".

`/product/firewall` illustrated a direct-IP attempt as a row in the decision
log. A connection to a public address never reaches the gateway, because the
twin has no route to one, so it is blocked more strongly than a rule and leaves
no row. The ledger row is now a CONNECT to an unlisted name, which the gateway
does refuse and does log, and the bypass panel says what actually happens.

Four pages said the control plane was not deployed. It is, at
app.antifailure.dev, invitation only, and `/sla` now describes both environments
and what production is configured for rather than describing staging alone.
