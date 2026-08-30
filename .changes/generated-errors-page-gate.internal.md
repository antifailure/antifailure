# fixed

The local `just gate` regeneration check compared the generated Go constants
against the catalog but never compared the generated error reference page
against it, even though both come out of the same `errgen` run. A page left
stale after hand-editing the catalog would pass locally and only be caught by
CI's unrestricted diff. `docs/src/content/docs/reference/errors.md` is now in
the local check's path list, so a drifted page fails the same way in both
places. The catalog and the generator already carried the more important fix,
excluding the 32 `planned: true` codes from the page and naming their count
instead, before this change: the page was verified to already match what
`errgen` produces.
