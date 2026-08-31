# fixed

A manifest with several problems reports them as a list again. The wrapping
introduced with the terminal width work collapsed the newlines the validator
had already put between them, so two independent problems ran together into
what looked like one sentence, at the moment somebody is working out what is
wrong with their file.

A service whose name is wider than `af explain`'s gutter now puts the name on
its own line with its facts hanging underneath, instead of pushing them off the
right margin and leaving the row misaligned against its own continuation.

`af golden list` says "not recorded" where a golden carries no rules hash. A
blank cell under that heading reads as none, and the truth is that nobody knows
whether the rules have changed since.

`af down` wraps the reason a resource could not be removed, which carries a
provider's own error text and is the longest line on the page.
