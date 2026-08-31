# fixed

`www/tsconfig.json` and `console/tsconfig.json` are committed the way
`next build` writes them, so a build no longer leaves a dirty tree in either
workspace. Next 16 rewrites both on every build, expanding the arrays,
setting `jsx` to `react-jsx` and adding the `.next/dev/types` include.

Four agents reverted the identical diff by hand in one day and described it
four different ways, because a stale Next 15 install does not do it at all.
Every report was accurate about the tree it was made in, and the variable
none of them could see was inside their own `node_modules`. That matters
now rather than later: once every install matches its lockfile, console
starts being rewritten on every build, and the person who meets it first
would have had nothing to read. Each file carries the reason in a comment,
which survives the rewrite.
