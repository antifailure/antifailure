"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError } from "@/lib/api";
import { Button } from "@/components/ui";
import type { Page } from "@/lib/load";

/**
 * A cursor-paginated list, and an honest statement of how much of it you have.
 *
 * Every list route here takes a limit and answers with a `nextCursor`, and
 * until this hook existed the console decoded that cursor and threw it away.
 * An organization with two hundred runs saw fifty of them, in a table that
 * looked complete, with nothing on the page saying otherwise. A truncation
 * nobody is told about is worse than a slow page: the reader draws a
 * conclusion from a set they believe is everything.
 *
 * What it will not do is invent a total. Nothing in the response says how many
 * rows exist, so the footer says how many are loaded and whether there are
 * more, and only claims a total once it has actually reached the end. "50 of
 * about 200" would be a number the console made up.
 */

interface Paged<T> {
  status: "loading" | "ready" | "error";
  items: T[];
  error: ApiError | null;
  /** True while a further page is being fetched, which is a different state
   *  from the first load: the table is on screen and must not be replaced by a
   *  skeleton. */
  loadingMore: boolean;
  /** Set when fetching a further page failed. The rows already loaded stay,
   *  because throwing away what you have because the next page failed is a
   *  worse answer than showing it. */
  moreError: string | null;
  hasMore: boolean;
  /** How many pages have been fetched. The footer needs it to tell "one page
   *  that happens to be everything" from "everything, after you asked for the
   *  rest", which want different words. */
  pages: number;
  loadMore: () => void;
  reload: () => void;
}

export function usePaged<T>(
  fetchPage: (cursor?: string) => Promise<Page<T>>,
  deps: unknown[] = [],
): Paged<T> {
  const [state, setState] = useState<{
    status: "loading" | "ready" | "error";
    items: T[];
    error: ApiError | null;
    cursor: string | null;
  }>({ status: "loading", items: [], error: null, cursor: null });
  const [loadingMore, setLoadingMore] = useState(false);
  const [moreError, setMoreError] = useState<string | null>(null);
  const [pages, setPages] = useState(1);
  const [nonce, setNonce] = useState(0);

  const alive = useRef(true);
  const run = useRef(fetchPage);
  run.current = fetchPage;

  useEffect(() => {
    alive.current = true;
    setState({ status: "loading", items: [], error: null, cursor: null });
    setMoreError(null);
    setLoadingMore(false);
    setPages(1);
    run
      .current()
      .then((page) => {
        if (alive.current) {
          setState({ status: "ready", items: page.items, error: null, cursor: page.nextCursor });
        }
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
          cursor: null,
        });
      });
    return () => {
      alive.current = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, nonce]);

  const loadMore = useCallback(() => {
    setState((s) => {
      if (s.cursor === null || s.status !== "ready") return s;
      setLoadingMore(true);
      setMoreError(null);
      run
        .current(s.cursor)
        .then((page) => {
          if (!alive.current) return;
          setState((prev) => ({
            ...prev,
            // Appended rather than replaced, and de-duplicated by identity is
            // deliberately NOT done here: these rows have no key this hook
            // knows about. A cursor on a creation timestamp cannot repeat a
            // row, which is why the routes use one instead of an offset.
            items: [...prev.items, ...page.items],
            cursor: page.nextCursor,
          }));
          setPages((n) => n + 1);
        })
        .catch((e: unknown) => {
          if (!alive.current) return;
          setMoreError(e instanceof Error ? e.message : "The next page did not load.");
        })
        .finally(() => {
          if (alive.current) setLoadingMore(false);
        });
      return s;
    });
  }, []);

  return {
    status: state.status,
    items: state.items,
    error: state.error,
    loadingMore,
    moreError,
    hasMore: state.cursor !== null,
    pages,
    loadMore,
    reload: () => setNonce((n) => n + 1),
  };
}

/**
 * The footer under a paginated table.
 *
 * It says the count it actually has and nothing more. With a cursor still
 * outstanding it says "at least", because the number on screen is a floor and
 * not a total; once the cursor is exhausted the number IS the total and it
 * says so plainly.
 *
 * Rendered only when there is something to say. A list of six rows that fits
 * on one page gets no footer, because "6 of 6" under six visible rows is
 * furniture.
 *
 * It does stay after the last page is fetched, and that is why it takes
 * `pages`. The first draft hid itself the moment `hasMore` went false, so the
 * "All 24 runs" line could never render: pressing Show more made the footer
 * vanish, which reads as the control breaking rather than as the list being
 * complete. A person who asked for the rest is owed the confirmation that they
 * now have it.
 */
export function PageFooter({
  count,
  noun,
  hasMore,
  pages,
  loadingMore,
  moreError,
  onMore,
}: {
  count: number;
  /** Singular. "run" becomes "1 run" and "12 runs". */
  noun: string;
  hasMore: boolean;
  pages: number;
  loadingMore: boolean;
  moreError: string | null;
  onMore: () => void;
}) {
  if (!hasMore && moreError === null && pages === 1) return null;
  const label = `${count.toLocaleString()} ${count === 1 ? noun : `${noun}s`}`;
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 border-t border-rule px-4 py-3">
      <p className="text-[12px] leading-5 text-dim">
        {hasMore ? `Showing the newest ${label}. There are more.` : `All ${label}.`}
      </p>
      <div className="flex flex-wrap items-center gap-3">
        {moreError ? (
          <p role="alert" className="text-[12px] leading-5 text-fail">
            {moreError}
          </p>
        ) : null}
        {hasMore ? (
          <Button onClick={onMore} busy={loadingMore}>
            {loadingMore ? "Loading" : moreError ? "Try again" : "Show more"}
          </Button>
        ) : null}
      </div>
    </div>
  );
}
