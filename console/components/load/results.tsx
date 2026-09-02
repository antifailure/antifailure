"use client";

import { Badge, Empty, Row, Table, TableWrap, Td, Th, When } from "@/components/ui";
import { bytes } from "@/lib/format";
import { RouteCell, Stat } from "@/components/load/primitives";
import {
  AVAILABILITY_FACTS,
  REASON_NOTES,
  VERDICT_FACTS,
  count,
  duration,
  increase,
  isNumericMeasure,
  measured,
  ms,
  nothingWasChecked,
  percent,
  rate,
  rateShortfall,
  type EvidenceItem,
  type RouteMetric,
  type RunResult,
  type ThresholdVerdict,
  type Verdict,
} from "@/lib/load";

/* -------------------------------------------------------------------------
 * What the run measured
 * ---------------------------------------------------------------------- */

/**
 * The tiles for a run that sent traffic.
 *
 * The target and the achieved rate sit side by side because the gap between
 * them is the first thing worth looking at: a run that asked for 200 a second
 * and got 60 has already found something, before a single latency number is
 * read. Reporting the target alone is how a load test says everything was fine
 * while the queue grew.
 */
function Traffic({ result }: { result: RunResult }) {
  const short = rateShortfall(result);
  // Ten percent, because a load generator never lands exactly on its target
  // and a banner that fires on ordinary jitter is a banner people stop
  // reading. Below that it is scheduling noise; above it the application is
  // the thing that did not keep up.
  const missed = short !== null && short > 0.1;
  const errored = result.errorRate !== null && result.errorRate > 0;

  return (
    <>
      {missed ? (
        <p
          role="status"
          className="border-b border-rule bg-[rgba(138,90,0,0.07)] px-4 py-2.5 text-[12.5px] leading-6 text-warn"
        >
          This run asked for {rate(result.targetRate)} requests per second and achieved{" "}
          {rate(result.achievedRate)}, {percent(short)} short. The application did not keep up with
          the rate it was sent, so every latency figure below was measured under a queue.
        </p>
      ) : null}
      <dl className="grid grid-cols-2 gap-x-6 gap-y-5 px-4 py-4 sm:grid-cols-4">
        <Stat label="Requests" value={count(result.requests)} />
        <Stat
          label="Achieved rate"
          value={rate(result.achievedRate)}
          note={
            result.targetRate === null
              ? "requests per second"
              : `per second, against ${rate(result.targetRate)} asked for`
          }
          tone={missed ? "warn" : undefined}
        />
        <Stat label="Failed requests" value={count(result.failures)} tone={errored ? "fail" : undefined} />
        <Stat label="Error rate" value={percent(result.errorRate)} tone={errored ? "fail" : undefined} />
        {result.kind === "http_scenario" ? (
          <>
            <Stat label="Sessions" value={count(result.sessions)} />
            <Stat label="Iterations" value={count(result.iterations)} />
            <Stat
              label="Scheduled"
              value={duration(result.scheduledMs)}
              note="what the plan asked for"
            />
            <Stat label="Took" value={duration(result.durationMs)} />
          </>
        ) : (
          <Stat label="Took" value={duration(result.durationMs)} />
        )}
      </dl>
    </>
  );
}

/**
 * The tiles for a run that drove a browser.
 *
 * Five outcomes and not two. A real `af test` run returned nothing passed,
 * nothing failed and one unverified, because the persona could not be created:
 * with passed and failed alone a console draws that as a run with no failures,
 * which is the exit code zero over nothing defect this product has already
 * shipped once. The banner above says it outright when it happens.
 */
function Workflows({ result }: { result: RunResult }) {
  const empty = nothingWasChecked(result);
  const failed = (result.workflowsFailed ?? 0) > 0;
  const flaky = (result.workflowsFlaky ?? 0) > 0;

  return (
    <>
      {empty ? (
        <p
          role="alert"
          className="border-b border-rule bg-[rgba(138,90,0,0.07)] px-4 py-2.5 text-[12.5px] leading-6 text-warn"
        >
          This run had {count(result.workflows)} workflows to drive and none of them passed, failed
          or came back flaky. Nothing was checked, which is not the same as nothing being wrong. The
          blocked and unverified counts beside this say which it was.
        </p>
      ) : null}
      <dl className="grid grid-cols-2 gap-x-6 gap-y-5 px-4 py-4 sm:grid-cols-4">
        <Stat label="Workflows" value={count(result.workflows)} />
        <Stat label="Passed" value={count(result.workflowsPassed)} />
        <Stat label="Failed" value={count(result.workflowsFailed)} tone={failed ? "fail" : undefined} />
        <Stat label="Flaky" value={count(result.workflowsFlaky)} tone={flaky ? "warn" : undefined} />
        <Stat
          label="Blocked"
          value={count(result.workflowsBlocked)}
          note="never reached the application"
          tone={(result.workflowsBlocked ?? 0) > 0 ? "warn" : undefined}
        />
        <Stat
          label="Unverified"
          value={count(result.workflowsUnverified)}
          note="ran and proved nothing"
          tone={(result.workflowsUnverified ?? 0) > 0 ? "warn" : undefined}
        />
        <Stat label="Steps" value={count(result.steps)} />
        <Stat label="Took" value={duration(result.durationMs)} />
      </dl>
    </>
  );
}

/**
 * The tiles for a run that wandered.
 *
 * Two goal counts rather than one boolean, because a version selects up to
 * fifty goals and one boolean cannot answer for fifty. Which goal became
 * unreachable is the whole finding, and the thresholds table below carries it.
 */
function Wander({ result }: { result: RunResult }) {
  const missed =
    result.goals !== null && result.goalsReached !== null && result.goalsReached < result.goals;
  return (
    <dl className="grid grid-cols-2 gap-x-6 gap-y-5 px-4 py-4 sm:grid-cols-4">
      <Stat label="Goals" value={count(result.goals)} />
      <Stat
        label="Reached"
        value={count(result.goalsReached)}
        tone={missed ? "warn" : undefined}
        note={missed ? "the rest were not" : undefined}
      />
      <Stat
        label="Findings"
        value={count(result.findings)}
        note="places the application cost somebody effort"
        tone={(result.findings ?? 0) > 0 ? "warn" : undefined}
      />
      <Stat label="Took" value={duration(result.durationMs)} />
    </dl>
  );
}

/**
 * The measurements, by kind.
 *
 * A switch and not a superset. The schema carries a CHECK refusing a result of
 * one kind wearing another's columns, so a browser result has no request count
 * and an observed mix has no workflow count. Drawing all of them and letting
 * the empty ones render as dashes would present a column that does not apply
 * as a measurement that failed.
 */
export function ResultSummary({ result }: { result: RunResult }) {
  if (result.kind === "browser_workflow") return <Workflows result={result} />;
  if (result.kind === "exploration") return <Wander result={result} />;
  return <Traffic result={result} />;
}

/* -------------------------------------------------------------------------
 * Errors, by reason
 * ---------------------------------------------------------------------- */

/**
 * What failed, and why.
 *
 * The reason is the only part of an error count that tells somebody what to
 * do. A thousand timeouts and a thousand connection refusals are the same
 * number and completely different problems: one is an application too slow to
 * answer, the other is a service that never came up. A single "1,809 errors"
 * tile, which is what most load tools show, is the number without the finding.
 *
 * The set is closed apart from HTTP statuses, which arrive spelled as their
 * number. An unknown reason is rendered plainly rather than dropped, because a
 * reason the console has not been taught is still the truth about the run.
 */
export function ErrorReasons({ result }: { result: RunResult }) {
  if (result.errorReasons.length === 0) {
    return (
      <Empty title="No failed requests">
        Every request this run sent came back without a transport error and
        without a status at or above 500.
      </Empty>
    );
  }
  const total = result.errorReasons.reduce((a, e) => a + e.count, 0);
  return (
    <TableWrap>
      <Table>
        <thead>
          <tr>
            <Th>Reason</Th>
            <Th numeric>Requests</Th>
            <Th>Share of errors</Th>
            <Th>What it usually means</Th>
          </tr>
        </thead>
        <tbody>
          {result.errorReasons.map((e) => (
            <Row key={e.reason}>
              <Td mono>{e.reason}</Td>
              <Td label="Requests" numeric>
                {count(e.count)}
              </Td>
              <Td label="Share">
                <span className="flex items-center gap-3">
                  <span className="tnum w-[5ch] shrink-0 text-right text-[12.5px]">
                    {percent(total === 0 ? null : e.count / total)}
                  </span>
                  {/* One colour for every row: these are shares of a single
                      whole, so a hue per reason would encode the bar's own
                      length a second time and nothing else. */}
                  <span className="hidden h-2 w-16 shrink-0 sm:block" aria-hidden>
                    <span
                      className="block h-2 rounded-sm bg-fail"
                      style={{
                        width:
                          total === 0 ? "0%" : `${Math.max(3, (e.count / total) * 100).toFixed(2)}%`,
                      }}
                    />
                  </span>
                </span>
              </Td>
              <Td label="Means" className="max-w-[44ch]">
                {REASON_NOTES[e.reason] ?? (
                  <span className="text-dim">
                    {/^\d{3}$/.test(e.reason)
                      ? "The application answered with this status."
                      : "A reason this console has no note for. It came from the runner as written."}
                  </span>
                )}
              </Td>
            </Row>
          ))}
        </tbody>
      </Table>
    </TableWrap>
  );
}

/**
 * Routes the safe list refused.
 *
 * Shown rather than hidden, and this is the part worth defending. Every route
 * is unsafe until a safe pattern matches it, so a run that sent less than
 * somebody expected is usually a safe list that is too narrow rather than
 * traffic that was not there. Hiding the refusals makes that impossible to
 * diagnose from the console, and a blocked verdict impossible to explain.
 */
export function RefusedRoutes({ routes }: { routes: string[] }) {
  if (routes.length === 0) return null;
  return (
    <div className="border-t border-rule px-4 py-4">
      <p className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
        Refused by the safe list
      </p>
      <p className="mt-1 max-w-[74ch] text-[12.5px] leading-6 text-muted">
        {routes.length === 1 ? "One route was" : `${routes.length} routes were`} in this workload and
        were not sent, because no safe pattern in the manifest matched. That is the default and
        usually the right one: a generator that finds POST /checkout in an access log and runs it
        four hundred times charges four hundred cards.
      </p>
      <ul className="mt-3 space-y-1">
        {routes.map((r) => (
          <li key={r} className="break-all font-mono text-[12.5px] text-muted">
            {r}
          </li>
        ))}
      </ul>
    </div>
  );
}

/* -------------------------------------------------------------------------
 * Thresholds
 * ---------------------------------------------------------------------- */

/**
 * What the manifest asserted, what it concluded, and what it measured.
 *
 * The threshold and the observation are their own columns rather than being
 * left inside the sentence: a dashboard cannot chart a sentence, and "p95 was
 * 486ms against a limit of 400ms" is a number a reader has to parse back out
 * of prose every time.
 *
 * Both columns are blank for `every_request_succeeded` and `status_in`, which
 * are not numeric comparisons and carry no number, and the observation alone
 * is blank when nothing was sent. That is not the same as an observation of
 * zero, so it renders as absent rather than as 0.
 *
 * The scenario column appears only when a row carries one. A mix has no
 * scenarios, and a column of dashes on every row reads as data that failed to
 * load rather than as a column that does not apply.
 */
export function Thresholds({ thresholds }: { thresholds: ThresholdVerdict[] }) {
  if (thresholds.length === 0) {
    return (
      <Empty title="Nothing was asserted">
        This run measured what it did and judged none of it. A workload whose
        manifest declares no assertions produces numbers to read, not a verdict
        to act on.
      </Empty>
    );
  }
  const scoped = thresholds.some((t) => t.scenario !== null);
  // Only draw the numeric columns when at least one threshold has a number in
  // them. An assertion that only requires every request to have succeeded
  // would otherwise get two columns of dashes.
  const numeric = thresholds.some((t) => t.threshold !== null || t.observed !== null);

  return (
    <TableWrap>
      <Table>
        <thead>
          <tr>
            {scoped ? <Th>Scenario</Th> : null}
            <Th>Assertion</Th>
            <Th>Measure</Th>
            <Th>Scope</Th>
            {numeric ? <Th numeric>Threshold</Th> : null}
            {numeric ? <Th numeric>Observed</Th> : null}
            <Th>Verdict</Th>
            <Th>Detail</Th>
          </tr>
        </thead>
        <tbody>
          {thresholds.map((t, i) => (
            <Row key={`${t.scenario ?? ""}-${t.name}-${t.scope ?? ""}-${i}`}>
              {scoped ? (
                <Td label="Scenario">
                  {t.scenario ?? <span className="text-dim">whole run</span>}
                </Td>
              ) : null}
              <Td>{t.name}</Td>
              <Td label="Measure" mono className="text-dim">
                {t.measure ?? "--"}
              </Td>
              <Td label="Scope" mono>
                {t.scope ?? <span className="font-sans text-dim">everything it sent</span>}
              </Td>
              {numeric ? (
                <Td label="Threshold" numeric className="whitespace-nowrap">
                  {measured(t.measure, t.threshold)}
                </Td>
              ) : null}
              {numeric ? (
                <Td label="Observed" numeric className="whitespace-nowrap">
                  {/* "not measured" only where there was a number to measure.
                      every_request_succeeded and status_in have no threshold at
                      all, so an empty cell there is the column not applying
                      rather than a measurement that failed, and saying "not
                      measured" would report a gap that does not exist. */}
                  {t.observed !== null ? (
                    measured(t.measure, t.observed)
                  ) : isNumericMeasure(t.measure) ? (
                    <span className="text-dim">not measured</span>
                  ) : (
                    "--"
                  )}
                </Td>
              ) : null}
              <Td label="Verdict">
                <ThresholdVerdictBadge verdict={t.verdict} />
              </Td>
              <Td label="Detail" className="max-w-[40ch]">
                {t.detail ?? "--"}
              </Td>
            </Row>
          ))}
        </tbody>
      </Table>
    </TableWrap>
  );
}

/**
 * A threshold's verdict, in the product's five words.
 *
 * `flaky`, `blocked` and `unverified` are drawn apart from a failure and never
 * as a pass, exactly as they are on a run. An assertion about requests that
 * were never sent has not held.
 */
function ThresholdVerdictBadge({ verdict }: { verdict: Verdict }) {
  if (verdict === "pass") return <Badge tone="pass">Held</Badge>;
  if (verdict === "fail") return <Badge tone="fail">Broke</Badge>;
  return <Badge tone="warn">{VERDICT_FACTS[verdict].label}</Badge>;
}

/* -------------------------------------------------------------------------
 * Route level differences
 * ---------------------------------------------------------------------- */

/**
 * How much slower each route is than production, drawn from a centre line.
 *
 * A diverging encoding, which is the right family because the quantity has a
 * meaningful zero and two opposite directions. The control plane records a
 * RATIO against a baseline p95, not a millisecond delta, so this shows a ratio.
 *
 * The colour is never the signal on its own, and that is a measurement rather
 * than a principle. This console's pass green and fail red separate by a Delta
 * E of 26.8 to normal vision and 4.0 under deuteranopia, so to a red green
 * colourblind reader the two poles are very nearly the same colour. The signed
 * number carries the direction, the bar's side carries it again, and the
 * colour agrees with both.
 */
function DeltaBar({ ratio, largest }: { ratio: number | null; largest: number }) {
  if (ratio === null || largest === 0) return <span className="block h-2 w-full" aria-hidden />;
  const share = Math.min(1, Math.abs(ratio) / largest);
  const slower = ratio > 0;
  return (
    <span className="relative block h-2 w-full" aria-hidden>
      {/* The centre line stays under every bar, so "no change" has a position
          on the axis rather than being an empty cell. */}
      <span className="absolute inset-y-0 left-1/2 w-px -translate-x-1/2 bg-[rgba(16,16,16,0.18)]" />
      <span
        className={`absolute top-0 h-2 rounded-sm ${slower ? "bg-fail" : "bg-pass"}`}
        style={
          slower
            ? { left: "50%", width: `${(share * 50).toFixed(2)}%` }
            : { right: "50%", width: `${(share * 50).toFixed(2)}%` }
        }
      />
    </span>
  );
}

/** The comparison in words. This is what a reader who cannot see the bar gets,
 *  so it says the direction outright rather than relying on a sign. */
function Delta({ r }: { r: RouteMetric }) {
  const ratio = increase(r);
  if (ratio === null) {
    return (
      <span className="text-dim">
        no baseline
        <span className="sr-only">, so this route was not compared</span>
      </span>
    );
  }
  if (ratio === 0) return <span className="text-muted">no change</span>;
  const slower = ratio > 0;
  return (
    <span className={slower ? "text-fail" : "text-pass"}>
      {percent(Math.abs(ratio))} {slower ? "slower" : "faster"}
    </span>
  );
}

/**
 * Every route, worst regression first.
 *
 * The order is the finding. Sorted by name, a reader has to read all of it to
 * learn which change matters; sorted by movement, the answer is the first row.
 * Routes with no baseline sink to the bottom rather than sorting as though
 * they had not moved, which is the same distinction the engine draws when it
 * says a route with no baseline is never a breach.
 *
 * The scenario column appears only when a row carries one. Two scenarios in a
 * run can send the same route and their two p95 values do not average into a
 * p95, so the pair is the identity of the row and both halves have to be
 * visible when both exist.
 */
export function Routes({ routes }: { routes: RouteMetric[] }) {
  if (routes.length === 0) {
    return (
      <Empty title="No per route measurement">
        This run stored no route breakdown. That is not the same as a run in
        which no route was touched.
      </Empty>
    );
  }

  const scoped = routes.some((r) => r.scenario !== null);
  const sorted = [...routes].sort((a, b) => {
    const ia = increase(a);
    const ib = increase(b);
    if (ia === null && ib === null) return a.route.localeCompare(b.route);
    if (ia === null) return 1;
    if (ib === null) return -1;
    return ib - ia;
  });
  const largest = sorted.reduce((m, r) => {
    const i = increase(r);
    return i === null ? m : Math.max(m, Math.abs(i));
  }, 0);
  const compared = sorted.filter((r) => increase(r) !== null).length;

  return (
    <>
      <TableWrap>
        <Table>
          <thead>
            <tr>
              {scoped ? <Th>Scenario</Th> : null}
              <Th>Route</Th>
              <Th numeric>Sent</Th>
              <Th numeric>Errors</Th>
              <Th numeric>p95</Th>
              <Th numeric>Baseline p95</Th>
              <Th>Against baseline</Th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((r) => (
              <Row key={`${r.scenario ?? ""} ${r.route}`}>
                {scoped ? (
                  <Td label="Scenario">{r.scenario ?? <span className="text-dim">the mix</span>}</Td>
                ) : null}
                <Td mono>
                  <RouteCell route={r.route} />
                </Td>
                <Td label="Sent" numeric>
                  {count(r.sent)}
                </Td>
                <Td label="Errors" numeric>
                  {r.errors !== null && r.errors > 0 ? (
                    <span className="text-fail">{count(r.errors)}</span>
                  ) : (
                    count(r.errors)
                  )}
                </Td>
                <Td label="p95" numeric className="whitespace-nowrap">
                  {ms(r.latency.p95Ms)}
                </Td>
                <Td label="Baseline p95" numeric className="whitespace-nowrap">
                  {r.baselineP95Ms === null ? (
                    <span className="text-dim">--</span>
                  ) : (
                    ms(r.baselineP95Ms)
                  )}
                </Td>
                <Td label="Against baseline">
                  {/* The number leads and the bar follows, so a reader who
                      cannot see the bar's colour or its side has already been
                      told the direction in words. */}
                  <span className="flex items-center gap-3">
                    <span className="tnum whitespace-nowrap text-[12.5px]">
                      <Delta r={r} />
                    </span>
                    <span className="hidden w-16 shrink-0 sm:block">
                      <DeltaBar ratio={increase(r)} largest={largest} />
                    </span>
                  </span>
                </Td>
              </Row>
            ))}
          </tbody>
        </Table>
      </TableWrap>
      {compared < sorted.length ? (
        <p className="border-t border-rule px-4 py-2.5 text-[12px] leading-5 text-dim">
          {sorted.length - compared} of {sorted.length} routes have no production baseline, so they
          carry no comparison and can never count as a regression. They are listed last.
        </p>
      ) : null}
    </>
  );
}

/* -------------------------------------------------------------------------
 * Evidence
 * ---------------------------------------------------------------------- */

/**
 * What the run kept, and whether it can still be fetched.
 *
 * Three states and not a boolean, because "it is at /home/runner/work/... on a
 * machine that no longer exists" is a useful sentence and a broken link is
 * not. Reports in this product have carried exactly those paths, and a console
 * that renders one as a link sends somebody to a 404 and blames itself. So the
 * locator is shown as text, never as a link, and the availability says what it
 * is a locator FOR.
 */
export function EvidenceList({ evidence }: { evidence: EvidenceItem[] }) {
  if (evidence.length === 0) {
    return (
      <Empty title="No evidence recorded">
        This run stored nothing. Retention is a policy decision, so an empty
        list can mean the run produced nothing or that what it produced was
        never recorded here.
      </Empty>
    );
  }
  return (
    <TableWrap>
      <Table>
        <thead>
          <tr>
            <Th>Kind</Th>
            <Th>What it is</Th>
            <Th>Where</Th>
            <Th numeric>Size</Th>
            <Th>Availability</Th>
          </tr>
        </thead>
        <tbody>
          {evidence.map((e) => {
            const fact = AVAILABILITY_FACTS[e.availability];
            return (
              <Row key={`${e.kind}:${e.locator}`}>
                <Td mono>{e.kind}</Td>
                <Td label="What it is">{e.label ?? "--"}</Td>
                <Td label="Where" mono className="max-w-[40ch]">
                  <span className="block break-all">{e.locator}</span>
                </Td>
                <Td label="Size" numeric>
                  {bytes(e.sizeBytes)}
                </Td>
                <Td label="Availability">
                  <span className="flex flex-col gap-1">
                    <span>
                      <Badge tone={fact.fetchable ? "pass" : "neutral"}>{fact.label}</Badge>
                    </span>
                    <span className="max-w-[36ch] text-[11.5px] leading-5 text-dim">
                      {fact.meaning}
                    </span>
                  </span>
                </Td>
              </Row>
            );
          })}
        </tbody>
      </Table>
    </TableWrap>
  );
}

/* -------------------------------------------------------------------------
 * Where a stop got to
 * ---------------------------------------------------------------------- */

/**
 * A cancellation that a runtime has not confirmed.
 *
 * The control plane cannot reach a runtime, so a stop is a durable command
 * with a deadline rather than a flag on the row. `expired` is the one that
 * matters: it means nothing acknowledged the stop before the deadline, and the
 * run may still be going out there. A console that showed a cancelled run and
 * nothing else would be the same silent nothing the old teardown was.
 */
export function CancelState({
  state,
  outcome,
  detail,
  requestedAt,
  acknowledgedAt,
  label,
  meaning,
}: {
  state: string;
  outcome: string | null;
  detail: string | null;
  requestedAt: string | null;
  acknowledgedAt: string | null;
  label: string;
  meaning: string;
}) {
  return (
    <div className="px-4 py-4">
      <div className="flex flex-wrap items-center gap-3">
        <Badge tone={state === "acknowledged" ? "neutral" : state === "failed" || state === "expired" ? "fail" : "warn"}>
          {label}
        </Badge>
        <span className="text-[12px] text-dim">
          Requested <When value={requestedAt} />
        </span>
        {acknowledgedAt ? (
          <span className="text-[12px] text-dim">
            Confirmed <When value={acknowledgedAt} />
          </span>
        ) : null}
      </div>
      <p className="mt-2 max-w-[70ch] text-[12.5px] leading-6 text-muted">{meaning}</p>
      {outcome ? (
        <p className="mt-2 max-w-[70ch] text-[12.5px] leading-6 text-ink">
          The runtime reported: {outcome}
        </p>
      ) : null}
      {detail ? (
        <p className="mt-1 max-w-[70ch] text-[12.5px] leading-6 text-muted">{detail}</p>
      ) : null}
    </div>
  );
}
