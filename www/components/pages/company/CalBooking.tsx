"use client";

import { useEffect, useRef, useState } from "react";

/**
 * The booking embed on /contact.
 *
 * Three decisions here are load bearing, so they are written down rather than
 * left to be rediscovered.
 *
 * It embeds an EVENT TYPE, not the profile. https://cal.com/virsanghavi is a
 * profile page and it renders the account bio, which today reads "CEO @
 * Ravioli (ravioli.live)". That string belongs to a different company and has
 * no business on this site. The bio lives in a cal.com account that only its
 * owner can edit, and nothing in this repository can change it. The event type
 * page does not render it at all: it carries the organizer's name, the event
 * title, the duration, the location and the calendar. So the fix is the link
 * we point at rather than a style rule fighting somebody else's markup, which
 * would break the first time they ship a class name.
 *
 * It loads on approach, not on paint. The embed pulls roughly 90KB of
 * JavaScript from a third party and then an iframe behind it. Loading that
 * during first paint would cost this page its measurements for a widget most
 * visitors scroll past, so an IntersectionObserver holds it until the section
 * is close to the viewport. The module is imported by one page, so it is in
 * one route's bundle rather than the shared one.
 *
 * It always renders a plain link. A booking widget that fails is an empty
 * rectangle, and an empty rectangle is worse than no widget: there is nothing
 * to click and nothing that says why. Anybody who blocks third party frames,
 * or who is behind a network that does, gets a real address they can open
 * instead. The link is not conditional on the failure being detected, because
 * detection is the part most likely to be wrong.
 */

/** The event type. See the note above about why this is not the profile. */
const CAL_LINK = "virsanghavi/30min";

/** Where somebody goes when the frame does not load, or they would rather not. */
const CAL_PROFILE = "https://cal.com/virsanghavi";

const CAL_ORIGIN = "https://app.cal.com";
const EMBED_SCRIPT = `${CAL_ORIGIN}/embed/embed.js`;

/**
 * How long to wait for an iframe before showing the address instead.
 *
 * Generous on purpose. This is not a timeout on the booking, it is a timeout
 * on a script tag, and a slow connection that would have worked in nine
 * seconds should not be told the widget is broken.
 */
const GIVE_UP_MS = 12000;

/**
 * The shape embed.js expects to already exist.
 *
 * Its first statement is `const h = window.Cal`, and it never creates that
 * object itself: the published snippet installs a queue, every call made
 * before the script arrives lands in `Cal.q`, and the script drains the queue
 * on load. This is that queue, typed, rather than the minified snippet pasted
 * in. `ns` is unused here because a single embed on a single page needs no
 * namespace, but embed.js reads it, so it has to be there.
 */
type CalQueue = {
  (...args: unknown[]): void;
  q: unknown[];
  ns: Record<string, unknown>;
  loaded: boolean;
};

type CalWindow = Window & { Cal?: CalQueue };

/**
 * The site's own colours, handed to the booker.
 *
 * Every value is a token this site already paints with: the primary action is
 * black with white text, because that is what Button's `filled` theme is, and
 * the greens are deliberately absent. #33bf00 on white measures 2.6:1, so a
 * confirm button in the brand green would be the one control on the page a
 * person cannot read. `cal-border-booker-width` is zeroed because the widget
 * sits inside a card that already draws a hairline, and two frames a pixel
 * apart is the tell of an embed nobody looked at.
 */
const CAL_THEME = {
  "cal-brand": "#000000",
  "cal-brand-emphasis": "#292929",
  "cal-brand-text": "#ffffff",
  "cal-brand-accent": "#ffffff",
  "cal-bg": "#ffffff",
  "cal-bg-emphasis": "#ececeb",
  "cal-bg-subtle": "#f7f7f5",
  "cal-bg-muted": "#f7f7f5",
  "cal-text-emphasis": "#000000",
  "cal-text": "#61646b",
  "cal-text-subtle": "#797d86",
  "cal-text-muted": "#94979e",
  "cal-text-inverted": "#ffffff",
  "cal-border": "rgba(0, 0, 0, 0.12)",
  "cal-border-emphasis": "rgba(0, 0, 0, 0.4)",
  "cal-border-subtle": "rgba(0, 0, 0, 0.08)",
  "cal-border-muted": "rgba(0, 0, 0, 0.06)",
  "cal-border-booker": "transparent",
  "cal-border-booker-width": "0px",
  radius: "8px",
} as const;

type Phase = "waiting" | "loading" | "ready" | "failed";

/**
 * A skeleton the shape of the thing that replaces it.
 *
 * A spinner would be less work and would tell a reader nothing about what is
 * arriving. This is the booker's actual layout: the event summary on the left,
 * a month grid, and a column of times. Nothing here animates. A widget that
 * pulses while the page is idle is asking for attention it has not earned, and
 * this one is below the fold by definition.
 */
function BookerSkeleton() {
  return (
    <div
      className="grid grid-cols-[240px_minmax(0,1fr)_190px] gap-8 p-7 max-lg:grid-cols-1 max-lg:gap-7 max-md:p-6"
      aria-hidden
    >
      <div className="space-y-3">
        <div className="h-3.5 w-24 rounded-full bg-black/[0.07]" />
        <div className="h-5 w-40 rounded-full bg-black/[0.09]" />
        <div className="h-3 w-20 rounded-full bg-black/[0.06]" />
        <div className="h-3 w-28 rounded-full bg-black/[0.06]" />
      </div>
      <div className="space-y-3">
        <div className="h-4 w-32 rounded-full bg-black/[0.09]" />
        <div className="grid grid-cols-7 gap-2">
          {Array.from({ length: 35 }, (_, i) => (
            <div key={i} className="aspect-square rounded-[6px] bg-black/[0.05]" />
          ))}
        </div>
      </div>
      <div className="space-y-2.5 max-lg:hidden">
        {Array.from({ length: 8 }, (_, i) => (
          <div key={i} className="h-10 rounded-[8px] bg-black/[0.05]" />
        ))}
      </div>
    </div>
  );
}

/**
 * What a reader gets instead of an empty box.
 *
 * It says which part failed, because "something went wrong" sends somebody to
 * reload a page that will fail again for the same reason, and it gives the
 * address in full so it can be copied rather than only clicked.
 */
function BookerFallback() {
  return (
    <div className="flex flex-col items-start p-7 max-md:p-6">
      <p className="text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
        The booking calendar is served in a frame by cal.com, and this browser
        or network did not load it. The calendar itself is fine. Open it
        directly and the times are the same.
      </p>
      <a
        href={CAL_PROFILE}
        className="mt-6 inline-flex min-h-11 items-center rounded-full bg-black px-5 text-[14px] font-medium text-white transition-colors hover:bg-[#292929]"
      >
        Open the calendar on cal.com
      </a>
    </div>
  );
}

export function CalBooking() {
  const mount = useRef<HTMLDivElement | null>(null);
  const started = useRef(false);
  const [phase, setPhase] = useState<Phase>("waiting");

  useEffect(() => {
    const element = mount.current;
    if (!element) return;

    let cancelled = false;
    let giveUp: ReturnType<typeof setTimeout> | undefined;
    let watcher: MutationObserver | undefined;

    const start = () => {
      // React runs effects twice in development under strict mode, and
      // embed.js answers a second `inline` for a live element by logging
      // "Inline embed already exists" and doing nothing. The guard keeps that
      // out of the console rather than letting it look like a real warning.
      if (started.current) return;
      started.current = true;
      setPhase("loading");

      const target = window as CalWindow;
      if (!target.Cal) {
        const queue = ((...args: unknown[]) => {
          queue.q.push(args);
        }) as CalQueue;
        queue.q = [];
        queue.ns = {};
        // Claimed before the script is appended, so the queue never asks for a
        // second copy of it.
        queue.loaded = true;
        target.Cal = queue;

        const script = document.createElement("script");
        script.src = EMBED_SCRIPT;
        script.async = true;
        script.onerror = () => {
          if (!cancelled) setPhase("failed");
        };
        document.head.appendChild(script);
      }

      const cal = target.Cal;
      cal("init", { origin: CAL_ORIGIN });
      cal("inline", {
        elementOrSelector: element,
        calLink: CAL_LINK,
        config: { layout: "month_view", theme: "light" },
      });
      // `theme: "light"` is not a default and leaving it out is a real defect
      // rather than a preference. cal.com follows the visitor's system setting
      // unless told otherwise, and this site declares `colorScheme: "light"`
      // for every page. Without this line, anybody whose machine is in dark
      // mode gets a black calendar sitting in a white card.
      cal("ui", {
        theme: "light",
        cssVarsPerTheme: { light: CAL_THEME },
      });

      // The iframe is the only honest signal that this worked. `onload` on the
      // script fires when the code arrives, which is not the same as the frame
      // rendering, and a network that answers the script and blocks the frame
      // is exactly the case this has to survive.
      const settle = () => {
        if (cancelled || !element.querySelector("iframe")) return;
        setPhase("ready");
        watcher?.disconnect();
        if (giveUp !== undefined) clearTimeout(giveUp);
      };
      watcher = new MutationObserver(settle);
      watcher.observe(element, { childList: true, subtree: true });
      settle();

      giveUp = setTimeout(() => {
        if (!cancelled && !element.querySelector("iframe")) setPhase("failed");
      }, GIVE_UP_MS);
    };

    // No IntersectionObserver, no deferral: load it rather than leave a
    // permanent skeleton on a browser that cannot tell us when it is near.
    if (typeof IntersectionObserver === "undefined") {
      start();
      return () => {
        cancelled = true;
      };
    }

    const approach = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          approach.disconnect();
          start();
        }
      },
      // Begins the fetch about one viewport early, so the widget is usually
      // there by the time it is scrolled to.
      { rootMargin: "600px 0px" },
    );
    approach.observe(element);

    return () => {
      cancelled = true;
      approach.disconnect();
      watcher?.disconnect();
      if (giveUp !== undefined) clearTimeout(giveUp);
    };
  }, []);

  return (
    <div>
      <div className="overflow-hidden rounded-[8px] bg-white ring-1 ring-black/10">
        {/* The mount is always in the tree and never keyed off state: cal.com
            writes its iframe into this exact element, and swapping the node
            under it after the call would leave the embed pointing at a
            detached div. The skeleton and the fallback sit beside it and the
            mount carries no height until the frame arrives.

            No min-height on the ready state, and the first attempt had one.
            cal.com sizes its own iframe by posting the booker's height back to
            the embed script, and a floor under that is a floor the card cannot
            go below: 620px against a 568px frame left 52 pixels of white
            inside the card, under the calendar, on every desktop viewport. The
            skeleton is what reserves space while the frame is on its way, so
            the reserved height belongs there and not here. */}
        <div ref={mount} className={phase === "ready" ? undefined : "h-0 overflow-hidden"} />
        {phase === "failed" ? <BookerFallback /> : null}
        {phase !== "ready" && phase !== "failed" ? <BookerSkeleton /> : null}
      </div>
      <p className="mt-5 text-[14px] leading-6 tracking-extra-tight text-gray-new-40">
        Rather open it in its own tab?{" "}
        <a
          href={CAL_PROFILE}
          className="text-black underline decoration-black/20 underline-offset-4"
        >
          cal.com/virsanghavi
        </a>{" "}
        lists every length of call.
      </p>
    </div>
  );
}
