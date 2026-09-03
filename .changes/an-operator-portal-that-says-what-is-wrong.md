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

Every operator mutation the console sent was refused with 403. The client argued in a comment
that the operator cookie being SameSite=Strict made a cross-site token unnecessary; the control
plane requires `x-antifailure-admin-csrf` on every operator write and has asserted that in three
ways the whole time. Suspending and resuming an organization from the portal could not have
worked. The token is now fetched, cached, sent, and refreshed once on a refusal.
