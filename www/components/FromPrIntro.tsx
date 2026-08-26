"use client";

import { useEffect, useRef, useState } from "react";
import { useScroll } from "motion/react";
import { useInViewPlay } from "@/lib/useInViewPlay";

const COPY =
  "A disposable production twin for every risky change. Connect a repository and cloud environment. The platform proves whether it is safe to ship.";
const WORDS = COPY.split(" ");

export function FromPrIntro() {
  const ref = useRef<HTMLElement>(null);
  const { reduced } = useInViewPlay(ref, 0.12);
  const { scrollYProgress } = useScroll({
    target: ref,
    offset: ["start start", "end end"],
  });
  const [t, setT] = useState(reduced ? 1 : 0);

  useEffect(() => {
    if (reduced) {
      setT(1);
      return;
    }
    setT(scrollYProgress.get());
    return scrollYProgress.on("change", (v) => setT(v));
  }, [scrollYProgress, reduced]);

  return (
    <section ref={ref} id="from-pr" className="relative bg-black">
      <div className="h-[180vh]">
        <div className="sticky top-[58px] flex min-h-[calc(100vh-58px)] items-center px-8 py-16 lg:px-16 lg:pl-[260px]">
          <h2 className="mx-auto max-w-[820px] text-center text-[40px] font-semibold leading-[1.15] tracking-[-0.035em] md:text-[48px]">
            {WORDS.map((word, i) => {
              const lit = reduced || t >= i / Math.max(1, WORDS.length - 1);
              return (
                <span
                  key={`${word}-${i}`}
                  className={lit ? "text-white" : "text-white/25"}
                  style={{ transition: "color 0.12s linear" }}
                >
                  {word}
                  {i < WORDS.length - 1 ? " " : ""}
                </span>
              );
            })}
          </h2>
        </div>
      </div>
    </section>
  );
}
