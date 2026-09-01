"use client";

import { Suspense, useState } from "react";
import { useSessionContext } from "@/components/session";
import { query, useApi } from "@/lib/api";
import { may } from "@/lib/roles";
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
}

interface Overview {
  provenance: Provenance;
  acquisition: {
    bySource: Breakdown[];
    byLanding: Breakdown[];
    waitlistBySource: Breakdown[];
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

  // The permission the console knows about. The server checks it again and then
  // checks something this copy cannot express: that the caller belongs to the
  // organization operating this installation. So hiding the page here is a
  // convenience and never the boundary, and the server's refusal is what the
  // Loaded branch below renders when it disagrees.
  if (!may(session.data?.role, "analytics.read")) {
    return (
      <Page title="Analytics">
        <Card title="Analytics">
          <Empty title="Your role cannot see this">
            The dashboard covers the whole installation rather than one organization, so it needs
            the analytics.read permission, which owners and admins have.
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
                      accent
                    />
                    <Group
                      heading="Where they landed"
                      caption="The shape of the first page, never the path or the slug."
                      rows={data.acquisition.byLanding}
                    />
                  </div>
                  <Group
                    heading="Waitlist submissions, by the channel that brought them"
                    caption="Attributed to where the browsing session started, not to the page holding the form."
                    rows={data.acquisition.waitlistBySource}
                    accent
                  />
                </div>
              )}
            </Card>

            <Card
              title="Organizations"
              note="Every step counts organizations first seen in this window, so a step can never be wider than the one above it."
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
 * Three states that all render as zeros and mean different things: recording is
 * off, the rollup has never run, and nothing happened. A page that cannot tell
 * a reader which of those they are looking at is a page that will be believed
 * about the wrong one.
 */
function Provenance({ p }: { p: Provenance }) {
  return (
    <div className="rounded-lg border border-rule bg-card px-4 py-3">
      <p className="text-[13px] leading-6 text-muted">
        <span className="text-ink">
          {p.from} to {p.to}
        </span>
        {". "}
        {p.recording ? null : (
          <span className="text-warn">
            Recording is switched off on this control plane, so nothing new is arriving. Set
            AF_ANALYTICS_SURROGATE_SECRET to turn it on.{" "}
          </span>
        )}
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

function Group({
  heading,
  caption,
  rows,
  accent = false,
}: {
  heading: string;
  caption: string;
  rows: Breakdown[];
  accent?: boolean;
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
              tone={accent ? "accent" : "neutral"}
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
