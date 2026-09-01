"use client";

import type { ReactNode } from "react";
import { Empty, Row, Table, TableWrap, Td, Th, When } from "@/components/ui";
import { Provenance, RouteCell } from "@/components/load/primitives";
import {
  SOURCE_FACTS,
  count,
  ms,
  percent,
  type DeterministicSource,
  type LoadSource,
  type ObservedSource,
  type SourceKind,
  type Step,
} from "@/lib/load";

/* -------------------------------------------------------------------------
 * The header both kinds share
 * ---------------------------------------------------------------------- */

/**
 * What this source is and what a result from it is worth.
 *
 * The reproducibility sentence is why this is a block rather than a subtitle.
 * It is the fact a reader needs before a single number underneath: a scenario
 * that replays request for request and a mix that replays only as a shape are
 * not equally strong evidence, and the console is the only place that
 * difference gets said out loud.
 */
export function SourceHeader({ kind }: { kind: SourceKind }) {
  const fact = SOURCE_FACTS[kind];
  return (
    <div className="border-b border-rule px-4 py-4">
      <Provenance kind={kind} />
      <p className="mt-2 max-w-[74ch] text-[13px] leading-6 text-muted">{fact.what}</p>
      <p className="mt-2 max-w-[74ch] text-[12.5px] leading-6 text-dim">
        <span className="font-medium text-muted">Reproducible:</span> {fact.reproducible}
      </p>
    </div>
  );
}

function Facts({ children }: { children: ReactNode }) {
  return <dl className="grid gap-x-8 gap-y-4 px-4 py-4 sm:grid-cols-2">{children}</dl>;
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">{label}</dt>
      <dd className="mt-1 break-words text-[13px] leading-6 text-ink">{children}</dd>
    </div>
  );
}

/* -------------------------------------------------------------------------
 * Observed
 * ---------------------------------------------------------------------- */

/**
 * Traffic measured from production, and the mix it compiled to.
 *
 * The window and the request count carry the same weight as the routes,
 * because they are what makes the mix trustworthy. A shape derived from four
 * minutes of a Tuesday afternoon answers a different question than one derived
 * from a week, and a reader who sees only the route table cannot tell them
 * apart.
 *
 * The excluded routes are shown rather than hidden, and that is the part worth
 * defending. Every route is unsafe until a safe pattern matches it, so a mix
 * that looks thin is usually a safe list that is too narrow rather than
 * traffic that was not there. Hiding the exclusions makes that impossible to
 * diagnose from the console.
 */
function Observed({ s }: { s: ObservedSource }) {
  const weighted = s.routes.filter((r) => r.weight !== null);
  const widest = weighted.reduce((m, r) => Math.max(m, r.weight as number), 0);
  return (
    <>
      <Facts>
        <Fact label="Compiled from">
          {s.origin ? <code className="font-mono text-[12.5px]">{s.origin}</code> : "not recorded"}
        </Fact>
        <Fact label="Sample">{s.sample ?? "not recorded"}</Fact>
        <Fact label="Window">
          {s.windowStart || s.windowEnd ? (
            <span className="inline-flex flex-wrap items-center gap-1.5">
              <When value={s.windowStart} />
              <span className="text-dim">to</span>
              <When value={s.windowEnd} />
            </span>
          ) : (
            "not recorded"
          )}
        </Fact>
        <Fact label="Requests observed">{count(s.requestsObserved)}</Fact>
      </Facts>

      {s.routes.length === 0 ? (
        <Empty title="No routes in the mix">
          The source was accepted and compiled to nothing that may be sent.
          Every route is unsafe until a safe pattern matches it, so this is
          usually a safe list that matches none of production's traffic rather
          than a log that was empty.
        </Empty>
      ) : (
        <TableWrap>
          <Table>
            <thead>
              <tr>
                <Th>Route</Th>
                <Th>Share of traffic</Th>
              </tr>
            </thead>
            <tbody>
              {[...s.routes]
                .sort((a, b) => (b.weight ?? 0) - (a.weight ?? 0))
                .map((r) => (
                  <Row key={`${r.method ?? ""} ${r.path}`}>
                    <Td mono>
                      <RouteCell route={r.method ? `${r.method} ${r.path}` : r.path} />
                    </Td>
                    <Td label="Share">
                      <span className="flex items-center gap-3">
                        <span className="tnum w-[5ch] shrink-0 text-right text-[12.5px]">
                          {percent(r.weight)}
                        </span>
                        {/* One colour: these are shares of a single whole, so a
                            hue per route would encode the bar's own length a
                            second time and nothing else. */}
                        <span className="hidden h-2 w-24 shrink-0 sm:block" aria-hidden>
                          <span
                            className="block h-2 rounded-sm bg-[rgba(16,16,16,0.68)]"
                            style={{
                              width:
                                r.weight === null || widest === 0
                                  ? "0%"
                                  : `${Math.max(2, (r.weight / widest) * 100).toFixed(2)}%`,
                            }}
                          />
                        </span>
                      </span>
                    </Td>
                  </Row>
                ))}
            </tbody>
          </Table>
        </TableWrap>
      )}

      {s.excluded.length > 0 ? (
        <div className="border-t border-rule px-4 py-4">
          <p className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
            Excluded by the safe list
          </p>
          <p className="mt-1 max-w-[74ch] text-[12.5px] leading-6 text-muted">
            {s.excluded.length === 1 ? "One route was" : `${s.excluded.length} routes were`} in
            production's traffic and will not be sent, because no safe pattern matched. That is the
            default and usually the right one: a generator that finds POST /checkout in an access
            log and runs it four hundred times charges four hundred cards.
          </p>
          <ul className="mt-3 space-y-1">
            {s.excluded.map((r) => (
              <li key={`${r.method ?? ""} ${r.path}`} className="break-all font-mono text-[12.5px] text-muted">
                {r.method ? <span className="text-dim">{r.method} </span> : null}
                {r.path}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </>
  );
}

/* -------------------------------------------------------------------------
 * Deterministic
 * ---------------------------------------------------------------------- */

/** One step, and any steps sent alongside it. Parallel steps are nested rather
 *  than flattened into siblings, because a journey that sends two requests at
 *  once is a different journey from one that sends them in order. */
function StepRow({ step, index, depth = 0 }: { step: Step; index: number; depth?: number }) {
  const timing = [
    step.thinkMs === null ? null : `think ${step.thinkMs}ms`,
    step.jitterMs === null ? null : `jitter ${step.jitterMs}ms`,
    step.afterMs === null ? null : `after ${step.afterMs}ms`,
  ].filter(Boolean);
  return (
    <>
      <Row>
        <Td numeric>{depth === 0 ? index : ""}</Td>
        <Td label="Step">
          <span style={{ paddingLeft: depth * 14 }} className="block">
            {depth > 0 ? <span className="text-dim">at the same time: </span> : null}
            {step.name ?? <span className="text-dim">unnamed</span>}
          </span>
        </Td>
        <Td label="Request" mono>
          <RouteCell route={step.request} />
        </Td>
        <Td label="Timing" className="text-dim">
          {timing.length === 0 ? "--" : timing.join(", ")}
        </Td>
      </Row>
      {step.parallel.map((p, i) => (
        <StepRow key={`${index}-${i}-${p.request}`} step={p} index={index} depth={depth + 1} />
      ))}
    </>
  );
}

/**
 * A scenario somebody wrote.
 *
 * Steps keep their authored order rather than being sorted. That order is the
 * content: a deterministic scenario is a sequence a person chose, and
 * reordering it for tidiness would present a different scenario from the one
 * that runs.
 *
 * Assertions are shown in the scenario's own field names. Renaming
 * `p95_below_ms` to "latency threshold" would make somebody translate back to
 * the YAML every time they wanted to change it.
 */
function Deterministic({ s }: { s: DeterministicSource }) {
  return (
    <>
      <Facts>
        <Fact label="File">
          {s.path ? <code className="break-all font-mono text-[12.5px]">{s.path}</code> : "not recorded"}
        </Fact>
        <Fact label="Scenario">{s.scenarioName ?? "not recorded"}</Fact>
      </Facts>
      {s.description ? (
        <p className="border-t border-rule px-4 py-3 text-[13px] leading-6 text-muted">
          {s.description}
        </p>
      ) : null}

      {s.steps.length === 0 ? (
        <Empty title="No steps">
          This scenario has no steps, so a run of it would send nothing.
        </Empty>
      ) : (
        <TableWrap>
          <Table>
            <thead>
              <tr>
                {/* Numbered, because here the order genuinely is information:
                    step three runs after step two. */}
                <Th numeric>Step</Th>
                <Th>Name</Th>
                <Th>Request</Th>
                <Th>Timing</Th>
              </tr>
            </thead>
            <tbody>
              {s.steps.map((step, i) => (
                <StepRow key={`${i}-${step.request}`} step={step} index={i + 1} />
              ))}
            </tbody>
          </Table>
        </TableWrap>
      )}

      <div className="border-t border-rule">
        <div className="px-4 pt-4">
          <p className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">Assertions</p>
        </div>
        {s.assertions.length === 0 ? (
          <Empty title="Nothing is asserted">
            This scenario measures and judges nothing, so a run of it produces
            numbers to read rather than a verdict to act on.
          </Empty>
        ) : (
          <TableWrap>
            <Table>
              <thead>
                <tr>
                  <Th>Name</Th>
                  <Th>Scope</Th>
                  <Th>Requires</Th>
                </tr>
              </thead>
              <tbody>
                {s.assertions.map((a, i) => {
                  const requires = [
                    a.everyRequestSucceeded ? "every request succeeded" : null,
                    a.p95BelowMs === null ? null : `p95 below ${ms(a.p95BelowMs)}`,
                    a.errorRateBelow === null ? null : `error rate below ${percent(a.errorRateBelow)}`,
                    a.statusIn.length === 0 ? null : `status in ${a.statusIn.join(", ")}`,
                  ].filter(Boolean) as string[];
                  return (
                    <Row key={`${a.name}-${i}`}>
                      <Td>{a.name}</Td>
                      <Td label="Scope" mono>
                        {a.step ?? <span className="font-sans text-dim">whole scenario</span>}
                      </Td>
                      <Td label="Requires" className="max-w-[44ch]">
                        {requires.length === 0 ? (
                          // An assertion with no measure is one the engine's
                          // own validator refuses, so seeing one here means
                          // the stored row and the scenario disagree.
                          <span className="text-warn">
                            nothing, which the scenario validator would refuse
                          </span>
                        ) : (
                          requires.join("; ")
                        )}
                      </Td>
                    </Row>
                  );
                })}
              </tbody>
            </Table>
          </TableWrap>
        )}
      </div>
    </>
  );
}

/* -------------------------------------------------------------------------
 * The one entry point
 * ---------------------------------------------------------------------- */

/**
 * The source, rendered by its kind.
 *
 * A switch, not a shared form with optional fields. The two branches share no
 * row of markup, which is the point: an observed mix and an authored scenario
 * are not two skins on one idea, and the moment they share a table they begin
 * to look like they are.
 */
export function SourceView({ source }: { source: LoadSource | null }) {
  if (source === null) {
    return (
      <Empty title="No configuration stored">
        This source has nothing to send. That is a gap in the record rather than
        an empty mix.
      </Empty>
    );
  }
  return source.kind === "observed" ? <Observed s={source} /> : <Deterministic s={source} />;
}
