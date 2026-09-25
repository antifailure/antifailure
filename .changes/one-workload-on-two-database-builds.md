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
