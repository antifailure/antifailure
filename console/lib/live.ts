/**
 * The live view's state, kept as pure data so `node --test` can reach it.
 *
 * The console splits every piece of logic that a screen depends on into a
 * React free file for one blunt reason: the tests strip types but cannot parse
 * JSX, so a reducer that lived beside a component could never be tested. This
 * is that reducer. A component subscribes to a stream, hands each event here,
 * and renders whatever `LiveState` comes back.
 *
 * The wire shape mirrors runner/src/live.ts exactly. It is the runner that
 * writes these events, over a socket the engine relays; this file only reads
 * them. The one rule worth stating twice: a `frame` carries base64 image bytes
 * and is EPHEMERAL. It reaches this view from the runner edge and is rendered
 * and forgotten. It is never fetched from the control plane, which by design
 * holds a reference and a hash and never a body. Keeping the live source and
 * the control plane apart is the whole reason the "never a body" promise the
 * portal makes stays true while a person still gets to watch.
 */

export type Surface = "web" | "terminal" | "desktop" | "ios";

export type AgentState = "pending" | "connecting" | "live" | "ended" | "error";

export interface HelloEvent {
  readonly t: "hello";
  readonly run: string;
  readonly at: string;
  readonly protocol: number;
}

export interface AgentEvent {
  readonly t: "agent";
  readonly at: string;
  readonly agent: string;
  readonly surface: Surface;
  readonly state: AgentState;
  readonly persona?: string;
  readonly workflow?: string;
  readonly verdict?: string;
}

export interface StepEvent {
  readonly t: "step";
  readonly at: string;
  readonly agent: string;
  readonly seq: number;
  readonly text: string;
  readonly url?: string;
  readonly action?: string;
}

export interface FrameEvent {
  readonly t: "frame";
  readonly at: string;
  readonly agent: string;
  readonly seq: number;
  readonly mime: "image/jpeg";
  readonly w: number;
  readonly h: number;
  readonly b64: string;
}

export interface DoneEvent {
  readonly t: "done";
  readonly run: string;
  readonly at: string;
  readonly passed: number;
  readonly failed: number;
  readonly flaky: number;
  readonly blocked: number;
  readonly unverified: number;
}

export type LiveEvent = HelloEvent | AgentEvent | StepEvent | FrameEvent | DoneEvent;

/** The most steps a pane keeps. A long run produces thousands and a watcher
 *  reads the recent ones, so the old ones are dropped rather than grown without
 *  bound. */
export const STEP_CAP = 200;

/** parseLiveLine reads one NDJSON line into an event, or undefined for a blank
 *  or malformed one. Tolerant on purpose: a stream is read one partial buffer
 *  at a time and a half-written trailing line is normal, not a fault. An object
 *  with no string `t` is not an event and is refused rather than passed on. */
export function parseLiveLine(line: string): LiveEvent | undefined {
  const trimmed = line.trim();
  if (!trimmed) return undefined;
  try {
    const parsed = JSON.parse(trimmed) as { t?: unknown };
    return typeof parsed.t === "string" ? (parsed as LiveEvent) : undefined;
  } catch {
    return undefined;
  }
}

export interface StepView {
  readonly seq: number;
  readonly text: string;
  readonly url?: string;
  readonly action?: string;
  readonly at: string;
}

export interface FrameView {
  readonly b64: string;
  readonly w: number;
  readonly h: number;
  readonly seq: number;
  readonly at: string;
}

export interface AgentView {
  readonly id: string;
  readonly persona?: string;
  readonly workflow?: string;
  readonly surface: Surface;
  readonly state: AgentState;
  readonly verdict?: string;
  /** The latest frame this agent produced, kept by sequence so a frame that
   *  arrives late and out of order can never replace a newer one. */
  readonly lastFrame?: FrameView;
  readonly steps: readonly StepView[];
  /** Every step the agent took, including the ones dropped from `steps`. */
  readonly stepCount: number;
  /** The highest sequence seen from this agent, over steps and frames alike. */
  readonly latestSeq: number;
}

export interface Counts {
  readonly passed: number;
  readonly failed: number;
  readonly flaky: number;
  readonly blocked: number;
  readonly unverified: number;
}

export interface LiveState {
  readonly run?: string;
  readonly protocol?: number;
  /** Agents in first seen order, so the switcher's numbering is stable as the
   *  run goes and a watcher who pressed 2 keeps looking at the same agent. */
  readonly agents: readonly AgentView[];
  readonly counts?: Counts;
}

export const initialState: LiveState = { agents: [] };

/** blank makes a new agent from the first event that named it, so a step or a
 *  frame that arrives before its `agent` event still has somewhere to land. */
function blank(id: string, surface: Surface = "web"): AgentView {
  return { id, surface, state: "pending", steps: [], stepCount: 0, latestSeq: 0 };
}

/** reduce folds one event into the state and never throws. An event whose shape
 *  is wrong for its type is skipped rather than allowed to blank the whole view:
 *  one bad element must not take the collection with it. A new state object is
 *  returned whenever something changed, so a React view re-renders. */
export function reduce(state: LiveState, event: LiveEvent): LiveState {
  switch (event.t) {
    case "hello": {
      if (typeof event.run !== "string") return state;
      return { ...state, run: event.run, protocol: event.protocol };
    }
    case "done": {
      const { passed, failed, flaky, blocked, unverified } = event;
      if ([passed, failed, flaky, blocked, unverified].some((n) => typeof n !== "number")) {
        return state;
      }
      return { ...state, counts: { passed, failed, flaky, blocked, unverified } };
    }
    case "agent": {
      if (typeof event.agent !== "string") return state;
      return withAgent(state, event.agent, event.surface, (a) => ({
        ...a,
        surface: event.surface ?? a.surface,
        state: event.state ?? a.state,
        ...(event.persona ? { persona: event.persona } : {}),
        ...(event.workflow ? { workflow: event.workflow } : {}),
        ...(event.verdict ? { verdict: event.verdict } : {}),
      }));
    }
    case "step": {
      if (typeof event.agent !== "string" || typeof event.seq !== "number") return state;
      return withAgent(state, event.agent, undefined, (a) => {
        const step: StepView = {
          seq: event.seq,
          text: event.text,
          ...(event.url ? { url: event.url } : {}),
          ...(event.action ? { action: event.action } : {}),
          at: event.at,
        };
        const steps = [...a.steps, step];
        return {
          ...a,
          steps: steps.length > STEP_CAP ? steps.slice(steps.length - STEP_CAP) : steps,
          stepCount: a.stepCount + 1,
          latestSeq: Math.max(a.latestSeq, event.seq),
        };
      });
    }
    case "frame": {
      if (typeof event.agent !== "string" || typeof event.seq !== "number") return state;
      if (typeof event.b64 !== "string" || !event.b64) return state;
      return withAgent(state, event.agent, undefined, (a) => {
        // A lower sequence than the frame already shown arrived late and out of
        // order. Keeping it would flicker the pane backwards, so it is dropped.
        if (a.lastFrame && event.seq <= a.lastFrame.seq) {
          return { ...a, latestSeq: Math.max(a.latestSeq, event.seq) };
        }
        return {
          ...a,
          lastFrame: { b64: event.b64, w: event.w, h: event.h, seq: event.seq, at: event.at },
          latestSeq: Math.max(a.latestSeq, event.seq),
        };
      });
    }
    default:
      return state;
  }
}

/** withAgent applies a change to the named agent, creating it in first seen
 *  order when it has not been named before. */
function withAgent(
  state: LiveState,
  id: string,
  surface: Surface | undefined,
  change: (a: AgentView) => AgentView,
): LiveState {
  const index = state.agents.findIndex((a) => a.id === id);
  if (index === -1) {
    const created = change(blank(id, surface));
    return { ...state, agents: [...state.agents, created] };
  }
  const agents = state.agents.slice();
  agents[index] = change(agents[index]!);
  return { ...state, agents };
}

/** reduceAll folds a whole cast at once, for a replay or a test. */
export function reduceAll(events: readonly LiveEvent[], from: LiveState = initialState): LiveState {
  return events.reduce(reduce, from);
}

/** Which player a pane should use for an agent, decided from what the agent is
 *  and where it is in its life. Pure, so the choice is testable without a DOM.
 *
 *   connecting : the browser or process is still opening. Nothing to show yet.
 *   cast       : a terminal agent. Its output is text and the text is the cast,
 *                so there are no pixels to render and there should not be.
 *   frames     : a pixel surface (web, desktop, ios) that is live and has a
 *                frame. The latest frame is the picture.
 *   ended      : the run is over. The last frame, if any, plus the verdict.
 */
export function transportFor(
  surface: Surface,
  state: AgentState,
  hasFrame: boolean,
): "connecting" | "cast" | "frames" | "ended" {
  if (state === "ended" || state === "error") return "ended";
  if (state === "pending" || state === "connecting") return "connecting";
  if (surface === "terminal") return "cast";
  return hasFrame ? "frames" : "connecting";
}

export type ChipTone = "pass" | "fail" | "warn" | "neutral";

/** The tone of an agent's status chip. A static colour and a word, never a
 *  pulse: a chip that throbs while the reader is doing nothing is the tell this
 *  console bans by name. The verdict wins once the agent has ended, because
 *  "ended" alone does not say whether it passed. */
export function statusChipTone(state: AgentState, verdict?: string): ChipTone {
  if (state === "error") return "fail";
  if (state === "ended") {
    const v = (verdict ?? "").toLowerCase();
    if (v === "pass" || v === "passed") return "pass";
    if (v === "fail" || v === "failed") return "fail";
    if (v === "flaky" || v === "unverified" || v === "blocked") return "warn";
    return "neutral";
  }
  if (state === "live") return "pass";
  return "neutral";
}

/** The word shown in the status chip. */
export function statusChipLabel(state: AgentState, verdict?: string): string {
  if (state === "ended" && verdict) return verdict;
  return state;
}
