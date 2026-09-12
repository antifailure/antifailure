# fixed

A first `af up` could sit silent for twenty five minutes and return nothing.

The first thing `af up` does is obtain the egress sidecar's image, and no
release had ever published one, so every machine compiled it. The compile
starts by pulling a Go base image, and on a network refusing TLS handshakes
that pull stalled. Nothing bounded it and nothing was shown: the build's output
was read to the end before any of it was printed. The measured run was killed
at 25 minutes 32 seconds on one line, and zero of four stacks came up.

A release now publishes the sidecar to `ghcr.io/antifailure/af-proxy` for
`linux/amd64` and `linux/arm64`, and `af up` fetches it before it compiles
anything. A build from a commit no release covers still compiles, and now shows
each step and the base image download as they happen, with a line every fifteen
seconds naming how long it has run and what the daemon last said. Fetching is
bounded at two minutes and compiling at ten, `AF_PROXY_IMAGE_TIMEOUT` raises
both, and a step that runs out stops with `AF-RUN-048` saying what it was
doing. `AF_PROXY_IMAGE` now names your own copy of the image on the local
runtime too, as it already did on Kubernetes.

The compile's base image is pinned by digest. The sidecar's name is a digest of
its source and its Dockerfile text, and while that text named a moving tag, two
machines could hold two different sidecar binaries under one name. Every
sidecar image now also declares the source it was built from, and one that
declares anything else is refused rather than run, whatever it is called.
