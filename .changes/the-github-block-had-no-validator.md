# fixed

Nothing checked the values in the `github` block, so `fork_policy: nevr`,
`mode: sideways` and `teardown_on: [closed, merged]` all loaded without a word.

The JSON Schema has carried those enumerations from the start, and the manifest
reference is generated from it, so the page told a reader the values and
nothing held them to it. `normalize` fills an EMPTY fork policy and leaves a
misspelt one alone, and everything downstream reads an unrecognised policy as
`label`. That is the safe direction and it is silent: somebody who writes
`nevr` gets label behaviour, believes forks are refused outright, and is
running a stranger's code behind one label instead. A typo in a security
control has to be an error.

Two pages in this repository were shipping `teardown_on: [closed, merged]`, and
one of them told the reader to add it so that "a merged pull request gives its
branch back", which is wrong twice: the key is read by nothing, and teardown
happens anyway.

`manifestcheck` could not see any of it. The gate exists precisely because a
documented manifest the engine refuses is invisible to vale, cspell, lychee and
claimcheck, and it only ever asked whether a KEY exists. It reads enumerated
values now: against the tree as it was, it reports four problems on two pages
where it used to report none.
