"use client";

import type { ApiError } from "@/lib/api";
import { Button } from "@/components/ui";

/**
 * The end of a list, saying whether it is the end.
 *
 * Three routes page and three screens read the first page and stopped, so an
 * organization with 200 runs saw 50 in a table that looked complete. A list
 * that quietly shows a third of the rows is worse than one that looks broken,
 * because the reader acts on it: somebody checking whether a run happened, or
 * whether an environment was torn down, got a confident wrong answer.
 *
 * So this renders in BOTH states rather than hiding itself when there is no
 * more. "All 24 runs." is the whole point: it is the only place the screen ever
 * says the list is complete, and a footer that disappears at the end can only
 * ever say the opposite. It refuses to invent a total it does not have, which
 * is why the two sentences are shaped differently rather than being one
 * sentence with a count in it.
 */
export function More({
  shown,
  noun,
  hasMore,
  busy,
  error,
  onMore,
}: {
  shown: number;
  /** Singular and plural, because "All 1 runs." is how a count reads when
   *  somebody templated it and never looked at a list with one row in it. */
  noun: { one: string; many: string };
  hasMore: boolean;
  busy: boolean;
  error: ApiError | null;
  onMore: () => void;
}) {
  const things = `${shown} ${shown === 1 ? noun.one : noun.many}`;
  return (
    <div className="border-t border-rule px-4 py-3">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        {hasMore ? (
          <>
            <Button busy={busy} onClick={onMore}>
              {busy ? "Loading" : error ? "Try again" : "Show more"}
            </Button>
            <span className="text-[12.5px] text-muted">
              Showing the newest {things}. There are more.
            </span>
          </>
        ) : (
          <span className="text-[12.5px] text-muted">All {things}.</span>
        )}
      </div>
      {error ? (
        <p role="alert" className="mt-2 text-[12px] leading-5 text-fail">
          {error.message}
        </p>
      ) : null}
    </div>
  );
}
