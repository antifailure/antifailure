// One run: what the agents concluded, and what they left behind.
//
// The order on this page is the order somebody needs it in. The verdicts come
// first because they are the answer. The steps that reproduce a failure come
// with the verdict rather than in a separate place, because a failing workflow
// with the reproduction three clicks away is a failing workflow nobody
// reproduces. The artifacts come last: they are evidence, and evidence is what
// you reach for after you know what you are looking for.
//
// The five verdicts are not five shades of the same thing. Two of them are not
// statements about the change at all, and the page says so out loud rather
// than encoding it in a colour somebody has to learn.

import type { Metadata } from "next";
import { Suspense } from "react";
import { Chrome } from "../../../components/Chrome";
import {
  Duration,
  Empty,
  Failure,
  Mono,
  Page,
  PageHead,
  Panel,
  TableFrame,
  TableSkeleton,
  Td,
  Th,
  Tr,
  VerdictChip,
  VERDICTS,
} from "../../../components/ui";
import { ApiError, query, type Artifact, type Verdict } from "../../../lib/api";
import { asJson } from "../../../lib/json";
import { requireActor } from "../../../lib/guard";
import { isNavigation } from "../../../lib/navigation";
import { NoOrganization, Unreachable } from "../../../components/Screens";

export const dynamic = "force-dynamic";

export async function generateMetadata({
  params,
}: {
  params: Promise<{ runId: string }>;
}): Promise<Metadata> {
  const { runId } = await params;
  return { title: `Run ${runId.slice(0, 8)}` };
}

export default async function RunPage({ params }: { params: Promise<{ runId: string }> }) {
  const { runId } = await params;

  let actor: Awaited<ReturnType<typeof requireActor>>;
  try {
    actor = await requireActor(`/runs/${runId}`);
  } catch (err) {
    // A redirect is not a failure. Next.js signals one by throwing, and
    // catching it here is what turned "go and sign in" into a full page
    // error saying the control plane did not answer.
    if (isNavigation(err)) throw err;
    return <Unreachable detail={err instanceof ApiError ? err.message : String(err)} />;
  }
  if (actor === "no-organization") return <NoOrganization />;

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
          title={`Run ${runId.slice(0, 8)}`}
          lede="What the agents did, what they concluded, and the evidence they left. A blocked or unverified result is not a failure and is not counted as one."
        />

        <Suspense
          fallback={
            <Panel title="Verdicts">
              <TableSkeleton rows={4} columns={4} />
            </Panel>
          }
        >
          <Verdicts runId={runId} />
        </Suspense>

        <div className="mt-6">
          <Suspense
            fallback={
              <Panel title="Evidence">
                <TableSkeleton rows={3} columns={4} />
              </Panel>
            }
          >
            <Artifacts runId={runId} />
          </Suspense>
        </div>
      </Page>
    </Chrome>
  );
}

async function Verdicts({ runId }: { runId: string }) {
  let verdicts: Verdict[];
  try {
    verdicts = await query<Verdict[]>("runs.verdicts", { runId });
  } catch (err) {
    if (isNavigation(err)) throw err;
    if (err instanceof ApiError && err.code === "BAD_REQUEST") {
      return (
        <Failure
          title="That is not a run identifier"
          detail="A run is named by a UUID. This address carries something else, so there is nothing to look up."
        />
      );
    }
    return (
      <Failure
        title="The verdicts could not be read"
        detail={err instanceof ApiError ? err.message : String(err)}
      />
    );
  }

  if (verdicts.length === 0) {
    return (
      <Panel title="Verdicts">
        <Empty
          title="This run recorded no verdicts"
          says="Either it is still going, or it ended before any workflow finished. A run that failed to bring its environment up ends here, and the failure belongs to the environment rather than to any workflow."
        />
      </Panel>
    );
  }

  const counted = verdicts.reduce<Record<string, number>>((acc, v) => {
    acc[v.value] = (acc[v.value] ?? 0) + 1;
    return acc;
  }, {});

  return (
    <Panel
      title="Verdicts"
      note={Object.entries(counted)
        .map(([value, n]) => `${n} ${value}`)
        .join(", ")}
    >
      <TableFrame>
        <thead>
          <tr>
            <Th>Workflow</Th>
            <Th>Verdict</Th>
            <Th numeric>Steps</Th>
            <Th numeric>Took</Th>
          </tr>
        </thead>
        <tbody>
          {verdicts.map((v) => (
            <Tr key={v.workflow}>
              <Td className="align-top">
                <p className="font-medium tracking-snug text-ink">{v.workflow}</p>
                {v.persona ? (
                  <p className="mt-0.5 text-[12.5px] text-faint">as {v.persona}</p>
                ) : null}
                {v.summary ? (
                  <p className="mt-1.5 max-w-[70ch] text-[12.5px] leading-[1.55] text-muted">
                    {v.summary}
                  </p>
                ) : null}
                <Reproduction steps={v.reproduction} />
              </Td>
              <Td className="align-top">
                <VerdictChip value={v.value} />
                <p className="mt-1.5 max-w-[34ch] text-[12px] leading-[1.5] text-faint">
                  {VERDICTS[v.value]?.means ?? ""}
                </p>
              </Td>
              <Td numeric className="align-top text-muted">
                {v.steps}
              </Td>
              <Td numeric className="align-top text-muted">
                <Duration ms={v.duration_ms} />
              </Td>
            </Tr>
          ))}
        </tbody>
      </TableFrame>
    </Panel>
  );
}

/**
 * The steps a failure can be reproduced from.
 *
 * `reproduction` is jsonb, which means it is whatever was written into it. An
 * array of strings is what the runner sends; anything else is shown as what it
 * is rather than crashed on, because a page that throws on an unexpected shape
 * loses the whole run rather than one field of it.
 */
function Reproduction({ steps: raw }: { steps: unknown }) {
  const steps = asJson(raw);
  const lines = Array.isArray(steps)
    ? steps.filter((s): s is string => typeof s === "string")
    : [];

  if (lines.length === 0) {
    if (steps === null || steps === undefined) return null;
    if (Array.isArray(steps) && steps.length === 0) return null;
    return (
      <details className="mt-2">
        <summary className="cursor-pointer text-[12.5px] text-muted hover:text-ink">
          Reproduction, in the shape it was recorded
        </summary>
        <pre className="mt-1.5 max-w-full overflow-x-auto rounded-lg bg-sunken px-3 py-2 font-mono text-[12px] leading-[1.6] text-muted">
          {JSON.stringify(steps, null, 2)}
        </pre>
      </details>
    );
  }

  return (
    <details className="mt-2">
      <summary className="cursor-pointer text-[12.5px] text-muted hover:text-ink">
        {lines.length} steps to reproduce this
      </summary>
      <ol className="mt-1.5 flex list-none flex-col gap-1">
        {lines.map((line, i) => (
          <li key={i} className="flex gap-2 text-[12.5px] leading-[1.55] text-muted">
            <span className="numeric shrink-0 text-faint">{String(i + 1).padStart(2, "0")}</span>
            <span className="min-w-0 break-words">{line}</span>
          </li>
        ))}
      </ol>
    </details>
  );
}

async function Artifacts({ runId }: { runId: string }) {
  let artifacts: Artifact[];
  try {
    artifacts = await query<Artifact[]>("runs.artifacts", { runId });
  } catch (err) {
    if (isNavigation(err)) throw err;
    if (err instanceof ApiError && err.code === "BAD_REQUEST") return null;
    return (
      <Failure
        title="The evidence could not be listed"
        detail={err instanceof ApiError ? err.message : String(err)}
      />
    );
  }

  if (artifacts.length === 0) {
    return (
      <Panel title="Evidence">
        <Empty
          title="Nothing was kept"
          says="A run records a video, a trace, and a screenshot per workflow. None is listed here, which means either the run has not finished writing them or its retention window has passed."
        />
      </Panel>
    );
  }

  return (
    <Panel title="Evidence" note={`${artifacts.length} artifacts`}>
      <TableFrame>
        <thead>
          <tr>
            <Th>Kind</Th>
            <Th numeric>Step</Th>
            <Th>Type</Th>
            <Th numeric>Size</Th>
            <Th>Digest</Th>
          </tr>
        </thead>
        <tbody>
          {artifacts.map((a) => (
            <Tr key={a.id}>
              <Td>
                <span className="font-medium tracking-snug text-ink">{a.kind}</span>
                {!a.retained ? (
                  <span className="mt-0.5 block text-[12px] text-faint">
                    listed, no longer stored
                  </span>
                ) : null}
              </Td>
              <Td numeric className="text-muted">
                {a.step ?? <span className="text-faint">&mdash;</span>}
              </Td>
              <Td className="text-[12.5px] text-muted">
                {a.content_type ?? <span className="text-faint">&mdash;</span>}
              </Td>
              <Td numeric className="text-muted">
                {formatBytes(a.size_bytes)}
              </Td>
              <Td>
                {a.sha256 ? (
                  <Mono className="text-faint" >{a.sha256.slice(0, 12)}</Mono>
                ) : (
                  <span className="text-faint">&mdash;</span>
                )}
              </Td>
            </Tr>
          ))}
        </tbody>
      </TableFrame>
    </Panel>
  );
}

function formatBytes(bytes: number | null): string {
  if (bytes === null || bytes === undefined) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
