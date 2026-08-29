"use client";

import { useEffect, useRef, useState } from "react";
import { cn } from "@/lib/cn";

const SECTIONS = [
  { id: "migrations", title: "Migration Safety", theme: "light" as const },
  { id: "workload", title: "Workload Studio", theme: "light" as const },
  { id: "features", title: "Safety properties", theme: "light" as const },
  { id: "twins", title: "Isolated Twin", theme: "light" as const },
  { id: "firewall", title: "Side-Effect Firewall", theme: "light" as const },
];

export function TocWrapper({ children }: { children: React.ReactNode }) {
  return (
    <div className="relative">
      <div className="absolute top-0 bottom-0 left-[calc(50%-min(100vw,1600px)/2+32px)] h-full max-xl:hidden">
        <Toc />
      </div>
      {children}
    </div>
  );
}

function Toc() {
  const [active, setActive] = useState(SECTIONS[0].id);
  const [theme, setTheme] = useState<"light" | "sage">("light");
  const tocRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const update = () => {
      if (!tocRef.current) return;
      const links = tocRef.current.querySelectorAll("li");
      let current = SECTIONS[0];
      SECTIONS.forEach((section, index) => {
        const el = document.getElementById(section.id);
        const link = links[index];
        if (!el || !link) return;
        if (el.getBoundingClientRect().top <= link.getBoundingClientRect().top) {
          current = section;
        }
      });
      setActive(current.id);
      setTheme(current.theme);
    };
    update();
    window.addEventListener("scroll", update, { passive: true });
    window.addEventListener("resize", update);
    return () => {
      window.removeEventListener("scroll", update);
      window.removeEventListener("resize", update);
    };
  }, []);

  return (
    <div className="sticky top-0 z-10 pt-40 pb-60" ref={tocRef}>
      <ul className="flex w-[224px] flex-col gap-y-1.5">
        {SECTIONS.map((section) => {
          const isActive = active === section.id;
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
                  theme === "sage" && isActive && "text-black before:bg-black",
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
