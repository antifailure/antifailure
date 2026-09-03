"use client";

import { Suspense } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import {
  Badge,
  Card,
  CardSkeleton,
  Empty,
  LinkButton,
  Loaded,
  Page,
  When,
} from "@/components/ui";
import {
  AdminPage,
  DataTable,
  EmptyList,
  Facts,
  StatusChip,
  type Fact,
} from "@/components/admin/primitives";
import { bytes } from "@/lib/format";
import { duration } from "@/lib/loadshapes";
import { expiryPhrase, toneForStanding, type RunStanding } from "@/lib/productshapes";
import {
  useTwin,
  type TwinDetail,
  type TwinRunSummary,
  type TwinTeardown,
  type TwinWorkloadSummary,
} from "@/lib/admin-product";

/**
 * One twin, and everything hanging off it.
 *
 * A QUERY STRING rather than /admin/product/twins/[id], because the console is
 * a static export and a dynamic segment cannot be exported without knowing
 * every id at build time. next.config.ts says so outright.
 *
 * THE ORDER OF THE PANELS IS THE ORDER THE QUESTIONS ARE ASKED. What is it and
 * whose is it. What data was it built from and was that data ever verified.
 * What has run on it. Has anybody already asked for it to go away. The last one
 * is on the page because an operator who cannot see a pending teardown asks for
 * a second one, and two requests against one environment is how a queue starts
 * fighting itself.
 */
export default function ProductTwinDetailPage() {
  return (
    // useSearchParams needs a boundary in an exported app, and the fallback is
    // the shape of the page rather than a spinner, so the layout does not jump
    // when the id resolves.
    <Suspense
      fallback={
        <Page title="Twin">
          <CardSkeleton count={2} />
        </Page>
      }
    >
      <TwinDetailView />
    </Suspense>
  );
}

function TwinDetailView() {
  const params = useSearchParams();
  const id = params.get("id");
  const state = useTwin(id ?? "");

  if (!id) {
    return (
      <AdminPage title="Twin" lede="One environment, and the runs on it.">
        <Card>
          <Empty
            title="No twin named"
            action={<LinkButton href="/admin/product/twins">Open the twin list</LinkButton>}
          >
            This page needs a twin in its address. Open one from the list rather than typing the
            address by hand.
          </Empty>
        </Card>
      </AdminPage>
    );
  }

  return (
    <Loaded
      state={state}
      framed
      skeleton={
        <Page title="Twin">
          <CardSkeleton count={3} />
        </Page>
      }
    >
      {(twin) => <TwinBody twin={twin} />}
    </Loaded>
  );
}

function TwinBody({ twin }: { twin: TwinDetail }) {
  const facts: Fact[] = [
    {
      label: "Organization",
      value: (
        <Link
          href={`/admin/customers/users/organization?org=${encodeURIComponent(twin.orgSlug)}`}
          className="inline-flex min-h-11 items-center underline decoration-transparent underline-offset-4 hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
        >
          {twin.orgName} <span className="ml-1.5 font-mono text-[12px] text-muted">{twin.orgSlug}</span>
        </Link>
      ),
    },
    { label: "Repository", value: twin.repository, mono: true },
    {
      label: "Branch",
      value: (
        <span>
          <span className="font-mono text-[12px]">{twin.branch}</span>
          {twin.defaultBranch === twin.branch ? (
            <span className="ml-2 text-[12px] text-muted">the default branch</span>
          ) : null}
        </span>
      ),
    },
    {
      label: "Pull request",
      value: twin.pullRequest === null ? null : `Number ${twin.pullRequest}`,
    },
    {
      label: "State",
      value: (
        <span className="flex flex-wrap items-center gap-1.5">
          <StatusChip value={twin.state} />
          {twin.tornDownAt ? <Badge tone="neutral">torn down</Badge> : null}
        </span>
      ),
    },
    {
      label: "Preview address",
      value: twin.previewUrl ? (
        // rel on an outbound link to a customer's own environment. It is not
        // this console's origin and it is not trusted with a window handle.
        <a
          href={twin.previewUrl}
          rel="noreferrer noopener"
          target="_blank"
          className="inline-flex min-h-11 items-center break-words underline decoration-[rgba(16,16,16,0.25)] underline-offset-4 hover:decoration-ink sm:min-h-0"
        >
          {twin.previewUrl}
        </a>
      ) : null,
    },
    { label: "Runtime", value: twin.runtime, mono: true },
    { label: "Engine environment id", value: twin.envId, mono: true },
    { label: "Created by", value: twin.createdBy, mono: true },
    { label: "Created", value: <When value={twin.createdAt} /> },
    { label: "Last event applied", value: <When value={twin.updatedAt} /> },
    {
      label: "Expiry",
      value: (
        <span className={twin.tornDownAt === null && isOverdue(twin) ? "text-warn" : undefined}>
          {expiryPhrase(twin.expiresAt)}
          {twin.expiresAt ? (
            <span className="ml-2 text-[12px] text-muted">
              <When value={twin.expiresAt} />
            </span>
          ) : null}
        </span>
      ),
    },
    { label: "Torn down", value: twin.tornDownAt ? <When value={twin.tornDownAt} /> : null },
    {
      label: "Highest event sequence",
      // Tabular, because it is a number somebody compares against the one in a
      // log line. Events arrive out of order and this is what the row last
      // believed.
      value: <span className="tnum">{twin.lastSequence.toLocaleString()}</span>,
    },
  ];

  return (
    <AdminPage
      title={twin.envId}
      lede={`${twin.repository} on ${twin.branch}, owned by ${twin.orgSlug}.`}
      actions={
        // Two links because the question widens. "Why did this one fail" is
        // answered on the twin; "is the whole tenant failing" is the next
        // thing somebody asks and is a different query.
        <span className="flex flex-wrap items-center gap-2">
          <LinkButton
            variant="secondary"
            href={
              `/admin/product/runs?org=${encodeURIComponent(twin.orgId)}` +
              `&slug=${encodeURIComponent(twin.orgSlug)}`
            }
          >
            Every run for {twin.orgSlug}
          </LinkButton>
          <LinkButton href={`/admin/product/runs?environmentId=${encodeURIComponent(twin.id)}`}>
            Every run on this twin
          </LinkButton>
        </span>
      }
    >
      <div className="space-y-6">
        <Card title="This twin">
          <Facts facts={facts} />
        </Card>

        <Card
          title="Golden data"
          note="The scanned copy this environment was built from."
        >
          {twin.goldenVersion === null ? (
            <Empty title="This twin names no golden version">
              The environment row carries no golden version, so nothing here says which scanned copy
              it was built from. That is the state a twin is in before its first scan lands.
            </Empty>
          ) : twin.golden === null ? (
            <Empty title="No record of that version">
              The twin names <span className="font-mono text-[12px]">{twin.goldenVersion}</span> and
              this control plane holds no golden version row for it. Nothing has failed; a version
              recorded by an engine that never reported back looks exactly like this. It is not the
              same as unverified, and this page will not call it that.
            </Empty>
          ) : (
            <Facts
              facts={[
                { label: "Version", value: twin.golden.version, mono: true },
                {
                  label: "Verified",
                  value: twin.golden.verified ? (
                    <Badge tone="pass">verified</Badge>
                  ) : (
                    <span className="flex flex-wrap items-center gap-2">
                      <Badge tone="warn">not verified</Badge>
                      <span className="text-[12.5px] text-muted">
                        The scan produced this copy and nothing attested to it.
                      </span>
                    </span>
                  ),
                },
                { label: "Source digest", value: twin.golden.sourceDigest, mono: true },
                { label: "Masking rules digest", value: twin.golden.rulesDigest, mono: true },
                { label: "Size", value: bytes(twin.golden.sizeBytes) },
                { label: "Scanned", value: <When value={twin.golden.createdAt} /> },
              ]}
            />
          )}
        </Card>

        <Card
          title="Agent runs"
          note="The twenty most recent. A run that finished is not a run that passed."
        >
          <DataTable
            columns={AGENT_COLUMNS}
            rows={twin.runs}
            keyOf={(r) => r.id}
            href={(r) => `/admin/product/runs/detail?kind=agent&id=${encodeURIComponent(r.id)}`}
            empty={
              <EmptyList title="No agent run on this twin">
                The environment exists and nothing has been run against it. A run appears here when
                the engine reports one, which for a pull request twin is usually within a minute of
                it coming up.
              </EmptyList>
            }
          />
        </Card>

        <Card title="Load runs" note="The twenty most recent.">
          <DataTable
            columns={LOAD_COLUMNS}
            rows={twin.workloadRuns}
            keyOf={(r) => r.id}
            href={(r) => `/admin/product/runs/detail?kind=load&id=${encodeURIComponent(r.id)}`}
            empty={
              <EmptyList title="No load run on this twin">
                Nobody has run a workload against this environment. Load runs are requested per
                workload rather than created with the twin, so an environment with none is ordinary.
              </EmptyList>
            }
          />
        </Card>

        <Card
          title="Teardown requests"
          note="Asked for is not dispatched, and dispatched is not confirmed."
        >
          <DataTable
            columns={TEARDOWN_COLUMNS}
            rows={twin.teardowns}
            keyOf={(t) => t.id}
            empty={
              <EmptyList title="Nobody has asked for this twin to go away">
                No teardown has been recorded against this environment. If it is past its expiry,
                the sweeper has not reached it and nobody has asked by hand.
              </EmptyList>
            }
          />
        </Card>
      </div>
    </AdminPage>
  );
}

/**
 * The standing, with the run's own state word beside it only when they differ.
 *
 * The pairing is the point: a load run whose state is `succeeded` and whose
 * standing is `failed` ran to completion and found a failure, and seeing both
 * is what stops somebody reading the green word and closing the tab. When they
 * agree there is nothing to reconcile, and "FAILED failed" said the same thing
 * twice in two type sizes.
 */
function Outcome({ standing, state }: { standing: RunStanding; state: string }) {
  const word = state.replace(/_/g, " ");
  return (
    <span className="flex flex-wrap items-center gap-1.5">
      <Badge tone={toneForStanding(standing)}>{standing}</Badge>
      {word === standing ? null : <span className="text-[12px] text-muted">{word}</span>}
    </span>
  );
}

function isOverdue(twin: TwinDetail): boolean {
  if (twin.expiresAt === null) return false;
  return new Date(twin.expiresAt).getTime() < Date.now();
}

const AGENT_COLUMNS = [
  {
    key: "kind",
    header: "Run",
    cell: (r: TwinRunSummary) => (
      <span className="block min-w-0">
        <span className="block truncate font-medium text-ink">{r.kind}</span>
        <span className="block truncate font-mono text-[12px] text-muted">{r.id.slice(0, 8)}</span>
      </span>
    ),
  },
  {
    key: "standing",
    header: "Outcome",
    cell: (r: TwinRunSummary) => <Outcome standing={r.standing} state={r.state} />,
  },
  {
    key: "verdicts",
    header: "Verdicts",
    numeric: true,
    cell: (r: TwinRunSummary) =>
      r.verdicts === 0 ? (
        // Zero verdicts on a finished run is the answer, not a blank. A run
        // that reported nothing is exactly the exit-code-zero-over-nothing
        // case, so it is said in words rather than as a 0 beside a green chip.
        <span className="text-dim">none reported</span>
      ) : (
        `${r.verdicts - r.failing} of ${r.verdicts}`
      ),
  },
  {
    key: "duration",
    header: "Took",
    numeric: true,
    cell: (r: TwinRunSummary) =>
      duration(
        r.startedAt && r.finishedAt
          ? new Date(r.finishedAt).getTime() - new Date(r.startedAt).getTime()
          : null,
      ),
  },
  {
    key: "when",
    header: "Started",
    cell: (r: TwinRunSummary) => <When value={r.startedAt ?? r.createdAt} />,
  },
];

const LOAD_COLUMNS = [
  {
    key: "workload",
    header: "Workload",
    cell: (r: TwinWorkloadSummary) => (
      <span className="block truncate font-medium text-ink">{r.workload}</span>
    ),
  },
  {
    key: "standing",
    header: "Outcome",
    cell: (r: TwinWorkloadSummary) => <Outcome standing={r.standing} state={r.state} />,
  },
  {
    key: "verdict",
    header: "Verdict",
    cell: (r: TwinWorkloadSummary) => <StatusChip value={r.verdict} />,
  },
  {
    key: "failure",
    header: "Failure",
    cell: (r: TwinWorkloadSummary) =>
      r.failureCode ? (
        <span className="font-mono text-[12px] text-fail">{r.failureCode}</span>
      ) : (
        <span className="text-dim">--</span>
      ),
  },
  {
    key: "when",
    header: "Requested",
    cell: (r: TwinWorkloadSummary) => <When value={r.requestedAt} />,
  },
];

const TEARDOWN_COLUMNS = [
  {
    key: "state",
    header: "Standing",
    cell: (t: TwinTeardown) => <StatusChip value={t.state} />,
  },
  { key: "reason", header: "Reason", cell: (t: TwinTeardown) => t.reason },
  {
    key: "attempts",
    header: "Attempts",
    numeric: true,
    cell: (t: TwinTeardown) => t.attempts.toLocaleString(),
  },
  {
    key: "error",
    header: "Last error",
    cell: (t: TwinTeardown) =>
      t.lastError ? (
        <span className="break-words text-[12.5px] text-fail">{t.lastError}</span>
      ) : (
        <span className="text-dim">--</span>
      ),
  },
  {
    key: "requested",
    header: "Asked",
    cell: (t: TwinTeardown) => <When value={t.requestedAt} />,
  },
  {
    key: "acknowledged",
    header: "Confirmed",
    cell: (t: TwinTeardown) =>
      t.acknowledgedAt ? (
        <When value={t.acknowledgedAt} />
      ) : (
        // Not a blank. A teardown that was recorded and never confirmed is a
        // real and common state, and it is the one worth seeing.
        <span className="text-muted">not yet</span>
      ),
  },
];
