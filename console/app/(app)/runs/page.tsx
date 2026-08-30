"use client";

import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { ago, bytes, when } from "@/lib/format";
import { mutate, query, useApi } from "@/lib/api";
import { may } from "@/lib/roles";
import { useSessionContext } from "@/components/session";
import {
  Badge,
  Button,
  Card,
  Empty,
  Field,
  Loaded,
  Page,
  Row,
  Table,
  TableSkeleton,
  TableWrap,
  Td,
  Th,
  inputClass,
  toneFor,
} from "@/components/ui";

interface Environment {
  env_id: string;
  branch: string;
  state: string;
  repository: string;
}

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
                        <Td>{v.persona ?? "--"}</Td>
                        <Td>
                          <Badge tone={toneFor(v.value)}>{v.value}</Badge>
                        </Td>
                        <Td className="max-w-[36ch]">{v.summary ?? "--"}</Td>
                        <Td numeric>{v.steps ?? "--"}</Td>
                        <Td numeric>{seconds(v.duration_ms)}</Td>
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
                        <Td numeric>{a.step ?? "--"}</Td>
                        <Td mono>{a.content_type ?? "--"}</Td>
                        <Td numeric>{bytes(a.size_bytes)}</Td>
                        <Td mono className="max-w-[18ch] truncate">
                          {a.sha256 ? a.sha256.slice(0, 12) : "--"}
                        </Td>
                        <Td>
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


/**
 * Starting a run against an environment that is already up.
 *
 * Deliberately not `af ci`, which brings an environment up, runs everything and
 * tears it down again. Somebody pressing this has an environment and wants one
 * more thing run against it, so the dispatch carries the command and the
 * console says which environment it is going to.
 *
 * The environments offered are the ones that are not torn down. Dispatching at
 * a torn down environment is refused by the control plane before GitHub is
 * asked, and offering it here would be a control that exists to be refused.
 */
function Start({ onStarted }: { onStarted: () => void }) {
  const session = useSessionContext();
  const environments = useApi<{ environments: Environment[] }>(
    () => query("environments.list", { limit: 100 }),
    [],
  );
  const csrf = session.data?.csrfToken ?? "";
  const [envId, setEnvId] = useState("");
  const [kind, setKind] = useState<"agents" | "load">("agents");
  const [workflows, setWorkflows] = useState("");
  const [seconds, setSeconds] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [asked, setAsked] = useState<string | null>(null);

  return (
    <Card
      title="Start a run"
      note="Dispatches your workflow in your repository, on the branch the environment is on."
    >
      <Loaded state={environments} skeleton={<TableSkeleton rows={1} cols={3} />}>
        {(data) => {
          const live = data.environments.filter((e) => e.state !== "torn_down");
          if (live.length === 0) {
            return (
              <Empty title="No environment to run against">
                A run needs an environment that is up. Ask for one on the
                Environments page, and this fills when the engine reports it.
              </Empty>
            );
          }
          const chosen = live.find((e) => e.env_id === envId)?.env_id ?? live[0]!.env_id;
          return (
            <form
              className="px-4 py-4"
              onSubmit={async (e) => {
                e.preventDefault();
                setBusy(true);
                setError(null);
                setAsked(null);
                try {
                  if (kind === "agents") {
                    const only = workflows
                      .split(",")
                      .map((w) => w.trim())
                      .filter(Boolean);
                    await mutate(
                      "agents.run",
                      { envId: chosen, ...(only.length ? { workflows: only } : {}) },
                      csrf,
                    );
                  } else {
                    const n = Number(seconds);
                    await mutate(
                      "load.run",
                      {
                        envId: chosen,
                        ...(Number.isFinite(n) && n > 0 ? { seconds: Math.round(n) } : {}),
                      },
                      csrf,
                    );
                  }
                  setAsked(`Asked GitHub to run ${kind} against ${chosen}.`);
                  onStarted();
                } catch (err) {
                  setError(err instanceof Error ? err.message : "That did not work.");
                } finally {
                  setBusy(false);
                }
              }}
            >
              <div className="grid gap-3 sm:grid-cols-[2fr_1fr_2fr_auto] sm:items-end">
                <Field label="Environment">
                  <select
                    className={inputClass}
                    value={chosen}
                    onChange={(e) => setEnvId(e.target.value)}
                  >
                    {live.map((e) => (
                      <option key={e.env_id} value={e.env_id}>
                        {e.env_id} ({e.branch})
                      </option>
                    ))}
                  </select>
                </Field>
                <Field label="Run">
                  <select
                    className={inputClass}
                    value={kind}
                    onChange={(e) => setKind(e.target.value === "load" ? "load" : "agents")}
                  >
                    <option value="agents">agents</option>
                    <option value="load">load</option>
                  </select>
                </Field>
                {kind === "agents" ? (
                  <Field label="Workflows">
                    <input
                      className={inputClass}
                      value={workflows}
                      onChange={(e) => setWorkflows(e.target.value)}
                      placeholder="sign-up, checkout"
                    />
                  </Field>
                ) : (
                  <Field label="Seconds">
                    <input
                      className={inputClass}
                      inputMode="numeric"
                      value={seconds}
                      onChange={(e) => setSeconds(e.target.value)}
                      placeholder="60"
                    />
                  </Field>
                )}
                <Button type="submit" variant="primary" busy={busy}>
                  {busy ? "Asking" : "Start"}
                </Button>
              </div>
              {/* Under the row rather than in a Field: a hint inside one grid
                  cell makes it taller, and items-end then lifts that input
                  clear of the ones beside it. */}
              {error ? (
                <p role="alert" className="mt-2.5 text-[12px] leading-5 text-fail">
                  {error}
                </p>
              ) : (
                <p role={asked ? "status" : undefined} className="mt-2.5 text-[12px] leading-5 text-dim">
                  {asked ??
                    (kind === "agents"
                      ? "Workflows are comma separated. Empty runs all of them."
                      : "Empty leaves the command's own default, which is a minute.")}
                </p>
              )}
            </form>
          );
        }}
      </Loaded>
    </Card>
  );
}

function Runs() {
  const session = useSessionContext();
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
      {may(session.data?.role, "agents.run") ? (
        <div className="mb-6">
          <Start onStarted={state.reload} />
        </div>
      ) : null}

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
                          <Td>{r.kind}</Td>
                          <Td>{r.repository}</Td>
                          <Td mono>{r.env_id}</Td>
                          <Td>
                            <Badge tone={toneFor(r.state)}>{r.state}</Badge>
                          </Td>
                          <Td numeric>
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
                          <Td>
                            <span title={when(r.started_at ?? r.created_at)}>
                              {ago(r.started_at ?? r.created_at)}
                            </span>
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
    <Suspense fallback={<Page title="Runs"><TableSkeleton /></Page>}>
      <Runs />
    </Suspense>
  );
}
