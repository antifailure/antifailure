# www.antifailure.dev and the two certificates

Readers reported `ERR_SSL_PROTOCOL_ERROR` and "the connection for this site is
not secure" on `https://www.antifailure.dev`, and asked whether the site was
going down. This is what was measured, what turned out to be true, and what is
still owed to a human with access to the Azure portal.

Measured against the live site on 2026-09-01, not inferred from the source.

## What is true

| Fact | Evidence |
| --- | --- |
| Apex and www both answer 200 | `curl` exit 0, HTTP 200 on both |
| They are one Static Web App, not two | identical `etag: "65684348"` and `content-length: 356669` on both hostnames |
| www does not redirect | it serves the page itself, status 200 |
| Two separate certificates | apex SAN is `DNS:antifailure.dev` alone, www SAN is `DNS:www.antifailure.dev` alone |
| Both currently valid | GeoTrust TLS RSA CA G1, `notBefore=Aug 27 2026`, `notAfter=Feb 27 2027` |
| Both were issued five days before the reports | `notBefore=Aug 27 00:00:00 2026 GMT` on both |
| The canonical host is the apex | 25 occurrences of `https://antifailure.dev` in the site, 0 of `https://www.antifailure.dev` |

Two certificates with disjoint SANs means www is a genuinely separate custom
domain with its own Azure managed certificate, provisioned and renewed on its
own schedule. That is a second thing that can lapse, and until now nothing
anywhere would have noticed if it did. Anyone typing the apex would have seen a
completely healthy site.

## The HSTS header is not the mechanism

The header is real and it is set in `www/public/staticwebapp.config.json`,
which is the main site configuration and therefore applies to both hostnames:

    "strict-transport-security": "max-age=63072000; includeSubDomains; preload"

Confirmed served on the apex and on www in production. But it is not what makes
www fail hard, and removing `includeSubDomains` would change nothing, because
the domain is preloaded by its top level domain rather than by anything we do:

    curl "https://hstspreload.org/api/v2/status?domain=antifailure.dev"
    {"name":"antifailure.dev","status":"preloaded","bulk":false,"preloadedDomain":"dev"}

`preloadedDomain` is `dev`, not `antifailure.dev`. The whole `.dev` top level
domain sits on the browsers' built in HSTS preload list with
`includeSubDomains`. The rule is compiled into the browser binary, it applies on
a cold profile with no prior visit, and no header of ours turns it on or off.
`bulk: false` says this specific domain was never individually submitted, so the
`preload` directive in our header is an inert opt in signal rather than a false
claim: the outcome it describes is already true, by a route we do not control
and cannot reverse.

Two further corrections to how this was first reasoned about:

- **HSTS never caches a failure.** It records "use HTTPS for this host". It
  stores no certificate or handshake state. So a reader who hit a broken www
  does not stay broken for the two year `max-age`; they recover on their next
  connection, as soon as the server does. There is no sticky bad state to wait
  out.
- **What preload actually contributes** is the removal of the click through
  interstitial. A certificate or handshake fault on any `*.antifailure.dev` name
  is a hard `ERR_SSL_PROTOCOL_ERROR` with no "proceed anyway" for the reader.
  There is no degraded mode. So the reported symptom is consistent with a real,
  transient TLS fault on www, and not with a browser holding on to old state.

## A redirect would not have removed the TLS surface

This was the plan and it does not survive contact with how a redirect is
delivered. A 301 from `https://www.antifailure.dev` to `https://antifailure.dev`
is an HTTP response, and it travels over a TLS connection to www. The handshake
has to succeed before the redirect can be sent. If www's certificate is broken,
the reader gets `ERR_SSL_PROTOCOL_ERROR` and never sees the 301.

Redirecting www is still worth doing for canonicalization, because two
hostnames both answering 200 for every page is a duplicate content defect. It is
just not a fix for the failure that was reported. The only way to genuinely
remove the www TLS surface is to remove the www hostname from DNS altogether,
which converts a TLS error into a "site cannot be reached" error for everyone
who types www. That is not obviously better for a reader.

The same reasoning rules out a client side redirect emitted from the pages: the
script cannot run until the page loads, and the page cannot load until the
handshake succeeds.

## The redirect cannot be done in this repository

`staticwebapp.config.json` has no hostname condition. From the published schema
at `https://www.schemastore.org/staticwebapp.config.json`, a route object
accepts exactly `allowedRoles`, `headers`, `methods`, `redirect`, `rewrite`,
`route`, `statusCode`, with `additionalProperties: false`, and `route` matches a
URL path pattern. The only host shaped key anywhere in the schema is
`forwardingGateway.allowedForwardedHosts`, which is a Front Door allowlist and
not a redirect.

Both hostnames are served by the same app and therefore the same configuration
file, so any route added there fires identically on both and cannot tell them
apart. A `"route": "/*"` redirect to the apex would redirect the apex to itself,
which is an infinite loop on the canonical host. No rule was added, because a
rule that looks like a fix and does nothing is worse than an absence.

## What a human with Azure access needs to do

None of this was attempted. It needs portal or CLI access that this work did
not have, and it changes live infrastructure.

1. **Check why both certificates were reissued on 2026-08-27**, five days
   before the reports. In the portal: Static Web App `af-site`, resource group
   `af-web`, Custom domains. Confirm `www.antifailure.dev` shows validated and
   its certificate healthy rather than a stale or partially provisioned state.

       az staticwebapp hostname list -n af-site -g af-web -o table

   A custom domain that is mid provisioning serves handshake failures, which is
   exactly the reported symptom, and it resolves itself without ever appearing
   in a log anyone reads.

2. **Decide about the redirect on its own merits**, which are canonicalization
   rather than TLS. Static Web Apps cannot do it. The two real options are an
   Azure Front Door profile in front of the app with a rules engine rule that
   301s on the www host, which costs money and is a substantial change, or a URL
   forwarding rule at the registrar if it supports one over HTTPS, which moves
   the www certificate off Azure and onto the registrar. Doing nothing is
   defensible: every page already carries `rel="canonical"` pointing at the
   apex, so search engines are already deduplicating correctly.

3. **Do not remove the www custom domain** as a shortcut to "one certificate".
   Because `.dev` is preloaded, a www name that resolves without a valid
   certificate is a hard failure, and a www name that does not resolve is a
   different hard failure. Keeping it as a healthy managed domain is the least
   bad state until option 2 is decided.

## What changed here

`tools/site/check-tls.sh`, run by `just check-tls` and by the deploy workflow
after a publish. For every hostname it asserts a verified handshake, that the
served certificate actually covers the name it was served for, and that it is
not inside the renewal window. It was proved to fail on all three fault classes
before it was trusted: a forced expiry window, a host serving a certificate for
another name, and an expired certificate, each exiting 1, against exit 0 on the
real hostnames.

## What could not be verified

The reader's failure was never reproduced. Every request from this machine
succeeded on both hostnames, over TLS 1.2 and TLS 1.3. That is a limit of one
vantage point and not a refutation of the report: a handshake that works from
here proves one edge node answered one client correctly, and says nothing about
the node a reader in another region is routed to. If the fault was a
provisioning window on 2026-08-27 it has already closed, and the check added
here is what would catch the next one.
