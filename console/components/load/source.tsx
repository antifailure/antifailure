"use client";

import { useState } from "react";
import { query, useApi } from "@/lib/api";
import { may } from "@/lib/roles";
import { useSessionContext } from "@/components/session";
import {
  Button,
  Card,
  CellLink,
  Empty,
  Field,
  Row,
  Table,
  TableSkeleton,
  TableWrap,
  Td,
  Th,
  When,
  inputClass,
} from "@/components/ui";
import { StateBadge, VerdictBadge } from "@/components/load/primitives";
import { SourceHeader, SourceView } from "@/components/load/sources";
import { Denied, LoadError, SourceSkeleton } from "@/components/load/states";
import {
  count,
  getSource,
  listRuns,
  listVersions,
  startRun,
  type RunRow,
  type SourceDetail,
  type Version,
} from "@/lib/load";

interface Environment {
  env_id: string;
  branch: string;
  state: string;
  repository: string;
}

/**
 * Starting a run.
 *
 * The safe and unsafe patterns are on this form and not buried in a settings
 * page, because they decide what the run is allowed to do to the twin and a
 * person pressing Start should be looking at them. The placeholder is a glob
 * in the engine's own syntax, "GET /api/*", rather than a bare path, since
 * that is what the matcher takes.
 */
function Start({ source, onStarted }: { source: SourceDetail; onStarted: () => void }) {
  const session = useSessionContext();
  const csrf = session.data?.csrfToken ?? "";
  const environments = useApi<{ environments: Environment[] }>(
    () => query("environments.list", { limit: 100 }),
    [],
  );

  const [envId, setEnvId] = useState("");
  const [scale, setScale] = useState("1");
  const [duration, setDuration] = useState("60");
  const [concurrency, setConcurrency] = useState("");
  const [safe, setSafe] = useState("GET /api/*");
  const [unsafe, setUnsafe] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [started, setStarted] = useState<string | null>(null);

  const positive = (v: string) => {
    const n = Number(v);
    return v.trim() !== "" && Number.isFinite(n) && n > 0 ? n : undefined;
  };
  const patterns = (v: string) =>
    v
      .split(",")
      .map((r) => r.trim())
      .filter(Boolean);

  return (
    <Card
      title="Start a run"
      note="Sends this source at an environment that is already up, and records the result here."
    >
      {environments.status === "error" && environments.error ? (
        <LoadError error={environments.error} retry={environments.reload} />
      ) : environments.status === "loading" || environments.data === null ? (
        <TableSkeleton rows={2} cols={3} />
      ) : (
        (() => {
          const live = environments.data.environments.filter((e) => e.state !== "torn_down");
          if (live.length === 0) {
            return (
              <Empty
                title="No environment to run against"
                action={
                  <a
                    className="inline-flex h-11 items-center justify-center rounded-md bg-ink px-4 text-[14px] font-medium text-white transition-colors hover:bg-[#2b2b2b]"
                    href="/environments"
                  >
                    Ask for an environment
                  </a>
                }
              >
                Load needs a twin to send traffic at. Ask for an environment,
                and this fills once the engine reports it is up.
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
                setStarted(null);
                try {
                  const { runId } = await startRun(
                    {
                      sourceId: source.id,
                      envId: chosen,
                      ...(positive(scale) === undefined ? {} : { scale: positive(scale) }),
                      ...(positive(duration) === undefined
                        ? {}
                        : { durationSeconds: positive(duration) }),
                      ...(positive(concurrency) === undefined
                        ? {}
                        : { concurrency: positive(concurrency) }),
                      ...(patterns(safe).length ? { safe: patterns(safe) } : {}),
                      ...(patterns(unsafe).length ? { unsafe: patterns(unsafe) } : {}),
                    },
                    csrf,
                  );
                  setStarted(
                    runId
                      ? `Started as ${runId}. It appears below.`
                      : "Started. It appears below once the runner takes it.",
                  );
                  onStarted();
                } catch (err) {
                  setError(err instanceof Error ? err.message : "That did not work.");
                } finally {
                  setBusy(false);
                }
              }}
            >
              <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
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
                <Field label="Scale" hint="A multiplier on the source's own rate.">
                  <input
                    className={inputClass}
                    inputMode="decimal"
                    value={scale}
                    onChange={(e) => setScale(e.target.value)}
                    placeholder="1"
                  />
                </Field>
                <Field label="Duration" hint="Seconds.">
                  <input
                    className={inputClass}
                    inputMode="numeric"
                    value={duration}
                    onChange={(e) => setDuration(e.target.value)}
                    placeholder="60"
                  />
                </Field>
                <Field label="Concurrency" hint="Empty leaves the engine's own default.">
                  <input
                    className={inputClass}
                    inputMode="numeric"
                    value={concurrency}
                    onChange={(e) => setConcurrency(e.target.value)}
                  />
                </Field>
                <Field
                  label="Safe patterns"
                  hint="Comma separated globs. Nothing is sent unless it matches one."
                >
                  <input
                    className={inputClass}
                    value={safe}
                    onChange={(e) => setSafe(e.target.value)}
                    placeholder="GET /api/*"
                  />
                </Field>
                <Field
                  label="Unsafe patterns"
                  hint="Named explicitly as allowed to change state on the twin."
                >
                  <input
                    className={inputClass}
                    value={unsafe}
                    onChange={(e) => setUnsafe(e.target.value)}
                    placeholder="POST /api/cart"
                  />
                </Field>
              </div>
              <div className="mt-4 flex flex-wrap items-center gap-3">
                <Button type="submit" variant="primary" busy={busy}>
                  {busy ? "Starting" : "Start the run"}
                </Button>
                {error ? (
                  <p role="alert" className="text-[12.5px] leading-6 text-fail">
                    {error}
                  </p>
                ) : started ? (
                  <p role="status" className="text-[12.5px] leading-6 text-muted">
                    {started}
                  </p>
                ) : (
                  <p className="text-[12.5px] leading-6 text-dim">
                    Every route is unsafe until a safe pattern matches it, so an empty safe list
                    sends nothing.
                  </p>
                )}
              </div>
            </form>
          );
        })()
      )}
    </Card>
  );
}

function Versions({ id }: { id: string }) {
  const state = useApi<Version[]>(() => listVersions(id), [id]);
  return (
    <Card title="Versions" note="An edit makes a new version. A run records which one it used.">
      {state.status === "error" && state.error ? (
        <LoadError error={state.error} retry={state.reload} />
      ) : state.status === "loading" || state.data === null ? (
        <TableSkeleton rows={3} cols={4} />
      ) : state.data.length === 0 ? (
        <Empty title="No version history">
          Nothing has been recorded against this source since it was created.
        </Empty>
      ) : (
        <TableWrap>
          <Table>
            <thead>
              <tr>
                <Th>Version</Th>
                <Th>Created</Th>
                <Th>By</Th>
                <Th numeric>Runs</Th>
                <Th>Note</Th>
              </tr>
            </thead>
            <tbody>
              {state.data.map((v) => (
                <Row key={v.id}>
                  <Td>{v.version === null ? "--" : `v${v.version}`}</Td>
                  <Td label="Created">
                    <When value={v.createdAt} />
                  </Td>
                  <Td label="By">{v.author ?? "--"}</Td>
                  <Td label="Runs" numeric>
                    {count(v.runCount)}
                  </Td>
                  <Td label="Note" className="max-w-[40ch]">
                    {v.note ?? "--"}
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

function RunsFor({
  id,
  onOpen,
  nonce,
}: {
  id: string;
  onOpen: (runId: string) => void;
  nonce: number;
}) {
  const state = useApi<{ items: RunRow[] }>(() => listRuns({ sourceId: id }), [id, nonce]);
  return (
    <Card title="Runs" note="Every run of this source, newest first.">
      {state.status === "error" && state.error ? (
        <LoadError error={state.error} retry={state.reload} />
      ) : state.status === "loading" || state.data === null ? (
        <TableSkeleton rows={4} cols={5} />
      ) : state.data.items.length === 0 ? (
        <Empty title="Never run">
          This source is configured and has never been sent anywhere. Start it
          above and the result lands here.
        </Empty>
      ) : (
        <TableWrap>
          <Table>
            <thead>
              <tr>
                {/* When, not which id. Every row here is a run of the same
                    source, so the id distinguishes nothing a person is looking
                    for, and a truncated one leading the row reads as a word
                    that got cut off. */}
                <Th>Started</Th>
                <Th>Verdict</Th>
                <Th>State</Th>
                <Th>Finished</Th>
                <Th>Run</Th>
              </tr>
            </thead>
            <tbody>
              {state.data.items.map((r) => (
                <Row key={r.id} onClick={() => onOpen(r.id)}>
                  <Td>
                    <CellLink href={`/load?run=${encodeURIComponent(r.id)}`}>
                      <When value={r.startedAt ?? r.createdAt} />
                    </CellLink>
                  </Td>
                  <Td label="Verdict">
                    <VerdictBadge verdict={r.verdict} />
                  </Td>
                  <Td label="State">
                    <StateBadge state={r.state} />
                  </Td>
                  <Td label="Finished">
                    {r.finishedAt ? <When value={r.finishedAt} /> : <span className="text-dim">not yet</span>}
                  </Td>
                  <Td label="Run" mono className="text-dim">
                    {r.id}
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

export function SourceDetailView({
  id,
  onClose,
  onOpenRun,
}: {
  id: string;
  onClose: () => void;
  onOpenRun: (runId: string) => void;
}) {
  const session = useSessionContext();
  const canRun = may(session.data?.role, "load.run");
  const state = useApi<SourceDetail | null>(() => getSource(id), [id]);
  const [runNonce, setRunNonce] = useState(0);

  if (state.status === "error" && state.error) {
    return (
      <LoadError
        error={state.error}
        retry={state.reload}
        back={<Button onClick={onClose}>Back to Load</Button>}
      />
    );
  }
  if (state.status === "loading") return <SourceSkeleton />;
  const source = state.data;
  if (source === null || source === undefined) {
    return (
      <Empty title="That source is not here" action={<Button onClick={onClose}>Back to Load</Button>}>
        The address names a source that does not exist, or one that belongs to
        another organization.
      </Empty>
    );
  }

  return (
    <div className="space-y-6">
      <Card
        title={source.name}
        note={source.repository ?? undefined}
        actions={<Button onClick={onClose}>Close</Button>}
      >
        <SourceHeader kind={source.kind} />
        <SourceView source={source.source} />
      </Card>

      {canRun ? (
        <Start source={source} onStarted={() => setRunNonce((n) => n + 1)} />
      ) : (
        <Denied what="Start a run" />
      )}

      <RunsFor id={source.id} onOpen={onOpenRun} nonce={runNonce} />
      <Versions id={source.id} />
    </div>
  );
}
