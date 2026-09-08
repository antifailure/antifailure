# fixed

`schemas/manifest.v1.json` closed `runtime.provider` to `local` and
`kubernetes`, so a documentation page could not show the one manifest line a
registered cloud runtime exists to answer. `manifestcheck` refused the page,
and the reason it gave was false:

    runtime.provider is "ecs", which is not one the manifest accepts. The
    engine refuses it with AF-MAN-002. It has to be one of: kubernetes, local

The engine does not refuse it. Nothing validates a manifest against the JSON
Schema at parse time, and the switch in `newRuntime` consults the runtime
registry before it refuses anything, so a build that carries an `ecs` runtime
accepts that manifest and runs the environment. The gate said no about the
right field for a reason a reader would believe and that was not true, which is
worse than saying nothing.

`runtime.provider` now carries no list, the way `datastore.engine` carries
none, and for the reason that field already records: a manifest naming a
runtime this build has no provider for is refused by the provider lookup, by
name, against the runtimes that build actually has, which says more than an
unknown value would. A schema that enumerates providers is a schema somebody
has to edit every time a build registers one, which is the opposite of an
extension point.

`manifestcheck`'s message now reports what the gate actually read, which is the
schema, and stops asserting what a build will do. The enum half of that gate
had no test of its own until now.
