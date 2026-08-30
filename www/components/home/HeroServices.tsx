"use client";

import { useEffect, useRef, useState } from "react";
import { MiniFilm } from "./visuals/hero";

const PLAY_MS = 6400;
const OVERLAP_MS = 500;
const STAGGER_MS = PLAY_MS - OVERLAP_MS;

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
    description: "Simulators instead of charging cards or emailing users.",
    kind: "firewall" as const,
  },
  {
    title: "Workload Studio",
    description: "Observed patterns, deterministic journeys, and exploratory users.",
    kind: "workload" as const,
  },
  {
    title: "Migration Safety",
    description: "Locks, plans, and rollback feasibility before it ships.",
    kind: "migration" as const,
  },
];

export function HeroServices() {
  const [autoPlayIndex, setAutoPlayIndex] = useState(0);
  const [trailingIndex, setTrailingIndex] = useState<number | null>(null);
  const playIndexRef = useRef(0);
  const items = HERO_SERVICES;

  useEffect(() => {
    const id = window.setInterval(() => {
      const current = playIndexRef.current;
      const next = (current + 1) % items.length;
      setTrailingIndex(current);
      playIndexRef.current = next;
      setAutoPlayIndex(next);
    }, STAGGER_MS);
    return () => window.clearInterval(id);
  }, [items.length]);

  useEffect(() => {
    if (trailingIndex == null) return;
    const id = window.setTimeout(() => setTrailingIndex(null), OVERLAP_MS);
    return () => window.clearTimeout(id);
  }, [trailingIndex]);

  return (
    <ul className="grid grid-cols-5 grid-rows-[auto_auto] gap-x-16 gap-y-8 max-xl:-mx-5 max-xl:flex max-xl:snap-x max-xl:snap-mandatory max-xl:scroll-px-5 max-xl:gap-x-8 max-xl:gap-y-0 max-xl:overflow-x-auto max-xl:px-5 max-xl:no-scrollbars max-md:gap-x-6">
      {items.map((item, index) => {
        const isActive = autoPlayIndex === index || trailingIndex === index;
        return (
          <li
            key={item.title}
            className="group row-span-2 grid w-full max-w-64 cursor-default grid-rows-subgrid content-start text-black max-xl:w-[min(16rem,78vw)] max-xl:shrink-0 max-xl:snap-start max-xl:grid-rows-[auto_auto] max-xl:gap-y-8 max-xl:self-start max-md:gap-y-6"
          >
            <p className="block max-w-sm text-base tracking-extra-tight text-pretty text-gray-new-50">
              <span className="font-semibold text-black">{item.title}.</span> {item.description}
            </p>
            <span className="relative block aspect-[5/4] w-full overflow-hidden rounded-[12px] border border-black/[0.08] bg-white font-sans shadow-[0_1px_0_rgba(0,0,0,0.03)] transition-[border-color] duration-300 ease-[cubic-bezier(0.16,1,0.3,1)] [@media(hover:hover)]:group-hover:border-black/[0.16]">
              <MiniFilm kind={item.kind} active={isActive} hovered={false} />
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
