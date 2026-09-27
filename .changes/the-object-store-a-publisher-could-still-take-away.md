# changed

The container image that every golden store suite needs is now pulled from a
registry this project controls, and the pin, the mirror and the registry are held
to one digest by a gate rather than by a comment.

Between 23:17 and 23:25 UTC on 2026-09-24 a publisher made its own MinIO images
private, and the consequence was out of all proportion to the cause: the object
storage step of the engine job began failing on every pull request, the test step
after it reported `skipped`, and a required check went red having examined
nothing. Six pull requests and the main branch were blocked behind one third
party's decision about its own namespace.

The pin moved that day to a different publisher's build of the same source, and
both files said in as many words that it was a stopgap, because the replacement
namespace is a frozen archive with no retention promise. This finishes the job.
The image is copied into `ghcr.io/antifailure/minio` and the two places that
start it, the CI workflow and the `stores` recipe, name our copy.

The copy is byte identical to what was reviewed, and that is checked rather than
asserted. Manifests are moved as bytes with `crane copy` rather than pulled and
pushed, which would collapse a multi architecture index into whichever single
platform did the pulling and change its digest, so one declaration still resolves
to the right image on an amd64 runner and on an arm64 laptop. The digest is read
back off the registry afterwards and a mismatch fails the job. It was also
verified by hand for the copy now in use: an anonymous manifest read returning
the same digest, both child manifests present for amd64 and arm64, every layer
and config blob of both readable with no credential, and the pulled image running
and refusing a deliberately wrong signature exactly as the original does.

Anonymous readability is the property that matters rather than ownership. A
runner and a fresh clone have no registry login, so a private mirror would have
reintroduced the failure it exists to escape. Nothing gained a credential, a
scope or a login step.

The new risk a mirror brings is that the registry and the repository drift apart,
so the check that used to hold the sites equal has been replaced with one that
can still see them. The old one groups references by name, which stopped being
able to compare a pin against the mirror's source the moment the two named
different registries. The new one refuses a pin the mirror does not copy, a pin
left on the upstream at the correct digest, a mirror whose own verification is
aimed at something it did not copy, a mirror of a moving tag, a copy published
under a version it is not, a mirror nothing consumes, and a mirror workflow it
cannot read at all. That last one matters most: a gate that reads a missing
declaration as a clean tree is the failure this project keeps finding in its own
instruments.

One defect in the mirror workflow itself is fixed here too, and it was found by
running it rather than by reading it. Its install step reported success while the
step after it died with `crane: command not found`, because `go install` writes to
a directory that is not on the runner's path. The tool is now called by its full
path and the install step runs it once, so a tool that arrived is proved rather
than assumed.
