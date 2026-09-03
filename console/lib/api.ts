"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import { unwrapTrpc } from "@/lib/trpc-envelope";

/**
 * Where the API is.
 *
 * Empty in every build that ships, because the console is served by the
 * control plane itself and therefore shares its origin. That is what makes the
 * session cookie work at all: it is SameSite=Lax, so it rides a same-origin
 * fetch and would not ride a cross-site one.
 *
 * The variable exists for `next dev`, where the console runs on :3100 and the
 * control plane on :8080. Unset, which is every production build, it is the
 * empty string and every request is relative.
 */
const BASE = process.env.NEXT_PUBLIC_AF_API ?? "";

/** Sent on every mutation. Read from GET /auth/session, which derives it from
 *  the session secret without revealing it. */
const CSRF_HEADER = "x-antifailure-csrf";

export interface Session {
  signedIn: boolean;
  label?: string;
  orgId?: string | null;
  role?: string | null;
  csrfToken?: string;
  /** The ways in this deployment offers, when signed out. Absent from an
   *  older control plane, which is read as GitHub alone rather than as none. */
  methods?: string[];
  signupsOpen?: boolean;
  githubAppInstallUrl?: string;
  plan?: string | null;
  hostedRequiredPlan?: string | null;
  hostedAccess?: boolean;
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  constructor(message: string, status: number, code: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

async function readError(res: Response): Promise<ApiError> {
  let message = `The control plane answered ${res.status}.`;
  let code = "UNKNOWN";
  try {
    const body = (await res.json()) as {
      error?: { message?: string; data?: { code?: string } } | string;
    };
    if (typeof body.error === "string") message = body.error;
    else if (body.error?.message) message = body.error.message;
    if (typeof body.error === "object" && body.error?.data?.code) code = body.error.data.code;
  } catch {
    // A body that is not JSON tells us nothing the status has not already
    // said. Keep the status message rather than inventing a better one.
  }
  return new ApiError(message, res.status, code);
}

/** A tRPC query. GET, so it is cacheable and cannot be a CSRF target. */
export async function query<T>(path: string, input?: unknown): Promise<T> {
  const qs = input === undefined ? "" : `?input=${encodeURIComponent(JSON.stringify(input))}`;
  const res = await fetch(`${BASE}/trpc/${path}${qs}`, {
    credentials: "same-origin",
    headers: { accept: "application/json" },
  });
  if (!res.ok) throw await readError(res);
  return unwrapTrpc<T>(await res.json());
}

/** A tRPC mutation. Needs the CSRF token from the session. */
export async function mutate<T>(path: string, input: unknown, csrf: string): Promise<T> {
  const res = await fetch(`${BASE}/trpc/${path}`, {
    method: "POST",
    credentials: "same-origin",
    headers: { "content-type": "application/json", [CSRF_HEADER]: csrf },
    body: JSON.stringify(input),
  });
  if (!res.ok) throw await readError(res);
  return unwrapTrpc<T>(await res.json());
}

/** A plain JSON endpoint on the control plane, for the few things that are not
 *  tRPC: the session, sign-out, device approval, and provider keys. */
export async function rest<T>(
  path: string,
  init: {
    method?: string;
    body?: unknown;
    /** The TENANT token, sent as x-antifailure-csrf. */
    csrf?: string;
    /**
     * Anything else the caller has to send.
     *
     * The operator portal's own token goes here, because it is a DIFFERENT
     * header: the guard for `af_admin_session` reads
     * `x-antifailure-admin-csrf` and never looks at `csrf` above. Passing the
     * operator token as `csrf` would send it under the tenant name and be
     * refused exactly as if nothing had been sent, which is a failure with no
     * symptom in the request. See adminMutate in lib/admin.ts.
     */
    headers?: Record<string, string>;
  } = {},
): Promise<T> {
  const method = init.method ?? "GET";
  const headers: Record<string, string> = { accept: "application/json" };
  if (init.body !== undefined) headers["content-type"] = "application/json";
  if (init.csrf) headers[CSRF_HEADER] = init.csrf;
  Object.assign(headers, init.headers ?? {});
  const res = await fetch(`${BASE}${path}`, {
    method,
    credentials: "same-origin",
    headers,
    body: init.body === undefined ? undefined : JSON.stringify(init.body),
  });
  if (!res.ok) throw await readError(res);
  return (await res.json()) as T;
}

/** Whatever a rejected fetch threw, as the error the screens render. A fetch
 *  that never reached the control plane rejects with a TypeError, which
 *  carries no status and no code of ours. */
function asApiError(e: unknown): ApiError {
  return e instanceof ApiError
    ? e
    : new ApiError("The control plane could not be reached.", 0, "NETWORK");
}

type State<T> =
  | { status: "loading"; data: null; error: null }
  | { status: "ready"; data: T; error: null }
  | { status: "error"; data: null; error: ApiError };

/**
 * One fetch, with the three states every screen in here has to render.
 *
 * The point of returning a discriminated union rather than `{data, loading}`
 * is that a page cannot forget the error branch: `state.data` does not exist
 * unless the status is "ready", so the compiler asks for the other two.
 *
 * A RELOAD is not a first load, and the difference is the whole reason this
 * hook is longer than the effect it wraps. `reload()` used to reset to
 * `{status: "loading", data: null}`, which is right when the deps change,
 * because a different environment's rows have nothing to do with this one's,
 * and wrong when the same question is being asked again: the reader loses what
 * they were reading to a skeleton, and if the second answer FAILS they lose it
 * for good and are shown a full page error over data that was fine a moment
 * ago.
 *
 * Where that lands is the Plan page's Refresh from Stripe, which somebody
 * presses immediately after paying, on a network they have just been reminded
 * is doing something. It never reproduces on a fast local control plane. With
 * 400ms of injected latency it is every press.
 *
 * So a reload keeps what is on screen, says `refreshing` while it is in
 * flight, and puts a failure in `refreshError` rather than in `error`, where
 * it would blank the page. A dependency change still resets, because then the
 * held data genuinely belongs to a different question.
 */
export function useApi<T>(fn: () => Promise<T>, deps: unknown[] = []) {
  const [state, setState] = useState<State<T>>({ status: "loading", data: null, error: null });
  const [nonce, setNonce] = useState(0);
  const [refreshing, setRefreshing] = useState(false);
  const [refreshError, setRefreshError] = useState<ApiError | null>(null);
  const alive = useRef(true);
  const run = useRef(fn);
  run.current = fn;

  // What the last effect run saw, so this one can tell a reload from a
  // dependency change. `nonce` alone cannot: a page that changes its filter
  // and reloads in the same commit would keep the previous filter's rows.
  const seen = useRef<{ nonce: number; deps: unknown[] } | null>(null);
  // Whether the held state is worth keeping, read inside the effect without
  // making the effect depend on it.
  const held = useRef(state);
  held.current = state;
  // Which request is the current one. Two reloads in flight can land out of
  // order, and the older answer must not overwrite the newer.
  const seq = useRef(0);

  useEffect(() => {
    alive.current = true;
    const before = seen.current;
    const sameDeps =
      before !== null &&
      before.deps.length === deps.length &&
      before.deps.every((v, i) => Object.is(v, deps[i]));
    seen.current = { nonce, deps };
    const reloading = sameDeps && before.nonce !== nonce && held.current.status === "ready";

    const mine = ++seq.current;
    if (reloading) {
      setRefreshing(true);
      setRefreshError(null);
    } else {
      setState({ status: "loading", data: null, error: null });
      setRefreshError(null);
    }

    run
      .current()
      .then((data) => {
        if (!alive.current || mine !== seq.current) return;
        setState({ status: "ready", data, error: null });
        setRefreshing(false);
      })
      .catch((error: unknown) => {
        if (!alive.current || mine !== seq.current) return;
        setRefreshing(false);
        // The held rows are still the last thing the control plane actually
        // said. Throwing them away because the next question went unanswered
        // tells the reader less than keeping them and saying so.
        if (reloading) setRefreshError(asApiError(error));
        else setState({ status: "error", data: null, error: asApiError(error) });
      });
    return () => {
      alive.current = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, nonce]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  return { ...state, refreshing, refreshError, reload };
}

/** The signed-in session, or the absence of one. */
export function useSession() {
  return useApi<Session>(() => rest<Session>("/auth/session"), []);
}

/**
 * A list that arrives one page at a time, with what is on screen kept.
 *
 * `useApi` cannot do this. It resets to `{status: "loading", data: null}` on
 * every dependency change, which is right for one fetch and wrong for a second
 * page: asking for more would blank the rows the reader is looking at and then
 * replace them with the next page rather than adding to it.
 *
 * The three list routes page three different ways and none of them can be
 * guessed at from the outside, so the caller supplies the fetch: `runs.recent`
 * takes `before` and returns `nextCursor`, `environments.list` takes `cursor`
 * and returns `nextCursor`, and `audit.list` takes `before` as a number and
 * returns a bare array, where a full page is the only signal that there is
 * another. Each page adapts its own route and this holds the rows.
 *
 * The failure of a later page is kept apart from the failure of the first,
 * because they are different events for the reader: the first leaves nothing on
 * screen and the second leaves a correct partial list that is still worth
 * reading. So `error` blanks the list and `moreError` sits under it.
 */
export function usePages<Row>(
  fetchPage: (cursor: string | null) => Promise<{ rows: Row[]; next: string | null }>,
  deps: unknown[] = [],
) {
  const [data, setData] = useState<Row[]>([]);
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState<ApiError | null>(null);
  const [next, setNext] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [moreError, setMoreError] = useState<ApiError | null>(null);
  const [nonce, setNonce] = useState(0);
  const [refreshing, setRefreshing] = useState(false);
  const [refreshError, setRefreshError] = useState<ApiError | null>(null);
  const alive = useRef(true);
  const run = useRef(fetchPage);
  run.current = fetchPage;

  // The same distinction `useApi` makes, for the same reason. Every caller of
  // this hook reloads after a mutation: /runs after Start, /environments after
  // Create and after a teardown. Blanking a fifty row table because somebody
  // started one run is a worse answer than showing the fifty rows and the new
  // one a moment later, and blanking it PERMANENTLY because the reload failed
  // is losing data the reader had.
  const seen = useRef<{ nonce: number; deps: unknown[] } | null>(null);
  const ready = useRef(false);
  const seq = useRef(0);

  useEffect(() => {
    alive.current = true;
    const before = seen.current;
    const sameDeps =
      before !== null &&
      before.deps.length === deps.length &&
      before.deps.every((v, i) => Object.is(v, deps[i]));
    seen.current = { nonce, deps };
    const reloading = sameDeps && before.nonce !== nonce && ready.current;

    const mine = ++seq.current;
    setRefreshError(null);
    setMoreError(null);
    if (reloading) {
      setRefreshing(true);
    } else {
      setStatus("loading");
      setData([]);
      setError(null);
      setNext(null);
      ready.current = false;
    }
    run
      .current(null)
      .then((page) => {
        if (!alive.current || mine !== seq.current) return;
        // A reload starts again from the first page on purpose. Holding the
        // pages somebody had already asked for would mean stitching a fresh
        // first page onto stale later ones, and the cursor that joined them
        // no longer describes the list.
        setData(page.rows);
        setNext(page.next);
        setStatus("ready");
        setRefreshing(false);
        ready.current = true;
      })
      .catch((e: unknown) => {
        if (!alive.current || mine !== seq.current) return;
        setRefreshing(false);
        if (reloading) setRefreshError(asApiError(e));
        else {
          setError(asApiError(e));
          setStatus("error");
        }
      });
    return () => {
      alive.current = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, nonce]);

  const more = useCallback(() => {
    if (busy || next === null) return;
    setBusy(true);
    setMoreError(null);
    run
      .current(next)
      .then((page) => {
        if (!alive.current) return;
        setData((held) => [...held, ...page.rows]);
        setNext(page.next);
      })
      .catch((e: unknown) => {
        if (!alive.current) return;
        setMoreError(asApiError(e));
      })
      .finally(() => {
        if (alive.current) setBusy(false);
      });
  }, [busy, next]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  // `data` rather than `rows` so this drops into `Loaded` where a `useApi`
  // was, which is what keeps the loading and error branches in one place
  // instead of being written out again on every page that pages.
  return {
    status,
    data,
    error,
    hasMore: next !== null,
    more,
    busy,
    moreError,
    refreshing,
    refreshError,
    reload,
  };
}
