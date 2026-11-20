# security

Eighteen published advisories against packages this repository ships, and one
more found while fixing them, are closed: `next` 16.4.6 in the site, the docs
and the console and 16.4.5 in the Next.js example; `hono` and
`@hono/node-server` in the control plane and its enterprise edition; `astro`
7.4.13 in the docs; `fast-xml-parser` 5.12.1 in the site API. The one that
matters most for a running control plane is `@hono/node-server` reading a
backslash in a request path as a separator, which let a path slip past
path-based access control in front of it.
