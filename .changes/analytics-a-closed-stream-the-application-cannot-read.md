# added

A closed analytics stream, separate from the engine's event stream and unable
to become a second copy of it. An event whose name is not in the catalog, or
whose payload carries a field the catalog does not declare, is refused and
counted rather than stored and filtered later.

The organization is recorded as a keyed hash rather than as an id, so the
stream counts organizations and follows one through a funnel without being able
to name one. The application role holds INSERT on the stream and no SELECT, so
only the rollup, which runs as the schema owner, ever reads it, and only daily
aggregates come back out.
