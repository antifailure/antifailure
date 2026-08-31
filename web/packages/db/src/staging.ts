// A control plane database shaped like a used one.
//
// Two things need this and they need the same thing. Development needs a
// database with enough in it that a page is worth looking at: an empty
// environment matrix proves nothing about whether a hundred rows are readable.
// And the dogfood pipeline needs a source to mask, because masking rules that
// are only ever run against three fixture rows are rules nobody has tested.
//
// So what it writes is deliberately uncomfortable. Every field that would hold
// something about a person in production holds something that looks like it
// here: addresses that parse as addresses, names that parse as names, bearer
// tokens that match a token detector, an IPv4 in every session row, a phone
// number in a free text field where somebody pasted one. That is the point. A
// masking rule set is only proved by a scanner failing to find anything after
// it runs, and a scanner can only fail to find something that was there.
//
// It is deterministic. The same scale produces the same database, down to the
// addresses, so two dogfood runs are comparable and a masking failure can be
// reproduced rather than re-rolled.

import type postgres from 'postgres'
// A note that cost an afternoon, kept here because the two templates in this
// repository behave differently and the difference is invisible until a page
// renders a wall of escaped quotes.
//
// drizzle's `sql` template sends `${JSON.stringify(x)}::jsonb` as a text
// parameter, and Postgres casts the text to jsonb. Correct.
//
// postgres.js's own template reads the `::jsonb` in the query and applies its
// own JSON serializer to the parameter, so a value that is already a JSON
// string is encoded a second time. `["a"]` is stored as the jsonb *string*
// `"[\"a\"]"`, `jsonb_typeof` says `string` where it should say `array`, and
// every reader gets text where it expected structure. Use `sql.json(value)`
// with no cast on this side, and pass the value rather than its encoding.
import { createPool } from './client.ts'
import { appendAudit } from './audit.ts'

export interface StagingOptions {
  /** Multiplies every count. 1 is a small organization; 10 is a busy one. */
  scale?: number
  /** The connection the application would use, for the audit chain, which has
   *  to be appended through the same path the application appends through or
   *  it is not the same chain. */
  appUrl: string
  /** Fixed, so two runs produce the same database. */
  seed?: number
  log?: (line: string) => void
  /** The instant the newest row is dated from. Fixed by default for the same
   *  reason the seed is. */
  now?: Date
}

export interface StagingReport {
  organizations: number
  users: number
  repositories: number
  environments: number
  runs: number
  verdicts: number
  artifacts: number
  events: number
  auditEntries: number
  networkRules: number
  maskingRules: number
  goldens: number
  sessions: number
  tokens: number
}

/**
 * A small deterministic generator.
 *
 * Not Math.random, and not for cryptographic reasons: the whole value of this
 * data is that the same scale produces the same database, so a masking rule
 * that fails can be reproduced by running it again rather than by hoping the
 * same row comes back.
 */
class Rng {
  #state: number
  constructor(seed: number) {
    this.#state = seed >>> 0 || 1
  }
  next(): number {
    // xorshift32. Short, deterministic, and good enough to spread rows out.
    let x = this.#state
    x ^= x << 13
    x ^= x >>> 17
    x ^= x << 5
    this.#state = x >>> 0
    return this.#state / 0x1_0000_0000
  }
  int(maxExclusive: number): number {
    return Math.floor(this.next() * maxExclusive)
  }
  pick<T>(items: readonly T[]): T {
    return items[this.int(items.length)]!
  }
  /** True with the given probability. */
  chance(p: number): boolean {
    return this.next() < p
  }
}

const FIRST = [
  'ada', 'grace', 'alan', 'edsger', 'barbara', 'ken', 'dennis', 'margaret',
  'linus', 'katherine', 'donald', 'frances', 'tim', 'radia', 'john', 'jean',
  'leslie', 'shafi', 'peter', 'anita',
]
const LAST = [
  'lovelace', 'hopper', 'turing', 'dijkstra', 'liskov', 'thompson', 'ritchie',
  'hamilton', 'torvalds', 'johnson', 'knuth', 'allen', 'berners-lee',
  'perlman', 'mccarthy', 'sammet', 'lamport', 'goldwasser', 'naur', 'borg',
]
const COMPANIES = ['northwind', 'contoso', 'initech', 'globex', 'umbrella', 'hooli']

const REPO_NAMES = [
  'checkout', 'ledger', 'billing-api', 'web', 'notifications', 'search',
  'ingest', 'admin', 'reporting', 'identity', 'inventory', 'gateway',
]

const ENV_STATES = ['running', 'running', 'running', 'sleeping', 'creating', 'queued', 'failed', 'torn_down'] as const
const RUN_KINDS = ['test', 'test', 'load', 'insights', 'up'] as const
const RUN_STATES = ['complete', 'complete', 'complete', 'failed', 'running', 'queued', 'cancelled'] as const
const VERDICTS = ['pass', 'pass', 'pass', 'pass', 'fail', 'flaky', 'blocked', 'unverified'] as const
const WORKFLOWS = [
  'sign-in-with-a-link', 'view-the-environment-matrix', 'open-a-run',
  'edit-a-network-policy', 'read-the-audit-log', 'place-an-order',
  'subscribe-and-cancel', 'reset-a-password',
]
const ARTIFACT_KINDS = ['video', 'trace', 'screenshot', 'console'] as const

const HOSTS = [
  'api.stripe.com', 'api.resend.com', 'api.twilio.com', 'api.github.com',
  'hooks.slack.com', 'api.segment.io', 's3.amazonaws.com', 'api.openai.com',
  'registry.npmjs.org', 'proxy.golang.org',
]
const MODES = ['block', 'allow', 'capture', 'mock', 'sandbox'] as const

const AUDIT_ACTIONS = [
  'environment.torn_down', 'network.rule_proposed', 'masking.rule_proposed',
  'masking.rule_approved', 'member.role_changed', 'token.revoked',
  'organization.suspended', 'organization.resumed', 'audit.exported',
]

/**
 * Empties every table this seeder writes to.
 *
 * Named rather than globbed. A TRUNCATE that discovers its own table list
 * would happily empty a table somebody added for something else, and this is
 * the one function here that destroys data.
 */
export async function resetStaging(admin: postgres.Sql): Promise<void> {
  // CASCADE, in one statement, so the order of the list does not have to
  // encode the foreign keys and cannot go stale when one changes.
  await admin.unsafe(`TRUNCATE TABLE
    audit_entries, events, artifacts, verdicts, runs, environments,
    golden_versions, masking_rules, network_rules, engine_tokens,
    email_signin_tokens, sessions, oauth_states, github_installations,
    members, repositories, users, organizations
    RESTART IDENTITY CASCADE`)
}

/**
 * Fills a migrated database.
 *
 * `admin` must be the owning role: this writes to every table, including the
 * ones the application deliberately cannot write to, which is the whole reason
 * a seeder is not something the application could do to itself.
 */
export async function seedStaging(
  admin: postgres.Sql,
  options: StagingOptions,
): Promise<StagingReport> {
  const scale = Math.max(1, options.scale ?? 1)
  const rng = new Rng(options.seed ?? 20260827)
  const log = options.log ?? (() => {})
  const now = options.now ?? new Date('2026-08-01T09:00:00.000Z')

  const ago = (days: number, hours = 0): Date =>
    new Date(now.getTime() - days * 86_400_000 - hours * 3_600_000)

  const report: StagingReport = {
    organizations: 0, users: 0, repositories: 0, environments: 0, runs: 0,
    verdicts: 0, artifacts: 0, events: 0, auditEntries: 0, networkRules: 0,
    maskingRules: 0, goldens: 0, sessions: 0, tokens: 0,
  }

  // -------------------------------------------------------------------------
  // Organizations. More than one, always, because a control plane with a
  // single tenant proves nothing about isolation and masks nothing that could
  // leak across one.
  // -------------------------------------------------------------------------
  const orgs: { id: string; slug: string }[] = []
  for (let i = 0; i < 3; i += 1) {
    const slug = i === 0 ? 'antifailure' : `${COMPANIES[i]!}-${i}`
    const [row] = await admin<{ id: string }[]>`
      INSERT INTO organizations (slug, name, github_login, plan, created_at, updated_at)
      VALUES (${slug}, ${slug}, ${slug}, ${i === 0 ? 'enterprise' : 'team'},
              ${ago(400 - i * 30)}, ${ago(1)})
      RETURNING id`
    orgs.push({ id: row!.id, slug })
    report.organizations += 1
  }
  const home = orgs[0]!
  log(`${orgs.length} organizations`)

  // -------------------------------------------------------------------------
  // People. Addresses and names that a detector will find, which is what makes
  // the masking rules worth having.
  // -------------------------------------------------------------------------
  const users: { id: string; login: string; email: string; orgId: string }[] = []
  const userCount = 12 * scale
  for (let i = 0; i < userCount; i += 1) {
    const first = rng.pick(FIRST)
    const last = rng.pick(LAST)
    const login = `${first}${last.replace(/[^a-z]/g, '')}${i}`
    const company = rng.pick(COMPANIES)
    // A real-looking address on a real-looking domain. Not example.test: the
    // scanner has to have something to find, or a masking rule that does
    // nothing passes.
    const email = `${first}.${last.replace(/[^a-z]/g, '')}@${company}.com`
    const org = i < userCount * 0.7 ? home : rng.pick(orgs)
    const [row] = await admin<{ id: string }[]>`
      INSERT INTO users (github_id, github_login, email, name, avatar_url, created_at, updated_at)
      VALUES (${1_000_000 + i}, ${login}, ${email},
              ${`${title(first)} ${title(last)}`},
              ${`https://avatars.githubusercontent.com/u/${1_000_000 + i}?v=4`},
              ${ago(300 - i)}, ${ago(rng.int(30))})
      RETURNING id`
    users.push({ id: row!.id, login, email, orgId: org.id })
    report.users += 1

    await admin`
      INSERT INTO members (org_id, user_id, role, source, created_at, updated_at)
      VALUES (${org.id}, ${row!.id},
              ${i === 0 ? 'owner' : i < 3 ? 'admin' : rng.chance(0.2) ? 'viewer' : 'member'},
              ${rng.chance(0.7) ? 'github' : 'manual'}, ${ago(300 - i)}, ${ago(rng.int(30))})`
  }
  log(`${users.length} users`)

  const homeUsers = users.filter((u) => u.orgId === home.id)

  // Installations, so the GitHub path has something to resolve. The identifier
  // is derived from the index rather than from a running total, which is the
  // shape of a mistake worth naming: `report.organizations` is already three by
  // the time this loop runs, so every row asked for the same id and the second
  // one hit the unique index.
  for (const [index, org] of orgs.entries()) {
    await admin`
      INSERT INTO github_installations (org_id, installation_id, account_login, account_type, created_at, updated_at)
      VALUES (${org.id}, ${40_000_000 + index}, ${org.slug}, 'Organization', ${ago(300)}, ${ago(2)})`
  }

  // -------------------------------------------------------------------------
  // Sessions and engine tokens. Both hold secrets in production, and both are
  // columns a masking run has to deal with rather than columns to forget.
  // -------------------------------------------------------------------------
  for (let i = 0; i < 6 * scale; i += 1) {
    const user = rng.pick(users)
    await admin`
      INSERT INTO sessions (token_hash, user_id, org_id, user_agent, ip, created_at, last_seen_at, expires_at)
      VALUES (decode(${hex(rng, 32)}, 'hex'), ${user.id}, ${user.orgId},
              ${'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36'},
              ${`203.0.${rng.int(255)}.${rng.int(255)}`},
              ${ago(rng.int(10))}, ${ago(rng.int(2))}, ${ago(-20)})`
    report.sessions += 1
  }

  for (const org of orgs) {
    for (let i = 0; i < 2 * scale; i += 1) {
      await admin`
        INSERT INTO engine_tokens (org_id, name, token_hash, prefix, created_at, last_used_at)
        VALUES (${org.id}, ${`ci-runner-${i}`}, decode(${hex(rng, 32)}, 'hex'),
                ${`aft_${hex(rng, 4)}`}, ${ago(200 - i)}, ${ago(rng.int(3))})`
      report.tokens += 1
    }
  }

  // -------------------------------------------------------------------------
  // Repositories, environments, runs, verdicts, artifacts.
  // -------------------------------------------------------------------------
  const repos: { id: string; orgId: string; fullName: string }[] = []
  for (const org of orgs) {
    const count = org.id === home.id ? Math.min(REPO_NAMES.length, 6 * scale) : 3
    for (let i = 0; i < count; i += 1) {
      const fullName = `${org.slug}/${REPO_NAMES[i % REPO_NAMES.length]}`
      const [row] = await admin<{ id: string }[]>`
        INSERT INTO repositories (org_id, full_name, default_branch, github_id, private, created_at, updated_at)
        VALUES (${org.id}, ${fullName}, 'main', ${700_000 + repos.length}, true, ${ago(280 - i)}, ${ago(rng.int(10))})
        RETURNING id`
      repos.push({ id: row!.id, orgId: org.id, fullName })
      report.repositories += 1
    }
  }
  log(`${repos.length} repositories`)

  // Goldens first: an environment names the golden it branched from.
  const goldens: string[] = []
  for (const repo of repos) {
    for (let i = 0; i < 2; i += 1) {
      const version = `g-2026${String(7 + i).padStart(2, '0')}${String(1 + rng.int(27)).padStart(2, '0')}-${hex(rng, 3)}`
      const verified = i === 0 || rng.chance(0.8)
      await admin`
        INSERT INTO golden_versions
          (org_id, repository_id, version, source_digest, rules_digest, verified, attestation, size_bytes, created_at)
        VALUES (${repo.orgId}, ${repo.id}, ${version}, ${`sha256:${hex(rng, 32)}`}, ${`sha256:${hex(rng, 32)}`},
                ${verified},
                ${admin.json({
                  verified,
                  columns: 180 + rng.int(60),
                  rowsSampled: 50_000 + rng.int(200_000),
                  findings: verified ? [] : ['orders.notes still parses as an email address'],
                  signedAt: ago(rng.int(20)).toISOString(),
                })},
                ${2_000_000_000 + rng.int(8_000_000_000)}, ${ago(rng.int(30))})`
      goldens.push(version)
      report.goldens += 1
    }
  }

  const environments: { id: string; envId: string; orgId: string; repoId: string }[] = []
  const envCount = 30 * scale
  for (let i = 0; i < envCount; i += 1) {
    const repo = rng.chance(0.75)
      ? rng.pick(repos.filter((r) => r.orgId === home.id))
      : rng.pick(repos)
    const state = rng.pick(ENV_STATES)
    const pr = rng.chance(0.8) ? 100 + i : null
    const branch = pr ? `${rng.pick(['feat', 'fix', 'chore'])}/${rng.pick(REPO_NAMES)}-${i}` : 'main'
    // The index is part of the identifier, not decoration. Two environments
    // for the same repository on `main` are a normal thing to have a week
    // apart, and without it the second one collides with the first on the
    // (org, env_id) unique index.
    const envId = `af-${repo.fullName.split('/')[1]}-${branch.replace(/[^a-z0-9]+/gi, '-')}-${i}`.slice(0, 60)
    const created = ago(rng.int(45), rng.int(24))
    const [row] = await admin<{ id: string }[]>`
      INSERT INTO environments
        (org_id, repository_id, env_id, branch, pull_request, state, preview_url, runtime,
         golden_version, created_by, last_sequence, created_at, updated_at, expires_at, torn_down_at)
      VALUES (${repo.orgId}, ${repo.id}, ${envId}, ${branch}, ${pr}, ${state},
              ${state === 'running' || state === 'sleeping' ? `http://${envId}.preview.antifailure.dev` : null},
              ${rng.pick(['local', 'kubernetes'])},
              ${rng.pick(goldens)},
              ${rng.pick(users.filter((u) => u.orgId === repo.orgId) ?? users).id},
              ${rng.int(400)}, ${created}, ${ago(rng.int(3))},
              ${new Date(created.getTime() + 7 * 86_400_000)},
              ${state === 'torn_down' ? ago(rng.int(20)) : null})
      RETURNING id`
    environments.push({ id: row!.id, envId, orgId: repo.orgId, repoId: repo.id })
    report.environments += 1
  }
  log(`${environments.length} environments`)

  for (const env of environments) {
    const runCount = 1 + rng.int(4)
    for (let r = 0; r < runCount; r += 1) {
      const state = rng.pick(RUN_STATES)
      const started = ago(rng.int(40), rng.int(24))
      const [run] = await admin<{ id: string }[]>`
        INSERT INTO runs (org_id, environment_id, kind, state, started_at, finished_at, last_sequence, created_at)
        VALUES (${env.orgId}, ${env.id}, ${rng.pick(RUN_KINDS)}, ${state}, ${started},
                ${state === 'running' || state === 'queued'
                  ? null
                  : new Date(started.getTime() + 60_000 + rng.int(600_000))},
                ${rng.int(200)}, ${started})
        RETURNING id`
      report.runs += 1

      const verdictCount = 2 + rng.int(4)
      const chosen = new Set<string>()
      for (let v = 0; v < verdictCount; v += 1) {
        const workflow = rng.pick(WORKFLOWS)
        if (chosen.has(workflow)) continue
        chosen.add(workflow)
        const value = rng.pick(VERDICTS)
        const persona = rng.pick(homeUsers.length ? homeUsers : users)
        await admin`
          INSERT INTO verdicts
            (org_id, run_id, workflow, persona, value, summary, steps, duration_ms, reproduction, created_at)
          VALUES (${env.orgId}, ${run!.id}, ${workflow},
                  ${rng.pick(['returning-customer', 'new-signup', 'admin', 'viewer'])},
                  ${value},
                  ${summaryFor(value, workflow, persona.email)},
                  ${3 + rng.int(20)}, ${800 + rng.int(40_000)},
                  ${admin.json(
                    value === 'fail' || value === 'flaky'
                      ? [
                          `Open http://${env.envId}.preview.antifailure.dev/login`,
                          `Fill "Email address" with ${persona.email}`,
                          'Press "Send link"',
                          'Read the captured message and follow the link',
                          'The page showed neither a signed in state nor an error',
                        ]
                      : [],
                  )},
                  ${started})`
        report.verdicts += 1

        for (const kind of ARTIFACT_KINDS) {
          if (kind !== 'trace' && rng.chance(0.4)) continue
          await admin`
            INSERT INTO artifacts
              (org_id, run_id, kind, step, storage_key, content_type, size_bytes, sha256, retained, created_at)
            VALUES (${env.orgId}, ${run!.id}, ${kind}, ${v + 1},
                    ${`runs/${run!.id}/${kind}-${v + 1}`},
                    ${kind === 'video' ? 'video/webm' : kind === 'screenshot' ? 'image/png' : 'application/zip'},
                    ${10_000 + rng.int(9_000_000)}, ${hex(rng, 32)},
                    ${rng.chance(0.9)}, ${started})`
          report.artifacts += 1
        }
      }
    }
  }
  log(`${report.runs} runs, ${report.verdicts} verdicts, ${report.artifacts} artifacts`)

  // -------------------------------------------------------------------------
  // Policy: network rules and masking rules.
  // -------------------------------------------------------------------------
  for (const org of orgs) {
    for (let i = 0; i < HOSTS.length; i += 1) {
      const repo = rng.chance(0.5) ? rng.pick(repos.filter((r) => r.orgId === org.id)) : null
      await admin`
        INSERT INTO network_rules
          (org_id, repository_id, host, mode, paths, methods, rate_limit, credential, note, position, created_at, updated_at)
        VALUES (${org.id}, ${repo?.id ?? null}, ${HOSTS[i]!}, ${rng.pick(MODES)},
                ${rng.chance(0.3) ? ['/v1/*'] : null}, ${rng.chance(0.2) ? ['POST'] : null},
                ${rng.chance(0.3) ? '20/s' : null},
                ${rng.chance(0.2) ? 'STRIPE_SECRET_KEY' : null},
                ${`added while wiring ${REPO_NAMES[i % REPO_NAMES.length]}`},
                ${i}, ${ago(200 - i)}, ${ago(rng.int(20))})`
      report.networkRules += 1
    }
  }

  const MASKED_COLUMNS: [string, string, string][] = [
    ['users', 'email', 'email'],
    ['users', 'name', 'name'],
    ['users', 'avatar_url', 'nullify'],
    ['users', 'github_login', 'handle'],
    ['sessions', 'ip', 'ip'],
    ['sessions', 'user_agent', 'preserve'],
    ['audit_entries', 'actor_label', 'name'],
    ['engine_tokens', 'prefix', 'preserve'],
    ['environments', 'preview_url', 'preserve'],
    ['events', 'payload', 'redact_json'],
  ]
  for (const repo of repos) {
    for (const [table, column, transform] of MASKED_COLUMNS) {
      await admin`
        INSERT INTO masking_rules
          (org_id, repository_id, table_name, column_name, transform, link, reason, confirmed, created_at, updated_at)
        VALUES (${repo.orgId}, ${repo.id}, ${table}, ${column}, ${transform},
                ${column === 'email' ? 'person' : null},
                ${`classified while onboarding ${repo.fullName}`},
                ${rng.chance(0.75)}, ${ago(150)}, ${ago(rng.int(15))})
        ON CONFLICT (org_id, repository_id, table_name, column_name) DO NOTHING`
      report.maskingRules += 1
    }
  }
  log(`${report.networkRules} network rules, ${report.maskingRules} masking rules`)

  // -------------------------------------------------------------------------
  // Events. The partitioned table, and the one that actually gets large.
  // -------------------------------------------------------------------------
  const EVENT_TYPES = [
    'environment.created', 'environment.ready', 'environment.torn_down',
    'network.decision', 'verdict.recorded', 'run.started', 'run.finished',
  ]
  const eventCount = 200 * scale
  for (let i = 0; i < eventCount; i += 1) {
    const env = rng.pick(environments)
    const type = rng.pick(EVENT_TYPES)
    const occurred = ago(rng.int(60), rng.int(24))
    await admin`
      INSERT INTO events (org_id, idempotency_key, env_id, environment_id, sequence, type, payload, occurred_at, received_at)
      VALUES (${env.orgId}, ${`${env.envId}-${i}`}, ${env.envId}, ${env.id}, ${i % 400}, ${type},
              ${admin.json(payloadFor(type, rng, users))},
              ${occurred}, ${new Date(occurred.getTime() + rng.int(5000))})
      ON CONFLICT DO NOTHING`
    report.events += 1
  }
  log(`${report.events} events`)

  // -------------------------------------------------------------------------
  // The audit log, appended through the application's own path so the chain is
  // a real chain. Writing these rows directly would produce a log that fails
  // its own verification, which is worse than having no log.
  // -------------------------------------------------------------------------
  const pool = createPool({ url: options.appUrl, max: 4 })
  try {
    for (const org of orgs) {
      const orgUsers = users.filter((u) => u.orgId === org.id)
      const orgRepos = repos.filter((r) => r.orgId === org.id)
      const count = 20 * scale
      for (let i = 0; i < count; i += 1) {
        const actor = orgUsers.length ? rng.pick(orgUsers) : rng.pick(users)
        const action = rng.pick(AUDIT_ACTIONS)
        await pool.withTenant({ orgId: org.id, userId: actor.id }, (db) =>
          appendAudit(db, {
            orgId: org.id,
            actorUserId: actor.id,
            actorLabel: actor.login,
            action,
            targetType: targetTypeFor(action),
            targetId: targetIdFor(action, rng, orgRepos, environments),
            origin: rng.pick(['web', 'api', 'engine', 'github']),
            detail: detailFor(action, rng),
            occurredAt: ago(rng.int(90), rng.int(24)),
          }),
        )
        report.auditEntries += 1
      }
    }
  } finally {
    await pool.close()
  }
  log(`${report.auditEntries} audit entries`)

  // The sequence is what makes the chain verifiable in the order it was
  // written; the occurred_at values are deliberately shuffled so the page has
  // to sort by sequence rather than by time, which is the bug this catches.
  await admin`ANALYZE`
  return report
}

// ---------------------------------------------------------------------------

function title(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1)
}

function hex(rng: Rng, bytes: number): string {
  let out = ''
  for (let i = 0; i < bytes; i += 1) out += rng.int(256).toString(16).padStart(2, '0')
  return out
}

function summaryFor(value: string, workflow: string, email: string): string {
  switch (value) {
    case 'pass':
      return `Every expectation held for ${workflow}.`
    case 'fail':
      return `After signing in as ${email}, the page showed neither a signed in state nor an error.`
    case 'flaky':
      return `Passed on the first attempt and failed on the second, as ${email}.`
    case 'blocked':
      return 'No inbox was available, so the link could not be read. This is the environment, not the change.'
    default:
      return 'The page neither confirmed nor contradicted the expectation.'
  }
}

/**
 * An event payload with something in it worth masking.
 *
 * A network decision carries a host and a path; a verdict carries an address.
 * Both are the reason `events.payload` is one of the harder columns to mask:
 * it is free-form JSON, and a rule that nullifies it loses the data the whole
 * table exists for.
 */
function payloadFor(
  type: string,
  rng: Rng,
  users: { email: string; login: string }[],
): Record<string, string | number | boolean> {
  const user = rng.pick(users)
  switch (type) {
    case 'network.decision':
      return {
        host: rng.pick(HOSTS),
        mode: rng.pick(MODES),
        method: rng.pick(['GET', 'POST']),
        path: `/v1/${rng.pick(['charges', 'emails', 'messages', 'users'])}`,
        allowed: rng.chance(0.6),
      }
    case 'verdict.recorded':
      return {
        workflow: rng.pick(WORKFLOWS),
        verdict: rng.pick(VERDICTS),
        persona: user.email,
        steps: 3 + rng.int(20),
      }
    case 'environment.created':
      return { by: user.login, email: user.email, runtime: rng.pick(['local', 'kubernetes']) }
    default:
      return { at: rng.int(1000), by: user.login }
  }
}

function targetTypeFor(action: string): string {
  if (action.startsWith('environment.')) return 'environment'
  if (action.startsWith('network.') || action.startsWith('masking.')) return 'repository'
  if (action.startsWith('member.')) return 'member'
  if (action.startsWith('token.')) return 'engine_token'
  return 'organization'
}

function targetIdFor(
  action: string,
  rng: Rng,
  repos: { fullName: string }[],
  environments: { envId: string }[],
): string {
  if (action.startsWith('environment.')) return rng.pick(environments).envId
  if (repos.length && (action.startsWith('network.') || action.startsWith('masking.'))) {
    return rng.pick(repos).fullName
  }
  return `id-${rng.int(100000)}`
}

function detailFor(action: string, rng: Rng): Record<string, unknown> {
  switch (action) {
    case 'network.rule_proposed':
      return { host: rng.pick(HOSTS), mode: rng.pick(MODES) }
    case 'masking.rule_proposed':
      return { table: 'users', column: 'email', transform: 'email' }
    case 'member.role_changed':
      return { role: rng.pick(['admin', 'member', 'viewer']) }
    case 'organization.suspended':
      return { reason: 'spend investigation' }
    case 'audit.exported':
      return { format: 'json', entries: 100 + rng.int(900) }
    default:
      return {}
  }
}
