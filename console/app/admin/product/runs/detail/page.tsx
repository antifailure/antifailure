"use client";

import { Suspense } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Badge, Card, CardSkeleton, Empty, LinkButton, Loaded, Page, When } from "@/components/ui";
import {
  AdminPage,
  DataTable,
  EmptyList,
  Facts,
  MetricRow,
  StatusChip,
  type Fact,
} from "@/components/admin/primitives";
import { bytes } from "@/lib/format";
import { duration } from "@/lib/loadshapes";
import { metricsFor, toneForStanding } from "@/lib/productshapes";
import {
  useRun,
  type AgentRunDetail,
  type AgentVerdict,
  type CheckRunDetail,
  type LoadRunDetail,
  type RunArtifact,
  type RunKind,
} from "@/lib/admin-product";

/**
 * One run, and why it ended the way it did.
 *
 * THREE SHAPES BEHIND ONE ADDRESS. The family travels in the query string
 * beside the id, because the three run families share an id space only by
 * accident and asking the server to guess which table a uuid is in would be a
 * lookup in three tables to answer a question the link already knew.
 *
 * WHAT EACH SHAPE LEADS WITH is the thing somebody came for. An agent run leads
 * with its verdicts, because "which workflow failed" is the question. A load
 * run leads with its numbers and its reproduce command. A check leads with the
 * commit, because a check that passed on the wrong head is the failure that
 * looks like a pass.
 */
export default function ProductRunDetailPage() {
  return (
    <Suspense
      fallback={
        <Page title="Run">
          <CardSkeleton count={2} />
        </Page>
      }
    >
      <RunDetailView />
    </Suspense>
  );
}

function isRunKind(value: string | null): value is RunKind {
  return value === "agent" || value === "load" || value === "check";
}

function RunDetailView() {
  const params = useSearchParams();
  const id = params.get("id");
  const kindParam = params.get("kind");
  const kind: RunKind = isRunKind(kindParam) ? kindParam : "agent";
  const state = useRun(kind, id ?? "");

  if (!id || !isRunKind(kindParam)) {
    return (
      <AdminPage title="Run" lede="One run, and what it touched.">
        <Card>
          <Empty
            title={id ? "That is not a run family" : "No run named"}
            action={<LinkButton href="/admin/product/runs">Open the run list</LinkButton>}
          >
            {id
              ? "The address names a family this portal does not serve. The three are agent, load and check."
              : "This page needs a run in its address. Open one from the list rather than typing the address by hand."}
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
        <Page title="Run">
          <CardSkeleton count={3} />
        </Page>
      }
    >
      {(run) =>
        run.kind === "agent" ? (
          <AgentRun run={run} />
        ) : run.kind === "load" ? (
          <LoadRun run={run} />
        ) : (
          <CheckRun run={run} />
        )
      }
    </Loaded>
  );
}

/* -------------------------------------------------------------------------
 * Shared pieces
 * ---------------------------------------------------------------------- */

function OrgLink({ slug }: { slug: string }) {
  return (
    <Link
      href={`/admin/customers/users/organization?org=${encodeURIComponent(slug)}`}
      className="inline-flex min-h-11 items-center font-mono text-[12px] underline decoration-transparent underline-offset-4 hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
    >
      {slug}
    </Link>
  );
}

/**
 * The standing, with the family's own state word beside it only when the two
 * differ.
 *
 * The pairing is the point of this component: a load run whose state is
 * `succeeded` and whose standing is `failed` ran to completion and found a
 * failure, and seeing both together is what stops somebody reading the green
 * word and closing the tab. When they agree there is nothing to reconcile, and
 * "FAILED state failed" said the same thing twice in two type sizes.
 */
function Standing({ standing, state }: { standing: string; state: string }) {
  const word = state.replace(/_/g, " ");
  return (
    <span className="flex flex-wrap items-center gap-2">
      <Badge tone={toneForStanding(standing as never)}>{standing}</Badge>
      {word === standing ? null : (
        <span className="text-[12.5px] text-muted">the run&rsquo;s own state is {word}</span>
      )}
    </span>
  );
}

/** A block of machine text: a command, a digest, a payload. Scrolls inside its
 *  own box rather than pushing the page sideways on a phone. */
function Machine({ children }: { children: string }) {
  return (
    <pre className="scroll-x max-w-full overflow-x-auto rounded-md border border-rule bg-paper px-3 py-2.5 font-mono text-[12px] leading-5 text-ink">
      {children}
    </pre>
  );
}

/* -------------------------------------------------------------------------
 * Agent runs
 * ---------------------------------------------------------------------- */

function AgentRun({ run }: { run: AgentRunDetail }) {
  const failing = run.verdicts.filter((v) => v.value === "fail" || v.value === "blocked");
  const facts: Fact[] = [
    { label: "Organization", value: <OrgLink slug={run.orgSlug} /> },
    { label: "Repository", value: run.repository, mono: true },
    { label: "Branch", value: run.branch, mono: true },
    {
      label: "Pull request",
      value: run.pullRequest === null ? null : `Number ${run.pullRequest}`,
    },
    {
      label: "Twin",
      value: (
        <Link
          href={`/admin/product/twins/detail?id=${encodeURIComponent(run.environmentId)}`}
          className="inline-flex min-h-11 items-center font-mono text-[12px] underline decoration-transparent underline-offset-4 hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
        >
          {run.envId}
        </Link>
      ),
    },
    { label: "Kind", value: run.runKind },
    { label: "Outcome", value: <Standing standing={run.standing} state={run.state} /> },
    { label: "Queued", value: <When value={run.createdAt} /> },
    { label: "Started", value: run.startedAt ? <When value={run.startedAt} /> : null },
    { label: "Finished", value: run.finishedAt ? <When value={run.finishedAt} /> : null },
    { label: "Took", value: duration(run.durationMs) },
    {
      label: "Highest event sequence",
      value: <span className="tnum">{run.lastSequence.toLocaleString()}</span>,
    },
  ];

  return (
    <AdminPage
      title={`${run.runKind} run on ${run.envId}`}
      lede={`${run.repository} on ${run.branch}, owned by ${run.orgSlug}.`}
      actions={
        <LinkButton
          variant="secondary"
          href={`/admin/product/twins/detail?id=${encodeURIComponent(run.environmentId)}`}
        >
          Open the twin
        </LinkButton>
      }
    >
      <div className="space-y-6">
        {run.standing === "unknown" ? (
          // The exit-code-zero-over-nothing case, said out loud. A run that
          // reached a terminal state and reported no verdict at all did nothing
          // and found nothing, and reading it as a pass is the defect this
          // repository has already shipped once.
          <div role="status" className="rounded-lg border border-rule bg-[rgba(138,90,0,0.12)] px-4 py-3">
            <p className="text-[12.5px] leading-5 text-ink">
              This run finished and reported no verdict. It is not a pass and it is not a failure:
              nothing was checked. A run in this state usually means the persona could not be
              created or the environment was not reachable when the engine got there.
            </p>
          </div>
        ) : null}

        <Card title="This run">
          <Facts facts={facts} />
        </Card>

        <Card
          title="Verdicts"
          note={
            failing.length > 0
              ? `${failing.length} of ${run.verdicts.length} failed or were blocked.`
              : undefined
          }
        >
          <DataTable
            columns={VERDICT_COLUMNS}
            rows={run.verdicts}
            keyOf={(v) => v.id}
            empty={
              <EmptyList title="This run reported no verdict">
                The run exists and nothing was recorded against it. That is what a run that could
                not reach the environment looks like, and it is why the outcome above says nothing
                was checked rather than calling it a pass.
              </EmptyList>
            }
          />
        </Card>

        <Card
          title="Artifacts"
          note="What the run left behind. The bytes are not served from this portal."
        >
          <DataTable
            columns={ARTIFACT_COLUMNS}
            rows={run.artifacts}
            keyOf={(a) => a.id}
            empty={
              <EmptyList title="No artifact was recorded">
                Nothing was uploaded for this run. A run that failed before it started leaves
                nothing, and so does one whose artifacts were never retained.
              </EmptyList>
            }
          />
        </Card>
      </div>
    </AdminPage>
  );
}

const VERDICT_COLUMNS = [
  {
    key: "workflow",
    header: "Workflow",
    cell: (v: AgentVerdict) => (
      <span className="block min-w-0">
        <span className="block truncate font-medium text-ink">{v.workflow}</span>
        {v.persona ? (
          <span className="block truncate text-[12px] text-muted">as {v.persona}</span>
        ) : null}
      </span>
    ),
  },
  {
    key: "value",
    header: "Verdict",
    cell: (v: AgentVerdict) => <StatusChip value={v.value} />,
  },
  {
    key: "summary",
    header: "Summary",
    cell: (v: AgentVerdict) =>
      v.summary ? (
        <span className="block max-w-[52ch] break-words text-[12.5px] leading-5">{v.summary}</span>
      ) : (
        <span className="text-dim">--</span>
      ),
  },
  {
    key: "steps",
    header: "Steps",
    numeric: true,
    cell: (v: AgentVerdict) => v.steps.toLocaleString(),
  },
  {
    key: "duration",
    header: "Took",
    numeric: true,
    cell: (v: AgentVerdict) => duration(v.durationMs),
  },
  {
    key: "reproduction",
    header: "Reproduction",
    cell: (v: AgentVerdict) =>
      v.reproduction === null || v.reproduction === undefined ? (
        // Never assembled from the form. A command a console builds drifts from
        // the one that ran, and being the same one is the only reason to print
        // it at all.
        <span className="text-dim">none recorded</span>
      ) : (
        <Machine>{JSON.stringify(v.reproduction, null, 2)}</Machine>
      ),
  },
];

const ARTIFACT_COLUMNS = [
  {
    key: "kind",
    header: "Artifact",
    cell: (a: RunArtifact) => (
      <span className="block min-w-0">
        <span className="block truncate font-medium text-ink">{a.kind}</span>
        {a.contentType ? (
          <span className="block truncate font-mono text-[12px] text-muted">{a.contentType}</span>
        ) : null}
      </span>
    ),
  },
  {
    key: "step",
    header: "Step",
    numeric: true,
    cell: (a: RunArtifact) => (a.step === null ? <span className="text-dim">--</span> : a.step),
  },
  {
    key: "size",
    header: "Size",
    numeric: true,
    cell: (a: RunArtifact) => bytes(a.sizeBytes),
  },
  {
    key: "retained",
    header: "Retained",
    cell: (a: RunArtifact) =>
      a.retained ? (
        <Badge tone="pass">kept</Badge>
      ) : (
        // The row survives retention on purpose, so a timeline can say the
        // bytes went rather than showing a gap that reads as a bug.
        <Badge tone="neutral">bytes removed</Badge>
      ),
  },
  {
    key: "sha",
    header: "Digest",
    mono: true,
    cell: (a: RunArtifact) =>
      a.sha256 ? a.sha256.slice(0, 12) : <span className="text-dim">--</span>,
  },
];

/* -------------------------------------------------------------------------
 * Load runs
 * ---------------------------------------------------------------------- */

function LoadRun({ run }: { run: LoadRunDetail }) {
  // The typed field rather than a cast back to Record. The cast was there
  // because metricsFor takes the loose shape, and casting a typed value to
  // the type it already has is how a field quietly stops being checked.
  const result = run.result;
  const errorReasons = Object.entries(
    (result?.error_reasons as Record<string, number> | undefined) ?? {},
  );
  const refused = (result?.refused_routes as string[] | undefined) ?? [];

  const facts: Fact[] = [
    { label: "Organization", value: <OrgLink slug={run.orgSlug} /> },
    { label: "Workload", value: `${run.workload} (${run.workloadKind.replace(/_/g, " ")})` },
    { label: "Version", value: run.workloadVersion },
    { label: "Repository", value: run.repository, mono: true },
    { label: "Git ref", value: run.gitRef, mono: true },
    {
      label: "Twin",
      value: run.environmentId ? (
        <Link
          href={`/admin/product/twins/detail?id=${encodeURIComponent(run.environmentId)}`}
          className="inline-flex min-h-11 items-center font-mono text-[12px] underline decoration-transparent underline-offset-4 hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
        >
          {run.envId ?? run.environmentId.slice(0, 8)}
        </Link>
      ) : null,
    },
    { label: "Outcome", value: <Standing standing={run.standing} state={run.state} /> },
    { label: "Verdict", value: run.verdict ? <StatusChip value={run.verdict} /> : null },
    {
      label: "Failure code",
      value: run.failureCode ? (
        <span className="font-mono text-[12px] text-fail">{run.failureCode}</span>
      ) : null,
    },
    { label: "Detail", value: run.detail },
    { label: "Attempt", value: run.attempt },
    { label: "Requested", value: <When value={run.requestedAt} /> },
    { label: "Dispatched", value: run.dispatchedAt ? <When value={run.dispatchedAt} /> : null },
    { label: "Started", value: run.startedAt ? <When value={run.startedAt} /> : null },
    { label: "Finished", value: run.finishedAt ? <When value={run.finishedAt} /> : null },
    { label: "Took", value: duration(run.durationMs) },
    { label: "Believed until", value: <When value={run.deadlineAt} /> },
  ];

  const lease: Fact[] = [
    { label: "Held by", value: run.leaseHolder, mono: true },
    { label: "Lease expires", value: run.leaseExpiresAt ? <When value={run.leaseExpiresAt} /> : null },
    { label: "Lease last lost", value: run.leaseLostAt ? <When value={run.leaseLostAt} /> : null },
    {
      label: "Times taken over",
      value: (
        <span className="tnum">
          {run.leaseTakeovers.toLocaleString()}
          {run.leaseTakeovers > 0 && run.result === null ? (
            <span className="ml-2 font-sans text-[12px] text-warn">
              Taken over with no result recorded. That is the shape of an engine that keeps dying,
              not of a slow test.
            </span>
          ) : null}
        </span>
      ),
    },
    {
      label: "Cancel asked",
      value: run.cancelRequestedAt ? <When value={run.cancelRequestedAt} /> : null,
    },
    { label: "Cancel reason", value: run.cancelReason },
    {
      label: "Cancel confirmed",
      value: run.cancelledAt ? (
        <When value={run.cancelledAt} />
      ) : run.cancelRequestedAt ? (
        // Asked for is not done. A cancel recorded and never confirmed is the
        // state a run sits in while nothing is draining the queue.
        <span className="text-warn">asked for, not confirmed</span>
      ) : null,
    },
  ];

  return (
    <AdminPage
      title={`${run.workload} on ${run.repository}`}
      lede={`Load run for ${run.orgSlug}, against ${run.gitRef}.`}
      actions={
        run.environmentId ? (
          <LinkButton
            variant="secondary"
            href={`/admin/product/twins/detail?id=${encodeURIComponent(run.environmentId)}`}
          >
            Open the twin
          </LinkButton>
        ) : undefined
      }
    >
      <div className="space-y-6">
        <Card title="This run">
          <Facts facts={facts} />
        </Card>

        <Card
          title="Reproducing it"
          note="The command the engine reported, not one this console assembled."
        >
          <div className="px-4 py-4">
            {run.reproduceCommand ? (
              <Machine>{run.reproduceCommand}</Machine>
            ) : (
              <p className="text-[13px] leading-6 text-muted">
                No command was recorded. The engine reports one when it starts, so a run with none
                either never started or was reported by a build too old to send it. This page will
                not assemble one, because a command built from the form drifts from the one that
                ran and being the same one is the whole point.
              </p>
            )}
            {run.manifestDigest ? (
              <p className="mt-3 text-[12px] text-dim">
                Manifest digest{" "}
                <span className="font-mono text-[12px] text-muted">{run.manifestDigest}</span>. Two
                runs of the same command against different manifests are different runs.
              </p>
            ) : null}
          </div>
        </Card>

        <Card title="What it measured">
          {result === null ? (
            <Empty title="No result was recorded">
              The run exists and no engine reported a result for it. That is what an abandoned run
              looks like, and it is different from a run that measured zero.
            </Empty>
          ) : (
            <div className="space-y-4 px-4 py-4">
              <MetricRow metrics={metricsFor(result)} />
              <p className="text-[12px] text-dim">
                Recorded <When value={String(result.recorded_at ?? run.finishedAt ?? "")} />
                {result.source ? ` from ${String(result.source)}` : null}.
              </p>
            </div>
          )}
        </Card>

        {errorReasons.length > 0 || refused.length > 0 ? (
          <Card
            title="Why the failures failed"
            note="A total alone loses the only part that says what to fix."
          >
            <div className="space-y-4 px-4 py-4">
              {errorReasons.length > 0 ? (
                <ul className="space-y-1.5">
                  {errorReasons.map(([reason, count]) => (
                    <li key={reason} className="flex flex-wrap items-baseline justify-between gap-3">
                      <span className="font-mono text-[12px] text-ink">{reason}</span>
                      <span className="tnum text-[13px] text-muted">
                        {Number(count).toLocaleString()}
                      </span>
                    </li>
                  ))}
                </ul>
              ) : null}
              {refused.length > 0 ? (
                <div>
                  <p className="text-[12px] font-medium uppercase tracking-[0.08em] text-dim">
                    Routes the safe list refused
                  </p>
                  <ul className="mt-1.5 space-y-1">
                    {refused.map((route) => (
                      <li key={route} className="break-words font-mono text-[12px] text-ink">
                        {route}
                      </li>
                    ))}
                  </ul>
                </div>
              ) : null}
            </div>
          </Card>
        ) : null}

        <Card title="Who was holding it" note="A lost lease is not a dead engine.">
          <Facts facts={lease} />
        </Card>
      </div>
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * Pull request checks
 * ---------------------------------------------------------------------- */

function CheckRun({ run }: { run: CheckRunDetail }) {
  const approvalStale =
    run.fromFork && run.approvedSha !== null && run.approvedSha !== run.headSha;

  const facts: Fact[] = [
    { label: "Organization", value: <OrgLink slug={run.orgSlug} /> },
    { label: "Repository", value: run.repository, mono: true },
    { label: "Pull request", value: `Number ${run.pullRequest}` },
    { label: "Title", value: run.title },
    {
      label: "Pull request state",
      value: (
        <span className="flex flex-wrap items-center gap-1.5">
          <StatusChip value={run.pullRequestState} />
          {run.draft ? <Badge tone="neutral">draft</Badge> : null}
          {run.fromFork ? <Badge tone="warn">from a fork</Badge> : null}
        </span>
      ),
    },
    { label: "Head branch", value: run.headRef, mono: true },
    { label: "Base branch", value: run.baseRef, mono: true },
    { label: "Head repository", value: run.headRepository, mono: true },
    { label: "Head commit", value: run.headSha, mono: true },
    { label: "Attempt", value: run.attempt },
    { label: "Outcome", value: <Standing standing={run.standing} state={run.state} /> },
    { label: "Detail", value: run.detail },
    {
      label: "Twin",
      value: run.envId ? <span className="font-mono text-[12px]">{run.envId}</span> : null,
    },
    { label: "Reported by", value: run.reportedBy },
    {
      label: "GitHub check run",
      value: run.checkRunId ? (
        <span className="font-mono text-[12px]">{run.checkRunId}</span>
      ) : (
        // Null is a state to serve, not a crash: the app is installed without
        // `checks: write`, so the comment lands and the check does not.
        <span className="text-muted">
          none, which means this installation does not hold the checks permission
        </span>
      ),
    },
    {
      label: "Workflow run",
      value: run.workflowRunId ? (
        <span className="font-mono text-[12px]">{run.workflowRunId}</span>
      ) : null,
    },
    { label: "Queued", value: <When value={run.queuedAt} /> },
    { label: "Started", value: run.startedAt ? <When value={run.startedAt} /> : null },
    { label: "Finished", value: run.finishedAt ? <When value={run.finishedAt} /> : null },
    { label: "Took", value: duration(run.durationMs) },
    { label: "Gives up at", value: <When value={run.deadlineAt} /> },
  ];

  return (
    <AdminPage
      title={`${run.repository} pull request ${run.pullRequest}`}
      lede={`Check on ${run.headSha.slice(0, 12)}, owned by ${run.orgSlug}.`}
    >
      <div className="space-y-6">
        {run.fromFork ? (
          <Card title="This head branch lives in another repository">
            <Facts
              facts={[
                { label: "Head repository", value: run.headRepository, mono: true },
                {
                  label: "Approved commit",
                  value: run.approvedSha ? (
                    <span className="flex flex-wrap items-center gap-2">
                      <span className="font-mono text-[12px]">{run.approvedSha}</span>
                      {approvalStale ? (
                        // The approval is of a COMMIT and not of the pull
                        // request, so a push after it leaves an approval of
                        // code nobody looked at. This is the whole reason the
                        // column stores a sha.
                        <Badge tone="warn">not the head that ran</Badge>
                      ) : (
                        <Badge tone="pass">matches the head</Badge>
                      )}
                    </span>
                  ) : null,
                },
                { label: "Approved by", value: run.approvedBy },
                {
                  label: "Approved",
                  value: run.approvedAt ? <When value={run.approvedAt} /> : null,
                },
              ]}
            />
          </Card>
        ) : null}

        <Card title="This check">
          <Facts facts={facts} />
        </Card>

        <Card
          title="The report as it arrived"
          note="Already reduced to counts and verdicts by the engine. Never a body, a log or a screenshot."
        >
          <div className="px-4 py-4">
            {run.verdict === null || run.verdict === undefined ? (
              <p className="text-[13px] leading-6 text-muted">
                Nothing was reported for this generation. A check that says nothing before its
                deadline becomes unverified rather than staying queued forever, which is why the
                outcome above may read unverified with nothing here.
              </p>
            ) : (
              <Machine>{JSON.stringify(run.verdict, null, 2)}</Machine>
            )}
          </div>
        </Card>
      </div>
    </AdminPage>
  );
}
