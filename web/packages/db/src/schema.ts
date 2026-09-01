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
  date,
  index,
  inet,
  integer,
  jsonb,
  numeric,
  pgEnum,
  pgTable,
  primaryKey,
  smallint,
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

// githubId and githubLogin are optional because GitHub is no longer the only
// way an account can exist: a member provisioned by SCIM or created
// just-in-time from an assertion has neither. See migrations/0012. The unique
// index still stands; Postgres permits any number of NULLs in one.
export const users = pgTable('users', {
  id: uuid('id').primaryKey().defaultRandom(),
  githubId: bigint('github_id', { mode: 'number' }),
  githubLogin: text('github_login'),
  email: text('email').notNull(),
  identitySource: text('identity_source').notNull().default('github'),
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
  plan: text('plan').notNull().default('free'),
  // Set during an incident to stop this organization creating anything new.
  // What is already running keeps running and can still be read.
  suspendedAt: timestamp('suspended_at', { withTimezone: true }),
  suspendedReason: text('suspended_reason'),
  suspendedBy: text('suspended_by'),
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

export const emailSignInTokens = pgTable('email_signin_tokens', {
  id: uuid('id').primaryKey().defaultRandom(),
  tokenHash: bytea('token_hash').notNull(),
  // Lowercased. The user is resolved when the token comes back, not when it is
  // issued: see migrations/0012 for why issuing must not be able to read a
  // user row by address.
  email: text('email').notNull(),
  redirectTo: text('redirect_to'),
  ip: inet('ip'),
  userAgent: text('user_agent'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  expiresAt: timestamp('expires_at', { withTimezone: true }).notNull(),
  consumedAt: timestamp('consumed_at', { withTimezone: true }),
}, (t) => [
  uniqueIndex('email_signin_tokens_hash_key').on(t.tokenHash),
  index('email_signin_tokens_expiry_idx').on(t.expiresAt),
])

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
  // Proposed by one person, approved by another, or by the same person in a
  // team small enough that the distinction is bookkeeping. A rule is inert
  // until approvedAt is set: effectiveEgress will not read it and no
  // environment applies it.
  proposedBy: uuid('proposed_by'),
  approvedBy: uuid('approved_by'),
  approvedAt: timestamp('approved_at', { withTimezone: true }),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [index('network_rules_scope_idx').on(t.orgId, t.repositoryId, t.position)])

// Where an organization's environments are allowed to run. A name and a
// provider, never a credential: the control plane does not reach a runtime, it
// hands the name to the customer's own CI. See migrations/0018.
export const runtimes = pgTable('runtimes', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  name: text('name').notNull(),
  provider: text('provider').notNull(),
  labels: textArray('labels').notNull(),
  note: text('note'),
  registeredBy: uuid('registered_by'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
  // Removed rather than deleted, so an environment that named this runtime
  // still resolves to something the console can explain.
  removedAt: timestamp('removed_at', { withTimezone: true }),
}, (t) => [index('runtimes_org_idx').on(t.orgId, t.createdAt)])

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
  // 'engine' or 'cli'. A cli token acts as a person and carries user_id; an
  // engine token is a machine and deliberately has none. See migration 0012.
  kind: text('kind').notNull().default('engine'),
  userId: uuid('user_id'),
  scopes: text('scopes').array().notNull().default([]),
  expiresAt: timestamp('expires_at', { withTimezone: true }),
}, (t) => [index('engine_tokens_org_idx').on(t.orgId)])

// A terminal signing in. It has no tenant until somebody approves it, which is
// why it is not in tenantScopedTables and is classified separately in the
// tenancy suite: its rows are reachable only by a secret the caller already
// holds, the same shape as oauth_states.
export const deviceAuthorizations = pgTable('device_authorizations', {
  id: uuid('id').primaryKey().defaultRandom(),
  deviceCodeHash: bytea('device_code_hash').notNull(),
  userCode: text('user_code').notNull(),
  scopes: text('scopes').array().notNull(),
  clientLabel: text('client_label').notNull(),
  approvedAt: timestamp('approved_at', { withTimezone: true }),
  approvedUserId: uuid('approved_user_id'),
  approvedOrgId: uuid('approved_org_id'),
  deniedAt: timestamp('denied_at', { withTimezone: true }),
  redeemedAt: timestamp('redeemed_at', { withTimezone: true }),
  lastPolledAt: timestamp('last_polled_at', { withTimezone: true }),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  expiresAt: timestamp('expires_at', { withTimezone: true }).notNull(),
}, (t) => [index('device_authorizations_expiry_idx').on(t.expiresAt)])

// A customer's provider key. The ciphertext is here; nothing reads it except
// the code handing the key to the provider. See src/providers/seal.ts.
export const providerKeys = pgTable('provider_keys', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  provider: text('provider').notNull(),
  ciphertext: bytea('ciphertext').notNull(),
  nonce: bytea('nonce').notNull(),
  keyVersion: text('key_version').notNull().default('v1'),
  fingerprint: text('fingerprint').notNull(),
  last4: text('last4').notNull(),
  createdBy: uuid('created_by'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  rotatedAt: timestamp('rotated_at', { withTimezone: true }),
  revokedAt: timestamp('revoked_at', { withTimezone: true }),
})

// What a provider key may spend this month, and what it has. A missing row is
// zero rather than unlimited; see src/providers/store.ts for why.
export const providerBudgets = pgTable('provider_budgets', {
  orgId: uuid('org_id').notNull(),
  provider: text('provider').notNull(),
  period: timestamp('period', { mode: 'string' }).notNull(),
  capUsd: numeric('cap_usd', { precision: 12, scale: 4 }).notNull(),
  spentUsd: numeric('spent_usd', { precision: 12, scale: 4 }).notNull().default('0'),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [primaryKey({ columns: [t.orgId, t.provider, t.period] })])

// Partitioned by month on occurred_at. Both keys carry the partition column
// because Postgres requires it; see migrations/0011_partition_events.sql for
// why the column is occurred_at and not received_at.
export const events = pgTable('events', {
  id: uuid('id').notNull().defaultRandom(),
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
  primaryKey({ columns: [t.id, t.occurredAt] }),
  uniqueIndex('events_idempotency_key').on(t.orgId, t.idempotencyKey, t.occurredAt),
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

// ---------------------------------------------------------------------------
// Single sign-on and SCIM
//
// Written by the enterprise edition's single sign-on and provisioning packages,
// and typed here because the schema drift suite requires every table in the
// database to be typed: a table nothing types is a table whose columns drift
// silently. See migrations/0012_sso_and_scim.sql for the isolation, which is
// the part that cannot be expressed here.
//
// Naming the enterprise directory in a comment here would fail the edition
// boundary gate, which greps this tree for that path. That gate is blunt on
// purpose and it is right to be: a comment naming a dependency is how a
// dependency starts.
// ---------------------------------------------------------------------------

export const ssoConnections = pgTable('sso_connections', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  handle: text('handle').notNull(),
  kind: text('kind').notNull(),
  displayName: text('display_name').notNull(),
  enabled: boolean('enabled').notNull().default(false),
  enforced: boolean('enforced').notNull().default(false),
  defaultRole: memberRole('default_role').notNull().default('member'),
  idpEntityId: text('idp_entity_id'),
  idpSsoUrl: text('idp_sso_url'),
  idpCertificates: textArray('idp_certificates').notNull().default([]),
  oidcIssuer: text('oidc_issuer'),
  oidcClientId: text('oidc_client_id'),
  oidcAuthorizationEndpoint: text('oidc_authorization_endpoint'),
  oidcTokenEndpoint: text('oidc_token_endpoint'),
  oidcJwksUri: text('oidc_jwks_uri'),
  groupRoleMap: jsonb('group_role_map').notNull().default({}),
  clockSkewSeconds: integer('clock_skew_seconds').notNull().default(300),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [
  uniqueIndex('sso_connections_handle_key').on(t.handle),
  index('sso_connections_org_idx').on(t.orgId),
])

// Deliberately its own table. Row-level security is row level, so the policy
// that lets an unauthenticated callback find a connection by its handle would
// expose every column of that row. Nothing unauthenticated reaches this one.
export const ssoConnectionSecrets = pgTable('sso_connection_secrets', {
  connectionId: uuid('connection_id').primaryKey(),
  orgId: uuid('org_id').notNull(),
  oidcClientSecret: bytea('oidc_client_secret'),
  spPrivateKey: bytea('sp_private_key'),
  spCertificate: text('sp_certificate'),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
})

export const ssoDomains = pgTable('sso_domains', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  connectionId: uuid('connection_id').notNull(),
  domain: text('domain').notNull(),
  verificationToken: text('verification_token').notNull(),
  verifiedAt: timestamp('verified_at', { withTimezone: true }),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [
  uniqueIndex('sso_domains_org_id_domain_key').on(t.orgId, t.domain),
  index('sso_domains_org_idx').on(t.orgId),
])

export const ssoLoginStates = pgTable('sso_login_states', {
  state: text('state').primaryKey(),
  orgId: uuid('org_id').notNull(),
  connectionId: uuid('connection_id').notNull(),
  nonce: text('nonce'),
  codeVerifier: text('code_verifier'),
  requestId: text('request_id'),
  relayState: text('relay_state'),
  redirectTo: text('redirect_to'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  expiresAt: timestamp('expires_at', { withTimezone: true }).notNull(),
}, (t) => [index('sso_login_states_expiry_idx').on(t.expiresAt)])

export const ssoAssertionsSeen = pgTable('sso_assertions_seen', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  connectionId: uuid('connection_id').notNull(),
  assertionId: text('assertion_id').notNull(),
  seenAt: timestamp('seen_at', { withTimezone: true }).notNull().defaultNow(),
  expiresAt: timestamp('expires_at', { withTimezone: true }).notNull(),
}, (t) => [
  uniqueIndex('sso_assertions_seen_connection_id_assertion_id_key')
    .on(t.connectionId, t.assertionId),
  index('sso_assertions_seen_expiry_idx').on(t.expiresAt),
])

export const ssoBreakGlassCodes = pgTable('sso_break_glass_codes', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  codeHash: bytea('code_hash').notNull(),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  usedAt: timestamp('used_at', { withTimezone: true }),
  usedBy: uuid('used_by'),
}, (t) => [uniqueIndex('sso_break_glass_codes_org_id_code_hash_key').on(t.orgId, t.codeHash)])

export const scimTokens = pgTable('scim_tokens', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  name: text('name').notNull(),
  tokenHash: bytea('token_hash').notNull(),
  prefix: text('prefix').notNull(),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  lastUsedAt: timestamp('last_used_at', { withTimezone: true }),
  expiresAt: timestamp('expires_at', { withTimezone: true }),
  revokedAt: timestamp('revoked_at', { withTimezone: true }),
}, (t) => [
  uniqueIndex('scim_tokens_token_hash_key').on(t.tokenHash),
  index('scim_tokens_org_idx').on(t.orgId),
])

export const scimResources = pgTable('scim_resources', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  userId: uuid('user_id'),
  externalId: text('external_id'),
  userName: text('user_name').notNull(),
  active: boolean('active').notNull().default(true),
  givenName: text('given_name'),
  familyName: text('family_name'),
  displayName: text('display_name'),
  version: integer('version').notNull().default(1),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [
  uniqueIndex('scim_resources_org_id_user_name_key').on(t.orgId, t.userName),
  index('scim_resources_user_idx').on(t.userId),
])

export const scimGroups = pgTable('scim_groups', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  externalId: text('external_id'),
  displayName: text('display_name').notNull(),
  role: memberRole('role'),
  version: integer('version').notNull().default(1),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [uniqueIndex('scim_groups_org_id_display_name_key').on(t.orgId, t.displayName)])

// resourceId is nullable on purpose, and it is the ordering this whole feature
// gets wrong most often: Okta and Entra ID both send group membership naming
// users they have not created yet. The reference is kept as it arrived and
// resolved when the user turns up.
export const scimGroupMembers = pgTable('scim_group_members', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  groupId: uuid('group_id').notNull(),
  memberRef: text('member_ref').notNull(),
  resourceId: uuid('resource_id'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [
  uniqueIndex('scim_group_members_group_id_member_ref_key').on(t.groupId, t.memberRef),
  index('scim_group_members_resource_idx').on(t.resourceId),
])

// ---------------------------------------------------------------------------
// Billing. See migrations/0020_billing.sql for the policies, which are the
// interesting half: a Stripe webhook has no tenant and declares the customer
// its verified payload named.
// ---------------------------------------------------------------------------

/** One Stripe customer per organization, keyed by the organization so that a
 *  second one is a constraint violation rather than a double charge. */
export const billingCustomers = pgTable('billing_customers', {
  orgId: uuid('org_id').primaryKey(),
  stripeCustomerId: text('stripe_customer_id').notNull(),
  email: text('email'),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
})

/** Metadata, never a card. A table of its own so that a verified delivery can
 *  write it without holding UPDATE on the column that says which organization
 *  a customer belongs to; see migrations/0020_billing.sql. */
export const paymentMethods = pgTable('payment_methods', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  stripePaymentMethodId: text('stripe_payment_method_id').notNull(),
  stripeCustomerId: text('stripe_customer_id').notNull(),
  kind: text('kind').notNull().default('card'),
  brand: text('brand'),
  last4: text('last4'),
  expMonth: integer('exp_month'),
  expYear: integer('exp_year'),
  detachedAt: timestamp('detached_at', { withTimezone: true }),
  lastEventAt: timestamp('last_event_at', { withTimezone: true }).notNull(),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
})

export const subscriptions = pgTable('subscriptions', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  stripeSubscriptionId: text('stripe_subscription_id').notNull(),
  stripeCustomerId: text('stripe_customer_id').notNull(),
  /** The plan this entitles, which is what moves organizations.plan. */
  plan: text('plan').notNull(),
  priceId: text('price_id'),
  /** Seats. The only number a price multiplies. */
  quantity: integer('quantity').notNull().default(1),
  status: text('status').notNull(),
  currentPeriodStart: timestamp('current_period_start', { withTimezone: true }),
  currentPeriodEnd: timestamp('current_period_end', { withTimezone: true }),
  cancelAtPeriodEnd: boolean('cancel_at_period_end').notNull().default(false),
  canceledAt: timestamp('canceled_at', { withTimezone: true }),
  /** The watermark that makes an out-of-order delivery harmless. */
  lastEventAt: timestamp('last_event_at', { withTimezone: true }).notNull(),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
})

export const invoices = pgTable('invoices', {
  id: uuid('id').primaryKey().defaultRandom(),
  orgId: uuid('org_id').notNull(),
  stripeInvoiceId: text('stripe_invoice_id').notNull(),
  stripeCustomerId: text('stripe_customer_id').notNull(),
  stripeSubscriptionId: text('stripe_subscription_id'),
  number: text('number'),
  status: text('status').notNull(),
  // Minor units as an integer, because that is what money is.
  amountDue: bigint('amount_due', { mode: 'number' }).notNull().default(0),
  amountPaid: bigint('amount_paid', { mode: 'number' }).notNull().default(0),
  currency: text('currency').notNull().default('usd'),
  hostedInvoiceUrl: text('hosted_invoice_url'),
  periodStart: timestamp('period_start', { withTimezone: true }),
  periodEnd: timestamp('period_end', { withTimezone: true }),
  paidAt: timestamp('paid_at', { withTimezone: true }),
  lastEventAt: timestamp('last_event_at', { withTimezone: true }).notNull(),
  createdAt: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
})

/** Every verified delivery, once. The primary key is the provider's own event
 *  id, so a retry inserts nothing and nothing is applied twice. */
export const billingEvents = pgTable('billing_events', {
  stripeEventId: text('stripe_event_id').primaryKey(),
  /** Null until the customer is attached to an organization. */
  orgId: uuid('org_id'),
  stripeCustomerId: text('stripe_customer_id').notNull(),
  type: text('type').notNull(),
  eventCreatedAt: timestamp('event_created_at', { withTimezone: true }).notNull(),
  receivedAt: timestamp('received_at', { withTimezone: true }).notNull().defaultNow(),
  processedAt: timestamp('processed_at', { withTimezone: true }),
  outcome: text('outcome').notNull().default('unresolved'),
  payload: jsonb('payload').notNull().default({}),
})

// ---------------------------------------------------------------------------
// Analytics
//
// A closed stream, deliberately carrying no org_id. See migrations/0021: an
// org_id would make these tables joinable back to a customer, which is the one
// property the surrogate exists to remove. They are absent from
// tenantScopedTables below for that reason and are named in the cross-tenant
// suite's list of tables that are global on purpose.
// ---------------------------------------------------------------------------

export const analyticsEvents = pgTable('analytics_events', {
  eventId: text('event_id').notNull(),
  name: text('name').notNull(),
  version: smallint('version').notNull().default(1),
  occurredAt: timestamp('occurred_at', { withTimezone: true }).notNull(),
  receivedAt: timestamp('received_at', { withTimezone: true }).notNull().defaultNow(),
  source: text('source').notNull(),
  orgSurrogate: text('org_surrogate'),
  sessionSurrogate: text('session_surrogate'),
  actorKind: text('actor_kind').notNull(),
  privacyBasis: text('privacy_basis').notNull(),
  consentId: text('consent_id'),
  payload: jsonb('payload').notNull().default({}),
}, (t) => [
  primaryKey({ columns: [t.eventId, t.occurredAt] }),
  index('analytics_events_day_idx').on(t.occurredAt, t.name),
])

export const analyticsOrgFacts = pgTable('analytics_org_facts', {
  orgSurrogate: text('org_surrogate').primaryKey(),
  firstSeenOn: date('first_seen_on').notNull(),
  lastActiveOn: date('last_active_on').notNull(),
  firstEnvironmentOn: date('first_environment_on'),
  firstProvenRunOn: date('first_proven_run_on'),
  firstPaidOn: date('first_paid_on'),
  plan: text('plan'),
  environmentsCreated: bigint('environments_created', { mode: 'number' }).notNull().default(0),
  runsFinished: bigint('runs_finished', { mode: 'number' }).notNull().default(0),
  updatedAt: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [
  index('analytics_org_facts_first_seen_idx').on(t.firstSeenOn),
  index('analytics_org_facts_last_active_idx').on(t.lastActiveOn),
])

export const analyticsDaily = pgTable('analytics_daily', {
  day: date('day').notNull(),
  name: text('name').notNull(),
  dimA: text('dim_a').notNull().default(''),
  dimB: text('dim_b').notNull().default(''),
  events: bigint('events', { mode: 'number' }).notNull().default(0),
  organizations: bigint('organizations', { mode: 'number' }).notNull().default(0),
  sessions: bigint('sessions', { mode: 'number' }).notNull().default(0),
  computedAt: timestamp('computed_at', { withTimezone: true }).notNull().defaultNow(),
}, (t) => [
  primaryKey({ columns: [t.day, t.name, t.dimA, t.dimB] }),
  index('analytics_daily_day_idx').on(t.day, t.name),
])

export const analyticsRollupState = pgTable('analytics_rollup_state', {
  id: boolean('id').primaryKey().default(true),
  lastRunAt: timestamp('last_run_at', { withTimezone: true }),
  settledAfter: date('settled_after'),
})

/** Every table the application writes to, for the cross-tenant suite. A table
 *  added to the schema and forgotten here is a table nobody proved is
 *  isolated, so the suite asserts this list covers the database. */
export const tenantScopedTables = [
  members, githubInstallations, repositories, environments, goldenVersions,
  runs, verdicts, artifacts, maskingRules, networkRules, runtimes, engineTokens,
  events, auditEntries, providerKeys, providerBudgets,
  ssoConnections, ssoConnectionSecrets, ssoDomains, ssoLoginStates,
  ssoAssertionsSeen, ssoBreakGlassCodes,
  scimTokens, scimResources, scimGroups, scimGroupMembers,
  billingCustomers, paymentMethods, subscriptions, invoices, billingEvents,
] as const
