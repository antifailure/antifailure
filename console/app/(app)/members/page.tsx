"use client";

import { useState } from "react";
import { mutate, query, useApi } from "@/lib/api";
import { useSessionContext } from "@/components/session";
import {
  Badge,
  Button,
  Card,
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
  selectClass,
} from "@/components/ui";

interface Member {
  github_login: string;
  name: string | null;
  avatar_url: string | null;
  role: string;
  source: string;
  created_at: string;
}

interface SyncReport {
  added: string[];
  removed: string[];
  changed: { login: string; from: string; to: string }[];
}

const ROLES = ["owner", "admin", "member", "viewer"] as const;
const MAY_MANAGE = new Set(["owner", "admin"]);

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

  return (
    <div className="flex items-center gap-2">
      <select
        aria-label={`Role for ${member.github_login}`}
        value={member.role}
        disabled={busy}
        onChange={async (e) => {
          const role = e.target.value;
          setBusy(true);
          setError(null);
          try {
            await mutate("members.setRole", { githubLogin: member.github_login, role }, csrf);
            onChanged();
          } catch (err) {
            setError(err instanceof Error ? err.message : "That did not work.");
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
 * everybody at once, and the only one that takes access away.
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
    // Outcome first, button last, so the button stays pinned to the right edge
    // of the header whatever length the message turns out to be.
    <div className="flex flex-wrap items-center justify-end gap-x-3 gap-y-1">
      {error ? (
        <span role="alert" className="min-w-0 max-w-[46ch] break-words text-left text-[12px] leading-4 text-fail sm:text-right">
          {error}
        </span>
      ) : report ? (
        <span role="status" className="min-w-0 max-w-[46ch] break-words text-left text-[12px] leading-4 text-dim sm:text-right">
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
            setError(err instanceof Error ? err.message : "That did not work.");
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
  const mayManage = MAY_MANAGE.has(session.data?.role ?? "");

  return (
    <Page
      title="Members"
      lede={
        mayManage
          ? "Who is in this organization and what each of them can do. Changing a role takes effect on their next request."
          : "Who is in this organization and what each of them can do. Changing a role needs owner or admin."
      }
    >
      <Card
        title="People"
        actions={
          mayManage ? <SyncFromGitHub csrf={csrf} onSynced={state.reload} /> : null
        }
      >
        <Loaded state={state} skeleton={<TableSkeleton rows={4} cols={4} />}>
          {(rows) =>
            rows.length === 0 ? (
              <Empty title="No members">
                Membership follows a GitHub App installation, so this fills when
                the app reports who belongs to the organization.
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
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((m) => (
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
                                <span className="block truncate text-[12px] text-dim">{m.name}</span>
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

export default function MembersPage() {
  return <Members />;
}
