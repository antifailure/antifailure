"use client";

import { useEffect, useRef, useState } from "react";
import { useApi } from "@/lib/api";
import { may } from "@/lib/roles";
import { useSessionContext } from "@/components/session";
import { Button, Card, Empty, When } from "@/components/ui";
import {
  LatencyLadder,
  RunStatusBadge,
  StatusNote,
  ThroughputStats,
} from "@/components/workloads/primitives";
import { EvidenceList, RouteDeltas, Thresholds } from "@/components/workloads/results";
import {
  Command,
  PartialNotice,
  RunSkeleton,
  WorkloadError,
} from "@/components/workloads/states";
import { Provenance } from "@/components/workloads/primitives";
import {
  cancelRun,
  count,
  getRun,
  isRunning,
  isTerminal,
  retryRun,
  seconds,
  STATUS_FACTS,
  verdictContradiction,
  type RunDetail as Run,
} from "@/lib/workloads";

/**
 * How often a run that is still going is asked about again.
 *
 * Six seconds rather than one. A workload run is minutes long, so a second
 * would be sixty requests a minute per open tab to learn something that
 * changes on the order of tens of seconds, and every one of those requests
 * costs the same tenant's database the run itself is competing with.
 */
const POLL_MS = 6000;

/**
 * Ask again while the run is going, and stop the moment it is not.
 *
 * The stop condition is the reason this is written out rather than being a
 * bare setInterval. A poll that keeps running after the run has finished is a
 * request every six seconds forever on a tab somebody left open, and it is
 * invisible in every way except the load it makes.
 *
 * `document.hidden` is checked at each tick rather than subscribed to: a
 * backgrounded tab does not need the answer, and browsers already throttle the
 * timer, so this only has to avoid making the request rather than avoid being
 * woken.
 */
function usePoll(active: boolean, reload: () => void) {
  const fn = useRef(reload);
  fn.current = reload;
  useEffect(() => {
    if (!active) return;
    const id = setInterval(() => {
      if (typeof document !== "undefined" && document.hidden) return;
      fn.current();
    }, POLL_MS);
    return () => clearInterval(id);
  }, [active]);
}

/** A settings row for the run, so a number in the results can be read against
 *  what was asked for. */
function Settings({ run }: { run: Run }) {
  const rows: [string, string][] = [
    ["Execution", run.execution ?? "not recorded"],
    ["Version", run.version === null ? "not recorded" : `v${run.version}`],
    ["Scale", run.scale === null ? "not recorded" : `${run.scale}x production`],
    ["Duration asked for", seconds(run.durationSeconds)],
    ["Concurrency", count(run.concurrency)],
    ["Environment", run.env_id ?? "not recorded"],
  ];
  return (
    <dl className="grid gap-x-8 gap-y-4 px-4 py-4 sm:grid-cols-3">
      {rows.map(([k, v]) => (
        <div key={k} className="min-w-0">
          <dt className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">{k}</dt>
          <dd className="mt-1 break-words text-[13px] text-ink">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

/**
 * Which routes the run was allowed to touch.
 *
 * Unsafe routes are listed explicitly rather than being everything not named
 * safe. A workload aimed at a twin is allowed to do destructive things, and
 * which ones is a decision somebody made; presenting it as the complement of
 * a safe list would hide it behind arithmetic.
 */
function Routes({ run }: { run: Run }) {
  if (run.safeRoutes.length === 0 && run.unsafeRoutes.length === 0) {
    return (
      <Empty title="No route policy on this run">
        The run named neither safe nor unsafe routes, so it sent whatever its
        source contained.
      </Empty>
    );
  }
  return (
    <div className="grid gap-6 px-4 py-4 sm:grid-cols-2">
      {(
        [
          ["Safe", run.safeRoutes, "Sent freely."],
          ["Unsafe", run.unsafeRoutes, "Allowed to change state on the twin."],
        ] as const
      ).map(([label, routes, note]) => (
        <div key={label} className="min-w-0">
          <p className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
            {label}
          </p>
          <p className="mt-1 text-[12px] leading-5 text-dim">{note}</p>
          {routes.length === 0 ? (
            <p className="mt-2 text-[13px] text-muted">None named.</p>
          ) : (
            <ul className="mt-2 space-y-1">
              {routes.map((r) => (
                <li key={r} className="break-all font-mono text-[12.5px] text-ink">
                  {r}
                </li>
              ))}
            </ul>
          )}
        </div>
      ))}
    </div>
  );
}

export function RunView({ runId, onClose }: { runId: string; onClose: () => void }) {
  const session = useSessionContext();
  const csrf = session.data?.csrfToken ?? "";
  const canRun = may(session.data?.role, "load.run");

  const state = useApi<Run | null>(() => getRun(runId), [runId]);
  const run = state.status === "ready" ? state.data : null;

  const [acting, setActing] = useState<"cancel" | "retry" | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  // Poll only while this run is actually going. A terminal run's numbers do
  // not change, so asking again would be a request that can only ever return
  // what is already on the screen.
  usePoll(run !== null && isRunning(run.status), state.reload);

  if (state.status === "error" && state.error) {
    return (
      <WorkloadError
        error={state.error}
        retry={state.reload}
        back={<Button onClick={onClose}>Back to workloads</Button>}
      />
    );
  }
  if (state.status === "loading" || run === null) {
    // `ready` with a null body is reachable: the id in the address bar names
    // nothing, and the control plane answers with no row rather than a 404.
    // Treating that as a wait forever would be a screen that never resolves.
    if (state.status === "ready") {
      return (
        <Empty title="That run is not here" action={<Button onClick={onClose}>Back to workloads</Button>}>
          The address names a run that does not exist, or one that belongs to
          another organization.
        </Empty>
      );
    }
    return <RunSkeleton />;
  }

  const fact = STATUS_FACTS[run.status];
  const results = run.results;
  const contradiction =
    results === null ? null : verdictContradiction(run.status, results.thresholds);
  const partial =
    results !== null && (results.partial || isRunning(run.status) || run.status === "cancelled");

  return (
    <div className="space-y-6">
      <Card
        title="Run"
        note={run.id}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            {canRun && isRunning(run.status) && run.status !== "cancelling" ? (
              <Button
                variant="danger"
                busy={acting === "cancel"}
                onClick={async () => {
                  setActing("cancel");
                  setActionError(null);
                  setNote(null);
                  try {
                    await cancelRun(run.id, undefined, csrf);
                    setNote("Stop requested. The runner acknowledges it on its next check in.");
                    state.reload();
                  } catch (e) {
                    setActionError(e instanceof Error ? e.message : "That did not work.");
                  } finally {
                    setActing(null);
                  }
                }}
              >
                {acting === "cancel" ? "Stopping" : "Stop this run"}
              </Button>
            ) : null}
            {canRun && isTerminal(run.status) ? (
              <Button
                busy={acting === "retry"}
                onClick={async () => {
                  setActing("retry");
                  setActionError(null);
                  setNote(null);
                  try {
                    const { runId: next } = await retryRun(run.id, csrf);
                    setNote(
                      next
                        ? `Started again as ${next}.`
                        : "Started again. It appears in the run list once the runner takes it.",
                    );
                    state.reload();
                  } catch (e) {
                    setActionError(e instanceof Error ? e.message : "That did not work.");
                  } finally {
                    setActing(null);
                  }
                }}
              >
                {acting === "retry" ? "Starting" : "Run it again"}
              </Button>
            ) : null}
            <Button onClick={onClose}>Close</Button>
          </div>
        }
      >
        <div className="border-b border-rule px-4 py-4">
          <div className="flex flex-wrap items-center gap-3">
            <RunStatusBadge status={run.status} />
            {run.kind ? <Provenance kind={run.kind} /> : null}
            {run.definition_name ? (
              <span className="text-[13px] text-muted">{run.definition_name}</span>
            ) : null}
          </div>
          <StatusNote status={run.status} />
          {run.detail ? (
            <p className="mt-2 max-w-[70ch] text-[12.5px] leading-6 text-muted">{run.detail}</p>
          ) : null}
          <p className="mt-3 flex flex-wrap items-center gap-x-5 gap-y-1 text-[12px] text-dim">
            <span>
              Started <When value={run.started_at ?? run.created_at} />
            </span>
            {run.finished_at ? (
              <span>
                Finished <When value={run.finished_at} />
              </span>
            ) : null}
            {isRunning(run.status) ? (
              // Said in words rather than shown as something that moves. A
              // reader who wants it sooner has a button; a reader who does not
              // is not being waved at.
              <span>Rechecking every {POLL_MS / 1000} seconds</span>
            ) : null}
          </p>
          {actionError ? (
            <p role="alert" className="mt-3 text-[12.5px] leading-6 text-fail">
              {actionError}
            </p>
          ) : note ? (
            <p role="status" className="mt-3 text-[12.5px] leading-6 text-muted">
              {note}
            </p>
          ) : null}
        </div>
        <Settings run={run} />
      </Card>

      {results === null ? (
        <Card title="Results">
          {/* "yet" only while there is still a run to produce it. A blocked run
              is finished, and telling somebody its numbers are still coming is
              a promise the run has already broken. */}
          <Empty title={isRunning(run.status) ? "Nothing measured yet" : "Nothing was measured"}>
            {isRunning(run.status)
              ? "The runner has not reported a measurement yet. This fills in as the run reports."
              : "This run finished without storing a measurement. There is nothing here to read, which is not the same as a run that measured zero."}
          </Empty>
        </Card>
      ) : (
        <>
          <Card title="Throughput">
            {partial ? (
              <PartialNotice reason={run.status === "cancelled" ? "cancelled" : "running"} />
            ) : null}
            <ThroughputStats t={results.throughput} />
            {results.throughput.durationSeconds !== null ? (
              <p className="border-t border-rule px-4 py-2.5 text-[12px] text-dim">
                Measured over {seconds(results.throughput.durationSeconds)}.
              </p>
            ) : null}
          </Card>

          {results.percentiles.length > 0 ? (
            <Card title="Latency" note="Response time by percentile, slowest bar first in the tail.">
              <LatencyLadder percentiles={results.percentiles} />
            </Card>
          ) : (
            <Card title="Latency">
              <Empty title="No percentiles recorded">
                This run stored no latency distribution. An empty ladder is
                shown rather than one drawn at zero, because a p99 of nothing
                and an unmeasured p99 are different facts.
              </Empty>
            </Card>
          )}

          <Card
            title="Thresholds"
            note={
              fact.conclusive
                ? "What the run was asked to hold to."
                : "Read these against the status above. This run did not reach a verdict."
            }
          >
            {/* A contradiction between the headline verdict and the table under
                it is said out loud rather than left for a reader to notice.
                The console cannot correct a status the control plane computed,
                but presenting a pass over a broken or unevaluated threshold
                without comment is how a green run over nothing survives. */}
            {contradiction ? (
              <p
                role="alert"
                className="border-b border-rule bg-[rgba(138,90,0,0.07)] px-4 py-2.5 text-[12.5px] leading-6 text-warn"
              >
                {contradiction}
              </p>
            ) : null}
            <Thresholds thresholds={results.thresholds} />
          </Card>

          <Card
            title="Routes"
            note="Baseline against candidate, worst regression first."
          >
            <RouteDeltas routes={results.routes} />
          </Card>

          <Card title="Evidence">
            <EvidenceList evidence={results.evidence} />
          </Card>

          <Card
            title="Reproduce this run"
            note="As the control plane recorded it when it dispatched."
          >
            <Command command={results.command} />
          </Card>
        </>
      )}

      <Card title="Route policy">
        <Routes run={run} />
      </Card>
    </div>
  );
}
