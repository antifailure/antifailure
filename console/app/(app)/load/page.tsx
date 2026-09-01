"use client";

import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useApi } from "@/lib/api";
import {
  Card,
  CellLink,
  Empty,
  Page,
  Row,
  Table,
  TableSkeleton,
  TableWrap,
  Td,
  Th,
  When,
} from "@/components/ui";
import { Provenance, StateBadge, VerdictBadge } from "@/components/load/primitives";
import { SourceDetailView } from "@/components/load/source";
import { RunView } from "@/components/load/run";
import { Explorations } from "@/components/load/exploration";
import { LoadError } from "@/components/load/states";
import {
  SOURCE_FACTS,
  SOURCE_KINDS,
  count,
  listRuns,
  listSources,
  type RunRow,
  type SourceKind,
  type SourceRow,
} from "@/lib/load";

/* -------------------------------------------------------------------------
 * The list
 * ---------------------------------------------------------------------- */

/**
 * Filtering by where the traffic came from.
 *
 * Real buttons rather than a select, because there are three choices and the
 * set is closed. No count on the buttons: it would need a query per kind, and
 * a number that is only right until somebody else starts a run is worse than
 * no number.
 */
function KindFilter({
  value,
  onChange,
}: {
  value: SourceKind | null;
  onChange: (kind: SourceKind | null) => void;
}) {
  const options: { key: SourceKind | null; label: string }[] = [
    { key: null, label: "All" },
    ...SOURCE_KINDS.map((k) => ({ key: k as SourceKind | null, label: SOURCE_FACTS[k].noun })),
  ];
  return (
    <div className="flex flex-wrap gap-1.5" role="group" aria-label="Filter by source">
      {options.map((o) => {
        const active = value === o.key;
        return (
          <button
            key={o.label}
            type="button"
            onClick={() => onChange(o.key)}
            aria-pressed={active}
            // min-w-11 because "All" is three characters and h-9 plus px-3 came
            // to 40px wide under a thumb. globals.css raises the height to 44
            // on a phone and cannot know about the width.
            className={`inline-flex h-9 min-w-11 items-center justify-center rounded-md px-3 text-[13px] font-medium transition-colors ${
              active
                ? "bg-ink text-white"
                : "border border-rule bg-card text-muted hover:border-rule-strong hover:text-ink"
            }`}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

function Sources({ kind, onOpen }: { kind: SourceKind | null; onOpen: (id: string) => void }) {
  const state = useApi<{ items: SourceRow[] }>(() => listSources(kind ? { kind } : {}), [kind]);

  return (
    <Card title="Sources" note="What can be sent at a twin, and where each one's traffic came from.">
      {state.status === "error" && state.error ? (
        <LoadError error={state.error} retry={state.reload} />
      ) : state.status === "loading" || state.data === null ? (
        <TableSkeleton rows={5} cols={5} />
      ) : state.data.items.length === 0 ? (
        kind === null ? (
          <Empty title="No load sources yet">
            A source is traffic with a known origin: a mix compiled from
            production's own access log, or a scenario somebody wrote. Run{" "}
            <code className="font-mono text-[12.5px]">af load run</code> against an environment and
            what it sent appears here, with every run of it and what each one proved.
          </Empty>
        ) : (
          <Empty title={`No ${SOURCE_FACTS[kind].noun.toLowerCase()} sources`}>
            {SOURCE_FACTS[kind].what} Nothing of this kind has been recorded for your organization
            yet. The other kind may still have something.
          </Empty>
        )
      ) : (
        <TableWrap>
          <Table>
            <thead>
              <tr>
                <Th>Name</Th>
                <Th>Source</Th>
                <Th>Repository</Th>
                <Th numeric>Runs</Th>
                <Th>Last run</Th>
              </tr>
            </thead>
            <tbody>
              {state.data.items.map((s) => (
                <Row key={s.id} onClick={() => onOpen(s.id)}>
                  <Td>
                    <CellLink href={`/load?source=${encodeURIComponent(s.id)}`}>{s.name}</CellLink>
                  </Td>
                  <Td label="Source">
                    <Provenance kind={s.kind} />
                  </Td>
                  <Td label="Repository">{s.repository ?? "--"}</Td>
                  <Td label="Runs" numeric>
                    {count(s.runCount)}
                  </Td>
                  <Td label="Last run">
                    {s.lastRunAt ? (
                      <span className="flex flex-wrap items-center gap-2">
                        <When value={s.lastRunAt} />
                        <VerdictBadge verdict={s.lastVerdict} />
                      </span>
                    ) : (
                      <span className="text-dim">never</span>
                    )}
                  </Td>
                </Row>
              ))}
            </tbody>
          </Table>
        </TableWrap>
      )}
    </Card>
  );
}

function RecentRuns({ onOpen }: { onOpen: (runId: string) => void }) {
  const state = useApi<{ items: RunRow[] }>(() => listRuns({ limit: 8 }), []);
  return (
    <Card title="Recent runs" note="Across every source.">
      {state.status === "error" && state.error ? (
        <LoadError error={state.error} retry={state.reload} />
      ) : state.status === "loading" || state.data === null ? (
        <TableSkeleton rows={4} cols={5} />
      ) : state.data.items.length === 0 ? (
        <Empty title="Nothing has run">
          A run appears the first time load is sent at an environment.
        </Empty>
      ) : (
        <TableWrap>
          <Table>
            <thead>
              <tr>
                <Th>Source</Th>
                <Th>Verdict</Th>
                <Th>State</Th>
                <Th>Environment</Th>
                <Th>Started</Th>
              </tr>
            </thead>
            <tbody>
              {state.data.items.map((r) => (
                <Row key={r.id} onClick={() => onOpen(r.id)}>
                  <Td>
                    <CellLink href={`/load?run=${encodeURIComponent(r.id)}`}>
                      {r.sourceName ?? r.id}
                    </CellLink>
                  </Td>
                  <Td label="Verdict">
                    <VerdictBadge verdict={r.verdict} />
                  </Td>
                  <Td label="State">
                    <StateBadge state={r.state} />
                  </Td>
                  <Td label="Environment" mono>
                    {r.envId ?? "--"}
                  </Td>
                  <Td label="Started">
                    <When value={r.startedAt ?? r.createdAt} />
                  </Td>
                </Row>
              ))}
            </tbody>
          </Table>
        </TableWrap>
      )}
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * The three views
 * ---------------------------------------------------------------------- */

/**
 * One page, three views, selected by the query string.
 *
 * Not `/load/[id]`. This console is a static export served by the control
 * plane's own process, so there is no server to resolve a dynamic segment and
 * no way to know every id at build time. A detail view is a query string on a
 * static page, the same shape the runs screen already uses.
 */
function Load() {
  const params = useSearchParams();
  const router = useRouter();
  const sourceId = params.get("source");
  const runId = params.get("run");
  const [kind, setKind] = useState<SourceKind | null>(null);

  if (runId) {
    return (
      <Page title="Load run" lede="What this run sent, what came back, and what it proved.">
        <RunView runId={runId} onClose={() => router.push("/load")} />
      </Page>
    );
  }

  if (sourceId) {
    return (
      <Page title="Load source" lede="Where its traffic comes from, every version, and every run.">
        <SourceDetailView
          id={sourceId}
          onClose={() => router.push("/load")}
          onOpenRun={(id) => router.push(`/load?run=${encodeURIComponent(id)}`)}
        />
      </Page>
    );
  }

  return (
    <Page
      title="Load"
      lede="Traffic sent at a disposable twin, shaped like production's own. A load test that hammers one endpoint proves the endpoint is fast, which nobody doubted; what breaks under real traffic is the mix."
      actions={<KindFilter value={kind} onChange={setKind} />}
    >
      <div className="space-y-6">
        <Sources kind={kind} onOpen={(id) => router.push(`/load?source=${encodeURIComponent(id)}`)} />
        <RecentRuns onOpen={(id) => router.push(`/load?run=${encodeURIComponent(id)}`)} />
        <Explorations />
      </div>
    </Page>
  );
}

export default function LoadPage() {
  return (
    <Suspense
      fallback={
        <Page title="Load">
          <Card title="Sources">
            <TableSkeleton rows={5} cols={5} />
          </Card>
        </Page>
      }
    >
      <Load />
    </Suspense>
  );
}
