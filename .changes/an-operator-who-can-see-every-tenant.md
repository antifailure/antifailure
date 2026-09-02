# added

A database role the admin portal can read every tenant with, and the wall that
keeps the application out of it.

Answering "why did this account's run fail" has needed somebody who can look at
another organization's rows. Nothing in the schema allowed that, so in practice
it would have happened through a shared password at a psql prompt, where
nothing is recorded. `antifailure_admin` is that access made explicit: a
separate role with BYPASSRLS, which is the only mechanism that reads two
tenants at once now that every table carries FORCE ROW LEVEL SECURITY. It reads
widely and writes narrowly, to exactly the actions the portal offers, and it
gets INSERT and SELECT on the audit log and never UPDATE, so an operator cannot
rewrite the record of what operators did.

It is a role rather than a privilege on purpose. The application cannot be
granted its way into it, because reaching it means opening a connection with a
password the application process is not given. A test asserts nothing has been
granted membership and that SET ROLE into it is refused; granting it to the
application breaks that test and the operator-notes isolation test together.

Impersonation is recorded on the session row, where the code that resolves a
session on every request cannot miss it, and a check constraint makes the rules
structural: the four columns are all or nothing, a blank reason is not a
reason, and the row must carry the sequence number of the audit entry that
authorised it. An impersonated session that was never audited cannot be
represented at all, which is a stronger guarantee than writing the two rows in
the right order and trusting nobody reorders them.

Support notes are not tenant data. The application role holds no grant on that
table, so an operator's private note about a customer cannot appear in that
customer's export or on any page they can open.
