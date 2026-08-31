"use client";

import { cn } from "@/lib/cn";
import { DiscoverCard } from "./safety/DiscoverCard";
import { FailClosedCard } from "./safety/FailClosedCard";
import { VerdictCard } from "./safety/VerdictCard";

/**
 * Card geometry is authored against a 288x350 reference card and resolved
 * against each card's own width, so a card stays an exact scaled copy of the
 * reference at every viewport.
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
    description: "A gate on the pull request carrying the rows, the trace, and the video behind it.",
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
        <article className="@container relative aspect-[288/350]" data-card key={title}>
          <div
            className="absolute inset-0 overflow-hidden border border-black/[0.08] bg-white"
            style={{ borderRadius: u(20) }}
          >
            <div className="pointer-events-none absolute inset-x-0 bottom-0 select-none" style={{ top: u(90) }}>
              <Visual />
            </div>
            <div className="relative" style={{ paddingLeft: u(24), paddingRight: u(24), paddingTop: u(28) }}>
              <h3
                className="font-medium tracking-extra-tight text-black-pure"
                style={{ fontSize: u(13), lineHeight: u(17) }}
              >
                {title}
              </h3>
              <p
                className="tracking-extra-tight text-pretty text-gray-new-50"
                style={{ fontSize: u(11.5), lineHeight: u(16), marginTop: u(5) }}
              >
                {description}
              </p>
            </div>
          </div>
        </article>
      ))}
    </div>
  );
}
