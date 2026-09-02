# fixed

The nightly dogfood job ran the control plane three times under three example
names. Its matrix declared a manifest per leg and no step read the value, so
every leg ran the harness against the repository root and failed with AF-DB-012
because that job never refreshed a golden. Two of the three names it declared
were directories that have never existed, and the two examples that do exist
beside `go-api` had never been run by anything. The matrix is now the three
examples in the tree, a name with no manifest behind it fails its leg instead of
skipping it, and the golden comes from the harness flag written for this job
that nothing had ever passed.

The harness read `.antifailure/events` and the engine writes `.antifailure/logs`,
so every run record carried no events, no environment, no golden and no per phase
timing, and the six phase budgets, the teardown assertion and the leak count all
sit past that early return and had never evaluated anything. A missing log now
fails the run rather than adding a line to it.

The control plane job's `af insights` step ran after `af ci` had torn the branch
down, so it printed AF-DB-014 on every run and `|| true` reported it green. It is
removed: `af ci` runs the same insights inside the environment and writes them
into the report the job already uploads.
