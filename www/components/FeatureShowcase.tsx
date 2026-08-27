"use client";

import { useEffect, useState } from "react";
import { FeatureRail, RAIL } from "./FeatureRail";

export function FeatureShowcase({ children }: { children: React.ReactNode }) {
  const [active, setActive] = useState<(typeof RAIL)[number]["id"]>("pr");
  const [showRail, setShowRail] = useState(true);

  useEffect(() => {
    const ids = ["from-pr", "ide", "migration", "twins", "firewall", "dashboard"];
    const map: Record<string, (typeof RAIL)[number]["id"]> = {
      "from-pr": "pr",
      ide: "pr",
      migration: "migration",
      twins: "twins",
      firewall: "firewall",
      dashboard: "gate",
    };
    const els = ids.map((id) => document.getElementById(id)).filter(Boolean) as HTMLElement[];
    const io = new IntersectionObserver(
      (entries) => {
        const vis = entries
          .filter((e) => e.isIntersecting)
          .sort((a, b) => b.intersectionRatio - a.intersectionRatio)[0];
        if (vis?.target.id && map[vis.target.id]) setActive(map[vis.target.id]);
      },
      { rootMargin: "-20% 0px -45% 0px", threshold: [0.15, 0.4, 0.7] },
    );
    els.forEach((el) => io.observe(el));
    return () => io.disconnect();
  }, []);

  useEffect(() => {
    const trust = document.getElementById("trust");
    if (!trust) return;
    const io = new IntersectionObserver(
      ([entry]) => setShowRail(!entry.isIntersecting),
      { rootMargin: "120px 0px 0px 0px", threshold: 0 },
    );
    io.observe(trust);
    return () => io.disconnect();
  }, []);

  return (
    <div className="relative">
      <div
        className={`pointer-events-none sticky top-[72px] z-20 hidden lg:block ${
          showRail ? "opacity-100" : "pointer-events-none opacity-0"
        }`}
        style={{ transition: "opacity 0.25s ease" }}
        aria-hidden={!showRail}
      >
        <div
          className={`absolute left-8 top-2 lg:left-16 ${showRail ? "pointer-events-auto" : "pointer-events-none"}`}
        >
          <FeatureRail active={active} light={active === "migration" || active === "gate"} />
        </div>
      </div>
      {children}
    </div>
  );
}
