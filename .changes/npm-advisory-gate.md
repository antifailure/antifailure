# security

`npm audit` now runs against every lockfile in the repository, beside
govulncheck in `security.yml`, on every pull request and every morning.
Advisories are held to `.npmaudit.yaml` under the same three rules as
`.govulncheck.yaml`: an advisory with no written decision fails, so does a
decision past its expiry, and so does one that matches nothing.

govulncheck reads Go modules and stops there, and every `npm ci` in CI passes
`--no-audit`, so the seven lockfiles present when this gate landed, one of which
builds the control plane, had no advisory check at all. They were clean with dev
dependencies included. The runner was reported as uncovered then; it now has
the repository's eighth lockfile and is audited too.
