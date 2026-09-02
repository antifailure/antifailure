"use client";

import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { may } from "@/lib/roles";
import { useSessionContext } from "@/components/session";
import {
  Button,
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
import { KindMark, StateBadge, VerdictBadge } from "@/components/load/primitives";
import { NewWorkload, WorkloadDetailView } from "@/components/load/workload";
import { RunView } from "@/components/load/run";
import { Promote } from "@/components/load/exploration";
import { LoadError } from "@/components/load/states";
import { WindowFooter, useWindow } from "@/components/load/paging";
import {
  KINDS,
  KIND_FACTS,
  count,
  listRuns,
  listWorkloads,
  type Kind,
  type RunRow,
  type WorkloadRow,
} from "@/lib/load";

/* -------------------------------------------------------------------------
 * The list
 * ---------------------------------------------------------------------- */

/**
 * Filtering by what a workload is.
 *
 * Real buttons rather than a select, because there are five choices and the
 * set is closed. No count on the buttons: it would need a query per kind, and
 * a number that is only right until somebody else starts a run is worse than
 * no number.
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
    <div className="flex flex-wrap gap-1.5" role="group" aria-label="Filter by kind">
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

function Workloads({
  kind,
  onOpen,
  canEdit,
}: {
  kind: Kind | null;
  onOpen: (slug: string) => void;
  canEdit: boolean;
}) {
  const [making, setMaking] = useState(false);
  const state = useWindow<WorkloadRow>(
    (limit) => listWorkloads({ ...(kind ? { kind } : {}), limit }),
    [kind],
  );

  return (
    <Card
      title="Workloads"
      note="What can be run against a twin, and what each one last found."
      actions={
        canEdit ? (
          <Button onClick={() => setMaking((m) => !m)}>
            {making ? "Cancel" : "New workload"}
          </Button>
        ) : undefined
      }
    >
      {making ? (
        <NewWorkload
          onCreated={(slug) => {
            setMaking(false);
            onOpen(slug);
          }}
        />
      ) : null}
      {state.status === "error" && state.error ? (
        <LoadError error={state.error} retry={state.reload} />
      ) : state.status === "loading" ? (
        <TableSkeleton rows={5} cols={5} />
      ) : state.items.length === 0 ? (
        kind === null ? (
          <Empty title="No workloads yet">
            A workload is something the engine can run against a twin, selected
            out of your own manifest by name: a traffic mix, a scenario, a
            browser workflow, or an exploration. Make one and every run of it
            appears here, with what each one proved.
          </Empty>
        ) : (
          <Empty title={`No ${KIND_FACTS[kind].noun.toLowerCase()} workloads`}>
            {KIND_FACTS[kind].what} Nothing of this kind has been defined for
            your organization yet. The other three may still have something.
          </Empty>
        )
      ) : (
        <>
          <TableWrap>
            <Table>
              <thead>
                <tr>
                  <Th>Name</Th>
                  <Th>Kind</Th>
                  <Th>Repository</Th>
                  <Th numeric>Runs</Th>
                  <Th>Last run</Th>
                </tr>
              </thead>
              <tbody>
                {state.items.map((w) => (
                  <Row key={w.slug} onClick={() => onOpen(w.slug)}>
                    <Td>
                      <CellLink href={`/load?workload=${encodeURIComponent(w.slug)}`}>
                        {w.name}
                      </CellLink>
                    </Td>
                    <Td label="Kind">
                      <KindMark kind={w.kind} />
                    </Td>
                    <Td label="Repository">{w.repository ?? "--"}</Td>
                    <Td label="Runs" numeric>
                      {count(w.runs)}
                    </Td>
                    <Td label="Last run">
                      {w.lastRunAt ? (
                        <span className="flex flex-wrap items-center gap-2">
                          <When value={w.lastRunAt} />
                          <VerdictBadge verdict={w.lastVerdict} />
                          {w.lastState ? <StateBadge state={w.lastState} /> : null}
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
          <WindowFooter
            count={state.items.length}
            noun="workload"
            more={state.more}
            atCap={state.atCap}
            widening={state.widening}
            widenError={state.widenError}
            onWiden={state.widen}
            narrow="Filter by kind to reach the rest."
          />
        </>
      )}
    </Card>
  );
}

function RecentRuns({ onOpen }: { onOpen: (runId: string) => void }) {
  // Twenty rather than the list default of fifty. This is the second card on a
  // page whose subject is the workloads above it, so it is a recent activity
  // glance with a way to go further, not the run history.
  const state = useWindow<RunRow>((limit) => listRuns({ limit }), [], 20);
  return (
    <Card title="Recent runs" note="Across every workload.">
      {state.status === "error" && state.error ? (
        <LoadError error={state.error} retry={state.reload} />
      ) : state.status === "loading" ? (
        <TableSkeleton rows={4} cols={5} />
      ) : state.items.length === 0 ? (
        <Empty title="Nothing has run">
          A run appears the first time a workload is sent at an environment.
        </Empty>
      ) : (
        <>
          <TableWrap>
            <Table>
              <thead>
                <tr>
                  <Th>Workload</Th>
                  <Th>Verdict</Th>
                  <Th>State</Th>
                  <Th>Environment</Th>
                  <Th>Requested</Th>
                </tr>
              </thead>
              <tbody>
                {state.items.map((r) => (
                  <Row key={r.id} onClick={() => onOpen(r.id)}>
                    <Td>
                      <CellLink href={`/load?run=${encodeURIComponent(r.id)}`}>
                        {r.workloadSlug ?? r.id}
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
                    <Td label="Requested">
                      <When value={r.requestedAt} />
                    </Td>
                  </Row>
                ))}
              </tbody>
            </Table>
          </TableWrap>
          <WindowFooter
            count={state.items.length}
            noun="run"
            more={state.more}
            atCap={state.atCap}
            widening={state.widening}
            widenError={state.widenError}
            onWiden={state.widen}
            narrow="Open a workload to see only its own."
          />
        </>
      )}
    </Card>
  );
}

/**
 * The way in to promotion, kept off the index as a form.
 *
 * Promotion needs a document that lives on whoever ran the command, so it is a
 * screen rather than a button, and putting the whole form on the index would
 * give the largest control on the page to the rarest action. The sentence is
 * here rather than only there because somebody arriving at Load and seeing
 * "exploration" will assume it is a fourth kind of traffic unless told
 * otherwise in the first line they read.
 */
function ExplorationCard({ onPromote }: { onPromote: () => void }) {
  return (
    <Card title="Exploration" note="An agent choosing its own way through the product, from a seed.">
      <div className="px-4 py-4">
        <p className="max-w-[74ch] text-[12.5px] leading-6 text-muted">
          An exploration finds a route nobody wrote down. It does not produce
          load: <code className="font-mono">af explore</code> drives a real browser, and what it
          reached can be compiled into a workflow for your manifest, which{" "}
          <code className="font-mono">af test</code> runs. A discovery is worth nothing until
          somebody commits it, and that is the step this does.
        </p>
        <div className="mt-3">
          <Button onClick={onPromote}>Promote an exploration</Button>
        </div>
      </div>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * The four views
 * ---------------------------------------------------------------------- */

/**
 * One page, four views, selected by the query string.
 *
 * Not `/load/[slug]`. This console is a static export served by the control
 * plane's own process, so there is no server to resolve a dynamic segment and
 * no way to know every name at build time. A detail view is a query string on
 * a static page, the same shape the runs screen already uses.
 */
function Load() {
  const params = useSearchParams();
  const router = useRouter();
  const session = useSessionContext();
  const canEdit = may(session.data?.role, "workloads.edit");
  const slug = params.get("workload");
  const runId = params.get("run");
  const promoting = params.get("promote");
  const [kind, setKind] = useState<Kind | null>(null);

  const openRun = (id: string) => router.push(`/load?run=${encodeURIComponent(id)}`);
  const openWorkload = (s: string) => router.push(`/load?workload=${encodeURIComponent(s)}`);

  if (runId) {
    return (
      <Page title="Run" lede="What this run did, what it found, and how to run it again.">
        <RunView
          runId={runId}
          onClose={() => router.push("/load")}
          onOpenRun={openRun}
          onOpenWorkload={openWorkload}
        />
      </Page>
    );
  }

  if (promoting !== null) {
    return (
      <Page
        title="Promote an exploration"
        lede="Compiles what an agent found into a workflow your manifest can run, and says in full what it could not carry across."
        actions={<Button onClick={() => router.push("/load")}>Back to Load</Button>}
      >
        <Promote
          fromWorkload={promoting === "1" ? null : promoting}
          onPromoted={() => undefined}
        />
      </Page>
    );
  }

  if (slug) {
    return (
      <Page title="Workload" lede="What it runs, every version of it, and every run.">
        <WorkloadDetailView
          slug={slug}
          onClose={() => router.push("/load")}
          onOpenRun={openRun}
          onPromote={(s) => router.push(`/load?promote=${encodeURIComponent(s)}`)}
        />
      </Page>
    );
  }

  return (
    <Page
      title="Load"
      lede="Work shaped like production's own, run against a disposable twin. A load test that hammers one endpoint proves the endpoint is fast, which nobody doubted; what breaks under real traffic is the mix."
      actions={<KindFilter value={kind} onChange={setKind} />}
    >
      <div className="space-y-6">
        <Workloads kind={kind} onOpen={openWorkload} canEdit={canEdit} />
        <RecentRuns onOpen={openRun} />
        <ExplorationCard onPromote={() => router.push("/load?promote=1")} />
      </div>
    </Page>
  );
}

export default function LoadPage() {
  return (
    <Suspense
      fallback={
        <Page title="Load">
          <Card title="Workloads">
            <TableSkeleton rows={5} cols={5} />
          </Card>
        </Page>
      }
    >
      <Load />
    </Suspense>
  );
}
