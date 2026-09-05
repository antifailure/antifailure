// A stand in for posthog-js, so a test can watch what is handed to it.
//
// NOT A MOCK OF THE THING UNDER TEST. The module under test is lib/posthog.ts:
// its gate, its host resolution and the configuration it builds all run for
// real. What is replaced is the vendor on the other side of the dynamic import,
// because loading a browser bundle into a node process proves nothing and fails
// for reasons that read like something else.
//
// The record lives on globalThis rather than in this module, because the module
// is cached for the whole run and each test needs to start from nothing.

export interface StubRecord {
  inits: { key: string; options: Record<string, unknown> }[]
  stopped: number
  optedOut: number
  /** Recorder buffers thrown away. The switch promises this and the vendor's
   *  own opt out does not do it outside its cookieless path. */
  discarded: number
  /** Configuration changed after init. Turning request batching off is what
   *  stops the vendor's unload handler flushing what is already queued. */
  reconfigured: Record<string, unknown>[]
}

declare global {
  // eslint-disable-next-line no-var
  var __posthogStub: StubRecord | undefined
}

export function stubRecord(): StubRecord {
  globalThis.__posthogStub ??= { inits: [], stopped: 0, optedOut: 0, discarded: 0, reconfigured: [] }
  return globalThis.__posthogStub
}

export function resetStub(): void {
  globalThis.__posthogStub = { inits: [], stopped: 0, optedOut: 0, discarded: 0, reconfigured: [] }
}

export default {
  init(key: string, options: Record<string, unknown>): void {
    stubRecord().inits.push({ key, options })
  },
  sessionRecording: {
    dispose(options?: { discardBufferedEvents?: boolean }): void {
      if (options?.discardBufferedEvents) stubRecord().discarded += 1
    },
  },
  set_config(config: Record<string, unknown>): void {
    stubRecord().reconfigured.push(config)
  },
  stopSessionRecording(): void {
    stubRecord().stopped += 1
  },
  opt_out_capturing(): void {
    stubRecord().optedOut += 1
  },
}
