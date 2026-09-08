# fixed

The check that said your generated files were stale was wrong about three of
the eleven pull requests it stopped.

One CI step ran thirteen generators and then compared, and it was named
"Generated files are current". Four of those generators are
`go test <package> -update-something`, which runs the whole package, so a test
in the engine's cli package that has nothing to do with any committed artifact
failed under that name. On one morning it said that about three branches whose
generated files were fine.

The other eight had really drifted, and there the step printed a diff and
stopped. A diff names the file that moved. It does not name which of the
thirteen generators owns it, so the only remedy a reader could infer was
`just generate`, the whole set, several minutes of npm and Docker. Six of those
eight needed `go run ./tools/docsembed` alone, which takes about a second.

The generating and the comparing are two steps now, so the name asserts only
what the step checked, and the comparison is `tools/gendrift`, which groups the
drifted paths under the command that rewrites each one. It also refuses two
things the old comparison could not see: a generated file that is new and
therefore untracked, which `git diff` cannot report at all, and a ledger row
pointing at a path no longer in the tree, which would otherwise go on printing
a number about a file it had stopped comparing.

The local gate compared a hand written list of paths in the justfile while CI
compared the whole tree, so the two asked different questions. Both run this
now, and only CI passes `-strict`, which additionally refuses a changed path no
generator claims.
