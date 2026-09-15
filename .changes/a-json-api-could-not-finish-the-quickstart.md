# fixed

`af init` wrote personas that sign in with a password whatever the repository
was, so the documented first run could not finish on a service that serves
JSON. A JSON API owns no users table, `af up` refused with AF-DB-022, "the
personas could not be created, so signing in will not work", and `af test`
exited 3 with no verdict. Had it got past that, the agent would have waited for
a login form on a service that answers JSON until the workflow's budget was
gone, which is what this repository's own `examples/go-api` says in the comment
above the `login: none` it sets by hand.

Detection now decides how a persona signs in from what the repository shows. A
password persona needs somewhere to create the account and a form to type the
password into, so the draft keeps `login: password` when the repository depends
on an authentication provider that owns its users, declares a users table in
its own SQL or Prisma schema, or renders any markup. Only a repository with
none of those gets `login: none`, where a password persona cannot work at all
and a persona that never signs in lets the workflow run and be judged. `af
init` says which it chose, and why, under Assumed.
