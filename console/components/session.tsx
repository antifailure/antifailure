"use client";

import { createContext, useContext, useEffect, type ReactNode } from "react";
import { useSession } from "@/lib/api";
import { shouldRefreshSession } from "@/lib/session-freshness";

type SessionState = ReturnType<typeof useSession>;

const Ctx = createContext<SessionState | null>(null);

/**
 * One fetch of /auth/session for the whole application.
 *
 * Every screen needs the role (to know which controls to render) and the CSRF
 * token (to send a mutation), and each of them calling useSession() meant two
 * or three identical requests per page and three independent loading states
 * that could disagree with each other for a few hundred milliseconds. It is
 * one request, in the layout, read from context.
 */
export function SessionProvider({ children }: { children: ReactNode }) {
  const state = useSession();
  const reload = state.reload;

  useEffect(() => {
    const onFocus = () => {
      if (shouldRefreshSession("focus", document.visibilityState)) reload();
    };
    const onVisibilityChange = () => {
      if (shouldRefreshSession("visibilitychange", document.visibilityState)) reload();
    };

    window.addEventListener("focus", onFocus);
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [reload]);

  return <Ctx.Provider value={state}>{children}</Ctx.Provider>;
}

/** The session, from the provider. Throws rather than silently refetching if a
 *  component is used outside it, because a second fetch is exactly the bug
 *  this replaces. */
export function useSessionContext(): SessionState {
  const value = useContext(Ctx);
  if (!value) {
    throw new Error("useSessionContext outside SessionProvider");
  }
  return value;
}
