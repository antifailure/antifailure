# fixed

`af golden refresh` published an empty golden when the variable naming
production held nothing, and exited 0.

A manifest that sets `database.source_url_env` is saying where production is.
When the variable it names was unset, the refresh read that as "no source
configured", which is the shape a project with no production yet has. It
started a candidate, copied nothing into it, masked nothing, verified nothing,
and committed a golden carrying this project's own provenance. The last line it
printed was `Bring an environment up from it with: af up`.

`af up` already refuses this, with AF-DB-012, when a manifest names a source
and no golden exists. That guard then passed, because a golden for this project
now did exist. The environment came up, the migrations ran, and it held none of
production's shape or volume while looking entirely correct. There was nothing
to read that said otherwise: `af golden list` showed the empty version as
verified and made for this project, which it was.

An exported but empty variable is the same case and was equally silent, which
is how a pull request from a fork reaches it. Forks get no secrets, so the
variable is empty in exactly the runs nobody watches.

A refresh whose named source holds nothing now stops before a candidate
database is started:

    AF-DB-016 database.source_url_env names PRODUCTION_DATABASE_URL, and
              PRODUCTION_DATABASE_URL holds nothing in this shell.
      Next: Export PRODUCTION_DATABASE_URL with the read only connection string
            of the database to copy, then refresh again. To build a golden with
            no production behind it, remove database.source_url_env and set
            database.seed instead.

A manifest with no `source_url_env` is unaffected: the empty golden it gets is
what it asked for.
