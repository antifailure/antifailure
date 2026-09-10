"use client";

/**
 * Logs & Error Explorer.
 *
 * WHAT THIS PAGE IS HONEST ABOUT, before anything else. There is still no
 * error-tracking table for the ENGINE in this product. No exception group for a
 * customer's run, no stack trace store, no log line store. Anything here
 * resembling Sentry for the engine would be invented, and an invented error
 * explorer is worse than none: an operator reads "0 errors" during an incident
 * and stops looking.
 *
 * WHAT CHANGED, and the distinction is the whole of it. That statement was
 * always about the engine's exceptions, which need the engine to report them.
 * It was also true of the control plane's OWN failures, and those need nothing
 * from anybody: this process already catches every one of them in two handlers
 * that had already decided what is safe to write down, and threw the result
 * away everywhere except a container's stdout. A self hoster with no log
 * aggregation could see `af_http_requests_total{status_class="5xx"}` go up and
 * could not see what any of it was. So there is now a real store, it is a
 * GROUP per fingerprint rather than a log line, and the "What this page cannot
 * show you" card below says exactly where its edge is.
 *
 * So this is built out of what the control plane genuinely records:
 *
 *   control_plane_failures       the control plane's own failures, grouped by a
 *                                fingerprint over the declared route, the
 *                                method, the error class and the driver code.
 *   workload_runs.failure_code   grouped, which is the closest thing to a
 *                                fingerprint the ENGINE's side has.
 *   verdicts                     which workflows are failing, across tenants.
 *   events                       what is arriving, how fast, and how far behind.
 *
 * Between them they answer the question an operator actually opens this page
 * with, which is not "show me a stack trace" but "what is failing right now,
 * how many customers does it touch, and did it start when we deployed".
 *
 * THE EVENT STREAM SHOWS SHAPE AND TIMING AND NEVER A PAYLOAD VALUE. The
 * payload is the customer's data. The route returns the key names and the byte
 * size, the page says that out loud, and the boundary is the column list in the
 * query, because row level security cannot restrict a column and the operator
 * pool bypasses it anyway. The failure store inherits exactly that standard:
 * no message, no stack, no payload, and the boundary is the signature of
 * `recordFailure` rather than a policy.
 *
 * WHY THIS PAGE POLLS RATHER THAN HOLDING A STREAM OPEN. Server sent events
 * were the alternative and they lose here on two facts about this deployment.
 * The control plane runs as a container app in multiple revision mode with more
 * than one replica behind one ingress, so a held connection pins the operator
 * to whichever replica answered and dies on every revision swap, which is
 * exactly the moment an operator is watching. And everything on this page is
 * already an aggregate over Postgres, which every replica shares, so a poll
 * gets the same answer from any of them and gains the reconnection behaviour
 * for free. The cost is latency bounded by the interval, which for a page
 * measuring things in seconds and minutes is not a cost.
 *
 * WHAT THE LIVE TICK DELIBERATELY DOES NOT REFRESH: the event stream, because
 * it is paged. Refreshing a list somebody has pressed More on three times
 * throws away three pages and puts them back at the top, which is worse than
 * being a minute behind. It keeps its own Refresh.
 */

import { useCallback, useState } from "react";
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
import { useInterval } from "@/components/load/polling";
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
  startedInOneBuild,
  useEventStream,
  useFailureStore,
  useLogsOverview,
  type ControlPlaneFailure,
  type EventRow,
  type EventTypeSummary,
  type FailureGroup,
  type FailureStoreStatus,
  type FailureStoreView,
  type WorkflowFailure,
} from "@/lib/admin-operations";

/**
 * How often the page asks again.
 *
 * The same ten seconds the control plane flushes its failure buffer at, so a
 * failure is on screen within about two ticks of happening. Faster would be
 * asking for an answer that has not changed; slower and an operator watching an
 * incident start would be watching a page that says nothing is wrong.
 */
const LIVE_MS = 10_000;

export default function OperationsLogsPage() {
  const [hours, setHours] = useState("24");
  const [type, setType] = useState("");
  const [live, setLive] = useState(true);
  const overview = useLogsOverview(hours, "");
  const failures = useFailureStore(hours);
  const stream = useEventStream(hours, type, "");

  // The two aggregates only. The stream is paged and keeps its own Refresh; see
  // the header for why refreshing it on a tick would be a regression.
  const tick = useCallback(() => {
    overview.reload();
    failures.reload();
  }, [overview, failures]);

  useInterval(live, LIVE_MS, tick);

  // The freshness claim comes from the control plane's OWN clock, carried back
  // in `at`, rather than from `new Date()` here. Every timestamp in the rows
  // below is on that clock, so a browser whose clock is minutes out would
  // otherwise be told the rows are from the future.
  const at = failures.data?.status ? failures.data.at : (overview.data?.at ?? null);
  const refreshFailed = failures.refreshError ?? overview.refreshError;

  return (
    <AdminPage
      href="/admin/operations/logs"
      lede="What is failing across every organization, and what is arriving from the engines. Built from the control plane's own failures, run outcomes and the event stream, which is what this installation records."
      actions={
        <>
          <Button
            pressed={live}
            onClick={() => setLive((on) => !on)}
            aria-label={live ? "Turn live updates off" : "Turn live updates on"}
          >
            {live ? "Live updates on" : "Live updates off"}
          </Button>
          <Button
            onClick={() => {
              tick();
              stream.reload();
            }}
            busy={overview.refreshing || failures.refreshing}
          >
            Refresh
          </Button>
        </>
      }
    >
      <div className="space-y-6">
        <Freshness at={at} live={live} failed={refreshFailed !== null} />

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
                    // FIRST, and it was not here when this row was first drawn.
                    // Looking at the rendered page found the defect: the four
                    // numbers below are all about the engine side, so an
                    // installation whose CONTROL PLANE was failing two hundred
                    // times an hour showed four zeros at the top of the page,
                    // which is the exact "0 errors during an incident" reading
                    // this page's header exists to prevent.
                    //
                    // Null rather than zero until the store has answered, which
                    // is what Metric prints as "Not measured": a zero here
                    // before the second request lands would be a reassurance
                    // nobody measured.
                    //
                    // "This control plane" rather than "Control plane
                    // failures", which is not a wording preference: the longer
                    // label wraps to two lines in this column while the other
                    // four do not, and the number under it then sits a line
                    // lower than every number beside it. The word "failures"
                    // lives in the note, where it has room.
                    {
                      label: "This control plane",
                      value: failuresInWindow(failures.data),
                      note: noteForFailures(failures.data),
                    },
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

        <ControlPlaneFailuresCard state={failures} />
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
 * The headline number for the store
 * ---------------------------------------------------------------------- */

/**
 * Occurrences in the window, or null when the store has not answered yet.
 *
 * Null and zero are different answers and this is the place that refuses to
 * confuse them: `Metric` prints null as "Not measured" and zero as a zero, and
 * a zero printed before the store has replied is a reassurance nobody measured.
 */
function failuresInWindow(view: FailureStoreView | null): number | null {
  if (!view) return null;
  return view.failures.reduce((n, f) => n + f.occurrences, 0);
}

/** What the number is over, including the two ways it is short of the truth. */
function noteForFailures(view: FailureStoreView | null): string {
  if (!view) return "failures it caught in itself";
  if (!view.status.recording) return "failures: nothing is being recorded on this replica";
  if (view.truncated) {
    return `failures, across the ${view.limit} most recent groups, which is a cut list`;
  }
  const groups = view.failures.length;
  return `failures it caught in itself, across ${groups} ${groups === 1 ? "group" : "groups"}`;
}

/* -------------------------------------------------------------------------
 * Freshness
 * ---------------------------------------------------------------------- */

/**
 * How old the numbers are, said in words, once for the page.
 *
 * DELIBERATELY NOT A LIVE INDICATOR. No dot, nothing pulsing, nothing pinging.
 * A throbbing badge on a page that is working correctly is an animation running
 * forever while the reader does nothing, and it says less than the sentence
 * does: "Updated 4s ago" is the actual freshness, which is the thing an
 * operator has to decide with, and a green dot is not.
 *
 * AND DELIBERATELY NOT A SECOND `StaleNotice`. The first version put one at the
 * top of the page as well, and the rendered result said the same thing three
 * times: `Loaded` already puts "Could not refresh. Showing the last answer."
 * with a retry on every card whose reload failed, which is the house treatment
 * and is beside the data it qualifies. What was missing above the fold was not
 * another banner but the timestamp, so that is all this is, with one clause
 * that changes when a refresh has failed.
 *
 * `When` renders both the relative and the absolute time, the latter as the
 * title and the accessible label, so "4s ago" can be compared against a log
 * line rather than only felt.
 */
function Freshness({
  at,
  live,
  failed,
}: {
  at: string | null;
  live: boolean;
  failed: boolean;
}) {
  if (!at) {
    return (
      <p role="status" className="text-[12.5px] leading-6 text-muted">
        Nothing has loaded yet.
      </p>
    );
  }
  return (
    <p role="status" className={`text-[12.5px] leading-6 ${failed ? "text-warn" : "text-muted"}`}>
      Updated <When value={at} />, by the control plane&apos;s clock.{" "}
      {failed
        ? `The last refresh did not land, so these are the last good numbers. It keeps trying every ${LIVE_MS / 1000} seconds.`
        : live
          ? `Asking again every ${LIVE_MS / 1000} seconds while this tab is in front. The event stream below is paged and refreshes only when you ask it to.`
          : "Live updates are off, so these numbers stay as they are until you press Refresh."}
    </p>
  );
}

/* -------------------------------------------------------------------------
 * The control plane's own failures
 * ---------------------------------------------------------------------- */

const CONTROL_PLANE_COLUMNS: Column<ControlPlaneFailure>[] = [
  {
    key: "what",
    header: "Failure",
    cell: (f) => (
      <>
        <span className="block truncate font-mono text-[12px] font-medium text-ink">{f.kind}</span>
        <span className="mt-0.5 block truncate font-mono text-[12px] text-muted">{f.route}</span>
        {f.providerCode ? (
          <span className="mt-0.5 block font-mono text-[12px] text-dim">
            driver code {f.providerCode}
          </span>
        ) : null}
      </>
    ),
  },
  {
    key: "where",
    header: "Caught by",
    cell: (f) => (
      <>
        <StatusChip value={f.source === "trpc" ? "tRPC" : "HTTP"} tone="neutral" />
        <span className="mt-0.5 block font-mono text-[12px] text-dim">{f.method}</span>
      </>
    ),
  },
  {
    key: "occurrences",
    header: "Occurrences",
    numeric: true,
    cell: (f) => f.occurrences.toLocaleString(),
  },
  {
    key: "build",
    header: "Build",
    cell: (f) => (
      <>
        <span className="block font-mono text-[12px] text-ink">{f.lastSeenVersion}</span>
        {/* The deploy overlay, as a comparison rather than a chart. There is no
            deployment table in this product, so a line on a graph would be
            invented. Two builds named is the honest form of the same answer. */}
        <span className="mt-0.5 block text-[12px] text-dim">
          {startedInOneBuild(f)
            ? "first seen on this build"
            : `first seen on ${f.firstSeenVersion}`}
        </span>
      </>
    ),
  },
  { key: "first", header: "First seen", cell: (f) => <When value={f.firstSeen} /> },
  { key: "last", header: "Last seen", cell: (f) => <When value={f.lastSeen} /> },
  {
    key: "request",
    header: "Latest request id",
    mono: true,
    cell: (f) =>
      f.lastRequestId ? (
        <span className="break-all text-[12px]">{f.lastRequestId}</span>
      ) : (
        <span className="text-dim">none recorded</span>
      ),
  },
];

function ControlPlaneFailuresCard({ state }: { state: ReturnType<typeof useFailureStore> }) {
  return (
    <Card
      title="Control plane failures"
      note="What this control plane caught in itself, grouped. A row is a kind of failure on a route, not a single occurrence, which is what keeps the table's size a function of the code rather than of how badly the day is going."
    >
      <Loaded state={state} skeleton={<TableSkeleton rows={4} cols={7} />}>
        {(view) => (
          <>
            <StoreState status={view.status} />
            <DataTable
              columns={CONTROL_PLANE_COLUMNS}
              rows={view.failures}
              keyOf={(f) => f.fingerprint}
              empty={
                view.status.recording ? (
                  <EmptyList title="This control plane has not failed in this window">
                    No request reached either error handler in the period selected above. That is
                    what a working installation looks like. Widen the window if you are looking for
                    something older, and read the line above for whether anything is being recorded
                    at all.
                  </EmptyList>
                ) : (
                  <EmptyList title="Nothing is being recorded">
                    This replica has AF_FAILURE_STORE turned off, so an empty list here means
                    nothing was written down, not that nothing failed. Unset the variable and
                    restart to record again.
                  </EmptyList>
                )
              }
              footer={
                <div className="border-t border-rule px-4 py-3">
                  <span className="text-[12.5px] text-muted">
                    {view.truncated
                      ? `The ${view.failures.length} most recent groups, cut at ${view.limit}. The store holds ${view.status.groups.toLocaleString()} in total.`
                      : `All ${view.failures.length} ${view.failures.length === 1 ? "group" : "groups"} seen in this window. The store holds ${view.status.groups.toLocaleString()} in total, across every window.`}
                  </span>
                </div>
              }
            />
          </>
        )}
      </Loaded>
    </Card>
  );
}

/**
 * What the store itself is doing, above the rows it produced.
 *
 * This exists because a short list looks the same whether nothing failed or
 * nothing was recorded, and those are opposite facts. Every sentence here names
 * a way the list below might be shorter than the truth. The row is present on a
 * healthy installation too, saying so plainly, because a notice that only ever
 * appears when something is wrong is a notice nobody has learned to read.
 */
function StoreState({ status }: { status: FailureStoreStatus }) {
  // Each problem carries its own short label rather than a shared "read this
  // first". Two reasons, and the second is what the rendered page showed: a
  // label naming the condition is what a scanner needs, and a three word badge
  // in a flex row wrapped to three stacked lines beside its own paragraph.
  const problems: { label: string; text: string }[] = [];
  if (!status.recording) {
    problems.push({
      label: "not recording",
      text: "The replica that answered has AF_FAILURE_STORE turned off, so it is writing nothing. During a rolling deploy one revision can be recording while another is not.",
    });
  }
  if (status.atCap) {
    problems.push({
      label: "at the cap",
      text: `The store is holding ${status.groups.toLocaleString()} groups against a cap of ${status.cap.toLocaleString()}, so a NEW kind of failure is being refused a row right now. Groups already here keep counting. The capped outcome on af_control_plane_failures_total counts what is being lost.`,
    });
  }
  if (status.retentionDays === null) {
    problems.push({
      label: "no retention",
      text: "Nothing on this installation sweeps old groups: the retention pass needs AF_MAINTENANCE_DATABASE_URL or AF_MIGRATION_DATABASE_URL, because the application role is deliberately given no DELETE here. A group stays until somebody removes it, so read the dates rather than assuming a row is current.",
    });
  }

  return (
    <div className="border-b border-rule px-4 py-3">
      {problems.length === 0 ? (
        <p className="max-w-[76ch] text-[12.5px] leading-6 text-muted">
          <Badge tone="pass">recording</Badge> Holding{" "}
          {status.groups.toLocaleString()} of at most {status.cap.toLocaleString()} groups, each
          kept for {status.retentionDays} days past its last occurrence.
        </p>
      ) : (
        <ul className="space-y-2">
          {problems.map((p) => (
            <li
              key={p.label}
              className="flex max-w-[80ch] flex-wrap items-baseline gap-x-2 gap-y-1 text-[12.5px] leading-6 text-warn"
            >
              <span className="shrink-0 whitespace-nowrap">
                <Badge tone="warn">{p.label}</Badge>
              </span>
              {/* Full width below the phone breakpoint, so the badge sits on
                  its own line and the sentence gets the whole column. Beside
                  the badge at 320 the text had about 120 pixels to work with
                  and ran to eleven lines. */}
              <span className="w-full sm:w-auto sm:min-w-0 sm:flex-1">{p.text}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
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
 *
 * KEPT AND NARROWED rather than deleted when the control plane's own failures
 * became a real store. The half that is now false is one sentence; the rest of
 * it is still exactly true, and a page that quietly dropped its own list of
 * limits the moment one of them was fixed would be teaching the reader that the
 * list is decoration.
 */
function WhatIsNotRecorded() {
  return (
    <Card title="What this page cannot show you">
      <div className="space-y-3 px-4 py-4 text-[13px] leading-6 text-muted">
        <p className="max-w-[70ch]">
          The control plane&apos;s own failures are grouped and counted, above. The
          engine&apos;s are not. Nothing records an exception, a stack trace or a log line from a
          run: those happen in a customer&apos;s environment and would need the engine to report
          them. What you have for the engine side is arithmetic over run outcomes and the event
          stream.
        </p>
        <p className="max-w-[70ch]">
          The control plane store keeps no message and no stack, and that is a boundary rather than
          an omission. A query failure from this stack renders as the whole statement with its
          parameters after it, so an error message here can carry a tenant&apos;s event payload.
          What is kept is the declared route, the method, the error class and the driver code, which
          is exactly what the log line beside it already carried.
        </p>
        <p className="max-w-[70ch]">
          It also carries no organization, so it cannot tell you how many tenants a control plane
          failure touched. Writing one next to a failure would turn a small operational table into
          tenant data. &quot;Failures by code&quot; above answers the per tenant question for the
          engine side, which is where it is normally asked.
        </p>
        <p className="max-w-[70ch]">
          Event payloads are deliberately not returned. The payload is the customer&apos;s data:
          request bodies, database values, whatever the engine observed. The field names and the
          size are enough to tell whether ingestion is working and what shape is arriving, and they
          are not a copy of a tenant&apos;s data sitting in an operator&apos;s browser. Every read
          on this page is recorded against the operator who made it.
        </p>
        <p className="max-w-[70ch] text-dim">
          <Badge tone="neutral">what would change this</Badge> Grouped exceptions from a run would
          need a table with a fingerprint, an occurrence count and a stack trace, plus something on
          the engine side that reports them. That is a schema change and an engine change, not a
          page.
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
