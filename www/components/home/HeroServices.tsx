"use client";

import { useEffect, useState } from "react";
import { cn } from "@/lib/cn";
import { MiniFilm } from "./visuals/hero";

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
    description: "Observed patterns, deterministic journeys, and Crowdi users.",
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
  const items = HERO_SERVICES;

  useEffect(() => {
    const id = window.setInterval(() => {
      setAutoPlayIndex((i) => (i + 1) % items.length);
    }, 2800);
    return () => window.clearInterval(id);
  }, [items.length]);

  return (
    <ul className="grid grid-cols-5 grid-rows-[auto_auto] gap-x-16 gap-y-8 max-xl:gap-x-6 max-xl:gap-y-6 max-lg:-mx-5 max-lg:flex max-lg:snap-x max-lg:snap-mandatory max-lg:scroll-px-5 max-lg:gap-x-8 max-lg:gap-y-0 max-lg:overflow-x-auto max-lg:px-5 max-lg:no-scrollbars max-md:gap-x-6">
      {items.map((item, index) => {
        const isActive = autoPlayIndex === index;
        return (
          <li
            key={item.title}
            className="group row-span-2 grid w-full max-w-64 cursor-default grid-rows-subgrid content-start text-black max-lg:shrink-0 max-lg:snap-start max-lg:grid-rows-[auto_auto] max-lg:gap-y-8 max-lg:self-start max-md:gap-y-6"
          >
            <p className="block max-w-sm text-base tracking-extra-tight text-pretty text-gray-new-50 max-xl:text-sm/normal max-lg:text-base">
              <span className="font-semibold text-black">{item.title}.</span> {item.description}
            </p>
            <span className="relative block aspect-[5/4] w-full overflow-hidden bg-[#f1f1ef] font-sans ring-1 ring-black/10">
              <MiniFilm kind={item.kind} active={isActive} hovered={false} />
              <span
                className="noise pointer-events-none absolute inset-0 opacity-[0.16] mix-blend-multiply"
                aria-hidden
              />
              <span
                className={cn(
                  "pointer-events-none absolute inset-0 bg-black/0 transition-colors duration-200 ease-[cubic-bezier(0.16,1,0.3,1)]",
                  "[@media(hover:hover)]:group-hover:bg-black/45",
                )}
                aria-hidden
              />
            </span>
          </li>
        );
      })}
    </ul>
  );
}
