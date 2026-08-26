# Contributing to Antifailure

Thank you for being here. This document is short on ceremony and long on the
few things that actually matter for a project that touches production data.

## The one command

```
just gate
```

That runs every quality gate the CI runs, in the same order, with the same
tool versions. If it is green locally it is green in CI. If it is red, the
report under `.gate-reports/` names the file and the line.

## Getting set up

```
git clone https://github.com/antifailure/antifailure
cd antifailure
just setup     # installs pinned tool versions into .tools/
just build     # builds the af binary into bin/af
just test      # unit and property tests
```

You need Go 1.25, Node 22 or newer with pnpm 10, and a working Docker daemon.
`just setup` reports anything missing with the command that installs it.
`af doctor` does the same for a machine that only runs Antifailure.

## Sign your commits off

Every commit needs a Developer Certificate of Origin sign-off. There is no CLA.

```
git commit -s -m "engine: fix chunk boundary on partitioned tables"
```

By signing off you state that you wrote the patch or otherwise have the right
to submit it under this repository's license: MIT everywhere except `ee/`,
which is under the Antifailure Enterprise License. The full DCO text is at
https://developercertificate.org. There is deliberately no CLA.

## Branches and pull requests

Branch names are `area/short-description`, for example `masking/fpe-tweak` or
`docs/neon-provider`. Open one pull request per logical change.

The pull request template asks for four things and CI checks that they are
filled in:

1. What changed.
2. Which failure paths are covered, named by test.
3. Security considerations, or an explicit "none, and here is why".
4. Evidence: pasted command output, not a paraphrase.

Every pull request adds a changelog fragment under `.changes/` with the
category and one sentence. A missing fragment fails a gate. If the change is
genuinely invisible to users, `.changes/<pr>.internal.md` records that.

## Writing tests

Tests are not optional and they are not decorative. The rule we hold ourselves
to: every behavior has a test that would fail if the behavior broke, and every
failure path listed in a design note has a test that exercises it.

* Unit tests live next to the code.
* Pure transformations get property tests (`pgregory.net/rapid` in Go,
  `fast-check` in TypeScript).
* Every parser gets a fuzz target.
* Every provider implementation runs the shared conformance suite. A provider
  that skips a behavior must skip it explicitly, naming the missing capability.
* No real clock, no real randomness, no real network, no sleeps. Time comes
  from an injected `clock.Clock`; the fakes in `engine/internal/testutil/fakes`
  cover every external dependency and can inject faults.

New tests run twenty times in CI before a pull request can merge. A test that
fails once out of twenty is a bug in the test or the code, not noise.

## Writing a provider

Providers are the main extension point and they are meant to be written by
people outside this repository. Start at
`docs/src/content/docs/contributing/provider-authoring.md`. The short version:

```go
import "github.com/antifailure/antifailure/engine/conformance"

func TestMyProvider(t *testing.T) {
    conformance.RunDatabase(t, func(t *testing.T) provider.Database {
        return myprovider.New(...)
    }, conformance.Options{})
}
```

The suite tells you exactly what conformant means; its README is generated
from the subtest names, so it can never drift from what actually runs.

## Style

Go is `gofmt` and `goimports` clean with the linter set in `.golangci.yml`, all
rules as errors. TypeScript is strict, no `any`, formatted by Biome. Prose in
comments, docs, commit messages, and user-facing strings does not use em dashes
or double hyphens as punctuation. Error messages are written in the second
person, name the thing that failed, and say what to do next.

Every user-facing error carries a code from
`engine/internal/errors/catalog.yaml`. Adding a code without a catalog entry
fails the build, and so does a catalog entry that nothing returns.

## Architecture decisions

A change to a public interface needs an ADR in `docs/adr/`, using
`docs/adr/0000-template.md`. Keep it to one page.

## Security

Do not open a public issue for a security problem. `SECURITY.md` has the
address and our response targets.

Never commit a secret, a real customer record, or a screenshot containing
either. Test fixtures use synthetic data from seeded generators. Fake
credentials carry the `AF_FAKE_` prefix so scanners can tell them apart from
real ones, and the fakes refuse to start with a value that lacks it.

## Getting help

Open a discussion for a question, an issue for a bug, and an RFC pull request
for a design. We would rather answer a question early than review a large
change built on a wrong assumption.
