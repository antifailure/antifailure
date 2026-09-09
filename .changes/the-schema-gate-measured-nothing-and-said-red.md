# fixed

The 593 constraint schema gate had been measuring nothing, and saying red while
it did.

`TestEverySchemaConstraintIsEnforced` builds a base manifest by filling every
field the schema declares, then breaks one cell at a time and requires the
validator to refuse each break. #315 added `load.traffic.max_age` to
`schemas/manifest.v1.json` without a tuning override, so the fixture filled that
field with the application's name and the validator refused "web" as a duration.
The base manifest was therefore refused before any cell was broken, and in the
test's own words every cell measured against it says nothing. The gate reported
a failure it had not found.

Separately #327 created `engine/internal/manifest/manifest.v1.json`, a copy of
the published schema read through go:embed, from a base that predated that same
field. Neither pull request conflicted with the other and both passed their own
gates, because the two changes travelled different paths and git had nothing to
compare.

Main carried both for thirteen commits. Every branch inherited the red through
the merge ref, and three separate lanes spent time hunting their own code for a
failure that was never theirs.

The override is the field's own documented default of 336h, beside the two
`max_age` overrides already present for the same reason.

The pinned constraint count moves from 593 to 600 in the same change, and it is
the same defect one layer down. `require` stops at the first failure, so once
the base manifest was refused the count assertion below it was never reached.
593 was written down by a lane that could not have run it, and the seven that
were missing are exactly `load.traffic`: `profile` and `max_age` with a type and
a maxLength each, plus the block's own type, additionalProperties and required.
