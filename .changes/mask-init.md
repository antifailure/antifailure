# added

`masking.yaml` had to be written by hand, column by column, from a plan that
listed what the defaults had decided and what they could not place. Most
projects never wrote one, so every column nothing recognised was emptied by a
default nobody had read, and the plan kept listing the same questions on
every run.

`af mask init` writes the file. It reads the schema of the source, or of this
environment's branch when one is up, decides every column the way the built
in rules would, and writes one explicit rule per column: the default restated
with its reason where a default matched, and a rule that empties the column,
with a reason saying it was unrecognised, where nothing did. A column that
cannot be emptied, because it is unique or cannot hold null or is a type the
masker cannot rewrite, gets the nearest thing that can run and a reason saying
which. The result leaves `af mask plan` with zero problems and zero
unclassified columns, which is the property a masking file is for.

It refuses to replace a file that is already there without `--force`, since
the rules somebody edited are the most valuable thing in it. `af init` runs
the same code when the manifest names a production database and the shell
holds it, and `af start` gained a rung for the file, after the database source
and before the golden, naming `af mask init` as the next command when the
source is set and the file is not there.
