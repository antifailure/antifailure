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

/** Recorded environment consumption and scheduled UTC history. */
export default function AdministrationAnalyticsPage() {
  const [window, setWindow] = useState<UsageWindow>("24h");
  const usage = useAdminUsage(window);
  const spend = useAdminSpend();

  return (
    <AdminPage
      href="/admin/administration/analytics"
      lede="See which organizations use the platform, how their usage changes, and what they spend on models."
    >
      <div className="grid gap-5">
        <Card
          title="Consumption by organization"
          note="One environment kept for an hour uses one environment-hour. Completed environments remain counted after cleanup."
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
            {(data) => <><UsageTrend usage={data} /><UsageTable usage={data} /></>}
          </Loaded>
        </Card>

        <Card
          title="Model spend against budget"
          note="Recorded model spend compared with each organization's budget for that period."
        >
          <Loaded state={spend} skeleton={<TableSkeleton rows={4} cols={5} />}>
            {(data) => <SpendTable spend={data} />}
          </Loaded>
        </Card>

        <Card title="How usage is counted">
          <p className="max-w-[72ch] px-4 py-4 text-[13px] leading-6 text-muted">
            Totals include active and completed environments. Cleanup preserves their recorded
            hours. The daily chart uses saved UTC totals from scheduled maintenance; today's
            bar is partial. Deleting an organization removes its usage history. Records removed
            before usage history was introduced cannot be recovered.
          </p>
        </Card>
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

function UsageTrend({ usage }: { usage: AdminUsage }) {
  if (!usage.series?.length) return (
    <p className="border-b border-rule px-4 py-4 text-[13px] text-muted">
      No saved daily measurements in this window. Current totals, if any, are below.
    </p>
  );
  const maximum = Math.max(...usage.series.map((point) => point.hours), 1);
  return (
    <figure className="border-b border-rule px-4 py-4">
      <figcaption className="text-[13px] font-medium text-ink">Recorded environment-hours · UTC</figcaption>
      <p className="mt-1 mb-4 text-[12px] leading-5 text-muted">
        Calendar days intersecting this window. Partial boundary days differ from the rolling totals below.
      </p>
      <p className="mb-2 text-[12px] text-muted">Scale: 0 to {maximum.toLocaleString()} hours</p>
      <div className="flex h-36 items-end gap-1" aria-hidden="true">
        {usage.series.map((point) => (
          <div key={point.day} className="flex h-full min-w-0 flex-1 items-end" title={`${point.day}: ${point.hours} hours`}>
            <div className="w-full rounded-t bg-ink" style={{ height: `${point.hours / maximum * 100}%` }} />
          </div>
        ))}
      </div>
      <div className="mt-2 flex justify-between text-[12px] text-muted">
        <span>{usage.series[0].day}</span><span>{usage.series[usage.series.length - 1].day}</span>
      </div>
      <details className="mt-3 text-[13px] text-muted">
        <summary className="min-h-11 cursor-pointer py-3">Read daily measurements</summary>
        <TableWrap><Table><thead><tr><Th>Day (UTC)</Th><Th numeric>Hours</Th><Th>Measured</Th></tr></thead>
          <tbody>{usage.series.map((point) => <tr key={point.day}>
            <Td>{point.day}</Td><Td numeric label="Hours">{point.hours}</Td>
            <Td label="Measured">{point.measuredAt ? <When value={point.measuredAt} /> : 'No recorded usage'}</Td>
          </tr>)}</tbody>
        </Table></TableWrap>
      </details>
    </figure>
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
