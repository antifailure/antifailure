# added

The migration lint could tell you a change would lock a table or rewrite it,
and it stayed silent on the change that quietly opened the data. A migration
that disabled row level security, rewrote a policy so it admitted every row,
granted a table to PUBLIC or anon or authenticated, dropped the column a table
is scoped by across tenants, or handed a role BYPASSRLS, SUPERUSER or
CREATEROLE, applied cleanly and reported pass. Every one of those is a
migration that succeeds in review and lets one tenant read another's rows, and
none of them was what the seventeen availability rules were watching for.

There are now database-security rules on the same migration path, reasoning
about the statements the change adds rather than the branch's absolute grant
and role catalogue, because a snapshot restored twin inherits roles the change
never touched and a rule that read them would fire on inherited state. They
carry the security namespace in their finding rule and route to a
`security.db_security` policy key of their own, so a project can gate a
broadened grant apart from a non concurrent index and the release gate gives
each one a security exit code: a broadened grant is refused on policy grounds,
and the rest are verification failures. Each defaults to fail and a manifest
can lower any of them. The permissive-policy rule fires only on a tautological
clause, never merely on a policy that omits a tenant column, so an admin only
policy is not the false positive that gets the check switched off. BYPASSRLS is
caught apart from a grant, because it is an attribute on a role that makes
every policy stop applying to that role's sessions rather than a privilege on a
table, and conflating the two is how a review misses it.
