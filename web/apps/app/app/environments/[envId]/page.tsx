// One environment, and everything that has run against it.
//
// This is the page somebody opens from a pull request comment, so it has to
// answer "what is this and did it work" above the fold and keep the detail
// below. The runs are the point of the page: an environment with no runs is a
// preview nobody exercised, and saying that plainly is more useful than an
// empty table.

import type { Metadata } from "next";
import { Suspense } from "react";
import { notFound } from "next/navigation";
import { Chrome } from "../../../components/Chrome";
import {
  Empty,
  Failure,
  LinkButton,
  Mono,
  Page,
  PageHead,
  Panel,
  StateChip,
  TableFrame,
  TableSkeleton,
  Td,
  Th,
  Tr,
  When,
  Chip,
} from "../../../components/ui";
import { ApiError, query, type Environment, type Run } from "../../../lib/api";
import { requireActor } from "../../../lib/guard";
import { isNavigation } from "../../../lib/navigation";
import { NoOrganization, Unreachable } from "../../../components/Screens";

export const dynamic = "force-dynamic";

export async function generateMetadata({
  params,
}: {
  params: Promise<{ envId: string }>;
}): Promise<Metadata> {
  const { envId } = await params;
  return { title: envId };
}

const RUN_STATES: Record<string, "pass" | "fail" | "flaky" | "neutral" | "quiet"> = {
  complete: "pass",
  running: "flaky",
  queued: "neutral",
  failed: "fail",
  cancelled: "quiet",
};

export default async function EnvironmentPage({
  params,
}: {
  params: Promise<{ envId: string }>;
}) {
  const { envId: raw } = await params;
  const envId = decodeURIComponent(raw);

  let actor: Awaited<ReturnType<typeof requireActor>>;
  try {
    actor = await requireActor(`/environments/${encodeURIComponent(envId)}`);
  } catch (err) {
    // A redirect is not a failure. Next.js signals one by throwing, and
    // catching it here is what turned "go and sign in" into a full page
    // error saying the control plane did not answer.
    if (isNavigation(err)) throw err;
    return <Unreachable detail={err instanceof ApiError ? err.message : String(err)} />;
  }
  if (actor === "no-organization") return <NoOrganization />;

  let env: Environment;
  try {
    env = await query<Environment>("environments.get", { envId });
  } catch (err) {
    if (isNavigation(err)) throw err;
    // NOT_FOUND is what the API answers both for an environment that does not
    // exist and for one belonging to somebody else, deliberately, so this page
    // must not distinguish them either.
    if (err instanceof ApiError && err.code === "NOT_FOUND") notFound();
    return <Unreachable detail={err instanceof ApiError ? err.message : String(err)} />;
  }

  return (
    <Chrome current="/" who={actor.label} org={actor.orgSlug} role={actor.role}>
      <Page>
        <div className="mb-2">
          <a
            href="/"
            className="text-[12.5px] text-muted underline-offset-4 hover:text-ink hover:underline"
          >
            &larr; Environments
          </a>
        </div>

        <PageHead
          title={env.env_id}
          lede={`${env.repository} on ${env.branch}${env.pull_request ? `, pull request #${env.pull_request}` : ""}.`}
          actions={
            env.preview_url ? (
              <LinkButton href={env.preview_url} rel="noreferrer noopener" variant="primary">
                Open the preview
              </LinkButton>
            ) : undefined
          }
        />

        <Panel title="What this is">
          <dl className="grid grid-cols-1 divide-y divide-hair sm:grid-cols-2 sm:divide-y-0 lg:grid-cols-3">
            <Detail label="State">
              <StateChip value={env.state} />
            </Detail>
            <Detail label="Runtime">
              {env.runtime ? <Mono>{env.runtime}</Mono> : <Faint>not recorded</Faint>}
            </Detail>
            <Detail label="Golden">
              {env.golden_version ? (
                <Mono>{env.golden_version}</Mono>
              ) : (
                <Faint>branched from no published golden</Faint>
              )}
            </Detail>
            <Detail label="Preview address">
              {env.preview_url ? (
                <a
                  href={env.preview_url}
                  rel="noreferrer noopener"
                  className="break-all text-[13px] text-ink underline underline-offset-4 hover:no-underline"
                >
                  {env.preview_url}
                </a>
              ) : (
                <Faint>none yet</Faint>
              )}
            </Detail>
            <Detail label="Created">
              <When at={env.created_at} className="text-[13px]" />
            </Detail>
            <Detail label="Expires">
              {env.expires_at ? (
                <When at={env.expires_at} className="text-[13px]" />
              ) : (
                <Faint>no lifetime set</Faint>
              )}
            </Detail>
          </dl>
        </Panel>

        <div className="mt-6">
          <Suspense
            fallback={
              <Panel title="Runs">
                <TableSkeleton rows={4} columns={4} />
              </Panel>
            }
          >
            <Runs envId={env.env_id} />
          </Suspense>
        </div>
      </Page>
    </Chrome>
  );
}

function Detail({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="border-hair px-4 py-3.5 sm:border-b sm:px-5 lg:[&:nth-child(n+4)]:border-b-0">
      <dt className="text-[11.5px] uppercase tracking-[0.06em] text-faint">{label}</dt>
      <dd className="mt-1.5 text-[13px] text-ink">{children}</dd>
    </div>
  );
}

function Faint({ children }: { children: React.ReactNode }) {
  return <span className="text-[13px] text-faint">{children}</span>;
}

async function Runs({ envId }: { envId: string }) {
  let runs: Run[];
  try {
    runs = await query<Run[]>("runs.list", { envId, limit: 25 });
  } catch (err) {
    if (isNavigation(err)) throw err;
    return (
      <Failure
        title="The runs could not be read"
        detail={err instanceof ApiError ? err.message : String(err)}
      />
    );
  }

  if (runs.length === 0) {
    return (
      <Panel title="Runs">
        <Empty
          title="Nothing has run here"
          says="The environment exists and no agent, load run, or database review has been recorded against it. That is what an environment that came up and was never exercised looks like."
        />
      </Panel>
    );
  }

  return (
    <Panel title="Runs" note={`${runs.length} most recent`}>
      <TableFrame>
        <thead>
          <tr>
            <Th>Run</Th>
            <Th>Kind</Th>
            <Th>State</Th>
            <Th>Started</Th>
            <Th>Finished</Th>
          </tr>
        </thead>
        <tbody>
          {runs.map((run) => (
            <Tr key={run.id}>
              <Td>
                <a
                  href={`/runs/${run.id}`}
                  className="font-medium tracking-snug text-ink underline-offset-4 hover:underline"
                >
                  <Mono>{run.id.slice(0, 8)}</Mono>
                </a>
              </Td>
              <Td className="text-muted">{run.kind}</Td>
              <Td>
                <Chip tone={RUN_STATES[run.state] ?? "neutral"}>{run.state}</Chip>
              </Td>
              <Td className="text-[12.5px] text-muted">
                <When at={run.started_at} />
              </Td>
              <Td className="text-[12.5px] text-muted">
                <When at={run.finished_at} />
              </Td>
            </Tr>
          ))}
        </tbody>
      </TableFrame>
    </Panel>
  );
}
