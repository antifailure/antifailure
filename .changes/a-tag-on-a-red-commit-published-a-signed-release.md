# fixed

A release tag no longer publishes from a commit CI has not passed.

The release workflow triggered on a `v*` tag and on nothing else, and a tag is
one command anybody with push access can run on any commit. Pushed onto a red
commit it built four binaries, wrote a checksum file, generated a bill of
materials, signed both with cosign and published the lot, green the whole way.
The signature was even honest. It says the release workflow in this repository
produced those bytes, which was true, and it says nothing about whether the
commit worked. Nothing else did either.

A gate job now runs before anything is built and waits for CI's conclusion on
the commit the tag names, the same rule `cd.yml` has applied to deployment
since the beginning and with the same budget behind it. Green publishes. Red
refuses. Still running waits, because a busy queue is not a broken commit.

Two conclusions that read like a pass and are not: a run GitHub reports as
`cancelled`, which is the same word it uses for a job that hit its own time
limit, for a run somebody stopped by hand and for a run a newer push superseded,
and a run reported as `skipped`, which looks in a list exactly like one that
passed and means nothing ran. Both refuse. So does a conclusion nobody has
invented yet.

If your tag is refused with `cancelled`, re-run CI on that commit, wait for
green, then re-run the release.
