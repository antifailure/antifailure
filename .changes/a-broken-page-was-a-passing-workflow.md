# security

The control plane sent the failing SQL statement to the browser.

tRPC's error formatter withheld the stack, with a comment saying why: a stack
names internal paths and table names to anyone who can provoke an error. The
message beside it was not withheld, and the database driver writes a query
failure as "Failed query: <the whole statement>" followed by the bound
parameters. A renamed table put the schema, the joins, the WHERE clause and the
source comments inside the SQL onto the console's error card, for any signed-in
viewer to read. An INTERNAL_SERVER_ERROR now carries a fixed sentence and the
real cause is logged where the operator can read it instead.

Every other tRPC code still carries the message somebody wrote for the reader,
because blanking those would turn "your role cannot see this" into a shrug.
