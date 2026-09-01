# security

`af up` branched another project's golden.

A golden pool is shared: the Docker provider keeps goldens as images on a
machine wide daemon, and a published store is shared by a fleet on purpose.
Selection filtered on the masking rules digest, which says how a golden was
masked and not whose it is. A project with no `masking.yaml` declares no rules,
so every project on a machine without one hashed to the same value and drew
from one pool.

Reproduced with two ordinary Express repositories. One declared a production
database and refreshed a golden from it. The other declared none, printed in
its own generated manifest that "branches will start empty", and `af up`
brought up a database holding the first project's tables and its rows.

A golden now records the project it was made for, along with the variable
naming production, the seed command, the masking digest, the subset and the
Postgres major, and an environment branches only a golden whose record equals
its own. `af golden pull` refuses a published golden made for another project,
checking the claim in the signed attestation before restoring anything.
`af golden gc` collects only this project's versions, where running it in one
repository used to delete another repository's goldens. `af golden list` says
which project each version belongs to.

Every run now says where its data came from, rather than printing a version
identifier alone:

    branching the database from gv_20260901033741_74234e98, made for
    acme-billing from the database named by PRODUCTION_DATABASE_URL

Expect one golden refresh per project the first time a command runs after this
change, because no existing golden carries the record.
