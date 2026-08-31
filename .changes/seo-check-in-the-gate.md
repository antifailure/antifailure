# fixed

`just gate` now runs the marketing site's own checks, through a new `just seo`,
and `tools/gatecheck` can see the family of gates that hid it.

`npm run check:seo` asserts that the shipped site has the sitemap, robots,
canonicals, OpenGraph, structured data, markdown twins and skip link it claims
to have. Every one of those was absent from production when the check was
written, and none of them breaks a build or fails a type check when it goes
missing. It has run in `ci.yml` on every pull request since. Nothing in the
justfile ran it, so `just gate` was green on a tree CI refused, on the surface
a customer sees first.

`tools/gatecheck` could not say so. Its npm pattern wanted `test` or `tsc`, so
every `npm run` in every workflow matched nothing at all: not one gate seen
three times, but `npm run build` in www, in docs and in console seen never,
and `check:seo` with them. `npm run` is now a gate family, every script of it.
`npm test` and `npm run test` are one gate, because they are one command.

One script it now sees is not a gate: `npm run seed` writes the fixture rows a
dogfood run is then driven against, and asserts nothing about the tree. It is
exempt by name in `exemptFromGate` with that reason recorded, rather than
excluded by a pattern written around it, because the next `npm run` somebody
adds to CI has to be reported rather than skipped in silence.

It makes seven `npm run` gates visible that nothing was looking at: four
builds, a typecheck, `check:seo`, and `seed`. Five were already covered, one is
the gap this fixes, and one is the exemption. `just seo` runs 34 assertions and
passes; with `www/public/og.png` moved aside it reports 33 of 34 and the recipe
exits 1.
