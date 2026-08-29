"use client";

import { useState } from "react";
import { ago, when } from "@/lib/format";
import { mutate, query, useApi } from "@/lib/api";
import { useSessionContext } from "@/components/session";
import {
  Badge,
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
} from "@/components/ui";

interface Member {
  github_login: string;
  name: string | null;
  avatar_url: string | null;
  role: string;
  source: string;
  created_at: string;
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
        className="h-8 rounded-[5px] border border-rule bg-card px-2 text-[12.5px] text-ink outline-none focus:border-rule-strong disabled:opacity-60"
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
      <Card title="People">
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
                        <Td>
                          {mayManage ? (
                            <RolePicker member={m} csrf={csrf} onChanged={state.reload} />
                          ) : (
                            <Badge>{m.role}</Badge>
                          )}
                        </Td>
                        <Td>{m.source}</Td>
                        <Td>
                          <span title={when(m.created_at)}>{ago(m.created_at)}</span>
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
