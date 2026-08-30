# security

The eight open Dependabot alerts on the default branch are one dependency, and
`SECURITY.md` now says so along with what can and cannot be done about them.

They are `github.com/docker/docker` v28.5.1, four Moby advisories counted twice
because `engine/go.mod` and `ee/engine/go.mod` both require it. None has a fix
to take: no version of `github.com/docker/docker` or `github.com/moby/moby`
above v28.5.2 exists, three of the four cover everything through 28.5.2, and the
fourth names a Docker Engine release rather than a module version. All four are
daemon-side bugs reached through one function, `copyInto`, which copies into a
container that has been created and not started.

The gate gap this exposed is not the npm one. govulncheck asks what is
reachable, Dependabot asks what is present, and nothing here reads the second
answer, so a green daily scan sat alongside eight open alerts. The per advisory
analysis is in `docs/security/pentest-readiness.md` so a penetration tester is
not paid to redo it.
