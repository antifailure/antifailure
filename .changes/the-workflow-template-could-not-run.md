# fixed

The GitHub workflow template every new user is told to copy contained a command
that exits with a usage error.

`af ci --output report.md` is in `examples/github-workflow.yml` and in the
getting started page that tells a reader to copy it. `af ci` used to carry a
local `--output` meaning "a file to write", which shadowed the persistent
`-o, --output` that means text or json everywhere else. That was renamed to
`--report`, and the note recording the rename says the example workflow moved
with it. It did not. The generated command reference did, so the two
descriptions of the same command disagreed and only the one nobody copies was
right.

What that cost, run rather than reasoned about: `af ci --output report.md`
exits 2 with "the output format \"report.md\" is not recognised; use text or
json". The step after it in the template is `if: always() && hashFiles
('report.md') != ''`, and `report.md` is not empty, because the previous step
wrote the change analysis into it. So the job goes red on a usage error and
then posts the change analysis to the pull request as though it were the run's
report.

`just docexamples` did not see either one, for three separate reasons, and all
three are now closed.

It only read `docs/src/content/docs`. The example workflows are documentation
that happens not to be markdown, and they are the version most people actually
run, because the page says to copy the file rather than type the commands.

Its pattern was anchored on a line beginning with `af`, and a workflow puts the
command in a YAML value, so `run: af ci --output report.md` could not match. A
pattern that cannot match looks exactly like a pattern that found nothing.

And it checked that a flag exists rather than that its value is one the command
takes. `--output` exists. `--output report.md` does not run.

The value check is scoped to the persistent flag by pointer identity, because
the first version of it reported `af oracle --keep -o oracle.md` as broken.
That one is correct: `af oracle` still defines its own local `--output`, the
same shadowing that was called a defect when `af ci` had it. A gate is worth
having only while every finding is real.
