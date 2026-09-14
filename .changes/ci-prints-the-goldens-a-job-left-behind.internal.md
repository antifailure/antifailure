# fixed

CI now says how many goldens each job leaves on its runner, where before it
could not see a golden at all.

The "Nothing was left behind" steps count managed containers and networks. A
golden is neither: with the Docker provider it is an image, and on the managed
ClickHouse it is a database. It also outlives `af down` by design, because a
refresh makes one so the next `af up` can branch it. So a test that made a
golden and never removed it passed every check, and the goldens piled up on
developers' machines instead, fourteen images and eighteen ClickHouse databases
from one test on one laptop.

The engine job now records the golden images on the runner before its suites
and prints the ones they left afterwards, with the golden databases on the
managed ClickHouse beside them. Each dogfood job records the images before its
refresh and prints how many it left against the one it refreshed on purpose.
These steps print and never fail. The comment on the engine job's leftover
check also no longer says the ClickHouse goldens are swept by the suites, which
they were not.

The assertion, that the engine job leaves none and a dogfood job leaves exactly
the goldens it refreshed, is its own pull request once a run on main has
measured what a clean job leaves.
