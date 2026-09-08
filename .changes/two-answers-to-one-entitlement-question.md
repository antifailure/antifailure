# fixed

There were two entitlement systems and they did not know about each other.

The engine has `license.Feature`, twelve names a signed license may carry. The
control plane has `organizations.plan` and `entitlements.ts`, which is real,
tested, and about quotas. Nothing reconciled them, so "what does this customer
get" had no single answer, and a self hosted license and a hosted plan could
disagree in silence.

Worse than the disagreement was what the twelve names did. Three of them are
checked by something. The other nine are named in a license, printed by
`af license status`, listed on the licensing page and sold, and change nothing
whatever when they are absent, because no code anywhere asks whether they are
on. A customer who bought nine features and one who bought none were running the
identical product, and the page written to answer that question said "each is
named in the license, so a license permits exactly what was bought".

`ee/engine/feature/catalogue.go` is now the one answer. Every feature carries
what it is, where it is enforced as `path:symbol`, and, when it is not enforced,
which of three reasons applies: the hosted control plane covers it under the
plan as a whole rather than under this name, it is implemented and deliberately
free, or the capability is not built.

Nine tests hold it up, across two build systems and three trees. A feature in
the license and not the catalogue fails. A `feature.Declare` with no catalogue
entry fails. A row claiming enforcement at a file that contains no
`feature.Enabled` call for that exact feature fails, which is how
`compliance_packs` was found to have spent its whole life declared at
`compliance.Pack.Evaluate`, a function that takes no context and therefore
cannot ask about a license at all. And for each feature the catalogue calls
enforced, the real entry point is called twice, once with a license granting
everything else and once with everything, and the behaviour has to differ.

The licensing page's feature table is generated from the catalogue and says the
number out loud. `runtimes.md` opened with "Requires an enterprise license with
the `multi_runtime` feature" and nothing requires it: the scheduler that would
place an environment has no caller outside its own tests, it lives under
`engine/internal` where the enterprise module cannot reach it, and no manifest
can express a placement requirement to begin with. A test now refuses any page
making that claim for a feature the catalogue does not call enforced.

The control plane's half is checked from the control plane, in
`licensed-features.test.ts`, because a developer renaming `orgProcedure` does
not run a Go module that is deliberately outside the workspace.
