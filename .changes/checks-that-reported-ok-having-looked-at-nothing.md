# fixed

Two gates in the engine job reported ok having checked nothing, for two
different reasons, and one of them was written specifically for the case in
which it could not run.

The leak check is the sharper of the pair. Its own comment says it exists
because "a suite crashed before its own assertion ran", and it carried no `if:`
key at all. A step with no condition takes the default `success()`, so it is
skipped the moment an earlier step in the job fails, and a crashed suite is a
failed step. The one situation it was written for was the one situation in which
it never ran. On a green job it duplicated an assertion the suites had already
made, and on a red one it printed nothing, which is a check that can only ever
agree with whatever already happened. It now runs on `!cancelled()` rather than
`always()`, deliberately: a cancelled run has killed the suites part way
through, so their cleanup never ran and containers are up by definition, and a
gate that reds every concurrency kill and every job timeout is a gate people
learn to read past. The two leak checks in the dogfood workflow already used
this idiom, so the engine job's was the only one of the three that was silent.

The three golden store round trip suites were the second. They skip when no
server answers, which is right on a laptop, and the engine job set
`AF_REQUIRE_DOCKER`, `AF_REQUIRE_DATABASE` and `AF_REQUIRE_RUNNER` with no
equivalent for object storage. So all three skipped on every pull request, `go
test` printed nothing for a skip as it always does, and the package reported ok.
What was going unexamined is the part no fixture can check and this repository
writes by hand: Signature Version 4 signing for S3, the account shared access
signature for Azure Blob, and the escaped object name, `alt=media` read,
separate upload path root and paging shape for Cloud Storage. TestS3Store's own
comment already said a fixture cannot tell a correct signature from a plausible
one.

`AF_REQUIRE_OBJECT_STORE` turns that skip into a failure, and the stores are
PROVIDED rather than merely required, because setting the variable with nothing
to point it at converts a vacuous pass into a permanent red wall, which is a
worse instrument and not a better one. MinIO, Azurite and fake-gcs-server now
start in the job and in a `just stores` recipe at the same three digests, so a
developer's run and the runner's run examine the same servers. One variable
covers all three suites, named for the resource rather than for a vendor,
because three variables is three chances to wire two and forget the third. The
three images are 62.2 MB, 113.8 MB and 25.4 MB compressed for the runner's
architecture, read from each registry's manifest rather than estimated.

MinIO comes from quay.io and not from Docker Hub. An anonymous pull token for
`docker.io/minio/minio` comes back carrying an empty access list, so the
command the old skip message told people to run needs a login that neither a
fresh clone nor a runner has.

Pinning those three exposed a third gate that could not say no.
TestEveryPinnedImageAgreesOnOneDigest holds every site naming one image to one
digest, and its pattern was lowercase across the name, the colon and the tag
together. A registry name is lowercase by specification and a tag is not, and
MinIO publishes no lowercase tag at all: its releases are named
`RELEASE.2025-09-07T16-13-09Z`. So a MinIO reference matched nothing, the image
never entered the map, and a tree naming two different digests for it in the
justfile and the workflow would have passed. The tag half now takes uppercase,
the grouping moved into a function so the falsification arm drives the gate
itself rather than a copy of its pattern, and the new arm checks both
directions: two sites at two digests is reported, two sites at one digest is
not, and the lowercase case that already worked still does.
