# fixed

The release workflow could have published a release short an asset and stayed
green. `softprops/action-gh-release` defaults `fail_on_unmatched_files` to
false, so a pattern in `files:` that matches nothing is a warning in the log and
a successful step, and the release goes out missing a file that nobody knows to
look for. The step now requires every pattern to match.

`tools/releasecheck` is the gate for that and for the two failures beside it,
both of which are also silent in the direction that ships: a file signed at one
path and published from another, and a job that runs cosign without the
`id-token: write` that keyless signing cannot work without. It resolves each
job's effective permissions rather than searching for the string, because a
job-level block replaces the workflow-level one rather than adding to it, so a
grant that reads correctly at the top of the file can be one the job does not
have.
