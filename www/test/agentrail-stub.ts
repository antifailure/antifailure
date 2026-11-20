// A stand in for agentrail-browser, so a test can watch what the gate hands it.
//
// The module under test is lib/agentrail.ts, and its gate runs for real. What is
// replaced is the vendor on the other side of the dynamic import, for the same
// reason test/posthog-stub.ts replaces posthog-js: a browser bundle in a node
// process proves nothing and fails for reasons that read like something else.

export interface AgentrailStubRecord {
  starts: Record<string, unknown>[]
  stops: number
}

declare global {
  // eslint-disable-next-line no-var
  var __agentrailStub: AgentrailStubRecord | undefined
}

export function agentrailStub(): AgentrailStubRecord {
  globalThis.__agentrailStub ??= { starts: [], stops: 0 }
  return globalThis.__agentrailStub
}

export function resetAgentrailStub(): void {
  globalThis.__agentrailStub = { starts: [], stops: 0 }
}

export function start(options: Record<string, unknown>) {
  agentrailStub().starts.push(options)
  return {
    ready: Promise.resolve(),
    flush() {},
    stop() {
      agentrailStub().stops += 1
    },
  }
}
