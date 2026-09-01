"use client";

import { useState } from "react";
import { may } from "@/lib/roles";
import { useSessionContext } from "@/components/session";
import { Button, Card, CellLink, Empty, When } from "@/components/ui";
import {
  Fact,
  Facts,
  KindMark,
  LatencyLadder,
  StateBadge,
  VerdictBadge,
  VerdictNote,
} from "@/components/load/primitives";
import {
  CancelState,
  ErrorReasons,
  EvidenceList,
  RefusedRoutes,
  ResultSummary,
  Routes,
  Thresholds,
} from "@/components/load/results";
import { Command, LoadError, PartialNotice, RunSkeleton } from "@/components/load/states";
import { StaleNotice, useInterval, useLive } from "@/components/load/polling";
import {
  COMMAND_FACTS,
  KIND_FACTS,
  STATE_FACTS,
  cancelRun,
  duration,
  hasLatency,
  inspectRun,
  isRunning,
  retryRun,
  verdictContradiction,
  type RunDetail,
} from "@/lib/load";

/**
 * How often a run still in flight is asked about again.
 *
 * Six seconds rather than one. A run is minutes long, so a second would be
 * sixty requests a minute per open tab to learn something that moves on the
 * order of tens of seconds, and every one of those costs the same tenant's
 * database the run is already competing with.
 */
const POLL_MS = 6000;

/** Whether this kind of run measures traffic, which decides whether a latency
 *  ladder and an error breakdown are tables that apply or tables of dashes. */
function sendsTraffic(kind: string | null): boolean {
  return kind === "observed_load" || kind === "http_scenario";
}

/**
 * When each thing happened, and what has not happened yet.
 *
 * The deadline is here rather than hidden, because it is the difference
 * between a run that is late and a run that is over. A run passes it and
 * becomes abandoned, which is not a failure, and somebody watching a slow run
 * is owed the moment that will happen.
 */
function Timeline({ run }: { run: RunDetail["run"] }) {
  return (
    <Facts columns={3}>
      <Fact label="Requested">
        <When value={run.requestedAt} />
      </Fact>
      <Fact label="Dispatched">
        {run.dispatchedAt ? (
          <When value={run.dispatchedAt} />
        ) : (
          <span className="text-muted">GitHub was never asked</span>
        )}
      </Fact>
      <Fact label="Claimed by an engine">
        {run.acceptedAt ? (
          <When value={run.acceptedAt} />
        ) : (
          <span className="text-muted">not yet</span>
        )}
      </Fact>
      <Fact label="Started">
        {run.startedAt ? <When value={run.startedAt} /> : <span className="text-muted">not yet</span>}
      </Fact>
      <Fact label="Finished">
        {run.finishedAt ? (
          <When value={run.finishedAt} />
        ) : (
          <span className="text-muted">not yet</span>
        )}
      </Fact>
      <Fact label={isRunning(run.state) ? "Gives up at" : "Deadline was"}>
        <When value={run.deadlineAt} />
      </Fact>
    </Facts>
  );
}

/** Where the run came from and where it ran, which is what makes it
 *  reproducible by anybody other than the person who started it. */
function Provenance({ run }: { run: RunDetail["run"] }) {
  return (
    <Facts columns={3}>
      <Fact label="Environment">
        {run.envId ? <code className="break-all font-mono text-[12.5px]">{run.envId}</code> : "--"}
      </Fact>
      <Fact label="Repository">{run.repository ?? "--"}</Fact>
      <Fact label="Branch">
        {run.gitRef ? <code className="break-all font-mono text-[12.5px]">{run.gitRef}</code> : "--"}
      </Fact>
      <Fact label="Version">{run.version === null ? "--" : `v${run.version}`}</Fact>
      <Fact label="Attempt">
        {run.attempt === null ? "--" : run.attempt === 1 ? "The first" : `Number ${run.attempt}`}
      </Fact>
      <Fact label="Manifest">
        {run.manifestDigest ? (
          <code className="break-all font-mono text-[11.5px]">
            {run.manifestDigest.slice(0, 16)}
          </code>
        ) : (
          <span className="text-muted">not recorded</span>
        )}
      </Fact>
    </Facts>
  );
}

export function RunView({
  runId,
  onClose,
  onOpenRun,
  onOpenWorkload,
}: {
  runId: string;
  onClose: () => void;
  onOpenRun: (id: string) => void;
  onOpenWorkload: (slug: string) => void;
}) {
  const session = useSessionContext();
  const csrf = session.data?.csrfToken ?? "";
  const canRun = may(session.data?.role, "workloads.run");

  // useLive rather than useApi: a refresh must not blank a screen that is
  // already showing results, and a failed refresh must not throw them away.
  const state = useLive<RunDetail | null>(() => inspectRun(runId), [runId]);
  const detail = state.data;

  const [acting, setActing] = useState<"cancel" | "retry" | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  /** Set the moment a stop is requested, so the button reflects it without
   *  waiting a poll interval. Rolled back if the server refuses. */
  const [stopping, setStopping] = useState(false);

  useInterval(detail !== null && isRunning(detail.run.state), POLL_MS, state.reload);

  if (state.status === "error" && state.error) {
    return (
      <LoadError
        error={state.error}
        retry={state.reload}
        back={<Button onClick={onClose}>Back to Load</Button>}
      />
    );
  }
  if (state.status === "loading") return <RunSkeleton />;
  if (detail === null || detail === undefined) {
    return (
      <Empty title="That run is not here" action={<Button onClick={onClose}>Back to Load</Button>}>
        The address names a run that does not exist, or one that belongs to
        another organization.
      </Empty>
    );
  }

  const run = detail.run;
  const result = detail.result;
  const contradiction = verdictContradiction(run.verdict, detail.thresholds);
  const traffic = sendsTraffic(run.kind);
  const stopRequested = run.cancelRequestedAt !== null || stopping;

  return (
    <div className="space-y-6">
      {/* Above the cards, because it changes what every number under it means.
          Only rendered when a refresh has actually failed: a notice that
          appears every six seconds is one people stop reading. */}
      {state.refreshError ? (
        <StaleNotice
          message={state.refreshError}
          updatedAt={state.updatedAt}
          onRetry={state.reload}
          retrying={state.refreshing}
        />
      ) : null}

      <Card
        title="Run"
        note={run.id}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            {canRun && isRunning(run.state) ? (
              <Button
                variant="danger"
                busy={acting === "cancel"}
                disabled={stopRequested}
                onClick={async () => {
                  setActing("cancel");
                  setActionError(null);
                  setNote(null);
                  // Optimistic: the button reflects the request immediately
                  // rather than after the next poll, and rolls back on refusal.
                  setStopping(true);
                  try {
                    const stopped = await cancelRun({ runId: run.id }, csrf);
                    setNote(
                      stopped.stopped
                        ? "Nothing had claimed this run, so it is over now rather than waiting for a runtime to confirm."
                        : stopped.alreadyRequested
                          ? "A stop had already been asked for. This changed nothing."
                          : "Stop requested. A runtime confirms it below when it acts, and the request expires if none ever does.",
                    );
                    state.reload();
                  } catch (e) {
                    setStopping(false);
                    setActionError(e instanceof Error ? e.message : "That did not work.");
                  } finally {
                    setActing(null);
                  }
                }}
              >
                {stopRequested ? "Stopping" : "Stop this run"}
              </Button>
            ) : null}
            {canRun && !isRunning(run.state) && run.supersededBy === null ? (
              <Button
                busy={acting === "retry"}
                onClick={async () => {
                  setActing("retry");
                  setActionError(null);
                  setNote(null);
                  try {
                    const next = await retryRun(run.id, csrf);
                    setNote(
                      next.runId
                        ? `Started again as ${next.runId}. It runs the same version, which is what makes it an answer to whether this was a fluke.`
                        : "Started again. It appears in the list once an engine takes it.",
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
            <VerdictBadge verdict={run.verdict} />
            <StateBadge state={run.state} />
            {run.kind ? <KindMark kind={run.kind} /> : null}
            {run.workloadSlug ? (
              <button
                type="button"
                onClick={() => onOpenWorkload(run.workloadSlug as string)}
                className="text-[13px] text-ink underline decoration-[rgba(16,16,16,0.25)] underline-offset-4 hover:decoration-ink"
              >
                {run.workloadSlug}
              </button>
            ) : null}
          </div>

          {/* Both sentences, because state and verdict are two answers and
              neither implies the other. A run that succeeded and failed every
              threshold needs the second sentence to make sense of the first. */}
          <p className="mt-2 max-w-[70ch] text-[12.5px] leading-6 text-muted">
            {STATE_FACTS[run.state].meaning}
          </p>
          <VerdictNote verdict={run.verdict} />

          {run.detail ? (
            <p className="mt-2 max-w-[70ch] text-[12.5px] leading-6 text-muted">{run.detail}</p>
          ) : null}
          {run.failureCode ? (
            <p className="mt-2 text-[12.5px] leading-6 text-muted">
              Error code{" "}
              <code className="font-mono text-[12px] text-ink">{run.failureCode}</code>, as the
              engine reported it.
            </p>
          ) : null}

          {run.retryOf || run.supersededBy ? (
            <p className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-[12.5px] leading-6 text-muted">
              {run.retryOf ? (
                <span>
                  A retry of{" "}
                  <button
                    type="button"
                    onClick={() => onOpenRun(run.retryOf as string)}
                    className="font-mono text-[12px] text-ink underline decoration-[rgba(16,16,16,0.25)] underline-offset-4 hover:decoration-ink"
                  >
                    {run.retryOf}
                  </button>
                </span>
              ) : null}
              {run.supersededBy ? (
                <span>
                  Already retried, as{" "}
                  <button
                    type="button"
                    onClick={() => onOpenRun(run.supersededBy as string)}
                    className="font-mono text-[12px] text-ink underline decoration-[rgba(16,16,16,0.25)] underline-offset-4 hover:decoration-ink"
                  >
                    {run.supersededBy}
                  </button>
                  . Retry that one instead: two independent successors to one failure is a history
                  nobody can read.
                </span>
              ) : null}
            </p>
          ) : null}

          {isRunning(run.state) ? (
            // Said in words rather than shown as something that moves. A
            // reader who wants it sooner has a button; one who does not is not
            // being waved at.
            <p className="mt-3 text-[12px] text-dim">
              Rechecking every {POLL_MS / 1000} seconds.
            </p>
          ) : null}

          {actionError ? (
            <p role="alert" className="mt-3 max-w-[74ch] text-[12.5px] leading-6 text-fail">
              {actionError}
            </p>
          ) : note ? (
            <p role="status" className="mt-3 max-w-[74ch] text-[12.5px] leading-6 text-muted">
              {note}
            </p>
          ) : null}
        </div>
        <Provenance run={run} />
        <div className="border-t border-rule">
          <Timeline run={run} />
        </div>
      </Card>

      {detail.cancel ? (
        <Card title="The stop request" note="A stop is a command a runtime has to confirm.">
          <CancelState
            state={detail.cancel.state}
            outcome={detail.cancel.outcome}
            detail={detail.cancel.detail}
            requestedAt={detail.cancel.requestedAt}
            acknowledgedAt={detail.cancel.acknowledgedAt}
            label={COMMAND_FACTS[detail.cancel.state].label}
            meaning={COMMAND_FACTS[detail.cancel.state].meaning}
          />
        </Card>
      ) : null}

      {result === null ? (
        <Card title="Results">
          <Empty title={isRunning(run.state) ? "Nothing reported yet" : "Nothing was reported"}>
            {isRunning(run.state)
              ? "Nothing is written here until the run reaches an end, so this is empty for every run that is still going rather than partly filled in."
              : run.state === "abandoned"
                ? "No engine ever reported on this run, so there is nothing to read. That is a gap in the reporting and not a measurement of zero."
                : "This run reached an end and stored no measurement. There is nothing here to read, which is not the same as a run that measured zero."}
          </Empty>
        </Card>
      ) : (
        <>
          <Card
            title="What it measured"
            note={
              result.source
                ? `${KIND_FACTS[result.kind].measures} The mix was compiled from ${result.source}.`
                : KIND_FACTS[result.kind].measures
            }
          >
            {run.state === "cancelled" || run.state === "timed_out" ? (
              <PartialNotice reason={run.state} />
            ) : null}
            <ResultSummary result={result} />
            <RefusedRoutes routes={result.refusedRoutes} />
          </Card>

          {traffic ? (
            <Card title="Latency" note="The whole run together. The tail is what a user notices.">
              {hasLatency(result.latency) ? (
                <LatencyLadder latency={result.latency} />
              ) : (
                <Empty title="No distribution recorded">
                  This run stored no percentiles. An empty ladder is shown rather than one drawn at
                  zero, because a p99 of nothing and an unmeasured p99 are different facts.
                </Empty>
              )}
            </Card>
          ) : null}

          {traffic ? (
            <Card
              title="Errors"
              note="By reason. A thousand timeouts and a thousand refused connections are the same number and different problems."
            >
              <ErrorReasons result={result} />
            </Card>
          ) : null}

          <Card
            title="Thresholds"
            note={
              run.verdict === null
                ? "Read these against the verdict above. This run reached no verdict."
                : "What the manifest asserted, and whether it held."
            }
          >
            {/* A contradiction between the verdict and the table under it is
                said out loud rather than left to be noticed. The console cannot
                correct a verdict the control plane recorded, but a nightly
                corpus in this product once reported six passing workflows
                having never reached an agent, and presenting that combination
                quietly is how it survives. */}
            {contradiction ? (
              <p
                role="alert"
                className="border-b border-rule bg-[rgba(138,90,0,0.07)] px-4 py-2.5 text-[12.5px] leading-6 text-warn"
              >
                {contradiction}
              </p>
            ) : null}
            <Thresholds thresholds={detail.thresholds} />
          </Card>

          {traffic || detail.routes.length > 0 ? (
            <Card title="Routes" note="Against production's own p95, worst regression first.">
              <Routes routes={detail.routes} />
            </Card>
          ) : null}

          <Card title="Evidence" note="What the run left behind, and whether it can still be read.">
            <EvidenceList evidence={detail.evidence} />
          </Card>

          <Card
            title="Reproduce this run"
            note={
              result.durationMs === null
                ? undefined
                : `It took ${duration(result.durationMs)} the first time.`
            }
          >
            <Command command={run.reproduceCommand} />
          </Card>
        </>
      )}
    </div>
  );
}
