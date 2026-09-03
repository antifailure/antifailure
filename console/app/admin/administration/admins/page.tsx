"use client";

import { useState } from "react";
import {
  Badge,
  Button,
  Card,
  CardSkeleton,
  Confirm,
  Empty,
  Field,
  Loaded,
  Table,
  TableSkeleton,
  TableWrap,
  Td,
  Th,
  When,
  inputClass,
  selectClass,
} from "@/components/ui";
import { AdminPage, Drawer, Facts } from "@/components/admin/primitives";
import { ApiError } from "@/lib/api";
import { operatorMay, useAdminContext, useOperators, type Operator } from "@/lib/admin";
import {
  createOperator,
  restoreOperator,
  setOperatorRole,
  suspendOperator,
  useAdminCatalog,
  type AdminCatalog,
} from "@/lib/admin-administration";

/**
 * Who can reach this portal, what their role grants, and the catalog behind it.
 *
 * THE MOST SENSITIVE LIST IN THE PRODUCT. An operator account is cross-tenant
 * read of the entire customer base, so this page exists to make the answer to
 * "who has that" something somebody can look at rather than infer from a table
 * nobody reads.
 *
 * IT IS NO LONGER READ ONLY, and that is the substance of this change rather
 * than a feature on top of it. admin.operators.create, setRole, suspend and
 * restore have existed in the router since 0029. They are guarded, audited at
 * critical severity, and enforced by database triggers. Until this page they
 * had ZERO CALL SITES anywhere in the console, which is the exact shape of
 * failure this project keeps deleting: a capability that exists, passes every
 * test, and does nothing, because nothing reaches it.
 *
 * WHAT THE SERVER REFUSES, THIS PAGE DOES NOT PREDICT. Changing your own role
 * and suspending yourself are both refused server side; the root operator is
 * protected by a trigger in 0030 rather than by anything here. The controls are
 * shown and the refusal is rendered, because a button hidden by a guess about
 * the rules is a button that disappears the day the rules change, and nobody
 * finds out why.
 *
 * THERE IS NO CUSTOM ROLE TABLE. Roles are a compile-time constant, so the
 * matrix below is the one the server compiles with rather than data somebody
 * can edit. That is why this page reads it and never offers to change it: a
 * "create a role" button here would be a control with no route and no table
 * behind it.
 */
export default function AdministrationAdminsPage() {
  const { me } = useAdminContext();
  const mayWrite = operatorMay(me, "admin.operators.write");

  const operators = useOperators();
  const catalog = useAdminCatalog();

  const [open, setOpen] = useState<Operator | null>(null);
  const [creating, setCreating] = useState(false);
  const [search, setSearch] = useState("");

  // The drawer holds the row it was opened on, so a reload underneath it does
  // not swap the record out from under the reader. Re-read from the fresh list
  // by id so the panel still updates when a write lands.
  const selected = open ? (operators.data?.find((o) => o.id === open.id) ?? open) : null;

  return (
    <AdminPage
      href="/admin/administration/admins"
      actions={
        mayWrite ? (
          <Button variant="primary" onClick={() => setCreating(true)}>
            New operator
          </Button>
        ) : null
      }
    >
      <div className="grid gap-5">
        <Card
          title="Operator accounts"
          note="Everybody who can sign in to this portal. An operator account can read every tenant, so this list is the blast radius of the platform's own credentials."
        >
          <Loaded state={operators} skeleton={<TableSkeleton rows={4} cols={5} />}>
            {(all) => {
              const rows = filterOperators(all, search);
              return all.length === 0 ? (
                // Reachable in principle and alarming in practice: you are
                // reading this page, so at least one operator exists. Saying so
                // is more useful than an empty table that reads like a bug.
                <Empty title="No operator accounts">
                  This installation has no operator accounts, which cannot be true if you are
                  reading this page. Check that the portal is pointed at the database you expect.
                </Empty>
              ) : (
                <>
                  <OperatorSearch
                    value={search}
                    onChange={setSearch}
                    shown={rows.length}
                    total={all.length}
                  />
                  {rows.length === 0 ? (
                    <Empty title="No operator matches that">
                      Nothing in the {all.length} accounts on this installation matches
                      &ldquo;{search}&rdquo;. The search covers the name, the address and the role.
                    </Empty>
                  ) : (
                    <OperatorTable rows={rows} onOpen={setOpen} />
                  )}
                </>
              );
            }}
          </Loaded>
        </Card>

        <Card
          title="What each permission grants"
          note="The catalog the server compiles with. There is no custom role table, so this is the whole of it."
        >
          <Loaded state={catalog} skeleton={<CardSkeleton count={2} />}>
            {(data) => <PermissionCatalog catalog={data} />}
          </Loaded>
        </Card>
      </div>

      {selected ? (
        <OperatorDrawer
          operator={selected}
          me={me?.adminUserId ?? null}
          roles={catalog.data?.roles.map((r) => r.name) ?? []}
          mayWrite={mayWrite}
          onClose={() => setOpen(null)}
          onChanged={() => operators.reload()}
        />
      ) : null}

      {creating ? (
        <CreateOperator
          roles={catalog.data?.roles.map((r) => r.name) ?? []}
          onClose={() => setCreating(false)}
          onCreated={() => operators.reload()}
        />
      ) : null}
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * The directory
 * ---------------------------------------------------------------------- */

/**
 * Narrowing the directory, in the browser.
 *
 * FILTERING LOCALLY IS CORRECT HERE AND WOULD BE WRONG ANYWHERE ELSE IN THIS
 * PORTAL. admin.operators.list is not paged: it returns every operator account
 * and no cursor, so this component holds the complete list and a local filter
 * narrows all of it. Every other list here shows fifty rows out of thousands,
 * where filtering locally would narrow the page and present the result as the
 * whole answer, which is a confident wrong answer of exactly the kind that gets
 * acted on during an incident.
 *
 * The count beside the box says how many of how many, so it is never ambiguous
 * whether a short list is a filter or a small installation.
 */
function filterOperators(rows: Operator[], search: string): Operator[] {
  const needle = search.trim().toLowerCase();
  if (needle === "") return rows;
  return rows.filter((o) =>
    [o.name, o.email, o.role].some((field) => field.toLowerCase().includes(needle)),
  );
}

function OperatorSearch({
  value,
  onChange,
  shown,
  total,
}: {
  value: string;
  onChange: (next: string) => void;
  shown: number;
  total: number;
}) {
  return (
    <div className="flex flex-wrap items-end gap-3 border-b border-rule px-4 py-3">
      <div className="min-w-0 flex-1 basis-[16rem]">
        <Field label="Find an operator">
          <input
            className={inputClass}
            type="search"
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder="Name, address or role"
          />
        </Field>
      </div>
      <p className="basis-full text-[12px] leading-5 text-dim sm:basis-auto sm:pb-2.5">
        {value.trim() === ""
          ? `${total} ${total === 1 ? "account" : "accounts"}`
          : `${shown} of ${total} accounts`}
      </p>
    </div>
  );
}

function OperatorTable({
  rows,
  onOpen,
}: {
  rows: Operator[];
  onOpen: (o: Operator) => void;
}) {
  return (
    <TableWrap>
      <Table>
        <thead>
          <tr>
            <Th>Operator</Th>
            <Th>Role</Th>
            <Th>Can sign in</Th>
            <Th>Last signed in</Th>
            <Th>State</Th>
          </tr>
        </thead>
        <tbody>
          {rows.map((o) => (
            <tr key={o.id}>
              <Td>
                {/* A real button, not a clickable row. One focusable,
                    announced, Enter-activated target per row, and a row that is
                    only clickable is invisible to a keyboard. */}
                <button
                  type="button"
                  onClick={() => onOpen(o)}
                  className="-mx-1 -my-2 block min-h-11 w-full px-1 py-2 text-left underline decoration-transparent underline-offset-4 hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
                >
                  <span className="block truncate font-medium text-ink">{o.name}</span>
                  <span className="block truncate text-[12px] text-muted">{o.email}</span>
                </button>
              </Td>
              <Td label="Role">
                {/* Underscores are a database convention and not a word. The
                    role reads as English here and the value is unchanged
                    underneath. */}
                {o.role.replace(/_/g, " ")}
                {o.isRoot ? (
                  <span className="mt-1 block text-[12px] text-muted">
                    The root operator, which cannot be deleted, demoted or suspended
                  </span>
                ) : null}
              </Td>
              <Td label="Can sign in">
                {o.provisioned ? "Yes" : <span className="text-muted">Not provisioned</span>}
              </Td>
              <Td label="Last signed in">
                {o.lastSignedInAt ? (
                  <When value={o.lastSignedInAt} />
                ) : (
                  <span className="text-muted">Never</span>
                )}
              </Td>
              <Td label="State">
                {o.suspended ? (
                  <Badge tone="fail">suspended</Badge>
                ) : (
                  <Badge tone="pass">active</Badge>
                )}
              </Td>
            </tr>
          ))}
        </tbody>
      </Table>
    </TableWrap>
  );
}

/* -------------------------------------------------------------------------
 * One operator, and the four things that can be done to them
 * ---------------------------------------------------------------------- */

function OperatorDrawer({
  operator,
  me,
  roles,
  mayWrite,
  onClose,
  onChanged,
}: {
  operator: Operator;
  me: string | null;
  roles: string[];
  mayWrite: boolean;
  onClose: () => void;
  onChanged: () => void;
}) {
  const [role, setRole] = useState(operator.role);
  const [busy, setBusy] = useState<null | "role" | "suspend" | "restore">(null);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const [confirming, setConfirming] = useState<null | "suspend" | "restore">(null);
  const [reason, setReason] = useState("");

  const isSelf = me !== null && me === operator.id;

  async function run(kind: "role" | "suspend" | "restore", fn: () => Promise<unknown>) {
    setBusy(kind);
    setError(null);
    setDone(null);
    try {
      await fn();
      setConfirming(null);
      setReason("");
      setDone(
        kind === "role"
          ? `Role changed to ${role.replace(/_/g, " ")}.`
          : kind === "suspend"
            ? "Suspended. Their sessions stop resolving on the next request."
            : "Restored. Sessions that had already expired do not come back.",
      );
      onChanged();
    } catch (e) {
      // The server's own words. Every refusal on these routes says what it
      // refused and why, and paraphrasing it here would lose the reason.
      setError(e instanceof ApiError ? e.message : "That did not work.");
    } finally {
      setBusy(null);
    }
  }

  return (
    <>
      <Drawer open title={operator.name} onClose={onClose}>
        <Facts
          facts={[
            { label: "Email", value: operator.email },
            { label: "Role", value: operator.role.replace(/_/g, " ") },
            {
              label: "Can sign in",
              value: operator.provisioned
                ? "Yes"
                : "No. A password has never been set, so this account cannot be signed in to.",
            },
            {
              label: "Last signed in",
              value: operator.lastSignedInAt ? <When value={operator.lastSignedInAt} /> : "Never",
            },
            {
              label: "State",
              value: operator.suspended ? "Suspended" : "Active",
            },
            {
              label: "Root",
              value: operator.isRoot
                ? "Yes. The database refuses to demote, suspend or delete this row."
                : "No",
            },
            { label: "Identifier", value: operator.id, mono: true },
          ]}
        />

        {!mayWrite ? (
          <p className="border-t border-rule px-4 py-4 text-[13px] leading-6 text-muted">
            Your role can read this list and not change it. Only owner and super admin hold
            admin.operators.write, because granting an operator account is granting cross-tenant
            read of the entire customer base.
          </p>
        ) : (
          <div className="border-t border-rule px-4 py-4">
            <Field
              label="Role"
              hint={
                isSelf
                  ? "The server refuses to let an operator change their own role. Ask another owner."
                  : operator.isRoot
                    ? "A database trigger refuses to demote the root operator."
                    : "Takes effect on their next request."
              }
            >
              <select
                className={selectClass}
                value={role}
                onChange={(e) => setRole(e.target.value)}
              >
                {(roles.length > 0 ? roles : [operator.role]).map((r) => (
                  <option key={r} value={r}>
                    {r.replace(/_/g, " ")}
                  </option>
                ))}
              </select>
            </Field>

            <div className="mt-3 flex flex-wrap gap-2">
              <Button
                variant="primary"
                disabled={role === operator.role}
                busy={busy === "role"}
                onClick={() => run("role", () => setOperatorRole(operator.id, role))}
              >
                Change role
              </Button>
              {operator.suspended ? (
                <Button busy={busy === "restore"} onClick={() => setConfirming("restore")}>
                  Restore
                </Button>
              ) : (
                <Button variant="danger" onClick={() => setConfirming("suspend")}>
                  Suspend
                </Button>
              )}
            </div>

            {error ? (
              <p role="alert" className="mt-3 text-[13px] leading-6 text-fail">
                {error}
              </p>
            ) : null}
            {done ? (
              <p role="status" className="mt-3 text-[13px] leading-6 text-pass">
                {done}
              </p>
            ) : null}
          </div>
        )}
      </Drawer>

      <Confirm
        open={confirming === "suspend"}
        title={`Suspend ${operator.name}`}
        // The account's own address, not the word "suspend". Typing a fixed
        // word proves somebody read a dialog; typing the thing's own name
        // proves they read WHICH thing.
        phrase={operator.email}
        confirmLabel="Suspend this operator"
        busy={busy === "suspend"}
        error={error}
        onCancel={() => {
          setConfirming(null);
          setError(null);
        }}
        onConfirm={() => run("suspend", () => suspendOperator(operator.id, reason))}
      >
        <p className="text-[13px] leading-6 text-muted">
          They stop being able to sign in, and their existing operator sessions stop resolving on
          the next request. This is recorded in the platform audit chain at critical severity.
        </p>
        <div className="mt-3">
          <Field label="Reason" hint="Recorded with the action. The next person on call reads it.">
            <input
              className={inputClass}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="Left the company"
            />
          </Field>
        </div>
      </Confirm>

      <Confirm
        open={confirming === "restore"}
        title={`Restore ${operator.name}`}
        confirmLabel="Restore this operator"
        busy={busy === "restore"}
        error={error}
        onCancel={() => {
          setConfirming(null);
          setError(null);
        }}
        onConfirm={() => run("restore", () => restoreOperator(operator.id))}
      >
        <p className="text-[13px] leading-6 text-muted">
          They can sign in again, if a password was ever set. Sessions that expired while they were
          suspended do not come back.
        </p>
      </Confirm>
    </>
  );
}

/* -------------------------------------------------------------------------
 * Creating one
 * ---------------------------------------------------------------------- */

function CreateOperator({
  roles,
  onClose,
  onCreated,
}: {
  roles: string[];
  onClose: () => void;
  onCreated: () => void;
}) {
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState("read_only");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [effect, setEffect] = useState<string | null>(null);

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      const result = await createOperator({ email: email.trim(), name: name.trim(), role });
      // The server's own sentence about what it did, which says the account is
      // unusable until somebody provisions a password out of band. Replacing it
      // with "Created" here would hide the one thing the reader has to do next.
      setEffect(result.effect);
      onCreated();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "That did not work.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Drawer open title="New operator" onClose={onClose}>
      {effect ? (
        <div className="px-4 py-4">
          <p role="status" className="text-[13px] leading-6 text-ink">
            {effect}
          </p>
          <p className="mt-3 text-[13px] leading-6 text-muted">
            The row lands with no password, so it cannot be signed in to. There is no default
            credential anywhere in this product: somebody has to set one out of band before this
            account works.
          </p>
          <div className="mt-4">
            <Button variant="primary" onClick={onClose}>
              Done
            </Button>
          </div>
        </div>
      ) : (
        <form
          className="px-4 py-4"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <div className="grid gap-3">
            <Field label="Email" hint="What the audit chain records, because a name is not unique.">
              <input
                className={inputClass}
                type="email"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
              />
            </Field>
            <Field label="Name">
              <input
                className={inputClass}
                required
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            <Field
              label="Role"
              hint="Start at the least this person needs. Changing it later is one action and is recorded."
            >
              <select className={selectClass} value={role} onChange={(e) => setRole(e.target.value)}>
                {(roles.length > 0 ? roles : [role]).map((r) => (
                  <option key={r} value={r}>
                    {r.replace(/_/g, " ")}
                  </option>
                ))}
              </select>
            </Field>
          </div>

          {error ? (
            <p role="alert" className="mt-3 text-[13px] leading-6 text-fail">
              {error}
            </p>
          ) : null}

          <div className="mt-4 flex flex-wrap gap-2">
            <Button type="submit" variant="primary" busy={busy}>
              Create operator
            </Button>
            <Button onClick={onClose}>Cancel</Button>
          </div>
        </form>
      )}
    </Drawer>
  );
}

/* -------------------------------------------------------------------------
 * The catalog
 * ---------------------------------------------------------------------- */

/**
 * Every permission, what it means, and which roles hold it.
 *
 * A LIST RATHER THAN A GRID, and that is a readability decision rather than a
 * shortcut. Twenty two permissions against eight roles is a 176 cell matrix: it
 * needs horizontal scrolling on a laptop, it is unreadable on a phone at any
 * transformation, and the question somebody actually arrives with is "who can
 * do this" or "what can this role do", neither of which needs the full cross
 * product on screen at once. So each permission is a row carrying the sentence
 * an auditor reads and the roles that hold it, and the filter answers the
 * second question by narrowing to one role.
 */
function PermissionCatalog({ catalog }: { catalog: AdminCatalog }) {
  const [role, setRole] = useState("");

  const shown = role
    ? catalog.permissions.filter((p) => p.roles.includes(role))
    : catalog.permissions;

  return (
    <>
      <div className="flex flex-wrap items-end gap-3 border-b border-rule px-4 py-3">
        <div className="min-w-0 flex-1 basis-[14rem]">
          <Field label="Show what one role grants">
            <select className={selectClass} value={role} onChange={(e) => setRole(e.target.value)}>
              <option value="">Every permission</option>
              {catalog.roles.map((r) => (
                <option key={r.name} value={r.name}>
                  {r.name.replace(/_/g, " ")}
                </option>
              ))}
            </select>
          </Field>
        </div>
        <p className="basis-full text-[12px] leading-5 text-dim sm:basis-auto sm:pb-2.5">
          {role
            ? `${shown.length} of ${catalog.permissions.length} permissions are held by ${role.replace(/_/g, " ")}.`
            : `${catalog.permissions.length} permissions across ${catalog.roles.length} roles.`}
        </p>
      </div>

      {shown.length === 0 ? (
        <Empty title="That role holds nothing">
          Every built in role holds at least admin.portal.access, so a role with no permissions
          means the catalog and the role table disagree. That is a bug rather than a
          configuration.
        </Empty>
      ) : (
        <ul>
          {shown.map((p) => (
            <li key={p.name} className="border-b border-rule px-4 py-3.5 last:border-b-0">
              <p className="font-mono text-[12px] text-ink">{p.name}</p>
              <p className="mt-1 max-w-[72ch] text-[13px] leading-6 text-muted">{p.description}</p>
              <ul className="mt-2 flex flex-wrap gap-1.5">
                {p.roles.map((r) => (
                  <li key={r}>
                    <Badge tone={r === role ? "pass" : "neutral"}>{r.replace(/_/g, " ")}</Badge>
                  </li>
                ))}
              </ul>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}
