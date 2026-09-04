"use client";

import { type ReactNode } from "react";
import { ApiError } from "@/lib/api";
import { Bar, Button, Card, CommandBlock, TableSkeleton } from "@/components/ui";

/* -------------------------------------------------------------------------
 * Failure
 * ---------------------------------------------------------------------- */

/**
 * Why this did not load, told apart rather than lumped together.
 *
 * The shared `ErrorState` in ui.tsx splits two ways, forbidden and everything
 * else, which is right for the screens it was written for. This area needs
 * four, because the four have four different remedies and offering "Try again"
 * to somebody whose role is wrong is a button that exists to be refused:
 *
 *   denied        a role thing. Retrying will fail identically forever.
 *   disconnected  the request never arrived. Retrying is the entire remedy.
 *   missing       the name in the address bar is not a thing. Go back to the list.
 *   failed        the control plane answered and said no. Retrying may help.
 *
 * `disconnected` is the one worth having on its own. A thrown fetch arrives as
 * status 0 with code NETWORK, and without this branch that renders as "The
 * control plane answered 0", which is both untrue and useless.
 */
export function LoadError({
  error,
  retry,
  back,
  reading = "workloads and their runs",
  needs = "workloads.view",
}: {
  error: ApiError;
  retry?: () => void;
  back?: ReactNode;
  /** What the call that failed was fetching, and which permission it needs.
   *  Defaulted to the workloads themselves because that is most of this area,
   *  and overridden where it is not: the promotion screen reads the connected
   *  repositories, and telling somebody they need workloads.view when the
   *  refusal came from environments.view sends them to ask for the wrong
   *  thing. */
  reading?: string;
  needs?: string;
}) {
  const denied = error.status === 403 || error.code === "FORBIDDEN";
  const missing = error.status === 404 || error.code === "NOT_FOUND";
  const disconnected = error.status === 0 || error.code === "NETWORK";

  const title = denied
    ? `Your role cannot see ${reading}`
    : missing
      ? "That is not here"
      : disconnected
        ? "The control plane did not answer"
        : "That did not load";

  const body = denied
    ? `Reading ${reading} needs a role that holds ${needs}. An owner or an admin can change yours on the Members page.`
    : missing
      ? "The address names a workload or a run that does not exist, or one that belongs to another organization. A workload that has been archived is not here either."
      : disconnected
        ? "The request did not reach the control plane, so nothing is known about the state of your runs. This is a connection problem rather than an answer."
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
 * A wait shaped like a workload's detail, not a generic grey box.
 *
 * The point of a skeleton is that nothing moves when the data lands. This one
 * has the header block, the two-column fact grid and the table that the loaded
 * screen has, in the same order at the same heights, so the page does not
 * reflow under a reader who has already started looking at it. Static, like
 * every other skeleton in this console.
 */
export function WorkloadSkeleton() {
  // No role="status" on this wrapper. TableSkeleton carries one of its own, and
  // nesting two live regions made a screen reader announce "Loading" twice, as
  // "LoadingLoading the workload". One announcement, from the shared
  // component, is also what every other screen in this console does.
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
  // See WorkloadSkeleton: one live region, and it is TableSkeleton's.
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
 * A banner over numbers that cover less than was asked for.
 *
 * There used to be a third case here, a run still going whose results had
 * partly landed. It was deleted rather than kept because it can never happen:
 * nothing writes a result row before a terminal transition, so a running run
 * has no result at all and this banner had no way to be reached from that
 * state. A branch that cannot fire is a branch nobody can find the defect in.
 *
 * The two that remain are real and need different words. A cancelled run
 * stopped when somebody asked; a timed out one stopped because the engine ran
 * out of time. Both are dangerous read as a finished measurement, and neither
 * is dangerous once it is labelled.
 */
export function PartialNotice({ reason }: { reason: "cancelled" | "timed_out" }) {
  return (
    <p
      role="status"
      className="border-b border-rule bg-[rgba(138,90,0,0.07)] px-4 py-2.5 text-[12.5px] leading-6 text-warn"
    >
      {reason === "cancelled"
        ? "This run was stopped before it finished, so these numbers cover only the part that ran. They are not a measurement of the whole run."
        : "This run ran out of time, so these numbers cover only what it got through. They are not a measurement of the whole run."}
    </p>
  );
}

/* -------------------------------------------------------------------------
 * The reproducible command
 * ---------------------------------------------------------------------- */

/**
 * The command that reproduces this run.
 *
 * Shown exactly as the ENGINE reported it, and never rebuilt here from the
 * version's values. A console that assembles the command from what a body says
 * produces something that looks authoritative and drifts the first time a
 * default changes on either side, and the only reason to print a command at
 * all is that it is the same one.
 *
 * The copy button is a real button beside the code rather than an icon floating
 * over its corner, because the docs site shipped exactly that and the button
 * hung half outside the block over the paragraph beneath it.
 */
export function Command({ command }: { command: string | null }) {
  if (command === null) {
    return (
      <p className="px-4 py-4 text-[13px] leading-6 text-muted">
        No engine has reported a command for this run, so there is nothing here
        that is guaranteed to reproduce it. Rebuilding one from the version
        above would look authoritative and would not be.
      </p>
    );
  }

  return (
    <div className="px-4 py-4">
      <CommandBlock command={command} />
    </div>
  );
}

/* -------------------------------------------------------------------------
 * Refusal
 * ---------------------------------------------------------------------- */

/**
 * A control a role may not use, replaced by the reason rather than removed.
 *
 * Removing it is the usual choice and it is worse here: a viewer sent a link
 * to a workload who finds no way to run it cannot tell whether the product
 * lacks the feature or their role lacks the permission. Saying which is what
 * turns a dead end into a thing to go and ask for.
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
