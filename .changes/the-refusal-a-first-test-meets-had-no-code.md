# fixed

The refusal a new user is most likely to meet carried no code. `af test` on a
freshly initialised repository stopped with "no users table could be found, so
there is nowhere to create a persona", with no `AF-` number, no next step and no
docs link, from a binary whose refusal one command earlier is `AF-MAN-001` with
all three.

It is `AF-DB-022` now, and it names all three ways out rather than two:
`auth.table` for a table under a name detection did not recognise,
`auth.adapter: seed`, and `login: none` for a persona that never signs in, in
which case no account is needed at all. It is wrapped rather than replaced, so
the sentinel survives and a manifest where nobody signs in still carries on
instead of being refused over accounts it never wanted.
