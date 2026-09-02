"use client";

import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { ago, when } from "@/lib/format";
import { mutate, query, useApi, usePages } from "@/lib/api";
import { may } from "@/lib/roles";
import { useSessionContext } from "@/components/session";
import { More } from "@/components/pagination";
import {
  Badge,
  Button,
  Card,
  CellLink,
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
  When,
  inputClass,
  toneFor,
} from "@/components/ui";

interface Repository {
  id: string;
  full_name: string;
  default_branch: string;
}

interface Runtime {
  id: string | null;
  name: string;
  provider: string | null;
  labels: string[] | null;
  note: string | null;
  created_at: string | null;
  removed_at: string | null;
  registered_by: string | null;
  registered: boolean;
  environments: string;
}

interface Environment {
  id: string;
  env_id: string;
  branch: string;
  pull_request: number | null;
  state: string;
  preview_url: string | null;
  runtime: string | null;
  golden_version: string | null;
  created_at: string;
  updated_at: string;
  expires_at: string | null;
  repository: string;
}


function Detail({ envId, onClose }: { envId: string; onClose: () => void }) {
  const session = useSessionContext();
  const state = useApi<Environment>(() => query("environments.get", { envId }), [envId]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const csrf = session.data?.csrfToken ?? "";

  return (
    <Card
      title={envId}
      note="One environment, as the control plane last heard about it."
      actions={<Button onClick={onClose}>Close</Button>}
    >
      <Loaded state={state} skeleton={<TableSkeleton rows={4} cols={2} />}>
        {(env) => (
          <>
            <dl className="grid gap-x-8 gap-y-4 px-4 py-4 sm:grid-cols-2">
              {[
                ["Repository", env.repository],
                ["Branch", env.branch],
                ["Pull request", env.pull_request ? `#${env.pull_request}` : "none"],
                ["Runtime", env.runtime ?? "not reported"],
                ["Golden", env.golden_version ?? "none"],
                ["Created", `${when(env.created_at)} (${ago(env.created_at)})`],
                ["Updated", `${when(env.updated_at)} (${ago(env.updated_at)})`],
                ["Expires", env.expires_at ? when(env.expires_at) : "no expiry set"],
              ].map(([k, v]) => (
                <div key={k}>
                  <dt className="text-[11px] uppercase tracking-[0.08em] text-dim">{k}</dt>
                  <dd className="mt-1 text-[13px] text-ink">{v}</dd>
                </div>
              ))}
              <div>
                <dt className="text-[11px] uppercase tracking-[0.08em] text-dim">State</dt>
                <dd className="mt-1">
                  <Badge tone={toneFor(env.state)}>{env.state.replace("_", " ")}</Badge>
                </dd>
              </div>
              {env.preview_url ? (
                <div>
                  <dt className="text-[11px] uppercase tracking-[0.08em] text-dim">Preview</dt>
                  <dd className="mt-1 truncate text-[13px]">
                    <a
                      className="text-ink underline decoration-[rgba(16,16,16,0.25)] underline-offset-4 hover:decoration-ink"
                      href={env.preview_url}
                      rel="noreferrer noopener"
                    >
                      {env.preview_url}
                    </a>
                  </dd>
                </div>
              ) : null}
            </dl>

            {env.state !== "torn_down" && may(session.data?.role, "environments.teardown") ? (
              <div className="border-t border-rule px-4 py-3">
                <p className="text-[12.5px] leading-5 text-muted">
                  Tearing down marks the environment. The engine holding the
                  containers reads that and does the removing, so this asks
                  rather than reaches.
                </p>
                {error ? (
                  <p role="alert" className="mt-2 text-[12.5px] text-fail">
                    {error}
                  </p>
                ) : null}
                <div className="mt-3">
                  <Button
                    variant="danger"
                    busy={busy}
                    onClick={async () => {
                      setBusy(true);
                      setError(null);
                      try {
                        await mutate("environments.teardown", { envId }, csrf);
                        state.reload();
                      } catch (e) {
                        setError(e instanceof Error ? e.message : "That did not work.");
                      } finally {
                        setBusy(false);
                      }
                    }}
                  >
                    {busy ? "Requesting" : "Request teardown"}
                  </Button>
                </div>
              </div>
            ) : null}
          </>
        )}
      </Loaded>
    </Card>
  );
}


/**
 * Asking for an environment.
 *
 * The control plane does not bring one up. It dispatches a run of the
 * repository's own workflow, in the repository, on the branch named here, and
 * the engine does the work in the customer's CI where their database and their
 * credentials already are. So the button says what actually happens rather than
 * "Create": nothing appears in the table below until the engine reports it, and
 * a screen that claimed otherwise would show an environment that does not exist.
 */
function Create({ onRequested }: { onRequested: () => void }) {
  const session = useSessionContext();
  const repositories = useApi<Repository[]>(
    () => query("repositories.list", { includeArchived: false }),
    [],
  );
  const [repository, setRepository] = useState("");
  const [branch, setBranch] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [asked, setAsked] = useState<string | null>(null);
  const csrf = session.data?.csrfToken ?? "";

  return (
    <Card
      title="Ask for an environment"
      note="Dispatches your workflow in your repository. Nothing runs on this control plane."
    >
      <Loaded state={repositories} skeleton={<TableSkeleton rows={1} cols={3} />}>
        {(repos) =>
          repos.length === 0 ? (
            <Empty title="No repositories connected">
              An environment belongs to a repository. One appears here when the
              GitHub App reports an installation that includes it.
            </Empty>
          ) : (
            <form
              className="px-4 py-4"
              onSubmit={async (e) => {
                e.preventDefault();
                const repo = repository || repos[0]!.full_name;
                setBusy(true);
                setError(null);
                setAsked(null);
                try {
                  const answer = await mutate<{ ref: string }>(
                    "environments.create",
                    { repository: repo, ...(branch.trim() ? { branch: branch.trim() } : {}) },
                    csrf,
                  );
                  setAsked(`Asked GitHub to run the workflow on ${answer.ref}.`);
                  onRequested();
                } catch (err) {
                  setError(err instanceof Error ? err.message : "That did not work.");
                } finally {
                  setBusy(false);
                }
              }}
            >
              {/* The hint sits under the row, not in a Field: inside one it
                  makes that cell taller and items-end lifts its input clear of
                  the select beside it. */}
              <div className="grid gap-3 sm:grid-cols-[2fr_2fr_auto] sm:items-end">
                <Field label="Repository">
                  <select
                    className={inputClass}
                    value={repository || repos[0]!.full_name}
                    onChange={(e) => setRepository(e.target.value)}
                  >
                    {repos.map((r) => (
                      <option key={r.id} value={r.full_name}>
                        {r.full_name}
                      </option>
                    ))}
                  </select>
                </Field>
                <Field label="Branch">
                  <input
                    className={inputClass}
                    value={branch}
                    onChange={(e) => setBranch(e.target.value)}
                    placeholder="main"
                  />
                </Field>
                <Button type="submit" variant="primary" busy={busy}>
                  {busy ? "Asking" : "Ask for one"}
                </Button>
              </div>
              {error ? (
                <p role="alert" className="mt-2.5 text-[12px] leading-5 text-fail">
                  {error}
                </p>
              ) : (
                <p role={asked ? "status" : undefined} className="mt-2.5 text-[12px] leading-5 text-dim">
                  {asked ?? "Empty uses the repository's default branch. The environment appears below when the engine reports it."}
                </p>
              )}
            </form>
          )
        }
      </Loaded>
    </Card>
  );
}

/**
 * The runtime registry, against what is actually running.
 *
 * Two kinds of row, and the second is the reason this is a screen rather than a
 * settings list: a runtime somebody registered, and a runtime an environment is
 * reporting that nobody registered. The second is an environment running
 * somewhere the organization never agreed to, which is worth seeing and is
 * invisible in a table of what was registered.
 *
 * Registering a name changes nothing about where anything runs. The engine
 * decides that from the manifest in the repository, and the control plane holds
 * no credential for any runtime and never reaches one.
 */
function Runtimes() {
  const session = useSessionContext();
  const state = useApi<Runtime[]>(() => query("runtimes.list", { includeRemoved: false }), []);
  const csrf = session.data?.csrfToken ?? "";
  const mayManage = may(session.data?.role, "runtimes.manage");
  const [name, setName] = useState("");
  const [provider, setProvider] = useState("local");
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function act(key: string, run: () => Promise<unknown>) {
    setBusy(key);
    setError(null);
    try {
      await run();
      state.reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : "That did not work.");
    } finally {
      setBusy(null);
    }
  }

  return (
    <Card
      title="Runtimes"
      note="Where this organization has agreed its environments run, beside where they are actually running."
      actions={
        error ? (
          <span role="alert" className="min-w-0 max-w-[46ch] break-words text-left text-[12px] leading-4 text-fail sm:text-right">
            {error}
          </span>
        ) : null
      }
    >
      {mayManage ? (
        <form
          className="border-b border-rule px-4 py-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (!name.trim()) return;
            void act("register", async () => {
              await mutate("runtimes.register", { name: name.trim(), provider, labels: [] }, csrf);
              setName("");
            });
          }}
        >
          <div className="grid gap-3 sm:grid-cols-[2fr_1fr_auto] sm:items-end">
            <Field label="Name">
              <input
                className={inputClass}
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="eu-cluster"
                required
              />
            </Field>
            <Field label="Provider">
              <select
                className={inputClass}
                value={provider}
                onChange={(e) => setProvider(e.target.value)}
              >
                <option value="local">local</option>
                <option value="kubernetes">kubernetes</option>
              </select>
            </Field>
            <Button type="submit" busy={busy === "register"}>
              {busy === "register" ? "Registering" : "Register"}
            </Button>
          </div>
          <p className="mt-2.5 text-[12px] leading-5 text-dim">
            Lower case letters, digits and hyphens. Registering a name records
            where environments are meant to run and changes nothing about where
            they do: the manifest in the repository decides that.
          </p>
        </form>
      ) : null}

      <Loaded state={state} skeleton={<TableSkeleton rows={2} cols={4} />}>
        {(rows) =>
          rows.length === 0 ? (
            <Empty title="No runtimes">
              Nothing is registered and no environment has reported where it
              ran. This fills itself the first time one does.
            </Empty>
          ) : (
            <TableWrap>
              <Table>
                <thead>
                  <tr>
                    <Th>Runtime</Th>
                    <Th>Provider</Th>
                    <Th>Labels</Th>
                    <Th numeric>Environments</Th>
                    <Th>State</Th>
                    {mayManage ? <Th>Registration</Th> : null}
                  </tr>
                </thead>
                <tbody>
                  {rows.map((r) => (
                    <Row key={`${r.name}-${r.registered}`}>
                      <Td mono>{r.name}</Td>
                      <Td label="Provider">{r.provider ?? "not reported"}</Td>
                      <Td label="Labels">{r.labels?.length ? r.labels.join(", ") : "--"}</Td>
                      <Td label="Environments" numeric>
                        {Number(r.environments).toLocaleString()}
                      </Td>
                      <Td label="State">
                        {r.registered ? (
                          <Badge tone="pass">registered</Badge>
                        ) : (
                          <Badge tone="warn">nobody registered this</Badge>
                        )}
                      </Td>
                      {mayManage ? (
                        <Td label="Registration">
                          {r.registered ? (
                            <Button
                              variant="danger"
                              busy={busy === r.name}
                              onClick={() =>
                                void act(r.name, () =>
                                  mutate("runtimes.remove", { name: r.name }, csrf),
                                )
                              }
                            >
                              Remove
                            </Button>
                          ) : (
                            <Button
                              busy={busy === r.name}
                              onClick={() =>
                                void act(r.name, () =>
                                  mutate(
                                    "runtimes.register",
                                    { name: r.name, provider: "local", labels: [] },
                                    csrf,
                                  ),
                                )
                              }
                            >
                              Register
                            </Button>
                          )}
                        </Td>
                      ) : null}
                    </Row>
                  ))}
                </tbody>
              </Table>
            </TableWrap>
          )
        }
      </Loaded>
    </Card>
  );
}

function Environments() {
  const session = useSessionContext();
  const params = useSearchParams();
  const router = useRouter();
  const selected = params.get("env");
  const state = usePages<Environment>(
    async (cursor) => {
      const page = await query<{ environments: Environment[]; nextCursor: string | null }>(
        "environments.list",
        { limit: 50, ...(cursor === null ? {} : { cursor }) },
      );
      return { rows: page.environments, next: page.nextCursor };
    },
    [],
  );

  return (
    <Page
      title="Environments"
      lede="Every environment this organization has, newest first. State is what the engine last reported, not what was asked for."
    >
      {selected ? (
        <div className="mb-6">
          <Detail envId={selected} onClose={() => router.push("/environments")} />
        </div>
      ) : null}

      {may(session.data?.role, "environments.create") ? (
        <div className="mb-6">
          <Create onRequested={state.reload} />
        </div>
      ) : null}

      <Card title="All environments">
        <Loaded state={state} skeleton={<TableSkeleton rows={6} cols={5} />}>
          {(data) =>
            data.length === 0 ? (
              <Empty title="No environments yet">
                An environment appears here when the engine reports one, which
                happens the first time a run starts on a connected repository.
              </Empty>
            ) : (
              <>
              <TableWrap>
                <Table>
                  <thead>
                    <tr>
                      <Th>Environment</Th>
                      <Th>Repository</Th>
                      <Th>Branch</Th>
                      <Th>State</Th>
                      <Th>Created</Th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.map((env) => (
                      <Row
                        key={env.id}
                        onClick={() => router.push(`/environments?env=${encodeURIComponent(env.env_id)}`)}
                      >
                        <Td mono>
                          <CellLink href={`/environments?env=${encodeURIComponent(env.env_id)}`}>
                            {env.env_id}
                          </CellLink>
                        </Td>
                        <Td label="Repository">{env.repository}</Td>
                        <Td label="Branch">
                          {env.branch}
                          {env.pull_request ? (
                            <span className="ml-1.5 text-dim">#{env.pull_request}</span>
                          ) : null}
                        </Td>
                        <Td label="State">
                          <Badge tone={toneFor(env.state)}>{env.state.replace("_", " ")}</Badge>
                        </Td>
                        <Td label="Created">
                          <When value={env.created_at} />
                        </Td>
                      </Row>
                    ))}
                  </tbody>
                </Table>
              </TableWrap>
              <More
                shown={data.length}
                noun={{ one: "environment", many: "environments" }}
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

      <div className="mt-6">
        <Runtimes />
      </div>
    </Page>
  );
}

export default function EnvironmentsPage() {
  return (
    <Suspense fallback={<Page title="Environments"><TableSkeleton /></Page>}>
      <Environments />
    </Suspense>
  );
}
