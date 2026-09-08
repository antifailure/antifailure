# fixed

Main went red on a gate that refused two images nobody could pin, and the same
gate was right about a third.

`TestEveryContainerImageIsPinnedToADigest` matches any YAML `image:` field.
Two of them in `control-plane-image.yml` are `strategy.matrix.include` values
naming the images that workflow BUILDS AND PUSHES, so no digest for them exists
until the job runs. The gate landed in one pull request and the second matrix
row landed in another; each was green alone and main went red on the pair.

The matcher is narrowed by CONTEXT rather than by shape. The tempting repair is
to skip any value carrying no registry, tag or digest, since `control-plane`
carries none. That would also skip a bare `image: postgres` in a services
block, which resolves to `latest` and is exactly what this gate exists to
refuse. A hole opened while closing a false positive is worse than the false
positive, so the new test pins both directions: a build matrix row is ignored,
and a bare name in a services block is still refused.

The third finding was not a false positive. `enterprise-image-proof.sh` really
did run `postgres:17-alpine` with no digest, which is the moving tag this gate
was built to catch, and it is now pinned to the digest every other site names.

Narrowing by context left one hole, and it is checked rather than merely
stated. A matrix row can just as easily name an image the workflow PULLS, read
back as `image: ${{ matrix.pgversion }}`, which is the ordinary way to matrix a
database version. That reference was invisible from both ends: skipped in the
row for sitting in a matrix, and skipped at the field for being an expression.
So every matrix key an `image:` field interpolates is now checked in the rows
that carry it, and only those keys are.
