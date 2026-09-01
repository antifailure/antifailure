"use client";

import { useState } from "react";
import { may } from "@/lib/roles";
import { useSessionContext } from "@/components/session";
import { Button, Card, Empty, When } from "@/components/ui";
import {
  LatencyLadder,
  Provenance,
  StateBadge,
  VerdictBadge,
  VerdictNote,
} from "@/components/load/primitives";
import {
  Assertions,
  Breaches,
  Errors,
  EvidenceList,
  Routes,
  Throughput,
} from "@/components/load/results";
import { Command, LoadError, PartialNotice, RunSkeleton } from "@/components/load/states";
import { StaleNotice, useInterval, useLive } from "@/components/load/polling";
import {
  STATE_FACTS,
  VERDICT_FACTS,
  cancelRun,
  count,
  getRun,
  isRunning,
  isTerminal,
  retryRun,
  seconds,
  verdictContradiction,
  type RunDetail,
} from "@/lib/load";

/**
 * How often a run still in flight is asked about again.
 *
 * Six seconds rather than one. A load run is minutes long, so a second would
 * be sixty requests a minute per open tab to learn something that moves on the
 * order of tens of seconds, and every one of those costs the same tenant's
 * database the run is already competing with.
 */
const POLL_MS = 6000;

/** What was asked for, so a number in the results can be read against it. */
function Settings({ run }: { run: RunDetail }) {
  const rows: [string, string][] = [
    ["Version", run.version === null ? "not recorded" : `v${run.version}`],
    ["Scale", run.scale === null ? "not recorded" : `${run.scale}x the source's rate`],
    ["Duration asked for", seconds(run.durationSeconds)],
    ["Concurrency", count(run.concurrency)],
    ["Environment", run.envId ?? "not recorded"],
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
 * These come from the manifest, not from the form that started the run.
 * Neither `af load run` nor `af load scenario` has a --safe or --unsafe flag,
 * so the safe list is a decision committed alongside the code rather than one
 * somebody makes per run, and the console shows it read only for that reason.
 *
 * Both lists are shown and the default is said outright. Every route is unsafe
 * until a safe pattern matches it, so showing the safe list alone would let a
 * reader assume the rest were sent too.
 */
function Policy({ run }: { run: RunDetail }) {
  if (run.safe.length === 0 && run.unsafe.length === 0) {
    return (
      <Empty title="No patterns in the manifest">
        This repository declares neither list under `load`. Every route is
        unsafe until a safe pattern matches it, so a manifest with no safe list
        sends nothing at all.
      </Empty>
    );
  }
  return (
    <div className="grid gap-6 px-4 py-4 sm:grid-cols-2">
      {(
        [
          ["Safe", run.safe, "Matched by one of these, so it was sent."],
          ["Unsafe", run.unsafe, "Named explicitly as allowed to change state on the twin."],
        ] as const
      ).map(([label, patterns, note]) => (
        <div key={label} className="min-w-0">
          <p className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">{label}</p>
          <p className="mt-1 text-[12px] leading-5 text-dim">{note}</p>
          {patterns.length === 0 ? (
            <p className="mt-2 text-[13px] text-muted">None.</p>
          ) : (
            <ul className="mt-2 space-y-1">
              {patterns.map((p) => (
                <li key={p} className="break-all font-mono text-[12.5px] text-ink">
                  {p}
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

  // useLive rather than useApi: a refresh must not blank a screen that is
  // already showing results, and a failed refresh must not throw them away.
  const state = useLive<RunDetail | null>(() => getRun(runId), [runId]);
  const run = state.data;

  const [acting, setActing] = useState<"cancel" | "retry" | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  /** Set the moment a stop is requested, so the button reflects it without
   *  waiting a poll interval. Rolled back if the server refuses. */
  const [stopping, setStopping] = useState(false);

  useInterval(run !== null && isRunning(run.state), POLL_MS, state.reload);

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
  if (run === null || run === undefined) {
    // Reachable: the id in the address bar names nothing and the control plane
    // answers with no row rather than a 404. Treating that as a wait would be a
    // screen that never resolves.
    return (
      <Empty title="That run is not here" action={<Button onClick={onClose}>Back to Load</Button>}>
        The address names a run that does not exist, or one that belongs to
        another organization.
      </Empty>
    );
  }

  const results = run.results;
  const contradiction = verdictContradiction(run.verdict, results?.assertions ?? []);
  const partial =
    results !== null && (results.partial || isRunning(run.state) || run.state === "cancelled");
  const conclusive = run.verdict !== null && VERDICT_FACTS[run.verdict].conclusive;

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
        title="Load run"
        note={run.id}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            {canRun && isRunning(run.state) && run.state !== "cancelling" ? (
              <Button
                variant="danger"
                busy={acting === "cancel"}
                disabled={stopping}
                onClick={async () => {
                  setActing("cancel");
                  setActionError(null);
                  setNote(null);
                  // Optimistic: the button reflects the request immediately
                  // rather than after the next poll, and rolls back on refusal.
                  setStopping(true);
                  try {
                    await cancelRun(run.id, csrf);
                    setNote("Stop requested. The runner acknowledges it on its next check in.");
                    state.reload();
                  } catch (e) {
                    setStopping(false);
                    setActionError(e instanceof Error ? e.message : "That did not work.");
                  } finally {
                    setActing(null);
                  }
                }}
              >
                {stopping ? "Stopping" : "Stop this run"}
              </Button>
            ) : null}
            {canRun && isTerminal(run.state) ? (
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
                        : "Started again. It appears in the list once the runner takes it.",
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
            {run.kind ? <Provenance kind={run.kind} /> : null}
            {run.sourceName ? <span className="text-[13px] text-muted">{run.sourceName}</span> : null}
          </div>
          <VerdictNote verdict={run.verdict} />
          {run.verdict === null ? (
            <p className="mt-2 max-w-[64ch] text-[12.5px] leading-6 text-muted">
              {STATE_FACTS[run.state].meaning}
            </p>
          ) : null}
          {run.detail ? (
            <p className="mt-2 max-w-[70ch] text-[12.5px] leading-6 text-muted">{run.detail}</p>
          ) : null}
          <p className="mt-3 flex flex-wrap items-center gap-x-5 gap-y-1 text-[12px] text-dim">
            <span>
              Started <When value={run.startedAt ?? run.createdAt} />
            </span>
            {run.finishedAt ? (
              <span>
                Finished <When value={run.finishedAt} />
              </span>
            ) : null}
            {isRunning(run.state) ? (
              // Said in words rather than shown as something that moves. A
              // reader who wants it sooner has a button; one who does not is
              // not being waved at.
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
          <Empty title={isRunning(run.state) ? "Nothing measured yet" : "Nothing was measured"}>
            {isRunning(run.state)
              ? "The runner has not reported a measurement yet. This fills in as it reports."
              : "This run stored no measurement. There is nothing here to read, which is not the same as a run that measured zero."}
          </Empty>
        </Card>
      ) : (
        <>
          <Card
            title="Throughput"
            note={results.origin ? `Mix compiled from ${results.origin}.` : undefined}
          >
            {partial ? (
              <PartialNotice reason={run.state === "cancelled" ? "cancelled" : "running"} />
            ) : null}
            <Throughput results={results} />
            {results.durationSeconds !== null ? (
              <p className="border-t border-rule px-4 py-2.5 text-[12px] text-dim">
                Measured over {seconds(results.durationSeconds)}.
              </p>
            ) : null}
          </Card>

          <Card title="Latency" note="The whole run together. The tail is what a user notices.">
            {results.overall.p50Ms === null &&
            results.overall.p95Ms === null &&
            results.overall.p99Ms === null ? (
              <Empty title="No distribution recorded">
                This run stored no percentiles. An empty ladder is shown rather
                than one drawn at zero, because a p99 of nothing and an
                unmeasured p99 are different facts.
              </Empty>
            ) : (
              <LatencyLadder latency={results.overall} />
            )}
          </Card>

          <Card
            title="Errors"
            note="By reason. A thousand timeouts and a thousand refused connections are the same number and different problems."
          >
            <Errors results={results} />
          </Card>

          <Card
            title="Assertions"
            note={
              conclusive
                ? "What the source asserted, and whether it held."
                : "Read these against the verdict above. This run reached no verdict."
            }
          >
            {/* A contradiction between the verdict and the table under it is
                said out loud rather than left to be noticed. The console cannot
                correct a verdict the engine computed, but a nightly corpus in
                this product once reported six passing workflows having never
                reached an agent, and presenting that combination quietly is how
                it survives. */}
            {contradiction ? (
              <p
                role="alert"
                className="border-b border-rule bg-[rgba(138,90,0,0.07)] px-4 py-2.5 text-[12.5px] leading-6 text-warn"
              >
                {contradiction}
              </p>
            ) : null}
            <Assertions assertions={results.assertions} />
            {results.breaches.length > 0 ? (
              <div className="border-t border-rule">
                <p className="px-4 pt-4 text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
                  Thresholds exceeded
                </p>
                <Breaches breaches={results.breaches} />
              </div>
            ) : null}
          </Card>

          <Card title="Routes" note="Against production's own p95, worst regression first.">
            <Routes routes={results.routes} />
          </Card>

          <Card title="Evidence">
            <EvidenceList evidence={results.evidence} />
          </Card>

          <Card title="Reproduce this run" note="As the control plane recorded it at dispatch.">
            <Command command={results.command} />
          </Card>
        </>
      )}

      <Card title="Route policy" note="From the manifest. Not a per-run setting: neither load command has a flag for it.">
        <Policy run={run} />
      </Card>
    </div>
  );
}
