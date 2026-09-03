// The Administration lane of the operator portal.
//
// WHAT THIS FILE IS FOR, and why it is empty rather than absent. The portal has
// six navigation groups and they are built in parallel. One module per group,
// mounted once, is what lets that happen without two people editing the same
// file: the Administration sections own THIS file and console/app/admin/administration, and
// nothing else. A lane that had to add its routes to router.ts instead would
// put six writers in one object literal, and a duplicate key in an object
// literal is the one merge conflict git does not report. It has already
// happened once in this directory: see the note in infra.ts about a second
// `admin:` key silently winning.
//
// THE SECTIONS THIS LANE OWNS: the Overview, Analytics & Usage, Admins &
// Permissions, and System Configuration.
//
// EVERY NUMBER HERE IS A QUERY. Not one field on any of these routes is
// estimated, defaulted, or filled in to make a card look complete. Where a
// measurement does not exist the field is absent and the page says so, because
// the failure this portal cannot afford is an operator during an incident
// reading a placeholder as an answer. That is also why there is no rollup
// table behind `usage`: there is none in this schema, and inventing a series
// would be worse than admitting there is no history.
//
// WHY THE OVERVIEW IS TWO ROUTES AND NOT ONE. `standing` reads organizations
// and environments; `activity` reads the operator audit chain. They are two
// permissions, and a route declares exactly one. Merging them into a
// convenient single call would have meant guarding the whole thing on the
// weaker of the two and checking the stronger one by hand inside the handler,
// which is how a boundary stops being a thing a test can see. The page asks
// for the ones the operator holds and renders what comes back.
//
// HOW TO ADD A ROUTE. Build it with adminProcedure(permission), which is the
// only exported way to make one, so declaring the permission and creating the
// route are a single act. There is no unguarded builder to reach for. Every
// read goes through ctx.adminDb, which is the operator pool: a cross tenant
// read has to be a credential the application cannot acquire rather than a
// claim it makes about itself. A mutation records what it changed with
// adminAudit, inside the same transaction as the change.

import { z } from 'zod'
import { sql } from 'drizzle-orm'
import { router } from '../trpc.ts'
import { adminProcedure, type AdminContext } from './trpc.ts'
import { capsFor, round } from '../costs.ts'
import { CONTROL_NAMES, controlStates } from './controls.ts'

/**
 * How far back the operator activity summary reads, in entries.
 *
 * BOUNDED BY POSITION RATHER THAN BY TIME, and that is the decision worth
 * defending. The natural question is "what happened in the last 24 hours", and
 * the natural query is a predicate on occurred_at. There is no index on that
 * column: 0029 indexes admin_audit_entries on (seq DESC) and on three other
 * seq pairs, so a time predicate is a sequential scan of the whole chain. That
 * chain grows on every operator READ, because adminProcedure audits queries
 * automatically, so on a busy installation it is the largest table the portal
 * touches and this is the page every operator opens first.
 *
 * Reading a fixed number of the most recent entries is one index range scan,
 * and it is also the more honest statement: the response says exactly how many
 * entries it looked at and the span they cover, rather than implying a
 * complete answer for a window it may only have partly read.
 */
const ACTIVITY_ENTRIES = 2000

/** Windows the usage page offers, in hours. Capped at thirty days because
 *  every one of these is a live aggregate and the cost is linear in the
 *  window. */
const USAGE_WINDOWS = { '24h': 24, '7d': 24 * 7, '30d': 24 * 30 } as const
type UsageWindow = keyof typeof USAGE_WINDOWS

function iso(value: Date | string): string {
  return value instanceof Date ? value.toISOString() : new Date(value).toISOString()
}

/** Postgres returns numeric and bigint as strings, because neither fits a
 *  double without losing something. Nothing downstream wants a string that
 *  looks like a number, and a NaN here would render as a missing measurement
 *  rather than a wrong one. */
function num(value: string | number | null | undefined): number {
  if (value === null || value === undefined) return 0
  const n = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(n) ? n : 0
}

export const administrationRouter = router({
  /**
   * What the installation is doing, for the page an operator lands on.
   *
   * Answers one question: is anything wrong. So it carries the things that are
   * wrong by definition, each of which is a state somebody has to act on, and
   * the two totals those states are read against. It deliberately does NOT
   * carry a wall of counts: a number nobody would act on differently at any
   * value is decoration on this page and noise during an incident.
   *
   * Guarded on admin.tenants.read, which every built in role holds, and which
   * is the permission that actually describes what is read here. Guarding it
   * on admin.portal.access instead would have made the landing page render for
   * a role that may not see organizations, by handing it organizations.
   */
  standing: adminProcedure('admin.tenants.read').query(async ({ ctx }) => {
    const c = ctx as AdminContext
    const now = c.clock.now()
    return c.adminDb(async (db) => {
      // organizations is one row per customer and has no growth path that
      // makes it large, so a count over it is the cheapest honest answer.
      // Contrast admin.tenants.list, which avoids counting deliberately
      // because it counts members and environments per row.
      const totals = await db.execute<{ total: string; suspended: string }>(sql`
        SELECT count(*) AS total,
               count(*) FILTER (WHERE suspended_at IS NOT NULL) AS suspended
        FROM organizations`)

      const suspended = await db.execute<{
        slug: string
        name: string
        suspended_at: Date | string
        suspended_reason: string | null
      }>(sql`
        SELECT slug, name, suspended_at, suspended_reason
        FROM organizations
        WHERE suspended_at IS NOT NULL
        ORDER BY suspended_at DESC
        LIMIT 6`)

      // environments_live_idx is a partial index on exactly this predicate, so
      // this is a scan of the live environments rather than of every
      // environment that ever existed.
      const live = await db.execute<{ live: string }>(sql`
        SELECT count(*) AS live FROM environments WHERE state <> 'torn_down'`)

      /*
       * Deletions that stopped part way.
       *
       * On this page because it is the one failure here that is somebody
       * else's legal problem rather than ours: a customer asked to be deleted,
       * the pipeline raised, and nothing about the product looks wrong
       * afterwards. organization_deletions records the step it failed on, so
       * the row can say which one rather than only that something broke.
       *
       * Cancelled and purged are both excluded. A cancelled deletion was
       * abandoned deliberately and a purged one finished, and neither is
       * waiting for a person.
       */
      const stuck = await db.execute<{
        id: string
        org_slug: string
        org_name: string
        last_error_at: Date | string
        last_error_step: string | null
        requested_at: Date | string
        attempts: number | string
      }>(sql`
        SELECT id, org_slug, org_name, last_error_at, last_error_step, requested_at, attempts
        FROM organization_deletions
        WHERE last_error_at IS NOT NULL
          AND purged_at IS NULL
          AND cancelled_at IS NULL
        ORDER BY last_error_at DESC
        LIMIT 6`)

      const t = totals[0]
      return {
        at: now.toISOString(),
        organizations: {
          total: num(t?.total),
          suspended: num(t?.suspended),
        },
        suspended: suspended.map((r) => ({
          slug: r.slug,
          name: r.name,
          reason: r.suspended_reason,
          since: iso(r.suspended_at),
        })),
        environments: { live: num(live[0]?.live) },
        stuckDeletions: stuck.map((r) => ({
          id: r.id,
          slug: r.org_slug,
          name: r.org_name,
          step: r.last_error_step,
          failedAt: iso(r.last_error_at),
          requestedAt: iso(r.requested_at),
          attempts: num(r.attempts),
        })),
      }
    })
  }),

  /**
   * What operators have been doing, summarised, and the last few things that
   * were not reads.
   *
   * The reads are counted and then filtered out of the list on purpose. Every
   * operator query writes `read.<path>`, so an unfiltered recent list is a
   * page of the reader's own page loads and the one suspension that matters is
   * six screens down. The count stays because the ratio is the interesting
   * part: a burst of refusals among the reads is the shape of somebody trying
   * doors.
   */
  activity: adminProcedure('admin.audit.read').query(async ({ ctx }) => {
    const c = ctx as AdminContext
    return c.adminDb(async (db) => {
      const summary = await db.execute<{
        entries: string
        writes: string
        refusals: string
        critical: string
        high: string
        oldest: Date | string | null
        newest: Date | string | null
      }>(sql`
        WITH recent AS (
          SELECT action, severity, occurred_at
          FROM admin_audit_entries
          ORDER BY seq DESC
          LIMIT ${ACTIVITY_ENTRIES}
        )
        SELECT count(*) AS entries,
               count(*) FILTER (WHERE action NOT LIKE 'read.%') AS writes,
               count(*) FILTER (WHERE action LIKE 'refused.%') AS refusals,
               count(*) FILTER (WHERE severity = 'critical') AS critical,
               count(*) FILTER (WHERE severity = 'high') AS high,
               min(occurred_at) AS oldest,
               max(occurred_at) AS newest
        FROM recent`)

      // Walks back along admin_audit_seq_idx until it has found six. Reads
      // dominate the chain, so it touches more rows than it returns, but it is
      // an index scan with an early stop rather than a sort of the table.
      const recent = await db.execute<{
        seq: string
        actor_label: string
        action: string
        target_type: string
        target_id: string | null
        subject_org_label: string | null
        severity: string
        occurred_at: Date | string
      }>(sql`
        SELECT seq, actor_label, action, target_type, target_id,
               subject_org_label, severity, occurred_at
        FROM admin_audit_entries
        WHERE action NOT LIKE 'read.%'
        ORDER BY seq DESC
        LIMIT 6`)

      const s = summary[0]
      return {
        // Said out loud so the page can say it too. "12 refusals" means
        // nothing without "out of the last 2000 recorded actions".
        readOver: num(s?.entries),
        requested: ACTIVITY_ENTRIES,
        oldest: s?.oldest ? iso(s.oldest) : null,
        newest: s?.newest ? iso(s.newest) : null,
        writes: num(s?.writes),
        refusals: num(s?.refusals),
        critical: num(s?.critical),
        high: num(s?.high),
        recent: recent.map((r) => ({
          seq: num(r.seq),
          actor: r.actor_label,
          action: r.action,
          targetType: r.target_type,
          targetId: r.target_id,
          organization: r.subject_org_label,
          severity: r.severity,
          occurredAt: iso(r.occurred_at),
        })),
      }
    })
  }),

  /**
   * What the installation is being used for, per organization.
   *
   * MEASURED IN ENVIRONMENT-HOURS, which is not a unit invented for this page.
   * costs.ts defines it as the product's one measurable unit of consumption,
   * derives every plan cap from it, and refuses runs against it. The
   * arithmetic below is that file's arithmetic: the OVERLAP of an environment
   * with the window, counting one still running up to now. What changes here
   * is the grouping, from one organization to all of them, because an operator
   * asking who is using the platform is asking the same question the cap asks,
   * about everybody at once.
   *
   * THERE IS NO ROLLUP TABLE IN THIS SCHEMA, and this route does not pretend
   * otherwise. Every figure is computed at the moment of the call, over
   * environments, so there is no series before what that table still holds and
   * nothing here can draw a trend. The response carries the window and the
   * fact that it was computed live, so the page states it rather than
   * implying a warehouse that does not exist.
   *
   * Two windows per row on purpose. The selected window answers "who is using
   * this", and the rolling twenty four hours answers "who is about to be
   * refused", because that is the window the per-day cap is actually enforced
   * over. One column without the other invites reading a thirty day total
   * against a daily cap.
   */
  usage: adminProcedure('admin.tenants.read')
    .input(
      z
        .object({
          window: z.enum(['24h', '7d', '30d']).default('24h'),
          limit: z.number().int().min(1).max(100).default(25),
        })
        .default({ window: '24h', limit: 25 }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const now = c.clock.now()
      const windowHours = USAGE_WINDOWS[input.window as UsageWindow]
      const since = new Date(now.getTime() - windowHours * 3600_000)
      const day = new Date(now.getTime() - 24 * 3600_000)

      return c.adminDb(async (db) => {
        const rows = await db.execute<{
          id: string
          slug: string
          name: string
          plan: string
          suspended_at: Date | string | null
          window_hours: string
          day_hours: string
          environments: string
          live: string
        }>(sql`
          SELECT o.id, o.slug, o.name, o.plan, o.suspended_at,
                 COALESCE(SUM(
                   EXTRACT(EPOCH FROM (
                     LEAST(COALESCE(e.torn_down_at, ${now.toISOString()}::timestamptz),
                           ${now.toISOString()}::timestamptz)
                     - GREATEST(e.created_at, ${since.toISOString()}::timestamptz)
                   )) / 3600.0
                 ), 0) AS window_hours,
                 -- Clamped at zero rather than filtered, because an
                 -- environment inside the wider window and outside the day
                 -- must contribute nothing to the day rather than a negative
                 -- number that quietly cancels somebody else's usage.
                 COALESCE(SUM(GREATEST(0,
                   EXTRACT(EPOCH FROM (
                     LEAST(COALESCE(e.torn_down_at, ${now.toISOString()}::timestamptz),
                           ${now.toISOString()}::timestamptz)
                     - GREATEST(e.created_at, ${day.toISOString()}::timestamptz)
                   )) / 3600.0
                 )), 0) AS day_hours,
                 count(e.id) AS environments,
                 count(e.id) FILTER (WHERE e.state <> 'torn_down') AS live
          FROM organizations o
          JOIN environments e ON e.org_id = o.id
          -- The overlap, which is what makes every duration above positive.
          WHERE e.created_at < ${now.toISOString()}::timestamptz
            AND COALESCE(e.torn_down_at, ${now.toISOString()}::timestamptz)
                > ${since.toISOString()}::timestamptz
          GROUP BY o.id, o.slug, o.name, o.plan, o.suspended_at
          ORDER BY window_hours DESC
          LIMIT ${input.limit}`)

        return {
          at: now.toISOString(),
          window: input.window,
          windowHours,
          since: since.toISOString(),
          rows: rows.map((r) => {
            const dayHours = round(num(r.day_hours))
            const dayCapHours = capsFor(r.plan).perDayHours
            return {
              id: r.id,
              slug: r.slug,
              name: r.name,
              plan: r.plan,
              suspended: r.suspended_at !== null,
              hours: round(num(r.window_hours)),
              dayHours,
              /** The cap costs.ts enforces on the rolling twenty four hours
               *  for this organization's plan, so the row beside it can be
               *  read against something rather than admired. */
              dayCapHours,
              /** Not a warning about the future. The next run that would push
               *  the total past the cap is refused by checkCostCap, so this is
               *  a statement that it is already happening. */
              overDayCap: dayHours > dayCapHours,
              environments: num(r.environments),
              live: num(r.live),
            }
          }),
        }
      })
    }),

  /**
   * What customers have spent against the model budgets somebody set for them.
   *
   * The one thing in this schema that IS a rollup, and it is the reason this
   * route exists separately from `usage`. provider_budgets is keyed
   * (org_id, provider, period) and carries spent_usd against cap_usd, so it
   * holds a real per period history that nothing has to recompute. Reading it
   * is a scan of a small table.
   *
   * An organization with no row for a provider has no budget and therefore
   * cannot spend on it at all, which 0012 says out loud. So an absent row is
   * not a zero and this route does not manufacture one: only rows that exist
   * are returned.
   */
  spend: adminProcedure('admin.tenants.read')
    .input(z.object({ limit: z.number().int().min(1).max(200).default(50) }).default({ limit: 50 }))
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        const rows = await db.execute<{
          slug: string
          name: string
          provider: string
          period: Date | string
          cap_usd: string
          spent_usd: string
          updated_at: Date | string
        }>(sql`
          SELECT o.slug, o.name, b.provider, b.period, b.cap_usd, b.spent_usd, b.updated_at
          FROM provider_budgets b
          JOIN organizations o ON o.id = b.org_id
          ORDER BY b.period DESC,
                   -- The proportion consumed, so the row somebody has to do
                   -- something about sorts to the top rather than the largest
                   -- budget doing so. NULLIF guards the zero cap, which the
                   -- table's own CHECK permits.
                   (b.spent_usd / NULLIF(b.cap_usd, 0)) DESC NULLS LAST
          LIMIT ${input.limit}`)

        return {
          rows: rows.map((r) => {
            const cap = num(r.cap_usd)
            const spent = num(r.spent_usd)
            return {
              slug: r.slug,
              name: r.name,
              provider: r.provider,
              // A date column, and the day is the whole value. Sliced rather
              // than formatted so the client is not handed a timestamp it
              // would have to guess a timezone for.
              period: iso(r.period).slice(0, 10),
              capUsd: cap,
              spentUsd: spent,
              /** Null rather than zero when the cap is zero. A percentage of
               *  nothing is not a number, and rendering it as 0 percent reads
               *  as plenty of room. */
              usedPercent: cap > 0 ? Math.round((spent / cap) * 1000) / 10 : null,
              updatedAt: iso(r.updated_at),
            }
          }),
        }
      })
    }),

  /**
   * How this installation is configured.
   *
   * WHAT THIS REPORTS IS RESOLVED CAPABILITY, NOT ENVIRONMENT VARIABLES. The
   * process reads its environment once at boot and builds a context; a page
   * that read the variables back would be reporting the intent rather than the
   * outcome, and the two differ exactly when somebody has made the mistake
   * this page would be opened to find. So every line below is read off the
   * context the running server is actually serving requests with.
   *
   * NO VALUE OF ANY CREDENTIAL IS RETURNED, and none is returned in a
   * truncated or fingerprinted form either. What comes back is whether a
   * capability resolved, and the name of the variable that would enable it if
   * it did not. A name is what somebody needs to fix it; the value is what
   * they need to leak it.
   *
   * Guarded on admin.infra.read, which is the permission the navigation
   * already names for this section, held by owner, super_admin,
   * infrastructure and security. It is deliberately not a new permission: the
   * catalog gains surface only when nothing in it fits, and this fits.
   */
  installation: adminProcedure('admin.infra.read').query(async ({ ctx }) => {
    const c = ctx as AdminContext
    const now = c.clock.now()

    /**
     * One capability, and how it got that way.
     *
     * `enabledBy` names the variable in both states rather than only when the
     * capability is missing, because the question on this page is as often
     * "why is this on" as "why is this off".
     */
    const capabilities = [
      {
        name: 'Payments',
        ready: c.stripe !== null,
        enabledBy: 'AF_STRIPE_SECRET_KEY',
        whenReady: 'This installation takes money. Billing routes reach Stripe live.',
        whenNot:
          'This installation charges nobody. Billing pages answer with the local record and every money write is refused.',
      },
      {
        name: 'Outbound email',
        ready: c.mailer !== null,
        enabledBy: 'AF_MAIL_FROM',
        whenReady: 'Invitations, sign in links and deletion exports are delivered.',
        whenNot:
          'Nothing is sent. Every route that would have mailed a link hands it back to the caller instead, so invitations still work and nobody is told they were emailed.',
      },
      {
        name: 'Operator database credential',
        // True by construction inside this handler: adminProcedure refuses
        // with PRECONDITION_FAILED when the pool is absent, so nobody reads
        // this line on an installation without one. It is here because a
        // configuration page that silently omits the thing it is running on
        // teaches the reader the list is partial.
        ready: true,
        enabledBy: 'AF_ADMIN_DATABASE_URL',
        whenReady: 'This portal has a BYPASSRLS credential the application cannot acquire.',
        whenNot: 'The operator portal cannot read anything.',
      },
      {
        name: 'Operator sets the plan',
        ready: c.operatorSetsPlan,
        enabledBy: 'AF_OPERATOR_SETS_PLAN',
        whenReady:
          "Whoever runs this installation decides each organization's plan. Customers cannot change their own.",
        whenNot: "An organization's own owner changes its plan.",
      },
      {
        name: 'Hosted plan requirement',
        ready: c.hostedRequiredPlan !== null,
        enabledBy: 'AF_HOSTED_REQUIRED_PLAN',
        whenReady:
          'Operational procedures are refused below the required plan, and billing stays reachable so an organization can reach it.',
        whenNot: 'Every organization reaches every procedure its own quotas allow.',
      },
    ]

    return c.adminDb(async (db) => {
      /*
       * The schema this installation is actually on.
       *
       * The first thing anybody checks when the control plane and the database
       * disagree, and until now there was nowhere to read it without a psql
       * session. schema_migrations is written by migrate.ts on every applied
       * file, so the newest name is the version and applied_at is when this
       * database got there.
       */
      const migrations = await db.execute<{
        name: string
        applied_at: Date | string
        total: string
      }>(sql`
        SELECT name, applied_at, count(*) OVER () AS total
        FROM schema_migrations
        ORDER BY name DESC
        LIMIT 1`)

      // State only. The switch itself lives on Incidents & Kill Switches,
      // which owns admin.emergency.set. Two pages carrying the same engage
      // button is how a control gets released twice and read as broken.
      const controls = await controlStates(db)

      const runtimes = await db.execute<{ registered: string; providers: string }>(sql`
        SELECT count(*) AS registered, count(DISTINCT provider) AS providers
        FROM runtimes WHERE removed_at IS NULL`)

      const m = migrations[0]
      return {
        at: now.toISOString(),
        productName: c.productName,
        appBaseUrl: c.appBaseUrl,
        hostedRequiredPlan: c.hostedRequiredPlan,
        capabilities,
        schema: m
          ? { version: m.name, appliedAt: iso(m.applied_at), applied: num(m.total) }
          : // A database with no schema_migrations rows is one migrate.ts has
            // never run against. Reported as absent rather than as version
            // zero, which would read as a fresh installation that is fine.
            null,
        controls: controls.map((s) => ({
          name: s.name,
          title: s.definition.title,
          effect: s.definition.effect,
          enforcedBy: s.definition.enforcedBy,
          engaged: s.engaged,
          engagedAt: s.engagedAt ? iso(s.engagedAt) : null,
          engagedBy: s.engagedBy,
          reason: s.reason,
        })),
        controlCount: CONTROL_NAMES.length,
        runtimes: {
          registered: num(runtimes[0]?.registered),
          providers: num(runtimes[0]?.providers),
        },
      }
    })
  }),
})
