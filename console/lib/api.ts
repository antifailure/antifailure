"use client";

import { useCallback, useEffect, useRef, useState } from "react";

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
  const body = (await res.json()) as { result?: { data?: T } };
  return body.result?.data as T;
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
  const body = (await res.json()) as { result?: { data?: T } };
  return body.result?.data as T;
}

/** A plain JSON endpoint on the control plane, for the few things that are not
 *  tRPC: the session, sign-out, device approval, and provider keys. */
export async function rest<T>(
  path: string,
  init: { method?: string; body?: unknown; csrf?: string } = {},
): Promise<T> {
  const method = init.method ?? "GET";
  const headers: Record<string, string> = { accept: "application/json" };
  if (init.body !== undefined) headers["content-type"] = "application/json";
  if (init.csrf) headers[CSRF_HEADER] = init.csrf;
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
 */
export function useApi<T>(fn: () => Promise<T>, deps: unknown[] = []) {
  const [state, setState] = useState<State<T>>({ status: "loading", data: null, error: null });
  const [nonce, setNonce] = useState(0);
  const alive = useRef(true);
  const run = useRef(fn);
  run.current = fn;

  useEffect(() => {
    alive.current = true;
    setState({ status: "loading", data: null, error: null });
    run
      .current()
      .then((data) => {
        if (alive.current) setState({ status: "ready", data, error: null });
      })
      .catch((error: unknown) => {
        if (!alive.current) return;
        setState({ status: "error", data: null, error: asApiError(error) });
      });
    return () => {
      alive.current = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, nonce]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  return { ...state, reload };
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
  const alive = useRef(true);
  const run = useRef(fetchPage);
  run.current = fetchPage;

  useEffect(() => {
    alive.current = true;
    setStatus("loading");
    setData([]);
    setError(null);
    setNext(null);
    setMoreError(null);
    run
      .current(null)
      .then((page) => {
        if (!alive.current) return;
        setData(page.rows);
        setNext(page.next);
        setStatus("ready");
      })
      .catch((e: unknown) => {
        if (!alive.current) return;
        setError(asApiError(e));
        setStatus("error");
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
  return { status, data, error, hasMore: next !== null, more, busy, moreError, reload };
}
