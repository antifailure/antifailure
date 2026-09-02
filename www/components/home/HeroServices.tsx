"use client";

import { useEffect, useState } from "react";
import { MiniFilm } from "./visuals/hero";

// The gap between one film starting and the next. Each film is 8 film-seconds
// at 1.12x, so a little over 7 real ones, and they overlap by about a second:
// the cascade reads as five things happening rather than five things queueing.
const STAGGER_MS = 5900;

export const HERO_SERVICES = [
  {
    title: "Isolated Twin",
    description: "A temporary copy of the application stack for every risky change.",
    kind: "twin" as const,
  },
  {
    title: "Safe State",
    description: "Sanitized, referentially consistent, production-shaped Postgres.",
    kind: "state" as const,
  },
  {
    title: "Side-Effect Firewall",
    description: "A stateful Stripe and captured mail instead of charging cards.",
    kind: "firewall" as const,
  },
  {
    title: "Load",
    description: "Traffic shaped like production's own access log, sent at the twin.",
    kind: "workload" as const,
  },
  {
    title: "Migration Safety",
    description: "Locks, table rewrites, and query plans before it ships.",
    kind: "migration" as const,
  },
];

export function HeroServices() {
  const items = HERO_SERVICES;

  // How many of the five have been started. Each film runs to its own end and
  // holds there, so this only ever counts up, and when it reaches the last card
  // there is nothing further to schedule.
  //
  // It used to be a setInterval over `(current + 1) % items.length`, which is a
  // carousel with no end: five films cut off short of their endings and
  // restarted from a blank frame, in rotation, for as long as the tab was open.
  // Started once and left to rest, the five cards settle into five composed
  // stills, which is what they were drawn to be.
  const [startedThrough, setStartedThrough] = useState(0);

  useEffect(() => {
    if (startedThrough >= items.length - 1) return;
    const id = window.setTimeout(() => setStartedThrough((n) => n + 1), STAGGER_MS);
    return () => window.clearTimeout(id);
  }, [startedThrough, items.length]);

  return (
    // Five peers, and the reader has to be able to see that there are five and
    // read all five: this row is the value proposition enumerated, not a feed
    // to browse. Below `xl` it used to be a horizontal scroller with its
    // scrollbar hidden, so from 640px to 1279px the fifth card sat off the
    // right edge with no cue that it existed and the fourth was cut mid-word.
    // It reflows now, and the scroller survives only below `sm`, where a card
    // is 78vw so the next one always peeks and a sideways swipe is the native
    // gesture anyway.
    //
    // `grid-rows-subgrid` on each card is what keeps the artwork tops aligned
    // across a row when the paragraphs above them wrap to different heights,
    // so the wrapped rows need the row pair, not a plain two-row card.
    <ul className="grid grid-cols-5 grid-rows-[auto_auto] gap-x-16 gap-y-8 max-xl:grid-cols-3 max-xl:gap-x-10 max-xl:gap-y-12 max-md:grid-cols-2 max-md:gap-x-8 max-md:gap-y-10 max-sm:-mx-5 max-sm:flex max-sm:snap-x max-sm:snap-mandatory max-sm:scroll-px-5 max-sm:gap-x-6 max-sm:gap-y-0 max-sm:overflow-x-auto max-sm:px-5 max-sm:no-scrollbars">
      {items.map((item, index) => {
        const started = index <= startedThrough;
        return (
          <li
            key={item.title}
            className="group row-span-2 grid w-full max-w-64 cursor-default grid-rows-subgrid content-start text-black max-xl:max-w-none max-sm:w-[min(16rem,78vw)] max-sm:shrink-0 max-sm:snap-start max-sm:grid-rows-[auto_auto] max-sm:gap-y-6 max-sm:self-start"
          >
            <p className="block max-w-sm text-base tracking-extra-tight text-pretty text-gray-new-50">
              <span className="font-semibold text-black">{item.title}.</span> {item.description}
            </p>
            <span className="relative block aspect-[5/4] w-full overflow-hidden rounded-[12px] border border-black/[0.08] bg-white font-sans shadow-[0_1px_0_rgba(0,0,0,0.03)] transition-[border-color] duration-300 ease-[cubic-bezier(0.16,1,0.3,1)] [@media(hover:hover)]:group-hover:border-black/[0.16]">
              <MiniFilm kind={item.kind} active={started} />
              <span
                className="pointer-events-none absolute inset-0"
                style={{
                  background:
                    "linear-gradient(to right, transparent 62%, #f7f7f5 100%), linear-gradient(to bottom, transparent 70%, #f7f7f5 100%)",
                }}
                aria-hidden
              />
            </span>
          </li>
        );
      })}
    </ul>
  );
}
