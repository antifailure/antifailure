"use client";

import { useState } from "react";
import Link from "next/link";
import {
  Badge,
  Card,
  Empty,
  Field,
  Loaded,
  Table,
  TableSkeleton,
  TableWrap,
  Td,
  Th,
  When,
  selectClass,
} from "@/components/ui";
import { AdminPage } from "@/components/admin/primitives";
import {
  useAdminSpend,
  useAdminUsage,
  type AdminSpend,
  type AdminUsage,
  type UsageWindow,
} from "@/lib/admin-administration";

/**
 * What the platform is being used for, measured rather than estimated.
 *
 * THE UNIT IS THE ENVIRONMENT-HOUR, and it is not a unit invented for this
 * page. src/costs.ts defines it as the one thing this system can both measure
 * and charge for: one environment held for one hour. Every plan cap is
 * expressed in it, every run is refused against it, and the arithmetic is two
 * columns on the environments table. A page here reporting "runs" or "activity"
 * would be reporting something no cap is enforced on and no invoice derives
 * from.
 *
 * WHAT THIS PAGE CANNOT SHOW, AND SAYS SO. There is no rollup, aggregate,
 * metric or daily-usage table anywhere in this schema. Every figure below is
 * computed at the moment the page loads, over the rows that still exist. That
 * has three honest consequences and the page states all three at the bottom
 * rather than hiding them: there is no history older than the underlying table,
 * there is no trend line, and the query costs something.
 *
 * The alternative was a growth chart over a series nobody stores. This
 * repository has a check called figurecheck precisely because an invented
 * fidelity score shipped once, drawn client side so that curl found nothing and
 * every cheap audit came back clean. A dashboard over invented numbers is worse
 * than a page that admits a gap.
 */
export default function AdministrationAnalyticsPage() {
  const [window, setWindow] = useState<UsageWindow>("24h");
  const usage = useAdminUsage(window);
  const spend = useAdminSpend();

  return (
    <AdminPage
      href="/admin/administration/analytics"
      lede="Measured in environment-hours, the unit every plan cap is enforced in. One environment held for one hour is one."
    >
      <div className="grid gap-5">
        <Card
          title="Consumption by organization"
          note="The overlap of each environment with the window, counting one still running up to now. Computed live: there is no rollup table behind this."
          actions={
            <div className="min-w-0">
              <Field label="Window">
                <select
                  className={selectClass}
                  value={window}
                  onChange={(e) => setWindow(e.target.value as UsageWindow)}
                >
                  <option value="24h">Last 24 hours</option>
                  <option value="7d">Last 7 days</option>
                  <option value="30d">Last 30 days</option>
                </select>
              </Field>
            </div>
          }
        >
          <Loaded state={usage} skeleton={<TableSkeleton rows={6} cols={6} />}>
            {(data) => <UsageTable usage={data} />}
          </Loaded>
        </Card>

        <Card
          title="Model spend against budget"
          note="provider_budgets, which is the one thing in this schema that is genuinely a rollup: one row per organization, provider and period."
        >
          <Loaded state={spend} skeleton={<TableSkeleton rows={4} cols={5} />}>
            {(data) => <SpendTable spend={data} />}
          </Loaded>
        </Card>

        <NotWired />
      </div>
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * Consumption
 * ---------------------------------------------------------------------- */

function UsageTable({ usage }: { usage: AdminUsage }) {
  if (usage.rows.length === 0) {
    return (
      <Empty title={`No environment ran in the ${windowLabel(usage.window)}`}>
        Nothing held an environment in that time, so there is no consumption to attribute. A
        longer window may find some.
      </Empty>
    );
  }

  return (
    <>
      <TableWrap>
        <Table>
          <thead>
            <tr>
              <Th>Organization</Th>
              <Th>Plan</Th>
              <Th numeric>Hours in window</Th>
              <Th numeric>Last 24h</Th>
              <Th numeric>Daily cap</Th>
              <Th numeric>Environments</Th>
            </tr>
          </thead>
          <tbody>
            {usage.rows.map((r) => (
              <tr key={r.id}>
                <Td>
                  <Link
                    href={`/admin/customers/users/organization?org=${encodeURIComponent(r.slug)}`}
                    className="-mx-1 -my-2 inline-flex min-h-11 items-center px-1 py-2 underline decoration-transparent underline-offset-4 hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
                  >
                    <span className="min-w-0">
                      <span className="block truncate font-medium text-ink">{r.name}</span>
                      <span className="block truncate text-[12px] text-muted">{r.slug}</span>
                    </span>
                  </Link>
                  {r.suspended ? (
                    <span className="mt-1 block">
                      <Badge tone="fail">suspended</Badge>
                    </span>
                  ) : null}
                </Td>
                <Td label="Plan">{r.plan}</Td>
                <Td label="Hours in window" numeric>
                  {r.hours.toLocaleString(undefined, { maximumFractionDigits: 2 })}
                </Td>
                <Td label="Last 24h" numeric>
                  {r.dayHours.toLocaleString(undefined, { maximumFractionDigits: 2 })}
                  {r.overDayCap ? (
                    <span className="mt-1 block">
                      {/* Not a forecast. checkCostCap refuses the next run that
                          would cross the cap, so this says it is already
                          happening. */}
                      <Badge tone="fail">over cap</Badge>
                    </span>
                  ) : null}
                </Td>
                <Td label="Daily cap" numeric>
                  {r.dayCapHours.toLocaleString()}
                </Td>
                <Td label="Environments" numeric>
                  {r.environments.toLocaleString()}
                  <span className="block text-[12px] text-dim">{r.live.toLocaleString()} live</span>
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      </TableWrap>
      <p className="border-t border-rule px-4 py-3 text-[12px] leading-5 text-dim">
        The {usage.rows.length === 25 ? "twenty five" : usage.rows.length} organizations that used
        the most, since <When value={usage.since} />. The daily cap column is the rolling twenty
        four hour cap this organization&apos;s plan is enforced against, which is why the last 24h
        column is shown at every window.
      </p>
    </>
  );
}

/* -------------------------------------------------------------------------
 * Spend
 * ---------------------------------------------------------------------- */

function SpendTable({ spend }: { spend: AdminSpend }) {
  if (spend.rows.length === 0) {
    return (
      <Empty title="No organization has a model budget">
        A row exists only where somebody set a cap, and an organization without one cannot spend
        at all. So this is a working state, not missing data.
      </Empty>
    );
  }

  return (
    <TableWrap>
      <Table>
        <thead>
          <tr>
            <Th>Organization</Th>
            <Th>Provider</Th>
            <Th>Period</Th>
            <Th numeric>Spent</Th>
            <Th numeric>Cap</Th>
            <Th numeric>Used</Th>
          </tr>
        </thead>
        <tbody>
          {spend.rows.map((r) => (
            <tr key={`${r.slug}-${r.provider}-${r.period}`}>
              <Td>
                <span className="block truncate font-medium text-ink">{r.name}</span>
                <span className="block truncate text-[12px] text-muted">{r.slug}</span>
              </Td>
              <Td label="Provider">{r.provider}</Td>
              <Td label="Period">{r.period}</Td>
              <Td label="Spent" numeric>
                {money(r.spentUsd)}
              </Td>
              <Td label="Cap" numeric>
                {money(r.capUsd)}
              </Td>
              <Td label="Used" numeric>
                {r.usedPercent === null ? (
                  // A percentage of a zero cap is not a number, and rendering
                  // it as 0 percent reads as plenty of room left.
                  <span className="text-dim">No cap</span>
                ) : r.usedPercent >= 100 ? (
                  <Badge tone="fail">{r.usedPercent}%</Badge>
                ) : r.usedPercent >= 80 ? (
                  <Badge tone="warn">{r.usedPercent}%</Badge>
                ) : (
                  `${r.usedPercent}%`
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
 * The gap, said out loud
 * ---------------------------------------------------------------------- */

/**
 * What this section is not, and what would make it more.
 *
 * On the page rather than in a comment, because the reader is the person who
 * would otherwise assume a trend line is coming or that these numbers reach
 * back further than they do. A section that quietly omits its limits is how a
 * measurement gets used for something it cannot support.
 */
function NotWired() {
  return (
    <Card title="What this page cannot show yet">
      <div className="px-4 py-4">
        <p className="max-w-[72ch] text-[13px] leading-6 text-muted">
          There is no usage rollup in this schema. No aggregate table, no daily metric, no stored
          series. Every figure above is computed when the page loads, over the rows that still
          exist, which has three consequences worth naming.
        </p>
        <ul className="mt-3 grid max-w-[72ch] gap-2.5 text-[13px] leading-6 text-muted">
          <li>
            <span className="font-medium text-ink">No history.</span> Consumption reaches back only
            as far as the environments table still holds rows. A torn down environment that was
            pruned took its hours with it.
          </li>
          <li>
            <span className="font-medium text-ink">No trend.</span> There is no series to draw a
            line through, so this page draws none. A chart here would be describing numbers nobody
            stored.
          </li>
          <li>
            <span className="font-medium text-ink">A real cost.</span> The consumption query is an
            aggregate across every tenant, so the window is capped at thirty days and the list at
            twenty five organizations.
          </li>
        </ul>
        <p className="mt-3 max-w-[72ch] text-[13px] leading-6 text-muted">
          Making it more would mean a rollup table and a write path that actually runs, on the
          schedule that already keeps the events partitions ahead of the writes. Model spend is the
          one place a period series does exist, which is why it is the only thing here with a past.
        </p>
      </div>
    </Card>
  );
}

/** The window as it reads inside a sentence. The select says "Last 24 hours",
 *  which does not fit after "ran in the". */
function windowLabel(window: UsageWindow): string {
  return window === "24h" ? "last 24 hours" : window === "7d" ? "last 7 days" : "last 30 days";
}

/** Dollars, two places, right aligned by the cell. Not currency-formatted with
 *  a symbol per row: the column heading says what the unit is once. */
function money(usd: number): string {
  return usd.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}
