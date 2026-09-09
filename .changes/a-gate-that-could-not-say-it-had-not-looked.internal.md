# fixed

`tools/gendrift` refuses to report a clean tree unless a generator has actually
run in it.

The list of generators is written in one place since #363, and both callers run
it, so the three copies cannot disagree any more. That closed the cause and left
the shape. The two halves are still two processes and nothing coupled them:
`go run ./tools/gendrift .` on its own ran no generator, compared the committed
bytes against themselves, and printed

    gendrift: 21 generated paths match their generators

That is the identical sentence a rebuilt tree prints. It was printed on thirteen
commits while `engine/internal/manifest/manifest.v1.json` sat stale on main, and
an edit to `ci.yml` that dropped or reordered the `-generate` step would have it
printed again, with the gate green throughout. A check that cannot say "I could
not look" as something different from "I looked and it is clean" is worse than
no check, because the reassuring sentence is what stops anybody asking.

`-generate` now leaves a receipt naming the commit it ran against and a hash of
the ledger it ran, and the comparison refuses to answer without one that matches
this tree. Three different refusals, because they are three different facts: no
receipt at all means no generator has run here; a receipt from another commit
means the generators saw a different tree; a receipt from another ledger means a
row has been added whose generator has never run, which is the original defect
one turn later. The receipt is cleared BEFORE the generators run rather than
after a failure, so there is no ordering in which it survives a run that died
half way and vouches for a half written tree.

The enumeration the ledger cannot do for itself is checked too. `-strict`
catches a generated file no row claims only when something wrote it during the
run, so it is blind to a generator that is in neither the ledger nor any
workflow: nothing rewrites the file, nothing changes, and the comparison passes.
Every tracked Go file that declares itself generated must now be claimed by the
ledger. Four do and four are claimed. That covers Go only, and it says so: the
marker is a Go convention, and the generated JSON, Markdown and golden frames
here carry no uniform equivalent.
