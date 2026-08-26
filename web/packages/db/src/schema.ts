// The typed view of the schema in migrations/.
//
// This file does not create anything. The migrations are the source of truth,
// because row-level security, grants, and check constraints are the parts that
// matter most here and none of them can be expressed in a table builder. What
// this file gives is types and a query surface, and a test asserts that it and
// the database still agree column for column, so the two cannot drift into
// disagreeing about what a row looks like.

import {
  bigint,
  bigserial,
  boolean,
  index,
  inet,
  integer,
  jsonb,
  pgEnum,
  pgTable,
  text,
  timestamp,
  customType,
  uniqueIndex,
  uuid,
} from 'drizzle-orm/pg-core'

// drizzle has no bytea helper. Tokens are stored as bytes rather than hex text
// so that a comparison is a fixed-length memcmp and a truncated value cannot
// compare equal to a prefix.
const bytea = customType<{ data: Buffer; driverData: Buffer }>({
  dataType() {
    return 'bytea'
  },
})

const textArray = customType<{ data: string[]; driverData: string[] }>({
  dataType() {
    return 'text[]'
  },
})

export const memberRole = pgEnum('member_role', ['owner', 'admin', 'member', 'viewer'])
export const environmentState = pgEnum('environment_state', [
  'queued', 'creating', 'running', 'sleeping', 'failed', 'torn_down',
])
export const runState = pgEnum('run_state', [
  'queued', 'running', 'complete', 'failed', 'cancelled',
])
export const verdictValue = pgEnum('verdict_value', [
  'pass', 'fail', 'flaky', 'blocked', 'unverified',
])

export const users = pgTable('users', {
  id: uuid('id').primaryKey().defaultRandom(),
  githubId: bigint('github_id', { mode: 'number' }).notNull(),
  githubLogin: text('github_login').notNull(),
  email: text('email').notNull(),
  name: text('name'),
  avatarUrl: text('avatar_url'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [uniqueIndex('users_github_id_key').on(t.githubId), index('users_email_idx').on(t.email)])

export const organizations = pgTable('organizations', {
  id: uuid('id').primaryKey().defaultRandom(),
  slug: text('slug').notNull(),
  name: text('name').notNull(),
  githubLogin: text('github_login'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [uniqueIndex('organizations_slug_key').on(t.slug)])

export const members = pgTable('members', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  userId: uuid('user_id').notNull(),
  role: memberRole('role').notNull().default('member'),
  source: text('source').notNull().default('github'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [uniqueIndex('members_org_user_key').on(t.orgId, t.userId), index('members_user_idx').on(t.userId)])

export const sessions = pgTable('sessions', {
  id: uuid('id').primaryKey().defaultRandom(),
  tokenHash: bytea('token_hash').notNull(),
  userId: uuid('user_id').notNull(),
  orgId: uuid('org_id'),
  userAgent: text('user_agent'),
  ip: inet('ip'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  lastSeenAt: timestamp('last_seen_at', { withTimezone: true }).notNull().defaultNow(),
  expiresAt: timestamp('expires_at', { withTimezone: true }).notNull(),
  revokedAt: timestamp('revoked_at', { withTimezone: true }),
}, (t) => [index('sessions_user_idx').on(t.userId), index('sessions_expiry_idx').on(t.expiresAt)])

export const oauthStates = pgTable('oauth_states', {
  state: text('state').primaryKey(),
  redirectTo: text('redirect_to'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  expiresAt: timestamp('expires_at', { withTimezone: true }).notNull(),
})

export const githubInstallations = pgTable('github_installations', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  installationId: bigint('installation_id', { mode: 'number' }).notNull(),
  accountLogin: text('account_login').notNull(),
  accountType: text('account_type').notNull(),
  suspendedAt: timestamp('suspended_at', { withTimezone: true }),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [index('github_installations_org_idx').on(t.orgId)])

export const repositories = pgTable('repositories', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  fullName: text('full_name').notNull(),
  defaultBranch: text('default_branch').notNull().default('main'),
  githubId: bigint('github_id', { mode: 'number' }),
  private: boolean('private').notNull().default(true),
  archivedAt: timestamp('archived_at', { withTimezone: true }),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [uniqueIndex('repositories_org_name_key').on(t.orgId, t.fullName)])

export const environments = pgTable('environments', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  repositoryId: uuid('repository_id').notNull(),
  envId: text('env_id').notNull(),
  branch: text('branch').notNull(),
  pullRequest: integer('pull_request'),
  state: environmentState('state').notNull().default('queued'),
  previewUrl: text('preview_url'),
  runtime: text('runtime'),
  goldenVersion: text('golden_version'),
  createdBy: uuid('created_by'),
  lastSequence: bigint('last_sequence', { mode: 'number' }).notNull().default(0),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
  expiresAt: timestamp('expires_at', { withTimezone: true }),
  tornDownAt: timestamp('torn_down_at', { withTimezone: true }),
}, (t) => [
  uniqueIndex('environments_org_env_key').on(t.orgId, t.envId),
  index('environments_repo_idx').on(t.repositoryId, t.createdAt),
  index('environments_state_idx').on(t.orgId, t.state),
])

export const goldenVersions = pgTable('golden_versions', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  repositoryId: uuid('repository_id').notNull(),
  version: text('version').notNull(),
  sourceDigest: text('source_digest'),
  rulesDigest: text('rules_digest'),
  verified: boolean('verified').notNull().default(false),
  attestation: jsonb('attestation'),
  sizeBytes: bigint('size_bytes', { mode: 'number' }),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [uniqueIndex('golden_versions_key').on(t.orgId, t.repositoryId, t.version)])

export const runs = pgTable('runs', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  environmentId: uuid('environment_id').notNull(),
  kind: text('kind').notNull(),
  state: runState('state').notNull().default('queued'),
  startedAt: timestamp('started_at', { withTimezone: true }),
  finishedAt: timestamp('finished_at', { withTimezone: true }),
  lastSequence: bigint('last_sequence', { mode: 'number' }).notNull().default(0),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [index('runs_env_idx').on(t.environmentId, t.createdAt)])

export const verdicts = pgTable('verdicts', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  runId: uuid('run_id').notNull(),
  workflow: text('workflow').notNull(),
  persona: text('persona'),
  value: verdictValue('value').notNull(),
  summary: text('summary'),
  steps: integer('steps').notNull().default(0),
  durationMs: integer('duration_ms'),
  reproduction: jsonb('reproduction'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [index('verdicts_run_idx').on(t.runId)])

export const artifacts = pgTable('artifacts', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  runId: uuid('run_id').notNull(),
  kind: text('kind').notNull(),
  step: integer('step'),
  storageKey: text('storage_key').notNull(),
  contentType: text('content_type'),
  sizeBytes: bigint('size_bytes', { mode: 'number' }),
  sha256: text('sha256'),
  retained: boolean('retained').notNull().default(true),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [index('artifacts_run_idx').on(t.runId, t.step)])

export const maskingRules = pgTable('masking_rules', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  repositoryId: uuid('repository_id').notNull(),
  tableName: text('table_name').notNull(),
  columnName: text('column_name').notNull(),
  transform: text('transform').notNull(),
  link: text('link'),
  reason: text('reason'),
  confirmed: boolean('confirmed').notNull().default(false),
  sourceDigest: text('source_digest'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [uniqueIndex('masking_rules_key').on(t.orgId, t.repositoryId, t.tableName, t.columnName)])

export const networkRules = pgTable('network_rules', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  repositoryId: uuid('repository_id'),
  host: text('host').notNull(),
  mode: text('mode').notNull(),
  paths: textArray('paths'),
  methods: textArray('methods'),
  rateLimit: text('rate_limit'),
  credential: text('credential'),
  fixtures: text('fixtures'),
  webhookPath: text('webhook_path'),
  note: text('note'),
  position: integer('position').notNull().default(0),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [index('network_rules_scope_idx').on(t.orgId, t.repositoryId, t.position)])

export const engineTokens = pgTable('engine_tokens', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  name: text('name').notNull(),
  tokenHash: bytea('token_hash').notNull(),
  prefix: text('prefix').notNull(),
  createdBy: uuid('created_by'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  lastUsedAt: timestamp('last_used_at', { withTimezone: true }),
  revokedAt: timestamp('revoked_at', { withTimezone: true }),
}, (t) => [index('engine_tokens_org_idx').on(t.orgId)])

export const events = pgTable('events', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  idempotencyKey: text('idempotency_key').notNull(),
  envId: text('env_id'),
  environmentId: uuid('environment_id'),
  runId: uuid('run_id'),
  sequence: bigint('sequence', { mode: 'number' }).notNull().default(0),
  type: text('type').notNull(),
  payload: jsonb('payload').notNull().default({}),
  occurredAt: timestamp('occurred_at', { withTimezone: true }).notNull(),
  receivedAt: timestamp('received_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [
  uniqueIndex('events_idempotency_key').on(t.orgId, t.idempotencyKey),
  index('events_env_sequence_idx').on(t.orgId, t.envId, t.sequence),
  index('events_received_idx').on(t.orgId, t.receivedAt),
])

export const auditEntries = pgTable('audit_entries', {
  seq: bigserial('seq', { mode: 'number' }).primaryKey(),
  orgId: uuid('org_id').notNull(),
  actorUserId: uuid('actor_user_id'),
  actorLabel: text('actor_label').notNull(),
  action: text('action').notNull(),
  targetType: text('target_type').notNull(),
  targetId: text('target_id'),
  origin: text('origin').notNull(),
  detail: jsonb('detail').notNull().default({}),
  occurredAt: timestamp('occurred_at', { withTimezone: true }).notNull().defaultNow(),
  prevHash: text('prev_hash'),
  entryHash: text('entry_hash').notNull(),
}, (t) => [index('audit_org_idx').on(t.orgId, t.seq)])

/** Every table the application writes to, for the cross-tenant suite. A table
 *  added to the schema and forgotten here is a table nobody proved is
 *  isolated, so the suite asserts this list covers the database. */
export const tenantScopedTables = [
  members, githubInstallations, repositories, environments, goldenVersions,
  runs, verdicts, artifacts, maskingRules, networkRules, engineTokens, events,
  auditEntries,
] as const
