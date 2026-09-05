"use client";

import { useState } from "react";
import Link from "next/link";
import { Badge, Button, Card, CardSkeleton, Confirm, Empty, Field, Loaded, Table,
  TableWrap, Td, Th, When, inputClass, toneFor } from "@/components/ui";
import { AdminPage } from "@/components/admin/primitives";
import { operatorMay, useAdminContext } from "@/lib/admin";
import { revokeCredential, useMcpSurface, type McpConnection } from "@/lib/admin-platform";

/** Hosted credentials are measured separately from local checkout sessions. */
export default function PlatformMcpPage() {
  const state = useMcpSurface();
  const { me } = useAdminContext();
  const [selected, setSelected] = useState<McpConnection | null>(null);
  const mayRevoke = operatorMay(me, "admin.keys.revoke");

  return (
    <AdminPage href="/admin/platform/mcp" lede="Manage the clients and credentials people have approved for the hosted control plane.">
      <Loaded state={state} framed skeleton={<CardSkeleton count={2} />}>
        {(surface) => (
          <div className="space-y-5">
            <Card title="Hosted MCP endpoint" note="Add this URL in an MCP client with HTTP and OAuth support, then sign in and choose an organization.">
              <div className="px-4 py-4">
                {surface.endpoint ? <code className="block break-all text-[16px] font-medium text-ink">{surface.endpoint}</code> :
                  <p className="text-[16px] text-muted">An application URL has not been configured for this installation.</p>}
                <p className="mt-3 text-[16px] sm:text-[13px] leading-6 text-muted">An agent can only do what its user and approved scopes allow. New environments are requested through the repository workflow.</p>
              </div>
              <dl className="grid grid-cols-2 border-t border-rule lg:grid-cols-4">
                {([
                  ["Registered clients", surface.counts.clients],
                  ["Active credentials", surface.counts.active],
                  ["Revoked", surface.counts.revoked],
                  ["Expired", surface.counts.expired],
                ] as const).map(([label, value]) => (
                  <div key={label} className="min-w-0 px-4 py-4">
                    <dt className="text-[16px] sm:text-[13px] text-muted">{label}</dt>
                    <dd className="mt-1 font-mono text-[28px] leading-9 tabular-nums text-ink">{value.toLocaleString()}</dd>
                  </div>
                ))}
              </dl>
            </Card>

            <Card title="Approved credentials" note="Active means unexpired and not revoked, not a live network connection. Last authentication includes protocol requests, not just tool calls.">
              {surface.connections.length === 0 ? (
                <Empty title="No MCP access has been approved yet">Connect an MCP client to the endpoint above, sign in and approve access. Its credential will appear here.</Empty>
              ) : (
                <TableWrap><Table className="sm:min-w-[760px] [&_td]:text-[16px] sm:[&_td]:text-[13px]"><thead><tr>
                  <Th>Client and person</Th><Th>Organization</Th><Th>Access</Th><Th>Last authentication</Th><Th>Expires</Th><Th>Status</Th>{mayRevoke ? <Th>Action</Th> : null}
                </tr></thead><tbody>{surface.connections.map((connection) => (
                  <tr key={connection.id}>
                    <Td><span className="block max-w-[24ch] break-words font-medium text-ink">{connection.clientName}</span><span className="block text-[16px] sm:text-[13px] text-muted">{connection.userLogin}</span><span className="block font-mono text-[12px] text-dim">{connection.prefix}</span><span className="block text-[13px] text-muted">Issued <When value={connection.createdAt} /></span></Td>
                    <Td label="Organization"><span className="break-words">{connection.orgSlug}</span></Td>
                    <Td label="Access"><span className="block text-[16px] sm:text-[13px]">{connection.scopes.includes("mcp:read") ? "Read" : "No read access"}</span><span className="block text-[16px] sm:text-[13px] text-muted">{connection.scopes.includes("mcp:write") ? "Request changes" : "No changes"}</span></Td>
                    <Td label="Last used">{connection.lastAuthenticatedAt ? <When value={connection.lastAuthenticatedAt} /> : <span className="text-muted">Not used yet</span>}</Td>
                    <Td label="Expires"><When value={connection.expiresAt} /></Td>
                    <Td label="Status"><Badge tone={toneFor(connection.standing)}>{connection.standing}</Badge></Td>
                    {mayRevoke ? <Td label="Action">{connection.standing === "active" ? <Button variant="danger" onClick={() => setSelected(connection)}>Revoke<span className="sr-only"> {connection.clientName} for {connection.orgSlug}</span></Button> : <span className="text-[16px] sm:text-[13px] text-muted">Already refused</span>}</Td> : null}
                  </tr>
                ))}</tbody></Table></TableWrap>
              )}
              <div className="flex flex-wrap items-center justify-between gap-3 border-t border-rule px-4 py-3 text-[16px] sm:text-[13px] leading-6 text-muted">
                <p>{surface.hasMore ? "Showing the newest 50 credentials. " : "All issued credentials are shown. "}Measured <When value={surface.at} />.</p>
                <Link className="inline-flex min-h-11 items-center font-medium text-ink underline underline-offset-4" href="/admin/platform/api-keys">Search all credentials</Link>
              </div>
            </Card>
            <p className="max-w-[75ch] text-[16px] sm:text-[13px] leading-6 text-muted">Local <code>af mcp</code> runs inside a checkout and is separate from this hosted endpoint. Its migration rehearsals and local runs are not counted here. <a className="font-medium text-ink underline underline-offset-4" href="https://antifailure.dev/docs/reference/mcp">Read the MCP setup guide</a>.</p>
          </div>
        )}
      </Loaded>
      {selected ? <RevokeConnection key={selected.id} connection={selected} onClose={() => setSelected(null)} onDone={() => state.reload()} /> : null}
    </AdminPage>
  );
}

function RevokeConnection({ connection, onClose, onDone }: { connection: McpConnection; onClose: () => void; onDone: () => void }) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function revoke() {
    if (reason.trim().length < 8) { setError("Give a reason of at least 8 characters."); return; }
    setBusy(true);
    setError(null);
    try { await revokeCredential(connection.id, reason.trim()); onDone(); onClose(); }
    catch (failure) { setError(failure instanceof Error ? failure.message : "Revocation failed. Try again."); }
    finally { setBusy(false); }
  }
  return <Confirm open title={`Revoke ${connection.clientName}?`} phrase={connection.prefix} confirmLabel="Revoke access" busy={busy} error={error} onCancel={onClose} onConfirm={() => void revoke()}>
    <p>The next request using this credential will be refused. Work already dispatched keeps running. {connection.userLogin} must reconnect their MCP client and approve access again.</p>
    <Field label="Reason" hint="Required. Recorded in the operator and organization audit logs."><input className={inputClass} value={reason} onChange={(event) => setReason(event.target.value)} maxLength={500} required /></Field>
  </Confirm>;
}
