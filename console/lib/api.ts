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
        setState({
          status: "error",
          data: null,
          error:
            error instanceof ApiError
              ? error
              : new ApiError(
                  "The control plane could not be reached.",
                  0,
                  "NETWORK",
                ),
        });
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
