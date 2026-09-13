# security

The engine no longer depends on `github.com/docker/docker`, which is the module
all eight Dependabot alerts on the default branch were matching.

They were four Moby advisories counted twice, because `engine/go.mod` and
`ee/engine/go.mod` both required that module, and none had a fix on that path:
it stops at v28.5.2, three of the four cover everything through 28.5.2, and the
Go vulnerability database records no fixed version for it at all. The engine
now uses `github.com/moby/moby/client` v0.6.0 and `github.com/moby/moby/api`
v1.56.0, the modules Moby split its client and API types into, which no
advisory names. `.govulncheck.yaml` is empty, because both of its entries named
the old path and matched nothing once it was gone.

This changed the module graph and not the daemon. All four advisories are bugs
in `dockerd`, which whoever runs it installs and upgrades, so the residual
exposure written up in `docs/security/pentest-readiness.md` is unchanged, and
the engine still sends the same archive upload it always did.

One difference is visible to a user. The new client negotiates no lower than
Docker API 1.40, which is Docker Engine 19.03, where the old one fell back as far
as 1.24. `af doctor` now reads the daemon's API version and fails its Docker
check below that floor, naming the version it found, instead of leaving an older
daemon to fail partway through an environment.

The gate gap this exposed is unchanged: govulncheck asks what is reachable,
Dependabot asks what is present, and nothing here reads the second answer.
