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
  /** A wider window is being fetched over rows that are already on screen,
   *  which must not be replaced by a skeleton. */
  widening: boolean;
  /** The wider window failed. The rows already loaded stay, because throwing
   *  away what you have because the next request failed is a worse answer than
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
  const [nonce, setNonce] = useState(0);

  const alive = useRef(true);
  const run = useRef(fetch);
  run.current = fetch;

  useEffect(() => {
    alive.current = true;
    setState({ status: "loading", items: [], error: null, more: false, limit: initialLimit });
    setWidenError(null);
    setWidening(false);
    run
      .current(initialLimit)
      .then((page) => {
        if (!alive.current) return;
        setState({
          status: "ready",
          items: page.items,
          error: null,
          more: page.more,
          limit: page.limit,
        });
      })
      .catch((e: unknown) => {
        if (!alive.current) return;
        setState({
          status: "error",
          items: [],
          error:
            e instanceof ApiError
              ? e
              : new ApiError("The control plane could not be reached.", 0, "NETWORK"),
          more: false,
          limit: initialLimit,
        });
      });
    return () => {
      alive.current = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, nonce, initialLimit]);

  const widen = useCallback(() => {
    setWidening(true);
    setWidenError(null);
    run
      .current(LIST_CAP)
      .then((page) => {
        if (!alive.current) return;
        setState({
          status: "ready",
          items: page.items,
          error: null,
          more: page.more,
          limit: page.limit,
        });
      })
      .catch((e: unknown) => {
        if (!alive.current) return;
        setWidenError(e instanceof Error ? e.message : "The wider list did not load.");
      })
      .finally(() => {
        if (alive.current) setWidening(false);
      });
  }, []);

  return {
    status: state.status,
    items: state.items,
    error: state.error,
    more: state.more,
    atCap: state.limit >= LIST_CAP,
    widening,
    widenError,
    widen,
    reload: useCallback(() => setNonce((n) => n + 1), []),
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
