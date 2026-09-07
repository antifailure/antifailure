# fixed

The fidelity report said a twin held no events while it was holding a masked,
verified copy of production's.

The datastores dimension was built from the manifest alone. It read the stance,
saw `golden`, and reported the store `absent` with the four things it did not
have named: no golden, no attestation, no tables and no rows. That was the
right answer while nothing could branch a second store, and it stopped being
the right answer when `af golden refresh` learned to make a golden of every
declared store and `af up` to branch each one. From then on an environment
holding a masked, verified, branched ClickHouse was reported as one that had
none. The product documented the defect about itself in the manifest reference,
which is better than hiding it and is not a fix: an instrument that understates
a twin is better than one that overstates it and it is still an instrument
saying something untrue about what it can see, and the report is the thing this
product asks you to trust when nothing else is.

A store declared `golden` that the environment branched is now reported from
the branch, in the two components the primary database has had all along: `data`
says how many tables and how many rows it holds and which golden it came from,
and `provenance` says whether that golden's signed attestation still matches
its own signature. They are separate for the reason the database's two are
separate, and the reason is sharper on the second store because the second
store is where the events are: a branch full of production's shape whose
provenance nothing can check is not the same result as one whose attestation
verifies, and a single verdict over both would hide whichever failed.

The `data` component is `unmeasured` rather than `reproduced`, and it says why.
Nothing here records what production's second store holds, so whether the
branch reproduces it is unknown, and calling it a reproduction would be the
defect the primary database closed one release earlier: a branch of two hundred
rows reported as reproducing a production of four billion, in the same words
and with the same verdict as a full copy. The committed volume profile that
answers this under `database.volume` has no equivalent for a second store yet.
The report names that gap rather than counting the store as a copy of
production nobody checked.

Everything else about the dimension is unchanged, deliberately. A store
declared `golden` that nothing branched is still `absent` with the same four
facts named, which is the line that makes the score go down on the stack this
was all added for. A store declared `empty`, `derived` or `topics_only` is
still `unmeasured`, because nothing here starts one, rebuilds one or creates a
topic in one, and a store reported reproduced on the strength of a declaration
would be the report believing a manifest instead of an environment. A store
that names no `source_url_env` has a golden of production's shape with none of
its rows, and its branch is a `substitution` rather than a reproduction, so the
fix cannot overstate in the direction the old code understated.

The number, on the same manifest and the same environment, with the only
difference being whether `af up` had branched the store: **89 percent before
and 100 after**, with the two components that would need production's own row
counts excluded and named. The 89 was never a twin missing a ninth of
production. It was the report unable to see a store that was there.
`just benchmark` runs the harness, which now prints all three rows.
