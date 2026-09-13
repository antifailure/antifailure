# fixed

The comment that documents what `af down` removes said it tears down golden
versions and images, and nothing had ever made that possible.

`af down` removes what an environment recorded making, by replaying the
journal, and a replay can only remove what something wrote into the journal
with a deleter registered for it. The journal declared a `golden.version` kind
and an `image` kind from its first commit, and nothing ever wrote either one.
The comment above `newDownCommand` read the declarations as behaviour, counted
fourteen kinds where there were fifteen, and said all of them are torn down.
Meanwhile goldens accumulated: one laptop held fourteen from a single test,
because a golden outlives an environment by design and nothing said so.

The comment now says what `af down` removes, in the order teardown takes it:
the runtime's own resources by its label sweep, the database branch, each
datastore branch, the rolling check's environments, and then a replay of what
has a deleter. It also says what `af down` never removes: goldens, which
`af golden gc` collects, and the service and egress proxy images, which nothing
journals.

Five journal kinds that nothing writes and nothing creates are gone:
`golden.version`, `image`, `zfs.dataset`, `dns.record` and
`webhook.registration`. A declaration can no longer be mistaken for a teardown
that happens. Three more are also written by nothing, and they stay, because
something does create each of those things without journaling it: golden store
uploads for `storage.object`, persona accounts in a provider's sandbox tenant
for `sandbox.object`, and the exploration and runner processes for
`runner.process`. Deleting those kinds would have hidden resources `af down`
cannot reclaim.
