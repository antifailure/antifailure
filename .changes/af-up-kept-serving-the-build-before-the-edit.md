# fixed

A second `af up` after an edit kept the previous build serving and printed ready.

The local runtime reused any running container with the right name. The image
reference carries a digest of the build context, so an edited tree names a new
image, but the container from the old one was still running, so it was
reported ready and the new image was never started. A fix rehearsed that way
was rehearsed against the build it was fixing, and passed. The reused
container was also returned early, with no address and without the ingress
being looked up, so a repeat `af up` printed a service with no URL.

`af up` now compares the running container's image to the one the tree
builds, by image ID so that `--rebuild` under an unchanged tag counts, replaces
it when they differ and says so in the progress line, and takes the same path
as a fresh container from the ingress onwards, so the address is always
printed.
