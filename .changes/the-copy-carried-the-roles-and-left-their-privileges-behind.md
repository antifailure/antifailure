# fixed

A golden carried every role its source named and none of the privileges the
source held for them, so a twin ran the application under a privilege shape
that was production's in neither direction.

The dump is taken with ownership and privileges dropped, which is right for
ownership: the restore's own user has to own what it restores. What the flags
throw away with the owner's grants is every other role's too. So the twin
arrived with the roles production names, the memberships between them and the
row level security policies that mention them, and not one GRANT. This
repository's own session sweeper enters antifailure_sweeper and was refused
`DELETE FROM sessions` with SQLSTATE 42501 within the first minute of every
environment, because the DELETE and the SELECT on one column that migration
0024 granted were not in the copy and 0024 is in the ledger, so nothing ran it
again. Every grant made by a migration the ledger records as applied was gone
in every branch, while the connecting superuser owned everything and was
refused nothing. Row level security travelled and grants did not, so the twin
was half faithful, and the half that failed loudly was the half production
keeps tightest.

After the restore the copy now reads the source's ACLs on tables, sequences,
views, columns, functions, types, the schemas themselves and the default
privileges, restricted to the roles it carries and to PUBLIC, and issues the
equivalent GRANT in the target, WITH GRANT OPTION where the source had it. An
object whose ACL the source has touched has PUBLIC's privileges revoked first,
so a function whose EXECUTE production revoked from everybody stays revoked in
the copy instead of being handed back by a fresh CREATE FUNCTION. Default
privileges are re-declared for the target's connecting user, because that is
who a branch's migrations create tables as, so a table a branch adds gets what
production's would. Ownership stays with the connecting user, deliberately, and
the owner's own ACL entry stays behind with it. A grant in a schema the copy
excludes stays behind with the schema.

Proved on one cluster, where a fresh target database lacks every grant however
many roles it shares with the source: after the copy each role is entered and
does exactly what the source allowed, and is refused with 42501 what it did
not. The two cluster proof carries the sweeper case verbatim: the
application's role enters the sweeper's and deletes expired sessions in the
copy as it does in production.
