# fixed

The post-publish careers smoke installed the runner's dependencies in `runner`
and then installed its browser from the repository root, where there is no
package.json, so npx fetched a different playwright and downloaded browsers for
that one. The runner launched its own pinned version and found no browser. Both
commands now run in `runner`, the way the dogfood workflow has always run them.
