"use client";

import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { FeatureDiagram, type FeatureKind } from "./visuals/FeatureDiagram";

export const HERO_SERVICES: {
  title: string;
  lines: [string, string];
  kind: FeatureKind;
  href: string;
}[] = [
  { title: "Isolated Twin", lines: ["Your stack, isolated", "for each change."], kind: "twin", href: "/product/twins" },
  { title: "Safe State", lines: ["Masked Postgres.", "Relationships intact."], kind: "state", href: "/product/safe-state" },
  { title: "Side-Effect Firewall", lines: ["Test payments and mail.", "Contain external calls."], kind: "firewall", href: "/product/firewall" },
  { title: "Load", lines: ["Your traffic mix.", "Against the new build."], kind: "workload", href: "/product/load" },
  { title: "Migration Safety", lines: ["Locks, rewrites, plans.", "Before you deploy."], kind: "migration", href: "/product/migrations" },
];

export function HeroServices() {
  const track = useRef<HTMLUListElement>(null);
  const [edges, setEdges] = useState({ start: true, end: false });
  const readEdges = useCallback(() => {
    const element = track.current;
    if (!element) return;
    const start = element.scrollLeft < 2;
    const end = element.scrollLeft + element.clientWidth >= element.scrollWidth - 2;
    setEdges((previous) => previous.start === start && previous.end === end ? previous : { start, end });
  }, []);
  useEffect(() => {
    const element = track.current;
    if (!element) return;
    const observer = new ResizeObserver(readEdges);
    observer.observe(element);
    readEdges();
    return () => observer.disconnect();
  }, [readEdges]);
  const move = (direction: number) => {
    const element = track.current;
    const item = element?.firstElementChild;
    if (!element || !item) return;
    element.scrollBy({
      left: direction * (item.getBoundingClientRect().width + 20),
      behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth",
    });
  };
  return (
    <div>
    <ul
      ref={track}
      onScroll={readEdges}
      aria-label="What a run does"
      className="grid grid-cols-5 gap-6 max-lg:-mx-8 max-lg:flex max-lg:snap-x max-lg:snap-mandatory max-lg:scroll-px-8 max-lg:gap-5 max-lg:overflow-x-auto max-lg:px-8 max-lg:pb-3 max-md:-mx-5 max-md:scroll-px-5 max-md:px-5"
    >
      {HERO_SERVICES.map((item) => (
        <li key={item.kind} className="min-w-0 max-lg:w-[220px] max-lg:shrink-0 max-lg:snap-start">
          <Link
            prefetch={false}
            href={item.href}
            className="group block border-t border-stroke pt-4 focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-black"
          >
            <p className="h-[72px] text-base leading-6 tracking-extra-tight text-gray-new-40">
              <span className="block whitespace-nowrap font-medium text-gray-new-10 group-hover:underline group-hover:underline-offset-4">{item.title}</span>
              {item.lines.map((line) => <span key={line} className="block whitespace-nowrap">{line}</span>)}
            </p>
            <div className="mt-4 h-[164px]">
              <FeatureDiagram kind={item.kind} />
            </div>
          </Link>
        </li>
      ))}
    </ul>
    <div className="mt-3 hidden items-center justify-between max-lg:flex">
      <span className="text-xs text-gray-new-40">Explore all five</span>
      <div className="flex gap-2">
        <button type="button" onClick={() => move(-1)} disabled={edges.start} aria-label="Previous features" className="flex size-11 items-center justify-center border border-stroke text-lg text-gray-new-10 hover:bg-sage focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-black disabled:cursor-not-allowed disabled:border-gray-new-90 disabled:text-gray-new-80 disabled:hover:bg-transparent">←</button>
        <button type="button" onClick={() => move(1)} disabled={edges.end} aria-label="Next features" className="flex size-11 items-center justify-center border border-stroke text-lg text-gray-new-10 hover:bg-sage focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-black disabled:cursor-not-allowed disabled:border-gray-new-90 disabled:text-gray-new-80 disabled:hover:bg-transparent">→</button>
      </div>
    </div>
    </div>
  );
}
