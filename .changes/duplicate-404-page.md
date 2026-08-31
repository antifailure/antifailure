# fixed

The site no longer publishes a second copy of its 404 page. Next writes the
not-found route twice under `output: "export"`: `/404.html`, which is the one
the host serves for a mistyped URL, and `/_not-found.html`, which nothing
routes to, nothing links to, and no sitemap lists. The second was answering
200 with 84KB at an address only a crawler guessing at framework internals
would find. The assembly drops it, verified in a browser rather than assumed:
with the files gone the 404 page renders and a client-side navigation off it
completes, with no request for the removed address and no console error.

`just links` now assembles the site with `tools/site/assemble.sh` instead of
its own copy of that logic, so the checks the assembly makes can fail on a
developer's machine rather than first in CI. The link count is unchanged by
the switch.
