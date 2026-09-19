"use client";

import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { bytes, when } from "@/lib/format";
import { mutate, query, useApi, usePages } from "@/lib/api";
import { may } from "@/lib/roles";
import { useSessionContext } from "@/components/session";
import { More } from "@/components/pagination";
import {
  Badge,
  Button,
  Card,
  CardSkeleton,
  CellLink,
  Empty,
  Field,
  LinkButton,
  Loaded,
  Machine,
  Page,
  Row,
  Table,
  TableSkeleton,
  TableWrap,
  Td,
  Th,
  When,
  inputClass,
  toneFor,
  type Tone,
} from "@/components/ui";
import { POLL_MS, useInterval } from "@/components/load/polling";
import { ReportMarkdown } from "@/lib/reportmarkdown";
import {
  agentsRunArgs,
  loadRunArgs,
  nothingWasVerifiedNotice,
  noVerdictsReason,
  recentRunSummary,
  reproductionText,
  runIsInFlight,
} from "@/lib/runshapes";

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
  passing?: string;
  failing?: string;
  proved?: string;
}

// The runs list colours its verdict summary with the same tokens as the rest
// of the console. `neutral` never comes back from `recentRunSummary`, but the
// map is total over `Tone` so it cannot silently miss a case.
const verdictTone: Record<Tone, string> = {
  pass: "text-pass",
  warn: "text-warn",
  fail: "text-fail",
  neutral: "text-dim",
};

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

/**
 * The banner over a verdict table that judged nothing.
 *
 * Every column in that table can be full and the run can still have proved
 * nothing: five rows reading "unverified" is a run whose personas were never
 * created, and with only pass and fail in a reader's head it draws as a run
 * with no failures. That is the exit code zero over nothing defect this product
 * has already shipped once, and `components/load/results.tsx` guards the load
 * view against exactly it. The runs page, which is the one a customer is shown,
 * did not.
 *
 * role="alert" and not "status": this is the screen contradicting the
 * impression the table beside it gives, which is the case the loud one is for.
 */
function NothingVerified({ values }: { values: readonly string[] }) {
  const notice = nothingWasVerifiedNotice(values);
  if (notice === null) return null;
  return (
    <p
      role="alert"
      className="border-b border-rule bg-[rgba(138,90,0,0.07)] px-4 py-2.5 text-[12.5px] leading-6 text-warn"
    >
      {notice}
    </p>
  );
}

/**
 * How to reproduce what the runner found, printed only if the runner recorded
 * it. The operator console has rendered this in a labelled column all along
 * (app/admin/product/runs/detail/page.tsx) while the page the customer is shown
 * fetched the column and threw it away.
 */
function Reproduction({ value }: { value: unknown }) {
  const text = reproductionText(value);
  if (text === null) return <span className="text-dim">none recorded</span>;
  return <Machine>{text}</Machine>;
}

function Detail({ runId, onClose }: { runId: string; onClose: () => void }) {
  const run = useApi<Run>(() => query("runs.get", { runId }), [runId]);
  const verdicts = useApi<Verdict[]>(() => query("runs.verdicts", { runId }), [runId]);
  const artifacts = useApi<Artifact[]>(() => query("runs.artifacts", { runId }), [runId]);

  // Ask again while the run can still change, and stop the moment it cannot.
  //
  // Nothing on this screen updated at all before this. The only reload in the
  // file hung off the Start card, which is not rendered without `agents.run`,
  // so a member watching a run they had been sent a link to had no way to see
  // it finish short of reloading the browser. The run this product exists to
  // show somebody was the one run in the console that could not be watched.
  //
  // All three reload together because they are one screen and the verdicts are
  // the reason anybody opened it. `useApi` keeps the rows it has across a
  // reload, so a tick does not blank the table it is refreshing. The condition
  // reads the run's own state rather than a timer, so a finished run makes
  // exactly zero further requests on a tab left open.
  const inFlight = runIsInFlight(run.data?.state);
  useInterval(inFlight, POLL_MS, () => {
    run.reload();
    verdicts.reload();
    artifacts.reload();
  });

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
              <Empty title="No verdicts">{noVerdictsReason(run.data?.state)}</Empty>
            ) : (
              <>
              <NothingVerified values={rows.map((v) => v.value)} />
              <TableWrap>
                <Table className="sm:min-w-[860px]">
                  <thead>
                    <tr>
                      <Th>Workflow</Th>
                      <Th>Persona</Th>
                      <Th>Verdict</Th>
                      <Th>Summary</Th>
                      <Th numeric>Steps</Th>
                      <Th numeric>Duration</Th>
                      <Th>Reproduction</Th>
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
                        <Td label="Reproduction" className="max-w-[34ch]">
                          <Reproduction value={v.reproduction} />
                        </Td>
                      </Row>
                    ))}
                  </tbody>
                </Table>
              </TableWrap>
              </>
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

type ReportCounts = {
  passed: number;
  failed: number;
  flaky: number;
  blocked: number;
  unverified: number;
};
type PrReport = {
  headSha: string;
  state: string;
  finishedAt: string | null;
  envId: string | null;
  counts: ReportCounts | null;
  environment: string | null;
  url: string | null;
  duration: string | null;
  markdown: string | null;
};

/**
 * The whole report a pull request check reported, rendered.
 *
 * A GitHub check links here as /runs?pr=<n>&commit=<sha>. Everything the run
 * gathered past the counts, the findings and their fixes, the migration
 * statements and locks, the invariants, the access probe, reaches this control
 * plane through the pull request callback rather than the events stream, so it
 * is not in a run row. It lands whole in the generation, and runs.report is the
 * only reader of it. This shows a scannable strip of the counts and then the
 * engine's own report in full, so nothing it measured is dropped on the way to
 * the person who has to act on it. It needs no run row to exist, which is the
 * whole point: the demo repositories report here without ever writing one.
 */
function PullRequestReport({ pr, commit }: { pr: number; commit: string | null }) {
  const report = useApi<PrReport | null>(
    () => query("runs.report", { pr, ...(commit ? { commit } : {}) }),
    [pr, commit],
  );
  const pending = report.data?.state === "queued" || report.data?.state === "running";
  useInterval(pending, POLL_MS, report.reload);

  return (
    <Loaded state={report} skeleton={<CardSkeleton count={2} />}>
      {(r) =>
        r === null ? (
          <Card title="Report">
            <Empty title="No report yet">
              No check on this control plane is waiting on pull request #{pr}. It appears here the
              moment a run reports one.
            </Empty>
          </Card>
        ) : (
          <div className="space-y-6">
            <Card title="Report" note={`Commit ${r.headSha.slice(0, 7)}`}>
              <div className="space-y-4 px-4 py-4">
                {r.counts ? (
                  <div className="flex flex-wrap gap-2">
                    <Badge tone="pass">{r.counts.passed} passed</Badge>
                    <Badge tone={r.counts.failed > 0 ? "fail" : "neutral"}>
                      {r.counts.failed} failed
                    </Badge>
                    <Badge tone={r.counts.flaky > 0 ? "warn" : "neutral"}>
                      {r.counts.flaky} flaky
                    </Badge>
                    <Badge tone="neutral">{r.counts.blocked} blocked</Badge>
                    <Badge tone="neutral">{r.counts.unverified} unverified</Badge>
                  </div>
                ) : null}
                <dl className="grid gap-x-8 gap-y-3 sm:grid-cols-3">
                  {[
                    ["Environment", r.environment ?? r.envId ?? "--"],
                    ["Duration", r.duration ?? "--"],
                    ["Finished", r.finishedAt ? when(r.finishedAt) : pending ? "running" : "--"],
                  ].map(([k, v]) => (
                    <div key={k}>
                      <dt className="text-[11px] uppercase tracking-[0.08em] text-dim">{k}</dt>
                      <dd className="mt-1 text-[13px] text-ink">{v}</dd>
                    </div>
                  ))}
                </dl>
              </div>
            </Card>
            {r.markdown ? (
              <Card title="Everything the run gathered" note="The engine's report in full, as it wrote it.">
                <ReportMarkdown source={r.markdown} />
              </Card>
            ) : (
              <Card title="Everything the run gathered">
                <Empty title="Nothing to show yet">
                  {pending
                    ? "The run is still going. The report fills in the moment it reports."
                    : "This run reported counts but no report body."}
                </Empty>
              </Card>
            )}
          </div>
        )
      }
    </Loaded>
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
  const [scale, setScale] = useState("");
  const [concurrency, setConcurrency] = useState("");
  const [seed, setSeed] = useState("");
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
              <Empty title="Create an environment first" action={<LinkButton href="/environments" variant="secondary">Set up an environment</LinkButton>}>
                A run executes workflows against an isolated copy of your app.
                Create that environment first, then return here to start a run
                from its configured GitHub workflow.
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
                  // The field-to-argument rule lives in runshapes so it can be
                  // tested: a blank knob is left out of the call rather than
                  // sent as a zero, so it reaches the command's own default.
                  if (kind === "agents") {
                    await mutate("agents.run", agentsRunArgs(chosen, workflows, seed), csrf);
                  } else {
                    await mutate(
                      "load.run",
                      loadRunArgs(chosen, seconds, scale, concurrency, seed),
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
              <div className="grid gap-3 sm:grid-cols-[2fr_1fr_auto] sm:items-end">
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
                <Button type="submit" variant="primary" busy={busy}>
                  {busy ? "Asking" : "Start"}
                </Button>
              </div>
              {/* The knobs for the chosen run. Every one is optional: left
                  blank it is not sent, so the command falls back to its own
                  default rather than to a zero. */}
              {kind === "agents" ? (
                <div className="mt-3 grid gap-3 sm:grid-cols-4">
                  <div className="sm:col-span-3">
                    <Field label="Workflows">
                      <input
                        className={inputClass}
                        value={workflows}
                        onChange={(e) => setWorkflows(e.target.value)}
                        placeholder="sign-up, checkout"
                      />
                    </Field>
                  </div>
                  <Field label="Seed">
                    <input
                      className={inputClass}
                      inputMode="numeric"
                      value={seed}
                      onChange={(e) => setSeed(e.target.value)}
                      placeholder="any number"
                    />
                  </Field>
                </div>
              ) : (
                <div className="mt-3 grid gap-3 sm:grid-cols-4">
                  <Field label="Seconds">
                    <input
                      className={inputClass}
                      inputMode="numeric"
                      value={seconds}
                      onChange={(e) => setSeconds(e.target.value)}
                      placeholder="60"
                    />
                  </Field>
                  <Field label="Scale">
                    <input
                      className={inputClass}
                      inputMode="decimal"
                      value={scale}
                      onChange={(e) => setScale(e.target.value)}
                      placeholder="1"
                    />
                  </Field>
                  <Field label="Concurrency">
                    <input
                      className={inputClass}
                      inputMode="numeric"
                      value={concurrency}
                      onChange={(e) => setConcurrency(e.target.value)}
                      placeholder="auto"
                    />
                  </Field>
                  <Field label="Seed">
                    <input
                      className={inputClass}
                      inputMode="numeric"
                      value={seed}
                      onChange={(e) => setSeed(e.target.value)}
                      placeholder="any number"
                    />
                  </Field>
                </div>
              )}
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
                      ? "Workflows are comma separated; empty runs all of them. A seed makes two runs decide the same way, so they can be compared."
                      : "Every field is optional. Empty seconds leaves the default minute; scale multiplies production's rate, concurrency caps requests in flight, and a seed makes two runs send the same sequence.")}
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
  // The Details link on a GitHub check arrives as ?pr=<number>&commit=<sha>.
  // Runs are keyed to environments and environments to pull requests, so the
  // newest run for that pull request is the one the check is about. Before
  // this the link carried only the commit, this page read only `run`, and
  // every click from GitHub landed on the list below.
  const prParam = params.get("pr");
  const prNumber = prParam !== null && /^\d+$/.test(prParam) ? Number(prParam) : null;
  const commit = params.get("commit");
  // `runs.recent` names its cursor `before` and returns `nextCursor`, which is
  // not the pair `environments.list` uses, so the adapter is here rather than
  // in the hook.
  const state = usePages<Run>(
    async (cursor) => {
      const page = await query<{ runs: Run[]; nextCursor: string | null }>("runs.recent", {
        limit: 50,
        ...(cursor === null ? {} : { before: cursor }),
      });
      return { rows: page.runs, next: page.nextCursor };
    },
    [],
  );
  const rows = state.data ?? [];

  const forPullRequest =
    prNumber === null ? null : rows.find((run) => run.pull_request === prNumber) ?? null;
  // The same rule as the detail, over the list: ask again only while a row on
  // it can still change. A list of finished runs is a list that will not move,
  // and polling one is a request every six seconds forever on a tab somebody
  // left open. Hooks run before the two early returns below because they have
  // to, so the condition carries the "is this list even on screen" part.
  useInterval(
    selected === null && prNumber === null && rows.some((r) => runIsInFlight(r.state)),
    POLL_MS,
    state.reload,
  );

  if (selected) {
    return (
      <Page title="Run" lede="Verdicts and artifacts, as the runner reported them.">
        <Detail runId={selected} onClose={() => router.push("/runs")} />
      </Page>
    );
  }

  if (prNumber !== null) {
    return (
      <Page title="Run" lede={`Pull request #${prNumber}, as the control plane received it.`}>
        <div className="space-y-6">
          <PullRequestReport pr={prNumber} commit={commit} />
          {forPullRequest ? (
            <Detail runId={forPullRequest.id} onClose={() => router.push("/runs")} />
          ) : null}
        </div>
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
            data.length === 0 ? (
              <Empty title="No runs yet" action={<LinkButton href="/cli#first-run" variant="secondary">Run your first workflow</LinkButton>}>
                Creating an environment does not test it. Execute the workflows
                in your manifest to get verdicts and evidence here.
              </Empty>
            ) : (
              <>
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
                    {data.map((r) => {
                      const summary = recentRunSummary({
                        total: Number(r.verdicts ?? 0),
                        passing: Number(r.passing ?? 0),
                        failing: Number(r.failing ?? 0),
                        proved: Number(r.proved ?? 0),
                      });
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
                            {summary === null ? (
                              <span className="text-dim">--</span>
                            ) : (
                              <span className={verdictTone[summary.tone]}>{summary.text}</span>
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
              <More
                shown={data.length}
                noun={{ one: "run", many: "runs" }}
                hasMore={state.hasMore}
                busy={state.busy}
                error={state.moreError}
                onMore={state.more}
              />
              </>
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
