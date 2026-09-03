"use client";

import { useState } from "react";
import { Badge, Button, Card, Loaded, TableSkeleton, When, type Tone } from "@/components/ui";
import {
  AdminPage,
  DataTable,
  Drawer,
  EmptyList,
  Facts,
  FilterBar,
  StatusChip,
  type Column,
} from "@/components/admin/primitives";
import { More } from "@/components/pagination";
import { ApiError } from "@/lib/api";
import { operatorMay, useAdminAudit, useAdminContext, type AdminAuditEntry } from "@/lib/admin";
import { exportChain, saveDocument, verifyChain, type ChainReport } from "@/lib/admin-security";

/**
 * The operator log: what people at this company did to customers' accounts.
 *
 * A SEPARATE CHAIN from a tenant's own audit log, which is why this page exists
 * rather than being a filter on the console's audit page. A platform action has
 * no organization to belong to: an operator signing in, an operator being
 * granted a role, an operator searching every tenant. Those have nowhere to go
 * in a table whose org_id is NOT NULL.
 *
 * Where an action DID concern one organization, that customer has their own
 * copy of it in their own log, written in the same transaction as this entry.
 * This page shows the vendor's half; the customer's half is the one that makes
 * it accountability rather than a private note.
 *
 * THE CHAIN IS HASH LINKED, AND THAT IS ONLY WORTH SOMETHING IF SOMEBODY CAN
 * CHECK IT. The verifier has existed in the database package since the chain
 * did, was tested, and had no caller outside a test, so the tamper evidence was
 * a property of the code rather than of the product. It is a button now, and
 * the export it sits beside carries every entry's prev_hash and entry_hash so
 * the file can be checked by somebody who does not trust us. Both need
 * admin.audit.export, which owner and security hold: reading the log is
 * oversight and every role has it, and producing a file of it is the act of
 * answering for it.
 *
 * THE DETAIL PANEL IS NOT DECORATION. Every entry carries a `detail` object
 * saying WHAT changed, and this page used to drop it on the floor: the table
 * showed that a plan was changed and never what it was changed to. An audit
 * log that records the verb and discards the object answers the easy half of
 * every question asked of it. The panel is where the rest of the entry is.
 */

/** Severity to the console's existing tones, rather than four new colours.
 *  `notice` is deliberately neutral: it is the level for a refusal that is
 *  ordinary, and colouring every refusal amber trains people to ignore amber. */
function toneOf(severity: AdminAuditEntry["severity"]): Tone {
  return severity === "critical" || severity === "high" ? "fail" : "neutral";
}

const SEVERITIES = [
  { value: "", label: "All severities" },
  { value: "critical", label: "Critical" },
  { value: "high", label: "High" },
  { value: "notice", label: "Notice" },
  { value: "info", label: "Info" },
];

export default function AdminAuditPage() {
  const [severity, setSeverity] = useState("");
  const [open, setOpen] = useState<AdminAuditEntry | null>(null);
  const state = useAdminAudit(severity);
  const { me } = useAdminContext();
  const mayExport = operatorMay(me, "admin.audit.export");

  const columns: Column<AdminAuditEntry>[] = [
    {
      key: "action",
      header: "Action",
      cell: (e) => (
        // A real button, so the panel opens from the keyboard and is announced
        // as something that does a thing. A row that only responds to a click
        // is invisible to everybody not using a mouse.
        <button
          type="button"
          onClick={() => setOpen(e)}
          className="-mx-1 -my-2 block min-h-11 min-w-0 max-w-full px-1 py-2 text-left underline decoration-transparent underline-offset-4 hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
        >
          <span className="block truncate font-medium text-ink">{e.action}</span>
          <span className="block truncate break-words font-mono text-[12px] text-muted">
            {e.targetId ? `${e.targetType} ${e.targetId}` : e.targetType}
          </span>
        </button>
      ),
    },
    // The chain position, and the number somebody quotes when reporting a break
    // in the chain.
    { key: "seq", header: "Seq", numeric: true, cell: (e) => e.seq.toLocaleString() },
    { key: "operator", header: "Operator", cell: (e) => e.actor },
    {
      key: "organization",
      header: "Organization",
      // An installation-wide action names no tenant, and that is a REAL ANSWER
      // rather than missing data. A blank cell would read as a bug and a dash
      // would make the reader infer which of the two it is, so the cell says it
      // outright. This is the whole reason the operator chain is a separate
      // table from any tenant's.
      cell: (e) => e.organization ?? <span className="text-muted">Platform-wide</span>,
    },
    {
      key: "severity",
      header: "Severity",
      cell: (e) => <StatusChip value={e.severity} tone={toneOf(e.severity)} />,
    },
    { key: "when", header: "When", cell: (e) => <When value={e.occurredAt} /> },
  ];

  return (
    <AdminPage
      href="/admin/security/audit"
      lede="Every action taken from this portal, newest first. Where an action concerned one organization, that customer has the same entry in their own audit log."
    >
      {mayExport ? <ChainTools severity={severity} /> : null}

      <Card>
        <FilterBar
          filters={[
            {
              label: "Severity",
              value: severity,
              onChange: setSeverity,
              options: SEVERITIES,
            },
          ]}
        />
        <Loaded state={state} skeleton={<TableSkeleton rows={8} cols={5} />}>
          {(rows) => (
            <DataTable
              columns={columns}
              rows={rows}
              keyOf={(e) => String(e.seq)}
              empty={
                <EmptyList title={severity ? "Nothing at that severity" : "Nothing recorded yet"}>
                  {severity
                    ? "No operator action has been recorded at that severity. Choose all severities to see the whole log."
                    : "No operator has done anything on this installation yet. Signing in is itself recorded, so this fills as soon as anybody uses the portal."}
                </EmptyList>
              }
              footer={
                <More
                  shown={rows.length}
                  noun={{ one: "entry", many: "entries" }}
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

      <Drawer
        open={open !== null}
        title={open ? open.action : "Entry"}
        onClose={() => setOpen(null)}
      >
        {open ? <EntryDetail entry={open} /> : null}
      </Drawer>
    </AdminPage>
  );
}

/**
 * Checking the chain, and taking a copy of it.
 *
 * Rendered only for an operator who holds admin.audit.export. The navigation
 * already hides what a role cannot reach and the server refuses it anyway, so
 * this is the third layer and the one that stops the page offering a button
 * that answers 403.
 */
function ChainTools({ severity }: { severity: string }) {
  const [report, setReport] = useState<ChainReport | null>(null);
  const [verifying, setVerifying] = useState(false);
  const [exporting, setExporting] = useState<"json" | "csv" | null>(null);
  const [saved, setSaved] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  function fail(err: unknown) {
    setError(err instanceof ApiError ? err.message : "The control plane could not be reached.");
  }

  async function verify() {
    setVerifying(true);
    setError(null);
    setSaved(null);
    try {
      setReport(await verifyChain());
    } catch (err) {
      fail(err);
    } finally {
      setVerifying(false);
    }
  }

  async function download(format: "json" | "csv") {
    setExporting(format);
    setError(null);
    setSaved(null);
    try {
      // A ceiling rather than the whole chain, and the file says whether it hit
      // it. A truncated export read as the whole log is somebody telling a
      // regulator that this is everything.
      const file = await exportChain({
        format,
        limit: 5000,
        ...(severity ? { severity } : {}),
      });
      saveDocument(file);
      setSaved(
        file.truncated
          ? `Saved ${file.filename}. It stopped at ${file.entryCount.toLocaleString()} entries, which is the ceiling, so it is not the whole chain.`
          : `Saved ${file.filename}, ${file.entryCount.toLocaleString()} entries, verified intact.`,
      );
      if (!file.verification.ok) {
        setReport({
          ok: false,
          entries: file.verification.entriesWalked,
          firstSeq: file.firstSeq,
          lastSeq: file.lastSeq,
          head: null,
          problems: file.verification.problems as ChainReport["problems"],
        });
      }
    } catch (err) {
      fail(err);
    } finally {
      setExporting(null);
    }
  }

  return (
    <Card
      className="mb-6"
      title="The chain itself"
      note="Every entry is hashed with the one before it. Verifying recomputes them; the export carries the hashes so somebody outside this company can do the same."
      actions={
        <>
          <Button onClick={() => void verify()} busy={verifying}>
            Verify
          </Button>
          <Button onClick={() => void download("json")} busy={exporting === "json"}>
            Export JSON
          </Button>
          <Button onClick={() => void download("csv")} busy={exporting === "csv"}>
            Export CSV
          </Button>
        </>
      }
    >
      <div className="px-4 py-3.5">
        {error ? (
          <p role="alert" className="text-[13px] leading-6 text-fail">
            {error}
          </p>
        ) : report === null ? (
          <p className="max-w-[72ch] text-[13px] leading-6 text-muted">
            {saved ??
              (severity
                ? `The export covers the ${severity} entries only, and says so inside the file. Verification always covers the whole range those entries span.`
                : "Nothing has been checked yet. Verifying walks every entry and reports every break rather than the first, because an investigation wants the extent of the tampering.")}
          </p>
        ) : report.ok ? (
          <p role="status" className="flex flex-wrap items-center gap-2 text-[13px] text-muted">
            <Badge tone="pass">Intact</Badge>
            <span>
              {report.entries.toLocaleString()} entries hash to what they say they do, from{" "}
              {(report.firstSeq ?? 0).toLocaleString()} to {(report.lastSeq ?? 0).toLocaleString()}.
            </span>
          </p>
        ) : (
          <div role="alert">
            <p className="flex flex-wrap items-center gap-2 text-[13px] text-ink">
              <Badge tone="fail">Broken</Badge>
              <span>
                {report.problems.length.toLocaleString()} of {report.entries.toLocaleString()}{" "}
                entries do not check out. Every break is listed rather than the first, because the
                first one only says where it started.
              </span>
            </p>
            <ul className="mt-2 grid gap-1.5">
              {report.problems.map((p) => (
                <li key={`${p.seq}-${p.kind}`} className="max-w-[80ch] text-[12.5px] leading-5 text-muted">
                  <span className="font-mono text-fail">
                    {p.seq.toLocaleString()} {p.kind.replace(/_/g, " ")}
                  </span>{" "}
                  {p.detail}
                </li>
              ))}
            </ul>
          </div>
        )}
        {saved && report !== null ? (
          <p role="status" className="mt-2 text-[13px] text-muted">
            {saved}
          </p>
        ) : null}
      </div>
    </Card>
  );
}

/** One entry in full, including the part the table has no column for. */
function EntryDetail({ entry }: { entry: AdminAuditEntry }) {
  return (
    <>
      <Facts
        facts={[
          { label: "Chain position", value: entry.seq.toLocaleString(), mono: true },
          { label: "Operator", value: entry.actor },
          {
            label: "Organization",
            value: entry.organization ?? <span className="text-muted">Platform-wide</span>,
          },
          { label: "Target", value: entry.targetType },
          { label: "Target id", value: entry.targetId, mono: true },
          {
            label: "Severity",
            value: <StatusChip value={entry.severity} tone={toneOf(entry.severity)} />,
          },
          { label: "When", value: <When value={entry.occurredAt} /> },
        ]}
      />
      <div className="border-t border-rule px-4 py-4">
        <h3 className="text-[12px] font-medium text-dim">What changed</h3>
        {entry.detail === null || entry.detail === undefined ? (
          <p className="mt-2 text-[13px] leading-6 text-muted">
            This entry carries no detail. That is the record as written, not a failure to load it.
          </p>
        ) : (
          // Shown as it was recorded, rather than pulled apart into fields.
          // The shape differs per action, and a renderer that guessed at it
          // would silently drop the keys it did not expect, which on an audit
          // log is the one thing that must not happen. It scrolls in its own
          // box so a long entry cannot push the panel sideways.
          <pre className="scroll-x mt-2 rounded-md border border-rule bg-paper p-3 font-mono text-[12px] leading-5 text-ink">
            {JSON.stringify(entry.detail, null, 2)}
          </pre>
        )}
      </div>
    </>
  );
}
