# added

The comparison could vary the application and never the database.

`af load compare` has always brought a second environment up from the base
revision, pinned both sides to one golden, and sent both the same traffic. The
one thing it could not vary was the database: `database.image` lives in the
manifest, the baseline deliberately takes everything but the build context from
the candidate's manifest, so both sides ran one database build. A person
hardening their own storage engine wanted the opposite experiment, the same
workload and the same rows on two builds of THEIR database, and there was no way
to ask for it.

`--image` and `--baseline-image` name the build each side runs, each defaulting
to `database.image`, so a run that names neither is the run that existed before
them. When only the images differ the two sides run the same application
revision built from the same tree, and a base revision equal to this one is
allowed instead of refused: that refusal used to be unconditional, which would
have dragged a second application build in alongside the second database and
left no difference attributable to either. The report names the axis that
differed, revision, image or both, and says plainly that a difference cannot be
attributed to either when both moved.

There is still one golden, because two would be two sets of rows. One build
wrote that data directory and the other opens it, so a build that CANNOT open it
is now its own finding, AF-DB-044, carrying the server's own account: the
verdict line is the same sentence for a catalog version, a block size and a
write ahead log format, and only the detail beneath it says which. It is noticed
when the container stops rather than after the readiness wait, so the most
useful verdict in the feature does not arrive as a timeout. A major version
mismatch between the two images is refused before either environment is built,
by the provider's own comparison against the server rather than against a tag.

A guide walks the whole flow end to end, because the pieces existed on four
separate pages and nothing joined them: declare the build, run one workload
against both builds on one golden, read the throughput and the latency
distribution, then crash the database and read what the recovery kept. Every
output block on it is from a run, and the page says which command produced each
one.

An environment already running is refused rather than measured. The comparison
keeps the environment identifier on purpose, so a database branch already up was
adopted for a run that asked for a different build: the copy never happened, the
container kept serving the build it was started with, and the report named the
one that was asked for. Every number in it was then attributed to a build that
did not run. That is now AF-DB-045, which says which build is running and which
was asked for. The branch is not replaced, because a branch is copy on write and
replacing one destroys everything written since it was made.

And the copy of a golden onto another build is keyed on that build's image ID
rather than on its name, because a name is a thing somebody repoints. Rebuilding
an image in place and running the comparison again, which is the whole iteration
loop for the person this is for, reused the copy made from the previous build
while the preflight validated the new one.

