# fixed

The Kubernetes runtime could not copy any public image into a local cluster.

A cluster's nodes cannot see the Docker daemon that built or pulled an image,
so the runtime copies it in. That copy is `kind load docker-image`, which
exports the image and imports it into the node's store with `--all-platforms`.

A Docker installation whose image store keeps OCI indexes, which is the default
once the containerd image store is on, holds every image pulled from a registry
as a manifest list: one entry per platform, with the layers for the other
platforms absent because nothing ever needed them. The import then refuses the
whole thing, naming a content digest it cannot find, and the digest belongs to
a platform this machine was never going to run.

It is not intermittent and it is not one image. On such a machine every public
image failed, which meant the Kubernetes runtime conformance suite could not
run a single behavior: it did not skip, it reported a hard failure in its setup
before the first environment existed, in words that read as the runtime being
broken. Thirty four behaviors that describe what a runtime has to do, including
every containment behavior, went unmeasured for anybody whose Docker is
configured that way.

A failed copy now falls back to an archive holding one platform, which is the
form a node can import. The platform is `linux` and this machine's
architecture, because a container image is a Linux image and a local cluster's
nodes are containers on this same machine.
