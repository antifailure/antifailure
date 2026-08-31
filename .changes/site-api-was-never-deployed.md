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

**This changes live behaviour on antifailure.dev**, which is worth saying
plainly so that a header is not blamed for something unrelated three weeks from
now. Every security header the site's own Static Web Apps configuration
declares was being thrown away before publishing. `tools/site/assemble.sh` generated a
configuration file over the top of `www/public/staticwebapp.config.json`, so
antifailure.dev served the platform's default 126 day HSTS instead of the two
year one with preload, no permissions policy, no cross origin opener policy,
none of the cache headers on hashed assets, and answered `/product/crowdi` with
`200` instead of the redirect that file asks for. The generated parts are merged
onto that file now rather than replacing it, and the assembly fails if the
result loses any of them.

After the merge the site starts sending `cross-origin-opener-policy:
same-origin`, a permissions policy, HSTS with `preload`, and immutable cache
headers on the hashed assets, and `/product/crowdi` starts redirecting. Nothing
in `www` opens a popup, so nothing should notice the opener policy.

Nothing would have told anybody about any of this, so `waitlist.yml` runs every
morning and signs the same address up twice. `alreadyJoined` is read back out of
the table rather than computed, so it can only be true on the second attempt if
the first attempt's write landed. A 200 from an endpoint that writes nowhere is
exactly the state this was in, which is why the check asserts on that field
rather than on the status code.

The waitlist endpoint had no tests, in a file whose last line said its exports
were there for the tests. It has 24 now, over what counts as an address, when
the rate limiter trips, and what each storage failure answers. A caller that is
over its per address or per IP allowance is refused before the endpoint does any
work rather than after.
