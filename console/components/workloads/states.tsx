"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { ApiError } from "@/lib/api";
import { Bar, Button, Card, TableSkeleton } from "@/components/ui";

/* -------------------------------------------------------------------------
 * Failure
 * ---------------------------------------------------------------------- */

/**
 * Why this did not load, told apart rather than lumped together.
 *
 * The shared `ErrorState` in ui.tsx splits two ways, forbidden and everything
 * else, which is right for the screens it was written for. The Studio needs
 * four, because the four have four different remedies and offering "Try again"
 * to somebody whose role is wrong is a button that exists to be refused:
 *
 *   denied        a role thing. Retrying will fail identically forever.
 *   disconnected  the request never arrived. Retrying is the entire remedy.
 *   missing       the id in the address bar is not a thing. Go back to the list.
 *   failed        the control plane answered and said no. Retrying may help.
 *
 * `disconnected` is the one worth having on its own. `useApi` turns a thrown
 * fetch into status 0 with code NETWORK, and without this branch that renders
 * as "The control plane answered 0", which is both untrue and useless.
 */
export function WorkloadError({
  error,
  retry,
  back,
}: {
  error: ApiError;
  retry?: () => void;
  back?: ReactNode;
}) {
  const denied = error.status === 403 || error.code === "FORBIDDEN";
  const missing = error.status === 404 || error.code === "NOT_FOUND";
  const disconnected = error.status === 0 || error.code === "NETWORK";

  const title = denied
    ? "Your role cannot see workloads"
    : missing
      ? "That workload is not here"
      : disconnected
        ? "The control plane did not answer"
        : "That did not load";

  const body = denied
    ? "Reading workloads needs a role that holds environments.view. An owner or an admin can change yours on the Members page."
    : missing
      ? "The address names a definition or a run that does not exist, or that belongs to another organization. It may have been deleted."
      : disconnected
        ? "The request did not reach the control plane, so nothing is known about the state of your workloads. This is a connection problem rather than an answer."
        : error.message;

  return (
    <div className="px-6 py-12 text-center" role="alert">
      <p className="text-[14px] font-medium text-ink">{title}</p>
      <p className="mx-auto mt-2 max-w-[54ch] text-[13px] leading-6 text-muted">{body}</p>
      {/* One recovery action, and only the one that can work. */}
      <div className="mt-5 flex flex-wrap justify-center gap-2">
        {retry && !denied && !missing ? (
          <Button onClick={retry} variant="primary">
            Try again
          </Button>
        ) : null}
        {back}
      </div>
    </div>
  );
}

/* -------------------------------------------------------------------------
 * Waiting
 * ---------------------------------------------------------------------- */

/**
 * A wait shaped like a definition detail, not a generic grey box.
 *
 * The point of a skeleton is that nothing moves when the data lands. This one
 * has the header block, the two-column fact grid and the table that the loaded
 * screen has, in the same order at the same heights, so the page does not
 * reflow under a reader who has already started looking at it. Static, like
 * every other skeleton in this console.
 */
export function DefinitionSkeleton() {
  // No role="status" on this wrapper. TableSkeleton carries one of its own, and
  // nesting two live regions made a screen reader announce "Loading" twice, as
  // "LoadingLoading the workload". One announcement, from the shared component,
  // is also what every other screen in this console does.
  return (
    <div className="space-y-6">
      <div className="rounded-lg border border-rule bg-card">
        <div className="border-b border-rule px-4 py-4">
          <Bar className="h-3 w-24" />
          <Bar className="mt-2.5 h-2.5 w-full max-w-[52ch]" />
          <Bar className="mt-1.5 h-2.5 w-full max-w-[40ch]" />
        </div>
        <div className="grid gap-x-8 gap-y-4 px-4 py-4 sm:grid-cols-2">
          {Array.from({ length: 4 }).map((_, i) => (
            <div key={i}>
              <Bar className="h-2 w-16" />
              <Bar className="mt-2 h-3 w-32" />
            </div>
          ))}
        </div>
        <TableSkeleton rows={4} cols={3} />
      </div>
    </div>
  );
}

/** A wait shaped like the run detail: a status line, four stat tiles, then
 *  the result tables. */
export function RunSkeleton() {
  // See DefinitionSkeleton: one live region, and it is TableSkeleton's.
  return (
    <div className="space-y-6">
      <div className="rounded-lg border border-rule bg-card">
        <div className="border-b border-rule px-4 py-3">
          <Bar className="h-3.5 w-28" />
        </div>
        <div className="grid grid-cols-2 gap-x-6 gap-y-5 px-4 py-4 sm:grid-cols-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <div key={i}>
              <Bar className="h-2 w-14" />
              <Bar className="mt-2 h-5 w-20" />
            </div>
          ))}
        </div>
      </div>
      <div className="rounded-lg border border-rule bg-card">
        <div className="border-b border-rule px-4 py-3">
          <Bar className="h-3.5 w-24" />
        </div>
        <TableSkeleton rows={4} cols={4} />
      </div>
    </div>
  );
}

/* -------------------------------------------------------------------------
 * Partial
 * ---------------------------------------------------------------------- */

/**
 * A banner over numbers that are not final.
 *
 * Two ways to arrive here and they need different words. A run still going has
 * results that will change; a cancelled run has results that will not, but
 * that cover less than was asked for. Both are dangerous read as a finished
 * measurement, and neither is dangerous once it is labelled.
 */
export function PartialNotice({ reason }: { reason: "running" | "cancelled" }) {
  return (
    <p
      role="status"
      className="border-b border-rule bg-[rgba(138,90,0,0.07)] px-4 py-2.5 text-[12.5px] leading-6 text-warn"
    >
      {reason === "running"
        ? "These numbers cover the part of the run that has landed so far. They will change while it is still going."
        : "This run was cancelled, so these numbers cover only the part that ran. They are not a measurement of the whole workload."}
    </p>
  );
}

/* -------------------------------------------------------------------------
 * The reproducible command
 * ---------------------------------------------------------------------- */

/**
 * The command that reproduces this run.
 *
 * Shown exactly as the control plane recorded it at dispatch, and never
 * rebuilt here from the form's values. A console that assembles the command
 * from what the fields say produces something that looks authoritative and
 * drifts the first time a default changes on either side, and the only reason
 * to print a command at all is that it is the same one.
 *
 * The copy button is a real button beside the code rather than an icon floating
 * over its corner, because the docs site shipped exactly that and the button
 * hung half outside the block over the paragraph beneath it.
 */
export function Command({ command }: { command: string | null }) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
    },
    [],
  );

  if (command === null) {
    return (
      <p className="px-4 py-4 text-[13px] leading-6 text-muted">
        The control plane did not record a command for this run, so there is
        nothing here that is guaranteed to reproduce it. Rebuilding one from the
        settings above would look authoritative and would not be.
      </p>
    );
  }

  return (
    <div className="px-4 py-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        {/* overflow-x-auto by hand, NOT the shared .scroll-x helper. That
            class turns itself off below 640px (globals.css sets it to
            overflow-x: visible there) because at that width the tables it was
            written for stop scrolling and stack instead. A command block does
            not stack, so borrowing the class clipped a long command dead at
            the card edge on a phone: no scrollbar, no ellipsis, just a
            sentence that stopped. */}
        <pre className="min-w-0 flex-1 overflow-x-auto rounded-md border border-rule bg-[rgba(16,16,16,0.03)] px-3 py-2.5 font-mono text-[12.5px] leading-6 text-ink">
          <code>{command}</code>
        </pre>
        <Button
          onClick={() => {
            // Not every browser and not every context has the clipboard API:
            // it needs a secure origin, and a control plane on plain http over
            // a LAN is a real way this console gets used. Failing quietly and
            // leaving the button saying "Copy" is correct, because the command
            // is selectable text either way.
            void navigator.clipboard
              ?.writeText(command)
              .then(() => {
                setCopied(true);
                if (timer.current) clearTimeout(timer.current);
                timer.current = setTimeout(() => setCopied(false), 2000);
              })
              .catch(() => undefined);
          }}
        >
          {copied ? "Copied" : "Copy"}
        </Button>
      </div>
      {/* aria-live rather than a tooltip, so the confirmation reaches somebody
          who is not looking at the button they just pressed. */}
      <span aria-live="polite" className="sr-only">
        {copied ? "Command copied to the clipboard" : ""}
      </span>
    </div>
  );
}

/* -------------------------------------------------------------------------
 * Refusal
 * ---------------------------------------------------------------------- */

/**
 * A control a role may not use, replaced by the reason rather than removed.
 *
 * Removing it is the usual choice and it is worse here: a viewer who has been
 * sent a link to a workload and finds no way to run it cannot tell whether the
 * product lacks the feature or their role lacks the permission. Saying which
 * is what turns a dead end into a thing to go and ask for.
 */
export function Denied({ what }: { what: string }) {
  return (
    <Card title={what}>
      <p className="px-4 py-4 text-[13px] leading-6 text-muted">
        Your role cannot do this. An owner or an admin can change it on the
        Members page.
      </p>
    </Card>
  );
}
