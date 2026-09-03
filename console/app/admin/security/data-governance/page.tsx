"use client";

import { useId, useState } from "react";
import {
  Badge,
  Bar,
  Button,
  Card,
  Field,
  Loaded,
  TableSkeleton,
  When,
  inputClass,
  type Tone,
} from "@/components/ui";
import {
  AdminPage,
  DataTable,
  EmptyList,
  Facts,
  FilterBar,
  MetricRow,
  StatusChip,
  type Column,
} from "@/components/admin/primitives";
import { More } from "@/components/pagination";
import { ApiError } from "@/lib/api";
import { operatorMay, useAdminContext } from "@/lib/admin";
import {
  exportSubject,
  lookupSubject,
  saveDocument,
  useDeletions,
  useErasure,
  useMasking,
  useOrphanedAccounts,
  type Deletion,
  type DeletionStep,
  type MaskingRule,
  type OrphanedAccount,
  type SubjectAnswer,
  type SubjectLocation,
} from "@/lib/admin-security";

/**
 * Data Governance: the question a customer's lawyer asks, and the parts of it
 * this product cannot answer.
 *
 * THREE QUESTIONS, IN THE ORDER THEY ARE ASKED. What do you hold about this
 * person, where is it, and how do I make it go away. The first two are
 * answerable today and this page answers them precisely. The third is answered
 * honestly, which means saying that organization erasure works, per person
 * erasure does not exist, and naming what would be needed to build it.
 *
 * WHY THERE IS NO COMPLIANCE DASHBOARD HERE. There is no retention policy
 * table, no data residency table, no consent record and no subject request
 * table anywhere in this schema. A page of green ticks over that would be read
 * by somebody making a legal claim on behalf of the company. So what is not
 * wired is stated in words, on the page, next to what is.
 *
 * THE SUBJECT LOOKUP IS A FORM AND NOT A LIVE SEARCH, deliberately. Reading a
 * person's data map writes an audit entry naming that person, which is the
 * entry a data protection request is answered from. Firing that on every
 * keystroke would fill the operator log with people nobody meant to open, and
 * bury the entries that matter among them.
 */

const STEP_COPY: Record<DeletionStep, { label: string; tone: Tone }> = {
  stop_work: { label: "Stopping work", tone: "warn" },
  cancel_subscription: { label: "Cancelling the subscription", tone: "warn" },
  await_entitlement_end: { label: "Waiting for the paid period", tone: "neutral" },
  revoke_credentials: { label: "Revoking credentials", tone: "warn" },
  export: { label: "Producing the export", tone: "warn" },
  purge: { label: "Deleting the data", tone: "warn" },
  done: { label: "Erased", tone: "pass" },
  cancelled: { label: "Called off", tone: "neutral" },
};

export default function DataGovernancePage() {
  return (
    <AdminPage
      href="/admin/security/data-governance"
      lede="What this installation holds about a person, where it lives, and every organization erasure it has been asked for."
    >
      <div className="grid gap-6">
        <SubjectLookup />
        <Deletions />
        <Orphans />
        <Masking />
        <NotWired />
      </div>
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * The subject lookup
 * ---------------------------------------------------------------------- */

function SubjectLookup() {
  const { me } = useAdminContext();
  const mayExport = operatorMay(me, "admin.governance.export");
  const [term, setTerm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [answer, setAnswer] = useState<SubjectAnswer | null>(null);
  const searchId = useId();

  async function run(input: { userId?: string; query?: string }) {
    setBusy(true);
    setError(null);
    try {
      setAnswer(await lookupSubject(input));
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "The control plane could not be reached.",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card
      title="What is held about one person"
      note="An email address or a GitHub login. Every table that references an account is discovered from the database at the moment you ask, so a table added last week is in the answer."
    >
      <form
        role="search"
        className="flex flex-wrap items-end gap-2 border-b border-rule px-4 py-3.5"
        onSubmit={(e) => {
          e.preventDefault();
          if (term.trim().length > 0) void run({ query: term.trim() });
        }}
      >
        <div className="min-w-0 grow basis-[16rem]">
          <label htmlFor={searchId} className="block text-[12px] font-medium text-muted">
            Email address or GitHub login
          </label>
          <input
            id={searchId}
            type="search"
            className={inputClass}
            value={term}
            placeholder="person@example.com"
            onChange={(e) => setTerm(e.target.value)}
          />
        </div>
        <Button type="submit" variant="primary" busy={busy} disabled={term.trim().length === 0}>
          Look up
        </Button>
      </form>

      {error ? (
        <p role="alert" className="border-b border-rule px-4 py-3 text-[13px] text-fail">
          {error}
        </p>
      ) : null}

      {answer === null ? (
        <div className="px-4 py-6">
          <p className="max-w-[68ch] text-[13px] leading-6 text-muted">
            Nothing has been looked up yet. Looking somebody up is itself recorded in the operator
            log, at notice severity and with their name on it, because the person asking what you
            hold about them is also entitled to know who read it.
          </p>
        </div>
      ) : answer.subject === null ? (
        <Candidates answer={answer} onPick={(id) => void run({ userId: id })} />
      ) : (
        <SubjectDetail answer={answer} mayExport={mayExport} />
      )}
    </Card>
  );
}

function Candidates({
  answer,
  onPick,
}: {
  answer: SubjectAnswer;
  onPick: (id: string) => void;
}) {
  if (answer.candidates.length === 0) {
    return (
      <EmptyList title="No account matches that">
        No account on this installation has that email address or GitHub login. An account that was
        never created holds nothing, which is a real answer to a data protection request and is
        different from one whose data was erased.
      </EmptyList>
    );
  }
  return (
    <div className="px-4 py-4">
      <p className="text-[13px] leading-6 text-muted">
        That matched more than one account, so nothing has been read yet and nothing was recorded.
        Choose the person you mean.
      </p>
      <ul className="mt-3 grid gap-1.5">
        {answer.candidates.map((c) => (
          <li key={c.id}>
            <button
              type="button"
              onClick={() => onPick(c.id)}
              className="flex min-h-11 w-full items-center justify-between gap-3 rounded-md border border-rule bg-card px-3 py-2 text-left hover:border-rule-strong"
            >
              <span className="min-w-0">
                <span className="block truncate text-[13px] font-medium text-ink">
                  {c.githubLogin}
                </span>
                <span className="block truncate text-[12px] text-muted">
                  {c.name ? `${c.name} ` : ""}
                  {c.email ?? "no email address recorded"}
                </span>
              </span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

function SubjectDetail({ answer, mayExport }: { answer: SubjectAnswer; mayExport: boolean }) {
  const subject = answer.subject!;
  const map = answer.map ?? [];
  const held = map.filter((m) => m.rows > 0);

  const columns: Column<SubjectLocation>[] = [
    {
      key: "table",
      header: "Where",
      cell: (m) => (
        <>
          <span className="block truncate font-mono text-[12.5px] font-medium text-ink">
            {m.table}
          </span>
          <span className="block truncate font-mono text-[12px] text-muted">{m.column}</span>
        </>
      ),
    },
    {
      key: "rows",
      header: "Rows",
      numeric: true,
      // "1000 or more" rather than a number nobody counted. The count stops at
      // a ceiling because some of these columns have no index and an operator
      // page must not be able to sequentially scan the runs table while
      // customers are using the database.
      cell: (m) => (m.atLeast ? `${m.rows.toLocaleString()} or more` : m.rows.toLocaleString()),
    },
    {
      key: "onDelete",
      header: "If the account is deleted",
      cell: (m) =>
        m.onDelete === "cascade" ? (
          <StatusChip value="Deleted with it" tone="pass" />
        ) : m.onDelete === "set null" ? (
          <StatusChip value="Kept, de-identified" tone="warn" />
        ) : (
          <StatusChip value={`Refused while it exists (${m.onDelete})`} tone="fail" />
        ),
    },
  ];

  return (
    <>
      <Facts
        facts={[
          { label: "GitHub login", value: subject.githubLogin },
          { label: "Name", value: subject.name },
          { label: "Email address", value: subject.email },
          { label: "Account id", value: subject.id, mono: true },
          { label: "Account created", value: <When value={subject.createdAt} /> },
          {
            label: "Organizations",
            value:
              subject.organizations.length === 0 ? (
                <span className="text-muted">None. This account belongs to no organization.</span>
              ) : (
                // The slug and the role are two facts, and Badge uppercases
                // its contents, so putting both inside one made "northwind
                // owner" read as a single label rather than an organization and
                // a role in it.
                <span className="flex flex-wrap items-center gap-x-1.5 gap-y-1">
                  {subject.organizations.map((o) => (
                    <span key={o.slug} className="inline-flex items-center gap-1.5">
                      <Badge>{o.slug}</Badge>
                      <span className="text-[12.5px] text-muted">{o.role}</span>
                    </span>
                  ))}
                </span>
              ),
          },
        ]}
      />

      {mayExport ? <ExportSubject userId={subject.id} login={subject.githubLogin} /> : null}

      <div className="border-t border-rule">
        <h3 className="px-4 pt-4 text-[13px] font-semibold tracking-extra-tight text-ink">
          Where it is
        </h3>
        <p className="max-w-[72ch] px-4 pt-1 text-[12px] leading-5 text-dim">
          {held.length.toLocaleString()} of {map.length.toLocaleString()} places that can hold data
          about an account hold data about this one. The rest are listed with zero, because &quot;we
          hold nothing there&quot; and &quot;we did not look&quot; must not render the same.
        </p>
        <DataTable
          columns={columns}
          rows={map}
          keyOf={(m) => `${m.table}.${m.column}`}
          empty={
            <EmptyList title="No table references accounts">
              Nothing in this database has a foreign key to the accounts table, which would mean the
              schema is not what this page expects. That is a bug rather than an answer.
            </EmptyList>
          }
        />
      </div>

      {answer.retained ? (
        <div className="border-t border-rule px-4 py-4">
          <h3 className="text-[13px] font-semibold tracking-extra-tight text-ink">
            Kept on purpose, and not erasable
          </h3>
          <dl className="mt-2 grid gap-3">
            {answer.retained.map((r) => (
              <div key={`${r.table}.${r.column}`}>
                <dt className="font-mono text-[12px] text-muted">
                  {r.table}.{r.column}
                </dt>
                <dd className="mt-0.5 max-w-[72ch] text-[13px] leading-6 text-ink">{r.why}</dd>
              </div>
            ))}
          </dl>
        </div>
      ) : null}
    </>
  );
}

/** The document, and the reason it is being produced, which the audit entry
 *  carries. An export with no reason is the entry an investigation cannot use. */
function ExportSubject({ userId, login }: { userId: string; login: string }) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const short = reason.trim().length > 0 && reason.trim().length < 8;

  async function run() {
    setBusy(true);
    setError(null);
    try {
      const file = await exportSubject(userId, reason.trim());
      saveDocument(file);
      setDone(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "The control plane could not be reached.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="border-t border-rule px-4 py-4">
      <h3 className="text-[13px] font-semibold tracking-extra-tight text-ink">
        Produce this as a file
      </h3>
      <p className="mt-1 max-w-[72ch] text-[12px] leading-5 text-dim">
        A map of where the data is, not a copy of the data. This product builds one data export and
        it is scoped to an organization rather than a person. The file records who produced it, why,
        and the same answer this page shows.
      </p>
      {/* Stacked rather than a row. The field carries a hint under it, so a
          row aligned on its bottom edge left the button level with the hint
          text and a full input height below the thing it acts on. */}
      <div className="mt-3 max-w-[34rem]">
        <Field
          label="Why"
          hint="Kept on the audit entry, at high severity, because a document naming somebody is leaving."
          error={short ? "At least eight characters, so the entry says something." : null}
        >
          <input
            className={inputClass}
            value={reason}
            placeholder="Subject access request from counsel"
            onChange={(e) => {
              setReason(e.target.value);
              setDone(false);
            }}
          />
        </Field>
        <div className="mt-3">
          <Button onClick={() => void run()} busy={busy} disabled={reason.trim().length < 8}>
            Download the map
          </Button>
        </div>
      </div>
      {error ? (
        <p role="alert" className="mt-2 text-[13px] text-fail">
          {error}
        </p>
      ) : done ? (
        <p role="status" className="mt-2 text-[13px] text-muted">
          Saved as subject-{login}.json, and recorded in the operator log.
        </p>
      ) : null}
    </div>
  );
}

/* -------------------------------------------------------------------------
 * Erasure requests
 * ---------------------------------------------------------------------- */

const DELETION_STATES = [
  { value: "open", label: "In progress" },
  { value: "stuck", label: "Stuck" },
  { value: "finished", label: "Finished" },
  { value: "all", label: "All" },
];

function Deletions() {
  const [state, setState] = useState<"open" | "stuck" | "finished" | "all">("open");
  const rows = useDeletions(state);

  const columns: Column<Deletion>[] = [
    {
      key: "organization",
      header: "Organization",
      cell: (d) => (
        <>
          <span className="block truncate font-medium text-ink">{d.organization}</span>
          <span className="block truncate font-mono text-[12px] text-muted">{d.slug}</span>
        </>
      ),
    },
    {
      key: "step",
      header: "Step",
      cell: (d) => (
        <>
          <StatusChip value={STEP_COPY[d.step].label} tone={STEP_COPY[d.step].tone} />
          {d.waitingUntil ? (
            <span className="mt-1 block text-[12px] text-muted">
              until <When value={d.waitingUntil} />
            </span>
          ) : null}
        </>
      ),
    },
    {
      key: "problem",
      header: "Problem",
      cell: (d) =>
        d.lastError === null ? (
          <span className="text-dim">None</span>
        ) : (
          <>
            <span className="block font-mono text-[12px] text-fail">{d.lastError.step}</span>
            <span className="block max-w-[36ch] break-words text-[12px] text-muted">
              {d.lastError.message}
            </span>
          </>
        ),
    },
    { key: "attempts", header: "Attempts", numeric: true, cell: (d) => d.attempts.toLocaleString() },
    {
      key: "export",
      header: "Export",
      cell: (d) =>
        d.export === null ? (
          <span className="text-dim">Not yet</span>
        ) : d.export.destroyedAt !== null ? (
          <StatusChip value="Destroyed" tone="neutral" />
        ) : d.export.available ? (
          <>
            <StatusChip value="Downloadable" tone="warn" />
            <span className="mt-1 block text-[12px] text-muted">
              until <When value={d.export.expiresAt} />
            </span>
          </>
        ) : (
          <span className="text-dim">Empty</span>
        ),
    },
    { key: "requestedBy", header: "Requested by", cell: (d) => d.requestedBy },
    { key: "requestedAt", header: "Requested", cell: (d) => <When value={d.requestedAt} /> },
  ];

  return (
    <Card
      title="Erasure requests"
      note="Every organization this installation has been asked to delete. The step is derived the same way the customer's own page derives it, so the two cannot disagree."
    >
      <FilterBar
        filters={[
          {
            label: "State",
            value: state,
            onChange: (next) => setState(next as "open" | "stuck" | "finished" | "all"),
            options: DELETION_STATES,
          },
        ]}
      />
      <Loaded state={rows} skeleton={<TableSkeleton rows={4} cols={7} />}>
        {(data) => (
          <DataTable
            columns={columns}
            rows={data}
            keyOf={(d) => d.id}
            empty={
              state === "stuck" ? (
                <EmptyList title="No erasure is stuck">
                  Every deletion in progress has completed its last step without error. A deletion
                  that fails records the step and the message, and the resumer retries it on the
                  same interval as the session sweep.
                </EmptyList>
              ) : (
                <EmptyList title="No organization has asked to be erased">
                  A customer requests deletion from their own settings. There is no operator route
                  that starts one, deliberately: erasing an account is the customer&apos;s decision
                  to make.
                </EmptyList>
              )
            }
            footer={
              <More
                shown={data.length}
                noun={{ one: "request", many: "requests" }}
                hasMore={rows.hasMore}
                busy={rows.busy}
                error={rows.moreError}
                onMore={rows.more}
              />
            }
          />
        )}
      </Loaded>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * What erasure leaves behind
 * ---------------------------------------------------------------------- */

function Orphans() {
  const rows = useOrphanedAccounts();

  const columns: Column<OrphanedAccount>[] = [
    {
      key: "githubLogin",
      header: "Account",
      cell: (a) => <span className="font-medium text-ink">{a.githubLogin}</span>,
    },
    { key: "name", header: "Name", cell: (a) => a.name ?? <span className="text-dim">Not set</span> },
    {
      key: "email",
      header: "Email address",
      cell: (a) => a.email ?? <span className="text-dim">Not set</span>,
    },
    { key: "createdAt", header: "Created", cell: (a) => <When value={a.createdAt} /> },
  ];

  return (
    <Card
      title="Accounts belonging to no organization"
      note="Erasing an organization deletes everything scoped to it and leaves the people in it. An account whose last organization was erased keeps its email address and name, and no route in this product deletes it."
    >
      <Loaded state={rows} skeleton={<TableSkeleton rows={4} cols={4} />}>
        {(data) => (
          <DataTable
            columns={columns}
            rows={data}
            keyOf={(a) => a.id}
            empty={
              <EmptyList title="Every account belongs to an organization">
                Nothing has been left behind by an erasure, and nobody has signed in without joining
                one. This list fills when an organization is purged.
              </EmptyList>
            }
            footer={
              <More
                shown={data.length}
                noun={{ one: "account", many: "accounts" }}
                hasMore={rows.hasMore}
                busy={rows.busy}
                error={rows.moreError}
                onMore={rows.more}
              />
            }
          />
        )}
      </Loaded>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * Masking rules
 * ---------------------------------------------------------------------- */

const CONFIRMED = [
  { value: "", label: "All rules" },
  { value: "no", label: "Not confirmed" },
  { value: "yes", label: "Confirmed" },
];

function Masking() {
  const [confirmed, setConfirmed] = useState("");
  const rows = useMasking(confirmed);

  const columns: Column<MaskingRule>[] = [
    {
      key: "column",
      header: "Column",
      cell: (m) => (
        <>
          <span className="block truncate font-mono text-[12.5px] font-medium text-ink">
            {m.table}.{m.column}
          </span>
          <span className="block truncate text-[12px] text-muted">{m.repository}</span>
        </>
      ),
    },
    { key: "organization", header: "Organization", cell: (m) => m.organization },
    { key: "transform", header: "Transform", mono: true, cell: (m) => m.transform },
    {
      key: "confirmed",
      header: "Confirmed",
      cell: (m) =>
        m.confirmed ? (
          <StatusChip value="Confirmed" tone="pass" />
        ) : (
          <StatusChip value="Not confirmed" tone="warn" />
        ),
    },
    {
      key: "reason",
      header: "Reason",
      cell: (m) =>
        m.reason ? (
          <span className="block max-w-[36ch] break-words">{m.reason}</span>
        ) : (
          <span className="text-dim">Not given</span>
        ),
    },
    { key: "updatedAt", header: "Changed", cell: (m) => <When value={m.updatedAt} /> },
  ];

  return (
    <Card
      title="Personal data customers have declared in their own databases"
      note="A masking rule names a column that must be transformed before it is cloned into a twin. It is the only register of where personal data lives in the data this product handles, and it is written by the customer rather than by us: a column nobody wrote a rule for is a column nobody masked."
    >
      <FilterBar
        filters={[
          { label: "Confirmed", value: confirmed, onChange: setConfirmed, options: CONFIRMED },
        ]}
      />
      <Loaded state={rows} skeleton={<TableSkeleton rows={5} cols={6} />}>
        {(data) => (
          <DataTable
            columns={columns}
            rows={data}
            keyOf={(m) => m.id}
            empty={
              <EmptyList title="No masking rule has been declared">
                No customer has declared a column that needs transforming before it is cloned. Rules
                are written from a repository&apos;s masking page, or proposed by a scan of the
                schema; there is no operator route that writes one on a customer&apos;s behalf.
              </EmptyList>
            }
            footer={
              <More
                shown={data.length}
                noun={{ one: "rule", many: "rules" }}
                hasMore={rows.hasMore}
                busy={rows.busy}
                error={rows.moreError}
                onMore={rows.more}
              />
            }
          />
        )}
      </Loaded>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * The honest part
 * ---------------------------------------------------------------------- */

/**
 * What this product cannot do, named rather than drawn.
 *
 * The copy is not written here. It comes back from the control plane, so the
 * sentences the page shows and the sentences the exported document carries are
 * the same sentences. Two copies of a compliance statement is one copy that is
 * wrong, and the wrong one is whichever a lawyer is reading.
 */
function NotWired() {
  const state = useErasure();
  return (
    <Card
      title="What is not wired, and what it would take"
      note="Read from the control plane rather than written here, so this page and the exported document say the same thing."
    >
      <Loaded
        state={state}
        skeleton={
          <div className="grid gap-3 px-4 py-4">
            <Bar className="h-3 w-3/4" />
            <Bar className="h-3 w-full" />
            <Bar className="h-3 w-2/3" />
          </div>
        }
      >
        {(data) => (
          <dl className="grid gap-4 px-4 py-4">
            <Statement
              term="Erasing one person"
              tone="fail"
              state="Not implemented"
              body={data.erasure.perSubject}
            />
            <Statement
              term="Erasing an organization"
              tone="pass"
              state="Implemented"
              body={data.erasure.perOrganization}
            />
            <Statement
              term="What erasure leaves"
              tone="warn"
              state="Known residue"
              body={data.erasure.residue}
            />
            <Statement
              term="Retention"
              tone="fail"
              state="No policy is enforced"
              body={data.erasure.retention}
            />
          </dl>
        )}
      </Loaded>
    </Card>
  );
}

function Statement({
  term,
  state,
  tone,
  body,
}: {
  term: string;
  state: string;
  tone: Tone;
  body: string;
}) {
  return (
    <div>
      <dt className="flex flex-wrap items-center gap-2">
        <span className="text-[13px] font-semibold tracking-extra-tight text-ink">{term}</span>
        <Badge tone={tone}>{state}</Badge>
      </dt>
      <dd className="mt-1 max-w-[72ch] text-[13px] leading-6 text-muted">{body}</dd>
    </div>
  );
}
