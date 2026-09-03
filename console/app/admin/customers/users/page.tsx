"use client";

import { useState } from "react";
import {
  Badge,
  Button,
  Card,
  Confirm,
  Field,
  Loaded,
  TableSkeleton,
  When,
  inputClass,
} from "@/components/ui";
import {
  AdminPage,
  DataTable,
  Drawer,
  EmptyList,
  Facts,
  FilterBar,
  type Column,
} from "@/components/admin/primitives";
import { More } from "@/components/pagination";
import { operatorMay, useAdminContext, useTenants, type Tenant } from "@/lib/admin";
import {
  restoreUser,
  revokeUserSession,
  suspendUser,
  useAdminUsers,
  useUserSessions,
  type AdminUserRow,
} from "@/lib/admin-customers";
import type { ApiError } from "@/lib/api";

/**
 * Every organization and every account on the installation.
 *
 * WHAT AN OPERATOR OPENS THIS TO ANSWER: somebody named an account, and the
 * first thing anybody needs is the row. Everything else in the portal is
 * reached from here, which is why it is one screen with two lists rather than
 * two screens: support work does not know in advance whether the thing it was
 * given is a company or a person, and it is frequently an email address that
 * turns out to be both.
 *
 * ONE SELECT RATHER THAN TABS. The two lists answer the same question about
 * different tables, they are the same shape, and the console already has a
 * labelled filter control that is keyboard reachable and reflows on a phone.
 * A tablist would be a second interaction pattern in a console that has none.
 */

type View = "organizations" | "people";

export default function CustomersUsersPage() {
  const [view, setView] = useState<View>("organizations");
  // Held per list rather than shared. Switching from an organization search to
  // the people list and carrying the word over means the reader is shown an
  // empty table and no reason for it.
  const [orgSearch, setOrgSearch] = useState("");
  const [personSearch, setPersonSearch] = useState("");

  return (
    <AdminPage
      href="/admin/customers/users"
      lede="Every organization and every account on this installation. Counts are live rather than billed figures."
    >
      {view === "organizations" ? (
        <Organizations search={orgSearch} setSearch={setOrgSearch} view={view} setView={setView} />
      ) : (
        <People search={personSearch} setSearch={setPersonSearch} view={view} setView={setView} />
      )}
    </AdminPage>
  );
}

/** The control that swaps the two lists, rendered by both so it sits in the
 *  same place in the same card whichever one is showing. */
function viewFilter(view: View, setView: (v: View) => void) {
  return [
    {
      label: "Showing",
      value: view,
      onChange: (next: string) => setView(next as View),
      options: [
        { value: "organizations", label: "Organizations" },
        { value: "people", label: "People" },
      ],
    },
  ];
}

/* -------------------------------------------------------------------------
 * Organizations
 * ---------------------------------------------------------------------- */

function Organizations({
  search,
  setSearch,
  view,
  setView,
}: {
  search: string;
  setSearch: (s: string) => void;
  view: View;
  setView: (v: View) => void;
}) {
  const state = useTenants(search);

  const columns: Column<Tenant>[] = [
    {
      key: "name",
      header: "Organization",
      cell: (t) => (
        <>
          <span className="block truncate font-medium text-ink">{t.name}</span>
          <span className="block truncate font-mono text-[12px] text-muted">{t.slug}</span>
        </>
      ),
    },
    { key: "plan", header: "Plan", cell: (t) => t.plan },
    { key: "members", header: "Members", numeric: true, cell: (t) => t.members.toLocaleString() },
    {
      key: "environments",
      header: "Environments",
      numeric: true,
      cell: (t) => t.environments.toLocaleString(),
    },
    { key: "created", header: "Created", cell: (t) => <When value={t.createdAt} /> },
    {
      key: "state",
      header: "State",
      cell: (t) => (
        <>
          {t.suspended ? <Badge tone="fail">suspended</Badge> : <Badge tone="pass">active</Badge>}
          {t.suspendedReason ? (
            // break-words because a reason is whatever an operator pasted, and
            // one unbreakable token here widens the whole table rather than
            // this cell.
            <span className="mt-1 block max-w-[36ch] break-words text-[12px] text-muted">
              {t.suspendedReason}
            </span>
          ) : null}
        </>
      ),
    },
  ];

  return (
    <Card>
      <FilterBar
        search={{
          value: search,
          onChange: setSearch,
          label: "Search organizations by name or slug",
          placeholder: "Name or slug",
        }}
        filters={viewFilter(view, setView)}
      />
      <Loaded state={state} skeleton={<TableSkeleton rows={6} cols={6} />}>
        {(rows) => (
          <DataTable
            columns={columns}
            rows={rows}
            keyOf={(t) => t.id}
            href={(t) => `/admin/customers/users/organization?org=${encodeURIComponent(t.slug)}`}
            empty={
              <EmptyList
                title={search ? "No organization matches that" : "No organizations yet"}
              >
                {search
                  ? "Nothing on this installation has that name or slug. Clear the search to see every organization."
                  : "Nobody has created an organization here. The first sign-in that creates one shows up in this table."}
              </EmptyList>
            }
            footer={
              <More
                shown={rows.length}
                noun={{ one: "organization", many: "organizations" }}
                hasMore={state.hasMore}
                busy={state.busy}
                error={state.moreError}
                onMore={state.more}
              />
            }
          />
        )}
      </Loaded>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * People
 * ---------------------------------------------------------------------- */

function People({
  search,
  setSearch,
  view,
  setView,
}: {
  search: string;
  setSearch: (s: string) => void;
  view: View;
  setView: (v: View) => void;
}) {
  const state = useAdminUsers(search);
  // The open record, held here rather than in the drawer, because the list has
  // to keep rendering underneath it and the drawer has to survive a reload of
  // the list it came from.
  const [open, setOpen] = useState<AdminUserRow | null>(null);

  const columns: Column<AdminUserRow>[] = [
    {
      key: "login",
      header: "Account",
      cell: (u) => (
        <>
          <span className="block truncate font-medium text-ink">{u.name || u.githubLogin}</span>
          <span className="block truncate font-mono text-[12px] text-muted">{u.githubLogin}</span>
        </>
      ),
    },
    { key: "email", header: "Email", cell: (u) => <span className="break-all">{u.email}</span> },
    {
      key: "orgs",
      header: "Organizations",
      numeric: true,
      cell: (u) => u.organizations.toLocaleString(),
    },
    { key: "created", header: "Created", cell: (u) => <When value={u.createdAt} /> },
    {
      key: "state",
      header: "State",
      cell: (u) => (
        <>
          {u.suspended ? <Badge tone="fail">suspended</Badge> : <Badge tone="pass">active</Badge>}
          {u.suspendedReason ? (
            <span className="mt-1 block max-w-[36ch] break-words text-[12px] text-muted">
              {u.suspendedReason}
            </span>
          ) : null}
        </>
      ),
    },
    {
      key: "open",
      header: "Sessions",
      cell: (u) => (
        // A real button rather than a clickable row: a row that opens something
        // on click has no keyboard equivalent and is announced as nothing.
        <Button onClick={() => setOpen(u)}>Open</Button>
      ),
    },
  ];

  return (
    <>
      <Card>
        <FilterBar
          search={{
            value: search,
            onChange: setSearch,
            label: "Search accounts by login or email",
            placeholder: "Login or email",
          }}
          filters={viewFilter(view, setView)}
        />
        <Loaded state={state} skeleton={<TableSkeleton rows={6} cols={6} />}>
          {(rows) => (
            <DataTable
              columns={columns}
              rows={rows}
              keyOf={(u) => u.id}
              empty={
                <EmptyList title={search ? "No account matches that" : "Nobody has signed in yet"}>
                  {search
                    ? "No account on this installation has that login or email address. Clear the search to see every account."
                    : "No account exists here yet. The first sign-in creates one and it shows up in this table."}
                </EmptyList>
              }
              footer={
                <More
                  shown={rows.length}
                  noun={{ one: "account", many: "accounts" }}
                  hasMore={state.hasMore}
                  busy={state.busy}
                  error={state.moreError}
                  onMore={state.more}
                />
              }
            />
          )}
        </Loaded>
      </Card>

      <PersonDrawer person={open} onClose={() => setOpen(null)} onChanged={state.reload} />
    </>
  );
}

/* -------------------------------------------------------------------------
 * One person, beside the list they came from
 * ---------------------------------------------------------------------- */

function PersonDrawer({
  person,
  onClose,
  onChanged,
}: {
  person: AdminUserRow | null;
  onClose: () => void;
  onChanged: () => void;
}) {
  const { me } = useAdminContext();
  const sessions = useUserSessions(person?.id ?? null);
  const [asking, setAsking] = useState<"suspend" | null>(null);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [effect, setEffect] = useState<string | null>(null);

  async function run(fn: () => Promise<unknown>) {
    setBusy(true);
    setError(null);
    try {
      await fn();
      setAsking(null);
      setReason("");
      sessions.reload();
      onChanged();
    } catch (err) {
      setError((err as ApiError).message);
    } finally {
      setBusy(false);
    }
  }

  const mayWrite = operatorMay(me, "admin.users.write");
  const mayRevoke = operatorMay(me, "admin.sessions.revoke");

  return (
    <Drawer
      open={person !== null}
      title={person ? person.name || person.githubLogin : "Account"}
      onClose={onClose}
      actions={
        person && mayWrite ? (
          person.suspended ? (
            <Button
              variant="primary"
              busy={busy}
              onClick={() => void run(() => restoreUser(person.id))}
            >
              Restore this account
            </Button>
          ) : (
            <Button variant="danger" onClick={() => setAsking("suspend")}>
              Suspend this account
            </Button>
          )
        ) : null
      }
    >
      {person ? (
        <>
          <Facts
            facts={[
              { label: "GitHub login", value: person.githubLogin, mono: true },
              { label: "Email", value: person.email },
              { label: "Name", value: person.name },
              { label: "Organizations", value: person.organizations.toLocaleString() },
              { label: "Created", value: <When value={person.createdAt} /> },
              {
                label: "State",
                value: person.suspended ? (
                  <Badge tone="fail">suspended</Badge>
                ) : (
                  <Badge tone="pass">active</Badge>
                ),
              },
              { label: "Suspended because", value: person.suspendedReason },
            ]}
          />

          {effect ? (
            <p role="status" className="px-4 pb-3 text-[12.5px] leading-5 text-muted">
              {effect}
            </p>
          ) : null}
          {error && asking === null ? (
            <p role="alert" className="px-4 pb-3 text-[12.5px] leading-5 text-fail">
              {error}
            </p>
          ) : null}

          <h3 className="border-y border-rule bg-paper px-4 py-2 text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
            Sessions
          </h3>
          <Loaded state={sessions} skeleton={<TableSkeleton rows={3} cols={2} />}>
            {(rows) =>
              rows.length === 0 ? (
                <EmptyList title="No sessions">
                  This account has not signed in, or every session it held has been swept. Sessions
                  are removed once they have expired.
                </EmptyList>
              ) : (
                <ul className="divide-y divide-rule">
                  {rows.map((s) => (
                    <li key={s.id} className="px-4 py-3">
                      <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                        <span className="text-[13px] text-ink">
                          {s.orgSlug ?? "No organization"}
                        </span>
                        {s.revoked ? (
                          <Badge tone="neutral">signed out</Badge>
                        ) : (
                          <Badge tone="pass">live</Badge>
                        )}
                      </div>
                      <p className="mt-1 text-[12px] leading-5 text-muted">
                        Last seen <When value={s.lastSeenAt} />, expires{" "}
                        <When value={s.expiresAt} />
                      </p>
                      <p className="mt-0.5 break-all font-mono text-[11.5px] leading-5 text-dim">
                        {s.ip ?? "no address"} {s.userAgent ? `· ${s.userAgent}` : ""}
                      </p>
                      {!s.revoked && mayRevoke ? (
                        <div className="mt-2">
                          <Button
                            busy={busy}
                            onClick={() =>
                              void run(async () => {
                                await revokeUserSession(
                                  s.id,
                                  `Revoked from the operator portal while reviewing ${person.githubLogin}.`,
                                );
                              })
                            }
                          >
                            Sign this session out
                          </Button>
                        </div>
                      ) : null}
                    </li>
                  ))}
                </ul>
              )
            }
          </Loaded>

          <Confirm
            open={asking === "suspend"}
            title={`Suspend ${person.githubLogin}?`}
            confirmLabel="Suspend"
            busy={busy}
            error={error}
            onCancel={() => {
              setAsking(null);
              setError(null);
            }}
            onConfirm={() =>
              void run(async () => {
                const result = await suspendUser(person.id, reason);
                // The route's own sentence rather than this page's. Two
                // sentences that mean the same thing today are two sentences
                // that disagree after somebody edits one.
                setEffect(result.effect);
              })
            }
          >
            <p className="text-[13px] leading-6 text-muted">
              <strong className="font-medium text-ink">What this does:</strong> every session this
              account holds stops working on its next request, and it cannot sign in again until
              restored. This is not the same as suspending an organization, which only stops new
              work.
            </p>
            <div className="mt-4">
              <Field
                label="Reason"
                hint="Recorded in the operator audit chain. Required."
              >
                <input
                  className={inputClass}
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                  maxLength={500}
                  required
                />
              </Field>
            </div>
          </Confirm>
        </>
      ) : null}
    </Drawer>
  );
}
