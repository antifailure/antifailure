"use client";

import { cn } from "@/lib/cn";
import type { CSSProperties } from "react";
import { DiscoverCard } from "./safety/DiscoverCard";
import { FailClosedCard } from "./safety/FailClosedCard";
import { VerdictCard } from "./safety/VerdictCard";

/**
 * Card geometry is authored against a 288x350 reference card and resolved
 * against each card's own width. Mobile captions keep a readable text size
 * and participate in layout above the scaled illustration.
 */
export const REF_CARD_WIDTH = 288;

export function u(px: number) {
  return `${((px / 288) * 100).toFixed(4)}cqw`;
}

const CARDS = [
  {
    title: "Fail closed.",
    description: "Unknown destinations are denied inside the twin, and an unverified golden cannot be branched.",
    Visual: FailClosedCard,
  },
  {
    title: "Traffic shaped like production's.",
    description: "The route mix out of your own access log, with the worst regression first.",
    Visual: DiscoverCard,
  },
  {
    title: "Pass or fail, with evidence.",
    description: "A gate on the pull request carrying the rows and the trace behind it.",
    Visual: VerdictCard,
  },
];

export function SafetyCards({ className }: { className?: string }) {
  return (
    <div
      className={cn("grid grid-cols-3 gap-5 font-sans max-lg:gap-4 max-md:grid-cols-1 max-md:gap-3", className)}
      data-cards="safety"
    >
      {CARDS.map(({ title, description, Visual }) => (
        <article className="@container relative aspect-[288/350] max-md:aspect-auto" data-card key={title}>
          <div
            className="absolute inset-0 overflow-hidden border border-black/[0.08] bg-white max-md:relative max-md:inset-auto"
            style={{ borderRadius: u(20) }}
          >
            <div className="relative" style={{ paddingLeft: u(24), paddingRight: u(24), paddingTop: u(28) }}>
              <h3
                className="font-medium tracking-extra-tight text-black-pure text-[length:var(--caption-size)] leading-[var(--caption-leading)] max-md:text-base max-md:leading-6"
                style={{ "--caption-size": u(13), "--caption-leading": u(17) } as CSSProperties}
              >
                {title}
              </h3>
              <p
                className="tracking-extra-tight text-pretty text-gray-new-50 text-[length:var(--caption-size)] leading-[var(--caption-leading)] max-md:text-base max-md:leading-6"
                style={{ "--caption-size": u(11.5), "--caption-leading": u(16), marginTop: u(5) } as CSSProperties}
              >
                {description}
              </p>
            </div>
            <div className="pointer-events-none absolute inset-x-0 bottom-0 top-[var(--visual-top)] select-none max-md:relative max-md:inset-auto max-md:h-[90cqw]" style={{ "--visual-top": u(90) } as CSSProperties}>
              <Visual />
            </div>
          </div>
        </article>
      ))}
    </div>
  );
}
