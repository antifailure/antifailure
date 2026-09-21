# fixed

The database conformance suite could skip every behaviour it has and exit 0.

`engine/internal/db/docker` holds the reference implementation's proof. It is
the only thing in the repository that can refuse a provider's copy on write
declaration, its branching or its goldens, and it is gated by `requireDocker`,
which probes the daemon and skips the test when it does not answer. The probe
allowed the daemon ten seconds, the package had no `TestMain`, and so a run
where every behaviour skipped printed `ok` and exited 0. Nothing said the proof
had not run.

That is not a hypothetical. On 2026-09-21, on a machine shared by several lanes
with 118 containers on the daemon, the probe timed out at ten seconds six times
in one session while the daemon was still serving other work and answered
normally within the minute either side. Three runs of a measurement loop and
three reps of a separate experiment were lost to it, each taking ten seconds
and exiting 0 where the real work takes four minutes. They were caught only
because somebody was reading timestamps and noticed a four minute test
finishing in eleven seconds. On a CI runner nobody is reading timestamps, and
the ok would have been read as the proof.

Two changes, and they are for the two halves of the failure. The probe now
allows ninety seconds, because a daemon that is genuinely absent still fails
fast, a refused connection being immediate rather than a timeout, so the larger
budget costs nothing in the case it is meant to detect. And a `TestMain` now
refuses a run in which every test that asked for the daemon skipped, unless
`AF_SKIP_DOCKER` says out loud that this machine has none.

The sibling package `engine/internal/runtime/local` reached both conclusions
first, for containment. This is the same guard for the other property.
