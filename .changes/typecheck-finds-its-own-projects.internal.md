# fixed

`just typecheck` now finds every TypeScript project in the tree instead of
naming five of them, so a project cannot go unchecked by being forgotten.

The hand written list was wrong twice. console was added only after a type
error in it passed this recipe and failed CI twenty minutes later. www was
never added, and a merge that left `contentLastModified` declared twice in
`www/lib/lastmod.ts` passed `just typecheck` and failed CI with a parse error
pointing at the end of the file rather than at the damage.

`just gate` would have caught it, through `just links`, which builds www before
checking its links. That is a thin defence: agents are told not to run the full
gate and to run the targeted gate for what they touched, so somebody editing
TypeScript runs the recipe named after typechecking, gets green, and has checked
nothing.

A project checked by another gate is now named with its reason, and a reason
that stops being true fails the recipe, so the excuses cannot rot either.
