# Agent replay SDK

Explicit Node.js capture for a customer agent, separate from Antifailure's own browser runner. Requires Node 24. This package is built from this repository; a merge does not publish it to npm.

```sh
npm ci
npm test
npm run build
npm pack
```

Install the resulting archive in the application. `AgentReplay.run` records metadata and keyed hashes by default. Opt into each content boundary by name. Supply the real immutable code revision and propagate the application's W3C trace ID with `run` options.

```ts
import { AgentReplay } from '@antifailure/replay';

const replay = new AgentReplay({
  project: 'billing',
  service: 'support-agent',
  commit: process.env.APP_COMMIT!,
  directory: '.antifailure-captures',
  policyVersion: 'billing-v1',
  onDiagnostic: reason => console.error(reason),
});

const answer = await replay.run({subscriptionId: 1}, async () => {
  return replay.boundary({
    kind: 'tool', name: 'help-search', version: '1',
    input: {query: 'cancelled subscription'},
  }, async () => searchApprovedHelpCenter());
});
```

`searchApprovedHelpCenter` is application code. Wrappers are explicit; unwrapped calls are not recorded. No automatic provider or framework coverage is claimed.

To retain replayable content, add `content: ['input', 'output', 'tool:help-search']`. This allows those bodies to be retained and requires a project redactor for personal fields. Credential fields and recognized credential strings are removed centrally. Changed content carries an identity-transformation issue and cannot be promoted as equivalent evidence. Masking a Postgres golden does not sanitize a prompt or tool response.

Boundary kinds are `model`, `tool`, `http`, `database`, and `effect`. Model identities require provider, model, instructions, tool schemas and behavior-changing settings. `captureHTTP` records a bounded text response, status, content type and retrieval time, and refuses redirects. Database boundaries run their callback against the isolated branch during replay; other callbacks are replaced by recorded responses. Effect callbacks are never executed during replay and appear in the effect ledger.

Use `replay.now()` for an application clock that should be frozen. Global clocks, background workers and arbitrary timers are not intercepted. Parallel runs have independent async context. Concurrent or unfinished boundaries within one captured run are reported incomplete in this first protocol because their causal ordering cannot be inferred safely.

The `replay` method belongs behind an application endpoint enabled only for a contained test environment. The engine sets `AF_REPLAY_ENABLED=true` there. The [example application](example/app.mjs) checks that flag before accepting replay requests. Do not expose a replay endpoint in production.

Capture defaults to at most 1,000 exchanges, 256 KiB per retained value, a 4 MiB artifact, 32 concurrent captures and a one-second flush budget. There is no unbounded upload queue. `onDiagnostic` receives failures; without a callback they become `AF_CAPTURE_INCOMPLETE` process warnings. `failClosed: true` makes a protected run fail when capture cannot be persisted. Supply token and cost metadata with the boundary method's optional usage callback; the SDK does not invent provider prices.

The runnable demonstration uses a synthetic model response, a real local HTTP help response, and a real Postgres query. From the repository root:

```sh
just test-agent-replay
```

It requires Docker and `psql`, builds the real CLI, creates a synthetic application repository, saves a verified golden, records a billing failure, verifies the fix, reintroduces the bug and runs the saved case. It prints the fixture and evidence directories. No paid model call is needed.
