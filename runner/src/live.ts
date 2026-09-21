// The live channel: what a run emits while it is still running, so a person
// can watch it happen rather than read what happened.
//
// A run today is one document in and one document out (see main.ts). That is
// the right contract for a verdict, and it is the wrong one for watching: by
// the time the document is written the run is over. This module adds a second,
// strictly optional channel that carries events and frames OUT of the runner
// while the browser is still open, over a local socket the engine hands us.
//
// Two properties are load bearing and are the reason the shapes below look the
// way they do.
//
//   1. The live channel is best effort and never changes a verdict. A run with
//      nobody watching, or a socket that will not connect, behaves exactly as a
//      run without this module: the sink is a no-op, every call returns, and the
//      ResultDocument is byte for byte what it was. A run must not fail because
//      the watcher went away.
//   2. Frames are EPHEMERAL. A `frame` event carries base64 image bytes, and it
//      exists only to be rendered live and then forgotten. It is written to the
//      local socket the engine reads and is never part of the ResultDocument,
//      never written to an artifact by this module, and never sent to the
//      control plane. The durable recording is the webm the browser already
//      writes (Evidence.video); the control plane holds a reference to it, not
//      the bytes. Keeping the two apart is what lets the "never a body" promise
//      the console makes stay true while a person still gets to watch.

import { connect, type Socket } from 'node:net';

/** The surfaces a run can drive. web, terminal, desktop and ios are live;
 *  android is declared here so the wire shape is stable before its driver has
 *  driven anything, and so a manifest naming it is refused by name rather than
 *  read as a typo. */
export type Surface = 'web' | 'terminal' | 'desktop' | 'ios' | 'android';

/** Which agent is which, from a watcher's point of view. One workflow running
 *  as one persona is one agent; one exploration goal is one agent. The id is
 *  stable for the life of the run so a watcher can follow or switch to it. */
export interface AgentDescriptor {
  readonly id: string;
  readonly persona?: string;
  readonly workflow?: string;
  readonly surface: Surface;
}

/** Where an agent is in its life. `pending` is declared but not started;
 *  `connecting` is opening its browser; `live` is driving; `ended` finished
 *  with a verdict; `error` is the runner's own failure, not the app's. */
export type AgentState = 'pending' | 'connecting' | 'live' | 'ended' | 'error';

/** The current protocol version. Bumped when the wire shape changes so a
 *  watcher can refuse a stream it cannot read rather than mis-render it. */
export const PROTOCOL = 1;

export interface HelloEvent {
  readonly t: 'hello';
  readonly run: string;
  readonly at: string;
  readonly protocol: number;
}

export interface AgentEvent {
  readonly t: 'agent';
  readonly at: string;
  readonly agent: string;
  readonly surface: Surface;
  readonly state: AgentState;
  readonly persona?: string;
  readonly workflow?: string;
  readonly verdict?: string;
}

export interface StepEvent {
  readonly t: 'step';
  readonly at: string;
  readonly agent: string;
  readonly seq: number;
  readonly text: string;
  readonly url?: string;
  readonly action?: string;
}

export interface FrameEvent {
  readonly t: 'frame';
  readonly at: string;
  readonly agent: string;
  readonly seq: number;
  readonly mime: 'image/jpeg';
  readonly w: number;
  readonly h: number;
  /** base64 of the JPEG. Ephemeral: rendered live and forgotten. */
  readonly b64: string;
}

export interface DoneEvent {
  readonly t: 'done';
  readonly run: string;
  readonly at: string;
  readonly passed: number;
  readonly failed: number;
  readonly flaky: number;
  readonly blocked: number;
  readonly unverified: number;
}

export type LiveEvent =
  | HelloEvent | AgentEvent | StepEvent | FrameEvent | DoneEvent;

/** encode is one event as one NDJSON line, newline included. A frame's bytes
 *  are already base64, so there is nothing here that a line boundary can break. */
export function encode(event: LiveEvent): string {
  return JSON.stringify(event) + '\n';
}

/** decode reads one line back. Returns undefined for a blank or malformed line
 *  rather than throwing, because a stream is read one partial buffer at a time
 *  and a half-written trailing line is normal, not a fault. */
export function decode(line: string): LiveEvent | undefined {
  const trimmed = line.trim();
  if (!trimmed) return undefined;
  try {
    const parsed = JSON.parse(trimmed) as LiveEvent;
    return typeof parsed?.t === 'string' ? parsed : undefined;
  } catch {
    return undefined;
  }
}

/** A place events go. Every method returns nothing and never throws: a watcher
 *  channel that fails must not take the run with it. */
export interface LiveSink {
  hello(run: string): void;
  agent(desc: AgentDescriptor, state: AgentState, verdict?: string): void;
  step(agent: string, ev: { text: string; url?: string; action?: string }): void;
  frame(agent: string, ev: { mime: 'image/jpeg'; w: number; h: number; b64: string }): void;
  done(counts: {
    passed: number; failed: number; flaky: number; blocked: number; unverified: number;
  }): void;
  /** Flush and release. Safe to call more than once. */
  close(): Promise<void>;
}

/** The next monotonic sequence number for an agent. One counter per agent
 *  covers both steps and frames, so a watcher can order everything an agent did
 *  by a single field and know the latest frame from the latest step. */
class Sequences {
  readonly #next = new Map<string, number>();
  take(agent: string): number {
    const n = (this.#next.get(agent) ?? 0) + 1;
    this.#next.set(agent, n);
    return n;
  }
}

/** nullSink discards everything. This is the default, and it is what makes the
 *  live channel free: a run nobody is watching pays for nothing. */
export function nullSink(): LiveSink {
  return {
    hello() {},
    agent() {},
    step() {},
    frame() {},
    done() {},
    async close() {},
  };
}

/** socketSink writes NDJSON events to a local socket the engine is listening on.
 *
 * The socket path comes from the job document. Connection is asynchronous and
 * events can fire before it completes, so lines are queued until the socket is
 * writable and flushed then. If the connection fails, or is refused, or drops,
 * the sink degrades to a no-op: the run goes on, and nothing here throws. That
 * is the whole discipline of a best-effort channel.
 */
export function socketSink(path: string): LiveSink {
  const seq = new Sequences();
  let socket: Socket | undefined = connect(path);
  let connected = false;
  let broken = false;
  const pending: string[] = [];

  const drop = () => {
    broken = true;
    pending.length = 0;
    if (socket) {
      socket.removeAllListeners();
      socket.destroy();
      socket = undefined;
    }
  };

  socket.on('connect', () => {
    connected = true;
    if (socket && pending.length) {
      socket.write(pending.join(''));
      pending.length = 0;
    }
  });
  // A refused or reset connection is expected: the watcher may never have been
  // there. Swallowed, and the sink becomes a no-op from here.
  socket.on('error', drop);

  const send = (event: LiveEvent) => {
    if (broken) return;
    const line = encode(event);
    if (connected && socket) {
      // A backed-up socket returns false; we do not block the run on it, we
      // just keep writing and let the kernel buffer. A watcher that cannot
      // keep up loses frames, never the run.
      try {
        socket.write(line);
      } catch {
        drop();
      }
    } else {
      pending.push(line);
    }
  };

  const now = () => new Date().toISOString();

  return {
    hello(run) {
      send({ t: 'hello', run, at: now(), protocol: PROTOCOL });
    },
    agent(desc, state, verdict) {
      send({
        t: 'agent', at: now(), agent: desc.id, surface: desc.surface, state,
        ...(desc.persona ? { persona: desc.persona } : {}),
        ...(desc.workflow ? { workflow: desc.workflow } : {}),
        ...(verdict ? { verdict } : {}),
      });
    },
    step(agent, ev) {
      send({
        t: 'step', at: now(), agent, seq: seq.take(agent), text: ev.text,
        ...(ev.url ? { url: ev.url } : {}),
        ...(ev.action ? { action: ev.action } : {}),
      });
    },
    frame(agent, ev) {
      send({
        t: 'frame', at: now(), agent, seq: seq.take(agent),
        mime: ev.mime, w: ev.w, h: ev.h, b64: ev.b64,
      });
    },
    done(counts) {
      send({ t: 'done', run: '', at: now(), ...counts });
    },
    async close() {
      if (broken || !socket) return;
      // A run can finish before the socket has finished connecting, and
      // everything it emitted is still sitting in `pending`: nothing is
      // written until the `connect` handler above flushes it. Ending the
      // socket here without waiting for that throws the ENTIRE live stream
      // away, and the watcher sees not one event rather than a few.
      //
      // It is not theoretical and it is not a slow machine. A desktop or
      // terminal agent whose work is a few function calls finishes in under a
      // millisecond, which is well inside one turn of the event loop, so the
      // faster the run the less a watcher is told about it. Bounded, so a
      // socket nobody is listening on still cannot hold the run open: an
      // already refused connection has set `broken` and returned above, and a
      // connection that fails during this wait costs the same half second the
      // drain below already allows.
      if (!connected) {
        await new Promise<void>((resolve) => {
          const timer = setTimeout(resolve, 500);
          timer.unref?.();
          const settle = () => { clearTimeout(timer); resolve(); };
          socket?.once('connect', settle);
          socket?.once('error', settle);
        });
      }
      if (broken || !socket) return;
      await new Promise<void>((resolve) => {
        const s = socket!;
        const finish = () => resolve();
        s.end(finish);
        // A socket that never drains must not hold the process open. The run
        // is already over here; the watcher gets what already made it out.
        setTimeout(() => { s.destroy(); resolve(); }, 500).unref?.();
      });
      socket = undefined;
    },
  };
}

/** How to grab one frame. Returns undefined when a frame could not be taken,
 *  which is normal mid-navigation and must not be treated as an error. */
export type Capture = () => Promise<{ w: number; h: number; b64: string } | undefined>;

/** FramePump samples a surface on an interval and pushes each frame to a sink.
 *
 * Separated from the browser Session so it can be tested without a browser: a
 * fake capture proves the cadence, the sequence, the overlap guard, and that a
 * capture that throws is swallowed rather than ending the run. The Session's
 * only job is to hand it a real `page.screenshot` capture.
 */
export class FramePump {
  readonly #capture: Capture;
  readonly #sink: LiveSink;
  readonly #agent: string;
  readonly #intervalMs: number;
  #timer: ReturnType<typeof setInterval> | undefined;
  #busy = false;
  #stopped = false;

  constructor(capture: Capture, sink: LiveSink, agent: string, intervalMs = 800) {
    this.#capture = capture;
    this.#sink = sink;
    this.#agent = agent;
    this.#intervalMs = intervalMs;
  }

  /** start begins sampling. The timer is unref'd so a pump left running can
   *  never by itself keep the process alive. */
  start(): void {
    if (this.#timer || this.#stopped) return;
    this.#timer = setInterval(() => { void this.tick(); }, this.#intervalMs);
    this.#timer.unref?.();
  }

  /** tick takes exactly one frame, and never lets two captures overlap: a slow
   *  screenshot must not stack up behind a fast interval. Exposed for the test
   *  so a frame can be forced without waiting on a timer. */
  async tick(): Promise<void> {
    if (this.#busy || this.#stopped) return;
    this.#busy = true;
    try {
      const shot = await this.#capture();
      if (shot && !this.#stopped) {
        this.#sink.frame(this.#agent, {
          mime: 'image/jpeg', w: shot.w, h: shot.h, b64: shot.b64,
        });
      }
    } catch {
      // A frame that could not be taken is not evidence of anything. Skip it.
    } finally {
      this.#busy = false;
    }
  }

  stop(): void {
    this.#stopped = true;
    if (this.#timer) {
      clearInterval(this.#timer);
      this.#timer = undefined;
    }
  }
}
