"use client";

import { Suspense } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { bytes, when } from "@/lib/format";
import { query, useApi } from "@/lib/api";
import {
  Badge,
  Button,
  Card,
  CellLink,
  Empty,
  Loaded,
  Page,
  Row,
  Table,
  TableSkeleton,
  TableWrap,
  Td,
  Th,
  When,
  toneFor,
} from "@/components/ui";

interface Run {
  id: string;
  kind: string;
  state: string;
  started_at: string | null;
  finished_at: string | null;
  created_at: string;
  env_id: string;
  branch: string;
  pull_request: number | null;
  repository: string;
  verdicts?: string;
  failing?: string;
}

interface Verdict {
  workflow: string;
  persona: string | null;
  value: string;
  summary: string | null;
  steps: number | null;
  duration_ms: number | null;
  reproduction: unknown;
}

interface Artifact {
  id: string;
  kind: string;
  step: number | null;
  content_type: string | null;
  size_bytes: string | number | null;
  sha256: string | null;
  retained: boolean;
}

function seconds(ms: number | null): string {
  if (ms === null || !Number.isFinite(ms)) return "--";
  return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`;
}

function Detail({ runId, onClose }: { runId: string; onClose: () => void }) {
  const run = useApi<Run>(() => query("runs.get", { runId }), [runId]);
  const verdicts = useApi<Verdict[]>(() => query("runs.verdicts", { runId }), [runId]);
  const artifacts = useApi<Artifact[]>(() => query("runs.artifacts", { runId }), [runId]);

  return (
    <div className="space-y-6">
      <Card
        title="Run"
        note={runId}
        actions={<Button onClick={onClose}>Close</Button>}
      >
        <Loaded state={run} skeleton={<TableSkeleton rows={3} cols={2} />}>
          {(r) => (
            <dl className="grid gap-x-8 gap-y-4 px-4 py-4 sm:grid-cols-2">
              {[
                ["Repository", r.repository],
                ["Environment", r.env_id],
                ["Branch", r.pull_request ? `${r.branch} #${r.pull_request}` : r.branch],
                ["Kind", r.kind],
                ["Started", r.started_at ? when(r.started_at) : "not started"],
                ["Finished", r.finished_at ? when(r.finished_at) : "not finished"],
              ].map(([k, v]) => (
                <div key={k}>
                  <dt className="text-[11px] uppercase tracking-[0.08em] text-dim">{k}</dt>
                  <dd className="mt-1 text-[13px] text-ink">{v}</dd>
                </div>
              ))}
              <div>
                <dt className="text-[11px] uppercase tracking-[0.08em] text-dim">State</dt>
                <dd className="mt-1">
                  <Badge tone={toneFor(r.state)}>{r.state}</Badge>
                </dd>
              </div>
            </dl>
          )}
        </Loaded>
      </Card>

      <Card title="Verdicts" note="One per workflow the runner exercised.">
        <Loaded state={verdicts} skeleton={<TableSkeleton rows={3} cols={4} />}>
          {(rows) =>
            rows.length === 0 ? (
              <Empty title="No verdicts">
                The runner records a verdict per workflow when it finishes. This
                run has not produced one, which usually means it is still going
                or it failed before the first workflow.
              </Empty>
            ) : (
              <TableWrap>
                <Table>
                  <thead>
                    <tr>
                      <Th>Workflow</Th>
                      <Th>Persona</Th>
                      <Th>Verdict</Th>
                      <Th>Summary</Th>
                      <Th numeric>Steps</Th>
                      <Th numeric>Duration</Th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((v, i) => (
                      <Row key={`${v.workflow}-${i}`}>
                        <Td mono>{v.workflow}</Td>
                        <Td label="Persona">{v.persona ?? "--"}</Td>
                        <Td label="Verdict">
                          <Badge tone={toneFor(v.value)}>{v.value}</Badge>
                        </Td>
                        <Td label="Summary" className="max-w-[36ch]">{v.summary ?? "--"}</Td>
                        <Td label="Steps" numeric>{v.steps ?? "--"}</Td>
                        <Td label="Duration" numeric>{seconds(v.duration_ms)}</Td>
                      </Row>
                    ))}
                  </tbody>
                </Table>
              </TableWrap>
            )
          }
        </Loaded>
      </Card>

      <Card
        title="Artifacts"
        note="What the run kept. Retention is a policy decision, so an artifact that was dropped says so rather than vanishing."
      >
        <Loaded state={artifacts} skeleton={<TableSkeleton rows={3} cols={4} />}>
          {(rows) =>
            rows.length === 0 ? (
              <Empty title="No artifacts">
                Nothing was stored for this run.
              </Empty>
            ) : (
              <TableWrap>
                <Table>
                  <thead>
                    <tr>
                      <Th>Kind</Th>
                      <Th numeric>Step</Th>
                      <Th>Type</Th>
                      <Th numeric>Size</Th>
                      <Th>Digest</Th>
                      <Th>Retained</Th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((a) => (
                      <Row key={a.id}>
                        <Td>{a.kind}</Td>
                        <Td label="Step" numeric>{a.step ?? "--"}</Td>
                        <Td label="Type" mono>{a.content_type ?? "--"}</Td>
                        <Td label="Size" numeric>{bytes(a.size_bytes)}</Td>
                        <Td label="Digest" mono className="max-w-[18ch] truncate">
                          {a.sha256 ? a.sha256.slice(0, 12) : "--"}
                        </Td>
                        <Td label="Retained">
                          <Badge tone={a.retained ? "pass" : "neutral"}>
                            {a.retained ? "kept" : "dropped"}
                          </Badge>
                        </Td>
                      </Row>
                    ))}
                  </tbody>
                </Table>
              </TableWrap>
            )
          }
        </Loaded>
      </Card>
    </div>
  );
}

function Runs() {
  const params = useSearchParams();
  const router = useRouter();
  const selected = params.get("run");
  const state = useApi<{ runs: Run[]; nextCursor: string | null }>(
    () => query("runs.recent", { limit: 50 }),
    [],
  );

  if (selected) {
    return (
      <Page title="Run" lede="Verdicts and artifacts, as the runner reported them.">
        <Detail runId={selected} onClose={() => router.push("/runs")} />
      </Page>
    );
  }

  return (
    <Page
      title="Runs"
      lede="Every run across every environment, newest first. A run with failing verdicts is one that found something."
    >
      <Card title="Recent runs">
        <Loaded state={state} skeleton={<TableSkeleton rows={6} cols={5} />}>
          {(data) =>
            data.runs.length === 0 ? (
              <Empty title="No runs yet">
                A run appears the first time the engine exercises an
                environment. Nothing here means nothing has run, not that
                something is broken.
              </Empty>
            ) : (
              <TableWrap>
                <Table>
                  <thead>
                    <tr>
                      <Th>Kind</Th>
                      <Th>Repository</Th>
                      <Th>Environment</Th>
                      <Th>State</Th>
                      <Th numeric>Verdicts</Th>
                      <Th>Started</Th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.runs.map((r) => {
                      const failing = Number(r.failing ?? 0);
                      const total = Number(r.verdicts ?? 0);
                      return (
                        <Row key={r.id} onClick={() => router.push(`/runs?run=${r.id}`)}>
                          <Td>
                            <CellLink href={`/runs?run=${r.id}`}>{r.kind}</CellLink>
                          </Td>
                          <Td label="Repository">{r.repository}</Td>
                          <Td label="Environment" mono>{r.env_id}</Td>
                          <Td label="State">
                            <Badge tone={toneFor(r.state)}>{r.state}</Badge>
                          </Td>
                          <Td label="Verdicts" numeric>
                            {total === 0 ? (
                              "--"
                            ) : failing > 0 ? (
                              <span className="text-fail">
                                {failing} of {total} failing
                              </span>
                            ) : (
                              <span className="text-pass">{total} passing</span>
                            )}
                          </Td>
                          <Td label="Started">
                            <When value={r.started_at ?? r.created_at} />
                          </Td>
                        </Row>
                      );
                    })}
                  </tbody>
                </Table>
              </TableWrap>
            )
          }
        </Loaded>
      </Card>
    </Page>
  );
}

export default function RunsPage() {
  return (
    <Suspense
      fallback={
        <Page title="Runs">
          <Card title="Recent runs">
            <TableSkeleton rows={6} cols={5} />
          </Card>
        </Page>
      }
    >
      <Runs />
    </Suspense>
  );
}
