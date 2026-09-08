# changed

The conformance suite had two answers, and the claim this database category is
sold on could honestly be given neither of them.

`CopyOnWrite_BranchTimeMatchesTheDeclaration` decides by stopwatch, and over a
fake cloud control plane on one local Postgres the stopwatch measures the
harness. The only way such a fixture can hand back a branch carrying the
golden's data is `CREATE DATABASE ... TEMPLATE`, which copies files, so a
truthful `CopyOnWrite: true` failed and a `CopyOnWrite: false` passed
comfortably. Both answers were about the harness.

There is now a third verdict, `UNPROVEN`, distinct from pass and from fail and
never called a skip. `Options.RealService` names the actual service a run
drives, and LEAVING IT EMPTY is what produces the unproven verdict. It is an
assertion of reality rather than an admission of simulation, because a field a
fake sets to excuse itself is a field a fake can simply never set. Forgetting
this one produces the safe answer.

It is symmetric. A run that asserts nothing is unproven whether the provider declares
true or false, because the false side is the one that would otherwise ship: a
snapshot restore provider passing comfortably against a copying fake publishes a
certified claim about its service, and nobody rereads a green check.

Six cells of the published comparison table carried a verdict beside the words
"not measured yet". A ledger now records the verdict per provider, two sweeps
check that no declaration lacks one and no published cell disagrees with one,
and `conformance.CopyOnWriteClaim` renders an unproven declaration as the word
`unproven` rather than as the declared value.

Two providers were measured rather than assumed while proving this, both
needing no account. `pgurl` declares false against a real Postgres and holds.
`docker` declares true against a real daemon and holds, so the provider
overview, which said its branch time grows with the database, was wrong and now
says what the measurement says.
