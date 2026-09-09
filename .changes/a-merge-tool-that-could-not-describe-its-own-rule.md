# fixed

The merge tool refused a pull request for four characters inside a code span,
on the pull request whose subject was that rule.

`tools/prmerge` strips fenced blocks and inline code spans before it looks for
an attribution trailer, and the comment on those two patterns says both are
removed before a text is judged, naming prosecheck's punctuation exemption as
the precedent for doing it. Only the attribution check actually did. The
punctuation check matched its patterns against the raw text.

So within one file the attribution rule could tell a mention from a use and the
punctuation rule could not, and the punctuation rule is the one that blocks the
merge. CLAUDE.md permits a command flag inside backticks in as many words, and
prosecheck accepts it, so the two gates gave opposite answers about the same
four characters and the stricter one was not the documented one.

That comment already records this exact failure happening once before, to the
attribution rule, under the heading that the rule could not tell a mention from
a use and refused its own pull request. It happened again to the rule two
hundred lines below it.

`prose` now applies the same two replacements. Both directions are pinned by a
test and each was mutation tested: removing the stripping refuses the code span
case again, and stripping everything stops refusing prose punctuation. A
markdown table separator is three hyphens and was never matched by the pattern,
which the test records so nobody adds an exemption for it.
