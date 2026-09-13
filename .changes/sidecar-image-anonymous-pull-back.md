# fixed

The release's check that the published egress sidecar image can be fetched
could not fail for the reason that mattered. Every customer's `af up` pulls
`ghcr.io/antifailure/af-proxy` with no credentials, and a package GitHub
creates is private on its first publish. The release pulled it back while
still logged in to `ghcr.io` from the push, so it passed against a package no
customer could read, and every first `af up` would have fallen back to
compiling the sidecar for up to 25 minutes with nothing going red.

The pull back now logs out and runs under an empty Docker configuration, the
same as a customer, and so does the read of the manifest list. When the
package is private it fails with the one time remedy: make the package public
on its settings page, then re-run the failed job. The release is not published
until that happens. `Cutting a release` now says so, and says to approve the
production deployment only after the release's `publish` job reads success.
