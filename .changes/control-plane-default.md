# fixed

`af login` with no arguments now signs in to `https://app.antifailure.dev`. It
had its own spelling of the hosted instance and it had drifted to
`app.dev.antifailure.dev`, which is the staging deployment, so a plain `af
login` signed a terminal in to staging while everything that sends events went
to production.

`af env pull` now uses the credential `af login` stored when no
`AF_CONTROL_PLANE_URL` is set. It resolved the origin before the default was
filled in, so somebody holding a perfectly good credential was told `AF-CPL-001
No control plane token is configured` and sent off to create a second one.
