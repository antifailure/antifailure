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
import { ContentSheet, type SheetId } from "./ContentSheet";

export type { SheetId };

// There was a second overlay here, an in-page copy of the waitlist form, and
// an openAuth that put it on screen. Nothing ever called openAuth: a grep for
// it found the definition, the context value and nothing else, while the same
// grep found openSheet with a real caller at AuthScreen.tsx. So the modal was
// unreachable, and it still shipped in the JavaScript bundle carrying the
// sentence "There is no hosted control plane to sign in to yet" long after
// there was one.
//
// Deleted rather than wired up, which is the unusual direction for dead code
// and is right here. The thing it duplicated is finished and reachable: the
// full screen at /signin and /signup is linked from the header, the hero, the
// pricing page and the footer. The modal was the older, smaller half of it,
// offering the waitlist and no GitHub button at all, so giving it a trigger
// would have meant intercepting a working page with a version of itself that
// cannot let an invited person in. A second implementation of one thing is
// also how one of them goes stale, which is exactly what happened to its copy.
type Overlay = { kind: "sheet"; id: SheetId } | null;

type ChromeValue = {
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

  const value = useMemo(() => ({ openSheet, close }), [openSheet, close]);

  return (
    <ChromeContext.Provider value={value}>
      {children}
      <ContentSheet
        open={overlay?.kind === "sheet"}
        id={overlay?.kind === "sheet" ? overlay.id : "brief"}
        onClose={close}
      />
    </ChromeContext.Provider>
  );
}
