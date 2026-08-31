# fixed

`tools/gatecheck` now pairs a CI gate to a justfile command by the directory it
runs in as well as by the command, and its passing line describes what it
actually checked.

Keyed on the command alone, `go test ./...` in `engine` and the same in `tools`
were one gate, and so were `npm test` in `web` and in `ee/web`; covering either
covered both, and dropping the enterprise half from `just gate` was green. The
directory is in neither command, so nothing about the strings could tell them
apart: CI carries it in `working-directory:` or a `cd` inside a `run:` block,
and the justfile in `cd`, `--prefix` or `-C`. Reading it means reading each side
in blocks, which also fixes the two ways the old line scan was wrong about
`just` and about YAML: a recipe without a shebang runs every line in a shell of
its own, so a `cd` does not carry to the next one, and a `working-directory:`
can sit after the `run:` it applies to.

The line it printed on success claimed every gate was "reachable from `just
gate`" and checked two weaker things: that the command appeared somewhere in
the justfile, and, separately, that every gate-shaped recipe was called. A
command sitting in a recipe `gate` never runs satisfied the first and was never
held to the second. Coverage now requires the recipe holding the command to be
one `just gate` reaches, and the passing output says how many gates were paired,
how many are exempt, and how many paired by command only because the justfile
works its directory out at run time.

The count went from 30 gates to 39. All nine are the same commands told apart by
where they run, and every one of them was already covered, so this found no gap
that was hiding. What it removes is the way one would have hidden.
