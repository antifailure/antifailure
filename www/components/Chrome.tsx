"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { AuthModal } from "./AuthModal";
import { ContentSheet, type SheetId } from "./ContentSheet";

export type { SheetId };

export type AuthMode = "login" | "signup";

type Overlay =
  | { kind: "auth"; mode: AuthMode }
  | { kind: "sheet"; id: SheetId }
  | null;

type ChromeValue = {
  openAuth: (mode: AuthMode) => void;
  openSheet: (id: SheetId) => void;
  close: () => void;
};

const ChromeContext = createContext<ChromeValue | null>(null);

export function useChrome() {
  const ctx = useContext(ChromeContext);
  if (!ctx) throw new Error("useChrome must be used inside ChromeProvider");
  return ctx;
}

export function ChromeProvider({ children }: { children: ReactNode }) {
  const [overlay, setOverlay] = useState<Overlay>(null);

  const close = useCallback(() => setOverlay(null), []);
  const openAuth = useCallback((mode: AuthMode) => setOverlay({ kind: "auth", mode }), []);
  const openSheet = useCallback((id: SheetId) => setOverlay({ kind: "sheet", id }), []);

  useEffect(() => {
    if (!overlay) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
    };
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    window.addEventListener("keydown", onKey);
    return () => {
      document.body.style.overflow = prev;
      window.removeEventListener("keydown", onKey);
    };
  }, [overlay, close]);

  const value = useMemo(() => ({ openAuth, openSheet, close }), [openAuth, openSheet, close]);

  return (
    <ChromeContext.Provider value={value}>
      {children}
      <AuthModal
        open={overlay?.kind === "auth"}
        mode={overlay?.kind === "auth" ? overlay.mode : "login"}
        onClose={close}
        onMode={(mode) => setOverlay({ kind: "auth", mode })}
      />
      <ContentSheet
        open={overlay?.kind === "sheet"}
        id={overlay?.kind === "sheet" ? overlay.id : "brief"}
        onClose={close}
      />
    </ChromeContext.Provider>
  );
}
