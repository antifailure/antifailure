# fixed

The three module updates dependabot proposed in #288 could not build, and the
reason was a fourth module it did not touch.

`github.com/charmbracelet/x/ansi` moving from 0.10.1 to 0.11.8 is a breaking
change to `ansi.Style`. `github.com/charmbracelet/x/cellbuf` was pinned at the
pseudo-version `v0.0.13-0.20250311204145-2c3ea96c31dd`, which is compiled against
the old shape, so seven CI jobs failed identically:

    cellbuf@v0.0.13-.../cell.go:198:10: b.SlowBlink undefined
      (type ansi.Style has no field or method SlowBlink)

`cellbuf` arrives indirectly, which is why the update group did not carry it. The
versions are readable from the module proxy rather than guessed: `cellbuf`
v0.0.13 requires `ansi` v0.8.0, v0.0.14 requires v0.11.0, and v0.0.15 requires
v0.11.5. So v0.0.15 is the release that matches, and it is bumped here alongside
the three.

`ee/engine/go.mod` pins all four as indirect and moves with them.

The failure is worth naming rather than absorbing: an automated bump that
compiles is not the same as an automated bump that is complete, and a group that
updates a module without its dependent produces a tree that no single pull
request in the group can fix.
