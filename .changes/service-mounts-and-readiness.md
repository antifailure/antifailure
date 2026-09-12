# fixed

A service with no published port was reported ready the moment its container
existed. Readiness for it was one look, taken immediately after the start, so a
process that exited two seconds later on a refused outbound call had already
been counted as up, and `af up` printed a tick beside it. Forty one of the fifty
services in the three published stacks this product was measured against
publish no port, so a stack whose services were all still starting, or all
dying, would have been reported ready. Readiness now has three answers:
**proved**, when a check passed; **unproved**, when the service is running and
there was nothing to check; and **failed**. Only proved counts as ready. A
service with nothing to check is watched for five seconds before it is
reported, every service is looked at again just before `af up` returns, and
`af up`, `af status`, the JSON output and the MCP tools all carry the third
answer instead of rounding it up to ready.

`health_command` is new. It runs inside the container and reports readiness by
exiting zero, which is the only check a service with no port can pass, and the
only one that can tell a Postgres still running its init scripts from one that
has finished. It fails with **AF-RUN-050** when it never passes. On Kubernetes
it becomes an exec readiness probe.

`mounts` is new too, and without it none of the three published stacks could be
written down at all. A `path` copies a file or directory out of the repository
into the container before the process starts; it is never bound to the host, so
a service cannot write back into the working tree, and a path that leaves the
repository, including through a symbolic link, is refused. A `volume` is a
named volume the environment owns, kept across a restart of the service and
removed by `af down`. A missing source fails with **AF-RUN-048** before
anything starts. The Kubernetes runtime refuses mounts by name with
**AF-RUN-049** until a cluster can prove them.
