# fixed

`prosecheck` now reads Markdown under `www` and `console`.

It keeps two lists: `documents`, the trees whose `.md` is checked, and `sources`,
the trees whose `.ts`, `.tsx` and `.mjs` carry copy. `www` and `console` were in
the second and not the first, so the site's TypeScript was scanned and a Markdown
file beside it was scanned by nothing at all. "The site is checked" was two
thirds true, and the missing third was invisible because the two lists each
looked complete on their own.

The hole is closed while it is still empty. There is one tracked Markdown file
under either tree today, `www/README.md`, and it is clean, so this changes no
prose. It makes the property permanent rather than true by luck.

One exemption came with it, and it is exactly as wide as the generator that
earned it. `next dev` writes `www/AGENTS.md` and appends its own block on every
start, em dashes included, so bringing that tree into scope turned the gate red
on a file nobody here wrote and nobody can keep clean. Worse, it would have been
red only for people who had run the dev server: CI only ever builds, so it would
have stayed green while every developer's machine failed. The skip matches the
generator's marker in the file's own bytes rather than a list of paths, so a
hand written `CLAUDE.md` at the same path is still checked and the exemption
cannot quietly grow. `www/CLAUDE.md`, which is one line reading `@AGENTS.md`,
carries no marker and is checked like anything else.

Four checks, each run rather than reasoned about: the character in
`www/README.md` is reported with its file and line; the same character in a file
at `www/CLAUDE.md` is reported, because it carries no marker; the two real em
dashes inside the generated block are not; and renaming the marker makes those
same two report immediately. Two tests cover the widened scan and the narrow
exemption, and both were watched failing against a deliberately broken version
before being believed.
