# added

A manifest could declare fifty services, pull ClickHouse and Redis and Kafka as
prebuilt images, and start every one of them. Exactly one of those stores held
production's data. `database` was a single struct and it was Postgres: one
golden, one masking pass, one verification scan, one branch. Everything else
came up as an empty container, and nothing in the product said so.

For a stack shaped like an analytics product that is the whole product. The
twin holds masked Postgres metadata and zero events, because the events live in
ClickHouse. Every query path that matters is untested, every migration touching
the analytics store is unrehearsed, and a workflow that reads a chart sees
nothing. The environment is a production twin of one component and a blank slate
for the rest.

The manifest gains `datastores`, a list, and every entry declares a stance:
`golden` for a masked and verified copy, `empty` for a store that is correct to
start with nothing, `derived` to rebuild one from another, `topics_only` for a
broker. **A datastore that declares no stance is refused.** Not every store
should be cloned, so the answer differs per store and only the person writing
the manifest knows it; a default would choose silently, once per manifest, and
choosing silently is how somebody ends up trusting a blank ClickHouse. `empty`
is a legitimate answer and `because` is required with it, because an empty
store somebody decided on and an empty store nobody noticed look identical from
inside a running environment.

`database` does not move and does not change. It normalizes into the entry named
`primary`, so a manifest that declares only `database:` already has the list,
and the component inventory for such a manifest is byte for byte the report it
produced before. There is one code path afterwards rather than two.

`provider.Datastore` is the interface an implementation satisfies, and
`conformance.RunDatastore` is the suite that decides whether one is finished. It
ships with a broken fake and a self test in the same commit: every behaviour is
shown going red against a store built with one thing wrong, and a test fails if
a behaviour is added without a control. Nothing yet refreshes a golden for a
second store or branches one. Those are the next lanes, and until they land
every declared store is reported unmeasured with the reason rather than counted
as either answer.
