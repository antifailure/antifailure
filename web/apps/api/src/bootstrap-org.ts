// The first organization on a control plane nobody has installed a GitHub App on.
//
// WHY THIS EXISTS AT ALL, because the shape of the product argues against it.
// A tenant normally begins when somebody installs the GitHub App, and that is
// the right default: an installation is a real act by a real account, it
// carries the login the organization is named after, and it is what makes
// membership synchronisable. Nothing here changes that.
//
// What it does not survive is self-hosting. An operator who runs their own
// control plane against their own Postgres has to create a GitHub App and
// install it before a single row exists, and until then every sign-in lands
// with no tenant, every tRPC procedure refuses to build an actor, and the
// console is an empty shell that cannot explain itself. Break-glass does not
// help: it can give somebody a role in an organization and it deliberately
// cannot create one, so on an empty database it has nothing to name.
//
// So this creates the organization and nothing else. It does not create a user,
// for exactly the reason break-glass does not: an account is proof somebody
// signed in, and a command that manufactures one turns a leaked database
// credential into an identity. It grants no role and issues no session. After
// this the operator signs in through GitHub as normal, which writes their user
// row, and then break-glass makes them the owner. Two commands, each doing one
// thing, neither of them a way in on its own.
//
// The slug matters more than it looks. slugFor in the webhook derives an
// organization's slug from the installing account's login, and rememberInstallation
// upserts on that slug. So an organization created here under the login the App
// will later be installed on is ADOPTED by that installation rather than
// duplicated beside it, and a self-hoster who starts without an App keeps
// everything when they add one. --github-login is what makes that deliberate
// rather than a coincidence.

import { createPool, appendAudit, sql, type Pool } from '@antifailure/db'

export class BootstrapRefused extends Error {}

/** The shape the organizations table enforces, checked here so the failure is
 *  a sentence rather than a constraint violation quoting a regular expression. */
const SLUG = /^[a-z0-9][a-z0-9-]{0,62}$/

export interface CreateOrganizationInput {
  /** A connection string row-level security does not apply to. */
  adminUrl: string
  /** The organization's slug, which is what appears in URLs. */
  slug: string
  /** Display name. Defaults to the slug. */
  name?: string
  /** The GitHub account this organization will belong to, when one is known.
   *  Setting it is what lets a later App installation adopt this row. */
  githubLogin?: string
  /** Reports what would change and writes nothing. */
  dryRun?: boolean
  /** Who ran it, for the audit entry. */
  operator?: string
  now?: Date
}

export interface CreateOrganizationResult {
  orgId: string
  slug: string
  name: string
  githubLogin: string | null
  /** False when the organization was already there, which is not an error. */
  created: boolean
  /** False for a dry run, and only for a dry run. */
  applied: boolean
  auditSeq: number | null
}

/**
 * Creates an organization directly, through a connection the policies do not
 * apply to.
 *
 * Idempotent, because the command an operator runs during a first deployment is
 * one they run twice when the first attempt logged something they did not
 * understand. Running it again reports the organization that is already there
 * and changes nothing about it: the name and the GitHub login are left alone,
 * since the row may have been adopted by an installation since.
 */
export async function createOrganization(
  input: CreateOrganizationInput,
): Promise<CreateOrganizationResult> {
  const slug = input.slug.trim().toLowerCase()
  if (!SLUG.test(slug)) {
    throw new BootstrapRefused(
      `"${input.slug}" is not a usable slug. It has to start with a letter or a digit and ` +
        'contain only lower case letters, digits and hyphens, up to 63 characters, because it ' +
        'appears in URLs and is what a license is issued against.',
    )
  }
  const name = (input.name ?? slug).trim() || slug
  const githubLogin = input.githubLogin?.trim() || null

  const now = input.now ?? new Date()
  const pool = createPool({ url: input.adminUrl, max: 1, rowSecurity: false })
  try {
    await assertUnrestricted(pool)

    const existing = await pool.withoutTenant((db) =>
      db.execute<{ id: string; name: string; github_login: string | null }>(sql`
        SELECT id, name, github_login FROM organizations WHERE slug = ${slug}`),
    )
    if (existing[0]) {
      const row = existing[0]
      return {
        orgId: row.id,
        slug,
        name: row.name,
        githubLogin: row.github_login,
        created: false,
        applied: !input.dryRun,
        auditSeq: null,
      }
    }
    if (input.dryRun) {
      return {
        orgId: '',
        slug,
        name,
        githubLogin,
        created: true,
        applied: false,
        auditSeq: null,
      }
    }

    const rows = await pool.withoutTenant((db) =>
      db.execute<{ id: string }>(sql`
        INSERT INTO organizations (slug, name, github_login, created_at, updated_at)
        VALUES (${slug}, ${name}, ${githubLogin}, ${now.toISOString()}, ${now.toISOString()})
        RETURNING id`),
    )
    const orgId = rows[0]!.id

    // The first entry in this organization's chain, and it says how the
    // organization came to exist. An operator reading the audit log later has
    // to be able to tell a tenant that began with an installation from one an
    // operator created by hand with the database credential, because those are
    // different facts about who decided this organization should exist.
    const entry = await pool.withTenant({ orgId }, (db) =>
      appendAudit(db, {
        orgId,
        actorUserId: null,
        actorLabel: 'bootstrap',
        action: 'organization.created',
        targetType: 'organization',
        targetId: slug,
        origin: 'operator',
        detail: { name, githubLogin, operator: input.operator ?? null },
        occurredAt: now,
      }),
    )

    return {
      orgId,
      slug,
      name,
      githubLogin,
      created: true,
      applied: true,
      auditSeq: entry.seq,
    }
  } finally {
    await pool.close()
  }
}

/**
 * Proves the connection can actually write before anything depends on it.
 *
 * The same probe break-glass makes, and for the same reason: with
 * `row_security = off` a connection the policies DO apply to raises here,
 * where the message can explain which role to use, rather than reading nothing
 * and reporting that the organization does not exist.
 */
async function assertUnrestricted(pool: Pool): Promise<void> {
  try {
    await pool.withoutTenant((db) => db.execute(sql`SELECT 1 FROM organizations LIMIT 1`))
  } catch (error) {
    throw new BootstrapRefused(
      `This connection cannot read the organizations table: ${reasonFor(error)}\n\n` +
        'Every tenant table is FORCE ROW LEVEL SECURITY, so the role that owns the schema is ' +
        'subject to the policies like anybody else, and the policies name only antifailure_app. ' +
        'Use a connection row-level security does not apply to: the cluster superuser, or a role ' +
        'with BYPASSRLS. That is the same connection string the bootstrap step used, not the one ' +
        'the application serves requests with.',
    )
  }
}

/** The database's own words, not the driver's. drizzle wraps a failure as
 *  "Failed query: <sql>" and hangs the server's message off `cause`. */
function reasonFor(error: unknown): string {
  const cause = (error as { cause?: unknown }).cause
  if (cause instanceof Error && cause.message) return cause.message
  return error instanceof Error ? error.message : String(error)
}
