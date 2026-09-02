# fixed

Expired sessions are now actually deleted. The sweeper had removed zero rows,
on every instance, for as long as it existed.

It ran on a connection with no tenant, and every policy on that table keys on
a declared value, so its DELETE matched nothing and reported success. A
statement that matches nothing does not raise.

The fix could not be a policy on the application's own role: permissive
policies are OR'd, so one naming no tenant widens every other policy on the
table, and a session row names a user and an organization. A per-tenant sweep
cannot work either, because `sessions.org_id` is nullable and an abandoned
sign-in leaves exactly the row a sweeper exists for.

So housekeeping gets a role of its own, entered for one transaction. Inside it
the sweeper reaches rows the DATABASE's clock calls expired and reads two of
their columns; a cutoff passed in can only narrow that, never widen it, so no
argument can make the sweep reach a live session. Reading a session token is
refused outright rather than returning nothing.
