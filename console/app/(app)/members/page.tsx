"use client";

import { useState } from "react";
import { mutate, query, useApi } from "@/lib/api";
import { useSessionContext } from "@/components/session";
import { may } from "@/lib/roles";
import {
  Badge,
  Button,
  Card,
  Confirm,
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
  selectClass,
  toneFor,
} from "@/components/ui";

interface Member {
  github_login: string;
  name: string | null;
  avatar_url: string | null;
  role: string;
  source: string;
  created_at: string;
}

interface Invitation {
  id: string;
  email: string;
  role: string;
  invitedBy: string;
  sentAt: string;
  expiresAt: string;
  state: "open" | "accepted" | "revoked" | "expired";
}

interface SyncReport {
  added: string[];
  removed: string[];
  changed: { login: string; from: string; to: string }[];
}

interface SentInvitation {
  email: string;
  role: string;
  link: string;
  expiresAt: string;
  emailed: boolean;
  note: string | null;
}

const ROLES = ["owner", "admin", "member", "viewer"] as const;

function saidWrong(err: unknown): string {
  return err instanceof Error ? err.message : "That did not work.";
}

/* -------------------------------------------------------------------------
 * The link, once
 * ---------------------------------------------------------------------- */

/**
 * What happened to an invitation, and the link either way.
 *
 * The link is shown whether or not the message was sent, because mail is
 * optional in this product: a control plane with no mailer configured cannot
 * send anything, and an invitation that only existed as an email would be a
 * control that silently does nothing there. Showing it always also means the
 * ordinary case, pasting it into chat, does not need a second click.
 */
function SentCard({ sent, onDone }: { sent: SentInvitation; onDone: () => void }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="rounded-md border border-rule bg-paper px-3.5 py-3" role="status">
      <p className="text-[13px] font-medium text-ink">
        {sent.emailed
          ? `Sent to ${sent.email} as ${sent.role}`
          : `Invitation ready for ${sent.email} as ${sent.role}`}
      </p>
      <p className="mt-1 max-w-[62ch] text-[12px] leading-5 text-muted">
        {sent.note ??
          "The message is on its way. The link below works too, and it is the only copy: it is not stored anywhere."}
      </p>
      <p className="mt-2 break-all font-mono text-[11.5px] text-ink">{sent.link}</p>
      <div className="mt-3 flex flex-wrap items-center gap-2">
        <Button
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(sent.link);
              setCopied(true);
            } catch {
              // Clipboard access can be refused, and the link is on the screen
              // either way, so this is a convenience rather than the mechanism.
              setCopied(false);
            }
          }}
        >
          {copied ? "Copied" : "Copy link"}
        </Button>
        <Button onClick={onDone}>Done</Button>
      </div>
    </div>
  );
}

/* -------------------------------------------------------------------------
 * Inviting
 * ---------------------------------------------------------------------- */

function Invite({ csrf, onSent }: { csrf: string; onSent: () => void }) {
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<(typeof ROLES)[number]>("member");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sent, setSent] = useState<SentInvitation | null>(null);

  if (sent) {
    return (
      <SentCard
        sent={sent}
        onDone={() => {
          setSent(null);
          setEmail("");
        }}
      />
    );
  }

  return (
    <form
      className="flex flex-wrap items-end gap-3"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError(null);
        try {
          setSent(
            await mutate<SentInvitation>(
              "invitations.create",
              { email: email.trim(), role },
              csrf,
            ),
          );
          onSent();
        } catch (err) {
          setError(saidWrong(err));
        } finally {
          setBusy(false);
        }
      }}
    >
      <div className="w-full max-w-[300px]">
        <Field label="Email address" error={error}>
          <input
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className={inputClass}
            placeholder="someone@yourcompany.com"
            autoComplete="off"
          />
        </Field>
      </div>
      <div>
        <Field label="Role">
          <select
            value={role}
            onChange={(e) => setRole(e.target.value as (typeof ROLES)[number])}
            className={`${selectClass} mt-1.5 h-9 w-full`}
          >
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
        </Field>
      </div>
      <Button type="submit" variant="primary" disabled={email.trim().length < 3} busy={busy}>
        Send invitation
      </Button>
    </form>
  );
}

/* -------------------------------------------------------------------------
 * Pending invitations
 * ---------------------------------------------------------------------- */

function Invitations({ csrf, mayManage }: { csrf: string; mayManage: boolean }) {
  const state = useApi<Invitation[]>(() => query("invitations.list"), []);
  const [busy, setBusy] = useState<string | null>(null);
  const [said, setSaid] = useState<{ tone: "ok" | "bad"; text: string } | null>(null);
  const [resent, setResent] = useState<SentInvitation | null>(null);
  const [revoking, setRevoking] = useState<Invitation | null>(null);
  const [gone, setGone] = useState<Set<string>>(new Set());

  return (
    <Card
      title="Invitations"
      note="People who have been asked to join and have not yet. A link expires after fourteen days."
      actions={mayManage ? undefined : null}
    >
      {mayManage ? (
        <div className="space-y-4 border-b border-rule px-4 py-4">
          <Invite csrf={csrf} onSent={state.reload} />
        </div>
      ) : null}

      <Loaded state={state} skeleton={<TableSkeleton rows={2} cols={4} />}>
        {(rows) => {
          const open = rows.filter((r) => r.state === "open" && !gone.has(r.id));
          const closed = rows.filter((r) => r.state !== "open" || gone.has(r.id)).slice(0, 10);
          if (open.length === 0 && closed.length === 0) {
            return (
              <Empty title="No invitations">
                Somebody who is not in your GitHub organization joins through an invitation. A
                finance person who needs the billing page, or a contractor, never appears on the
                Sync from GitHub list.
              </Empty>
            );
          }
          return (
            <>
              <TableWrap>
                <Table>
                  <thead>
                    <tr>
                      <Th>Address</Th>
                      <Th>Role</Th>
                      <Th>Invited by</Th>
                      <Th>Expires</Th>
                      <Th>
                        <span className="sr-only">Actions</span>
                      </Th>
                    </tr>
                  </thead>
                  <tbody>
                    {[...open, ...closed].map((row) => {
                      const isOpen = row.state === "open" && !gone.has(row.id);
                      return (
                        <Row key={row.id}>
                          <Td>
                            <span className="block truncate text-ink">{row.email}</span>
                            {!isOpen ? (
                              <Badge tone={toneFor(row.state === "accepted" ? "approved" : "revoked")}>
                                {gone.has(row.id) ? "revoked" : row.state}
                              </Badge>
                            ) : null}
                          </Td>
                          <Td label="Role">
                            <Badge>{row.role}</Badge>
                          </Td>
                          <Td label="Invited by">{row.invitedBy}</Td>
                          <Td label="Expires">
                            {/* Only while it can still be used. An accepted or
                                withdrawn invitation showing "in 14d" reads as
                                something that is still going to happen. */}
                            {isOpen ? <When value={row.expiresAt} /> : <span className="text-dim">--</span>}
                          </Td>
                          <Td label="Actions">
                            {mayManage && isOpen ? (
                              <span className="flex flex-wrap gap-2">
                                <Button
                                  busy={busy === `resend-${row.id}`}
                                  onClick={async () => {
                                    setBusy(`resend-${row.id}`);
                                    setSaid(null);
                                    try {
                                      setResent(
                                        await mutate<SentInvitation>(
                                          "invitations.resend",
                                          { id: row.id },
                                          csrf,
                                        ),
                                      );
                                      state.reload();
                                    } catch (err) {
                                      setSaid({ tone: "bad", text: saidWrong(err) });
                                    } finally {
                                      setBusy(null);
                                    }
                                  }}
                                >
                                  Send again
                                </Button>
                                <Button variant="danger" onClick={() => setRevoking(row)}>
                                  Withdraw
                                </Button>
                              </span>
                            ) : (
                              <span className="text-dim">--</span>
                            )}
                          </Td>
                        </Row>
                      );
                    })}
                  </tbody>
                </Table>
              </TableWrap>

              {resent ? (
                <div className="border-t border-rule px-4 py-3">
                  <SentCard sent={resent} onDone={() => setResent(null)} />
                </div>
              ) : null}
              {said ? (
                <div className="border-t border-rule px-4 py-3">
                  <p role="alert" className="text-[12px] leading-5 text-fail">
                    {said.text}
                  </p>
                </div>
              ) : null}
            </>
          );
        }}
      </Loaded>

      <Confirm
        open={revoking !== null}
        title="Withdraw this invitation?"
        confirmLabel="Withdraw it"
        busy={busy !== null}
        onCancel={() => setRevoking(null)}
        onConfirm={async () => {
          const row = revoking;
          if (!row) return;
          setBusy(`revoke-${row.id}`);
          setGone((g) => new Set(g).add(row.id));
          try {
            await mutate("invitations.revoke", { id: row.id }, csrf);
            state.reload();
          } catch (err) {
            setGone((g) => {
              const next = new Set(g);
              next.delete(row.id);
              return next;
            });
            setSaid({ tone: "bad", text: saidWrong(err) });
          } finally {
            setBusy(null);
            setRevoking(null);
          }
        }}
      >
        <p>
          The link sent to <span className="font-medium text-ink">{revoking?.email}</span> stops
          working. Nobody is removed: they were never here.
        </p>
      </Confirm>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * Members
 * ---------------------------------------------------------------------- */

function RolePicker({
  member,
  csrf,
  onChanged,
}: {
  member: Member;
  csrf: string;
  onChanged: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [role, setRole] = useState(member.role);

  return (
    <div className="flex items-center gap-2">
      <select
        aria-label={`Role for ${member.github_login}`}
        value={role}
        disabled={busy}
        onChange={async (e) => {
          const next = e.target.value;
          const previous = role;
          setBusy(true);
          setError(null);
          // Optimistic, and rolled back on failure. The refusal that actually
          // happens here is the last-owner guard, and a select that snaps back
          // beside the sentence explaining why is clearer than one that sits
          // still for a round trip and then changes.
          setRole(next);
          try {
            await mutate("members.setRole", { githubLogin: member.github_login, role: next }, csrf);
            onChanged();
          } catch (err) {
            setRole(previous);
            setError(saidWrong(err));
          } finally {
            setBusy(false);
          }
        }}
        className={selectClass}
      >
        {ROLES.map((r) => (
          <option key={r} value={r}>
            {r}
          </option>
        ))}
      </select>
      {error ? (
        <span role="alert" className="max-w-[34ch] text-[12px] leading-4 text-fail">
          {error}
        </span>
      ) : null}
    </div>
  );
}

/**
 * Reconciles the list against GitHub's.
 *
 * Sign-in can only ever speak for the person signing in: somebody added to the
 * GitHub organization is not here until they happen to sign in, and somebody
 * REMOVED from it keeps their role until they sign in again, which a person who
 * has been removed has no reason to do. This is the control that acts on
 * everybody at once.
 *
 * It reports what changed rather than just succeeding. "Synced" tells you
 * nothing; "removed 1" is the sentence somebody needs to see before they trust
 * a button that can remove people.
 */
function SyncFromGitHub({ csrf, onSynced }: { csrf: string; onSynced: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [report, setReport] = useState<SyncReport | null>(null);

  return (
    <div className="flex flex-wrap items-center justify-end gap-x-3 gap-y-1">
      {error ? (
        <span
          role="alert"
          className="min-w-0 max-w-[46ch] break-words text-left text-[12px] leading-4 text-fail sm:text-right"
        >
          {error}
        </span>
      ) : report ? (
        <span
          role="status"
          className="min-w-0 max-w-[46ch] break-words text-left text-[12px] leading-4 text-dim sm:text-right"
        >
          {describe(report)}
        </span>
      ) : null}
      <Button
        busy={busy}
        onClick={async () => {
          setBusy(true);
          setError(null);
          setReport(null);
          try {
            setReport(await mutate<SyncReport>("members.sync", {}, csrf));
            onSynced();
          } catch (err) {
            setError(saidWrong(err));
          } finally {
            setBusy(false);
          }
        }}
      >
        {busy ? "Syncing" : "Sync from GitHub"}
      </Button>
    </div>
  );
}

function describe(r: SyncReport): string {
  const parts: string[] = [];
  if (r.added.length) parts.push(`added ${r.added.length}`);
  if (r.removed.length) parts.push(`removed ${r.removed.length}`);
  if (r.changed.length) parts.push(`changed ${r.changed.length}`);
  return parts.length === 0 ? "Already matched GitHub." : `${parts.join(", ")}.`;
}

function Members() {
  const session = useSessionContext();
  const state = useApi<Member[]>(() => query("members.list"), []);
  const csrf = session.data?.csrfToken ?? "";
  const mayManage = may(session.data?.role, "members.manage");

  const [removing, setRemoving] = useState<Member | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [removed, setRemoved] = useState<Set<string>>(new Set());

  return (
    <Page
      title="Members"
      lede={
        mayManage
          ? "Who is in this organization and what each of them can do. Changing a role takes effect on their next request."
          : "Who is in this organization and what each of them can do. Changing a role needs owner or admin."
      }
    >
      <div className="space-y-6">
        <Card
          title="People"
          actions={mayManage ? <SyncFromGitHub csrf={csrf} onSynced={state.reload} /> : null}
        >
          <Loaded state={state} skeleton={<TableSkeleton rows={4} cols={5} />}>
            {(rows) => {
              const live = rows.filter((m) => !removed.has(m.github_login));
              return live.length === 0 ? (
                <Empty title="No members">
                  Membership follows a GitHub App installation, so this fills when the app reports
                  who belongs to the organization. Anybody else joins by invitation.
                </Empty>
              ) : (
                <TableWrap>
                  <Table>
                    <thead>
                      <tr>
                        <Th>Person</Th>
                        <Th>Role</Th>
                        <Th>Source</Th>
                        <Th>Joined</Th>
                        {mayManage ? (
                          <Th>
                            <span className="sr-only">Remove</span>
                          </Th>
                        ) : null}
                      </tr>
                    </thead>
                    <tbody>
                      {live.map((m) => (
                        <Row key={m.github_login}>
                          <Td>
                            <span className="flex items-center gap-2.5">
                              {m.avatar_url ? (
                                // eslint-disable-next-line @next/next/no-img-element
                                <img
                                  src={m.avatar_url}
                                  alt=""
                                  width={22}
                                  height={22}
                                  className="h-[22px] w-[22px] shrink-0 rounded-full border border-rule object-cover"
                                />
                              ) : (
                                <span className="grid h-[22px] w-[22px] shrink-0 place-items-center rounded-full border border-rule text-[10px] font-semibold uppercase text-muted">
                                  {m.github_login.slice(0, 1)}
                                </span>
                              )}
                              <span className="min-w-0">
                                <span className="block truncate text-ink">{m.github_login}</span>
                                {m.name ? (
                                  <span className="block truncate text-[12px] text-dim">
                                    {m.name}
                                  </span>
                                ) : null}
                              </span>
                            </span>
                          </Td>
                          <Td label="Role">
                            {mayManage ? (
                              <RolePicker member={m} csrf={csrf} onChanged={state.reload} />
                            ) : (
                              <Badge>{m.role}</Badge>
                            )}
                          </Td>
                          <Td label="Source">{m.source}</Td>
                          <Td label="Joined">
                            <When value={m.created_at} />
                          </Td>
                          {mayManage ? (
                            <Td label="Remove">
                              <Button variant="danger" onClick={() => setRemoving(m)}>
                                Remove
                              </Button>
                            </Td>
                          ) : null}
                        </Row>
                      ))}
                    </tbody>
                  </Table>
                </TableWrap>
              );
            }}
          </Loaded>
        </Card>

        <Invitations csrf={csrf} mayManage={mayManage} />
      </div>

      <Confirm
        open={removing !== null}
        title={`Remove ${removing?.github_login}?`}
        confirmLabel="Remove them"
        busy={busy}
        error={error}
        onCancel={() => {
          setRemoving(null);
          setError(null);
        }}
        onConfirm={async () => {
          const m = removing;
          if (!m) return;
          setBusy(true);
          setError(null);
          setRemoved((r) => new Set(r).add(m.github_login));
          try {
            await mutate("members.remove", { githubLogin: m.github_login }, csrf);
            setRemoving(null);
            state.reload();
          } catch (err) {
            setRemoved((r) => {
              const next = new Set(r);
              next.delete(m.github_login);
              return next;
            });
            setError(saidWrong(err));
          } finally {
            setBusy(false);
          }
        }}
      >
        <p>
          They lose access to this organization now, and every session they have signed in with
          stops working on its next request.
        </p>
        <p>
          Their account is not deleted and nothing they built is removed. If they are still in your
          GitHub organization, the next Sync from GitHub will add them back.
        </p>
      </Confirm>
    </Page>
  );
}

export default function MembersPage() {
  return <Members />;
}
