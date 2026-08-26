# ADR 0001: Go for the engine, TypeScript for the runner and the web

- **Status:** accepted
- **Date:** 2026-08-25
- **Deciders:** project owner

## Context

Antifailure has three kinds of work with genuinely different requirements.

The engine is a command line binary that users install with one command and
run on a laptop, in CI, and on a server. It shells out to Docker and Postgres,
holds long lived connections, runs a TLS terminating proxy in the hot path of
every request an environment makes, and must tear resources down reliably
after a crash. It needs a single static binary with no runtime to install, real
concurrency primitives, and a standard library that already speaks HTTP/1.1,
HTTP/2, TLS, and cryptography without a dependency.

The agent runner drives a real browser. Playwright is the only browser
automation library with the maturity we need for accessibility tree
observation, trace recording, and multi browser support, and its first class
binding is TypeScript. Model provider SDKs are also strongest in TypeScript.

The control plane is a web application. It shares types and validation with the
runner.

## Decision

The engine, the CLI, the sidecar proxy, and everything that touches the
customer's database is Go. The agent runner and the control plane are
TypeScript. The single source of truth for every type that crosses the boundary
is JSON Schema in `schemas/`, from which Go structs, TypeScript types, and Zod
validators are generated and committed. CI regenerates and fails on a diff.

Any future component in a third language, Rust included, is introduced only
behind a process boundary with a schema defined protocol, never as a cgo
dependency or a native module, so that the single static binary property and
the `CGO_ENABLED=0` build survive.

## Consequences

Easier: the engine ships as one file per platform, cross compiles to four
targets, and starts in milliseconds. `go test -race` and `goleak` catch the
concurrency bugs that a proxy and a lifecycle manager attract. The runner gets
Playwright and the model SDKs without a foreign function interface.

Harder: two toolchains, two linters, two test runners, and one code generation
step that must never drift. We pay for that with the generated code being
committed and gate checked rather than produced at build time, so a contributor
who does not run the generator gets a red CI rather than a mystery.

Committed to: no cgo in the engine. That rules out the SQLite C library, so the
local state store uses a pure Go SQLite driver. It also rules out linking a
Postgres client library, which is fine because pgx is pure Go and better.

## Alternatives considered

**Go everywhere, including the runner.** Rejected because the Go browser
automation options do not offer Playwright's accessibility tree extraction or
its trace format, and reimplementing either is a project in itself.

**TypeScript everywhere, including the engine.** Rejected because the engine
would then require a Node runtime on every machine that runs it, single binary
distribution becomes a bundler problem, and the proxy's per request latency
budget of five milliseconds at two thousand requests per second on one core is
not something we want to defend on a single threaded runtime.

**Rust for the proxy.** Rejected for now. The latency budget is comfortably met
in Go, and a second systems language would double the review surface on the
most security sensitive component in the product. The process boundary rule
above keeps the door open if measurement ever says otherwise.
