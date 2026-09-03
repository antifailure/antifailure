// The analytics catalog: every event that may be recorded, and nothing else.
//
// CLOSED MEANS REFUSED, NOT FILTERED.
//
// An event whose name is not here is rejected and counted. A payload field this
// file does not declare is rejected and counted. Neither is stored and cleaned
// up later, because a store that accepts anything is a store somebody
// eventually queries for the thing nobody meant to keep, and by then the rows
// exist.
//
// WHAT A FIELD MAY BE.
//
// A closed enum, a bounded identifier matched against a regular expression, a
// whole number in a range, or a boolean. That is the entire vocabulary and it
// is not an accident: every one of those is a value whose whole domain can be
// written down, so a reviewer can read this file and know exactly what the
// analytics store can contain.
//
// What a field may never be is free text. There is no `string` kind here, and
// adding one would be the change that quietly makes every rule below optional.
//
// WHERE THE DANGEROUS VALUES ARE TURNED INTO SAFE ONES.
//
// At the edge, in normalize.ts, before anything reaches this file. A referrer
// becomes a source enum and a campaign id. A URL becomes a canonical route id.
// A duration becomes a bucket. The raw values are never passed on and never
// stored, so the only way a repository name or a query string could reach the
// database is if somebody added a free-text field here, which they cannot.
//
// EVERY EVENT HAS A PRODUCER, AND THE PRODUCER IS NAMED.
//
// `producer` is a sentence saying where the event is emitted from. It is not
// decoration: a test reads it and fails on an event whose named producer has no
// call site, because an event type nothing emits is an unshipped feature that
// looks finished on a dashboard that shows zero for it forever.

/** The eight funnels the catalog is organised into. */
export const FUNNELS = [
  'acquisition',
  'identity',
  'onboarding',
  'environment',
  'validation',
  'adoption',
  'revenue',
  'retention',
] as const
export type Funnel = (typeof FUNNELS)[number]

/** Where an event was recorded. */
export const SOURCES = ['site', 'console', 'engine', 'control_plane'] as const
export type Source = (typeof SOURCES)[number]

/** What kind of thing acted. Never who. */
export const ACTOR_KINDS = ['visitor', 'user', 'engine', 'system'] as const
export type ActorKind = (typeof ACTOR_KINDS)[number]

/**
 * Why this data may be held.
 *
 * Recorded per event rather than per system, because the answer differs by
 * event and a single answer for the whole store is the answer that is wrong for
 * some of it. Nothing emits `consent` today: no consent record exists to point
 * at, and inventing one would be worse than the gap. The database refuses a
 * consent identifier without this basis and refuses this basis without one, so
 * the day a consent record exists the pairing is already correct.
 */
export const PRIVACY_BASES = ['legitimate_interest', 'contract', 'consent'] as const
export type PrivacyBasis = (typeof PRIVACY_BASES)[number]

// ---------------------------------------------------------------------------
// Field kinds
// ---------------------------------------------------------------------------

export type FieldSpec =
  /** One of a fixed list. The whole domain is written down. */
  | { kind: 'enum'; values: readonly string[] }
  /** A bounded identifier. The pattern is the domain, and it is anchored. */
  | { kind: 'id'; pattern: RegExp; maxLength: number }
  /** A whole number in a closed range. */
  | { kind: 'count'; min: number; max: number }
  | { kind: 'boolean' }

export interface EventSpec {
  funnel: Funnel
  version: number
  source: Source
  actorKind: ActorKind
  privacyBasis: PrivacyBasis
  /** What question this event exists to answer, for whoever reads a chart. */
  answers: string
  /** Where it is emitted. Checked by a test against real call sites. */
  producer: string
  /** Every field a payload may carry. A field not here is a rejection. */
  payload: Record<string, FieldSpec>
  /** Which payload fields become the two rollup dimensions, in order. An event
   *  with no dimensions rolls up as a plain daily count. */
  dimensions: readonly string[]
  /** True when the event carries an organization surrogate. Checked on the way
   *  in, so an event that is supposed to be attributable and arrives without an
   *  organization is a rejection rather than a row nobody can group. */
  organization: 'required' | 'optional' | 'never'
  /** True when the event carries an anonymous session surrogate. */
  session: 'required' | 'optional' | 'never'
}

// ---------------------------------------------------------------------------
// The vocabularies, named once because several events share them
// ---------------------------------------------------------------------------

/**
 * Where a visit came from, as a bounded set.
 *
 * Derived from the referrer's registrable domain at the edge and then thrown
 * away. `ai` is separate from `search` because an answer engine sending a
 * reader is a different acquisition channel from a results page, and rolling
 * them together is how a founder cannot tell which one is working.
 */
export const VISIT_SOURCES = [
  'direct',
  'search',
  'ai',
  'social',
  'github',
  'news',
  'email',
  'campaign',
  'referral',
  'internal',
] as const

/**
 * Which page, as a canonical route id rather than a path.
 *
 * A path carries a slug, and a slug is a title somebody wrote. These are the
 * shapes of page the site has, so `product_detail` covers every product page
 * without naming which one. That is a deliberate loss: knowing that product
 * pages convert is the question, and knowing which reader read which page is
 * not.
 */
export const SITE_ROUTES = [
  'home',
  'product',
  'product_detail',
  'solutions',
  'solutions_detail',
  'pricing',
  'blog',
  'blog_post',
  'legal',
  'signin',
  'signup',
  'other',
] as const

/** A campaign identifier, bounded and lower case. Never a query string. */
export const CAMPAIGN_PATTERN = /^[a-z0-9][a-z0-9_-]{0,31}$/

/** How long something took, as a bucket. A duration to the millisecond is a
 *  fingerprint; a bucket answers "is this getting slower". */
export const DURATION_BUCKETS = [
  'under_1m',
  'under_5m',
  'under_30m',
  'under_2h',
  'under_12h',
  'under_24h',
  'over_24h',
] as const

/** What an environment ran on, as a class rather than a name. */
export const RUNTIME_CLASSES = ['local', 'docker', 'kubernetes', 'cloud', 'unknown'] as const

/** The plans, matching web/apps/api/src/limits.ts. A test holds them together. */
export const PLANS = ['free', 'team', 'enterprise'] as const

const VERDICTS = ['pass', 'fail', 'flaky', 'blocked', 'unverified'] as const
const RUN_KINDS = ['test', 'agent', 'load', 'migration', 'other'] as const

// ---------------------------------------------------------------------------
// The catalog
// ---------------------------------------------------------------------------

export const CATALOG = {
  // -------------------------------------------------------------------------
  // Acquisition. Where people came from and where they landed.
  // -------------------------------------------------------------------------

  'site.page_viewed': {
    funnel: 'acquisition',
    version: 1,
    source: 'site',
    actorKind: 'visitor',
    // Aggregate, cookieless, and scoped to one browsing session. No profile is
    // built, nothing is shared, and the identifier dies with the tab.
    privacyBasis: 'legitimate_interest',
    answers: 'Which pages people land on, and which channel sent them.',
    producer: 'www/lib/beacon.ts, from the site beacon on every page view',
    payload: {
      route: { kind: 'enum', values: SITE_ROUTES },
      source: { kind: 'enum', values: VISIT_SOURCES },
      campaign: { kind: 'id', pattern: CAMPAIGN_PATTERN, maxLength: 32 },
      /** True for the first page of a browsing session, which is the one whose
       *  referrer is external and therefore the one attribution comes from. */
      entry: { kind: 'boolean' },
    },
    dimensions: ['source', 'route'],
    organization: 'never',
    session: 'required',
  },

  'site.cta_engaged': {
    funnel: 'acquisition',
    version: 1,
    source: 'site',
    actorKind: 'visitor',
    privacyBasis: 'legitimate_interest',
    answers: 'Which call to action people press, and on which page.',
    producer: 'www/lib/beacon.ts, from the sign-up screen the primary buttons lead to',
    payload: {
      // One value, because there is one producer. The docs, GitHub and install
      // buttons are server-rendered links with no click handler, and declaring
      // them here would put three bars on a chart that read zero forever. The
      // variation worth having is the route: dialogs opened per page against
      // submissions per page is form conversion by page.
      cta: { kind: 'enum', values: ['waitlist_open'] as const },
      route: { kind: 'enum', values: SITE_ROUTES },
    },
    dimensions: ['cta', 'route'],
    organization: 'never',
    session: 'required',
  },

  'site.waitlist_submitted': {
    funnel: 'acquisition',
    version: 1,
    source: 'site',
    actorKind: 'visitor',
    privacyBasis: 'legitimate_interest',
    answers:
      'Which channel and which landing page produce a waitlist address, which is ' +
      'the join between acquisition and identity.',
    producer: 'www/lib/beacon.ts, after the waitlist endpoint answers',
    payload: {
      /** The channel and page the SESSION started on, not this page. That is
       *  what makes attribution answer "what brought them here" rather than
       *  "which page had the form on it". */
      source: { kind: 'enum', values: VISIT_SOURCES },
      landing: { kind: 'enum', values: SITE_ROUTES },
      campaign: { kind: 'id', pattern: CAMPAIGN_PATTERN, maxLength: 32 },
      outcome: { kind: 'enum', values: ['joined', 'already', 'refused'] as const },
    },
    dimensions: ['source', 'landing'],
    organization: 'never',
    session: 'required',
  },

  // -------------------------------------------------------------------------
  // Identity. Who arrived, and how.
  // -------------------------------------------------------------------------

  'identity.signed_in': {
    funnel: 'identity',
    version: 1,
    source: 'control_plane',
    actorKind: 'user',
    privacyBasis: 'legitimate_interest',
    answers: 'Which sign-in method people use, and how many are signing in for the first time.',
    producer: 'web/apps/api/src/server.ts, on every completed sign-in',
    payload: {
      // Two values, because there are two producers. The device flow issues a
      // terminal credential to somebody who is already signed in, which is not
      // a sign-in, and single sign-on issues its sessions from the enterprise
      // edition. Declaring either here would be a bar that reads zero forever
      // and looks like a broken chart rather than an absent feature.
      method: { kind: 'enum', values: ['github', 'email_link'] as const },
      first_time: { kind: 'boolean' },
    },
    dimensions: ['method'],
    // Optional rather than required: a sign-in that lands with no organization
    // is exactly the state the console has a screen for, and losing the event
    // would hide the funnel step where people get stuck.
    organization: 'optional',
    session: 'never',
  },

  'identity.organization_created': {
    funnel: 'identity',
    version: 1,
    source: 'control_plane',
    actorKind: 'system',
    privacyBasis: 'contract',
    answers: 'How many organizations exist, and which route created them.',
    producer:
      'web/apps/api/src/github/webhook.ts rememberInstallation, on the delivery whose upsert ' +
      'actually creates the row. The other way an organization can come into existence is the ' +
      'bootstrap command, which runs as a CLI against the migration credential with no analytics ' +
      'recorder, so a self-hosted first organization is not counted here.',
    payload: {},
    dimensions: [],
    organization: 'required',
    session: 'never',
  },

  'identity.member_joined': {
    funnel: 'identity',
    version: 1,
    source: 'control_plane',
    actorKind: 'system',
    privacyBasis: 'contract',
    answers: 'How organizations grow, and whether growth comes from a directory or by hand.',
    producer:
      'web/apps/api/src/routers/index.ts members.sync, once per member the reconciliation adds. ' +
      'There is deliberately no `via` field: membership can only be created by that route today, ' +
      'so an enum with one value would be a dimension that is the same bar forever, and the ' +
      'directory-provisioned paths live in the enterprise edition rather than here.',
    payload: {
      role: { kind: 'enum', values: ['owner', 'admin', 'member', 'viewer'] as const },
    },
    dimensions: ['role'],
    organization: 'required',
    session: 'never',
  },

  // -------------------------------------------------------------------------
  // Onboarding. The steps between an account and a working environment.
  // -------------------------------------------------------------------------

  'onboarding.engine_token_minted': {
    funnel: 'onboarding',
    version: 1,
    source: 'control_plane',
    actorKind: 'user',
    privacyBasis: 'contract',
    answers: 'How many organizations get as far as wiring an engine into their CI.',
    producer: 'web/apps/api/src/tokens.ts mintEngineToken, from POST /v1/tokens',
    payload: {
      // console is in the enum and nothing emits it today, deliberately and
      // visibly: the console can list and revoke engine tokens and cannot mint
      // one, so wiring CI is a terminal-only step. That is a product gap rather
      // than an analytics one, and a bar labelled console reading zero forever
      // is the way it becomes visible to somebody who can fix it.
      via: { kind: 'enum', values: ['cli', 'console'] as const },
      first: { kind: 'boolean' },
    },
    dimensions: ['via'],
    organization: 'required',
    session: 'never',
  },

  'onboarding.provider_key_stored': {
    funnel: 'onboarding',
    version: 1,
    source: 'control_plane',
    actorKind: 'user',
    privacyBasis: 'contract',
    answers: 'How many organizations configure a model provider, which agent runs need.',
    producer: 'web/apps/api/src/providers/store.ts saveKey',
    payload: {
      provider: { kind: 'enum', values: ['anthropic', 'openai'] as const },
      replaced: { kind: 'boolean' },
    },
    dimensions: ['provider'],
    organization: 'required',
    session: 'never',
  },

  // -------------------------------------------------------------------------
  // Environment lifecycle. Counted from the engine's own events, without any
  // of what those events carry: no repository, no branch, no preview URL.
  // -------------------------------------------------------------------------

  'environment.created': {
    funnel: 'environment',
    version: 1,
    source: 'engine',
    actorKind: 'engine',
    privacyBasis: 'contract',
    answers: 'How much the product is actually used, and on what.',
    producer: 'web/apps/api/src/ingest.ts, when an environment row is created',
    payload: {
      runtime_class: { kind: 'enum', values: RUNTIME_CLASSES },
      declared_lifetime: { kind: 'boolean' },
    },
    dimensions: ['runtime_class'],
    organization: 'required',
    session: 'never',
  },

  'environment.torn_down': {
    funnel: 'environment',
    version: 1,
    source: 'engine',
    actorKind: 'engine',
    privacyBasis: 'contract',
    answers: 'How long environments live, which is the number the spend cap is about.',
    producer: 'web/apps/api/src/ingest.ts, on an environment.torn_down event',
    payload: {
      duration: { kind: 'enum', values: DURATION_BUCKETS },
      runtime_class: { kind: 'enum', values: RUNTIME_CLASSES },
    },
    dimensions: ['duration', 'runtime_class'],
    organization: 'required',
    session: 'never',
  },

  // -------------------------------------------------------------------------
  // Validation. Whether the product did the thing it exists to do.
  // -------------------------------------------------------------------------

  'validation.run_finished': {
    funnel: 'validation',
    version: 1,
    source: 'engine',
    actorKind: 'engine',
    privacyBasis: 'contract',
    answers: 'What proportion of runs reach a real verdict rather than an unverified one.',
    producer: 'web/apps/api/src/ingest.ts, on a verdict.recorded event',
    payload: {
      kind: { kind: 'enum', values: RUN_KINDS },
      verdict: { kind: 'enum', values: VERDICTS },
    },
    dimensions: ['kind', 'verdict'],
    organization: 'required',
    session: 'never',
  },

  // -------------------------------------------------------------------------
  // Adoption. Which capabilities anybody actually reaches for.
  // -------------------------------------------------------------------------

  'adoption.feature_used': {
    funnel: 'adoption',
    version: 1,
    source: 'control_plane',
    actorKind: 'user',
    privacyBasis: 'legitimate_interest',
    answers: 'Which parts of the product get used, and which were built for nobody.',
    producer:
      'web/apps/api/src/routers/index.ts, web/apps/api/src/routers/dispatch.ts, ' +
      'web/apps/api/src/routers/runtimes.ts and web/apps/api/src/providers/store.ts, one call ' +
      'beside each audit entry on a mutation that changes policy or starts work',
    payload: {
      feature: {
        kind: 'enum',
        values: [
          'masking_rule_changed',
          'masking_rule_approved',
          'network_rule_changed',
          'network_rule_approved',
          'agent_run',
          'load_run',
          'environment_requested',
          'environment_torn_down',
          'audit_exported',
          'runtime_registered',
          'provider_budget_set',
        ] as const,
      },
    },
    dimensions: ['feature'],
    organization: 'required',
    session: 'never',
  },

  // -------------------------------------------------------------------------
  // Revenue. The plan, never an amount and never a card.
  // -------------------------------------------------------------------------

  'revenue.plan_changed': {
    funnel: 'revenue',
    version: 1,
    source: 'control_plane',
    actorKind: 'system',
    privacyBasis: 'contract',
    answers: 'Who upgrades, who downgrades, and how long it took them.',
    producer: 'web/apps/api/src/billing/webhook.ts, when a delivery moves an organization plan',
    payload: {
      from: { kind: 'enum', values: PLANS },
      to: { kind: 'enum', values: PLANS },
    },
    dimensions: ['from', 'to'],
    organization: 'required',
    session: 'never',
  },

  'revenue.subscription_changed': {
    funnel: 'revenue',
    version: 1,
    source: 'control_plane',
    actorKind: 'system',
    privacyBasis: 'contract',
    answers: 'How many subscriptions are healthy, past due, or gone.',
    producer: 'web/apps/api/src/billing/webhook.ts, on every subscription delivery',
    payload: {
      plan: { kind: 'enum', values: PLANS },
      status: {
        kind: 'enum',
        values: [
          'trialing', 'active', 'past_due', 'canceled', 'unpaid', 'incomplete',
          'incomplete_expired', 'paused', 'other',
        ] as const,
      },
    },
    dimensions: ['plan', 'status'],
    organization: 'required',
    session: 'never',
  },
} as const satisfies Record<string, EventSpec>

export type EventName = keyof typeof CATALOG
export const EVENT_NAMES = Object.keys(CATALOG) as EventName[]

/**
 * EVENTS ARE COUNTS. FACTS ARE MILESTONES. Two funnels have no event, and that
 * is the rule rather than an omission.
 *
 * A count is commutative: two batches arriving in either order produce the same
 * number, so an event is exactly the right shape for one. A milestone is not.
 * "The first time this organization proved something" was an event first, and
 * it cannot be one: emitting it means reading whether it has already happened
 * and then writing, so two batches arriving at once both read nothing and both
 * emit, and a late batch carrying an OLDER verdict has to move the milestone
 * earlier, which an event already written cannot do.
 *
 * As a column it is `LEAST(existing, incoming)`: one statement, no race, and
 * the same answer whatever order events arrive in. So the milestones live in
 * analytics_org_facts, and the two funnels below are answered from it.
 *
 * ONBOARDING's last step is `first_event_on`, the day an organization's engine
 * first reached the control plane. RETENTION is `last_active_on` against
 * `first_seen_on`, which is a question about the ABSENCE of activity, and an
 * event cannot carry an absence.
 *
 * Written down here because a catalog covering eight funnels with events for
 * six of them otherwise reads like somebody forgot two.
 */
export const DERIVED_FROM_FACTS: Record<string, string> = {
  onboarding:
    'The last onboarding step, an organization whose engine has reported at all, is ' +
    'analytics_org_facts.first_event_on rather than an event, because it is a milestone.',
  environment:
    'Whether an organization ever brought one up is analytics_org_facts.first_environment_on.',
  validation:
    'Activation, the first verdict that proved something, is ' +
    'analytics_org_facts.first_proven_run_on. It was an event until the ordering tests ' +
    'showed two concurrent batches could both claim to be the first.',
  revenue: 'The day an organization first left the free plan is analytics_org_facts.first_paid_on.',
  retention:
    'Retention is analytics_org_facts.last_active_on against first_seen_on. It has no event ' +
    'because it is a question about the absence of activity.',
}

/** Every event in a funnel, for the dashboard and the documentation. */
export function eventsIn(funnel: Funnel): EventName[] {
  return EVENT_NAMES.filter((name) => CATALOG[name].funnel === funnel)
}

// ---------------------------------------------------------------------------
// Conversion sequences
//
// A NOTE ON THE WORD FUNNEL, WHICH THIS FILE NOW USES FOR TWO THINGS.
//
// `Funnel` above is a SECTION of the catalog: eight names that group the events
// by which part of the product they are about. It has no order and no window,
// and it is metadata for a reader.
//
// What follows is a MEASURED SEQUENCE: an ordered list of steps, a subject that
// has to complete them, and a window they all have to fall inside. That is the
// thing anybody means by a conversion funnel, and it is a different object.
// Both names are kept because renaming the first would touch every event.
//
// WHY THE SEQUENCES ARE DECLARED HERE RATHER THAN BUILT IN A UI.
//
// The obvious product is a funnel builder: pick any events, pick a window, see
// a chart. It is not available here and the reason is the same one the closed
// event catalog is built on. The rows a funnel is computed from carry a subject
// surrogate, and the application cannot read them; only the rollup can, and the
// rollup computes what this file declares. A builder would need the application
// to be able to run an arbitrary query against subject level rows, which is the
// exact capability migrations 0029 and 0030 exist to withhold.
//
// So a new question costs a declaration here and a rollup pass, and in exchange
// nobody can ask the store to single anybody out. This repository has made that
// trade once already for events; this is the same trade for queries.
// ---------------------------------------------------------------------------

/** Who has to complete the steps. Matches subject_kind in analytics_subject_days. */
export type FunnelSubject = 'organization' | 'session'

export interface FunnelStepDefinition {
  event: EventName
  /**
   * Which payload values count as completing this step. Absent means any
   * occurrence of the event does.
   *
   * The field must be one the event declares, and every value must be in that
   * field's enum. A test checks both, because a step filtering on a value that
   * cannot occur is a step nobody ever completes, which renders as a funnel
   * that collapses to zero and reads like a product problem.
   */
  where?: { field: string; values: readonly string[] }
  /** What reaching this step means, in the words the page needs. */
  meaning: string
}

export interface FunnelDefinition {
  /** Stored in analytics_funnel_weeks.funnel, so it is a stable identifier
   *  rather than a title: renaming the title must not orphan the rows. */
  id: string
  title: string
  subject: FunnelSubject
  /**
   * How long after the FIRST step the remaining steps have to happen.
   *
   * From the first step rather than between consecutive steps, because "signed
   * up and got a proven run within thirty days" is the question somebody asks,
   * and "each step within thirty days of the last" is a much weaker claim that
   * a subject could satisfy over a year without ever converting.
   */
  windowDays: number
  /** Why this window and not another, shown next to the chart. A window is an
   *  assumption, and an assumption a reader cannot see is one they will read
   *  the numbers without. */
  windowReason: string
  steps: readonly FunnelStepDefinition[]
}

export const FUNNEL_DEFINITIONS: readonly FunnelDefinition[] = [
  {
    id: 'acquisition',
    title: 'Visit to waitlist',
    subject: 'session',
    // A session cannot outlive a day: www/lib/analytics.ts caps it at twenty
    // four hours and ends it after thirty minutes idle. So a window wider than
    // a day could never change an answer, and a narrower one would cut off a
    // reader who left the tab open over lunch.
    windowDays: 1,
    windowReason:
      'One day, because a browsing session cannot last longer than that: the site ends a ' +
      'session after thirty minutes idle and after twenty four hours whatever happens.',
    steps: [
      {
        event: 'site.page_viewed',
        meaning: 'Landed on any page of the site.',
      },
      {
        event: 'site.cta_engaged',
        meaning: 'Reached the sign-up screen, from wherever they were.',
      },
      {
        event: 'site.waitlist_submitted',
        // `refused` is excluded on purpose. An address the endpoint would not
        // take is not a conversion, and counting it as one would make the last
        // step move whenever the validation rules changed.
        where: { field: 'outcome', values: ['joined', 'already'] },
        meaning: 'Left an address that the waitlist accepted.',
      },
    ],
  },
  {
    id: 'activation',
    title: 'Organization to proven run',
    subject: 'organization',
    windowDays: 30,
    windowReason:
      'Thirty days, which is the period a trial covers. An organization that takes longer ' +
      'than that to prove something did not activate; it came back.',
    steps: [
      {
        event: 'identity.organization_created',
        meaning: 'The organization came into existence.',
      },
      {
        event: 'onboarding.engine_token_minted',
        meaning: 'Somebody wired an engine into their CI, which needs a token.',
      },
      {
        event: 'environment.created',
        meaning: 'That engine brought an environment up.',
      },
      {
        event: 'validation.run_finished',
        // The verdicts that PROVED something. blocked and unverified finished a
        // run without proving anything, which is the distinction the whole
        // product is about, so it cannot be smoothed over here.
        where: { field: 'verdict', values: ['pass', 'fail', 'flaky'] },
        meaning: 'Got a verdict that proved something, rather than blocked or unverified.',
      },
    ],
  },
]

/** One funnel by id, or undefined. Used by the read layer, which takes an id
 *  from a request and must not assume it names anything. */
export function funnelDefinition(id: string): FunnelDefinition | undefined {
  return FUNNEL_DEFINITIONS.find((f) => f.id === id)
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

export interface Rejection {
  /** Which rule refused it, as a stable identifier a counter can be keyed on. */
  reason:
    | 'unknown_event'
    | 'unknown_field'
    | 'bad_value'
    | 'missing_field'
    | 'organization_required'
    | 'organization_forbidden'
    | 'session_required'
    | 'session_forbidden'
    | 'bad_envelope'
  /** One sentence naming what to fix. Never quotes the offending value, because
   *  a rejection message is a place a rejected value would otherwise be
   *  written down, and this whole file exists to stop that. */
  detail: string
}

/** A payload that has been checked against the catalog. */
export type ValidPayload = Record<string, string | number | boolean>

/**
 * Checks one payload against its event's declaration.
 *
 * Rejects an unknown field rather than dropping it, and the difference is the
 * whole design. Dropping is what a permissive parser does, and it means a
 * producer that starts sending a repository name gets a green response and a
 * silently discarded field, so nobody finds out until somebody reads the
 * producer. Refusing means the producer's own tests fail on the day the field
 * is added.
 *
 * Never quotes a value in a message, for the same reason: a rejection log is
 * exactly where a rejected value would end up being persisted.
 */
export function validatePayload(
  name: EventName,
  payload: Record<string, unknown>,
): { ok: true; payload: ValidPayload } | { ok: false; problem: Rejection } {
  const spec: EventSpec = CATALOG[name]
  const out: ValidPayload = {}

  for (const key of Object.keys(payload)) {
    if (!Object.hasOwn(spec.payload, key)) {
      return {
        ok: false,
        problem: {
          reason: 'unknown_field',
          detail: `${name} declares no field named ${key}. Add it to the catalog or stop sending it.`,
        },
      }
    }
  }

  for (const [key, field] of Object.entries(spec.payload)) {
    const value = payload[key]
    if (value === undefined || value === null) {
      // An absent field is absent, not empty. Every field here is optional at
      // the wire and its absence is meaningful: a page view with no campaign
      // came from no campaign, and writing '' for it would put a value in a
      // chart that nobody sent.
      continue
    }
    const checked = checkField(field, value)
    if (checked === null) {
      return {
        ok: false,
        problem: {
          reason: 'bad_value',
          detail: `${name}.${key} was not one of the values the catalog allows for it.`,
        },
      }
    }
    out[key] = checked
  }

  return { ok: true, payload: out }
}

function checkField(field: FieldSpec, value: unknown): string | number | boolean | null {
  switch (field.kind) {
    case 'enum':
      return typeof value === 'string' && field.values.includes(value) ? value : null
    case 'id':
      // Length first, so a pathological input is refused before the regular
      // expression sees it. The patterns here are all linear, and checking the
      // bound anyway costs nothing and removes the question.
      if (typeof value !== 'string' || value.length > field.maxLength) return null
      return field.pattern.test(value) ? value : null
    case 'count':
      if (typeof value !== 'number' || !Number.isInteger(value)) return null
      return value >= field.min && value <= field.max ? value : null
    case 'boolean':
      return typeof value === 'boolean' ? value : null
  }
}

/** Whether a name is in the catalog. Narrows, so a caller cannot forget to
 *  check before indexing. */
export function isEventName(name: string): name is EventName {
  return Object.hasOwn(CATALOG, name)
}

/**
 * The two dimension values a row rolls up under.
 *
 * The empty string for an absent dimension rather than null, because the daily
 * table's primary key has to distinguish rows and null is not equal to itself.
 */
export function dimensionsOf(name: EventName, payload: ValidPayload): [string, string] {
  const dims = CATALOG[name].dimensions
  return [String(payload[dims[0] ?? ''] ?? ''), String(payload[dims[1] ?? ''] ?? '')]
}
