/**
 * The Operations lane's shapes and the pure functions over them.
 *
 * Split from `admin-operations.ts` and holding NO IMPORT, which is what makes
 * it testable. The same split `loadshapes.ts` makes next to `load.ts`, for the
 * same reason: the calls next door reach the network and cannot run outside a
 * browser, and console unit tests are `node --test lib/*.test.ts`, which
 * resolves no `@/` alias and starts no browser. A helper that lives beside a
 * fetch is a helper with no test.
 *
 * Import from `@/lib/admin-operations`, not from here. That module re-exports
 * all of this, so a page imports one module and the contract with the control
 * plane is still one file to reconcile.
 */

export type Verdict = "ok" | "degraded" | "failing";
export interface HealthCheck {
  id: string;
  title: string;
  verdict: Verdict;
  value: number;
  unit: string;
  detail: string;
  /** Null when the check is ok. "No action needed" on every green row is noise
   *  on a page whose job is to make the red ones stand out. */
  remedy: string | null;
}
export interface HealthReport {
  checks: HealthCheck[];
  /** The worst verdict among the checks, derived on the server so the summary
   *  line cannot disagree with the rows under it. */
  verdict: Verdict;
  at: string;
}
export type TwinScope = "live" | "overdue" | "all";
export interface Twin {
  envId: string;
  orgId: string;
  orgSlug: string;
  repository: string;
  branch: string;
  pullRequest: number | null;
  state: string;
  previewUrl: string | null;
  runtime: string | null;
  goldenVersion: string | null;
  createdAt: string;
  updatedAt: string;
  expiresAt: string | null;
  tornDownAt: string | null;
  /** Past its expiry and still running. This is the row that costs money. */
  overdue: boolean;
  teardownPending: boolean;
  runs: number;
}
/**
 * How many rows the fleet and ledger routes are asked for.
 *
 * These two return an array and no cursor, so there is no `More` to render and
 * no way to ask for the next page. That makes the cap a claim the page has to
 * be honest about: at exactly this many rows the answer is "the first 200",
 * never "all of them", and the pages say which. Half the console's paging
 * defects were a screen that showed one page and called it the list.
 */
export const FLEET_LIMIT = 200;
export type TeardownStanding =
  | "nothing-to-reach"
  | "waiting-to-dispatch"
  | "dispatched-unconfirmed"
  | "confirmed"
  | "abandoned";
export interface Teardown {
  id: string;
  orgId: string;
  orgSlug: string;
  envId: string | null;
  repository: string | null;
  workflowRunId: string | null;
  reason: string;
  state: string;
  /** What actually happened, which the `state` column does not say: "pending"
   *  covers both asked for a second ago and asked for yesterday with nothing
   *  to reach. */
  standing: TeardownStanding;
  attempts: number;
  leaseHolder: string | null;
  leasedUntil: string | null;
  leaseExpired: boolean;
  lastError: string | null;
  requestedAt: string;
  acknowledgedAt: string | null;
  route: string;
}
export interface BlastRadius {
  organizations: number;
  environments: number;
  runs: number;
  alreadyRequested: number;
}
export interface RequestedTeardown {
  envId: string;
  recorded: boolean;
  /** False when there is no workflow run and no environment id, so nothing can
   *  be sent and the request will sit until it is abandoned. */
  reachable: boolean;
}
export interface FleetTeardownResult {
  radius: BlastRadius;
  requested: RequestedTeardown[];
  recorded: number;
  unreachable: number;
  /** The route's own sentence about what happens next. Rendered verbatim: it
   *  is the difference between "requested" and "torn down", and rewording it
   *  here would be the console claiming the second. */
  pending: string;
}
export type FindingKind = "sandbox-without-credential" | "never-approved" | "allow";
export interface FirewallRule {
  id: string;
  orgId: string;
  orgSlug: string;
  repository: string | null;
  host: string;
  mode: string;
  paths: string[] | null;
  methods: string[] | null;
  credential: string | null;
  note: string | null;
  position: number;
  proposedBy: string | null;
  approvedBy: string | null;
  approvedAt: string | null;
  createdAt: string;
  updatedAt: string;
}
export interface Finding {
  kind: FindingKind;
  rule: FirewallRule;
  says: string;
  /** `failing` is never acceptable, `review` is a judgement. Kept as the
   *  server's two words rather than scored into a number: a finding that is
   *  always wrong must not be something a threshold can hide. */
  severity: "failing" | "review";
}
export interface FirewallSummary {
  rules: number;
  organizations: number;
  forwardingLiveCredentials: number;
  neverApproved: number;
  allowed: number;
}
export type ControlName = "maintenance" | "signups" | "new_runs";
export interface ControlDefinition {
  name: ControlName;
  title: string;
  /** Exactly what stops working AND what keeps working. Read before
   *  confirming, so it is rendered in full rather than truncated. */
  effect: string;
  /** Where the refusal is, as `path/from/src:symbol`. A test opens that file
   *  and greps it for that symbol, so this is a claim the build checks. */
  enforcedBy: string;
  release: string;
}
export interface ControlState {
  name: ControlName;
  definition: ControlDefinition;
  engaged: boolean;
  engagedAt: string | null;
  reason: string | null;
  engagedBy: string | null;
  updatedAt: string | null;
}
/** The windows the routes accept. Not a free number: these queries scan by
 *  time, and a larger one during an incident is a statement timeout rather
 *  than an answer. */
export const WINDOWS = [
  { value: "1", label: "Last hour" },
  { value: "6", label: "Last 6 hours" },
  { value: "24", label: "Last 24 hours" },
  { value: "72", label: "Last 3 days" },
  { value: "168", label: "Last 7 days" },
] as const;
export interface FailureGroup {
  /** Null when the run failed and recorded no code. Shown as its own row: a
   *  failure with no code is a gap in the engine, and hiding it hides the gap. */
  failureCode: string | null;
  kind: string;
  state: string;
  runs: number;
  organizations: number;
  firstSeen: string;
  lastSeen: string;
  latestDetail: string | null;
  latestRunId: string;
}
export interface WorkflowFailure {
  workflow: string;
  value: string;
  runs: number;
  organizations: number;
  lastSeen: string;
  latestSummary: string | null;
}
export interface EventTypeSummary {
  type: string;
  events: number;
  organizations: number;
  lastReceivedAt: string;
  /** Seconds between the engine stamping the event and this control plane
   *  receiving it. Either timestamp alone looks fine while the pair is wrong. */
  lagSeconds: number;
}
export interface LogsOverview {
  hours: number;
  from: string;
  at: string;
  failures: FailureGroup[];
  workflows: WorkflowFailure[];
  eventTypes: EventTypeSummary[];
  /** Per list, whether it was cut off at `limit`. The page says "the top N"
   *  rather than implying it is the whole answer. */
  truncated: { failures: boolean; workflows: boolean; eventTypes: boolean };
  limit: number;
}
export interface EventRow {
  id: string;
  occurredAt: string;
  receivedAt: string;
  type: string;
  orgId: string;
  orgSlug: string;
  envId: string | null;
  runId: string | null;
  /** The payload's top level key NAMES. The values are never returned: the
   *  payload is the customer's data, and shape plus timing is what an operator
   *  debugging ingestion actually needs. */
  payloadKeys: string[];
  payloadBytes: number;
}
export interface EmailReach {
  linksIssued: number;
  linksUsed: number;
  /** Issued, never used, and now too old to use. At any volume this is what a
   *  delivery failure looks like from inside a product that keeps no delivery
   *  record. */
  linksUnused: number;
  linksLive: number;
  invitationsSent: number;
  invitationsAccepted: number;
  invitationsOpen: number;
  billingContacts: number;
}
export interface EmailStatus {
  hours: number;
  from: string;
  at: string;
  /** Whether this process has a mailer at all. The token row is written either
   *  way, so no query over the database can tell the two apart. */
  canSend: boolean;
  provider: "resend" | "recording" | "other" | null;
  /** Messages are kept in memory and delivered to nobody, which looks identical
   *  to working from every other angle. */
  recordingOnly: boolean;
  reach: EmailReach;
}
export type LinkStanding = "used" | "live" | "unused";
export interface SignInLink {
  id: string;
  email: string;
  createdAt: string;
  expiresAt: string;
  consumedAt: string | null;
  standing: LinkStanding;
  ip: string | null;
  userAgent: string | null;
  redirectTo: string | null;
}

/** The console's tones for a health verdict. `degraded` is warn rather than
 *  fail: a page where everything worth looking at is red is a page where
 *  nothing is. */
export function toneForVerdict(verdict: Verdict): "pass" | "warn" | "fail" {
  return verdict === "ok" ? "pass" : verdict === "degraded" ? "warn" : "fail";
}

/**
 * The tone of a teardown standing.
 *
 * `nothing-to-reach` and `abandoned` are failures rather than warnings, and
 * that is the whole point of the standing existing: both of them look like
 * "pending" in the state column, and both mean the environment is still up and
 * nothing further will happen on its own.
 */
export function toneForStanding(standing: TeardownStanding): "pass" | "warn" | "fail" {
  if (standing === "confirmed") return "pass";
  if (standing === "abandoned" || standing === "nothing-to-reach") return "fail";
  return "warn";
}

/** How a standing reads to a person. The wire values are hyphenated
 *  identifiers and a table full of them reads like a log file. */
export const STANDING_LABEL: Record<TeardownStanding, string> = {
  "nothing-to-reach": "nothing to reach",
  "waiting-to-dispatch": "waiting to dispatch",
  "dispatched-unconfirmed": "asked, not confirmed",
  confirmed: "confirmed gone",
  abandoned: "abandoned",
};

export const FINDING_LABEL: Record<FindingKind, string> = {
  "sandbox-without-credential": "live credential forwarded",
  "never-approved": "never approved",
  allow: "allowed to the real host",
};
