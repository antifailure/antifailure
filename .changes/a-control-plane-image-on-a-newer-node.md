# changed

The self hosted control plane image now runs on Node 26.

All three stages of deploy/docker/control-plane.Dockerfile move together, the
dependency install, the console build and the runtime, so an operator who pulls
the new tag gets one runtime rather than a mixture of two.

The three examples move with it, because an example is the first Dockerfile
most people copy. The Next.js example builds on node:26-alpine, the Go example
on golang:1.27-alpine over alpine:3.24, and the Django example on
python:3.14-slim.

One of the two examples that run a migration changes the psql it runs, and it
is worth saying because both of their comments used to name the old numbers.
The Go example's runtime moves from Alpine 3.20 to 3.24, which carries psql 18
under the unversioned postgresql-client package where 3.20 carried psql 16. A
client newer than the server is the direction libpq supports, so that migration
behaves as it did.

The Next.js example does not move. Its old base, node:22-alpine, is itself
built on Alpine 3.24 and was already giving it psql 18, so only the comment
there was out of date.
