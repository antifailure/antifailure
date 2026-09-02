# added

An operator portal, on its own mount point, behind a credential rather than
behind a claim.

Running a hosted control plane means somebody eventually has to look at a
tenant they do not belong to: to answer a support question, to read an audit
trail, to suspend an account that is abusing the service. Until now there was
no way to do that which was not either a database console or a tenant login
borrowed from a customer, and neither of those leaves a record anybody can
read afterwards.

The boundary is a separate Postgres role with `BYPASSRLS`, reached through its
own pool, and that is the point rather than an implementation detail. A
`current_setting` predicate is a claim the application makes about itself and
is only as good as the code that sets it; a role the server authenticates is a
credential, and code that has not been handed it cannot widen its own reach by
being wrong. The two are independent mechanisms and this surface uses the
second.

Every operator action writes to an audit chain of its own, separate from the
tenant audit log, so a tenant cannot see operator activity and an operator
cannot quietly edit the record of what they did.

Operator administration refuses to act on the caller. `admin.operators.write`
is held by `super_admin` as well as by `owner`, so the dangerous move on this
surface is not an operator abusing a customer, it is an operator widening their
own privileges: a `super_admin` could otherwise have set their own role to
`owner` and picked up every owner only permission with it. `setRole` and
`suspend` refuse when the target is the caller, and the refusal is on self
rather than on a list of forbidden roles, because "somebody else decides" needs
no list to maintain.

Creating an operator mints no credential. The row lands with a null password
hash, so the account exists and cannot be signed into until somebody provisions
one out of band.

Four surfaces are here: organizations, sessions, users, and operators. The
portal is the mount point and the boundary as much as it is those four: the
other operator lanes hang their sub routers off the same object, so there is
one place an operator route can exist and one matrix test that walks all of
them. Impersonation, the support console, and search have their schema and
their permission names and no routes yet.
