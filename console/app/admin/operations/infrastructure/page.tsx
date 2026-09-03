"use client";

/**
 * Infrastructure & Compute.
 *
 * FOUR QUESTIONS, IN THE ORDER AN OPERATOR ASKS THEM. Is this control plane
 * working. What is running on it. What was asked to stop and did not. What can
 * reach the internet from inside a preview. Every one of them is answered by a
 * route that already exists in web/apps/api/src/admin, and none of the queries
 * behind them is duplicated here.
 *
 * THE SECTIONS ARE STACKED RATHER THAN TABBED. A tab hides the section the
 * reader did not think to open, and the whole reason these four sit on one page
 * is that an operator looking at a red health row usually needs the fleet
 * underneath it in the same breath.
 *
 * THE TWO ARRAY ROUTES SAY THEY ARE CAPPED. `twins` and `teardowns` return an
 * array and no cursor, so there is no next page to ask for and the footer
 * cannot offer one. What it can do is refuse to imply the list is complete when
 * it is exactly the length of the cap, which is the same defect `More` exists
 * to prevent on the routes that do page.
 */

import { useState } from "react";
import {
  Badge,
  Button,
  Card,
  CardSkeleton,
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
  EmptyList,
  FilterBar,
  MetricRow,
  StatusChip,
  type Column,
} from "@/components/admin/primitives";
import { operatorMay, useAdminContext } from "@/lib/admin";
import {
  FINDING_LABEL,
  FLEET_LIMIT,
  STANDING_LABEL,
  requestFleetTeardown,
  teardownRadius,
  toneForStanding,
  toneForVerdict,
  useFirewall,
  useSystemHealth,
  useTeardowns,
  useTwins,
  type BlastRadius,
  type Finding,
  type HealthCheck,
  type Teardown,
  type TwinScope,
  type Twin,
} from "@/lib/admin-operations";

export default function OperationsInfrastructurePage() {
  return (
    <AdminPage
      href="/admin/operations/infrastructure"
      lede="Whether this control plane is working, what is running on it, what refused to stop, and what can reach the internet from inside a preview."
    >
      <div className="space-y-6">
        <HealthSection />
        <FleetSection />
        <TeardownSection />
        <FirewallSection />
      </div>
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * Health
 * ---------------------------------------------------------------------- */

const HEALTH_COLUMNS: Column<HealthCheck>[] = [
  {
    key: "title",
    header: "Check",
    cell: (c) => (
      <>
        <span className="block font-medium text-ink">{c.title}</span>
        <span className="mt-0.5 block max-w-[62ch] text-[12.5px] leading-5 text-muted">
          {c.detail}
        </span>
        {/* Only on rows that are not ok. "No action needed" printed on every
            green row is noise on a page whose entire job is to make the
            actionable ones stand out. */}
        {c.remedy ? (
          <span className="mt-1 block max-w-[62ch] text-[12.5px] leading-5 text-warn">
            {c.remedy}
          </span>
        ) : null}
      </>
    ),
  },
  {
    key: "verdict",
    header: "State",
    cell: (c) => <StatusChip value={c.verdict} tone={toneForVerdict(c.verdict)} />,
  },
  {
    key: "value",
    header: "Measured",
    numeric: true,
    cell: (c) => (
      <>
        <span className="block">{c.value.toLocaleString()}</span>
        <span className="block text-[12px] text-dim">{c.unit}</span>
      </>
    ),
  },
];

function HealthSection() {
  const state = useSystemHealth();
  return (
    <Loaded state={state} skeleton={<CardSkeleton count={1} />} framed>
      {(report) => {
        const bad = report.checks.filter((c) => c.verdict !== "ok");
        return (
          <Card
            title="System health"
            note={
              // The summary is a sentence before it is a chip. A reader
              // scanning for colour can miss one row of nine.
              bad.length === 0
                ? "Every check is inside its threshold."
                : `${bad.length} of ${report.checks.length} checks are worth looking at.`
            }
            actions={
              <>
                <StatusChip
                  value={report.verdict}
                  tone={toneForVerdict(report.verdict)}
                />
                <Button onClick={state.reload}>Refresh</Button>
              </>
            }
          >
            <DataTable
              columns={HEALTH_COLUMNS}
              rows={report.checks}
              keyOf={(c) => c.id}
              empty={
                <EmptyList title="No checks ran">
                  The health module returned nothing at all, which is not the same as everything
                  being fine. Retry, and if it stays empty the operator database connection is the
                  thing to look at.
                </EmptyList>
              }
              footer={
                <div className="border-t border-rule px-4 py-3 text-[12.5px] text-muted">
                  Computed from tables this control plane already writes. Read{" "}
                  <When value={report.at} />. Every verdict is derived from the number beside it,
                  so the state and the count cannot disagree.
                </div>
              }
            />
          </Card>
        );
      }}
    </Loaded>
  );
}

/* -------------------------------------------------------------------------
 * The fleet
 * ---------------------------------------------------------------------- */

const TWIN_COLUMNS: Column<Twin>[] = [
  {
    key: "env",
    header: "Environment",
    cell: (t) => (
      <>
        <span className="block truncate font-medium text-ink">{t.repository}</span>
        <span className="block truncate font-mono text-[12px] text-muted">{t.envId}</span>
      </>
    ),
  },
  // "Tenant" rather than "Organization" because it is the word the other three
  // tables on this page and the two on Logs already use for this column, and
  // twenty two sections calling one thing two names is how a portal stops
  // reading as one product.
  { key: "org", header: "Tenant", cell: (t) => t.orgSlug, mono: true },
  {
    key: "branch",
    header: "Branch",
    cell: (t) => (
      <>
        <span className="block truncate">{t.branch}</span>
        {t.pullRequest !== null ? (
          <span className="block text-[12px] text-dim">pull request {t.pullRequest}</span>
        ) : null}
      </>
    ),
  },
  {
    key: "state",
    header: "State",
    cell: (t) => (
      <div className="flex flex-wrap items-center gap-1.5">
        <StatusChip value={t.state} />
        {/* Past its expiry and still up. This is the row that costs money, and
            the state column alone says "running", which is true and not the
            point. */}
        {t.overdue ? <Badge tone="fail">overdue</Badge> : null}
        {t.teardownPending ? <Badge tone="warn">teardown asked</Badge> : null}
      </div>
    ),
  },
  { key: "runs", header: "Runs", numeric: true, cell: (t) => t.runs.toLocaleString() },
  { key: "expires", header: "Expires", cell: (t) => <When value={t.expiresAt} /> },
];

function FleetSection() {
  const [scope, setScope] = useState<TwinScope>("live");
  const state = useTwins(scope);
  const { me } = useAdminContext();
  const mayTeardown = operatorMay(me, "admin.infra.teardown");

  return (
    <Card
      title="Environments"
      note="Every production twin on this installation, across every organization."
    >
      <FilterBar
        filters={[
          {
            label: "Show",
            value: scope,
            onChange: (v) => setScope(v as TwinScope),
            options: [
              { value: "live", label: "Running now" },
              { value: "overdue", label: "Past their expiry" },
              { value: "all", label: "Everything, including torn down" },
            ],
          },
        ]}
      />
      <Loaded state={state} skeleton={<TableSkeleton rows={6} cols={6} />}>
        {(rows) => (
          <DataTable
            columns={TWIN_COLUMNS}
            rows={rows}
            keyOf={(t) => t.envId}
            empty={
              <EmptyList
                title={
                  scope === "overdue"
                    ? "Nothing is past its expiry"
                    : scope === "live"
                      ? "Nothing is running"
                      : "No environment has ever been created here"
                }
              >
                {scope === "overdue"
                  ? "Every environment that is up is still inside the lifetime it was given. This is the state you want."
                  : scope === "live"
                    ? "No organization has an environment up right now. Widen the filter to see the ones that have been torn down."
                    : "No customer has opened a pull request against a connected repository yet, so nothing has ever been built."}
              </EmptyList>
            }
            footer={<Capped shown={rows.length} noun={{ one: "environment", many: "environments" }} />}
          />
        )}
      </Loaded>
      {/* Below the list rather than beside its title, and that is not a layout
          preference. The reason has to gate the button BEFORE the confirmation
          opens, the way a kill switch card does, or the confirmation is
          reachable with the reason empty and the only thing standing between an
          operator and a fleet teardown with no recorded reason is a server side
          validation error they meet after confirming. A header action has
          nowhere to put a labelled field at 320 pixels. */}
      {mayTeardown ? <FleetTeardown onDone={state.reload} /> : null}
    </Card>
  );
}

/**
 * Asking for every environment in scope to be torn down.
 *
 * THE RADIUS IS FETCHED BEFORE THE CONFIRMATION AND THE COUNT IS WHAT YOU TYPE.
 * The server computes what this would touch rather than estimating it, so the
 * number in the dialog is the query's own answer; making it the confirmation
 * phrase means the operator cannot confirm without having read it. Typing the
 * word "teardown" would prove only that they can read a label.
 *
 * The result is reported as REQUESTED, never as torn down, in the route's own
 * words. Rows are written here; the sweeper is what reaches a runtime, and a
 * console that says "terminated" over a row that will sit pending until it is
 * abandoned is the exact defect the teardown ledger exists to expose.
 */
function FleetTeardown({ onDone }: { onDone: () => void }) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const [radius, setRadius] = useState<BlastRadius | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);

  const trimmed = reason.trim();

  async function begin() {
    setError(null);
    setRadius(null);
    setOpen(true);
    try {
      setRadius(await teardownRadius());
    } catch (e) {
      setError(
        e instanceof Error
          ? `The blast radius did not load, so there is nothing to confirm against. ${e.message}`
          : "The blast radius did not load.",
      );
    }
  }

  async function run() {
    setBusy(true);
    setError(null);
    try {
      const result = await requestFleetTeardown(trimmed);
      setDone(
        `Recorded ${result.recorded} of ${result.requested.length} requests. ${result.pending}`,
      );
      setOpen(false);
      setReason("");
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The control plane refused that.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-3 border-t border-rule px-4 py-4">
      <Field
        label="Why the fleet would be torn down"
        hint="Required before this can be confirmed, and recorded against every request it creates."
      >
        <input
          className={inputClass}
          value={reason}
          maxLength={500}
          onChange={(e) => setReason(e.target.value)}
          placeholder="Runtime provider incident, reclaiming everything"
          autoComplete="off"
        />
      </Field>
      <div className="flex flex-wrap items-center gap-3">
        <Button variant="danger" disabled={trimmed === ""} onClick={begin}>
          Tear down every environment shown
        </Button>
        {done ? (
          <span role="status" className="max-w-[46ch] text-[12.5px] leading-5 text-muted">
            {done}
          </span>
        ) : null}
      </div>

      <Confirm
        open={open}
        title="Tear down every running environment?"
        // The count, not a word. It is the server's own answer to what this
        // touches, and it cannot be typed without being read.
        phrase={radius ? String(radius.environments) : undefined}
        confirmLabel="Request the teardown"
        busy={busy}
        error={error}
        onConfirm={run}
        onCancel={() => {
          if (!busy) {
            setOpen(false);
            setError(null);
          }
        }}
      >
        {radius === null ? (
          <p>Working out what this would touch.</p>
        ) : (
          <>
            <p className="text-ink">
              This asks for {radius.environments.toLocaleString()}{" "}
              {radius.environments === 1 ? "environment" : "environments"} across{" "}
              {radius.organizations.toLocaleString()}{" "}
              {radius.organizations === 1 ? "organization" : "organizations"} to be torn down,
              carrying {radius.runs.toLocaleString()} {radius.runs === 1 ? "run" : "runs"} with
              them.
            </p>
            {radius.alreadyRequested > 0 ? (
              <p>
                {radius.alreadyRequested.toLocaleString()} of them already have a teardown request
                open and will not get a second one.
              </p>
            ) : null}
            <p>
              It writes requests and sends nothing. Each environment disappears from the list above
              only when its runtime confirms it is gone, and the ledger below is where a request
              that cannot reach anything shows up.
            </p>
          </>
        )}
        <p>
          <span className="text-dim">Recorded reason: </span>
          <span className="break-words text-ink">{trimmed}</span>
        </p>
      </Confirm>
    </div>
  );
}

/* -------------------------------------------------------------------------
 * The teardown ledger
 * ---------------------------------------------------------------------- */

const TEARDOWN_COLUMNS: Column<Teardown>[] = [
  {
    key: "target",
    header: "Request",
    cell: (t) => (
      <>
        <span className="block truncate font-medium text-ink">
          {t.repository ?? "no repository recorded"}
        </span>
        <span className="block truncate font-mono text-[12px] text-muted">{t.orgSlug}</span>
      </>
    ),
  },
  {
    key: "standing",
    header: "Standing",
    cell: (t) => (
      <>
        <StatusChip value={STANDING_LABEL[t.standing]} tone={toneForStanding(t.standing)} />
        {/* The route's own sentence about how this request can reach the thing
            it wants stopped. It is the difference between waiting and stuck,
            which the state column does not carry. */}
        <span className="mt-1 block max-w-[52ch] text-[12.5px] leading-5 text-muted">
          {t.route}
        </span>
        {t.lastError ? (
          <span className="mt-1 block max-w-[52ch] break-words text-[12.5px] leading-5 text-fail">
            {t.lastError}
          </span>
        ) : null}
      </>
    ),
  },
  {
    key: "attempts",
    header: "Attempts",
    numeric: true,
    cell: (t) => t.attempts.toLocaleString(),
  },
  { key: "requested", header: "Requested", cell: (t) => <When value={t.requestedAt} /> },
  {
    key: "reason",
    header: "Reason",
    cell: (t) => <span className="block max-w-[32ch] break-words">{t.reason}</span>,
  },
];

function TeardownSection() {
  const [openOnly, setOpenOnly] = useState(true);
  const state = useTeardowns(openOnly);
  return (
    <Card
      title="Teardown ledger"
      note="What was asked to stop, and whether anything can actually be sent to stop it."
    >
      <FilterBar
        filters={[
          {
            label: "Show",
            value: openOnly ? "open" : "all",
            onChange: (v) => setOpenOnly(v === "open"),
            options: [
              { value: "open", label: "Still open" },
              { value: "all", label: "Everything, including confirmed" },
            ],
          },
        ]}
      />
      <Loaded state={state} skeleton={<TableSkeleton rows={4} cols={5} />}>
        {(rows) => (
          <DataTable
            columns={TEARDOWN_COLUMNS}
            rows={rows}
            keyOf={(t) => t.id}
            empty={
              <EmptyList title={openOnly ? "Nothing is waiting to be torn down" : "Nothing has ever been torn down"}>
                {openOnly
                  ? "Every teardown that was asked for has been confirmed by the runtime that held the environment. Widen the filter to see the history."
                  : "No environment on this installation has been asked to stop. Environments also expire on their own, and the health check above counts the ones that have outlived their lifetime."}
              </EmptyList>
            }
            footer={<Capped shown={rows.length} noun={{ one: "request", many: "requests" }} />}
          />
        )}
      </Loaded>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * The egress firewall
 * ---------------------------------------------------------------------- */

const FINDING_COLUMNS: Column<Finding>[] = [
  {
    key: "host",
    header: "Rule",
    cell: (f) => (
      <>
        <span className="block truncate font-medium text-ink">{f.rule.host}</span>
        <span className="block truncate font-mono text-[12px] text-muted">
          {f.rule.orgSlug}
          {f.rule.repository ? ` / ${f.rule.repository}` : ""}
        </span>
      </>
    ),
  },
  {
    key: "finding",
    header: "Finding",
    cell: (f) => (
      <>
        <StatusChip
          value={FINDING_LABEL[f.kind]}
          tone={f.severity === "failing" ? "fail" : "warn"}
        />
        <span className="mt-1 block max-w-[64ch] text-[12.5px] leading-5 text-muted">
          {f.says}
        </span>
      </>
    ),
  },
  { key: "mode", header: "Mode", cell: (f) => <StatusChip value={f.rule.mode} /> },
  {
    key: "approved",
    header: "Approved",
    cell: (f) =>
      f.rule.approvedAt === null ? (
        <span className="text-warn">never</span>
      ) : (
        <>
          <When value={f.rule.approvedAt} />
          {f.rule.approvedBy ? (
            <span className="block truncate font-mono text-[12px] text-muted">
              {f.rule.approvedBy}
            </span>
          ) : null}
        </>
      ),
  },
];

function FirewallSection() {
  const state = useFirewall();
  return (
    <Loaded state={state} skeleton={<CardSkeleton count={1} />} framed>
      {({ summary, findings }) => (
        <div className="space-y-4">
          <MetricRow
            metrics={[
              { label: "Egress rules", value: summary.rules, note: "across every organization" },
              { label: "Organizations", value: summary.organizations, note: "with any rule at all" },
              {
                label: "Forwarding live credentials",
                value: summary.forwardingLiveCredentials,
                note: "never acceptable",
              },
              { label: "Never approved", value: summary.neverApproved, note: "inert, so ignored" },
              { label: "Allowed", value: summary.allowed, note: "reach the real host" },
            ]}
          />
          <Card
            title="Egress findings"
            note="Ordered by what the rule actually does, worst first, rather than by organization."
          >
            <DataTable
              columns={FINDING_COLUMNS}
              rows={findings}
              keyOf={(f) => `${f.kind}:${f.rule.id}`}
              empty={
                <EmptyList title="No rule is doing anything surprising">
                  Every egress rule on this installation is approved, in a mode that substitutes
                  what it promises to substitute, and none of them forwards a live credential. A
                  rule that has never been approved would appear here too, so an empty list also
                  means nothing is sitting inert.
                </EmptyList>
              }
              footer={
                <div className="border-t border-rule px-4 py-3 text-[12.5px] text-muted">
                  All {findings.length} {findings.length === 1 ? "finding" : "findings"}. A rule can
                  appear twice: fixing a missing sandbox credential does not approve it.
                </div>
              }
            />
          </Card>
        </div>
      )}
    </Loaded>
  );
}

/* -------------------------------------------------------------------------
 * The footer for a list that has no next page
 * ---------------------------------------------------------------------- */

/**
 * What `More` says for a route that does not page.
 *
 * These two routes take a limit and return an array, so there is no cursor and
 * nothing to ask for next. A footer that stayed silent would let a list sitting
 * exactly at the cap read as complete, which is the same wrong answer `More`
 * was written to stop. So this says which of the two it is, and when the list
 * is capped it says what to do about it rather than only that it happened.
 */
function Capped({ shown, noun }: { shown: number; noun: { one: string; many: string } }) {
  const things = `${shown} ${shown === 1 ? noun.one : noun.many}`;
  const capped = shown >= FLEET_LIMIT;
  return (
    <div className="border-t border-rule px-4 py-3">
      <span className="text-[12.5px] text-muted">
        {capped
          ? `Showing the first ${things}. This route returns at most ${FLEET_LIMIT} and offers no next page, so there are almost certainly more. Narrow the filter above.`
          : `All ${things}.`}
      </span>
    </div>
  );
}
