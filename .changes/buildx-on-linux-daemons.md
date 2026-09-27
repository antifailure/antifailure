# changed

On Linux Docker daemons that require a BuildKit session, Antifailure now uses
the installed Docker Buildx CLI to build service images. A recorded dogfood run
spent 95 seconds on its first legacy Docker build after the sessionless API
request was refused. Buildx keeps the modern builder's layer cache and supports
modern Dockerfiles without pulling BuildKit's session dependency graph into
the Antifailure binary.

If Buildx is absent, the existing legacy fallback remains. Antifailure checks
that the built image is visible to the daemon running the environment before
accepting it, and retains the managed labels, content-based image tag, and
redacted bounded build log.
