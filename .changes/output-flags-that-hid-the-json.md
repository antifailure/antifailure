# fixed

`af oracle -o json` wrote the comparison to a file called `json`, and
`af support bundle -o json` wrote a zip archive to a file called `json`. Both
exited 0 and neither said anything. Each command declared a local `--output`
with the `-o` shorthand, meaning a file to write, and a local flag silently
wins over the persistent one that means text or json everywhere else.

The JSON was not missing. `oracle.Result` is fully tagged and both commands
have always carried a `FormatJSON` branch. Those branches were unreachable,
because nothing could ever set the format on these two commands: a written,
wired, tested feature with no path to it. `af oracle` in particular is the one
command whose output exists to be read by something other than a person.

The local flags are now `af oracle --report`, matching `af ci` which carried
the identical defect and was renamed, and `af support bundle --archive`. Neither
takes a shorthand. `TestNoLocalFlagShadowsAPersistentOne` walks the whole
command tree and compares by pointer rather than by name, because cobra reports
inherited flags alongside local ones and a check written on names would report
every command in the tree.
