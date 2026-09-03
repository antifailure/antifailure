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
  selectClass,
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
import { until } from "@/lib/format";
import {
  addNote,
  retractNote,
  startImpersonation,
  useImpersonations,
  useNotes,
  useTenantDetail,
  type ImpersonationEvent,
  type LiveImpersonation,
  type NoteSubject,
  type TenantMember,
} from "@/lib/admin-customers";
import type { ApiError } from "@/lib/api";

/**
 * Answering a customer's question from their side, and the record of every time
 * anybody has.
 *
 * WHAT AN OPERATOR OPENS THIS TO ANSWER, in order: what did the last person
 * find out about this account, can I see what they are seeing, and who else has
 * been inside an account lately. The three cards below are those three
 * questions and nothing else.
 *
 * THE ACCOUNT IS REACHED THROUGH ITS ORGANIZATION, which is not the obvious
 * arrangement and is the correct one. An impersonation needs a person AND the
 * organization to act in, because a session with no organization shows an empty
 * console, and a person in the wrong one shows somebody else's. Picking the
 * organization first means the pair always agrees: every member listed is a
 * member, so the refusal the route carries for a non-member is unreachable from
 * this screen rather than being a validation message somebody has to read.
 *
 * WHY THERE IS NO LIVE COUNTDOWN. "Ends in 12 minutes" is computed when the
 * card renders and a Refresh button re-reads it. A number that ticks by itself
 * is a thing moving on the page while the reader does nothing, and it buys a
 * precision nobody acts on: the decision an operator makes off this figure is
 * the same at twelve minutes and at eleven.
 */
export default function CustomersSupportPage() {
  const { me } = useAdminContext();
  const [search, setSearch] = useState("");
  const [org, setOrg] = useState<Tenant | null>(null);
  const impersonations = useImpersonations();

  const maySeeRecord = operatorMay(me, "admin.impersonation.read");

  return (
    <AdminPage
      href="/admin/customers/support"
      actions={
        maySeeRecord ? (
          <Button onClick={impersonations.reload} busy={impersonations.status === "loading"}>
            Refresh
          </Button>
        ) : null
      }
    >
      <div className="grid gap-5">
        {org ? (
          <OneOrganization org={org} onClear={() => setOrg(null)} onStarted={impersonations.reload} />
        ) : (
          <FindOrganization search={search} setSearch={setSearch} onPick={setOrg} />
        )}

        {maySeeRecord ? <LiveCard state={impersonations} /> : null}
        {maySeeRecord ? <RecentCard state={impersonations} /> : null}
      </div>
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * Finding the account
 * ---------------------------------------------------------------------- */

function FindOrganization({
  search,
  setSearch,
  onPick,
}: {
  search: string;
  setSearch: (s: string) => void;
  onPick: (t: Tenant) => void;
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
      key: "state",
      header: "State",
      cell: (t) =>
        t.suspended ? <Badge tone="fail">suspended</Badge> : <Badge tone="pass">active</Badge>,
    },
    {
      key: "open",
      header: "Open",
      // A button rather than a link, because this selects the record on THIS
      // page rather than navigating: the impersonation record below has to stay
      // where the reader can see it while they decide.
      cell: (t) => <Button onClick={() => onPick(t)}>Open</Button>,
    },
  ];

  return (
    <Card title="Find a customer">
      <FilterBar
        search={{
          value: search,
          onChange: setSearch,
          label: "Search organizations by name or slug",
          placeholder: "Name or slug",
        }}
      />
      <Loaded state={state} skeleton={<TableSkeleton rows={5} cols={5} />}>
        {(rows) => (
          <DataTable
            columns={columns}
            rows={rows}
            keyOf={(t) => t.id}
            empty={
              <EmptyList title={search ? "No organization matches that" : "No organizations yet"}>
                {search
                  ? "Nothing on this installation has that name or slug. Clear the search to see every organization."
                  : "There is nobody to support yet. The first organization created here shows up in this table."}
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
 * One organization: what is known about it, and who is in it
 * ---------------------------------------------------------------------- */

function OneOrganization({
  org,
  onClear,
  onStarted,
}: {
  org: Tenant;
  onClear: () => void;
  onStarted: () => void;
}) {
  const { me } = useAdminContext();
  const detail = useTenantDetail(org.id);
  const [noteFor, setNoteFor] = useState<TenantMember | null>(null);
  const [actAs, setActAs] = useState<TenantMember | null>(null);

  const mayStart = operatorMay(me, "admin.impersonation.start");
  const mayReadNotes = operatorMay(me, "admin.support.read");

  const columns: Column<TenantMember>[] = [
    {
      key: "person",
      header: "Person",
      cell: (m) => (
        <>
          <span className="block truncate font-medium text-ink">{m.name || m.githubLogin}</span>
          <span className="block truncate font-mono text-[12px] text-muted">{m.githubLogin}</span>
        </>
      ),
    },
    { key: "email", header: "Email", cell: (m) => <span className="break-all">{m.email}</span> },
    { key: "role", header: "Role in this organization", cell: (m) => m.role },
    {
      key: "actions",
      header: "Actions",
      cell: (m) => (
        <span className="flex flex-wrap gap-2">
          {mayReadNotes ? <Button onClick={() => setNoteFor(m)}>Notes</Button> : null}
          {mayStart ? (
            <Button variant="danger" onClick={() => setActAs(m)}>
              Act as
            </Button>
          ) : null}
        </span>
      ),
    },
  ];

  return (
    <>
      <Card
        title={org.name}
        note={org.slug}
        actions={<Button onClick={onClear}>Back to the list</Button>}
      >
        <Facts
          facts={[
            { label: "Plan", value: org.plan },
            { label: "Created", value: <When value={org.createdAt} /> },
            {
              label: "State",
              value: org.suspended ? (
                <Badge tone="fail">suspended</Badge>
              ) : (
                <Badge tone="pass">active</Badge>
              ),
            },
            { label: "Suspended because", value: org.suspendedReason },
          ]}
        />
      </Card>

      {mayReadNotes ? (
        <Card
          title="Notes about this organization"
          note="Operators only. These are never shown to the customer and never appear in their audit log or their export."
        >
          <Notes subjectType="organization" subjectId={org.id} label={org.slug} />
        </Card>
      ) : null}

      <Card title="People in this organization">
        <Loaded state={detail} skeleton={<TableSkeleton rows={4} cols={4} />} framed>
          {(d) =>
            d === null ? (
              <EmptyList title="No organization loaded">
                Pick an organization from the list above.
              </EmptyList>
            ) : (
              <DataTable
                columns={columns}
                rows={d.members}
                keyOf={(m) => m.userId}
                empty={
                  <EmptyList title="Nobody is in this organization">
                    Every member has been removed, or it was created and never joined. There is no
                    account here to act as.
                  </EmptyList>
                }
              />
            )
          }
        </Loaded>
      </Card>

      <Drawer
        open={noteFor !== null}
        title={noteFor ? `Notes about ${noteFor.githubLogin}` : "Notes"}
        onClose={() => setNoteFor(null)}
      >
        {noteFor ? (
          <Notes subjectType="user" subjectId={noteFor.userId} label={noteFor.githubLogin} />
        ) : null}
      </Drawer>

      <ActAs
        member={actAs}
        org={org}
        onCancel={() => setActAs(null)}
        onStarted={onStarted}
      />
    </>
  );
}

/* -------------------------------------------------------------------------
 * Notes
 * ---------------------------------------------------------------------- */

function Notes({
  subjectType,
  subjectId,
  label,
}: {
  subjectType: NoteSubject;
  subjectId: string;
  label: string;
}) {
  const { me } = useAdminContext();
  const state = useNotes(subjectType, subjectId);
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [retracting, setRetracting] = useState<string | null>(null);
  const [retractReason, setRetractReason] = useState("");

  const mayWrite = operatorMay(me, "admin.support.write");

  async function run(fn: () => Promise<unknown>) {
    setBusy(true);
    setError(null);
    try {
      await fn();
      state.reload();
    } catch (err) {
      setError((err as ApiError).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <Loaded state={state} skeleton={<TableSkeleton rows={2} cols={1} />}>
        {(rows) =>
          rows.length === 0 ? (
            <EmptyList title="Nothing written down yet">
              {mayWrite
                ? "No operator has recorded anything about this one. The box below is what fills it."
                : "No operator has recorded anything about this one, and your role cannot add to it."}
            </EmptyList>
          ) : (
            <>
              <ul className="divide-y divide-rule">
                {rows.map((n) => (
                  <li key={n.id} className="px-4 py-3">
                    <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                      <span className="break-all text-[12px] text-muted">{n.author}</span>
                      <span className="text-[12px] text-dim">
                        <When value={n.createdAt} />
                      </span>
                    </div>
                    <p
                      className={`mt-1.5 whitespace-pre-wrap break-words text-[13px] leading-6 ${
                        // Struck through rather than hidden. A note somebody
                        // took back is still a thing an operator wrote about a
                        // customer, and the retraction is itself part of the
                        // record: 0023 makes the column a soft delete for
                        // exactly this reason.
                        n.retractedAt ? "text-dim line-through" : "text-ink"
                      }`}
                    >
                      {n.body}
                    </p>
                    {n.retractedAt ? (
                      <p className="mt-1.5 text-[12px] text-muted">
                        Retracted <When value={n.retractedAt} />
                      </p>
                    ) : mayWrite ? (
                      <div className="mt-2">
                        <Button onClick={() => setRetracting(n.id)}>Retract</Button>
                      </div>
                    ) : null}
                  </li>
                ))}
              </ul>
              <More
                shown={rows.length}
                noun={{ one: "note", many: "notes" }}
                hasMore={state.hasMore}
                busy={state.busy}
                error={state.moreError}
                onMore={state.more}
              />
            </>
          )
        }
      </Loaded>

      {mayWrite ? (
        <form
          className="border-t border-rule px-4 py-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (!draft.trim() || busy) return;
            void run(async () => {
              await addNote(subjectType, subjectId, draft.trim());
              setDraft("");
            });
          }}
        >
          <Field
            label={`Add a note about ${label}`}
            hint="Visible to operators only. Recorded in the operator audit chain, which does not carry the text."
            error={error}
          >
            <textarea
              className={`${inputClass} h-auto min-h-24 py-2 sm:h-auto`}
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              maxLength={10000}
              rows={3}
            />
          </Field>
          <div className="mt-2 flex justify-end">
            <Button type="submit" variant="primary" busy={busy} disabled={!draft.trim()}>
              Save this note
            </Button>
          </div>
        </form>
      ) : null}

      <Confirm
        open={retracting !== null}
        title="Retract this note?"
        confirmLabel="Retract"
        busy={busy}
        error={error}
        onCancel={() => {
          setRetracting(null);
          setRetractReason("");
          setError(null);
        }}
        onConfirm={() =>
          void run(async () => {
            if (retracting) await retractNote(retracting, retractReason);
            setRetracting(null);
            setRetractReason("");
          })
        }
      >
        <p className="text-[13px] leading-6 text-muted">
          The note stays on the record, struck through, with your reason beside it in the operator
          audit chain. Nothing is deleted, because a note somebody took back is part of what
          happened.
        </p>
        <div className="mt-4">
          <Field label="Reason" hint="At least eight characters. Recorded, not shown to the customer.">
            <input
              className={inputClass}
              value={retractReason}
              onChange={(e) => setRetractReason(e.target.value)}
              maxLength={500}
              required
            />
          </Field>
        </div>
      </Confirm>
    </>
  );
}

/* -------------------------------------------------------------------------
 * Acting as a customer
 * ---------------------------------------------------------------------- */

/** The lengths the route accepts, offered rather than typed. A free number
 *  field on this form is a place to put 600 and find out from a validation
 *  message that the cap is 60. */
const LENGTHS = [15, 30, 45, 60];

function ActAs({
  member,
  org,
  onCancel,
  onStarted,
}: {
  member: TenantMember | null;
  org: Tenant;
  onCancel: () => void;
  onStarted: () => void;
}) {
  const [reason, setReason] = useState("");
  const [minutes, setMinutes] = useState(30);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  return (
    <Confirm
      open={member !== null}
      title={member ? `Act as ${member.githubLogin}?` : "Act as"}
      // The organization's own slug typed out, not the word "confirm". Typing
      // "confirm" proves somebody can read a label; typing the slug proves they
      // know which customer they are about to become.
      phrase={org.slug}
      confirmLabel="Start acting as this account"
      busy={busy}
      error={error}
      onCancel={() => {
        setError(null);
        setReason("");
        onCancel();
      }}
      onConfirm={() => {
        if (!member) return;
        setBusy(true);
        setError(null);
        void (async () => {
          try {
            await startImpersonation({
              userId: member.userId,
              orgId: org.id,
              reason,
              minutes,
            });
            onStarted();
            // A full reload rather than a re-render, because this browser is
            // now signed in as the customer and the operator gate refuses every
            // procedure on the page. Re-rendering would replace a working
            // screen with a dozen failed panels sharing one cause; reloading
            // lands on the shell's own refusal, which states that cause once
            // and offers the way out.
            window.location.assign("/");
          } catch (err) {
            setError((err as ApiError).message);
            setBusy(false);
          }
        })();
      }}
    >
      <p className="text-[13px] leading-6 text-muted">
        <strong className="font-medium text-ink">What this does:</strong> this browser becomes{" "}
        <span className="font-mono">{member?.githubLogin}</span> in{" "}
        <span className="font-mono">{org.slug}</span>. The operator portal closes for this session
        until you end it, and every operator action is refused while it lasts.
      </p>
      <p className="mt-3 text-[13px] leading-6 text-muted">
        The customer is told. The record goes into their own audit log, attributed to you by
        address, with the reason you type below, and it is written before the session exists.
      </p>
      <div className="mt-4 grid gap-4">
        <Field
          label="Reason"
          hint="At least eight characters. The customer reads this. A ticket reference is the useful thing to put here."
        >
          <input
            className={inputClass}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            maxLength={500}
            required
          />
        </Field>
        <div>
          <label
            htmlFor="impersonation-minutes"
            className="block text-[12px] font-medium text-muted"
          >
            How long
          </label>
          <select
            id="impersonation-minutes"
            className={`${selectClass} mt-1.5 w-full`}
            value={minutes}
            onChange={(e) => setMinutes(Number(e.target.value))}
          >
            {LENGTHS.map((m) => (
              <option key={m} value={m}>
                {m} minutes
              </option>
            ))}
          </select>
          <span className="mt-1.5 block text-[12px] leading-5 text-dim">
            It stops on its own at the end. Ending it yourself is one press and is what you should
            do the moment you are finished.
          </span>
        </div>
      </div>
    </Confirm>
  );
}

/* -------------------------------------------------------------------------
 * The record
 * ---------------------------------------------------------------------- */

type ImpersonationState = ReturnType<typeof useImpersonations>;

function LiveCard({ state }: { state: ImpersonationState }) {
  // Read once per render rather than from a timer. See the note at the top of
  // this file about why there is no countdown.
  const now = new Date();

  const columns: Column<LiveImpersonation>[] = [
    {
      key: "account",
      header: "Account",
      cell: (r) => (
        <>
          <span className="block truncate font-medium text-ink">{r.githubLogin}</span>
          <span className="block truncate text-[12px] text-muted">{r.email}</span>
        </>
      ),
    },
    { key: "org", header: "Organization", cell: (r) => r.orgSlug ?? "None", mono: true },
    { key: "operator", header: "Operator", cell: (r) => r.operator },
    {
      key: "reason",
      header: "Reason",
      cell: (r) => <span className="block max-w-[40ch] break-words">{r.reason}</span>,
    },
    { key: "started", header: "Started", cell: (r) => <When value={r.startedAt} /> },
    { key: "ends", header: "Ends in", cell: (r) => until(r.endsAt, now) },
  ];

  return (
    <Card
      title="Inside a customer account right now"
      note="Every open impersonation on this installation, whoever started it."
    >
      <Loaded state={state} skeleton={<TableSkeleton rows={2} cols={6} />}>
        {(data) => (
          <DataTable
            columns={columns}
            rows={data.live}
            keyOf={(r) => r.sessionId}
            empty={
              <EmptyList title="Nobody is inside a customer account">
                No operator is acting as a customer at the moment. Open an organization above and
                pick a person to start one, which requires a reason and lasts minutes.
              </EmptyList>
            }
          />
        )}
      </Loaded>
    </Card>
  );
}

function RecentCard({ state }: { state: ImpersonationState }) {
  const columns: Column<ImpersonationEvent>[] = [
    {
      key: "what",
      header: "What happened",
      cell: (e) => (
        <Badge tone={e.action === "impersonation.started" ? "warn" : "neutral"}>
          {e.action === "impersonation.started" ? "started" : "ended"}
        </Badge>
      ),
    },
    { key: "operator", header: "Operator", cell: (e) => e.operator },
    { key: "org", header: "Organization", cell: (e) => e.organization ?? "None" },
    {
      key: "detail",
      header: "Account",
      cell: (e) => (
        <span className="font-mono text-[12px]">
          {typeof e.detail?.githubLogin === "string" ? e.detail.githubLogin : (e.targetId ?? "--")}
        </span>
      ),
    },
    {
      key: "reason",
      header: "Reason",
      cell: (e) => (
        <span className="block max-w-[40ch] break-words">
          {typeof e.detail?.reason === "string" ? e.detail.reason : "--"}
        </span>
      ),
    },
    { key: "when", header: "When", cell: (e) => <When value={e.occurredAt} /> },
    { key: "seq", header: "Entry", numeric: true, cell: (e) => e.seq.toLocaleString() },
  ];

  return (
    <Card
      title="Every impersonation on the record"
      note="From the operator audit chain, which is where a finished one still exists. The customer holds a copy of each of these in their own log."
    >
      <Loaded state={state} skeleton={<TableSkeleton rows={3} cols={7} />}>
        {(data) => (
          <DataTable
            columns={columns}
            rows={data.recent}
            keyOf={(e) => String(e.seq)}
            empty={
              <EmptyList title="Nobody has ever acted as a customer here">
                No impersonation has been started on this installation. When one is, it is recorded
                here and in that customer's own audit log at the same moment.
              </EmptyList>
            }
          />
        )}
      </Loaded>
    </Card>
  );
}
