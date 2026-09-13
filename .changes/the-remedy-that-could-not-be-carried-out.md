# fixed

The refusal that told a Heroku or Tiger Cloud user to run a statement their
vendor gives nobody the role to run.

`AF-DB-035` is what the `pgurl` provider returns when the host server's role may
not create databases, and it ended "Run: ALTER ROLE {role} CREATEDB". That is
right on a server you administer. On Heroku Postgres the assigned user may not
create or drop databases and there is no superuser to grant it, and a Tiger
Cloud service holds exactly one database. A reader there tried the statement,
got a permission error, and had been told nothing that works.

What works is already how `pgurl` is built. The vendor stays as
`database.source_url_env`, which needs read access only, and `PGURL_ADMIN_URL`
points at a Postgres you administer, where the goldens and the branches are
made.

- **`AF-DB-037`**, for a host recognised as a vendor whose own documentation
  says the grant is unavailable. It names the vendor, gives that remedy, and
  quotes the page and the date the verdict was read.
- **`AF-DB-035` now carries the same remedy after `ALTER ROLE`.** A Heroku
  Postgres host is an EC2 name that no suffix can recognise without claiming
  every EC2 machine, so a Heroku user still reads `AF-DB-035`. Without the
  second remedy the headline case of this defect would not have been fixed.
- **`af start`** names the vendor on its database rung and blocks on a
  documented no, without connecting to anything.
- **Managed Postgres vendors**, a new page, places thirteen vendors, each with
  the page its answer was read from. Four answer `unverified` rather than a
  guess, and those are named and never refused.

The server's own answer still decides whether anything is refused. The vendor
table only decides which sentence describes the refusal.
