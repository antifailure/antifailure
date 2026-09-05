# Contributing to Antifailure

Thank you for being here. This document is short on ceremony and long on the
few things that actually matter for a project that touches production data.

## The one command

```
just gate
```

That runs every quality gate CI runs, in the same order, with the same tool
versions. If it is green locally it is green in CI. If it is red, it prints
which gates failed and the last lines of each, and the full output of every
gate is under `.gate-reports/`.

That promise is checked rather than trusted: `tools/gatecheck` compares the
`gate` recipe against every workflow that runs on a pull request and fails the
build when they disagree, because a promise like this rots silently. It has
already caught two gates CI ran that the justfile did not.

**When a gate you did not touch goes red, check a clean copy of `main` before
you look at your own diff.** Three people arrived at this independently in one
evening and one of them lost two hours to it. A branch that has been open for a
while is being tested against its merge with `main`, so a failure can belong to
somebody else's commit entirely, and it will still be printed underneath your
change.

```
git worktree add /tmp/main-check origin/main && cd /tmp/main-check && just gate
```

The same move catches the opposite mistake, which is a gate that passes for you
and fails everywhere else. Your machine is not clean: it has the directory a
tool created, the image docker already pulled, the binary you built. All three
have produced a green local run and a red CI run here. `git clone --depth 1
file://$PWD /tmp/fresh` is a clean machine for the length of one command.

## Getting set up

```
git clone https://github.com/antifailure/antifailure
cd antifailure
just hooks     # turns on the commit hooks; see below
just setup     # checks your toolchain and names what is missing
just db        # starts the Postgres the control plane suites need
just build     # builds the af binary into bin/af
just test      # unit and property tests
```

`just` itself is the only thing you need before `just setup` can help you:
`brew install just`, or see https://just.systems. `just` with no argument
lists every recipe.

`just hooks` is worth the ten seconds. It adds the sign-off trailer for you
and refuses a commit authored by an address that is known to belong to somebody
else's GitHub account, which is a mistake this repository has actually made.
CI checks both, so the hooks only decide whether you find out before the push
or after it.

You need Go 1.26, Node 24 or newer, npm, and a working Docker daemon. The
documentation gates need two more: `vale` for prose style and `lychee` for
links, both `brew install`.
`just setup` reports anything missing with the command that installs it, and
it also checks the two things about your clone that CI enforces: that the
hooks are on and that your commit identity is set.

`af doctor` does the same for a machine that only runs Antifailure rather than
developing it.

## Sign your commits off

Every commit needs a Developer Certificate of Origin sign-off. There is no CLA.

```
git commit -s -m "engine: fix chunk boundary on partitioned tables"
```

By signing off you state that you wrote the patch or otherwise have the right
to submit it under this repository's license: MIT everywhere except `ee/`,
which is under the Antifailure Enterprise License. The full DCO text is at
https://developercertificate.org. There is deliberately no CLA.

## The sign-off is the only trailer

A commit here carries a `Signed-off-by` and nothing else. No `Co-Authored-By`,
no `Generated-with`, no session identifier, and no link back to whatever tool
was open at the time, in the commit message or in the pull request description.

The reason is not modesty. A commit message here exists to explain the failure
the change fixes, to somebody reading `git log` in two years, and a trailer
naming a tool run is neither part of that explanation nor true for long: the
session it points at is gone and the link outlives it. The history of this
repository is public and permanent, and the remedy for a trailer that reached
`main` is rewriting `main`, which breaks every clone.

`just attribution` is the check, it runs in `just gate` and on every pull
request, and it reads three surfaces: the message of every commit your branch
adds, the pull request's own description, and the body of the squash commit
`just merge` is about to write. It names which rule it matched and quotes the
line, so a refusal says what to delete.

If you use an assistant that appends a footer, turn that off rather than
stripping it by hand each time. The two shapes it usually takes, a
`Co-Authored-By:` trailer and a `Generated with ...` line, are both refused, and
so is the bare session URL that arrives with no key in front of it.

## Branches and pull requests

Branch names are `area/short-description`, for example `masking/fpe-tweak` or
`docs/neon-provider`. Open one pull request per logical change.

The pull request template asks for four things and CI checks that they are
filled in:

1. What changed.
2. Which failure paths are covered, named by test.
3. Security considerations, or an explicit "none, and here is why".
4. Evidence: pasted command output, not a paraphrase.

### Read the diff, not the message

Review the whole diff of a pull request, including the files the message does
not mention. No gate catches a commit whose message describes one thing and
whose diff does another, and it is worth knowing why rather than waiting for
somebody to build one.

`ff893073` is the case. Its subject and its entire message are about a Postgres
test port. It also removed 317 lines across twelve unrelated files: a runbook
step, four Terraform resources, a role assignment, a comment explaining a
failure, and a resource group from a document that counts them. Nine pull
requests were needed to put it back, eight days later, and every one of them
started with a person reading the diff.

The reason no consistency check sees this class is that it was a COHERENT
revert. It moved every side of every coupling at once: the runbook step and the
module comment that justified it, the step numbering and every reference to it,
the count of resource groups and the group being counted. Internal consistency
is exactly what a clean revert preserves, so a gate that compares two artifacts
against each other stays green over it by construction. That is not a gap
somebody can close with a better cross reference check, and building one is
time spent on a check that cannot fire.

The two shapes that ARE detectable are both useless as gates here. A commit
touching many files fires on every honest refactor, and a commit touching code
and documentation together fires on the correct habit this guide asks for. A
gate muted for noise reads as coverage while catching nothing, which is worse
than no gate.

So this one is a review practice and it stays one. If the message accounts for
part of the diff, ask about the rest before approving.

### Merging

Merge with `just merge <number>` and not with `gh pr merge` by hand.

```
just merge 231
just merge 231 -dry-run       # check everything and merge nothing
just merge 231 -check-fields  # ask the API whether its field names still exist
just merge 231 -confirm-only  # prove a merged commit carries the sign-off
```

The reason is a deployment outage rather than a style preference. Six pull
requests were merged on 2026-09-05 with `gh pr merge --squash --body ""`. A
squash commit made that way carries no `Signed-off-by` trailer, and the trailer
is a required context, so `commits are attributed to their author` failed on
main. `.github/workflows/cd.yml` has a gate job that waits for CI on the same
commit; it read that failure and skipped its build, staging and production
jobs, so staging stayed six merges behind at `cb3f30f1` while every one of
those changes was on main. It moved again only when the next merge,
`597b3819`, carried the trailer.

No hook can prevent this. `.githooks/prepare-commit-msg` writes the trailer
onto local commits and a squash commit is created on GitHub's side, so the
only thing that runs at the moment of the merge is whatever the person pressing
the button typed. `just merge` is that thing:

* it requires all nine of main's required contexts to report the literal word
  `success` on the pull request's exact head sha, and confirms that list
  against branch protection rather than trusting the copy in its own source;
* it reads `mergeStateStatus` and never `mergeable`, which only says whether
  the branch conflicts;
* it says which red checks it is ignoring, because `Antifailure` and
  `dogfood, against the control plane` are not required and reading a red mark
  on either as a block has cost this project hours;
* it builds the sign-off from your own `git config user.name` and
  `user.email`, which is clause (c) of the Developer Certificate of Origin:
  somebody relaying another person's contribution signs as themselves and
  leaves the authorship alone. It refuses a body carrying anybody else's
  sign-off rather than adding a second one;
* it never passes `--delete-branch`, which deletes the branch even when the
  merge is refused and so closes the pull request. It prints the deletion
  command for you to run afterwards;
* and it reads the commit back off the remote afterwards and fails if the
  trailer is not there, because a tool that intends to sign and cannot prove it
  did is the defect it was written to prevent. `-confirm-only` runs that one
  check on its own against any merged pull request, so the code that decides
  whether a commit is signed can be exercised on a real commit without merging
  one to exercise it.

The first version of this command asked `gh pr view` for a field called
`merged`. There is no such field, and all 25 of its tests passed anyway,
because every one of them answered from a fixture the suite itself had written.
The first real invocation died on its first call. So `-check-fields` reads the
other side of the boundary: it runs the same argument lists the merge path
runs, against a real pull request, and reports a name the API does not have or
a key a real response does not carry. It runs on every pull request in
`.github/workflows/ci.yml` as "the merge command still fits the API", and it
names what it could not check rather than passing over it.

## The changelog

Every change to something a user can see adds a fragment under `.changes/`.
The first line is one of `# added`, `# fixed`, `# changed` or `# security`,
and the rest is prose: what changed, and what it means for somebody using it.
The published changelog at https://antifailure.dev/changelog is built from
these files and from nothing else, so a change with no fragment is a change
nobody outside this repository hears about.

If the change is real but genuinely invisible to a user, name it
`.changes/<slug>.internal.md`. Those are kept and never published.

`just changecheck` is the gate, and it runs the range CI runs, so you find out
locally rather than twenty minutes later. It asks whether anything a user
could NOTICE changed, not whether anything changed: a test, a fixture, a
documentation page, a gate under `tools/`, a workflow, a lock file and a
generated file all pass in silence. `tools/changecheck/classify.go` holds the
list, and every exemption in it names the change on main that taught it.

For the genuine exception, say so in the commit and say why:

```
git commit -s --trailer "Changelog-None: a lint fix with no behaviour change"
```

The reason is the point, and it is why this is a trailer rather than a label:
a reviewer reads it in the diff, `git log --grep` finds it later, and the
local gate can see it. A trailer with nothing after it is refused.

This paragraph promised a gate from the first week of the project and there
was none until 2026-08-31. The sign-off rule above went the same way, and by
the time anybody counted, 65 of the first 80 commits carried no trailer.

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
  from an injected `clock.Clock`.
* `engine/internal/testutil/fakes` holds doubles for the two provider
  interfaces, and each comes with fault injection. `fakes.Break` and
  `fakes.BreakRuntime` take something that works and return something that
  violates exactly one guarantee, so you can point a suite at it and find out
  whether the suite could have failed. That matters more than the doubles
  themselves: a suite nobody has watched go red is a list of assertions that
  might all be vacuous, and assertions go vacuous quietly.
  It covers `provider.Database` and `provider.Runtime`. Time is NOT here: the
  project already has `clock.Fake` in `engine/internal/clock`, which implements
  the real `clock.Clock` interface, and a second clock in this package would be
  one that cannot be injected where the code expects one. It does not
  yet cover the object store, the control plane client or the secret sources,
  and this sentence will be wrong the moment somebody adds one, so add it here
  too.

Tests run with the race detector in CI, always. A test that fails once in
twenty runs is a bug in the test or the code, not noise, so re-running until
green is not a fix; find the ordering that broke it.

One local caveat that is not flakiness: several tests assert a wall clock
budget, and those measure the machine as much as the code. On a loaded machine
they fail while nothing is wrong. `just gate` says so when the load average is
above one and a half times the core count, and the remedy is to re-run the
failure on its own before believing it.

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

Go is `gofmt` and `goimports` clean. `just lint` runs the linter set in
`.golangci.yml`, which is chosen for signal rather than length: every rule in
it catches a bug that has actually shipped somewhere, and `unused` in
particular catches the failure this repository keeps producing, where something
is declared, documented, and never called, and reads as a working feature.

It is not a merge gate yet, and saying so is the point of this sentence. There
are 31 findings that predate the config, spread across packages several people
are editing at once, and turning the gate on before they are cleared would fail
every branch for something none of them did. `gofmt` and `go vet` are gates
today. If you are clearing findings in a package you own, that is welcome, and
the gate goes on when the count reaches zero. TypeScript is strict, no `any`, formatted by Biome. Prose in
comments, docs, commit messages, and user-facing strings does not use em dashes
or double hyphens as punctuation. Error messages are written in the second
person, name the thing that failed, and say what to do next.

`tools/prosecheck` is the gate for that rule, and it does not reach everywhere
the rule applies, so it is worth knowing what is actually holding you. It reads
Markdown under `docs`, `examples`, `.changes`, `www`, `console` and the
repository root, and every file with a `ts`, `tsx` or `mjs` extension under
`www`, `console` and `docs`. It does not read Go, SQL, Terraform, YAML, or the
api's and runner's TypeScript. A defect in a Go comment or a Terraform comment
is yours to catch, and the engine carries four `require.NotContains` assertions
of its own because nothing else checks the strings the CLI prints.

The double hyphen half of the rule cannot be widened past those trees, and that
is a limit rather than something nobody has got to yet. `--` is the SQL line
comment and the POSIX end of options marker, so outside prose it is syntax far
more often than punctuation: of the 1242 lines outside the gate that match the
rule, 1120 are SQL comments. An em dash collides with no language here, which
is why the two halves of one rule do not reach equally far. The measurement and
the reasoning are in `tools/prosecheck/main.go`.

Every user-facing error carries a code from
`engine/internal/errors/catalog.yaml`. Adding a code without a catalog entry
fails the build, and so does a catalog entry that nothing returns.

## Proposals and decisions

Two documents, for two different moments.

An **RFC** in `docs/rfc/` comes first, using `docs/rfc/0000-template.md`. It is
for a change somebody outside this repository will notice: a new or changed
public interface, a manifest key, an event type, a guarantee. Open it as a
pull request with the RFC alone, so the design is argued before the code
exists and nobody has to weigh a proposal against the work already spent on
it. An RFC that turns out to be uncontroversial is cheap; it is one page.

An **ADR** in `docs/adr/` comes after, using `docs/adr/0000-template.md`. It
records what was decided and why, including the alternatives that lost. A
change to a public interface needs one whether or not it needed an RFC, because
the next person to read the interface needs the reason and the pull request
discussion will not be where they look.

Keep both to one page. A proposal nobody finishes reading is not a proposal.

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
