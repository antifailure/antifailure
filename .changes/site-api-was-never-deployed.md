# fixed

The waitlist form on antifailure.dev dropped every address for two days.
`deploy.yml` passed no `api_location` and set `skip_api_build`, so every deploy
published a site with no API at all, and the platform removed the managed
function a hand deploy had put there. Every path under `/api` answered `500` and
`www/lib/waitlist.ts` turned that into "Something went wrong on our side", which
is a form telling somebody it is our fault while saying nothing about it
anywhere else. Three addresses reached the table on 27 August, none since. The
deploy now installs and publishes `api/`, sets the runtime the platform needs to
start it, and refuses to finish green unless `GET /api` answers and
`POST /api/waitlist` rejects an invalid address.

`/api` no longer answers a request it has nothing for with a `500` and an empty
body. `GET /api` returns the one endpoint this host serves and says where the
product's API actually is, and anything else under `/api` is a `404` that says
so. The new reference page at `/docs/reference/api` is the longer version of the
same answer.

Every security header the site's own Static Web Apps configuration declares was
being thrown away before publishing. `tools/site/assemble.sh` generated a
configuration file over the top of `www/public/staticwebapp.config.json`, so
antifailure.dev served the platform's default 126 day HSTS instead of the two
year one with preload, no permissions policy, no cross origin opener policy,
none of the cache headers on hashed assets, and answered `/product/crowdi` with
`200` instead of the redirect that file asks for. The generated parts are merged
onto that file now rather than replacing it, and the assembly fails if the
result loses any of them.

The waitlist endpoint had no tests, in a file whose last line said its exports
were there for the tests. It has 24 now, over what counts as an address, when
the rate limiter trips, and what each storage failure answers. A caller that is
over its per address or per IP allowance is refused before the endpoint does any
work rather than after.
