"use client";

import { Badge, Empty, Row, Table, TableWrap, Td, Th } from "@/components/ui";
import { RouteCell, Stat } from "@/components/load/primitives";
import {
  REASON_NOTES,
  VERDICT_FACTS,
  count,
  increase,
  isNumericMeasure,
  measured,
  ms,
  percent,
  rate,
  rateShortfall,
  type AssertionResult,
  type Breach,
  type Evidence,
  type Results,
  type RouteResult,
  type Verdict,
} from "@/lib/load";

/* -------------------------------------------------------------------------
 * Throughput
 * ---------------------------------------------------------------------- */

/**
 * What the run aimed for and what it achieved.
 *
 * The target and the achieved rate sit side by side because the gap between
 * them is the first thing worth looking at, and the engine's own field comment
 * says why: "Reporting the target instead is how a load test says everything
 * was fine while the queue grew." A run that asked for 200 a second and got 60
 * has already found something, before a single latency number is read.
 *
 * The shortfall is called out in words under the tile rather than left for a
 * reader to compute from two figures.
 */
export function Throughput({ results }: { results: Results }) {
  const short = rateShortfall(results);
  // Ten percent, because a load generator never lands exactly on its target
  // and a banner that fires on ordinary jitter is a banner people stop
  // reading. Below that it is scheduling noise; above it the application is
  // the thing that did not keep up.
  const missed = short !== null && short > 0.1;
  const errored = results.errorRate !== null && results.errorRate > 0;

  return (
    <>
      {missed ? (
        <p
          role="status"
          className="border-b border-rule bg-[rgba(138,90,0,0.07)] px-4 py-2.5 text-[12.5px] leading-6 text-warn"
        >
          This run asked for {rate(results.targetRate)} requests per second and achieved{" "}
          {rate(results.rate)}, {percent(short)} short. The application did not keep up with the
          rate it was sent, so every latency figure below was measured under a queue.
        </p>
      ) : null}
      <dl className="grid grid-cols-2 gap-x-6 gap-y-5 px-4 py-4 sm:grid-cols-4">
        <Stat label="Requests" value={count(results.sent)} />
        <Stat
          label="Achieved rate"
          value={rate(results.rate)}
          note={
            results.targetRate === null
              ? "requests per second"
              : `per second, against ${rate(results.targetRate)} asked for`
          }
          tone={missed ? "warn" : undefined}
        />
        <Stat
          label="Errors"
          value={count(results.errors.reduce((a, e) => a + e.count, 0))}
          tone={errored ? "fail" : undefined}
        />
        <Stat label="Error rate" value={percent(results.errorRate)} tone={errored ? "fail" : undefined} />
      </dl>
    </>
  );
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
 * The engine's `classify()` produces a closed set of six, but an unknown
 * reason is rendered plainly rather than dropped, because a reason the console
 * has not been taught is still the truth about the run.
 */
export function Errors({ results }: { results: Results }) {
  if (results.errors.length === 0) {
    return (
      <Empty title="No failed requests">
        Every request the run sent came back without a transport error and
        without a status at or above 400.
      </Empty>
    );
  }
  const total = results.errors.reduce((a, e) => a + e.count, 0);
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
          {results.errors.map((e) => (
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
                      style={{ width: total === 0 ? "0%" : `${Math.max(3, (e.count / total) * 100).toFixed(2)}%` }}
                    />
                  </span>
                </span>
              </Td>
              <Td label="Means" className="max-w-[44ch]">
                {REASON_NOTES[e.reason] ?? (
                  <span className="text-dim">
                    A reason this console has no note for. It came from the runner as written.
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

/* -------------------------------------------------------------------------
 * Assertions
 * ---------------------------------------------------------------------- */

/**
 * What the scenario asserted, what it concluded, and what it measured.
 *
 * The threshold and the observation are their own columns rather than being
 * left inside the sentence. The engine added them for exactly this reason: a
 * dashboard cannot chart a sentence, and "p95 was 486ms against a limit of
 * 400ms" is a number a reader has to parse back out of prose every time.
 *
 * Both columns are blank for `every_request_succeeded` and `status_in`, which
 * are not numeric comparisons and carry no number, and the observation alone
 * is blank when nothing was sent. That is not the same as an observation of
 * zero, so it renders as absent rather than as 0.
 *
 * The verdict is the product's four words, the same ones a run carries.
 */
export function Assertions({ assertions }: { assertions: AssertionResult[] }) {
  if (assertions.length === 0) {
    return (
      <Empty title="Nothing was asserted">
        This run measured the traffic and judged nothing. A source with no
        assertions produces numbers to read, not a verdict to act on.
      </Empty>
    );
  }
  // Only draw the numeric columns when at least one assertion has a number in
  // them. A scenario asserting only that every request succeeded would
  // otherwise get two columns of dashes, which reads as data that failed to
  // load rather than as a measure that has no number.
  const numeric = assertions.some((a) => a.threshold !== null || a.observed !== null);
  return (
    <TableWrap>
      <Table>
        <thead>
          <tr>
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
          {assertions.map((a, i) => (
            <Row key={`${a.name}-${i}`}>
              <Td>{a.name}</Td>
              <Td label="Measure" mono className="text-dim">
                {a.measure ?? "--"}
              </Td>
              <Td label="Scope" mono>
                {a.scope ?? <span className="font-sans text-dim">whole scenario</span>}
              </Td>
              {numeric ? (
                <Td label="Threshold" numeric>
                  {measured(a.measure, a.threshold)}
                </Td>
              ) : null}
              {numeric ? (
                <Td label="Observed" numeric>
                  {/* "not measured" only where there was a number to measure.
                      every_request_succeeded and status_in have no threshold at
                      all, so an empty cell there is the column not applying
                      rather than a measurement that failed, and saying "not
                      measured" would report a gap that does not exist. */}
                  {a.observed !== null
                    ? measured(a.measure, a.observed)
                    : isNumericMeasure(a.measure)
                      ? <span className="text-dim">not measured</span>
                      : "--"}
                </Td>
              ) : null}
              <Td label="Verdict">
                <AssertionVerdict verdict={a.verdict} />
              </Td>
              <Td label="Detail" className="max-w-[40ch]">
                {a.detail ?? "--"}
              </Td>
            </Row>
          ))}
        </tbody>
      </Table>
    </TableWrap>
  );
}

/**
 * An assertion's verdict, in the product's four words.
 *
 * `blocked` and `unverified` are drawn apart from a failure and never as a
 * pass, exactly as they are on a run. An assertion about requests that were
 * never sent has not held.
 */
function AssertionVerdict({ verdict }: { verdict: Verdict }) {
  if (verdict === "pass") return <Badge tone="pass">Held</Badge>;
  if (verdict === "fail") return <Badge tone="fail">Broke</Badge>;
  return <Badge tone="warn">{VERDICT_FACTS[verdict].label}</Badge>;
}

/** Thresholds the run exceeded, as the engine reported them. Rendered only
 *  when there are some: an empty breach list is the normal case and a table
 *  saying so would be noise on every healthy run. */
export function Breaches({ breaches }: { breaches: Breach[] }) {
  if (breaches.length === 0) return null;
  return (
    <TableWrap>
      <Table>
        <thead>
          <tr>
            <Th>What</Th>
            <Th numeric>Limit</Th>
            <Th numeric>Measured</Th>
            <Th>Detail</Th>
          </tr>
        </thead>
        <tbody>
          {breaches.map((b, i) => (
            <Row key={`${b.what}-${i}`}>
              <Td mono>{b.what}</Td>
              {/* Both as percentages, because both are fractions today:
                  `Result.Breaches` takes a p95 increase ratio and an error
                  rate fraction, and those are the only two breaches it
                  produces. Printing 1.314 beside a detail line that says
                  "131 percent slower" makes a reader do the arithmetic twice
                  to check the page agrees with itself. If the engine ever adds
                  a breach whose limit is a duration, this is where it breaks
                  and it will be obvious. */}
              <Td label="Limit" numeric>
                {percent(b.limit)}
              </Td>
              <Td label="Measured" numeric>
                {percent(b.measured)}
              </Td>
              <Td label="Detail" className="max-w-[44ch]">
                {b.detail ?? "--"}
              </Td>
            </Row>
          ))}
        </tbody>
      </Table>
    </TableWrap>
  );
}

/* -------------------------------------------------------------------------
 * Route level differences
 * ---------------------------------------------------------------------- */

/**
 * How much slower each route is than production, drawn from a centre line.
 *
 * A diverging encoding, which is the right family because the quantity has a
 * meaningful zero and two opposite directions. The engine reports a RATIO
 * against a baseline p95, not a millisecond delta, so this shows a ratio.
 *
 * The colour is never the signal on its own, and that is a measurement rather
 * than a principle. This console's pass green (#1e7a3a) and fail red (#b3261e)
 * separate by a Delta E of 26.8 to normal vision and 4.0 under deuteranopia,
 * so to a red-green colourblind reader the two poles are very nearly the same
 * colour. The signed number carries the direction, the bar's side carries it
 * again, and the colour agrees with both.
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
function Delta({ r }: { r: RouteResult }) {
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
 */
export function Routes({ routes }: { routes: RouteResult[] }) {
  if (routes.length === 0) {
    return (
      <Empty title="No per-route measurement">
        The run stored no route breakdown. That is not the same as a run in
        which no route was touched.
      </Empty>
    );
  }

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
              <Row key={r.route}>
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
                <Td label="p95" numeric>
                  {ms(r.latency.p95Ms)}
                </Td>
                <Td label="Baseline p95" numeric>
                  {r.hasBaseline ? ms(r.baselineP95Ms) : <span className="text-dim">--</span>}
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

/** What the run kept. An artifact retention dropped says so, rather than being
 *  absent and leaving a reader to wonder whether it ever existed. */
export function EvidenceList({ evidence }: { evidence: Evidence[] }) {
  if (evidence.length === 0) {
    return (
      <Empty title="No evidence kept">
        This run stored nothing. Retention is a policy decision, so an empty
        list can mean the run produced nothing or that what it produced has
        since been dropped.
      </Empty>
    );
  }
  return (
    <TableWrap>
      <Table>
        <thead>
          <tr>
            <Th>Kind</Th>
            <Th>Label</Th>
            <Th>Retained</Th>
          </tr>
        </thead>
        <tbody>
          {evidence.map((e) => (
            <Row key={e.id}>
              <Td mono>{e.kind}</Td>
              <Td label="Label">
                {e.href && e.retained !== false ? (
                  <a
                    className="inline-flex min-h-11 items-center text-ink underline decoration-[rgba(16,16,16,0.25)] underline-offset-4 hover:decoration-ink sm:min-h-0"
                    href={e.href}
                  >
                    {e.label ?? e.kind}
                  </a>
                ) : (
                  (e.label ?? "--")
                )}
              </Td>
              <Td label="Retained">
                <Badge tone={e.retained === false ? "neutral" : "pass"}>
                  {e.retained === false ? "dropped" : "kept"}
                </Badge>
              </Td>
            </Row>
          ))}
        </tbody>
      </Table>
    </TableWrap>
  );
}
