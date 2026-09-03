# fixed

The impersonation columns on `sessions` could not hold an operator
impersonation, and nothing read them.

Migration 0023 added four columns so that a session which is an operator acting
as a customer carries that fact on the row every request already reads, and
argued the point: a marker in a side table is a session that looks ordinary to
every check in the product. Two things were wrong with it, and both were
invisible until somebody wrote the first row.

`impersonated_by` referenced `users(id)`, and an operator is a row in
`admin_users`, a deliberately separate id space. The CHECK constraint was all
or nothing across four columns, so the id could be neither written nor left
out. Between them the two made an operator impersonation unrepresentable. And
`ON DELETE SET NULL` on that key could not coexist with the constraint: nulling
one of the four leaves the shape the CHECK refuses, so deleting the referenced
row failed on a table nobody was looking at.

Migration 0032 repoints the key at `admin_users`, moves the "this is an
impersonation" predicate onto `impersonation_audit_seq`, which is the one
column no cascade can null and the one that must exist before the row does, and
adds the foreign key into the audit chain that makes that structural.

`resolveSession` now reads them, which is what turns 0023's argument from a
comment into a property, and `GET /auth/session` reports it, so the person
being acted as is told while it is happening rather than only afterwards.
