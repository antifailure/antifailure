"use client";

import { useState } from "react";
import { query, useApi } from "@/lib/api";
import { may } from "@/lib/roles";
import { useSessionContext } from "@/components/session";
import {
  Badge,
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
import { Provenance, RunStatusBadge } from "@/components/workloads/primitives";
import { SourceHeader, SourceView } from "@/components/workloads/sources";
import { DefinitionSkeleton, Denied, WorkloadError } from "@/components/workloads/states";
import {
  count,
  getDefinition,
  listRuns,
  listVersions,
  promoteDiscovery,
  seconds,
  startRun,
  type Definition,
  type RunRow,
  type Version,
} from "@/lib/workloads";

interface Environment {
  env_id: string;
  branch: string;
  state: string;
  repository: string;
}

/**
 * Starting a run.
 *
 * Baseline and candidate are two presses rather than one, and that is
 * deliberate. A single "compare" button would have to choose when to run each
 * side, and the honest answer is that a baseline is measured against the code
 * before the change and a candidate against the code after it, which is two
 * moments a console cannot schedule on somebody's behalf. Naming the side
 * makes the comparison something a person assembles from two real runs rather
 * than something the product implies it did.
 */
function Start({
  definition,
  onStarted,
}: {
  definition: Definition;
  onStarted: () => void;
}) {
  const session = useSessionContext();
  const csrf = session.data?.csrfToken ?? "";
  const environments = useApi<{ environments: Environment[] }>(
    () => query("environments.list", { limit: 100 }),
    [],
  );

  const [envId, setEnvId] = useState("");
  const [execution, setExecution] = useState<"baseline" | "candidate">("candidate");
  const [scale, setScale] = useState("1");
  const [duration, setDuration] = useState("60");
  const [concurrency, setConcurrency] = useState("");
  const [safe, setSafe] = useState("");
  const [unsafe, setUnsafe] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [started, setStarted] = useState<string | null>(null);

  const numbers = (v: string) => {
    const n = Number(v);
    return v.trim() !== "" && Number.isFinite(n) && n > 0 ? n : undefined;
  };
  const routes = (v: string) =>
    v
      .split(",")
      .map((r) => r.trim())
      .filter(Boolean);

  return (
    <Card
      title="Start a run"
      note="Sends this workload at an environment that is already up, and records the result against this definition."
    >
      {environments.status === "error" && environments.error ? (
        <WorkloadError error={environments.error} retry={environments.reload} />
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
                A workload needs a twin to send traffic at. Ask for an
                environment, and this fills once the engine reports it is up.
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
                      definitionId: definition.id,
                      envId: chosen,
                      execution,
                      ...(numbers(scale) === undefined ? {} : { scale: numbers(scale) }),
                      ...(numbers(duration) === undefined
                        ? {}
                        : { durationSeconds: numbers(duration) }),
                      ...(numbers(concurrency) === undefined
                        ? {}
                        : { concurrency: numbers(concurrency) }),
                      ...(routes(safe).length ? { safeRoutes: routes(safe) } : {}),
                      ...(routes(unsafe).length ? { unsafeRoutes: routes(unsafe) } : {}),
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
                <Field
                  label="Side"
                  hint="Which half of the comparison this run is."
                >
                  <select
                    className={inputClass}
                    value={execution}
                    onChange={(e) =>
                      setExecution(e.target.value === "baseline" ? "baseline" : "candidate")
                    }
                  >
                    <option value="baseline">baseline</option>
                    <option value="candidate">candidate</option>
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
                <Field
                  label="Concurrency"
                  hint="Empty leaves the engine's own default."
                >
                  <input
                    className={inputClass}
                    inputMode="numeric"
                    value={concurrency}
                    onChange={(e) => setConcurrency(e.target.value)}
                    placeholder=""
                  />
                </Field>
                <Field label="Safe routes" hint="Comma separated. Read only.">
                  <input
                    className={inputClass}
                    value={safe}
                    onChange={(e) => setSafe(e.target.value)}
                    placeholder="/api/products, /api/search"
                  />
                </Field>
                <Field
                  label="Unsafe routes"
                  hint="Comma separated. Allowed to change state on the twin."
                >
                  <input
                    className={inputClass}
                    value={unsafe}
                    onChange={(e) => setUnsafe(e.target.value)}
                    placeholder="/api/checkout"
                  />
                </Field>
              </div>
              <div className="mt-4 flex flex-wrap items-center gap-3">
                <Button type="submit" variant="primary" busy={busy}>
                  {busy ? "Starting" : `Start the ${execution} run`}
                </Button>
                {error ? (
                  <p role="alert" className="text-[12.5px] leading-6 text-fail">
                    {error}
                  </p>
                ) : started ? (
                  <p role="status" className="text-[12.5px] leading-6 text-muted">
                    {started}
                  </p>
                ) : null}
              </div>
            </form>
          );
        })()
      )}
    </Card>
  );
}

/** Every version of the definition, so a result can be read against the thing
 *  that produced it. */
function Versions({ id }: { id: string }) {
  const state = useApi<Version[]>(() => listVersions(id), [id]);
  return (
    <Card title="Versions" note="An edit makes a new version. A run records which one it used.">
      {state.status === "error" && state.error ? (
        <WorkloadError error={state.error} retry={state.reload} />
      ) : state.status === "loading" || state.data === null ? (
        <TableSkeleton rows={3} cols={4} />
      ) : state.data.length === 0 ? (
        <Empty title="No version history">
          Nothing has been recorded against this definition since it was
          created.
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
                    <When value={v.created_at} />
                  </Td>
                  <Td label="By">{v.author ?? "--"}</Td>
                  <Td label="Runs" numeric>
                    {count(v.run_count)}
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

function RunsFor({ id, onOpen, nonce }: { id: string; onOpen: (runId: string) => void; nonce: number }) {
  const state = useApi<{ items: RunRow[] }>(() => listRuns({ definitionId: id }), [id, nonce]);
  return (
    <Card title="Runs" note="Every run of this workload, newest first.">
      {state.status === "error" && state.error ? (
        <WorkloadError error={state.error} retry={state.reload} />
      ) : state.status === "loading" || state.data === null ? (
        <TableSkeleton rows={4} cols={4} />
      ) : state.data.items.length === 0 ? (
        <Empty title="Never run">
          This workload has been defined but never sent anywhere. Start it above
          and the result lands here.
        </Empty>
      ) : (
        <TableWrap>
          <Table>
            <thead>
              <tr>
                <Th>Run</Th>
                <Th>Side</Th>
                <Th>Status</Th>
                <Th>Started</Th>
              </tr>
            </thead>
            <tbody>
              {state.data.items.map((r) => (
                <Row key={r.id} onClick={() => onOpen(r.id)}>
                  <Td>
                    <CellLink href={`/workloads?run=${encodeURIComponent(r.id)}`}>
                      <span className="font-mono text-[12px]">{r.id.slice(0, 12)}</span>
                    </CellLink>
                  </Td>
                  <Td label="Side">{r.execution ?? "--"}</Td>
                  <Td label="Status">
                    <RunStatusBadge status={r.status} />
                  </Td>
                  <Td label="Started">
                    <When value={r.started_at ?? r.created_at} />
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

export function DefinitionView({
  id,
  onClose,
  onOpenRun,
}: {
  id: string;
  onClose: () => void;
  onOpenRun: (runId: string) => void;
}) {
  const session = useSessionContext();
  const csrf = session.data?.csrfToken ?? "";
  const canRun = may(session.data?.role, "load.run");

  const state = useApi<Definition | null>(() => getDefinition(id), [id]);
  const [runNonce, setRunNonce] = useState(0);
  const [promoting, setPromoting] = useState<string | null>(null);
  const [promoteError, setPromoteError] = useState<string | null>(null);

  if (state.status === "error" && state.error) {
    return (
      <WorkloadError
        error={state.error}
        retry={state.reload}
        back={<Button onClick={onClose}>Back to workloads</Button>}
      />
    );
  }
  if (state.status === "loading") return <DefinitionSkeleton />;
  const definition = state.data;
  if (definition === null || definition === undefined) {
    return (
      <Empty
        title="That workload is not here"
        action={<Button onClick={onClose}>Back to workloads</Button>}
      >
        The address names a definition that does not exist, or one that belongs
        to another organization.
      </Empty>
    );
  }

  return (
    <div className="space-y-6">
      <Card
        title={definition.name}
        note={definition.repository ?? undefined}
        actions={<Button onClick={onClose}>Close</Button>}
      >
        <SourceHeader kind={definition.kind} />
        {definition.description ? (
          <p className="border-b border-rule px-4 py-3 text-[13px] leading-6 text-muted">
            {definition.description}
          </p>
        ) : null}
        <SourceView
          source={definition.source}
          canPromote={canRun}
          promoting={promoting}
          promoteError={promoteError}
          onPromote={async (discoveryId, name) => {
            setPromoting(discoveryId);
            setPromoteError(null);
            try {
              await promoteDiscovery({ definitionId: definition.id, discoveryId, name }, csrf);
              state.reload();
            } catch (e) {
              setPromoteError(
                e instanceof Error ? e.message : "That discovery could not be promoted.",
              );
            } finally {
              setPromoting(null);
            }
          }}
        />
      </Card>

      {canRun ? (
        <Start definition={definition} onStarted={() => setRunNonce((n) => n + 1)} />
      ) : (
        <Denied what="Start a run" />
      )}

      <RunsFor id={definition.id} onOpen={onOpenRun} nonce={runNonce} />
      <Versions id={definition.id} />
    </div>
  );
}
