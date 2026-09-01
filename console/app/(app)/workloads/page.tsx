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
import { Provenance, RunStatusBadge } from "@/components/workloads/primitives";
import { DefinitionView } from "@/components/workloads/definition";
import { RunView } from "@/components/workloads/run";
import { WorkloadError } from "@/components/workloads/states";
import {
  KINDS,
  KIND_FACTS,
  count,
  listDefinitions,
  listRuns,
  type DefinitionRow,
  type Kind,
  type RunRow,
} from "@/lib/workloads";

/* -------------------------------------------------------------------------
 * The list
 * ---------------------------------------------------------------------- */

/**
 * Filtering by where the traffic came from.
 *
 * A row of real buttons rather than a select, because there are exactly four
 * choices and the set is closed. The count is not shown on the buttons: it
 * would have to come from a query per kind, and a number that is only right
 * until somebody else starts a run is worse than no number.
 */
function KindFilter({
  value,
  onChange,
}: {
  value: Kind | null;
  onChange: (kind: Kind | null) => void;
}) {
  const options: { key: Kind | null; label: string }[] = [
    { key: null, label: "All" },
    ...KINDS.map((k) => ({ key: k as Kind | null, label: KIND_FACTS[k].noun })),
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
            className={`inline-flex h-9 items-center rounded-md px-3 text-[13px] font-medium transition-colors ${
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

function Definitions({
  kind,
  onOpen,
}: {
  kind: Kind | null;
  onOpen: (id: string) => void;
}) {
  const state = useApi<{ items: DefinitionRow[] }>(
    () => listDefinitions(kind ? { kind } : {}),
    [kind],
  );

  return (
    <Card
      title="Workloads"
      note="Traffic you can send at a twin, and where each one's traffic came from."
    >
      {state.status === "error" && state.error ? (
        <WorkloadError error={state.error} retry={state.reload} />
      ) : state.status === "loading" || state.data === null ? (
        <TableSkeleton rows={5} cols={5} />
      ) : state.data.items.length === 0 ? (
        kind === null ? (
          <Empty title="No workloads yet">
            A workload is traffic with a known origin: a shape measured from
            production, a scenario somebody wrote, or a route an agent found on
            its own. Record one with the CLI and it appears here, with every run
            of it and what each run proved.
          </Empty>
        ) : (
          <Empty title={`No ${KIND_FACTS[kind].noun.toLowerCase()} workloads`}>
            {KIND_FACTS[kind].what} Nothing of this kind has been recorded for
            your organization yet. The other kinds may still have something.
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
              {state.data.items.map((d) => (
                <Row key={d.id} onClick={() => onOpen(d.id)}>
                  <Td>
                    <CellLink href={`/workloads?definition=${encodeURIComponent(d.id)}`}>
                      {d.name}
                    </CellLink>
                  </Td>
                  <Td label="Source">
                    <Provenance kind={d.kind} />
                  </Td>
                  <Td label="Repository">{d.repository ?? "--"}</Td>
                  <Td label="Runs" numeric>
                    {count(d.run_count)}
                  </Td>
                  <Td label="Last run">
                    {d.last_run_at ? <When value={d.last_run_at} /> : <span className="text-dim">never</span>}
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
    <Card title="Recent runs" note="Across every workload.">
      {state.status === "error" && state.error ? (
        <WorkloadError error={state.error} retry={state.reload} />
      ) : state.status === "loading" || state.data === null ? (
        <TableSkeleton rows={4} cols={4} />
      ) : state.data.items.length === 0 ? (
        <Empty title="Nothing has run">
          A run appears the first time a workload is sent at an environment.
        </Empty>
      ) : (
        <TableWrap>
          <Table>
            <thead>
              <tr>
                <Th>Workload</Th>
                <Th>Source</Th>
                <Th>Side</Th>
                <Th>Status</Th>
                <Th>Started</Th>
              </tr>
            </thead>
            <tbody>
              {state.data.items.map((r) => (
                <Row key={r.id} onClick={() => onOpen(r.id)}>
                  <Td>
                    <CellLink href={`/workloads?run=${encodeURIComponent(r.id)}`}>
                      {r.definition_name ?? r.id.slice(0, 12)}
                    </CellLink>
                  </Td>
                  <Td label="Source">
                    {r.kind ? <Provenance kind={r.kind} /> : <span className="text-dim">--</span>}
                  </Td>
                  <Td label="Side">{r.execution ?? "--"}</Td>
                  <Td label="Status">
                    <RunStatusBadge status={r.status} />
                  </Td>
                  <Td label="Started">
                    <When value={r.started_at ?? r.created_at} />
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
 * Not `/workloads/[id]`. This console is a static export served by the control
 * plane's own process, so there is no server to resolve a dynamic segment and
 * no way to know every id at build time. A detail view is therefore a query
 * string on a static page, the same shape the runs screen already uses.
 */
function Studio() {
  const params = useSearchParams();
  const router = useRouter();
  const definitionId = params.get("definition");
  const runId = params.get("run");
  const [kind, setKind] = useState<Kind | null>(null);

  if (runId) {
    return (
      <Page title="Run" lede="What this workload did, and what it proved.">
        <RunView runId={runId} onClose={() => router.push("/workloads")} />
      </Page>
    );
  }

  if (definitionId) {
    return (
      <Page title="Workload" lede="Where its traffic comes from, every version, and every run.">
        <DefinitionView
          id={definitionId}
          onClose={() => router.push("/workloads")}
          onOpenRun={(id) => router.push(`/workloads?run=${encodeURIComponent(id)}`)}
        />
      </Page>
    );
  }

  return (
    <Page
      title="Workloads"
      lede="Traffic sent at a disposable twin. Observed from production, authored as a scenario, or discovered by an agent: three different things, kept apart because what each one proves is different."
      actions={<KindFilter value={kind} onChange={setKind} />}
    >
      <div className="space-y-6">
        <Definitions
          kind={kind}
          onOpen={(id) => router.push(`/workloads?definition=${encodeURIComponent(id)}`)}
        />
        <RecentRuns onOpen={(id) => router.push(`/workloads?run=${encodeURIComponent(id)}`)} />
      </div>
    </Page>
  );
}

export default function WorkloadsPage() {
  return (
    <Suspense
      fallback={
        <Page title="Workloads">
          <Card title="Workloads">
            <TableSkeleton rows={5} cols={5} />
          </Card>
        </Page>
      }
    >
      <Studio />
    </Suspense>
  );
}
