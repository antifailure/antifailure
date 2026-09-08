# fixed

`ee/web` holds four complete enterprise packages. `sso` is ten source files of
SAML 2.0 and OIDC with a Keycloak conformance suite. `scim` is fourteen routes
with an orderings suite. Both are driven end to end over real HTTP against a
real Postgres by their own tests, both are documented, and until now neither
was reachable by any customer, because no production binary ever called
`install()` on either of them.

Three files in the tree describe the process that would have mounted them.
`web/apps/api/src/extensions.ts` says "the enterprise entry point imports this
and calls it". `ee/web/sso/src/routes.ts` says "Registered by the enterprise
entry point". `ee/web/sso/test/harness.ts` says it registers the extension "the
way the enterprise entry point registers it". There was no enterprise entry
point.

It could not easily be written, which is why it was not. `main.ts` was the
whole program: six hundred lines of environment reading and server construction
at the top level of a module, so a second edition could only be built by
editing a file the community build must not be able to see, or by copying every
line. The copy is the same class of defect as the one being fixed, because two
copies of a configuration drift and the symptom is a bug only a paying customer
can reproduce.

The body is now `boot.ts` and a function with one seam, `main.ts` is three
lines that call it, and the enterprise entry point in `ee/web/server` adds
registrations and nothing else. One reader of every variable, one construction
of the server.

Two things that had never been read by anything now are. The control plane has
never had a licence reader: `ee/engine/license` runs in the engine, and
`ee/web/sso/src/provision.ts` has always taken the seat limit from "the host",
saying the licence is parsed elsewhere and the number handed over. There was no
host, so the seat limit AF-EE-004 sells has never refused a single member.

The gate mounts the routes and refuses them rather than not mounting them. Not
registering is cheaper and answers 404, which says the feature does not exist
and is indistinguishable from a build that never had it, a renamed route, a
proxy that dropped the path, and the state this fixes.
