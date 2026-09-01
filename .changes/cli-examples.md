# added

Every command shows a worked example. `af secret set --help` now shows what a
real invocation looks like rather than only what its switches are called, and
the same examples appear on the generated command reference page.

# fixed

An argument typed wrong now says what the command takes and offers one line to
copy, instead of cobra's "accepts 2 arg(s), received 1" printed on its own.
