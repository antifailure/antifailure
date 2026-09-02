# added

`af doctor` reports the Postgres client, and the newest server version it can
read.

Doctor's promise is that every problem it names is one you would otherwise meet
halfway through a run. Copying production shells out to `pg_dump` and
`pg_restore`, and it never looked at either. A machine with neither, or with a
client older than the source, said "This machine can run Antifailure" and then
failed at the step that comes after every other one: the repository read, the
manifest written, the images built, and only then does the copy stop.

`pg_dump` refuses a server newer than itself outright, and there is no flag for
it, so the ceiling is worth knowing before the twenty minutes rather than after.

```
  ok    Postgres client              pg_dump 18 at /opt/homebrew/bin/pg_dump, so
                                     it can copy a source up to Postgres 18
```

A warning rather than a failure when nothing is found. A project that fills its
golden from `database.seed` runs neither program, and doctor answers about the
machine, so it has no manifest to tell the two apart.

The ceiling is the newest client installed, which is not the one a copy runs. A
copy picks the oldest client that still clears the server it just asked, so that
a machine with 15, 16 and 18 copies a 16 server with the 16 client. Doctor has
connected to nothing and has no bar to clear, and asking the copy's question
with no bar returns the oldest install: the first version of this reported a
ceiling of Postgres 17 on a machine that copied an 18 source three commands
later. The two questions are now two functions and a test holds them apart.
