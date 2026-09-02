"use client";

import { useState } from "react";
import {
  Badge,
  Card,
  Empty,
  Loaded,
  Row,
  Table,
  TableSkeleton,
  TableWrap,
  Td,
  Th,
  When,
  selectClass,
  type Tone,
} from "@/components/ui";
import { AdminPage } from "@/components/admin/primitives";
import { More } from "@/components/pagination";
import { useAdminAudit, type AdminAuditEntry } from "@/lib/admin";

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
 */

/** Severity to the console's existing tones, rather than four new colours.
 *  `notice` is deliberately neutral: it is the level for a refusal that is
 *  ordinary, and colouring every refusal amber trains people to ignore amber. */
function toneOf(severity: AdminAuditEntry["severity"]): Tone {
  if (severity === "critical" || severity === "high") return "fail";
  if (severity === "notice") return "neutral";
  return "neutral";
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
  const state = useAdminAudit(severity);

  return (
    <AdminPage
      href="/admin/security/audit"
      lede="Every action taken from this portal, newest first. Where an action concerned one organization, that customer has the same entry in their own audit log."
      actions={
        <label className="flex min-w-0 items-center gap-2">
          <span className="sr-only">Filter by severity</span>
          <select
            className={selectClass}
            value={severity}
            onChange={(e) => setSeverity(e.target.value)}
          >
            {SEVERITIES.map((s) => (
              <option key={s.value} value={s.value}>
                {s.label}
              </option>
            ))}
          </select>
        </label>
      }
    >
      <Card>
        <Loaded state={state} skeleton={<TableSkeleton rows={8} cols={5} />}>
          {(rows) =>
            rows.length === 0 ? (
              <Empty title={severity ? "Nothing at that severity" : "Nothing recorded yet"}>
                {severity
                  ? "No operator action has been recorded at that severity. Choose all severities to see the whole log."
                  : "No operator has done anything on this installation yet. Signing in is itself recorded, so this fills as soon as anybody uses the portal."}
              </Empty>
            ) : (
              <TableWrap>
                <Table>
                  <thead>
                    <tr>
                      <Th numeric>Seq</Th>
                      <Th>Action</Th>
                      <Th>Operator</Th>
                      <Th>Organization</Th>
                      <Th>Severity</Th>
                      <Th>When</Th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((e) => (
                      <Row key={e.seq}>
                        {/* The chain position. Numeric so the column lines up,
                            and it is the number somebody quotes when reporting
                            a break in the chain. */}
                        <Td label="Seq" numeric>
                          {e.seq.toLocaleString()}
                        </Td>
                        <Td>
                          <span className="block truncate font-medium text-ink">{e.action}</span>
                          {e.targetId ? (
                            // truncate keeps this to one line in the table,
                            // but the table stacks into records on a phone
                            // where there is no column to truncate against, so
                            // break-words is what stops a long target id
                            // widening the record. Same defect as the
                            // suspension reason, one screen over.
                            <span className="block truncate break-words font-mono text-[12px] text-muted">
                              {e.targetType} {e.targetId}
                            </span>
                          ) : (
                            <span className="block text-[12px] text-muted">{e.targetType}</span>
                          )}
                        </Td>
                        <Td label="Operator">{e.actor}</Td>
                        <Td label="Organization">
                          {/* An installation-wide action names no tenant, and
                              that is a REAL ANSWER rather than missing data.
                              A blank cell would read as a bug and a dash would
                              make the reader infer which of the two it is, so
                              the cell says the thing outright. This is the
                              whole reason the operator chain is a separate
                              table from any tenant's. */}
                          {e.organization ?? (
                            <span className="text-muted">Platform-wide</span>
                          )}
                        </Td>
                        <Td label="Severity">
                          <Badge tone={toneOf(e.severity)}>{e.severity}</Badge>
                        </Td>
                        <Td label="When">
                          <When value={e.occurredAt} />
                        </Td>
                      </Row>
                    ))}
                  </tbody>
                </Table>
                <More
                  shown={rows.length}
                  noun={{ one: "entry", many: "entries" }}
                  hasMore={state.hasMore}
                  busy={state.busy}
                  error={state.moreError}
                  onMore={state.more}
                />
              </TableWrap>
            )
          }
        </Loaded>
      </Card>
    </AdminPage>
  );
}
