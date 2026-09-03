# fixed

A build the Docker daemon refused told you to read a log that does not exist.

`af up` on a service whose Dockerfile the build context does not carry printed
AF-BLD-001, "The build for service api failed after 0s", and next to it "Read
the build log above; the first error line names the step that failed." There
was no build log above. The daemon rejected the request before opening a
stream, so nothing was built and nothing was printed, and the only sentence
that identified the problem, "Cannot locate specified Dockerfile:
deploy/docker/control-plane.Dockerfile", was reachable only by running the
command again with `-v`.

That path is now AF-BLD-006, which carries the daemon's own sentence in the
message where it prints by default, and whose next step says nothing was built
and points at build.context and .dockerignore. AF-BLD-001 keeps the case it was
right for, a build that ran, produced output and lost, where the log really is
above.
