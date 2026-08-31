"use client";

import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { ago, when } from "@/lib/format";
import { mutate, query, useApi } from "@/lib/api";
import { useSessionContext } from "@/components/session";
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

const MAY_TEAR_DOWN = new Set(["owner", "admin", "member"]);

function Detail({ envId, onClose }: { envId: string; onClose: () => void }) {
  const session = useSessionContext();
  const state = useApi<Environment>(() => query("environments.get", { envId }), [envId]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const role = session.data?.role ?? "";
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

            {env.state !== "torn_down" && MAY_TEAR_DOWN.has(role) ? (
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

function Environments() {
  const params = useSearchParams();
  const router = useRouter();
  const selected = params.get("env");
  const state = useApi<{ environments: Environment[]; nextCursor: string | null }>(
    () => query("environments.list", { limit: 50 }),
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

      <Card title="All environments">
        <Loaded state={state} skeleton={<TableSkeleton rows={6} cols={5} />}>
          {(data) =>
            data.environments.length === 0 ? (
              <Empty title="No environments yet">
                An environment appears here when the engine reports one, which
                happens the first time a run starts on a connected repository.
              </Empty>
            ) : (
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
                    {data.environments.map((env) => (
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
            )
          }
        </Loaded>
      </Card>
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
