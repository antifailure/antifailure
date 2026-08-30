# added

A status page for the hosted control plane, checked from GitHub Actions
rather than from the control plane itself, so an outage of the control plane
cannot also silence the page reporting it. `deploy/status/probe.sh` and
`deploy/status/render.sh` do the work; `.github/workflows/status.yml` runs it
every five minutes and publishes to the `status-data` branch. See
`docs/self-hosting/status-page` for the design and the one manual step
(enabling GitHub Pages on that branch) left for a human.
