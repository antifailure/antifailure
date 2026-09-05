"use client";

import { Suspense, useState } from "react";
import { useSessionContext } from "@/components/session";
import { query, useApi } from "@/lib/api";
import { recordingState } from "@/lib/analytics-provenance";
import { mayReadAnalytics } from "@/lib/roles";
import { DayColumns, Meter } from "@/components/Meter";
import {
  Card,
  CardSkeleton,
  Empty,
  Loaded,
  Page,
  Row,
  Table,
  TableSkeleton,
  TableWrap,
  Td,
  Th,
  selectClass,
} from "@/components/ui";

/**
 * The operator's dashboard.
 *
 * WHAT IT IS FOR. Answering four questions: where people came from, where they
 * landed, whether they got as far as proving something, and whether they stayed.
 * Not admiring charts. Every panel here exists because one of those questions
 * needs it, and the catalog panel at the bottom exists because a chart that
 * reads zero is worth nothing unless somebody can tell whether that is a quiet
 * week or a producer that has never fired.
 *
 * A NUMBER WITH NO SOURCE DOES NOT GO ON THIS PAGE. The provenance line under
 * the title says the window, when the numbers were last computed, which days
 * are still moving and whether recording is switched on. Nothing here is
 * invented, nothing is a projection, and nothing is rounded into a headline.
 *
 * The site counts carry their own caveat, in the panel rather than in a footnote:
 * the beacon is unauthenticated, so those are a floor and a shape rather than an
 * audited total. A number whose reliability is not written next to it gets
 * quoted as though it were audited.
 */

/** What to add to a funnel's note about how far back it is final. Written next
 *  to the number rather than in a footnote: a week that is still gaining
 *  conversions reads as a worse funnel than it is. */
function provenanceNote(finalBefore: string | null): string {
  return finalBefore === null
    ? ""
    : ` Weeks from ${finalBefore} onwards can still gain conversions.`;
}

const WINDOWS = [
  { days: 7, label: "Last 7 days" },
  { days: 28, label: "Last 28 days" },
  { days: 90, label: "Last 90 days" },
] as const;

interface Breakdown {
  value: string;
  events: number;
  organizations: number;
  sessions: number;
}

interface Provenance {
  windowDays: number;
  from: string;
  to: string;
  lastRolledUpAt: string | null;
  settledAfter: string | null;
  recording: boolean;
  /** True when this installation recorded once and is not recording now. Off
   *  and never on read identically as zeros, and only one of them is a fault. */
  recordingStopped: boolean;
  /** The three insight shapes settle at different rates, so each panel says
   *  which of these applies to it rather than sharing one line. */
  funnelsFinalBefore: string | null;
  cohortsCompleteThrough: string | null;
  subjectDaysKept: number | null;
}

interface ConversionStep {
  step: string;
  meaning: string;
  subjects: number;
  ofPrevious: number | null;
}

interface Conversion {
  id: string;
  title: string;
  subject: string;
  windowDays: number;
  windowReason: string;
  fromWeek: string;
  toWeek: string;
  steps: ConversionStep[];
  empty: boolean;
}

interface CohortRow {
  cohortWeek: string;
  size: number;
  weeks: number[];
  enough: boolean;
}

interface Insights {
  activeOrganizations: { day: string; subjects: number }[];
  activeSessions: { day: string; subjects: number }[];
  activeWindows: { organizations: number; sessions: number };
  conversions: Conversion[];
  cohorts: { rows: CohortRow[]; width: number; empty: boolean };
  minCohortForARate: number;
  windowsKept: readonly number[];
}

interface Overview {
  provenance: Provenance;
  acquisition: {
    bySource: Breakdown[];
    byLanding: Breakdown[];
    leadsBySource: Breakdown[];
    views: { day: string; events: number; organizations: number; sessions: number }[];
  };
  organizations: {
    funnel: { step: string; meaning: string; organizations: number }[];
    retention: { total: number; activeLast7: number; activeLast28: number; dormant: number };
    plans: { plan: string; organizations: number }[];
  };
  adoption: Breakdown[];
  validation: Breakdown[];
  environments: Breakdown[];
  insights: Insights;
}

interface CatalogRow {
  name: string;
  funnel: string;
  answers: string;
  privacyBasis: string;
  producer: string;
  everRecorded: number;
  lastSeenOn: string | null;
}

interface CatalogAnswer {
  provenance: Provenance;
  funnels: { funnel: string; derivedFromFacts: string | null }[];
  events: CatalogRow[];
  siteCountsAreUnauthenticated: boolean;
  total: number;
}

function Analytics() {
  const session = useSessionContext();
  const [days, setDays] = useState<7 | 28 | 90>(28);

  // Two calls rather than one, and they are genuinely two questions. The
  // overview is "what happened in this window" and redraws when the window
  // changes; the catalog is "what can happen at all" and does not.
  const overview = useApi<Overview>(() => query("analytics.overview", { days }), [days]);
  const catalog = useApi<CatalogAnswer>(() => query("analytics.catalog"), []);

  // The shared helper asks both questions: whether this organization operates
  // the installation and whether this role holds analytics.read. The server
  // enforces both again, so this branch is a useful screen rather than the
  // boundary.
  if (!mayReadAnalytics(session.data)) {
    const operator = session.data?.analyticsOperator === true;
    return (
      <Page title="Analytics">
        <Card title="Analytics">
          <Empty
            title={
              operator
                ? "Your role cannot see this"
                : "This dashboard is not about your organization"
            }
          >
            {operator ? (
              <>
                The dashboard covers the whole installation, so it needs the
                analytics.read permission, which owners and admins have.
              </>
            ) : (
              <>
                It counts arrivals across the whole installation, so it belongs
                to whoever operates this control plane rather than to any one
                tenant on it. Your own runs, environments and usage are on the
                pages in the menu.
              </>
            )}
          </Empty>
        </Card>
      </Page>
    );
  }

  return (
    <Page
      title="Analytics"
      lede={
        <>
          Where people came from, where they landed, and how far they got. Every number here is a
          count over a closed set of values: no raw addresses, no page URLs, no repository names.
        </>
      }
      actions={
        <label className="flex items-center gap-2 text-[13px] text-muted">
          <span className="sr-only sm:not-sr-only">Window</span>
          <select
            className={selectClass}
            value={days}
            onChange={(e) => setDays(Number(e.target.value) as 7 | 28 | 90)}
          >
            {WINDOWS.map((w) => (
              <option key={w.days} value={w.days}>
                {w.label}
              </option>
            ))}
          </select>
        </label>
      }
    >
      <Loaded state={overview} skeleton={<CardSkeleton count={3} />} framed>
        {(data) => (
          <div className="space-y-6">
            <Provenance p={data.provenance} />

            <Card
              title="Acquisition"
              note="Cookieless counts from the marketing site. Unauthenticated, so a floor rather than an audited total."
            >
              {data.acquisition.views.every((p) => p.events === 0) ? (
                <Empty title="No page views in this window">
                  The site beacon records one event per page a reader lands on. Nothing has arrived
                  in this window, which is either a quiet period or a site that has not been
                  deployed with the beacon in it. The catalog below says which.
                </Empty>
              ) : (
                <div className="space-y-6 px-4 py-4">
                  <DayColumns points={data.acquisition.views} label="Page views per day" />
                  <div className="grid gap-x-8 gap-y-6 sm:grid-cols-2">
                    <Group
                      heading="Where they came from"
                      caption="The channel the visit started on. Derived from the referrer in the browser and never stored."
                      rows={data.acquisition.bySource}
                    />
                    <Group
                      heading="Where they landed"
                      caption="The shape of the first page, never the path or the slug."
                      rows={data.acquisition.byLanding}
                    />
                  </div>
                  <Group
                    heading="Contact requests, by the channel that brought them"
                    caption="Attributed to where the browsing session started, not to the page holding the form."
                    rows={data.acquisition.leadsBySource}
                  />
                </div>
              )}
            </Card>

            <Card
              title="Organizations"
              note="Organizations first seen in this window. Each step requires every step above it, so this is how many got all the way there."
            >
              {data.organizations.funnel[0]?.organizations === 0 ? (
                <Empty title="No organizations in this window">
                  An organization appears here the first time anything it does reaches the control
                  plane. Widen the window, or check the catalog below for whether the producers have
                  ever fired.
                </Empty>
              ) : (
                <TableWrap>
                  <Table>
                    <thead>
                      <tr>
                        <Th>Step</Th>
                        <Th>What it means</Th>
                        <Th numeric>Organizations</Th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.organizations.funnel.map((step) => (
                        <Row key={step.step}>
                          <Td label="Step">{step.step}</Td>
                          <Td label="What it means">
                            <span className="text-muted">{step.meaning}</span>
                          </Td>
                          <Td label="Organizations" numeric>
                            {step.organizations.toLocaleString()}
                          </Td>
                        </Row>
                      ))}
                    </tbody>
                  </Table>
                </TableWrap>
              )}
            </Card>

            <div className="grid gap-6 lg:grid-cols-2">
              <Card
                title="Still active"
                note="Counted over every organization ever seen, not only this window."
              >
                <div className="space-y-4 px-4 py-4">
                  <Meter
                    label="Active in the last 7 days"
                    value={data.organizations.retention.activeLast7}
                    max={data.organizations.retention.total}
                    note={`of ${data.organizations.retention.total.toLocaleString()}`}
                    tone="accent"
                  />
                  <Meter
                    label="Active in the last 28 days"
                    value={data.organizations.retention.activeLast28}
                    max={data.organizations.retention.total}
                    note={`of ${data.organizations.retention.total.toLocaleString()}`}
                  />
                  <Meter
                    label="Nothing for 28 days"
                    value={data.organizations.retention.dormant}
                    max={data.organizations.retention.total}
                    note={`of ${data.organizations.retention.total.toLocaleString()}`}
                  />
                </div>
              </Card>

              <Card title="Plans" note="As of the last event seen for each organization.">
                {data.organizations.plans.length === 0 ? (
                  <Empty title="No plan recorded yet">
                    A plan is written here when a billing delivery moves one, so an installation
                    that takes no money shows nothing.
                  </Empty>
                ) : (
                  <div className="space-y-1 px-4 py-4">
                    {data.organizations.plans.map((p) => (
                      <Meter
                        key={p.plan}
                        label={p.plan}
                        value={p.organizations}
                        max={Math.max(...data.organizations.plans.map((x) => x.organizations))}
                      />
                    ))}
                  </div>
                )}
              </Card>
            </div>

            {/* The three numbers a daily count cannot produce. Each says what
                makes it different from the panel above it, because two numbers
                that look like the same measurement and are not is how somebody
                quotes the wrong one. */}
            <div className="grid gap-6 lg:grid-cols-2">
              <Card
                title="Distinct organizations"
                note={`One point per day, counting every organization active in the preceding ${data.insights.activeWindows.organizations} days. Not the sum of the daily counts: an organization active on two days is one organization.`}
              >
                {data.insights.activeOrganizations.every((p) => p.subjects === 0) ? (
                  <Empty title="Nothing active in this window">
                    An organization counts as active on any day one of its events arrived. This
                    stays empty until an engine reports.
                  </Empty>
                ) : (
                  <div className="px-4 py-4">
                    <DayColumns
                      label={`Distinct organizations, ${data.insights.activeWindows.organizations} day window`}
                      points={data.insights.activeOrganizations.map((p) => ({
                        day: p.day,
                        events: p.subjects,
                      }))}
                    />
                  </div>
                )}
              </Card>

              <Card
                title="Distinct sessions"
                note={`One point per day, counting every browsing session seen in the preceding ${data.insights.activeWindows.sessions} days. A session ends after thirty minutes idle and after a day whatever happens.`}
              >
                {data.insights.activeSessions.every((p) => p.subjects === 0) ? (
                  <Empty title="No sessions in this window">
                    The site beacon counts a session when somebody opens a page. This stays empty
                    until the marketing site is being read.
                  </Empty>
                ) : (
                  <div className="px-4 py-4">
                    <DayColumns
                      label={`Distinct sessions, ${data.insights.activeWindows.sessions} day window`}
                      points={data.insights.activeSessions.map((p) => ({
                        day: p.day,
                        events: p.subjects,
                      }))}
                    />
                  </div>
                )}
              </Card>
            </div>

            {data.insights.conversions.map((funnel) => (
              <Card
                key={funnel.id}
                title={funnel.title}
                note={`Counted over ${funnel.subject === "session" ? "browsing sessions" : "organizations"} that took the first step between ${funnel.fromWeek} and ${funnel.toWeek}. Every step after the first has to happen in order and within ${funnel.windowDays} ${funnel.windowDays === 1 ? "day" : "days"} of it. ${funnel.windowReason}${provenanceNote(data.provenance.funnelsFinalBefore)}`}
              >
                {funnel.empty ? (
                  <Empty title="Nobody entered this funnel">
                    Nothing took the first step in this window, so there is no conversion to
                    report. The steps below would fill in from the top.
                  </Empty>
                ) : (
                  <div className="space-y-4 px-4 py-4">
                    {funnel.steps.map((step, index) => (
                      <Meter
                        key={step.step}
                        label={
                          <span>
                            <span className="block">{step.meaning}</span>
                            <span className="block text-xs text-black/50">{step.step}</span>
                          </span>
                        }
                        value={step.subjects}
                        max={funnel.steps[0]?.subjects ?? 0}
                        note={
                          step.ofPrevious === null
                            ? undefined
                            : `${Math.round(step.ofPrevious * 100)} percent of the step before`
                        }
                        tone={index === funnel.steps.length - 1 ? "accent" : "neutral"}
                      />
                    ))}
                  </div>
                )}
              </Card>
            ))}

            <Card
              title="Organizations that came back"
              note={`Of the organizations first seen in each week, how many did anything in the weeks after. A row is read across. ${data.provenance.subjectDaysKept === null ? "" : `The grid reaches back ${Math.floor(data.provenance.subjectDaysKept / 7)} weeks, which is how long the rows it is computed from are kept.`}${data.provenance.cohortsCompleteThrough === null ? "" : ` Cohorts after ${data.provenance.cohortsCompleteThrough} are still taking members.`}`}
            >
              {data.insights.cohorts.empty ? (
                <Empty title="No cohort has any members yet">
                  A cohort is the week an organization was first seen. This fills in as soon as
                  one organization has been seen for a week.
                </Empty>
              ) : (
                <TableWrap>
                  <Table>
                    <thead>
                      <tr>
                        <Th>Week</Th>
                        <Th>Organizations</Th>
                        {Array.from({ length: data.insights.cohorts.width - 1 }, (_, i) => (
                          <Th key={i}>{`+${i + 1}`}</Th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {data.insights.cohorts.rows.map((row) => (
                        <tr key={row.cohortWeek}>
                          <Td>{row.cohortWeek}</Td>
                          <Td>{row.size.toLocaleString()}</Td>
                          {row.weeks.slice(1).map((cell, i) => (
                            <Td key={i}>
                              {/* A count, and a rate only when the cohort is
                                  big enough for one to mean anything. A
                                  percentage over three organizations moves by
                                  thirty three points when one opens a laptop. */}
                              {row.size === 0
                                ? ""
                                : row.enough
                                  ? `${Math.round((cell / row.size) * 100)}%`
                                  : `${cell} of ${row.size}`}
                            </Td>
                          ))}
                        </tr>
                      ))}
                    </tbody>
                  </Table>
                </TableWrap>
              )}
            </Card>

            <div className="grid gap-6 lg:grid-cols-2">
              <Card title="Features used" note="One count per capability, never what was changed.">
                {data.adoption.length === 0 ? (
                  <Empty title="Nothing used in this window">
                    A capability is counted the moment somebody changes policy, starts a run, or
                    exports the audit log.
                  </Empty>
                ) : (
                  <div className="space-y-1 px-4 py-4">
                    {data.adoption.map((row) => (
                      <Meter
                        key={row.value}
                        label={row.value.replace(/_/g, " ")}
                        value={row.events}
                        max={Math.max(...data.adoption.map((x) => x.events))}
                      />
                    ))}
                  </div>
                )}
              </Card>

              <Card
                title="Verdicts"
                note="What proportion of runs reached a verdict that proved something."
              >
                {data.validation.length === 0 ? (
                  <Empty title="No verdicts recorded">
                    Nothing in the engine emits the verdict event this counts yet, so this panel is
                    empty by construction rather than because runs are not happening. The catalog
                    below says the same thing about the producer.
                  </Empty>
                ) : (
                  <div className="space-y-1 px-4 py-4">
                    {data.validation.map((row) => (
                      <Meter
                        key={row.value}
                        label={row.value}
                        value={row.events}
                        max={Math.max(...data.validation.map((x) => x.events))}
                        tone={row.value === "pass" ? "accent" : "neutral"}
                      />
                    ))}
                  </div>
                )}
              </Card>
            </div>

            <Card
              title="How long environments live"
              note="Bucketed on purpose: a duration to the second identifies one environment."
            >
              {data.environments.length === 0 ? (
                <Empty title="No environments torn down in this window">
                  An environment is counted here when the engine reports it removed.
                </Empty>
              ) : (
                <div className="space-y-1 px-4 py-4">
                  {data.environments.map((row) => (
                    <Meter
                      key={row.value}
                      label={row.value.replace(/_/g, " ")}
                      value={row.events}
                      max={Math.max(...data.environments.map((x) => x.events))}
                    />
                  ))}
                </div>
              )}
            </Card>
          </div>
        )}
      </Loaded>

      <div className="mt-6">
        <Card
          title="What is being counted"
          note="Every event in the catalog, and whether anything has ever emitted one."
        >
          <Loaded state={catalog} skeleton={<TableSkeleton rows={8} cols={4} />}>
            {(data) => (
              <>
                <p className="border-b border-rule px-4 py-3 text-[13px] leading-6 text-muted">
                  An event that has never arrived is a producer that has never fired, which reads
                  exactly like a quiet week on every chart above. That is what this table is for.
                </p>
                <TableWrap>
                  <Table>
                    <thead>
                      <tr>
                        <Th>Event</Th>
                        <Th>What it answers</Th>
                        <Th>Basis</Th>
                        <Th numeric>Recorded</Th>
                        <Th>Last seen</Th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.events.map((row) => (
                        <Row key={row.name}>
                          <Td label="Event" mono>
                            {row.name}
                          </Td>
                          <Td label="What it answers">
                            <span className="text-muted">{row.answers}</span>
                          </Td>
                          <Td label="Basis">
                            <span className="text-muted">{row.privacyBasis.replace(/_/g, " ")}</span>
                          </Td>
                          <Td label="Recorded" numeric>
                            {row.everRecorded === 0 ? (
                              <span className="text-warn">never</span>
                            ) : (
                              row.everRecorded.toLocaleString()
                            )}
                          </Td>
                          <Td label="Last seen">
                            <span className="text-muted">{row.lastSeenOn ?? "never"}</span>
                          </Td>
                        </Row>
                      ))}
                    </tbody>
                  </Table>
                </TableWrap>
                <div className="border-t border-rule px-4 py-3">
                  <p className="text-[13px] font-medium text-ink">Funnels with no event of their own</p>
                  <ul className="mt-2 space-y-1.5">
                    {data.funnels
                      .filter((f) => f.derivedFromFacts !== null)
                      .map((f) => (
                        <li key={f.funnel} className="text-[13px] leading-6 text-muted">
                          <span className="font-medium text-ink">{f.funnel}</span>
                          {": "}
                          {f.derivedFromFacts}
                        </li>
                      ))}
                  </ul>
                </div>
              </>
            )}
          </Loaded>
        </Card>
      </div>
    </Page>
  );
}

/**
 * Where the numbers came from, above the numbers.
 *
 * Four states that all render as flat or empty and mean different things:
 * recording was never on, recording was on and has STOPPED, the rollup has
 * never run, and nothing happened. A page that cannot tell a reader which of
 * those they are looking at is a page that will be believed about the wrong
 * one.
 *
 * The second is the one worth the extra sentence. Numbers that stopped moving
 * look exactly like numbers nobody is generating, so a control plane rolled
 * back past its analytics variables shows a plausible dashboard of real but
 * frozen figures. Saying "off" there is true and useless; saying it stopped is
 * what sends somebody to look.
 */
function Provenance({ p }: { p: Provenance }) {
  const recording = recordingState(p);
  return (
    <div className="rounded-lg border border-rule bg-card px-4 py-3">
      <p className="text-[13px] leading-6 text-muted">
        <span className="text-ink">
          {p.from} to {p.to}
        </span>
        {". "}
        {recording === "stopped" ? (
          <span className="text-warn">
            Recording has stopped on this control plane. It was recording and it is not now, so
            the numbers below end where they end instead of being current. A rollback to a
            revision from before AF_ANALYTICS_SURROGATE_SECRET was set does exactly this.{" "}
          </span>
        ) : recording === "never-recorded" ? (
          <span className="text-warn">
            Recording is switched off on this control plane, so nothing new is arriving. Set
            AF_ANALYTICS_SURROGATE_SECRET to turn it on.{" "}
          </span>
        ) : null}
        {p.lastRolledUpAt === null ? (
          <span className="text-warn">
            The rollup has never run, so every number here is zero because nothing has been
            computed, not because nothing happened.
          </span>
        ) : (
          <>
            Computed {p.lastRolledUpAt.slice(0, 16).replace("T", " ")} UTC.
            {p.settledAfter ? ` Days from ${p.settledAfter} are still absorbing late arrivals.` : ""}
          </>
        )}
      </p>
    </div>
  );
}

/**
 * One breakdown, as bars.
 *
 * Deliberately no accent colour. An earlier version highlighted whole groups,
 * and rendering it put seven full-width brand-green bars on one card, which
 * turns the accent into wallpaper and stops it meaning anything where it is
 * used to single one number out. The two places it survives are the ones where
 * it points at a single row: organizations still active this week, and the
 * verdicts that passed.
 */
function Group({
  heading,
  caption,
  rows,
}: {
  heading: string;
  caption: string;
  rows: Breakdown[];
}) {
  const max = rows.reduce((m, r) => Math.max(m, r.events), 0);
  return (
    <div className="min-w-0">
      <p className="text-[13px] font-medium text-ink">{heading}</p>
      <p className="mt-1 text-[12.5px] leading-5 text-dim">{caption}</p>
      {rows.length === 0 ? (
        <p className="mt-3 text-[13px] text-muted">Nothing in this window.</p>
      ) : (
        <div className="mt-2 space-y-0.5">
          {rows.map((r) => (
            <Meter
              key={r.value || "unlabelled"}
              label={r.value || "unlabelled"}
              value={r.events}
              max={max}
            />
          ))}
        </div>
      )}
    </div>
  );
}

export default function AnalyticsPage() {
  return (
    <Suspense
      fallback={
        <Page title="Analytics">
          <CardSkeleton count={3} />
        </Page>
      }
    >
      <Analytics />
    </Suspense>
  );
}
