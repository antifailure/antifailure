"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError } from "@/lib/api";
import { Button, When } from "@/components/ui";

/**
 * A refresh is not a load, and the shared `useApi` cannot tell them apart.
 *
 * `useApi` sets `{ status: "loading", data: null }` on every reload, which is
 * right for a screen that is genuinely starting over and wrong for one that is
 * already showing results. The run detail polls every six seconds, so against
 * the control plane it blanked itself to a skeleton and came back, once per
 * tick, for as long as somebody watched a running load test.
 *
 * That was invisible for a while and the reason is worth writing down: against
 * a fixture answering in a millisecond the blank window is shorter than a
 * frame, so a hundred samples all looked clean. Adding four hundred
 * milliseconds of latency, which is an ordinary hosted round trip, put the
 * page at zero cards in seven of a hundred and ten samples. The defect was
 * always there; the instrument could not see it.
 *
 * So this keeps the last good data across a refresh, and keeps it across a
 * FAILED refresh too. A transient poll failure replacing nine cards of real
 * measurements with "That did not load" throws away everything the reader
 * had, to report a problem with the request rather than with the data.
 */
interface Live<T> {
  /** "loading" only ever describes the FIRST fetch. */
  status: "loading" | "ready" | "error";
  data: T | null;
  /** Set only when the first fetch failed and there is nothing to show. */
  error: ApiError | null;
  /** A refresh is in flight over data that is already on screen. */
  refreshing: boolean;
  /** The last refresh failed. The data beside it is the last good answer. */
  refreshError: string | null;
  /** When the data on screen was actually fetched, so a stale screen can say
   *  how stale rather than looking current. */
  updatedAt: Date | null;
  reload: () => void;
}

export function useLive<T>(fetcher: () => Promise<T>, deps: unknown[] = []): Live<T> {
  const [state, setState] = useState<{
    status: "loading" | "ready" | "error";
    data: T | null;
    error: ApiError | null;
    updatedAt: Date | null;
  }>({ status: "loading", data: null, error: null, updatedAt: null });
  const [refreshing, setRefreshing] = useState(false);
  const [refreshError, setRefreshError] = useState<string | null>(null);

  const alive = useRef(true);
  const run = useRef(fetcher);
  run.current = fetcher;
  // Whether anything has ever loaded, read inside the fetch rather than from
  // state, so a refresh triggered in the same tick as a dependency change
  // cannot mistake itself for a first load.
  const loaded = useRef(false);

  const fetchNow = useCallback((first: boolean) => {
    if (!first) setRefreshing(true);
    run
      .current()
      .then((data) => {
        if (!alive.current) return;
        loaded.current = true;
        setState({ status: "ready", data, error: null, updatedAt: new Date() });
        setRefreshError(null);
      })
      .catch((e: unknown) => {
        if (!alive.current) return;
        const err =
          e instanceof ApiError
            ? e
            : new ApiError("The control plane could not be reached.", 0, "NETWORK");
        if (first || !loaded.current) {
          setState({ status: "error", data: null, error: err, updatedAt: null });
        } else {
          // Keep the data. Only the freshness claim changes.
          setRefreshError(err.message);
        }
      })
      .finally(() => {
        if (alive.current) setRefreshing(false);
      });
  }, []);

  useEffect(() => {
    alive.current = true;
    loaded.current = false;
    setState({ status: "loading", data: null, error: null, updatedAt: null });
    setRefreshError(null);
    fetchNow(true);
    return () => {
      alive.current = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps]);

  return {
    ...state,
    refreshing,
    refreshError,
    reload: useCallback(() => fetchNow(false), [fetchNow]),
  };
}

/**
 * Ask again on an interval, and stop the moment there is nothing to ask about.
 *
 * The stop condition is why this is written out rather than a bare
 * setInterval. A poll still running after a run has finished is a request every
 * few seconds forever on a tab somebody left open, invisible in every way
 * except the load it makes.
 *
 * `document.hidden` is checked at each tick rather than subscribed to: a
 * backgrounded tab does not need the answer and browsers already throttle the
 * timer, so this only has to avoid making the request.
 */
export function useInterval(active: boolean, ms: number, tick: () => void) {
  const fn = useRef(tick);
  fn.current = tick;
  useEffect(() => {
    if (!active) return;
    const id = setInterval(() => {
      if (typeof document !== "undefined" && document.hidden) return;
      fn.current();
    }, ms);
    return () => clearInterval(id);
  }, [active, ms]);
}

/**
 * A strip saying the numbers below are the last good ones.
 *
 * Shown only when a refresh has actually failed. A screen that is simply
 * refreshing says nothing, because a notice that appears every six seconds is
 * a notice people stop reading, and there is nothing wrong when it does.
 *
 * The time is absolute as well as relative, through the shared `When`, so
 * "2m ago" can be compared against a log line rather than only felt.
 */
export function StaleNotice({
  message,
  updatedAt,
  onRetry,
  retrying,
}: {
  message: string;
  updatedAt: Date | null;
  onRetry: () => void;
  retrying: boolean;
}) {
  return (
    <div
      role="status"
      className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-rule bg-[rgba(138,90,0,0.07)] px-4 py-2.5"
    >
      {/* "as of X rather than now" was the first wording and it collapsed on
          the commonest case: `ago` says "just now" under a minute, so a notice
          appearing six seconds after the last good answer read "as of just now
          rather than now". The numbers being the LAST GOOD ones is the point,
          and the timestamp qualifies them rather than being contrasted with
          the present. */}
      <p className="max-w-[74ch] text-[12.5px] leading-6 text-warn">
        The last refresh did not land. The numbers below are the last good ones,
        from <When value={updatedAt} />. {message}
      </p>
      <Button onClick={onRetry} busy={retrying}>
        {retrying ? "Refreshing" : "Refresh"}
      </Button>
    </div>
  );
}
