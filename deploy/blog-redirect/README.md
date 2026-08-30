# blog.antifailure.dev

A Static Web App whose only job is a 301 to `https://antifailure.dev/blog`.

## Why this exists rather than a blog

The writing lives at `antifailure.dev/blog`, not on this subdomain, and that is
deliberate. A subdomain is commonly treated as a separate site, so a blog on
one accumulates authority that never reaches the product pages and starts from
nothing itself. A subfolder shares the domain outright: the documentation, the
product pages and the writing compound into one thing. For a domain registered
this year, that difference is most of the available upside.

The subdomain still resolves, because people type it and because it may already
have been shared. It sends one permanent redirect, so every link ends up
pointing at one canonical URL instead of splitting between two.

`x-robots-tag: noindex, follow` is set so a crawler that reaches this host does
not index the redirect stub itself while still following it through.

## Why a second Static Web App

`af-site` is on the Free tier, which allows two custom domains, and both are
used by `antifailure.dev` and `www.antifailure.dev`. A second Free app costs
nothing and keeps the redirect isolated from the site's own deploy.

## Deploying

    az staticwebapp secrets list -n af-blog -g af-web \
      --query "properties.apiKey" -o tsv

Then, from this directory:

    npx --yes @azure/static-web-apps-cli deploy . \
      --deployment-token "$TOKEN" --env production

There is nothing to build. The two files are the whole application.
