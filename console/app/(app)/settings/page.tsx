"use client";

import { useState } from "react";
import { mutate, query, useApi, type ApiError } from "@/lib/api";
import { useSessionContext } from "@/components/session";
import { may } from "@/lib/roles";
import {
  Badge,
  Button,
  Card,
  CardSkeleton,
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
} from "@/components/ui";

/* -------------------------------------------------------------------------
 * Shapes, matched to what the routes actually return.
 * ---------------------------------------------------------------------- */

interface Deletion {
  id: string;
  organization: string;
  slug: string;
  requestedBy: string;
  requestedAt: string;
  reason: string | null;
  step:
    | "stop_work"
    | "cancel_subscription"
    | "await_entitlement_end"
    | "revoke_credentials"
    | "export"
    | "purge"
    | "done"
    | "cancelled";
  stoppedWork: { at: string; environments: number; runs: number } | null;
  cancelledSubscription: {
    at: string;
    subscription: string | null;
    entitlementEndsAt: string | null;
  } | null;
  revokedCredentials: {
    at: string;
    engineTokens: number;
    providerKeys: number;
    sessions: number;
    installations: number;
  } | null;
  exportedAt: string | null;
  purgedAt: string | null;
  cancelledAt: string | null;
  waitingUntil: string | null;
  export: {
    available: boolean;
    expiresAt: string | null;
    sizeBytes: number | null;
    downloads: number;
  } | null;
  lastError: { at: string; step: string; message: string } | null;
  attempts: number;
}

interface Settings {
  slug: string;
  name: string;
  githubLogin: string | null;
  plan: string;
  createdAt: string;
  suspended: boolean;
  suspendedReason: string | null;
  counts: {
    members: number;
    repositories: number;
    environments: number;
    openInvitations: number;
  };
  deletion: Deletion | null;
  exportRetentionDays: number;
}

interface BillingContact {
  contact: { email: string; name: string | null; updatedBy: string; updatedAt: string } | null;
  onFileWithStripe: string | null;
  hasCustomer: boolean;
}

interface SessionRow {
  id: string;
  person: string;
  name: string | null;
  ip: string | null;
  userAgent: string | null;
  startedAt: string;
  lastSeenAt: string;
  expiresAt: string;
  revokedAt: string | null;
  expired: boolean;
  isYou: boolean;
}

/* -------------------------------------------------------------------------
 * Small pieces
 * ---------------------------------------------------------------------- */

function saidWrong(err: unknown): string {
  return err instanceof Error ? err.message : "That did not work.";
}

/** One outcome line beside a control. Never a raw status code, never silence. */
function Said({ tone, children }: { tone: "ok" | "bad"; children: React.ReactNode }) {
  return (
    <p
      role={tone === "bad" ? "alert" : "status"}
      className={`text-[12px] leading-5 ${tone === "bad" ? "text-fail" : "text-dim"}`}
    >
      {children}
    </p>
  );
}

/** A read-only value with its label, for facts nobody can change here. */
function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">{label}</dt>
      <dd className="mt-1 truncate text-[13px] text-ink">{children}</dd>
    </div>
  );
}

/* -------------------------------------------------------------------------
 * The organization
 * ---------------------------------------------------------------------- */

function Organization({
  settings,
  csrf,
  mayEdit,
  onSaved,
}: {
  settings: Settings;
  csrf: string;
  mayEdit: boolean;
  onSaved: () => void;
}) {
  const [name, setName] = useState(settings.name);
  const [busy, setBusy] = useState(false);
  const [said, setSaid] = useState<{ tone: "ok" | "bad"; text: string } | null>(null);

  const changed = name.trim() !== settings.name && name.trim().length > 0;

  return (
    <Card
      title="Organization"
      note={
        mayEdit
          ? "The name is what people see. The identifier is what links and commands use, and it does not change."
          : "Changing the name needs owner or admin."
      }
    >
      <div className="space-y-5 px-4 py-4">
        <dl className="grid grid-cols-2 gap-x-4 gap-y-4 sm:grid-cols-4">
          <Fact label="Identifier">
            <span className="font-mono text-[12px]">{settings.slug}</span>
          </Fact>
          <Fact label="Plan">{settings.plan}</Fact>
          <Fact label="People">{settings.counts.members}</Fact>
          <Fact label="Repositories">{settings.counts.repositories}</Fact>
        </dl>

        {settings.suspended ? (
          <div className="rounded-md border border-[rgba(138,90,0,0.3)] bg-[rgba(138,90,0,0.06)] px-3.5 py-3">
            <p className="text-[13px] font-medium text-ink">
              This organization cannot create anything new
            </p>
            <p className="mt-1 text-[12px] leading-5 text-muted">
              What is already running keeps running and can still be read.
            </p>
            {/* The reason on its own line rather than welded to the sentence
                above it. It is a value somebody typed, or the phrase a deletion
                writes, and neither ends in a full stop, so joining them read as
                "deletion requested What is already running". */}
            <p className="mt-1 text-[12px] leading-5 text-dim">
              Reason: {settings.suspendedReason ?? "none was recorded"}.
            </p>
          </div>
        ) : null}

        {mayEdit ? (
          <form
            className="flex flex-wrap items-end gap-3"
            onSubmit={async (e) => {
              e.preventDefault();
              setBusy(true);
              setSaid(null);
              try {
                await mutate("org.rename", { name: name.trim() }, csrf);
                setSaid({ tone: "ok", text: "Saved." });
                onSaved();
              } catch (err) {
                setSaid({ tone: "bad", text: saidWrong(err) });
              } finally {
                setBusy(false);
              }
            }}
          >
            <div className="w-full max-w-[320px]">
              <Field label="Name">
                <input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  maxLength={100}
                  className={inputClass}
                />
              </Field>
            </div>
            <Button type="submit" variant="primary" disabled={!changed} busy={busy}>
              Save
            </Button>
            {said ? <Said tone={said.tone}>{said.text}</Said> : null}
          </form>
        ) : (
          <Fact label="Name">{settings.name}</Fact>
        )}
      </div>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * The billing contact
 * ---------------------------------------------------------------------- */

function BillingContactCard({ csrf }: { csrf: string }) {
  const state = useApi<BillingContact>(() => query("org.billingContact"), []);
  const [email, setEmail] = useState<string | null>(null);
  const [name, setName] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [said, setSaid] = useState<{ tone: "ok" | "bad"; text: string } | null>(null);

  return (
    <Card
      title="Billing contact"
      note="Where invoices and payment notices go. This is not anybody's sign-in address."
    >
      <Loaded state={state} framed skeleton={<CardSkeleton count={1} />}>
        {(loaded) => {
          const current = email ?? loaded.contact?.email ?? "";
          const currentName = name ?? loaded.contact?.name ?? "";
          const disagrees =
            loaded.hasCustomer &&
            loaded.contact !== null &&
            loaded.onFileWithStripe !== null &&
            loaded.onFileWithStripe !== loaded.contact.email;

          return (
            <div className="space-y-4 px-4 py-4">
              {disagrees ? (
                <div className="rounded-md border border-[rgba(138,90,0,0.3)] bg-[rgba(138,90,0,0.06)] px-3.5 py-3">
                  <p className="text-[13px] font-medium text-ink">
                    Invoices are still going to a different address
                  </p>
                  <p className="mt-1 text-[12px] leading-5 text-muted">
                    Stripe has{" "}
                    <span className="font-mono text-[11.5px]">{loaded.onFileWithStripe}</span> on
                    file. Save the address again to send it across.
                  </p>
                </div>
              ) : null}

              <form
                className="flex flex-wrap items-end gap-3"
                onSubmit={async (e) => {
                  e.preventDefault();
                  setBusy(true);
                  setSaid(null);
                  try {
                    const saved = await mutate<{ pushedToStripe: boolean; note: string | null }>(
                      "org.setBillingContact",
                      { email: current.trim(), name: currentName.trim() || undefined },
                      csrf,
                    );
                    setSaid({
                      tone: "ok",
                      text: saved.pushedToStripe
                        ? "Saved, and sent to Stripe."
                        : (saved.note ?? "Saved."),
                    });
                    state.reload();
                  } catch (err) {
                    setSaid({ tone: "bad", text: saidWrong(err) });
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                <div className="w-full max-w-[320px]">
                  <Field label="Email address">
                    <input
                      type="email"
                      value={current}
                      onChange={(e) => setEmail(e.target.value)}
                      className={inputClass}
                      placeholder="accounts@yourcompany.com"
                    />
                  </Field>
                </div>
                <div className="w-full max-w-[240px]">
                  <Field label="Name (optional)">
                    <input
                      value={currentName}
                      onChange={(e) => setName(e.target.value)}
                      className={inputClass}
                      placeholder="Accounts payable"
                    />
                  </Field>
                </div>
                <Button
                  type="submit"
                  variant="primary"
                  disabled={current.trim().length < 3}
                  busy={busy}
                >
                  Save
                </Button>
              </form>

              {said ? <Said tone={said.tone}>{said.text}</Said> : null}
              {loaded.contact ? (
                <p className="text-[12px] leading-5 text-dim">
                  Last changed by {loaded.contact.updatedBy},{" "}
                  <When value={loaded.contact.updatedAt} />.
                </p>
              ) : null}
            </div>
          );
        }}
      </Loaded>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * Sessions
 * ---------------------------------------------------------------------- */

function Sessions({ csrf }: { csrf: string }) {
  const state = useApi<SessionRow[]>(() => query("sessions.list", { includeRevoked: false }), []);
  const [busy, setBusy] = useState<string | null>(null);
  const [said, setSaid] = useState<{ tone: "ok" | "bad"; text: string } | null>(null);
  const [confirming, setConfirming] = useState<SessionRow | null>(null);
  const [dropped, setDropped] = useState<Set<string>>(new Set());

  async function revoke(row: SessionRow) {
    setBusy(row.id);
    setSaid(null);
    // Optimistic: the row goes now and comes back if the call fails, because a
    // table that sits still for a round trip after a destructive click reads as
    // a button that did nothing.
    setDropped((d) => new Set(d).add(row.id));
    try {
      await mutate("sessions.revoke", { id: row.id }, csrf);
      setSaid({
        tone: "ok",
        text: row.isYou
          ? "Signed out. Your next request will send you to the sign-in page."
          : `Signed ${row.person} out of that session.`,
      });
      state.reload();
    } catch (err) {
      setDropped((d) => {
        const next = new Set(d);
        next.delete(row.id);
        return next;
      });
      setSaid({ tone: "bad", text: saidWrong(err) });
    } finally {
      setBusy(null);
      setConfirming(null);
    }
  }

  return (
    <Card
      title="Signed in now"
      note="Every live session in this organization. Signing one out takes effect on its next request."
    >
      <Loaded state={state} skeleton={<TableSkeleton rows={3} cols={4} />}>
        {(rows) => {
          const live = rows.filter((r) => !dropped.has(r.id));
          if (live.length === 0) {
            return (
              <Empty title="Nobody is signed in">
                Sessions appear here as people sign in, and disappear when they expire.
              </Empty>
            );
          }
          return (
            <>
              <TableWrap>
                <Table>
                  <thead>
                    <tr>
                      <Th>Person</Th>
                      <Th>From</Th>
                      <Th>Last used</Th>
                      <Th>Expires</Th>
                      <Th>
                        <span className="sr-only">Sign out</span>
                      </Th>
                    </tr>
                  </thead>
                  <tbody>
                    {live.map((row) => (
                      <Row key={row.id}>
                        <Td>
                          <span className="flex flex-wrap items-center gap-2">
                            <span className="min-w-0">
                              <span className="block truncate text-ink">{row.person}</span>
                              {row.name ? (
                                <span className="block truncate text-[12px] text-dim">
                                  {row.name}
                                </span>
                              ) : null}
                            </span>
                            {row.isYou ? <Badge tone="pass">You</Badge> : null}
                          </span>
                        </Td>
                        <Td label="From">
                          <span className="block max-w-[26ch] truncate font-mono text-[11.5px] text-muted">
                            {row.ip ?? "unknown"}
                          </span>
                          {row.userAgent ? (
                            <span className="block max-w-[34ch] truncate text-[11.5px] text-dim">
                              {row.userAgent}
                            </span>
                          ) : null}
                        </Td>
                        <Td label="Last used">
                          <When value={row.lastSeenAt} />
                        </Td>
                        <Td label="Expires">
                          <When value={row.expiresAt} />
                        </Td>
                        <Td label="Sign out">
                          <Button
                            variant="danger"
                            busy={busy === row.id}
                            onClick={() => setConfirming(row)}
                          >
                            Sign out
                          </Button>
                        </Td>
                      </Row>
                    ))}
                  </tbody>
                </Table>
              </TableWrap>
              {said ? (
                <div className="border-t border-rule px-4 py-3">
                  <Said tone={said.tone}>{said.text}</Said>
                </div>
              ) : null}
            </>
          );
        }}
      </Loaded>

      <Confirm
        open={confirming !== null}
        title={confirming?.isYou ? "Sign yourself out?" : `Sign ${confirming?.person} out?`}
        confirmLabel="Sign out"
        busy={busy !== null}
        onCancel={() => setConfirming(null)}
        onConfirm={() => confirming && void revoke(confirming)}
      >
        {confirming?.isYou ? (
          <p>
            This is the session you are using right now. Your next request will send you to the
            sign-in page, and you can sign in again straight away.
          </p>
        ) : (
          <p>
            {confirming?.person} stays a member and can sign in again. Only this one session, last
            used from {confirming?.ip ?? "an unknown address"}, stops working.
          </p>
        )}
      </Confirm>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * The export
 * ---------------------------------------------------------------------- */

function ExportCard({ csrf, slug }: { csrf: string; slug: string }) {
  const [busy, setBusy] = useState(false);
  const [said, setSaid] = useState<{ tone: "ok" | "bad"; text: string } | null>(null);

  return (
    <Card
      title="Take a copy"
      note="Everything this control plane holds about your organization, as one file."
    >
      <div className="space-y-4 px-4 py-4">
        <p className="max-w-[62ch] text-[13px] leading-6 text-muted">
          People, repositories, masking rules, egress policy, environments, runs, verdicts, billing
          history and the audit log. Every name in it is the name you already use, so there is not
          one internal identifier in the file, and the masking and egress files inside it are the
          real ones: commit them and the engine reads them as they are.
        </p>
        <div className="flex flex-wrap items-center gap-3">
          <Button
            variant="primary"
            busy={busy}
            onClick={async () => {
              setBusy(true);
              setSaid(null);
              try {
                const doc = await mutate<Record<string, unknown>>("exports.organization", {}, csrf);
                const blob = new Blob([JSON.stringify(doc, null, 2)], {
                  type: "application/json",
                });
                const url = URL.createObjectURL(blob);
                const a = document.createElement("a");
                a.href = url;
                a.download = `antifailure-${slug}-${new Date().toISOString().slice(0, 10)}.json`;
                a.click();
                URL.revokeObjectURL(url);
                setSaid({ tone: "ok", text: "Downloaded." });
              } catch (err) {
                setSaid({ tone: "bad", text: saidWrong(err) });
              } finally {
                setBusy(false);
              }
            }}
          >
            {busy ? "Building" : "Download a copy"}
          </Button>
          {said ? <Said tone={said.tone}>{said.text}</Said> : null}
        </div>
      </div>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * Deleting the organization
 * ---------------------------------------------------------------------- */

/** "1 environment", not "1 environments". */
function count(n: number, one: string, many = `${one}s`): string {
  return `${n} ${n === 1 ? one : many}`;
}

const STEPS: { key: Deletion["step"]; label: string; detail: (d: Deletion) => string }[] = [
  {
    key: "stop_work",
    label: "Stop what is running",
    detail: (d) =>
      d.stoppedWork
        ? `${count(d.stoppedWork.environments, "environment")} torn down, ${count(d.stoppedWork.runs, "run")} cancelled`
        : "Environments are torn down and queued runs are cancelled",
  },
  {
    key: "cancel_subscription",
    label: "End the subscription",
    detail: (d) =>
      d.cancelledSubscription
        ? d.cancelledSubscription.subscription
          ? `Cancelled ${d.cancelledSubscription.subscription} at the end of the paid period`
          : "There was no subscription to cancel"
        : "Cancelled at Stripe, at the end of the period you have paid for",
  },
  {
    key: "await_entitlement_end",
    label: "Wait out the period you paid for",
    detail: (d) =>
      d.waitingUntil
        ? `Nothing else happens until ${new Date(d.waitingUntil).toLocaleString()}`
        : "Nothing to wait for",
  },
  {
    key: "revoke_credentials",
    label: "Revoke credentials",
    detail: (d) =>
      d.revokedCredentials
        ? [
            count(d.revokedCredentials.engineTokens, "engine token"),
            count(d.revokedCredentials.providerKeys, "provider key"),
            count(d.revokedCredentials.sessions, "session"),
            count(d.revokedCredentials.installations, "App installation"),
          ].join(", ")
        : "Engine tokens, provider keys, sessions and the GitHub App installation",
  },
  {
    key: "export",
    label: "Produce the export",
    detail: (d) =>
      d.exportedAt ? "Ready to download" : "A complete copy, taken before anything is removed",
  },
  {
    key: "purge",
    label: "Delete everything",
    detail: (d) => (d.purgedAt ? "Done" : "The organization and every row belonging to it"),
  },
];

function order(step: Deletion["step"]): number {
  const i = STEPS.findIndex((s) => s.key === step);
  return i < 0 ? STEPS.length : i;
}

function Progress({ deletion }: { deletion: Deletion }) {
  const at = deletion.step === "done" ? STEPS.length : order(deletion.step);
  return (
    <ol className="space-y-3">
      {STEPS.map((step, i) => {
        const done = i < at;
        const here = i === at;
        return (
          <li key={step.key} className="flex gap-3">
            <span
              aria-hidden
              className={`mt-[3px] grid h-4 w-4 shrink-0 place-items-center rounded-full border text-[9px] font-semibold ${
                done
                  ? "border-[rgba(30,122,58,0.45)] bg-[rgba(30,122,58,0.12)] text-pass"
                  : here
                    ? "border-rule-strong bg-card text-ink"
                    : "border-rule bg-card text-dim"
              }`}
            >
              {done ? "✓" : i + 1}
            </span>
            <span className="min-w-0">
              <span
                className={`block text-[13px] ${done || here ? "text-ink" : "text-dim"} ${
                  here ? "font-medium" : ""
                }`}
              >
                {step.label}
                {here ? <span className="sr-only"> (in progress)</span> : null}
                {done ? <span className="sr-only"> (done)</span> : null}
              </span>
              <span className="mt-0.5 block text-[12px] leading-5 text-muted">
                {step.detail(deletion)}
              </span>
            </span>
          </li>
        );
      })}
    </ol>
  );
}

function Deleting({
  settings,
  csrf,
  mayDelete,
  onChanged,
}: {
  settings: Settings;
  csrf: string;
  mayDelete: boolean;
  onChanged: () => void;
}) {
  const deletion = settings.deletion;
  const [reason, setReason] = useState("");
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [exportUrl, setExportUrl] = useState<string | null>(null);
  const [cancelling, setCancelling] = useState(false);

  if (deletion) {
    return (
      <Card
        title="This organization is being deleted"
        note={`Asked for by ${deletion.requestedBy}.`}
      >
        <div className="space-y-5 px-4 py-4">
          {deletion.reason ? (
            <p className="max-w-[62ch] text-[13px] leading-6 text-muted">
              Reason given: {deletion.reason}
            </p>
          ) : null}

          {deletion.lastError ? (
            <div
              role="alert"
              className="rounded-md border border-[rgba(179,38,30,0.3)] bg-[rgba(179,38,30,0.06)] px-3.5 py-3"
            >
              <p className="text-[13px] font-medium text-ink">
                It stopped at {deletion.lastError.step.replace(/_/g, " ")}
              </p>
              <p className="mt-1 max-w-[62ch] text-[12px] leading-5 text-muted">
                {deletion.lastError.message}
              </p>
              <p className="mt-1 text-[12px] leading-5 text-dim">
                Nothing has been deleted. It is retried on its own, and you can push it along.
              </p>
            </div>
          ) : null}

          <Progress deletion={deletion} />

          {exportUrl ? (
            <div className="rounded-md border border-rule bg-paper px-3.5 py-3">
              <p className="text-[13px] font-medium text-ink">Keep this link</p>
              <p className="mt-1 max-w-[62ch] text-[12px] leading-5 text-muted">
                It is the only way to download the export once the organization is gone, it is shown
                once, and it works for {settings.exportRetentionDays} days.
              </p>
              <p className="mt-2 break-all font-mono text-[11.5px] text-ink">{exportUrl}</p>
            </div>
          ) : null}

          {deletion.export?.available && deletion.exportedAt ? (
            <p className="text-[12px] leading-5 text-dim">
              The export is ready. Use the link you were given when you asked for the deletion; it
              works until <When value={deletion.export.expiresAt} />.
            </p>
          ) : null}

          {mayDelete ? (
            <div className="flex flex-wrap items-center gap-2">
              <Button
                busy={busy}
                onClick={async () => {
                  setBusy(true);
                  setError(null);
                  try {
                    await mutate("deletion.advance", {}, csrf);
                    onChanged();
                  } catch (err) {
                    setError(saidWrong(err));
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                Continue now
              </Button>
              <Button variant="danger" onClick={() => setCancelling(true)}>
                Call it off
              </Button>
              {error ? <Said tone="bad">{error}</Said> : null}
            </div>
          ) : (
            <p className="text-[12px] leading-5 text-dim">
              Only an owner can continue or call off a deletion.
            </p>
          )}
        </div>

        <Confirm
          open={cancelling}
          title="Call off the deletion?"
          confirmLabel="Call it off"
          busy={busy}
          error={error}
          onCancel={() => setCancelling(false)}
          onConfirm={async () => {
            setBusy(true);
            setError(null);
            try {
              await mutate("deletion.cancel", {}, csrf);
              setCancelling(false);
              onChanged();
            } catch (err) {
              setError(saidWrong(err));
            } finally {
              setBusy(false);
            }
          }}
        >
          <p>
            The organization stays. Anything that was already torn down stays torn down, and any
            credential that was already revoked stays revoked: you can issue new ones.
          </p>
          <p>
            The subscription is not restarted. If it was cancelled, buy a plan again from the Plan
            page.
          </p>
        </Confirm>
      </Card>
    );
  }

  return (
    <Card
      title="Delete this organization"
      note={
        mayDelete
          ? "It happens in order, it takes as long as your paid period has left, and it cannot be undone once it finishes."
          : "Only an owner can delete an organization."
      }
    >
      <div className="space-y-4 px-4 py-4">
        {/* Numbered, because these are steps in an order rather than a set of
            facts, and the order is the whole design: the subscription ends
            before the wait, and the wait ends before anything is revoked. */}
        <ol className="max-w-[62ch] list-decimal space-y-1.5 pl-5 text-[13px] leading-6 text-muted marker:text-dim">
          <li>Everything running is torn down and nothing new can be started.</li>
          <li>The subscription is cancelled at Stripe, at the end of the period you paid for.</li>
          <li>Nothing else happens until that period ends. You keep what you bought.</li>
          <li>Engine tokens, provider keys, sessions and the GitHub App installation are revoked.</li>
          <li>
            A complete copy is produced and you are given a link to it, kept for{" "}
            {settings.exportRetentionDays} days.
          </li>
          <li>
            Then the organization and every row belonging to it are deleted, including the audit
            log.
          </li>
        </ol>

        {mayDelete ? (
          <>
            <div className="max-w-[420px]">
              <Field
                label="Why (optional)"
                hint="Recorded on the deletion and in the audit log."
              >
                <input
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                  maxLength={1000}
                  className={inputClass}
                  placeholder="Moving to another vendor"
                />
              </Field>
            </div>
            <div className="flex flex-wrap items-center gap-3">
              <Button variant="danger" onClick={() => setAsking(true)}>
                Delete this organization
              </Button>
              {error ? <Said tone="bad">{error}</Said> : null}
            </div>
          </>
        ) : null}
      </div>

      <Confirm
        open={asking}
        title={`Delete ${settings.name}?`}
        phrase={settings.slug}
        confirmLabel="Start the deletion"
        busy={busy}
        error={error}
        onCancel={() => setAsking(false)}
        onConfirm={async () => {
          setBusy(true);
          setError(null);
          try {
            const started = await mutate<{ exportUrl: string }>(
              "deletion.request",
              { confirm: settings.slug, reason: reason.trim() || undefined },
              csrf,
            );
            setExportUrl(started.exportUrl);
            setAsking(false);
            onChanged();
          } catch (err) {
            setError(saidWrong(err));
          } finally {
            setBusy(false);
          }
        }}
      >
        <p>
          This deletes {count(settings.counts.repositories, "repository", "repositories")},{" "}
          {count(settings.counts.members, "person", "people")}, every environment, every run and
          verdict, the masking rules, the egress policy, the billing history and the audit log.
        </p>
        <p>
          Your database is not touched, because none of it is here: no snapshot, no masked branch
          and no captured request body ever reaches this control plane.
        </p>
        <p>You can call it off at any point before it finishes.</p>
      </Confirm>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * Closing your own account
 * ---------------------------------------------------------------------- */

/**
 * Every role sees this, including viewer, because it is about the holder rather
 * than about the organization.
 *
 * The copy says close and not delete, and lists what stays, because that is
 * what the route does: `audit_entries.actor_user_id` references `users` with NO
 * ACTION and the column is inside the hash chain, so the row cannot be removed
 * and the entries keep the name you had at the time. A control called Delete
 * that anonymises is a claim this product does not get to make.
 */
function CloseAccount({ csrf, label }: { csrf: string; label: string }) {
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [closed, setClosed] = useState<{ removed: string[]; kept: string[] } | null>(null);

  if (closed) {
    return (
      <Card title="Your account is closed">
        <div className="space-y-4 px-4 py-4">
          <div>
            <p className="text-[13px] font-medium text-ink">What was removed</p>
            <ul className="mt-1.5 max-w-[62ch] list-disc space-y-1 pl-5 text-[13px] leading-6 text-muted marker:text-dim">
              {closed.removed.map((r) => (
                <li key={r}>{r}</li>
              ))}
            </ul>
          </div>
          <div>
            <p className="text-[13px] font-medium text-ink">What was kept</p>
            <ul className="mt-1.5 max-w-[62ch] list-disc space-y-1 pl-5 text-[13px] leading-6 text-muted marker:text-dim">
              {closed.kept.map((k) => (
                <li key={k}>{k}</li>
              ))}
            </ul>
          </div>
          <p className="text-[12px] leading-5 text-dim">
            Your next request will send you to the sign-in page. Signing in again makes a new
            account.
          </p>
        </div>
      </Card>
    );
  }

  return (
    <Card
      title="Close your account"
      note="Yours alone. It does not delete the organization and does not affect anybody else."
    >
      <div className="space-y-4 px-4 py-4">
        <p className="max-w-[62ch] text-[13px] leading-6 text-muted">
          Your name, email address, GitHub identity and avatar are erased, your membership of this
          organization is removed, and every session you have signed in with stops working.
        </p>
        <p className="max-w-[62ch] text-[13px] leading-6 text-muted">
          It is called closing rather than deleting because the audit log keeps what you did under
          the name you had at the time. The log is a hash chain, so an entry cannot be rewritten,
          and those entries go when the organization does.
        </p>
        <div className="flex flex-wrap items-center gap-3">
          <Button variant="danger" onClick={() => setAsking(true)}>
            Close my account
          </Button>
          {error ? <Said tone="bad">{error}</Said> : null}
        </div>
      </div>

      <Confirm
        open={asking}
        title="Close your account?"
        phrase={label}
        confirmLabel="Close my account"
        busy={busy}
        error={error}
        onCancel={() => {
          setAsking(false);
          setError(null);
        }}
        onConfirm={async () => {
          setBusy(true);
          setError(null);
          try {
            setClosed(
              await mutate<{ removed: string[]; kept: string[] }>(
                "account.close",
                { confirm: label },
                csrf,
              ),
            );
            setAsking(false);
          } catch (err) {
            setError(saidWrong(err));
          } finally {
            setBusy(false);
          }
        }}
      >
        <p>
          You lose access to this organization now. Nothing you built is removed and nobody else is
          affected.
        </p>
        <p>
          If you are the only owner, this is refused: make somebody else an owner first, or delete
          the organization.
        </p>
      </Confirm>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * The page
 * ---------------------------------------------------------------------- */

export default function SettingsPage() {
  const session = useSessionContext();
  const state = useApi<Settings>(() => query("org.settings"), []);
  const csrf = session.data?.csrfToken ?? "";
  const role = session.data?.role ?? null;

  const mayEditSettings = may(role, "organization.settings");
  const mayBill = may(role, "billing.manage");
  const maySessions = may(role, "sessions.manage");
  const mayExport = may(role, "data.export");
  const mayDelete = may(role, "organization.delete");

  return (
    <Page
      title="Settings"
      lede="Everything about this organization that is not about one repository: who can reach it, where the bills go, and how to leave."
    >
      <Loaded state={state} framed skeleton={<CardSkeleton count={3} />}>
        {(settings) => (
          <div className="space-y-6">
            <Organization
              settings={settings}
              csrf={csrf}
              mayEdit={mayEditSettings}
              onSaved={state.reload}
            />
            {mayBill ? <BillingContactCard csrf={csrf} /> : null}
            {maySessions ? <Sessions csrf={csrf} /> : null}
            {mayExport ? <ExportCard csrf={csrf} slug={settings.slug} /> : null}
            <Deleting
              settings={settings}
              csrf={csrf}
              mayDelete={mayDelete}
              onChanged={state.reload}
            />
            <CloseAccount csrf={csrf} label={session.data?.label ?? ""} />
          </div>
        )}
      </Loaded>
    </Page>
  );
}
