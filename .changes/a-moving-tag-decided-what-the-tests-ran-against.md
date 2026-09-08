# security

Thirteen container images were named by a tag, so nothing in the repository
recorded what the tests had run against.

`postgres:17-alpine` appeared in eight places across four workflows, three more
in the justfile, and once in the preview script, and `quay.io/keycloak/keycloak:26.0`
in the single sign on conformance harness. A tag is a name the publisher can
repoint. The Postgres tag resolved to 17.11 when this was written and will
resolve to 17.12 with no commit here, which means the server that the row level
security suites proved things about is whichever build carried the tag that
morning. This repository already made the argument against itself, in
`engine/pkg/emulator/aws.go`, where LocalStack is pinned by digest because a tag
that moves changes what an environment was tested against without anything in
this repository changing. A database is the harder case, not the softer one.

All thirteen now name the multi architecture index digest, so a runner and a
developer's Mac resolve one declaration to their own image. Dependabot does not
and cannot refresh them: its docker file fetcher accepts a file only when the
name matches a Dockerfile, or it is a Helm values file, or its first YAML
document carries both `apiVersion` and `kind`, and a workflow carries `on` and
`jobs`. So the refresh is a person's job and the procedure is written beside the
pin. Three tests in `tools/gatecheck` hold it: one refuses a moving tag anywhere
a container is started, one refuses a tree where two sites pin the same image to
different digests, which is what a half finished bump looks like, and one proves
the check still says no.

The apt packages beside them, `postgresql-client-17`, `zsh` and
`libsecret-tools`, are deliberately left unpinned, and the reason is recorded
with the rest.
