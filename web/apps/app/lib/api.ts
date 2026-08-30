// Talking to the control plane API, from the server side of this application.
//
// Every read happens here, in a server component, with the browser's session
// cookie forwarded. The browser never calls the API itself for data: it has no
// token to present that the server does not already have, and doing the reads
// here means a page arrives rendered rather than as a shell that then fetches.
//
// The types below are written out rather than imported from the API's router.
// That looks like duplication and is not. Several of those procedures return
// the rows of a hand-written SELECT, which tRPC types as an untyped record, so
// importing the router would infer `unknown` for exactly the fields a page
// renders. What actually keeps the two in step is test/contract.test.ts, which
// runs both against a real database and asserts that every field named here
// comes back with the type named here. A shape can only drift past a check
// that reads the real response.

import { cookies, headers } from "next/headers";

const API = (process.env.AF_API_URL ?? "http://127.0.0.1:8080").replace(/\/+$/, "");

/** How long a page waits for the API before it says so. */
const TIMEOUT_MS = Number(process.env.AF_API_TIMEOUT_MS ?? 10_000);

export class NotSignedIn extends Error {
  constructor() {
    super("This request carried no session the control plane recognised.");
  }
}

export class ApiError extends Error {
  readonly code: string;
  constructor(code: string, message: string) {
    super(message);
    this.code = code;
  }
}

/** The session, as the API describes it. */
export interface Session {
  signedIn: boolean;
  label?: string;
  orgId?: string | null;
  orgSlug?: string | null;
  role?: "owner" | "admin" | "member" | "viewer" | null;
  csrfToken?: string;
}

async function cookieHeader(): Promise<string> {
  const jar = await cookies();
  return jar
    .getAll()
    .map((c) => `${c.name}=${c.value}`)
    .join("; ");
}

/** The address the browser used, so a redirect can be built against it. */
export async function origin(): Promise<string> {
  const h = await headers();
  const host = h.get("x-forwarded-host") ?? h.get("host") ?? "localhost";
  const proto = h.get("x-forwarded-proto") ?? "http";
  return `${proto}://${host}`;
}

async function call(
  method: "GET" | "POST",
  path: string,
  init: RequestInit = {},
): Promise<Response> {
  const cookie = await cookieHeader();
  return fetch(`${API}${path}`, {
    ...init,
    method,
    headers: {
      ...(init.headers ?? {}),
      ...(cookie ? { cookie } : {}),
    },
    // Nothing here is cacheable. Every response is one organization's rows,
    // resolved from a cookie, and a cached one is another tenant's page.
    cache: "no-store",
    signal: AbortSignal.timeout(TIMEOUT_MS),
  });
}

export async function session(): Promise<Session> {
  try {
    const res = await call("GET", "/auth/session");
    if (!res.ok) return { signedIn: false };
    return (await res.json()) as Session;
  } catch {
    // A control plane that cannot be reached is not a session that has
    // expired. The caller distinguishes them; sending somebody to the sign-in
    // page because the API is restarting is the wrong answer and it looks
    // exactly like being signed out.
    throw new ApiError("UNREACHABLE", "The control plane API did not answer.");
  }
}

/** Reads one procedure. */
export async function query<T>(path: string, input: unknown = {}): Promise<T> {
  const search = `?input=${encodeURIComponent(JSON.stringify(input))}`;
  let res: Response;
  try {
    res = await call("GET", `/trpc/${path}${search}`);
  } catch (err) {
    throw new ApiError(
      "UNREACHABLE",
      `The control plane API did not answer in ${Math.round(TIMEOUT_MS / 1000)}s.`,
    );
  }
  return unwrap<T>(res);
}

/** Calls one mutation, with the token the API demands on a write. */
export async function mutate<T>(path: string, input: unknown = {}): Promise<T> {
  // Read first, because the token a write has to present is derived from the
  // session and handed out by /auth/session. Deriving it here would mean this
  // application carried a copy of how it is constructed, and a security detail
  // implemented twice is a security detail that will eventually be two
  // different things.
  const current = await session();
  if (!current.signedIn || !current.csrfToken) throw new NotSignedIn();

  let res: Response;
  try {
    res = await call("POST", `/trpc/${path}`, {
      headers: {
        "content-type": "application/json",
        "x-antifailure-csrf": current.csrfToken,
      },
      body: JSON.stringify(input),
    });
  } catch {
    throw new ApiError("UNREACHABLE", "The control plane API did not answer.");
  }
  return unwrap<T>(res);
}

async function unwrap<T>(res: Response): Promise<T> {
  const text = await res.text();
  let body: unknown;
  try {
    body = JSON.parse(text);
  } catch {
    throw new ApiError("MALFORMED", `The API answered ${res.status} with something that is not JSON.`);
  }

  const error = (body as { error?: { message?: string; data?: { code?: string } } }).error;
  if (error) {
    const code = error.data?.code ?? "ERROR";
    if (code === "UNAUTHORIZED") throw new NotSignedIn();
    throw new ApiError(code, error.message ?? "The control plane refused that.");
  }
  if (res.status === 401) throw new NotSignedIn();
  if (!res.ok) {
    throw new ApiError(String(res.status), `The API answered ${res.status}.`);
  }

  // tRPC wraps a successful result. Reaching in here rather than running the
  // tRPC client keeps this application's dependency list to the framework and
  // nothing else, which is what makes it build with no network.
  const result = (body as { result?: { data?: unknown } }).result;
  if (result && "data" in result) return result.data as T;
  return body as T;
}

// ---------------------------------------------------------------------------
// The shapes, as the SELECTs in the router return them.
// ---------------------------------------------------------------------------

export type EnvironmentState =
  | "queued"
  | "creating"
  | "running"
  | "sleeping"
  | "failed"
  | "torn_down";

export interface Environment {
  id: string;
  env_id: string;
  branch: string;
  pull_request: number | null;
  state: EnvironmentState;
  preview_url: string | null;
  runtime: string | null;
  golden_version: string | null;
  created_at: string;
  updated_at: string;
  expires_at: string | null;
  repository: string;
}

export interface EnvironmentPage {
  environments: Environment[];
  nextCursor: string | null;
}

export type RunState = "queued" | "running" | "complete" | "failed" | "cancelled";

export interface Run {
  id: string;
  kind: string;
  state: RunState;
  started_at: string | null;
  finished_at: string | null;
  created_at: string;
}

export type VerdictValue = "pass" | "fail" | "flaky" | "blocked" | "unverified";

export interface Verdict {
  workflow: string;
  persona: string | null;
  value: VerdictValue;
  summary: string | null;
  steps: number;
  duration_ms: number | null;
  reproduction: unknown;
}

export interface Artifact {
  id: string;
  kind: string;
  step: number | null;
  content_type: string | null;
  size_bytes: number | null;
  sha256: string | null;
  retained: boolean;
}

export type Mode = "block" | "allow" | "capture" | "mock" | "sandbox" | "synth";

/** One rule, as `@antifailure/policy` compiles and orders it. */
export interface EgressRule {
  host: string;
  mode: Mode;
  paths?: string[];
  methods?: string[];
  rate_limit?: string;
  credential?: string;
  fixtures?: string;
  webhook_path?: string;
  note?: string;
}

export interface EffectivePolicy {
  default: Mode;
  /** In the order that decides, which is the order somebody has to read them
   *  in to predict anything. */
  rules: EgressRule[];
  hosts: string[];
}

/** What the engine decided about one request, and why. */
export interface Decision {
  mode: Mode;
  /** The host pattern of the rule that decided, empty when the default did. */
  ruleHost: string;
  rateLimit: string;
  credential: string;
  fixtures: string;
  webhookPath: string;
  matched: boolean;
  /** Whether the request reaches the real destination. Only allow and sandbox
   *  do: capture and mock answer inside the environment, synth invents. */
  allowed: boolean;
  reason: string;
}

/** One rule that was considered, in the order it was considered. */
export interface Match {
  rule: EgressRule;
  index: number;
  specificity: number;
  why: string;
}

export interface Explanation {
  decision: Decision;
  chain: Match[];
  inspectsHost: boolean;
}

export interface DecisionCount {
  host: string | null;
  mode: string | null;
  requests: string;
}

export interface AuditEntry {
  seq: number;
  actor_label: string;
  action: string;
  target_type: string;
  target_id: string | null;
  origin: string;
  detail: Record<string, unknown> | null;
  occurred_at: string;
}

export interface ChainProblem {
  seq: number;
  kind: "altered" | "broken_link" | "missing";
  detail: string;
}

export interface ChainReport {
  ok: boolean;
  entries: number;
  /** The head hash, which a signed export carries so a later check can detect
   *  a rewrite of everything before it. */
  head: string | null;
  /** Every break, not the first: an investigation wants the extent. */
  problems: ChainProblem[];
}

export interface OrgStatus {
  slug: string;
  plan: string;
  suspended: boolean;
  suspendedReason: string | null;
  quotas: {
    environments: QuotaVerdict;
    goldens: QuotaVerdict;
  };
}

export interface QuotaVerdict {
  allowed: boolean;
  current: number;
  limit: number;
  reason: string;
}
