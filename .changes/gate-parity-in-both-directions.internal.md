# fixed

The generators are listed in one place now, and `tools/gatecheck` compares
`just gate` against CI in both directions.

`engine/internal/manifest/manifest.v1.json` is a copy of
`schemas/manifest.v1.json`, read through `go:embed` because embed cannot reach
outside the engine module. It sat stale on main for thirteen commits and no
check could report it, because the list of generators was written out three
times and the three disagreed. `tools/gendrift`'s ledger named fifteen,
`just _generated` ran fifteen, and `ci.yml` ran twelve. Three of the ledger's
rows had no generator on the CI side, so on a clean checkout the files those
rows name were never rewritten, never showed as changed, and could never be
reported by a tool whose only question is `git status`. The other two were
`schemas/mockpack-vectors.json` and `schemas/webhook-vectors.json`. A developer
running `just gate` caught this and CI structurally could not, and both reported
under the words "generated files are current".

`gendrift -generate` runs the ledger, in the ledger's order, and both callers
use it, so there is one list and it is the same list that gets compared. The
order was a third disagreement: `docsembed` embeds six pages other generators
write, so it has to run last, and `ci.yml` ran it fourth of twelve.

`gatecheck` exists to fail the build when the local recipe and the CI gate
disagree, and it could not see this one, because every comparison in it started
from a CI gate and asked whether the justfile covered it. A gate the justfile
ran and no workflow ran was not a question anybody asked. It asks now, with its
own exemption list and its own staleness check on that list, and the first run
of it found three more: `just gate` ran `socketcheck`, `fieldsweep` and
`coverage` and no workflow ran any of them. The first two are in `ci.yml` now.
The third cannot be, because producing its coverage profile takes the better
part of an hour, and the exemption says so rather than passing over it: the
per package thresholds are not enforced on a pull request.

Two of the seven it reported were blind spots rather than gaps, and both are
fixed. `go run ../tools/scanrepo`, which is how `ci.yml` spells the credential
scan, matched no pattern at all. And `go test ./...` in a directory now pairs
with `go test` on a package under it, as its own weaker tier that the passing
output names, rather than being reported as a gap or folded in as an equal.
