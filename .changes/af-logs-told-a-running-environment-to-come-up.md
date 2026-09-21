# fixed

`af logs` answered every empty result with "Nothing has been written yet" and
told the reader to bring the environment up with `af up`. In a project whose
manifest declares one service, `ledger`, and a `database:` block, `af logs
database` printed exactly that with the environment up and serving. Both lines
were wrong. `database` is not a service, so the command had been asked for
something that does not exist, and `af up` would have changed nothing.

A name the manifest does not declare is now refused with AF-RUN-050, which
names it and lists the services the manifest does declare. The database block,
under the names people type for it, and any declared datastore are refused
with AF-RUN-051, which says it is not a service and points at the output of
the services that talk to it.

An empty result now says which of its causes it is. Nothing running for the
branch is the only one that offers `af up`. A running environment whose
service has written nothing says so and offers nothing, a service the runtime
reports as stopped is named with the runtime's own word for it, and a status
that could not be read offers no remedy at all. The MCP tool
`read_service_logs` makes the same distinction: an environment that is not
running is reported as such, rather than as services that wrote nothing.
