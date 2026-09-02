# added

Goldens on Postgres 18, and a refusal that names an edit somebody can make.

Postgres 18 has been the current release for a year and the docker provider
listed 14 through 17. A project whose production is on 18 had two ways to go,
and both were bad.

Setting `database.version: 18` was refused:

    AF-DB-003 The source database is Postgres 18, and this provider supports
              14, 15, 16, 17.
      Next: Use a provider that supports Postgres 18, or upgrade the source.

No provider in the build supported 18, so the first half of that sentence named
nothing, and the second half is the wrong direction: a source is not upgraded
to reach a version older than the one it is already on.

Leaving it at the default instead copied an 18 source into a 17 golden and said
nothing, so the preview ran a Postgres the application does not.

A golden built by the docker provider is the stock postgres image with the data
committed into it, so the majors it handles are the ones that image is
published for rather than a list this provider keeps. 18 is now in it, verified
by refreshing a golden from a Postgres 18 source holding several schemas, an
enum, a partitioned table, a materialized view, a generated column and a row
level security policy, then branching it and reading the rows back out of the
branch.

A major the provider really cannot build now says which key to edit and what it
may hold. The other providers are unchanged: they talk to a service and the
service decides.
