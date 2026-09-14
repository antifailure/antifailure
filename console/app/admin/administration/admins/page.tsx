"use client";

import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import {
  Badge,
  Button,
  Card,
  CardSkeleton,
  Confirm,
  CopyButton,
  Field,
  Loaded,
  TableSkeleton,
  When,
  inputClass,
  selectClass,
} from "@/components/ui";
import {
  AdminPage,
  DataTable,
  Drawer,
  EmptyList,
  Facts,
  StatusChip,
  type Column,
} from "@/components/admin/primitives";
import { ApiError } from "@/lib/api";
import { operatorMay, useAdminContext, useOperators, type Operator } from "@/lib/admin";
import {
  createOperator,
  restoreOperator,
  setOperatorPassword,
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
  // useSearchParams needs a Suspense boundary under `output: "export"`, the
  // same way the organization detail page wraps itself. Without it the build
  // refuses the route rather than failing at runtime.
  return (
    <Suspense
      fallback={
        <AdminPage href="/admin/administration/admins">
          <Card>
            <TableSkeleton rows={4} cols={5} />
          </Card>
        </AdminPage>
      }
    >
      <Admins />
    </Suspense>
  );
}

function Admins() {
  const { me } = useAdminContext();
  const mayWrite = operatorMay(me, "admin.operators.write");

  const operators = useOperators();
  const catalog = useAdminCatalog();

  const [creating, setCreating] = useState(false);
  const [search, setSearch] = useState("");

  /*
   * THE OPEN RECORD IS IN THE URL, NOT IN STATE.
   *
   * The console is a static export, so there are no dynamic segments and a
   * detail view is `?id=<id>`. Putting it in the query string rather than in
   * `useState` is what makes the panel linkable: an operator investigating an
   * account can send the address to somebody else, and the back button closes
   * the panel instead of leaving the page. `replace` rather than `push`, so
   * opening and closing three accounts does not bury the previous page under
   * three history entries.
   */
  const params = useSearchParams();
  const router = useRouter();
  const openId = params.get("id");
  const selected = openId ? (operators.data?.find((o) => o.id === openId) ?? null) : null;
  const close = () => router.replace("/admin/administration/admins");

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
                <EmptyList title="No operator accounts">
                  This installation has no operator accounts, which cannot be true if you are
                  reading this page. Check that the portal is pointed at the database you expect.
                </EmptyList>
              ) : (
                <>
                  <OperatorSearch
                    value={search}
                    onChange={setSearch}
                    shown={rows.length}
                    total={all.length}
                  />
                  <OperatorTable rows={rows} />
                </>
              );
            }}
          </Loaded>
        </Card>

        <Card
          title="What each permission grants"
          note="The catalog the server compiles with. An operator role is one of these four and nothing else: the custom roles a CUSTOMER can define are a tenant feature and reach no operator permission."
        >
          <Loaded state={catalog} skeleton={<CardSkeleton count={2} />}>
            {(data) => <PermissionCatalog catalog={data} />}
          </Loaded>
        </Card>
      </div>

      {selected ? (
        <OperatorDrawer
          // Keyed by the account, so changing `?id=` swaps the panel rather
          // than reusing it. The role select and the confirmation state are
          // component state, and without this a URL that moved from one
          // operator to another would show the previous one's role beside the
          // new one's name.
          key={selected.id}
          operator={selected}
          me={me?.adminUserId ?? null}
          roles={catalog.data?.roles.map((r) => r.name) ?? []}
          mayWrite={mayWrite}
          onClose={close}
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

/**
 * The directory, built out of the portal's own DataTable.
 *
 * `href` rather than a click handler, which is what gives each row one
 * focusable, announced, Enter-activated target. A row that is only clickable is
 * invisible to a keyboard, and a button here would open a panel nobody could
 * link to.
 *
 * No `onSort`, deliberately. admin.operators.list takes no ordering argument,
 * so a sortable header would have to reorder the rows in the browser, and
 * DataTable refuses to offer a sort it cannot actually perform.
 */
function OperatorTable({ rows }: { rows: Operator[] }) {
  const columns: Column<Operator>[] = [
    {
      key: "operator",
      header: "Operator",
      cell: (o) => (
        <span className="min-w-0">
          <span className="block truncate font-medium text-ink">{o.name}</span>
          <span className="block truncate text-[12px] text-muted">{o.email}</span>
        </span>
      ),
    },
    {
      key: "role",
      header: "Role",
      cell: (o) => (
        <>
          {/* Underscores are a database convention and not a word. The role
              reads as English here and the value is unchanged underneath. */}
          {o.role.replace(/_/g, " ")}
          {o.isRoot ? (
            <span className="mt-1 block text-[12px] text-muted">
              The root operator, which cannot be deleted, demoted or suspended
            </span>
          ) : null}
        </>
      ),
    },
    {
      key: "signin",
      header: "Can sign in",
      cell: (o) => (o.provisioned ? "Yes" : <span className="text-muted">Not provisioned</span>),
    },
    {
      key: "last",
      header: "Last signed in",
      cell: (o) =>
        o.lastSignedInAt ? <When value={o.lastSignedInAt} /> : <span className="text-muted">Never</span>,
    },
    {
      key: "state",
      header: "State",
      cell: (o) => <StatusChip value={o.suspended ? "suspended" : "active"} />,
    },
  ];

  return (
    <DataTable
      columns={columns}
      rows={rows}
      keyOf={(o) => o.id}
      href={(o) => `/admin/administration/admins?id=${encodeURIComponent(o.id)}`}
      empty={
        <EmptyList title="No operator matches that">
          The search covers the name, the address and the role.
        </EmptyList>
      }
    />
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
                : mayWrite
                  // Not "finishes the invitation", which is right for somebody
                  // a colleague created and wrong for the root operator, who
                  // was provisioned rather than invited and reaches this same
                  // panel with no password on a fresh installation.
                  ? "No. A password has never been set. Setting one below is what makes this account usable."
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
          <>
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

            {/* NOT KEYED on `operator.provisioned`, and that was a bug in
                this file for exactly as long as it took to look at the
                rendered page. Keying it there reads as tidy: the label should
                say "Replace the password" once the account has one. What it
                actually did was remount the component the instant the write
                succeeded, because the reload flips that prop, which threw away
                the panel showing the password that had just been set. The
                write worked, the directory updated, and the one thing the
                operator needed, the value to send the person, was on screen
                for no frames at all. The component reads the prop on every
                render, so the label is right without a remount. */}
            <SetPassword
              target={{
                id: operator.id,
                email: operator.email,
                provisioned: operator.provisioned,
                isRoot: operator.isRoot,
              }}
              isSelf={isSelf}
              onSet={onChanged}
            />
          </>
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
        // Not danger. Restoring gives an operator their account back, and a red
        // button beside "Keep it" reads as a deletion dialog: the two together
        // say the opposite of what pressing them does.
        tone="primary"
        cancelLabel="Leave it suspended"
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
  const [created, setCreated] = useState<{ id: string; email: string; effect: string } | null>(null);
  /** Set once the second half of the invitation has happened, so that the
   *  first half's sentence about an account that cannot sign in stops being
   *  rendered directly above one saying it now can. */
  const [provisioned, setProvisioned] = useState(false);

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      const address = email.trim().toLowerCase();
      const result = await createOperator({ email: address, name: name.trim(), role });
      // The id is kept, which is what lets this panel finish the invitation
      // rather than reporting that an unusable account now exists and sending
      // somebody to a shell with a connection string.
      //
      // The address is lowercased here as well as on the server, because this
      // one is used locally: it is the phrase the confirmation asks to be
      // typed and the address the panel says to send the password to, and an
      // operator who typed a capital would otherwise be shown one address and
      // asked to type another.
      setCreated({ id: result.id, email: address, effect: result.effect });
      onCreated();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "That did not work.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Drawer open title="New operator" onClose={onClose}>
      {created ? (
        <>
          {provisioned ? null : (
            <div className="px-4 py-4">
              <p role="status" className="text-[13px] leading-6 text-ink">
                {created.effect}
              </p>
              <p className="mt-3 text-[13px] leading-6 text-muted">
                Set one below to finish the invitation, or leave it and do that later from this
                operator's own panel. Whichever password is used is made in this browser and the
                server keeps only a hash of it.
              </p>
            </div>
          )}

          {/* The second half of the invitation, in the panel that sent it.
              Leaving the reader with "set a password out of band" and a Done
              button is what made every account created here unusable until
              somebody found a shell and the admin database URL. */}
          <SetPassword
            target={{ id: created.id, email: created.email, provisioned, isRoot: false }}
            isSelf={false}
            onSet={() => {
              setProvisioned(true);
              onCreated();
            }}
            onFinished={onClose}
          />

          {provisioned ? null : (
            <div className="px-4 py-4">
              {/* Not "Cancel", which would read as undoing the account, and not
                  "Done", which would claim the invitation is finished. The row
                  exists and cannot sign in, and leaving now is a real choice
                  that the drawer on the operator can complete later. */}
              <Button onClick={onClose}>Set it later</Button>
            </div>
          )}
        </>
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
 * The password, which is the half of an invitation nothing here could finish
 * ---------------------------------------------------------------------- */

/**
 * The unambiguous alphabet, and why it is not the whole of one.
 *
 * A password set here is READ OUT or PASTED INTO A MESSAGE by the person
 * setting it, because that is the only way it reaches the person it belongs to.
 * So the characters that cannot survive that trip are gone: `l` against `1`,
 * `O` against `0`, and capitals entirely, which are the difference between a
 * password that works and one that works when it is typed carefully. Thirty one
 * symbols over twenty characters is about ninety nine bits, which is far past
 * anything scrypt at this work factor needs to be safe.
 */
const PASSWORD_ALPHABET = "abcdefghjkmnpqrstuvwxyz23456789";

/** Characters per group, and groups, in a generated password. Twenty
 *  characters, grouped so a human can read them aloud without losing their
 *  place, and joined with single hyphens that count toward the length. */
const PASSWORD_GROUP = 5;
const PASSWORD_GROUPS = 4;

/**
 * A password the browser makes, which the server never chooses.
 *
 * THE GENERATION IS HERE AND NOT IN THE ROUTE, deliberately. A route that
 * generated a starting credential would put a value the server picked into the
 * row, and every log, proxy and error report between here and there would have
 * had a chance at it. This runs in the operator's own browser, the value goes
 * out once in the body of one request, and the server stores only a scrypt hash
 * of it. Nothing on the platform can read it back, which is why the panel says
 * so before it lets the reader close it.
 *
 * REJECTION SAMPLING RATHER THAN A MODULO. 256 is not a multiple of 31, so
 * `byte % 31` makes the first eight letters of the alphabet appear about twelve
 * percent more often than the rest. It is a small bias and it is a real one,
 * and discarding the eight byte values above the last whole multiple costs
 * nothing to do correctly.
 */
function generatePassword(): string {
  const limit = 256 - (256 % PASSWORD_ALPHABET.length);
  const wanted = PASSWORD_GROUP * PASSWORD_GROUPS;
  const picked: string[] = [];
  const bytes = new Uint8Array(64);
  while (picked.length < wanted) {
    crypto.getRandomValues(bytes);
    for (const byte of bytes) {
      if (picked.length === wanted) break;
      if (byte >= limit) continue;
      picked.push(PASSWORD_ALPHABET[byte % PASSWORD_ALPHABET.length]!);
    }
  }
  const groups: string[] = [];
  for (let i = 0; i < wanted; i += PASSWORD_GROUP) {
    groups.push(picked.slice(i, i + PASSWORD_GROUP).join(""));
  }
  return groups.join("-");
}

/** Everything this form needs to know about who it is acting on. Smaller than
 *  `Operator` on purpose: the create panel has an id and an address and nothing
 *  else, and it needs this form as much as the drawer does. */
type PasswordTarget = {
  id: string;
  email: string;
  /** Whether a working credential is being REPLACED rather than an invitation
   *  finished. The two are different events and get different words, a
   *  different button and, for the first, a typed confirmation. */
  provisioned: boolean;
  isRoot: boolean;
};

/**
 * Setting somebody's password, in the two places an operator arrives at it.
 *
 * WHAT THIS FIXES. `admin.operators.create` writes a row with no password and
 * says "Set a password out of band before it is usable", and the only out of
 * band that existed was a command holding the admin database connection string.
 * So a person invited through this portal appeared in the directory reading
 * `Not provisioned` and `Never`, and finishing the invitation needed a shell.
 * The route behind this form is what makes the invitation completable by the
 * person who sent it.
 *
 * IT DOES NOT PREDICT THE SERVER'S RULES, which is the same choice the role
 * control on this page makes. The minimum length, the refusal of a stray
 * newline, the demand for the current password when the target is you: all of
 * those are enforced and worded by the server, and rendered here as its own
 * sentence. A form that reimplemented them would be a second copy of a security
 * rule, drifting quietly, and hiding the button on a guess about the rules is
 * how a control disappears the day the rules change with nobody finding out.
 *
 * WHAT IT DOES DECIDE LOCALLY is which of two events this is. Finishing an
 * invitation is ordinary and needs one press. Replacing a password that works
 * ends every session that operator has and locks out whoever was using the old
 * one, so it asks for the address to be typed first, exactly as suspending
 * does. Root gets the same treatment for a different reason: the account the
 * database refuses to delete, demote or suspend is the one where a new password
 * is the whole of taking it over.
 */
function SetPassword({
  target,
  isSelf,
  onSet,
  onFinished,
}: {
  target: PasswordTarget;
  /** The server demands the current password only in this case. See the route. */
  isSelf: boolean;
  onSet: () => void;
  /**
   * What "I have sent it" does when this form is the LAST step of something
   * larger. In the drawer there is nothing after it, so dismissing the value
   * returns to the form. In the create panel the invitation is finished at
   * that moment, so the panel closes instead, and the button says so. Without
   * this the create panel offered two finishing buttons stacked on each other,
   * which is the shape of a screen nobody read after it was assembled.
   */
  onFinished?: () => void;
}) {
  const [password, setPassword] = useState("");
  const [current, setCurrent] = useState("");
  const [shown, setShown] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [effect, setEffect] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);

  // Replacing a credential that works, or touching root, is the shape that
  // gets a typed confirmation. Finishing an invitation is not.
  const grave = target.provisioned || target.isRoot;

  async function run() {
    setBusy(true);
    setError(null);
    try {
      const result = await setOperatorPassword({
        adminUserId: target.id,
        password,
        // Sent only when it can be needed. An empty string here would be a
        // wrong current password rather than an absent one, and the server
        // says different things about those two.
        ...(isSelf ? { currentPassword: current } : {}),
      });
      setConfirming(false);
      setCurrent("");
      // The server's own sentence, which is the only thing that knows how many
      // sessions it cut and whether the account is suspended anyway.
      setEffect(result.effect);
      onSet();
    } catch (e) {
      setConfirming(false);
      setError(e instanceof ApiError ? e.message : "That did not work.");
    } finally {
      setBusy(false);
    }
  }

  function dismiss() {
    // The value leaves the browser here, and the form comes back reading the
    // account's NEW state, because `target` is a prop rather than something
    // captured when this mounted.
    setEffect(null);
    setPassword("");
    setShown(false);
  }

  if (effect) {
    return (
      <div className="border-t border-rule px-4 py-4">
        <p role="status" className="text-[13px] leading-6 text-pass">
          {effect}
        </p>
        {isSelf ? null : (
          <>
            <p className="mt-3 text-[13px] leading-6 text-muted">
              This is the last time this password is on screen. Send it to {target.email} through
              something you would send a password through, and ask them to change it once they are
              in.
            </p>
            <div className="mt-3 flex flex-wrap items-start justify-between gap-3">
              {/* The same surface CommandBlock uses, and not that component:
                  a command wraps on its spaces and a password has none, so
                  this one breaks anywhere rather than running off the edge of
                  a phone with no scrollbar and no ellipsis. */}
              {/* basis-full below the small breakpoint, and that is not a
                  cosmetic preference. Sharing the row with the button at 390px
                  left about 210px for the value, and `break-all` then split a
                  generated password after its twenty second character, so the
                  line ended in a lone `c`. A password is READ OFF THIS SCREEN
                  and retyped; a break in the middle of a group is a character
                  somebody loses or reads as part of the separator. Full width
                  first, side by side only where there is room for both. */}
              <code className="min-w-0 basis-full select-all break-all rounded-md border border-rule bg-[rgba(16,16,16,0.03)] px-3 py-2.5 font-mono text-[12.5px] leading-6 text-ink sm:flex-1 sm:basis-0">
                {password}
              </code>
              <CopyButton
                value={password}
                label="Copy the password"
                said="Password copied to the clipboard"
              />
            </div>
          </>
        )}
        <div className="mt-4">
          {/* The only thing that takes the password off the screen. It is not
              a timer and not the drawer closing: an operator who is halfway
              through pasting it into a message should decide when it goes. */}
          <Button onClick={onFinished ?? dismiss} variant={onFinished ? "primary" : "secondary"}>
            {onFinished ? "Done" : isSelf ? "Done" : "I have sent it"}
          </Button>
        </div>
      </div>
    );
  }

  return (
    <>
      <form
        className="border-t border-rule px-4 py-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (grave) setConfirming(true);
          else void run();
        }}
      >
        <div className="grid gap-3">
          <Field
            label={target.provisioned ? "New password" : "Password"}
            hint={
              isSelf
                ? "You will stay signed in here. Every other session you have ends."
                : target.provisioned
                  ? "Replaces the one they have. Every session they are in ends on its next request."
                  : "The account cannot sign in until this is set. Nothing on the platform can read it back afterwards, so keep it until you have passed it on."
            }
          >
            <input
              className={inputClass}
              // Toggled rather than fixed. A password being set FOR somebody
              // else has to be read off the screen to be passed on, and one
              // being typed in an office should not sit there in plain text.
              type={shown ? "text" : "password"}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="new-password"
              spellCheck={false}
              required
            />
          </Field>

          <div className="flex flex-wrap gap-2">
            <Button
              onClick={() => {
                setPassword(generatePassword());
                // Generating one and hiding it is a control that does nothing:
                // the operator cannot pass on what they cannot see.
                setShown(true);
              }}
            >
              Generate one
            </Button>
            <Button onClick={() => setShown(!shown)} pressed={shown}>
              {shown ? "Hide" : "Show"}
            </Button>
            {password ? (
              <CopyButton value={password} label="Copy" said="Password copied to the clipboard" />
            ) : null}
          </div>

          {isSelf ? (
            <Field
              label="Your current password"
              hint="Asked for because this is your own account. An operator session lasts twelve hours, and without this a stolen one would be permanent."
            >
              <input
                className={inputClass}
                type="password"
                value={current}
                onChange={(e) => setCurrent(e.target.value)}
                autoComplete="current-password"
                required
              />
            </Field>
          ) : null}
        </div>

        <div className="mt-4 flex flex-wrap gap-2">
          <Button
            type="submit"
            variant={target.provisioned ? "danger" : "primary"}
            disabled={password === ""}
            busy={busy && !grave}
          >
            {target.provisioned ? "Replace the password" : "Set the password"}
          </Button>
        </div>

        {error ? (
          <p role="alert" className="mt-3 text-[13px] leading-6 text-fail">
            {error}
          </p>
        ) : null}
      </form>

      <Confirm
        open={confirming}
        // The title says which of the two things this is, rather than always
        // saying the loud one. The root operator reaches this dialog with no
        // password at all, and a heading reading "Replace the password"
        // directly above "nothing is being replaced" is a dialog arguing with
        // itself in the one place a reader is being asked to be careful.
        title={`${target.provisioned ? "Replace" : "Set"} the password for ${target.email}`}
        // The account's own address rather than a fixed word, the same as
        // suspending. Typing a word proves somebody read a dialog; typing the
        // address proves they read WHICH account.
        phrase={target.email}
        confirmLabel={target.provisioned ? "Replace it" : "Set it"}
        // Danger only when something is being taken away. Replacing a working
        // password ends every session that operator holds; giving a first one
        // to an account that has never had any is the same shape as restoring
        // a suspended operator, which this component's own note explains is
        // why `primary` exists. A red button beside "Not now" would say the
        // opposite of what pressing it does.
        tone={target.provisioned ? "danger" : "primary"}
        cancelLabel={target.provisioned ? "Keep it" : "Not now"}
        busy={busy}
        error={error}
        onCancel={() => {
          setConfirming(false);
          setError(null);
        }}
        onConfirm={() => void run()}
      >
        <p className="text-[13px] leading-6 text-muted">
          {target.provisioned
            ? "Their current password stops working, and every operator session they hold stops resolving on its next request. If they are in the middle of something, this ends it."
            : "This account has never had a password, so nothing is being replaced and nobody is being signed out. It is being given a way in."}
        </p>
        {target.isRoot ? (
          <p className="mt-3 text-[13px] leading-6 text-muted">
            This is the root operator, the account the database refuses to delete, demote or
            suspend. Setting its password is the one way its holder changes. This is recorded in
            the platform audit chain at critical severity under your address.
          </p>
        ) : null}
      </Confirm>
    </>
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
        <EmptyList title="That role holds nothing">
          Every built in role holds at least admin.portal.access, so a role with no permissions
          means the catalog and the role table disagree. That is a bug rather than a
          configuration.
        </EmptyList>
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
