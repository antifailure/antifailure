# changed

The manifest reference now says which of `github.mode`, `github.comment`,
`github.fork_policy` and `github.teardown_on` anything reads, which is none of
them. They are validated, defaulted and printed by `af explain`, and setting
`fork_policy: always` does not make a fork run.

The reason is architectural rather than an oversight and is written down beside
the table: the hosted control plane never reads your manifest, because a control
plane that read it would have to fetch your repository. What happens instead is
in the same section, so nobody has to find out by setting one.

A test fails if one of those fields gains a reader without the table being
corrected, and if a field is added to the block without being classified. The
direction that matters is the first one: somebody wiring a setting up and
leaving a page that says it does nothing is the more dangerous half.
