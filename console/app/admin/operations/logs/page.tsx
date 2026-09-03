"use client";

/**
 * Logs & Error Explorer.
 *
 * WHAT THIS PAGE IS HONEST ABOUT, before anything else. There is no
 * error-tracking table in this product. No exception group, no fingerprint, no
 * occurrence counter, no stack trace store, no log line store. Anything here
 * resembling Sentry would be invented, and an invented error explorer is worse
 * than none: an operator reads "0 errors" during an incident and stops looking.
 *
 * So this is built out of what the control plane genuinely records, and it says
 * so on the page rather than only in this comment:
 *
 *   workload_runs.failure_code   grouped, which is the closest thing to a
 *                                fingerprint this product has.
 *   verdicts                     which workflows are failing, across tenants.
 *   events                       what is arriving, how fast, and how far behind.
 *
 * That turns out to answer the question an operator actually opens this page
 * with, which is not "show me a stack trace" but "what is failing right now,
 * how many customers does it touch, and did it start when we deployed".
 *
 * THE EVENT STREAM SHOWS SHAPE AND TIMING AND NEVER A PAYLOAD VALUE. The
 * payload is the customer's data. The route returns the key names and the byte
 * size, the page says that out loud, and the boundary is the column list in the
 * query, because row level security cannot restrict a column and the operator
 * pool bypasses it anyway.
 */

import { useState } from "react";
import {
  Badge,
  Button,
  Card,
  CardSkeleton,
  Loaded,
  TableSkeleton,
  When,
} from "@/components/ui";
import { More } from "@/components/pagination";
import {
  AdminPage,
  DataTable,
  EmptyList,
  FilterBar,
  MetricRow,
  StatusChip,
  type Column,
} from "@/components/admin/primitives";
import {
  WINDOWS,
  useEventStream,
  useLogsOverview,
  type EventRow,
  type EventTypeSummary,
  type FailureGroup,
  type WorkflowFailure,
} from "@/lib/admin-operations";

export default function OperationsLogsPage() {
  const [hours, setHours] = useState("24");
  const [type, setType] = useState("");
  const overview = useLogsOverview(hours, "");
  const stream = useEventStream(hours, type, "");

  return (
    <AdminPage
      href="/admin/operations/logs"
      lede="What is failing across every organization, and what is arriving from the engines. Built from run outcomes and the event stream, which is what this control plane records."
      actions={
        <Button
          onClick={() => {
            overview.reload();
            stream.reload();
          }}
        >
          Refresh
        </Button>
      }
    >
      <div className="space-y-6">
        <Card>
          <FilterBar
            filters={[
              {
                label: "Window",
                value: hours,
                onChange: (v) => {
                  setHours(v);
                  // The type filter is cleared with the window on purpose: a
                  // type that had traffic in the last hour may have none in the
                  // last week, and a stream filtered to nothing under a
                  // freshly-widened window reads as "the widening broke it".
                  setType("");
                },
                options: WINDOWS.map((w) => ({ value: w.value, label: w.label })),
              },
            ]}
          />
          <Loaded state={overview} skeleton={<CardSkeleton count={1} />}>
            {(o) => (
              <div className="px-4 py-4">
                <MetricRow
                  metrics={[
                    {
                      label: "Failing runs",
                      value: o.failures.reduce((n, f) => n + f.runs, 0),
                      note: o.truncated.failures
                        ? `across the top ${o.limit} groups, which is a cut list`
                        : `across ${o.failures.length} ${o.failures.length === 1 ? "group" : "groups"}`,
                    },
                    {
                      label: "Workflows failing",
                      value: o.workflows.length,
                      note: o.truncated.workflows ? `cut at ${o.limit}` : "distinct workflows",
                    },
                    {
                      label: "Events",
                      value: o.eventTypes.reduce((n, t) => n + t.events, 0),
                      note: `across ${o.eventTypes.length} ${o.eventTypes.length === 1 ? "type" : "types"}`,
                    },
                    {
                      label: "Worst ingestion lag",
                      value:
                        o.eventTypes.length === 0
                          ? null
                          : Math.max(...o.eventTypes.map((t) => t.lagSeconds)),
                      unit: "seconds",
                      note: "engine stamp to arrival here",
                    },
                  ]}
                />
              </div>
            )}
          </Loaded>
        </Card>

        <FailuresCard state={overview} />
        <WorkflowsCard state={overview} />
        <EventTypesCard state={overview} selected={type} onSelect={setType} />
        <StreamCard state={stream} type={type} onClearType={() => setType("")} />
        <WhatIsNotRecorded />
      </div>
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * Failures, grouped by code
 * ---------------------------------------------------------------------- */

const FAILURE_COLUMNS: Column<FailureGroup>[] = [
  {
    key: "code",
    header: "Failure",
    cell: (f) => (
      <>
        <span className="block truncate font-mono text-[12px] font-medium text-ink">
          {/* Null is a real answer and gets its own row. A run that ends failed
              having recorded no code is a gap in the engine, and folding those
              rows into the others would hide it. */}
          {f.failureCode ?? "no code recorded"}
        </span>
        <span className="mt-0.5 block text-[12px] text-dim">
          {f.kind.replace(/_/g, " ")}
        </span>
        {f.latestDetail ? (
          <span className="mt-1 block max-w-[56ch] break-words text-[12.5px] leading-5 text-muted">
            {f.latestDetail}
          </span>
        ) : null}
      </>
    ),
  },
  { key: "state", header: "Ended as", cell: (f) => <StatusChip value={f.state} /> },
  { key: "runs", header: "Runs", numeric: true, cell: (f) => f.runs.toLocaleString() },
  {
    key: "organizations",
    header: "Tenants",
    numeric: true,
    cell: (f) => f.organizations.toLocaleString(),
  },
  { key: "first", header: "First seen", cell: (f) => <When value={f.firstSeen} /> },
  { key: "last", header: "Last seen", cell: (f) => <When value={f.lastSeen} /> },
];

function FailuresCard({ state }: { state: ReturnType<typeof useLogsOverview> }) {
  return (
    <Card
      title="Failures by code"
      note="Workload runs that ended failed, timed out or abandoned. All three are failures to the person whose test did not run."
    >
      <Loaded state={state} skeleton={<TableSkeleton rows={5} cols={6} />}>
        {(o) => (
          <DataTable
            columns={FAILURE_COLUMNS}
            rows={o.failures}
            keyOf={(f) => `${f.failureCode ?? "none"}:${f.kind}:${f.state}`}
            empty={
              <EmptyList title="Nothing failed in this window">
                No workload run ended failed, timed out or abandoned in the period selected above.
                Widen the window if you are looking for something older.
              </EmptyList>
            }
            footer={<Cut shown={o.failures.length} limit={o.limit} cut={o.truncated.failures} noun={{ one: "group", many: "groups" }} />}
          />
        )}
      </Loaded>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * Workflows
 * ---------------------------------------------------------------------- */

const WORKFLOW_COLUMNS: Column<WorkflowFailure>[] = [
  {
    key: "workflow",
    header: "Workflow",
    cell: (w) => (
      <>
        <span className="block truncate font-medium text-ink">{w.workflow}</span>
        {w.latestSummary ? (
          <span className="mt-1 block max-w-[56ch] break-words text-[12.5px] leading-5 text-muted">
            {w.latestSummary}
          </span>
        ) : null}
      </>
    ),
  },
  { key: "value", header: "Verdict", cell: (w) => <StatusChip value={w.value} /> },
  { key: "runs", header: "Verdicts", numeric: true, cell: (w) => w.runs.toLocaleString() },
  {
    key: "organizations",
    header: "Tenants",
    numeric: true,
    cell: (w) => w.organizations.toLocaleString(),
  },
  { key: "last", header: "Last seen", cell: (w) => <When value={w.lastSeen} /> },
];

function WorkflowsCard({ state }: { state: ReturnType<typeof useLogsOverview> }) {
  return (
    <Card
      title="Workflows failing"
      note="One workflow failing for several tenants at once is a product fault rather than a customer's."
    >
      <Loaded state={state} skeleton={<TableSkeleton rows={4} cols={5} />}>
        {(o) => (
          <DataTable
            columns={WORKFLOW_COLUMNS}
            rows={o.workflows}
            keyOf={(w) => `${w.workflow}:${w.value}`}
            empty={
              <EmptyList title="No workflow failed or was blocked">
                Every verdict recorded in this window passed, was flaky, or went unverified. A
                blocked workflow would appear here too, since a workflow nothing could run is not a
                workflow that passed.
              </EmptyList>
            }
            footer={<Cut shown={o.workflows.length} limit={o.limit} cut={o.truncated.workflows} noun={{ one: "workflow", many: "workflows" }} />}
          />
        )}
      </Loaded>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * Event types
 * ---------------------------------------------------------------------- */

function EventTypesCard({
  state,
  selected,
  onSelect,
}: {
  state: ReturnType<typeof useLogsOverview>;
  selected: string;
  onSelect: (type: string) => void;
}) {
  const columns: Column<EventTypeSummary>[] = [
    {
      key: "type",
      header: "Event type",
      cell: (t) => (
        // A real button, not a clickable row or a div with an onClick. It is
        // focusable, Enter activates it, and a screen reader is told it does
        // something.
        <button
          type="button"
          onClick={() => onSelect(selected === t.type ? "" : t.type)}
          aria-pressed={selected === t.type}
          className="-mx-1 -my-2 inline-flex min-h-11 items-center px-1 py-2 text-left font-mono text-[12px] font-medium text-ink underline decoration-transparent underline-offset-4 hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
        >
          {t.type}
        </button>
      ),
    },
    { key: "events", header: "Events", numeric: true, cell: (t) => t.events.toLocaleString() },
    {
      key: "organizations",
      header: "Tenants",
      numeric: true,
      cell: (t) => t.organizations.toLocaleString(),
    },
    {
      key: "lag",
      header: "Lag",
      numeric: true,
      cell: (t) => (
        <>
          <span className={t.lagSeconds >= 600 ? "text-fail" : ""}>
            {t.lagSeconds.toLocaleString()}
          </span>
          <span className="block text-[12px] text-dim">seconds</span>
        </>
      ),
    },
    { key: "last", header: "Last arrival", cell: (t) => <When value={t.lastReceivedAt} /> },
  ];

  return (
    <Card
      title="What is arriving"
      note="Lag is the gap between the engine stamping an event and this control plane receiving it. Either timestamp alone looks fine while the pair is wrong."
      actions={
        selected ? (
          <Button onClick={() => onSelect("")}>Clear filter</Button>
        ) : null
      }
    >
      <Loaded state={state} skeleton={<TableSkeleton rows={5} cols={5} />}>
        {(o) => (
          <DataTable
            columns={columns}
            rows={o.eventTypes}
            keyOf={(t) => t.type}
            empty={
              <EmptyList title="No events arrived in this window">
                Nothing has been received from any engine in the period selected above. On a live
                installation that is an ingestion fault rather than a quiet day, and the system
                health check for ingestion lag on the Infrastructure page is the next place to
                look.
              </EmptyList>
            }
            footer={<Cut shown={o.eventTypes.length} limit={o.limit} cut={o.truncated.eventTypes} noun={{ one: "type", many: "types" }} />}
          />
        )}
      </Loaded>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * The stream
 * ---------------------------------------------------------------------- */

const EVENT_COLUMNS: Column<EventRow>[] = [
  {
    key: "type",
    header: "Event",
    cell: (e) => (
      <>
        <span className="block truncate font-mono text-[12px] font-medium text-ink">{e.type}</span>
        <span className="block truncate text-[12px] text-muted">{e.orgSlug}</span>
      </>
    ),
  },
  { key: "occurred", header: "Occurred", cell: (e) => <When value={e.occurredAt} /> },
  { key: "received", header: "Received", cell: (e) => <When value={e.receivedAt} /> },
  {
    key: "env",
    header: "Environment",
    mono: true,
    cell: (e) => e.envId ?? <span className="text-dim">none</span>,
  },
  {
    key: "fields",
    header: "Fields",
    cell: (e) =>
      e.payloadKeys.length === 0 ? (
        <span className="text-dim">empty payload</span>
      ) : (
        <span className="block max-w-[36ch] break-words font-mono text-[12px] text-muted">
          {e.payloadKeys.join(", ")}
        </span>
      ),
  },
  {
    key: "bytes",
    header: "Payload",
    numeric: true,
    cell: (e) => (
      <>
        <span className="block">{e.payloadBytes.toLocaleString()}</span>
        <span className="block text-[12px] text-dim">bytes</span>
      </>
    ),
  },
];

function StreamCard({
  state,
  type,
  onClearType,
}: {
  state: ReturnType<typeof useEventStream>;
  type: string;
  onClearType: () => void;
}) {
  return (
    <Card
      title="Event stream"
      note={
        type
          ? `Filtered to ${type}. Field names and payload size only; the values are never returned.`
          : "Newest first. Field names and payload size only; the values are never returned."
      }
      actions={type ? <Button onClick={onClearType}>Clear filter</Button> : null}
    >
      <Loaded state={state} skeleton={<TableSkeleton rows={6} cols={6} />}>
        {(rows) => (
          <DataTable
            columns={EVENT_COLUMNS}
            rows={rows}
            keyOf={(e) => e.id}
            empty={
              <EmptyList
                title={type ? `No ${type} events in this window` : "No events in this window"}
                action={type ? <Button onClick={onClearType}>Clear the filter</Button> : undefined}
              >
                {type
                  ? "That type had traffic when the summary above was computed, or it was chosen from a wider window. Clear the filter or widen the window."
                  : "Nothing has arrived from any engine in the period selected above."}
              </EmptyList>
            }
            footer={
              <More
                shown={rows.length}
                noun={{ one: "event", many: "events" }}
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
 * The honest footer
 * ---------------------------------------------------------------------- */

/**
 * What this product does not record, named.
 *
 * On the page rather than only in a comment, because the reader who needs it is
 * the operator who came here looking for a stack trace and is about to conclude
 * there were no errors.
 */
function WhatIsNotRecorded() {
  return (
    <Card title="What this page cannot show you">
      <div className="space-y-3 px-4 py-4 text-[13px] leading-6 text-muted">
        <p className="max-w-[70ch]">
          There is no error-tracking store in this product. Nothing records an exception, a stack
          trace, a fingerprint, an occurrence count or a log line. Everything above is arithmetic
          over run outcomes and the event stream, which is what the control plane genuinely writes.
        </p>
        <p className="max-w-[70ch]">
          Event payloads are deliberately not returned. The payload is the customer&apos;s data:
          request bodies, database values, whatever the engine observed. The field names and the
          size are enough to tell whether ingestion is working and what shape is arriving, and they
          are not a copy of a tenant&apos;s data sitting in an operator&apos;s browser. Every read
          on this page is recorded against the operator who made it.
        </p>
        <p className="max-w-[70ch] text-dim">
          <Badge tone="neutral">what would change this</Badge> Grouped exceptions would need a
          table with a fingerprint, an occurrence count and a stack trace, plus something on the
          engine side that reports them. That is a schema change and an engine change, not a page.
        </p>
      </div>
    </Card>
  );
}

/**
 * The footer for an aggregate that has no cursor.
 *
 * A GROUP BY has no stable keyset to page through: the counts move between
 * calls, so a cursor into them would skip and repeat. So these lists are capped
 * on the server and this says when the cap was hit, rather than letting a cut
 * list read as the whole answer.
 */
function Cut({
  shown,
  limit,
  cut,
  noun,
}: {
  shown: number;
  limit: number;
  cut: boolean;
  noun: { one: string; many: string };
}) {
  const things = `${shown} ${shown === 1 ? noun.one : noun.many}`;
  return (
    <div className="border-t border-rule px-4 py-3">
      <span className="text-[12.5px] text-muted">
        {cut
          ? `The busiest ${things}. There are more: this is an aggregate, so it is capped at ${limit} rather than paged. Narrow the window to see further down the list.`
          : `All ${things}.`}
      </span>
    </div>
  );
}
