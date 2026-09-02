"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError } from "@/lib/api";
import { Button } from "@/components/ui";
import { LIST_CAP, type Windowed } from "@/lib/load";

/**
 * A list that says how much of itself you are looking at.
 *
 * Neither list route takes a cursor. Both take a limit, cap it at two hundred,
 * and answer with a bare array. So there is nothing to page through, and this
 * hook does not pretend otherwise by decoding a cursor that is not there.
 *
 * What it does instead is refuse to let a truncation be silent. Every fetch
 * asks the control plane for one row more than it shows, so "there are more"
 * is an answer from the server rather than a guess from a page that happened
 * to come back full. When there are more, the footer offers the widest window
 * the control plane will serve. When even that is full, it says so plainly
 * rather than presenting two hundred rows as the set.
 *
 * That distinction is the whole point. An organization with two hundred and
 * ten runs looking at fifty of them, in a table with nothing on the page
 * saying otherwise, draws a conclusion from a set they believe is everything.
 * The same defect shipped once already on `/runs`.
 *
 * Widening REPLACES the rows rather than appending to them. With no cursor
 * there is no way to ask for "the ones after this", so a second request is a
 * second look at the same list from the top, and appending it would repeat
 * every row already on screen.
 *
 * And `reload` does NOT blank the table. That distinction is the one the
 * shared `useApi` gets wrong and which cost this area a rework: it sets
 * `data: null` on every refresh, so a table already showing rows drops to a
 * skeleton and comes back. Against a fixture answering in a millisecond the
 * gap is shorter than a frame; at four hundred milliseconds, an ordinary
 * hosted round trip, it is a flash of nothing every time somebody starts a
 * run. A dependency changing IS a new subject and does blank, because the rows
 * on screen are then about something else.
 */
interface Windowing<T> {
  status: "loading" | "ready" | "error";
  items: T[];
  error: ApiError | null;
  /** The control plane had at least one row this window does not show. */
  more: boolean;
  /** The window is as wide as the control plane will answer, so `more` here
   *  means "there may be more" rather than "there are more". */
  atCap: boolean;
  /** A fetch is in flight over rows that are already on screen, which must not
   *  be replaced by a skeleton. */
  widening: boolean;
  /** That fetch failed. The rows already loaded stay, because throwing away
   *  what you have because the next request failed is a worse answer than
   *  showing it. */
  widenError: string | null;
  widen: () => void;
  reload: () => void;
}

export function useWindow<T>(
  fetch: (limit: number) => Promise<Windowed<T>>,
  deps: unknown[] = [],
  initialLimit = 50,
): Windowing<T> {
  const [state, setState] = useState<{
    status: "loading" | "ready" | "error";
    items: T[];
    error: ApiError | null;
    more: boolean;
    limit: number;
  }>({ status: "loading", items: [], error: null, more: false, limit: initialLimit });
  const [widening, setWidening] = useState(false);
  const [widenError, setWidenError] = useState<string | null>(null);

  const alive = useRef(true);
  const run = useRef(fetch);
  run.current = fetch;
  /** How wide the window on screen is, read inside a fetch rather than out of
   *  state, so a reload after a widen asks for the width the reader is looking
   *  at rather than dropping them back to fifty. */
  const width = useRef(initialLimit);
  /** Whether anything has ever loaded. A refresh over an empty error state has
   *  nothing to preserve, so it is allowed to show the skeleton again. */
  const loaded = useRef(false);

  const fetchAt = useCallback((limit: number, blank: boolean) => {
    width.current = limit;
    if (blank) {
      setState({ status: "loading", items: [], error: null, more: false, limit });
      setWidenError(null);
    } else {
      setWidening(true);
      setWidenError(null);
    }
    run
      .current(limit)
      .then((page) => {
        if (!alive.current) return;
        loaded.current = true;
        width.current = page.limit;
        setState({
          status: "ready",
          items: page.items,
          error: null,
          more: page.more,
          limit: page.limit,
        });
        setWidenError(null);
      })
      .catch((e: unknown) => {
        if (!alive.current) return;
        const err =
          e instanceof ApiError
            ? e
            : new ApiError("The control plane could not be reached.", 0, "NETWORK");
        if (blank || !loaded.current) {
          setState({ status: "error", items: [], error: err, more: false, limit });
        } else {
          // Keep the rows. Only the claim that they are current changes.
          setWidenError(err.message);
        }
      })
      .finally(() => {
        if (alive.current) setWidening(false);
      });
  }, []);

  useEffect(() => {
    alive.current = true;
    loaded.current = false;
    setWidening(false);
    fetchAt(initialLimit, true);
    return () => {
      alive.current = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, initialLimit]);

  const widen = useCallback(() => fetchAt(LIST_CAP, false), [fetchAt]);

  return {
    status: state.status,
    items: state.items,
    error: state.error,
    more: state.more,
    atCap: state.limit >= LIST_CAP,
    widening,
    widenError,
    widen,
    // Not a blanking reload. Somebody who has just started a run is looking at
    // the table it is about to appear in, and replacing it with a skeleton to
    // fetch one more row is the defect this hook exists to not have.
    reload: useCallback(() => fetchAt(width.current, false), [fetchAt]),
  };
}

/**
 * The footer under a windowed table.
 *
 * It says the count it actually has and nothing more. Nothing in the response
 * carries a total, so "50 of about 200" would be a number the console made up.
 *
 * Three sentences, because there are three genuinely different situations and
 * one of them is a wall rather than a button. A list that fits gets no footer
 * at all: "6 runs" under six visible rows is furniture.
 */
export function WindowFooter({
  count,
  noun,
  more,
  atCap,
  widening,
  widenError,
  onWiden,
  narrow,
}: {
  count: number;
  /** Singular. "run" becomes "1 run" and "12 runs". */
  noun: string;
  more: boolean;
  atCap: boolean;
  widening: boolean;
  widenError: string | null;
  onWiden: () => void;
  /** What a reader can do once the widest window is still full. Named by the
   *  caller because the filters differ per list, and an instruction that does
   *  not match the controls on screen is worse than none. */
  narrow?: string;
}) {
  if (!more && widenError === null && !atCap) return null;
  const label = `${count.toLocaleString()} ${count === 1 ? noun : `${noun}s`}`;

  const message =
    more && atCap
      ? // The weaker claim, said in weaker words. At the cap there is no extra
        // row to ask for, so a full window means there may be more rather than
        // that there are.
        `This is the newest ${label}, which is as many as the control plane answers with. There may be older ones behind it.`
      : more
        ? `Showing the newest ${label}. There are more.`
        : `All ${label}.`;

  return (
    <div className="flex flex-wrap items-center justify-between gap-3 border-t border-rule px-4 py-3">
      <p className="max-w-[74ch] text-[12px] leading-5 text-dim">
        {message}
        {more && atCap && narrow ? ` ${narrow}` : ""}
      </p>
      <div className="flex flex-wrap items-center gap-3">
        {widenError ? (
          <p role="alert" className="text-[12px] leading-5 text-fail">
            {widenError}
          </p>
        ) : null}
        {more && !atCap ? (
          <Button onClick={onWiden} busy={widening}>
            {widening ? "Loading" : widenError ? "Try again" : `Show up to ${LIST_CAP}`}
          </Button>
        ) : null}
      </div>
    </div>
  );
}
