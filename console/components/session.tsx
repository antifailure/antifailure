"use client";

import { createContext, useContext, type ReactNode } from "react";
import { useSession, type ApiError, type Session } from "@/lib/api";

interface SessionState {
  status: "loading" | "ready" | "error";
  data: Session | null;
  error: ApiError | null;
  reload: () => void;
}

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
  return <Ctx.Provider value={state as SessionState}>{children}</Ctx.Provider>;
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
