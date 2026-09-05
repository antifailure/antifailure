# fixed

Every form and every beacon on the marketing site was refused for anybody who
arrived on the `www` hostname.

`antifailure.dev` and `www.antifailure.dev` are two custom domains on one Azure
Static Web App. Both are Ready, both serve every page, and neither redirects to
the other, because a Static Web Apps route rule matches on a PATH and its
configuration schema has no hostname condition at all. `AF_SITE_ORIGIN` held one
origin, the apex. So a visitor who typed `www`, followed an old link, or was
handed the `www` page by a search engine sent `origin: https://www.antifailure.dev`
and the control plane answered 403 to every cross origin call the page makes:
the analytics beacon silently, the enterprise contact form as "Could not reach
the server", and the careers application form on something somebody had just
filled in.

`AF_SITE_ORIGIN` and the `site_origin` Terraform variable now take a comma
separated list, and every route that answers a cross origin browser compares
through one shared function rather than four copies of the same rule. A single
origin with no comma still parses to a list of one, so an installation that
already sets it needs no change, and the variable keeps the name and the type
`docs/reference/stability.md` promised. There is still no value meaning "any
origin", the comparison is still exact equality on the whole origin rather than a
suffix test, and a response still carries the one origin that matched rather than
the list.

Two new checks, because nothing could have caught this: the apex worked, so
every check anybody ran was green while a whole hostname was broken. `just
origincheck` runs on every branch and compares the hostnames the site is served
on against the origins each control plane is configured with, in both
directions. `just check-origins` runs after a publish, asks Azure which custom
domains are actually bound rather than trusting a list in the repository, and
asks the deployed control plane to answer a real preflight from each one. It
exits saying NOT CHECKED rather than passing when it cannot reach Azure.
