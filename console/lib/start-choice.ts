"use client";

import { useCallback, useEffect, useState } from "react";
import { readChoice, storageKey, type Choice } from "@/lib/start";

/**
 * The remembered answer to the start question, for one organization.
 *
 * `ready` is false until the browser has been asked. The console is a static
 * export, so the first render happens with no window at all and a page that
 * decided anything from `choice` before `ready` would decide it from the
 * prerender's nothing and then change its mind: the root would send everybody
 * to the question for one frame, including people who answered it weeks ago.
 *
 * Read on mount and again on every `storage` event, so a second tab sees the
 * answer the first one gave rather than asking the same question twice.
 * Storage can throw rather than answer, in a private window or with site data
 * blocked, and every access is wrapped for that: the answer is then simply not
 * remembered, which is the question being asked again and nothing worse.
 */
export function useStartChoice(orgId: string | null | undefined) {
  const key = orgId ? storageKey(orgId) : null;
  const [choice, setChoice] = useState<Choice | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    if (!key) return;
    const read = () => {
      try {
        setChoice(readChoice(window.localStorage.getItem(key)));
      } catch {
        setChoice(null);
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

  const remember = useCallback(
    (next: Choice) => {
      setChoice(next);
      if (!key) return;
      try {
        window.localStorage.setItem(key, next);
      } catch {
        // Not remembered, which is the question again next time. The page
        // still shows the answer that was just given.
      }
    },
    [key],
  );

  return { choice, ready, remember };
}
