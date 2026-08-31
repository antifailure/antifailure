"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { cn } from "@/lib/cn";

const SECTIONS = [
  { id: "migrations", title: "Migration Safety" },
  { id: "twins", title: "Isolated Twin" },
  { id: "features", title: "Safety properties" },
  { id: "workload", title: "Load" },
  { id: "firewall", title: "Side-Effect Firewall" },
];

/** Sticky header (56px below lg) plus the rail itself. */
const RAIL_OFFSET = 100;

/** Dissolve whichever edge still has a name behind it. */
function edgeMask({ left, right }: { left: boolean; right: boolean }) {
  if (!left && !right) return undefined;
  const start = left ? "transparent 0, #000 28px" : "#000 0";
  const end = right ? "#000 calc(100% - 28px), transparent 100%" : "#000 100%";
  return `linear-gradient(to right, ${start}, ${end})`;
}

function prefersReducedMotion() {
  return typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

function scrollToSection(id: string) {
  const el = document.getElementById(id);
  if (!el) return;
  const top = el.getBoundingClientRect().top + window.scrollY - RAIL_OFFSET;
  window.scrollTo({ top, behavior: prefersReducedMotion() ? "auto" : "smooth" });
}

/**
 * Where the reader is in the section stack.
 *
 * Two answers, because the two rails sit in different places. The desktop rail
 * is a column of links at a known height, so a section becomes current when its
 * top passes its own link. The mobile rail is a bar pinned under the header, so
 * a section becomes current when its top passes the bar.
 */
function useSectionState(tocRef: React.RefObject<HTMLDivElement | null>) {
  const [state, setState] = useState({ deskIndex: 0, railIndex: 0, progress: 0 });

  useEffect(() => {
    const update = () => {
      // The rail reads a third of the way down rather than at its own edge. A
      // probe at the bar itself named the previous section while most of the
      // screen already showed the next one.
      const probe = window.scrollY + Math.max(RAIL_OFFSET, window.innerHeight * 0.34);
      const links = tocRef.current?.querySelectorAll("li");

      let deskIndex = 0;
      let railIndex = 0;
      SECTIONS.forEach((section, index) => {
        const el = document.getElementById(section.id);
        if (!el) return;
        const top = el.getBoundingClientRect().top;
        if (top + window.scrollY <= probe) railIndex = index;

        const link = links?.[index];
        if (link && top <= link.getBoundingClientRect().top) deskIndex = index;
      });

      const first = document.getElementById(SECTIONS[0].id);
      const last = document.getElementById(SECTIONS[SECTIONS.length - 1].id);
      let progress = 0;
      if (first && last) {
        const start = first.getBoundingClientRect().top + window.scrollY;
        const end = last.getBoundingClientRect().bottom + window.scrollY;
        progress = Math.min(1, Math.max(0, (probe - start) / Math.max(1, end - start)));
      }

      setState((prev) =>
        prev.deskIndex === deskIndex &&
        prev.railIndex === railIndex &&
        Math.abs(prev.progress - progress) < 0.002
          ? prev
          : { deskIndex, railIndex, progress },
      );
    };

    update();
    window.addEventListener("scroll", update, { passive: true });
    window.addEventListener("resize", update);
    return () => {
      window.removeEventListener("scroll", update);
      window.removeEventListener("resize", update);
    };
  }, [tocRef]);

  return state;
}

export function TocWrapper({ children }: { children: React.ReactNode }) {
  const tocRef = useRef<HTMLDivElement>(null);
  const { deskIndex, railIndex, progress } = useSectionState(tocRef);

  return (
    <div className="relative">
      <div className="absolute top-0 bottom-0 left-[calc(50%-min(100vw,1600px)/2+32px)] h-full max-xl:hidden">
        <Toc activeIndex={deskIndex} tocRef={tocRef} />
      </div>
      <SectionRail activeIndex={railIndex} progress={progress} />
      {children}
    </div>
  );
}

/**
 * The mobile and tablet section rail.
 *
 * Below `xl` the desktop column is hidden, which left ten thousand pixels of
 * scroll with nothing to say where you were or what was left. This pins the
 * same five names under the header for exactly as long as the stack is on
 * screen, keeps the current one scrolled into view, and draws how far through
 * the argument the reader has come.
 */
function SectionRail({ activeIndex, progress }: { activeIndex: number; progress: number }) {
  const trackRef = useRef<HTMLDivElement>(null);
  const chipRefs = useRef<(HTMLAnchorElement | null)[]>([]);
  const [edges, setEdges] = useState({ left: false, right: true });

  const readEdges = useCallback(() => {
    const track = trackRef.current;
    if (!track) return;
    setEdges({
      left: track.scrollLeft > 4,
      right: track.scrollLeft + track.clientWidth < track.scrollWidth - 4,
    });
  }, []);

  useEffect(() => {
    const track = trackRef.current;
    const chip = chipRefs.current[activeIndex];
    if (!track || !chip) return;
    const left = chip.offsetLeft - (track.clientWidth - chip.clientWidth) / 2;
    track.scrollTo({ left: Math.max(0, left), behavior: prefersReducedMotion() ? "auto" : "smooth" });
    const id = window.setTimeout(readEdges, 420);
    return () => window.clearTimeout(id);
  }, [activeIndex, readEdges]);

  useEffect(readEdges, [readEdges]);

  return (
    <div
      className={cn(
        "sticky top-16 z-30 bg-[#f7f7f5] max-lg:top-14 xl:hidden",
      )}
    >
      <nav
        ref={trackRef}
        aria-label="Sections"
        onScroll={readEdges}
        style={{
          maskImage: edgeMask(edges),
          WebkitMaskImage: edgeMask(edges),
        }}
        className="no-scrollbars flex items-stretch gap-x-1 overflow-x-auto px-8 max-md:px-5"
      >
        {SECTIONS.map((section, index) => {
          const isActive = index === activeIndex;
          return (
            <a
              key={section.id}
              ref={(node) => {
                chipRefs.current[index] = node;
              }}
              href={`#${section.id}`}
              aria-current={isActive ? "location" : undefined}
              onClick={(event) => {
                event.preventDefault();
                scrollToSection(section.id);
              }}
              className={cn(
                "shrink-0 whitespace-nowrap border-b-[1.5px] py-3 pr-4 text-[13px] tracking-extra-tight transition-colors duration-200",
                "first:pl-0",
                isActive ? "border-black text-black" : "border-transparent text-black/55",
              )}
            >
              <span className="mr-1.5 font-mono text-[10px] tabular-nums text-black/30">
                {String(index + 1).padStart(2, "0")}
              </span>
              {section.title}
            </a>
          );
        })}
      </nav>
      <div className="h-px w-full bg-black/10" aria-hidden>
        <div
          className="h-px bg-black/45 transition-[width] duration-200 ease-out"
          style={{ width: `${Math.round(progress * 100)}%` }}
        />
      </div>
    </div>
  );
}

function Toc({
  activeIndex,
  tocRef,
}: {
  activeIndex: number;
  tocRef: React.RefObject<HTMLDivElement | null>;
}) {
  return (
    <div className="sticky top-0 z-10 pt-40 pb-60" ref={tocRef}>
      <ul className="flex w-[224px] flex-col gap-y-1.5">
        {SECTIONS.map((section, index) => {
          const isActive = index === activeIndex;
          return (
            <li key={section.id}>
              <a
                href={`#${section.id}`}
                className={cn(
                  "relative flex items-center gap-x-2.5 rounded-sm py-1.5 pl-[18px] whitespace-nowrap",
                  "text-[15px] leading-none tracking-tight transition-colors duration-200",
                  "before:absolute before:top-1/2 before:left-0 before:size-2 before:-translate-y-1/2 before:rounded-full before:transition-colors",
                  !isActive && "text-gray-new-50",
                  "hover:text-black",
                  isActive && "text-black before:bg-black",
                )}
                onClick={(e) => {
                  e.preventDefault();
                  document.getElementById(section.id)?.scrollIntoView({ behavior: "smooth", block: "start" });
                }}
              >
                {section.title}
              </a>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
