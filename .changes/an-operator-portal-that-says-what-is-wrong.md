# added

The operator portal's Overview now says what is wrong with the installation instead of only
listing what the portal contains. It leads with one sentence, backed by real queries: whether
an installation switch is engaged, whether a customer deletion stopped part way, whether an
organization is suspended, and whether an operator account is waiting to be provisioned. Under
it is a work list that is empty exactly when there is no work, and which names the questions it
asked so an empty answer cannot be mistaken for a panel that failed to load.

Analytics & Usage measures consumption in environment-hours, the unit every plan cap is already
enforced in, per organization and against that organization's own daily cap. It also reads model
spend against the budgets somebody set. The page states plainly that there is no usage rollup in
this schema, so the figures are computed live, there is no history older than the underlying
table, and it draws no trend line over a series nobody stores.

Admins & Permissions can now create an operator, change a role, and suspend or restore an
account. Those four routes existed, were guarded, were audited and were enforced by database
triggers, and no screen in the console reached any of them. The page also shows what every
platform permission grants and which roles hold it.

System Configuration reports what the running control plane actually resolved rather than what
its environment intended: which capabilities are configured, the name of the variable behind
each, the schema version this database is on, and whether any installation switch is engaged.
No credential value appears on it.

# fixed

Operator mutations returned the response envelope instead of the answer. The operator client
sent tRPC paths through the plain JSON transport, which returns the body as it arrives, so
every field a caller read off a mutation was undefined. Creating an operator wrote the row and
its audit entry and left the panel showing its own form, so the obvious next move was to press
the button again. Where a tRPC answer lives is now one function that the console's own tests
can execute.
