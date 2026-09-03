"use client";

import { Suspense, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Badge, Button, Card, LinkButton, Loaded, Page, TableSkeleton, When } from "@/components/ui";
import {
  AdminPage,
  DataTable,
  EmptyList,
  FilterBar,
  StatusChip,
  type Column,
} from "@/components/admin/primitives";
import { More } from "@/components/pagination";
import { duration } from "@/lib/loadshapes";
import { toneForStanding } from "@/lib/productshapes";
import { useRuns, type RunKind, type RunRow } from "@/lib/admin-product";

/**
 * The page an operator opens during an incident.
 *
 * IT IS BUILT FOR ONE SEQUENCE: find the run, see why it failed, see what it
 * touched. So the search is first, the failures filter is beside it, and every
 * row leads to the detail rather than to a count. There is no grid of totals at
 * the top, and that is deliberate: a number nobody can act on costs a cross
 * tenant aggregate to compute and answers a question nobody asked at three in
 * the morning.
 *
 * ONE FAMILY AT A TIME. An agent run has verdicts and artifacts, a load run has
 * percentiles and a lease, a pull request check has a head commit and a GitHub
 * check run. They are three objects, not three rows of one. A merged table
 * would need a column set that is true of none of them, and the columns it
 * would have to drop are exactly the ones somebody is here for. So the family
 * is a filter, the table says which one it is showing, and every column in it
 * means something.
 *
 * THE FAILURES FILTER IS COMPUTED BY THE SERVER. Filtering the fifty rows after
 * a page is cut returns an arbitrary number per page and eventually an empty
 * page with a cursor behind it, which reads as the end of a list that has more
 * in it.
 */
export default function ProductRunsPage() {
  return (
    <Suspense
      fallback={
        <Page title="Runs & Jobs">
          <TableSkeleton rows={8} cols={6} />
        </Page>
      }
    >
      <RunsView />
    </Suspense>
  );
}

function RunsView() {
  const params = useSearchParams();
  // Set when this page was opened from one twin. It is a filter the reader did
  // not choose on this screen, so the page says so out loud rather than showing
  // a short list that looks like the whole installation.
  const environmentId = params.get("environmentId");
  const orgId = params.get("org");
  const orgSlug = params.get("slug");

  const [kind, setKind] = useState<RunKind>("agent");
  const [search, setSearch] = useState("");
  const [failedOnly, setFailedOnly] = useState(false);
  const state = useRuns({ kind, search, failedOnly, environmentId, orgId });

  return (
    <AdminPage href="/admin/product/runs">
      {environmentId || orgId ? (
        <div className="mb-4 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border border-rule bg-card px-4 py-3">
          <p className="text-[12.5px] leading-5 text-ink">
            {environmentId ? (
              <>
                Showing runs on one twin only. Everything below is scoped to environment{" "}
                <span className="font-mono text-[12px]">{environmentId.slice(0, 8)}</span>.
              </>
            ) : (
              <>
                Showing one organization only. Everything below belongs to{" "}
                <span className="font-mono text-[12px]">{orgSlug ?? orgId!.slice(0, 8)}</span>.
              </>
            )}
          </p>
          <LinkButton href="/admin/product/runs" variant="secondary">
            Show every run
          </LinkButton>
        </div>
      ) : null}

      <Card>
        <FilterBar
          search={{
            value: search,
            onChange: setSearch,
            label: "Search runs by repository, branch or environment",
            placeholder: "Repository, branch or environment",
          }}
          filters={[
            {
              label: "Family",
              value: kind,
              onChange: (next) => setKind(next as RunKind),
              options: [
                { value: "agent", label: "Agent runs" },
                { value: "load", label: "Load runs" },
                { value: "check", label: "Pull request checks" },
              ],
            },
            {
              label: "Show",
              value: failedOnly ? "failed" : "all",
              onChange: (next) => setFailedOnly(next === "failed"),
              options: [
                { value: "all", label: "Everything, newest first" },
                { value: "failed", label: "Failures only" },
              ],
            },
          ]}
        />
        <Loaded state={state} skeleton={<TableSkeleton rows={8} cols={6} />}>
          {(rows) => (
            <DataTable
              columns={COLUMNS[kind]}
              rows={rows}
              keyOf={(r) => r.id}
              href={(r) =>
                `/admin/product/runs/detail?kind=${r.kind}&id=${encodeURIComponent(r.id)}`
              }
              empty={
                <RunsEmpty
                  kind={kind}
                  search={search}
                  failedOnly={failedOnly}
                  scoped={environmentId !== null}
                  onShowEverything={() => {
                    setSearch("");
                    setFailedOnly(false);
                  }}
                />
              }
              footer={
                <More
                  shown={rows.length}
                  noun={NOUN[kind]}
                  hasMore={state.hasMore}
                  busy={state.busy}
                  error={state.moreError}
                  onMore={state.more}
                />
              }
            />
          )}
        </Loaded>
      </Card>
    </AdminPage>
  );
}

const NOUN: Record<RunKind, { one: string; many: string }> = {
  agent: { one: "agent run", many: "agent runs" },
  load: { one: "load run", many: "load runs" },
  check: { one: "check", many: "checks" },
};

const FAMILY: Record<RunKind, string> = {
  agent: "agent run",
  load: "load run",
  check: "pull request check",
};

/**
 * Why the list is empty, and it is never the same sentence twice.
 *
 * "No failures" is the answer an operator wants and "no runs at all" is the one
 * that means something is broken upstream, and a single empty state that reads
 * the same for both is how an outage gets mistaken for a quiet morning.
 */
function RunsEmpty({
  kind,
  search,
  failedOnly,
  scoped,
  onShowEverything,
}: {
  kind: RunKind;
  search: string;
  failedOnly: boolean;
  scoped: boolean;
  onShowEverything: () => void;
}) {
  if (failedOnly && !search) {
    return (
      <EmptyList
        title={`No ${FAMILY[kind]} has failed`}
        action={<Button onClick={onShowEverything}>Show everything</Button>}
      >
        {scoped
          ? "Nothing has failed on this twin. This counts the failures the control plane was told about, so a run that never reported at all is not here: it is in the full list, still running or abandoned."
          : "Nothing in this family has failed recently. This counts the failures the control plane was told about, so a run that never reported at all is not here: switch to everything and look for one that is still running past its deadline."}
      </EmptyList>
    );
  }
  if (search || failedOnly) {
    return (
      <EmptyList
        title="Nothing matches those filters"
        action={<Button onClick={onShowEverything}>Clear the filters</Button>}
      >
        The search runs over the family above it, so a run of a different kind will not appear while
        this is set to {FAMILY[kind]}s.
      </EmptyList>
    );
  }
  return (
    <EmptyList title={`No ${FAMILY[kind]} on this installation`}>
      {kind === "agent"
        ? "No agent run has ever been reported. Agent runs arrive from the engine as it works, so an installation with twins and no runs means the engine is reaching the environments and not reporting back."
        : kind === "load"
          ? "Nobody has run a workload. Load runs are requested per workload rather than created with a twin, so an installation with none is ordinary rather than broken."
          : "No pull request has been checked. Checks arrive from the GitHub app, so if there are open pull requests on installed repositories this is worth chasing."}
    </EmptyList>
  );
}

/* -------------------------------------------------------------------------
 * The columns, per family
 *
 * Three sets rather than one, because the point of the family filter is that
 * these are different objects. A shared set would be the columns all three have
 * in common, which is the ones nobody comes here for.
 * ---------------------------------------------------------------------- */

function Org({ slug }: { slug: string }) {
  return (
    <Link
      href={`/admin/customers/users/organization?org=${encodeURIComponent(slug)}`}
      className="inline-flex min-h-11 items-center truncate font-mono text-[12px] underline decoration-transparent underline-offset-4 hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
    >
      {slug}
    </Link>
  );
}

function Outcome({ run }: { run: RunRow }) {
  // The standing decides the colour, and the family's own state word is shown
  // BESIDE it only when the two differ. An operator looking for `abandoned`
  // needs to read `abandoned` rather than a word this console chose for it, and
  // a run whose state is `failed` under a chip reading FAILED said the same
  // thing twice in two type sizes.
  const state = run.state.replace(/_/g, " ");
  return (
    <span className="flex flex-wrap items-center gap-1.5">
      <Badge tone={toneForStanding(run.standing)}>{run.standing}</Badge>
      {state === run.standing ? null : (
        <span className="text-[12px] text-muted">{state}</span>
      )}
    </span>
  );
}

/**
 * The one line that says why, and the reason it has a minimum width.
 *
 * At eight columns this cell was allocated about six characters by the table's
 * auto layout, so "The basket total stayed at zero after adding an item" came
 * out one word per line down a column nobody could read. The failure text is
 * the point of this screen, so it claims a floor and the columns beside it give
 * way, which is what `TableWrap` scrolling exists for.
 */
function Failure({ run }: { run: RunRow }) {
  if (!run.failure) {
    return run.standing === "failed" ? (
      // Failed with nothing said. That is a real and unhelpful state, and the
      // page says which of the two it is rather than leaving a blank that
      // reads as "no problem".
      <span className="block min-w-[18ch] text-[12.5px] leading-5 text-muted">
        Nothing was reported
      </span>
    ) : (
      <span className="text-dim">--</span>
    );
  }
  return (
    <span className="block min-w-[24ch] max-w-[46ch] break-words text-[12.5px] leading-5 text-ink">
      {run.failure}
    </span>
  );
}

/** When it ran and how long it took, in one column.
 *
 *  Two columns for these was what pushed the failure text into a sliver. They
 *  are one fact read together anyway: an operator scanning for the slow one is
 *  comparing durations against the moment they started. */
function Timing({ run, label }: { run: RunRow; label: string }) {
  return (
    <span className="block min-w-0">
      <span className="block">
        <When value={run.startedAt ?? run.at} />
      </span>
      <span className="block whitespace-nowrap text-[12px] text-muted">
        {run.durationMs === null ? `${label}, still going` : `took ${duration(run.durationMs)}`}
      </span>
    </span>
  );
}

const COLUMNS: Record<RunKind, Column<RunRow>[]> = {
  agent: [
    {
      key: "run",
      header: "Run",
      cell: (r) => (
        <span className="block min-w-0">
          <span className="block break-words font-mono text-[12px] font-medium text-ink">
            {r.envId ?? r.id.slice(0, 8)}
          </span>
          <span className="block truncate text-[12px] text-muted">{r.repository}</span>
        </span>
      ),
    },
    { key: "org", header: "Organization", cell: (r) => <Org slug={r.orgSlug} /> },
    {
      key: "ref",
      header: "Branch",
      cell: (r) => (
        <span className="block min-w-0">
          <span className="block break-words font-mono text-[12px]">{r.ref}</span>
          {r.pullRequest === null ? null : (
            <span className="block text-[12px] text-muted">Pull request {r.pullRequest}</span>
          )}
        </span>
      ),
    },
    {
      key: "outcome",
      header: "Outcome",
      cell: (r) => (
        <span className="block min-w-0">
          <Outcome run={r} />
          <span className="mt-0.5 block text-[12px] text-muted">
            {r.verdict ?? "no verdict reported"}
          </span>
        </span>
      ),
    },
    { key: "failure", header: "Why", cell: (r) => <Failure run={r} /> },
    { key: "at", header: "Started", cell: (r) => <Timing run={r} label="queued" /> },
  ],

  load: [
    {
      key: "run",
      header: "Run",
      cell: (r) => (
        <span className="block min-w-0">
          <span className="block truncate font-medium text-ink">{r.repository}</span>
          <span className="block break-words font-mono text-[12px] text-muted">{r.ref}</span>
        </span>
      ),
    },
    { key: "org", header: "Organization", cell: (r) => <Org slug={r.orgSlug} /> },
    {
      key: "outcome",
      header: "Outcome",
      cell: (r) => (
        <span className="block min-w-0">
          <Outcome run={r} />
          <span className="mt-0.5 block text-[12px] text-muted">
            {r.verdict ? `verdict ${r.verdict}` : "no verdict reported"}
          </span>
        </span>
      ),
    },
    {
      key: "env",
      header: "Twin",
      cell: (r) =>
        r.envId ? (
          <span className="block break-words font-mono text-[12px]">{r.envId}</span>
        ) : (
          <span className="text-dim">--</span>
        ),
    },
    { key: "failure", header: "Why", cell: (r) => <Failure run={r} /> },
    { key: "at", header: "Requested", cell: (r) => <Timing run={r} label="requested" /> },
  ],

  check: [
    {
      key: "run",
      header: "Check",
      cell: (r) => (
        <span className="block min-w-0">
          <span className="block truncate font-medium text-ink">{r.repository}</span>
          <span className="block text-[12px] text-muted">Pull request {r.pullRequest}</span>
        </span>
      ),
    },
    { key: "org", header: "Organization", cell: (r) => <Org slug={r.orgSlug} /> },
    {
      key: "ref",
      header: "Head branch",
      cell: (r) => <span className="block break-words font-mono text-[12px]">{r.ref}</span>,
    },
    { key: "outcome", header: "Outcome", cell: (r) => <Outcome run={r} /> },
    { key: "failure", header: "Why", cell: (r) => <Failure run={r} /> },
    { key: "at", header: "Queued", cell: (r) => <Timing run={r} label="queued" /> },
  ],
};
