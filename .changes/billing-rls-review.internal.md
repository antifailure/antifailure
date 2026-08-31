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

Both were watched failing before they were kept: the first against a pool whose
settings are session scoped rather than transaction scoped, in both the declared
identity and the `statement_timeout` half, and the second against a ledger policy
widened from SELECT to ALL.
