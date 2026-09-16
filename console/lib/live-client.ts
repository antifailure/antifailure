"use client";

import { useEffect, useReducer, useRef, useState } from "react";
import {
  reduce,
  parseLiveLine,
  initialState,
  type LiveEvent,
  type LiveState,
} from "./live.ts";
import { fixtureCast } from "./live-fixture.ts";

/**
 * Where the live view gets its events, and the discipline behind the choice.
 *
 * The events and the frames come from the RUNNER EDGE, never from the control
 * plane. That is the boundary the whole product is sold on: the control plane
 * holds counts, verdicts and a reference, and never a body, a log, a screenshot
 * or a frame. So `source` is a signed URL that reaches a relay in front of the
 * runner, and this hook connects a browser straight to it. It must never be a
 * control plane tRPC path, and nothing here fetches a frame from one.
 *
 * A "replay" source is the exception that proves it: it plays a recorded cast
 * that ships with the console, which is what a durable filmstrip plays back and
 * what the design is verified against. It still never touches the control plane
 * for a frame.
 */
export type LiveSource =
  | { readonly kind: "replay"; readonly cast?: readonly LiveEvent[]; readonly stepMs?: number }
  | { readonly kind: "sse"; readonly url: string };

/** The connection's own state, so the page can say connecting, live, ended or
 *  lost without inventing it from the agents. */
export type Connection = "connecting" | "live" | "ended" | "lost";

export interface Live {
  readonly state: LiveState;
  readonly connection: Connection;
}

/** A replay source over the bundled cast, the default a page uses when no live
 *  relay was handed to it. */
export function replaySource(cast: readonly LiveEvent[] = fixtureCast, stepMs = 550): LiveSource {
  return { kind: "replay", cast, stepMs };
}

/** useLiveAgents subscribes to a source and folds its events into a LiveState.
 *
 * The reducer does all the state work; this hook is only the transport. A
 * replay walks the cast on a timer. An sse source opens an EventSource and
 * feeds each line through the same reducer. Either way the frames arrive from
 * the edge, and the control plane is never asked for one.
 */
export function useLiveAgents(source: LiveSource): Live {
  const [state, dispatch] = useReducer(reduce, initialState);
  const [connection, setConnection] = useState<Connection>("connecting");
  // The reducer is dispatched through useReducer so React batches and the view
  // stays in step with the events, rather than a ref the render cannot see.
  const alive = useRef(true);

  useEffect(() => {
    alive.current = true;
    setConnection("connecting");

    if (source.kind === "replay") {
      const cast = source.cast ?? fixtureCast;
      const stepMs = source.stepMs ?? 550;
      let i = 0;
      setConnection("live");
      const id = setInterval(() => {
        if (!alive.current) return;
        if (i >= cast.length) {
          setConnection("ended");
          clearInterval(id);
          return;
        }
        const event = cast[i++]!;
        dispatch(event);
        if (event.t === "done") setConnection("ended");
      }, stepMs);
      return () => {
        alive.current = false;
        clearInterval(id);
      };
    }

    // A live relay over server sent events. Each message is one NDJSON line,
    // parsed and folded exactly as a replayed one is. EventSource reconnects on
    // its own; a terminal 'done' or an unrecoverable error settles the chip.
    if (typeof EventSource === "undefined") {
      setConnection("lost");
      return () => {
        alive.current = false;
      };
    }
    const es = new EventSource(source.url, { withCredentials: false });
    es.onopen = () => {
      if (alive.current) setConnection("live");
    };
    es.onmessage = (message: MessageEvent<string>) => {
      if (!alive.current) return;
      const event = parseLiveLine(message.data);
      if (!event) return;
      dispatch(event);
      if (event.t === "done") {
        setConnection("ended");
        es.close();
      }
    };
    es.onerror = () => {
      if (alive.current) setConnection("lost");
    };
    return () => {
      alive.current = false;
      es.close();
    };
  }, [source]);

  return { state, connection };
}
