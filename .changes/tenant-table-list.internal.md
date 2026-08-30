# fixed

`tenantScopedTables` said in its own comment that the cross-tenant suite
asserts it covers the database, and nothing read it: the suite asks the
database which tables carry `org_id`, which is the stronger check and left the
list as a stale copy nobody maintained. The typed schema is now checked against
it, so a table added with an `org_id` and left out of the list fails.
