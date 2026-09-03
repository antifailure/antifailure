// The dashboard's API, and the one gate that is not a permission.
//
// WHY A PERMISSION IS NOT ENOUGH HERE, WHICH IS TRUE OF NOTHING ELSE ON THIS
// SERVER.
//
// Every other route answers about the caller's own organization, so a
// permission is the whole question: an owner may see their own plan, their own
// environments, their own audit log. This one answers about the INSTALLATION.
// The acquisition channels, the plan mix, how many organizations reached a
// first proven run: that is the operator's business and it is nobody else's.
//
// And every organization has an owner, and an owner holds every permission
// inside their own organization. So a permission alone would hand the whole
// funnel to every customer who signs up. The gate is therefore membership of
// one named organization, configured by the operator, ANDed with a permission
// so that not every member of that organization sees it either.
//
// Null configuration means nobody, and the route says which variable to set
// rather than answering an empty page. A dashboard that renders zeros because
// it is switched off is indistinguishable from one that renders zeros because
// nobody has visited the site, and only one of those is a working system.

import { z } from 'zod'
import { TRPCError } from '@trpc/server'
import { sql } from 'drizzle-orm'
import { router, orgProcedure, type OrgContext } from '../trpc.ts'
import {
  CATALOG,
  DERIVED_FROM_FACTS,
  EVENT_NAMES,
  FUNNELS,
  FUNNEL_DEFINITIONS,
  isEventName,
  type EventName,
} from '../analytics/catalog.ts'
import {
  actives,
  breakdown,
  catalogStatus,
  conversion,
  freshness,
  organizationFunnel,
  planMix,
  retention,
  retentionGrid,
  series,
  MIN_COHORT_FOR_A_RATE,
} from '../analytics/read.ts'
import { ACTIVE_WINDOWS, RETENTION_WEEKS } from '../analytics/insights.ts'

/** The windows the dashboard offers. A closed set, because each one becomes a
 *  date range in a query and an unbounded one is a table scan a visitor can
 *  ask for. */
const WINDOW_DAYS = [7, 28, 90] as const

const windowInput = z.object({
  days: z.union([z.literal(7), z.literal(28), z.literal(90)]).default(28),
})

/**
 * Refuses unless the caller is a member of the configured operator
 * organization.
 *
 * The comparison is on the SLUG rather than on the identifier, because a slug
 * is what an operator can put in a deployment variable without first querying
 * their own database. It is read from the row rather than from the session, so
 * an organization renamed after somebody signed in loses access on their next
 * request rather than at their next sign-in, which may be never.
 */
async function requireOperator(c: OrgContext): Promise<void> {
  const configured = c.analyticsOperatorOrgSlug
  if (!configured) {
    throw new TRPCError({
      code: 'PRECONDITION_FAILED',
      message:
        'No operator organization is configured, so nobody can read the analytics dashboard. ' +
        'Set AF_ANALYTICS_OPERATOR_ORG to the slug of the organization that operates this ' +
        'control plane.',
    })
  }

  const rows = await c.pool.withTenant(c.tenant, async (db) =>
    db.execute<{ slug: string }>(sql`SELECT slug FROM organizations WHERE id = ${c.actor.orgId}`),
  )
  if (rows[0]?.slug !== configured) {
    throw new TRPCError({
      code: 'FORBIDDEN',
      // Deliberately does not name the configured organization. Telling a
      // customer which organization operates the installation is a fact about
      // somebody else, and the useful next step for them is nothing.
      message: 'The analytics dashboard covers the whole installation and is not yours to read.',
    })
  }
}

/**
 * Where the numbers came from, sent with every answer.
 *
 * A NUMBER WITH NO SOURCE DOES NOT GO ON THE PAGE, and this is what makes that
 * enforceable rather than a good intention: every shape below carries the
 * window it covers, whether the rollup has ever run, which days are still
 * moving, and whether recording is switched on at all. The page has no way to
 * draw a chart without also having the sentence that says what it is.
 */
export interface Provenance {
  windowDays: number
  from: string
  to: string
  /** Null when the rollup has never run: a chart of zeros means something
   *  different then, and the page says which. */
  lastRolledUpAt: string | null
  /** Days on or after this are still absorbing late arrivals. */
  settledAfter: string | null
  /** False when no surrogate secret is configured, so nothing is recorded. */
  recording: boolean
  /**
   * The three insight shapes settle at different rates, so each carries its
   * own answer. One freshness line for all of them would be right about the
   * daily counts and wrong about the other two: a funnel week is still gaining
   * conversions while every subject in it is inside its window, and a cohort's
   * first column is not its size until its week has ended.
   */
  funnelsFinalBefore: string | null
  cohortsCompleteThrough: string | null
  /** How far back a cohort grid can reach at all, so an empty corner reads as
   *  a retention policy rather than as a product with no customers. */
  subjectDaysKept: number | null
}

async function provenanceFor(c: OrgContext, days: number): Promise<Provenance> {
  const today = c.clock.now()
  const state = await c.pool.withoutTenant((db) => freshness(db))
  return {
    windowDays: days,
    from: dayString(new Date(today.getTime() - (days - 1) * 86_400_000)),
    to: dayString(today),
    lastRolledUpAt: state.lastRunAt,
    settledAfter: state.settledAfter,
    recording: c.analytics.enabled,
    funnelsFinalBefore: state.funnelsFinalBefore,
    cohortsCompleteThrough: state.cohortsCompleteThrough,
    subjectDaysKept: state.subjectDaysKept,
  }
}

export const analyticsRouter = router({
  /**
   * Everything one screen needs, in one call.
   *
   * Deliberately not seven procedures. The dashboard is one page and it would
   * fire all seven on load, which is seven round trips and seven separate ways
   * for a partial failure to render as a page that is half right with no
   * indication of which half. One call means one loading state, one error
   * state, and one provenance line that covers everything on the screen.
   */
  overview: orgProcedure('analytics.read')
    .input(windowInput)
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      await requireOperator(c)
      const today = c.clock.now()
      const days = input.days

      return c.pool.withoutTenant(async (db) => ({
        provenance: await provenanceFor(c, days),
        acquisition: {
          bySource: await breakdown(db, 'site.page_viewed', 'a', days, today),
          byLanding: await breakdown(db, 'site.page_viewed', 'b', days, today),
          waitlistBySource: await breakdown(db, 'site.waitlist_submitted', 'a', days, today),
          views: await series(db, 'site.page_viewed', days, today),
        },
        organizations: {
          funnel: await organizationFunnel(db, days, today),
          retention: await retention(db, today),
          plans: await planMix(db),
        },
        adoption: await breakdown(db, 'adoption.feature_used', 'a', days, today),
        validation: await breakdown(db, 'validation.run_finished', 'b', days, today),
        environments: await breakdown(db, 'environment.torn_down', 'a', days, today),

        // The three insights a daily count cannot produce. In the same call as
        // everything else for the reason the comment above gives: one page, one
        // loading state, one error state, one provenance line.
        insights: {
          // A DISTINCT count over the window, which is a different number from
          // anything in `acquisition` above. The panel says so, because two
          // numbers on one page that look like the same measurement and are not
          // is how somebody quotes the wrong one.
          activeOrganizations: await actives(db, 'organization', 28, '', days, today),
          activeSessions: await actives(db, 'session', 7, '', days, today),
          activeWindows: { organizations: 28, sessions: 7 },
          // Declared sequences, computed over events with a conversion window,
          // rather than the milestone funnel above which has no window and
          // cannot have one.
          conversions: (
            await Promise.all(
              FUNNEL_DEFINITIONS.map((f) => conversion(db, f.id, RETENTION_WEEKS, today)),
            )
          ).filter((f) => f !== null),
          cohorts: await retentionGrid(db, today),
          minCohortForARate: MIN_COHORT_FOR_A_RATE,
          windowsKept: ACTIVE_WINDOWS,
        },
      }))
    }),

  /**
   * One event's daily series, for the chart a reader drills into.
   *
   * Separate from the overview because it is asked for on demand, and because
   * a chart that redraws when somebody changes which event it is showing must
   * not refetch six other panels to do it.
   */
  series: orgProcedure('analytics.read')
    .input(windowInput.extend({ name: z.string() }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      await requireOperator(c)
      if (!isEventName(input.name)) {
        throw new TRPCError({
          code: 'BAD_REQUEST',
          message: 'That is not an event in the analytics catalog.',
        })
      }
      const name: EventName = input.name
      return c.pool.withoutTenant(async (db) => ({
        provenance: await provenanceFor(c, input.days),
        name,
        answers: CATALOG[name].answers,
        points: await series(db, name, input.days, c.clock.now()),
      }))
    }),

  /**
   * The catalog, with what has actually arrived under each name.
   *
   * This is the panel that answers "is this producer wired", live, rather than
   * from a report somebody wrote once. An event that is declared, described,
   * given a dimension and emitted by nothing is the exact shape of a feature
   * that looks finished, and the only way to see one is to look at what has
   * arrived rather than at what is defined.
   */
  catalog: orgProcedure('analytics.read').query(async ({ ctx }) => {
    const c = ctx as OrgContext
    await requireOperator(c)
    return c.pool.withoutTenant(async (db) => ({
      provenance: await provenanceFor(c, 28),
      funnels: FUNNELS.map((funnel) => ({
        funnel,
        // The reason a funnel has no event of its own, when it has none.
        // Written next to it rather than left to a reader to infer, because a
        // funnel with no events looks like an oversight and two of these are
        // deliberate.
        derivedFromFacts: DERIVED_FROM_FACTS[funnel] ?? null,
      })),
      events: await catalogStatus(db),
      /** What the page must say beside the site numbers. The beacon is
       *  unauthenticated, so its counts are a floor and a shape rather than an
       *  audited total, and a number whose reliability is not written next to
       *  it gets quoted as though it were audited. */
      siteCountsAreUnauthenticated: true,
      windows: WINDOW_DAYS,
      total: EVENT_NAMES.length,
    }))
  }),
})

function dayString(at: Date): string {
  return at.toISOString().slice(0, 10)
}
