# added

The marketing site's product analytics goes through the control plane, so a
reader's browser never opens a connection to posthog.com.

The site is a static export on Azure Static Web Apps. It has no server at
runtime and its route rules cannot rewrite to an external host, so the site
itself cannot forward anything and the alternative was a browser talking
straight to a vendor. `AF_POSTHOG_REGION` now mounts a proxy at `/ph` on the
control plane, which is the one process this product already runs on its own
hostname. It is same site rather than same origin, which is why every route
answers a CORS preflight and echoes one origin from `AF_SITE_ORIGIN`.

Three separate things stop it being a general forwarder, and none replaces the
others. The upstream host comes from a closed set of two regions, so no request,
header or setting can name a different one. The paths that reach PostHog are an
allowlist, so a path that is not on it is not a route and is answered 404 rather
than forwarded. A redirect from the upstream is refused rather than followed,
which is what stops the vendor from steering this process at somebody else's
server.

Nothing of the browser's crosses except `content-type`: no cookie, no
authorization header, and not the reader's address. PostHog therefore receives
no visitor IP at all. The cost is that its geolocation properties describe the
deployment rather than the reader, which is the trade taken on purpose, because
forwarding an address is the one direction that cannot be undone afterwards.

The allowlist holds both capture endpoints. posthog-js posts to `/e/` by default
and overrides it from the remote config, which for this project returns
`/i/v0/e/`. A proxy built from the documented default alone would have forwarded
the flag call, served the script, passed every check anybody ran, and lost every
single event.

The legal gate that held the site to "no analytics and no third-party script"
was rewritten rather than deleted. It now keys on whether posthog-js is actually
a dependency and asserts both states: absent, the page must still deny PostHog
by name; present, the denial must be gone and the subprocessor list must carry a
row for it. Two more gates beside it refuse any posthog.com ingestion host in the
site's source, require the configured `api_host` to be the mount this repository
actually serves, and fail if PostHog starts without consulting the measurement
flag the published page promises a reader can switch off.
