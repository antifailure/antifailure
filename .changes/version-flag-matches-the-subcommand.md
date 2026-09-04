# fixed

`af --version` answered "unknown flag" on a fresh install, which is the first
command a lot of people type and the first thing the product said to them.

`af version` was correct and `--version` was the spelling nobody had bound.
Both now run one implementation, so the text, the `--output json` object and
`--short` are identical whichever way they are asked for.

The obvious fix is the wrong one. cobra's built in `--version` prints a static
template from a package variable, and that variable is never stamped by any
build: it is why the enterprise binary once printed "community edition" from
`af version` while its own `af license status` printed "enterprise". Taking it
would have put that defect back, in a second place, on the spelling somebody
reaches for first. The edition is asked of the running binary instead, and a
test attaches an enterprise status and fails if the answer comes from anywhere
else.

`-v` is unchanged. It is the shorthand for `--verbose` on every command in the
tree, and `af -v` prints the help because a bare `af` prints the help, not
because the letter was unhandled. `--short` on its own is now refused by name
rather than quietly printing the help.
