"use client";

import { useCallback, useEffect, useState } from "react";
import { EMPTY_SETUP, readSetup, setupKey, writeSetup, type Setup } from "@/lib/onboarding";

/**
 * What the first sign-in walk remembers beyond the one word: the uses that
 * were ticked, the tier, and which setup cards were marked done. The same
 * discipline as useStartChoice, for the same reasons written there. `ready`
 * is false until the browser has been asked, because the console is a static
 * export and a step that decided anything from the prerender's nothing would
 * change its mind a frame later. Every storage access is wrapped, because a
 * private window can throw rather than answer, and the worst outcome of that
 * is the walk starting from the beginning.
 *
 * Read on mount and on every `storage` event, so a second tab shows what the
 * first one ticked rather than asking again from nothing.
 */
export function useSetup(orgId: string | null | undefined) {
  const key = orgId ? setupKey(orgId) : null;
  const [setup, setSetupState] = useState<Setup>(EMPTY_SETUP);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    if (!key) return;
    const read = () => {
      try {
        setSetupState(readSetup(window.localStorage.getItem(key)));
      } catch {
        setSetupState(EMPTY_SETUP);
      }
      setReady(true);
    };
    read();
    const onStorage = (event: StorageEvent) => {
      if (event.key === null || event.key === key) read();
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, [key]);

  const setSetup = useCallback(
    (update: (current: Setup) => Setup) => {
      setSetupState((current) => {
        const next = update(current);
        if (key) {
          try {
            window.localStorage.setItem(key, writeSetup(next));
          } catch {
            // Not remembered, which is the walk from the top next time. The
            // step still shows what was just chosen.
          }
        }
        return next;
      });
    },
    [key],
  );

  return { setup, ready, setSetup };
}
