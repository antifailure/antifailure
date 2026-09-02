# fixed

Every way a real production database can refuse a copy printed the transcript
and no next step.

Pointing a golden refresh at a Postgres carrying what a production schema
carries, rather than at an example, stops in four ordinary places. None of them
is a broken database and none of them said what to do:

A read only role, which is what the generated manifest's own comment tells you
to hand this tool, cannot read the sequences. The first one pg_dump reaches
ends the run with `permission denied for sequence UserProfile_Id_seq`.

Row level security is on, so Postgres refuses to dump a table it would have to
filter. It is right to refuse, because a dump taken under a policy carries only
the rows that role can see and nothing in it says so, but
`query would be affected by row-level security policy` is not a sentence that
tells you BYPASSRLS exists.

An extension the source has and a stock Postgres image does not, which is every
schema using PostGIS, pgvector, TimescaleDB or pg_cron, failed inside the
restore with a control file path.

A typo in the connection string arrived as the driver's account of four failed
dial attempts, under the words "read the roles the source's policies name",
which describes the query rather than the problem and sends the reader to look
at their policies.

Each of these now has a code, a cause and a remedy that was checked by running
it:

    AF-DB-017  pg_dump was refused when it read sequence UserProfile_Id_seq
               in the source database.
    AF-DB-018  Row level security on customers stops pg_dump from reading it
               as this role.
    AF-DB-007  The source database uses the extension postgis, and the
               Postgres the golden is built in does not carry it.
    AF-DB-023  The source database answered and refused the connection.
    AF-DB-024  The value of the variable named by database.source_url_env is
               not a connection string.

A host that is not listening keeps AF-DB-002, which it always should have had.
Anything not recognised keeps its whole transcript under AF-DB-019 and is told
to run the program itself, rather than being forced into the nearest code.

pg_dump stops at the first object it cannot read and says nothing about the
rest, so a read only role missing several grants used to cost one refresh per
grant, and each refresh starts a Postgres container before it discovers the
next one. Against the schema above that was three rounds: a sequence in one
schema, then row level security on a table, then USAGE on another schema.

So when a refusal is about privileges, the source is asked what else this role
cannot read, and all of it arrives at once:

    AF-DB-017 The role in the connection string cannot read all of the source
              database: no SELECT on sequence "Mixed Case
              Schema"."UserProfile_Id_seq"; no USAGE on schema analytics; row
              level security applies to public.customers

Applying every remedy that message names, in one go, publishes the golden. The
question is only asked after pg_dump has already refused, so it cannot stop a
copy that would have worked.

The transcript is still there under `-v` on every path, including the ones
where a code has replaced the headline.

AF-DB-007 was in the catalog already, marked as planned, and nothing returned
it. Its message described installing the extension on the target, which is not
something the docker provider can be asked to do, so it now says what the two
real options are.
