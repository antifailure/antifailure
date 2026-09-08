# fixed

The enterprise entry point ran and no image built it. `ee/web/server` is the
process that mounts single sign-on and directory provisioning, and the lane that
wrote it said so plainly rather than quietly: no image builds it, so it runs and
is not shipped. There was one control plane Dockerfile,
`deploy/docker/control-plane.Dockerfile`, and one workflow building it, with no
matrix in it at all. So four finished, tested, licensed packages were mounted by
a binary that no deployment anywhere could pull, which is this repository's own
signature defect, one layer out: something that works when you run it and is
absent from the artifact.

Nothing pointed at the missing image, and that is the better half of the news.
No compose file, no chart value, no Terraform image reference and no runbook
named an enterprise image, so there was no configuration waiting on something
that would never exist. The gap was one sided and closing it breaks nothing.

The obstacle was real and it is a boundary rather than an oversight. The
repository root `.dockerignore` excludes `ee`, and `ee/README.md` names that
exclusion as the fourth of four mechanisms keeping enterprise code out of the
community artifact. An enterprise image needs `ee` in its context. Deleting the
line would have traded a real edition boundary for a packaging convenience, and
it would have done it silently, because no check downstream would have gone red.
The enterprise Dockerfile carries its own ignore file beside it instead, which
BuildKit reads in its place, so the community context still cannot see `ee` and
neither list is a compromise. The community build now asserts that its own image
contains no enterprise tree, because that is the direction with no other alarm
on it.

The image is the community image plus registrations and its layout says so. The
enterprise packages reach the community ones through relative symlinks npm wrote
from `file:` dependencies, and a flattened layout leaves those dangling rather
than failing, so the image would build clean and the process would die on its
first import. The repository's own shape is preserved, and the working directory
is where the community image puts it so that `node bootstrap.mjs` still means
what the Helm chart, the Terraform job and the self-hosting page say it means.
`af-operator` hardcoded one of the two layouts; it now finds either and refuses
loudly when it is in neither, rather than handing `node` a path that does not
exist.

Built is not shipped and shipped is not working, so the workflow proves the
artifact rather than the source. Three containers, one database: the licensed
enterprise image answers a SAML metadata request with a document carrying its
own assertion consumer URL, the unlicensed one refuses the same request with 402
naming `sso` and the variable to set, and the community image built from the
same tree answers 404. Without the third, 402 and 404 are two numbers and
"refused by the gate rather than by absence" is a claim about a code path nobody
watched.
