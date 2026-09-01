"use client";

import { useState, type ReactNode } from "react";
import { Badge, Button, Empty, Row, Table, TableWrap, Td, Th, When } from "@/components/ui";
import { Provenance, RouteCell } from "@/components/workloads/primitives";
import {
  KIND_FACTS,
  count,
  percent,
  seconds,
  type DeterministicSource,
  type ExploratorySource,
  type Kind,
  type ObservedSource,
  type Source,
} from "@/lib/workloads";

/* -------------------------------------------------------------------------
 * The header every source shares
 * ---------------------------------------------------------------------- */

/**
 * What this workload is and what it is worth.
 *
 * The reproducibility sentence is the reason this block exists rather than
 * being a subtitle. It is the fact a person needs before they read a single
 * number underneath: an exactly reproducible scenario and a seeded exploration
 * that happened to find the same regression are not equally strong evidence,
 * and the console is the only place that difference gets stated.
 */
export function SourceHeader({ kind }: { kind: Kind }) {
  const fact = KIND_FACTS[kind];
  return (
    <div className="border-b border-rule px-4 py-4">
      <Provenance kind={kind} />
      <p className="mt-2 max-w-[70ch] text-[13px] leading-6 text-muted">{fact.what}</p>
      <p className="mt-2 max-w-[70ch] text-[12.5px] leading-6 text-dim">
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
 * The window and the request count are given the same weight as the routes,
 * because they are what makes the mix trustworthy or not. A shape derived from
 * four minutes of a Tuesday afternoon is a real answer to a different question
 * than one derived from a week, and a reader who only sees the route table
 * cannot tell those apart.
 */
function Observed({ s }: { s: ObservedSource }) {
  const shares = s.routes.filter((r) => r.share !== null);
  const widest = shares.reduce((m, r) => Math.max(m, r.share as number), 0);
  return (
    <>
      <Facts>
        <Fact label="Format">
          {s.format ? <code className="font-mono text-[12.5px]">{s.format}</code> : "not recorded"}
        </Fact>
        <Fact label="Sample">{s.sampleName ?? "not recorded"}</Fact>
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
        <Empty title="No compiled mix">
          The source was accepted but no route shape was stored against it, so
          there is nothing here to send.
        </Empty>
      ) : (
        <TableWrap>
          <Table>
            <thead>
              <tr>
                <Th>Route</Th>
                <Th>Share of traffic</Th>
                <Th numeric>Rate</Th>
              </tr>
            </thead>
            <tbody>
              {[...s.routes]
                .sort((a, b) => (b.share ?? 0) - (a.share ?? 0))
                .map((r) => (
                  <Row key={`${r.method ?? ""} ${r.route}`}>
                    <Td mono>
                      <RouteCell method={r.method} route={r.route} />
                    </Td>
                    <Td label="Share">
                      <span className="flex items-center gap-3">
                        <span className="tnum w-[5ch] shrink-0 text-right text-[12.5px]">
                          {percent(r.share)}
                        </span>
                        {/* One colour for every row. The bars are one series,
                            the share of a single whole, so a hue per route
                            would encode the length a second time and nothing
                            else. */}
                        <span className="hidden h-2 min-w-[80px] flex-1 sm:block" aria-hidden>
                          <span
                            className="block h-2 rounded-sm bg-[rgba(16,16,16,0.68)]"
                            style={{
                              width:
                                r.share === null || widest === 0
                                  ? "0%"
                                  : `${Math.max(2, (r.share / widest) * 100).toFixed(2)}%`,
                            }}
                          />
                        </span>
                      </span>
                    </Td>
                    <Td label="Rate" numeric>
                      {r.rps === null ? "--" : `${r.rps.toFixed(2)}/s`}
                    </Td>
                  </Row>
                ))}
            </tbody>
          </Table>
        </TableWrap>
      )}
    </>
  );
}

/* -------------------------------------------------------------------------
 * Deterministic
 * ---------------------------------------------------------------------- */

/**
 * A scenario somebody wrote.
 *
 * The steps keep their authored order rather than being sorted by weight. That
 * order is the content: a deterministic scenario is a sequence a person chose,
 * and reordering it for tidiness would present a different scenario from the
 * one that runs.
 */
function Deterministic({ s }: { s: DeterministicSource }) {
  return (
    <>
      <Facts>
        <Fact label="Scenario">
          {s.scenarioPath ? (
            <code className="font-mono text-[12.5px]">{s.scenarioPath}</code>
          ) : (
            "not recorded"
          )}
        </Fact>
        <Fact label="Pinned version">{s.scenarioVersion ?? "not recorded"}</Fact>
      </Facts>
      {s.steps.length === 0 ? (
        <Empty title="No steps">
          This scenario has no steps stored against it, so a run of it would
          send nothing.
        </Empty>
      ) : (
        <TableWrap>
          <Table>
            <thead>
              <tr>
                {/* Numbered, because here the order genuinely is information:
                    step 3 runs after step 2. */}
                <Th numeric>Step</Th>
                <Th>Name</Th>
                <Th>Route</Th>
                <Th numeric>Weight</Th>
              </tr>
            </thead>
            <tbody>
              {s.steps.map((step, i) => (
                <Row key={`${i}-${step.route}`}>
                  <Td numeric>{i + 1}</Td>
                  <Td label="Name">{step.name}</Td>
                  <Td label="Route" mono>
                    <RouteCell method={step.method} route={step.route} />
                  </Td>
                  <Td label="Weight" numeric>
                    {step.weight === null ? "--" : step.weight}
                  </Td>
                </Row>
              ))}
            </tbody>
          </Table>
        </TableWrap>
      )}
    </>
  );
}

/* -------------------------------------------------------------------------
 * Exploratory
 * ---------------------------------------------------------------------- */

/**
 * What an agent found, and the one thing worth doing with it.
 *
 * Promotion is the point of this screen. An exploratory run's output is a
 * candidate: it happened once, against one build, from one seed. Turning it
 * into a deterministic definition is what makes it something a team can rely
 * on, and a discovery that has already been promoted says so and offers the
 * definition instead of offering to promote it twice.
 */
function Exploratory({
  s,
  canPromote,
  onPromote,
  promoting,
  error,
}: {
  s: ExploratorySource;
  canPromote: boolean;
  onPromote: (discoveryId: string, name: string) => void;
  promoting: string | null;
  error: string | null;
}) {
  const [naming, setNaming] = useState<string | null>(null);
  const [name, setName] = useState("");

  return (
    <>
      <Facts>
        <Fact label="Seed">
          {s.seed ? <code className="font-mono text-[12.5px]">{s.seed}</code> : "not recorded"}
        </Fact>
        <Fact label="Entry point">
          {s.entryUrl ? (
            <code className="break-all font-mono text-[12.5px]">{s.entryUrl}</code>
          ) : (
            "not recorded"
          )}
        </Fact>
        <Fact label="Budget">{seconds(s.budgetSeconds)}</Fact>
        <Fact label="Step limit">{count(s.maxSteps)}</Fact>
      </Facts>

      {error ? (
        <p role="alert" className="border-t border-rule px-4 py-2.5 text-[12.5px] text-fail">
          {error}
        </p>
      ) : null}

      {s.discoveries.length === 0 ? (
        <Empty title="Nothing found yet">
          The agent has not reached a route it can report. An exploration that
          finds nothing is a result, not a failure: it means the seed and the
          budget did not take it anywhere new.
        </Empty>
      ) : (
        <TableWrap>
          <Table>
            <thead>
              <tr>
                <Th>Route</Th>
                <Th>Reached</Th>
                <Th>State</Th>
              </tr>
            </thead>
            <tbody>
              {s.discoveries.map((d) => (
                <Row key={d.id}>
                  <Td mono>
                    <RouteCell method={d.method} route={d.route} />
                  </Td>
                  <Td label="Reached">
                    <When value={d.reachedAt} />
                  </Td>
                  <Td label="State">
                    {d.promotedTo ? (
                      <span className="flex flex-wrap items-center gap-2">
                        <Badge tone="pass">Promoted</Badge>
                        <a
                          className="inline-flex min-h-11 items-center text-[12.5px] text-ink underline decoration-[rgba(16,16,16,0.25)] underline-offset-4 hover:decoration-ink sm:min-h-0"
                          href={`/workloads?definition=${encodeURIComponent(d.promotedTo)}`}
                        >
                          Open the scenario
                        </a>
                      </span>
                    ) : !canPromote ? (
                      <span className="text-[12.5px] text-dim">
                        Your role cannot promote
                      </span>
                    ) : naming === d.id ? (
                      <form
                        className="flex flex-wrap items-center gap-2"
                        onSubmit={(e) => {
                          e.preventDefault();
                          if (name.trim() === "") return;
                          onPromote(d.id, name.trim());
                        }}
                      >
                        <label className="sr-only" htmlFor={`promote-${d.id}`}>
                          Name for the scenario promoted from {d.route}
                        </label>
                        <input
                          id={`promote-${d.id}`}
                          autoFocus
                          className="h-9 w-[22ch] rounded-md border border-rule bg-card px-2.5 text-[13px] text-ink outline-none placeholder:text-dim focus:border-rule-strong"
                          placeholder="checkout-regression"
                          value={name}
                          onChange={(e) => setName(e.target.value)}
                        />
                        <Button
                          type="submit"
                          variant="primary"
                          busy={promoting === d.id}
                          disabled={name.trim() === ""}
                        >
                          {promoting === d.id ? "Promoting" : "Promote"}
                        </Button>
                        <Button
                          onClick={() => {
                            setNaming(null);
                            setName("");
                          }}
                        >
                          Cancel
                        </Button>
                      </form>
                    ) : (
                      <Button
                        onClick={() => {
                          setNaming(d.id);
                          setName("");
                        }}
                      >
                        Promote
                      </Button>
                    )}
                  </Td>
                </Row>
              ))}
            </tbody>
          </Table>
        </TableWrap>
      )}
    </>
  );
}

/* -------------------------------------------------------------------------
 * The one entry point
 * ---------------------------------------------------------------------- */

/**
 * The source, rendered by its kind.
 *
 * A switch rather than a shared form with optional fields. The three branches
 * do not share a single row of markup, which is the point: they are not three
 * skins on one idea, and the moment they share a table they start to look like
 * they are.
 */
export function SourceView({
  source,
  canPromote,
  onPromote,
  promoting,
  promoteError,
}: {
  source: Source | null;
  canPromote: boolean;
  onPromote: (discoveryId: string, name: string) => void;
  promoting: string | null;
  promoteError: string | null;
}) {
  if (source === null) {
    return (
      <Empty title="No source stored">
        This definition has no source configuration on it, so there is nothing
        to run. That is a gap in the record rather than an empty workload.
      </Empty>
    );
  }
  if (source.kind === "observed") return <Observed s={source} />;
  if (source.kind === "deterministic") return <Deterministic s={source} />;
  return (
    <Exploratory
      s={source}
      canPromote={canPromote}
      onPromote={onPromote}
      promoting={promoting}
      error={promoteError}
    />
  );
}
