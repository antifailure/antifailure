# fixed

GitHub reported this repository's license as `NOASSERTION`. No license in the
sidebar, absent from the license filter, and to anyone scanning it, all rights
reserved. For an MIT licensed developer tool that is a real loss, and it had
been true for as long as the file existed.

The cause was a correct paragraph in the wrong place. `LICENSE` held the MIT
text followed by a note that `ee/` is separately licensed. GitHub identifies a
license with Licensee, which normalizes the file and compares it against known
texts, and appended prose is folded into that comparison, so the extra
paragraph dropped the match below the threshold.

`LICENSE` is now the unmodified MIT text and detects as MIT. The carve out
moved to `LICENSING.md`, which states it in full and explains why it is not in
`LICENSE`. Nothing about the boundary weakened: it is also stated in
`ee/LICENSE.md`, in `ee/README.md`, in ADR 0002, and now in a header on every
source file under `ee/` rather than on 55 of 76 of them.

Licensee ignores HTML comments, so wrapping the old paragraph in one would have
restored detection with the text still in `LICENSE`. That was rejected. The
tools it would hide the carve out from are the same ones companies run before
adopting something, so a scan would have reported the whole repository as MIT
and missed the enterprise restriction entirely.

`just licensecheck` now holds both halves: `LICENSE` is the MIT text with
nothing appended, and the carve out is still stated outside it and in every
`ee/` source file. Checking only the first would be satisfied by deleting the
carve out, which is the worse of the two failures.
