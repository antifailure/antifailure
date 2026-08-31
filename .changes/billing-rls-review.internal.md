# added

Two regression tests under the cross-tenant suite, from an independent review of
the row-level security migration 0020 added for billing.

The first guards the property the whole webhook design rests on: the policies in
0013 and 0020 do not key on the tenant, they key on a name the caller declares,
so a declared name left behind on a pooled connection is read by whoever borrows
it next and reaches another company's rows. The test runs each of the three
scoping helpers on a pool of exactly one connection and then reads every setting
back on the same connection. The list of settings comes out of the pool's own
source, so a policy that keys on something new is covered without editing the
test. The first version of it asked `pg_settings`, which never reports a custom
setting made with `set_config`, so it returned nothing whether or not anything
had leaked; that is why it now reads each name with `current_setting`.

The second is the ledger: `billing_events` grants a tenant SELECT and UPDATE and
deliberately no INSERT, because the primary key is the payment provider's own
event id and a tenant able to insert could claim the id of an event that has not
arrived yet, which would make the real delivery look like a retry and be
dropped. Nothing else in the suite would have noticed that verb widening.

A third records a PostgreSQL behaviour the review established and there was
nowhere else to put: the new row of an UPDATE must satisfy the table's SELECT
policies as well as the UPDATE policy's `WITH CHECK`, with no RETURNING clause
needed. On `organizations` that means weakening the `WITH CHECK` on
`stripe_delivery_moves_plan` would remove a real defence and break nothing
visible, because the read half goes on catching the escape. The comment belongs
beside that policy and a migration that has already been applied cannot be
edited, so it is asserted here instead, which also means a Postgres upgrade that
changed the behaviour would say so. Writing it turned up a dependency nobody had
noticed: the defence holds only because that policy is `FOR UPDATE`. `FOR ALL`
is a SELECT policy too, so it would widen the read half and the second lock
would be gone.

All three were watched failing before they were kept: the first against a pool whose
settings are session scoped rather than transaction scoped, in both the declared
identity and the `statement_timeout` half, and the second against a ledger policy
widened from SELECT to ALL. The third was watched failing in both directions,
once with its write policy as `FOR ALL` and once with the read half of its own
negative control left closed.

One property is recorded rather than fixed. Row-level security cannot restrict a
column, so a verified delivery may write every column of the organization it
names, including the kill switch from 0010, and what prevents it is that every
UPDATE on that table names its columns explicitly. Column level privileges were
measured rather than assumed and do not express it: the narrow grant takes twenty
api tests red because the same role runs the GitHub upsert and both kill switch
routes, and the narrowest grant that keeps every path working is green and still
admits the kill switch. A privilege is granted to a role and a policy admits a
row, and there is no way to say "this column, but only on the path that policy
admitted". The reasoning sits beside the test so the next reader has it.
