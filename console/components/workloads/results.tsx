"use client";

import {
  Badge,
  Empty,
  Row,
  Table,
  TableWrap,
  Td,
  Th,
} from "@/components/ui";
import {
  count,
  deltaFraction,
  deltaMs,
  ms,
  percent,
  type Evidence,
  type RouteDelta,
  type Threshold,
} from "@/lib/workloads";

/* -------------------------------------------------------------------------
 * Thresholds
 * ---------------------------------------------------------------------- */

/**
 * What the run was asked to hold to, and whether it did.
 *
 * Three outcomes, not two. A threshold nothing measured has not passed, and
 * the third value is the only thing stopping this table from reporting a run
 * that never produced a latency figure as one whose latency threshold held.
 */
export function Thresholds({ thresholds }: { thresholds: Threshold[] }) {
  if (thresholds.length === 0) {
    return (
      <Empty title="No thresholds on this run">
        Nothing was asserted, so there is nothing to hold to. A run with no
        thresholds measures the workload; it does not judge it.
      </Empty>
    );
  }

  return (
    <TableWrap>
      <Table>
        <thead>
          <tr>
            <Th>Threshold</Th>
            <Th>Target</Th>
            <Th>Actual</Th>
            <Th>Outcome</Th>
          </tr>
        </thead>
        <tbody>
          {thresholds.map((t) => (
            <Row key={t.id}>
              <Td>{t.name}</Td>
              <Td label="Target" mono>
                {t.target ?? "--"}
              </Td>
              <Td label="Actual" mono>
                {t.actual ?? "--"}
              </Td>
              <Td label="Outcome">
                {t.outcome === "held" ? (
                  <Badge tone="pass">Held</Badge>
                ) : t.outcome === "broke" ? (
                  <Badge tone="fail">Broke</Badge>
                ) : (
                  <Badge tone="warn">Not evaluated</Badge>
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
 * Route level differences
 * ---------------------------------------------------------------------- */

/**
 * How much slower or faster, drawn from a centre line.
 *
 * A diverging encoding, which is the right family here because the quantity
 * has a meaningful zero and two opposite directions: slower is bad, faster is
 * good, and no change is nothing. The bar grows right for a regression and
 * left for an improvement, both scaled against the largest movement in the
 * table so the worst route is full width and everything else is read against
 * it.
 *
 * The colour is never the signal on its own, and that is a measurement rather
 * than a principle. This console's pass green (#1e7a3a) and fail red
 * (#b3261e) separate by a Delta E of 26.8 to normal vision and 4.0 under
 * deuteranopia, so to a red-green colourblind reader the improvement pole and
 * the regression pole are very nearly the same colour. The signed number
 * beside the bar is what carries the direction; the bar's side carries it
 * again; the colour agrees with both and is the only one of the three that
 * some readers will not get.
 */
function DeltaBar({ fraction, largest }: { fraction: number | null; largest: number }) {
  if (fraction === null || largest === 0) {
    return <span className="block h-2 w-full" aria-hidden />;
  }
  const share = Math.min(1, Math.abs(fraction) / largest);
  const slower = fraction > 0;
  return (
    <span className="relative block h-2 w-full" aria-hidden>
      {/* The centre line stays visible under every bar, so "no change" has a
          position on the axis rather than being an empty cell. */}
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

/** The signed change, as words and a number. This is the accessible name for
 *  the row's movement, and the bar beside it is decoration on top of it. */
function DeltaValue({ d }: { d: RouteDelta }) {
  const abs = deltaMs(d);
  const frac = deltaFraction(d);
  if (abs === null) {
    return (
      <span className="text-dim">
        {d.baselineMs === null && d.candidateMs === null
          ? "not measured"
          : d.baselineMs === null
            ? "new route"
            : "not in candidate"}
      </span>
    );
  }
  if (abs === 0) return <span className="text-muted">no change</span>;
  const slower = abs > 0;
  return (
    <span className={slower ? "text-fail" : "text-pass"}>
      {slower ? "+" : "-"}
      {ms(Math.abs(abs))}
      {frac === null ? "" : ` (${slower ? "+" : "-"}${percent(Math.abs(frac))})`}
    </span>
  );
}

/**
 * Every route, worst regression first.
 *
 * The order is the finding. A table sorted by route name asks a person to read
 * all of it to learn which change matters; sorted by movement, the answer is
 * the first row. Routes that could not be compared sink to the bottom rather
 * than sorting as though they had not moved.
 */
export function RouteDeltas({ routes }: { routes: RouteDelta[] }) {
  if (routes.length === 0) {
    return (
      <Empty title="No per-route breakdown">
        This run did not record a route level comparison. That happens when it
        ran on one side only, so there is nothing to compare against.
      </Empty>
    );
  }

  const sorted = [...routes].sort((a, b) => {
    const fa = deltaFraction(a);
    const fb = deltaFraction(b);
    if (fa === null && fb === null) return a.route.localeCompare(b.route);
    if (fa === null) return 1;
    if (fb === null) return -1;
    return fb - fa;
  });
  const largest = sorted.reduce((max, r) => {
    const f = deltaFraction(r);
    return f === null ? max : Math.max(max, Math.abs(f));
  }, 0);

  const compared = sorted.filter((r) => deltaFraction(r) !== null).length;

  return (
    <>
      <TableWrap>
        <Table>
          <thead>
            <tr>
              <Th>Route</Th>
              <Th numeric>Requests</Th>
              <Th numeric>Baseline</Th>
              <Th numeric>Candidate</Th>
              <Th>Change</Th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((r) => (
              <Row key={`${r.method ?? ""} ${r.route}`}>
                <Td mono>
                  {r.method ? (
                    <span className="text-dim">{r.method} </span>
                  ) : null}
                  {r.route}
                </Td>
                <Td label="Requests" numeric>
                  {count(r.requests)}
                </Td>
                <Td label="Baseline" numeric>
                  {ms(r.baselineMs)}
                </Td>
                <Td label="Candidate" numeric>
                  {ms(r.candidateMs)}
                </Td>
                <Td label="Change">
                  {/* The number leads and the bar follows it, so a reader who
                      cannot see the bar's colour or its side has already been
                      told the direction in words. */}
                  <span className="flex min-w-[16ch] items-center gap-3">
                    <span className="tnum whitespace-nowrap text-[12.5px]">
                      <DeltaValue d={r} />
                    </span>
                    <span className="hidden min-w-[80px] flex-1 sm:block">
                      <DeltaBar fraction={deltaFraction(r)} largest={largest} />
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
          {sorted.length - compared} of {sorted.length} routes were measured on
          one side only, so they carry no comparison. They are listed last.
        </p>
      ) : null}
    </>
  );
}

/* -------------------------------------------------------------------------
 * Evidence
 * ---------------------------------------------------------------------- */

/** What the run kept. An artifact that retention dropped says so, rather than
 *  being absent and leaving a reader to wonder whether it ever existed. */
export function EvidenceList({ evidence }: { evidence: Evidence[] }) {
  if (evidence.length === 0) {
    return (
      <Empty title="No evidence kept">
        This run stored nothing. Retention is a policy decision, so an empty
        list can mean the run produced nothing or that everything it produced
        has since been dropped.
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
