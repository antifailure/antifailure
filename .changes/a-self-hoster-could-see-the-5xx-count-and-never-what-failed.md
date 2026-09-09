# added

A self hosted control plane could see its 5xx count go up and never see what
had failed.

Both error handlers already caught every unexpected failure and both already
wrote a line naming the error class, the driver's code, the method and the
route. That line went to standard output. On the hosted control plane something
ships standard output somewhere searchable; on a self hosted one it is a line in
`docker logs`, with no count, no first seen and no grouping. The one number an
operator could reach, `af_http_requests_total{status_class="5xx"}`, says how
many and never says what. The Logs & Error Explorer said in its own header that
this product records no fingerprint and no occurrence counter, which was true of
the engine's exceptions and needed nothing from anybody to stop being true of
the control plane's own.

So the control plane now groups its own failures into a table it writes to its
own Postgres, and the operator portal reads them and updates itself every ten
seconds without a refresh. A row is a group rather than an occurrence, so the
table's size is a function of the code and not of how badly the day is going: a
self hoster's disk filling on the day their control plane starts failing would
be an incident caused by the feature meant to help with incidents. The store
holds at most five hundred groups and says on the page when it has reached that,
because a store that quietly under-reports is worse than none.

It keeps no message, no stack and no payload, for the reason already written on
the handler above it: a query failure from this stack renders as the whole
statement with its parameters after it, so a message here can carry a tenant's
data. It keeps no organization either, so it cannot say how many tenants a
failure touched, and the page says that in words rather than showing a zero.

Nothing records a stack trace from a customer's run. That still needs the engine
to report one.
