# added

The marketing site's product analytics goes through the control plane, so a
reader's browser never opens a connection to posthog.com.

That is transport and it is not a boundary. It changes the destination the
browser connects to, not who receives the data: PostHog, Inc. receives every
event, every autocaptured interaction and every session recording either way, in
their US cloud. What it buys is that a content blocker's vendor list does not
match the request, so the measurement is not silently half missing; that the
recorder bundle, the largest and most blockable thing posthog-js fetches,
arrives rather than failing while ingestion looks healthy; and that the reader's
address is dropped in passing. It does not buy the sentence "no third party sees
this", and PostHog is on the published subprocessor list with a row of its own
saying so.

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

One prefix, two upstreams: the script and remote config paths go to the region's
asset host and everything else to its ingestion host. Both are needed, and the
asset half is the one that fails quietly: posthog-js sends every target at a
custom api_host, so a proxy that forwarded only ingestion would leave the
recorder bundle being fetched from the vendor directly, where a blocker kills
replay while ingestion goes on looking healthy.

The legal gate that held the site to "no analytics and no third-party script"
was rewritten rather than deleted. It now keys on whether posthog-js is actually
a dependency and asserts both states: absent, the page must still deny PostHog
by name; present, the denial must be gone and the subprocessor list must carry a
row for it. Two more gates beside it refuse any posthog.com ingestion host in the
site's source, require the configured `api_host` to be the mount this repository
actually serves, and fail if PostHog starts without consulting the measurement
flag the published page promises a reader can switch off.
