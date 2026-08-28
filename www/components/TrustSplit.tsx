"use client";

import { useRef } from "react";
import { motion } from "motion/react";
import { EASE } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { RedTriangle } from "./icons";

function CylinderIcon({ play }: { play: boolean }) {
  return (
    <svg viewBox="0 0 28 28" className="h-7 w-7" fill="none">
      <motion.ellipse
        cx="14"
        cy="8"
        rx="8"
        ry="3.2"
        stroke="#FF3621"
        strokeWidth="1.4"
        initial={{ pathLength: 0 }}
        animate={{ pathLength: play ? 1 : 0 }}
        transition={{ duration: 0.7, ease: EASE }}
      />
      <motion.path
        d="M6 8v8c0 1.8 3.6 3.2 8 3.2s8-1.4 8-3.2V8"
        stroke="#FF3621"
        strokeWidth="1.4"
        initial={{ pathLength: 0 }}
        animate={{ pathLength: play ? 1 : 0 }}
        transition={{ duration: 0.8, delay: 0.12, ease: EASE }}
      />
      <motion.path
        d="M6 12c0 1.8 3.6 3.2 8 3.2s8-1.4 8-3.2"
        stroke="#FF3621"
        strokeWidth="1.4"
        initial={{ pathLength: 0 }}
        animate={{ pathLength: play ? 1 : 0 }}
        transition={{ duration: 0.6, delay: 0.28, ease: EASE }}
      />
    </svg>
  );
}

function StackIcon({ play }: { play: boolean }) {
  const layers = [
    { y: 16, delay: 0 },
    { y: 10, delay: 0.12 },
    { y: 4, delay: 0.24 },
  ];
  return (
    <svg viewBox="0 0 28 28" className="h-7 w-7" fill="none">
      {layers.map((l, i) => (
        <motion.rect
          key={l.y}
          x={4 + i * 2.5}
          width={20 - i * 5}
          height="6"
          stroke="#FF3621"
          strokeWidth="1.4"
          initial={{ y: l.y + 8, opacity: 0 }}
          animate={play ? { y: l.y, opacity: 1 } : { y: l.y + 8, opacity: 0 }}
          transition={{ duration: 0.5, delay: l.delay, ease: EASE }}
        />
      ))}
    </svg>
  );
}

export function TrustSplit() {
  const ref = useRef<HTMLElement>(null);
  const { story, reduced } = useInViewPlay(ref, 0.2);
  const play = story || reduced;

  return (
    <section ref={ref} className="min-h-screen bg-[#e8f1ed] text-black" id="trust">
      <div className="grid min-h-screen grid-cols-1 lg:grid-cols-[1.35fr_0.9fr]">
        <div className="flex min-h-screen flex-col border-black/10 px-10 py-12 lg:border-r lg:px-16">
          <div className="flex items-center gap-2 text-[11px] tracking-[0.16em] text-black/55">
            <RedTriangle />
            OPEN-CORE SAFETY
          </div>
          <h2 className="mt-8 max-w-[720px] text-[40px] font-semibold leading-[1.18] tracking-[-0.03em] md:text-[48px]">
            <span className="text-black">Open-core deployment safety, built for the customer boundary. </span>
            <span className="text-black/45">
              Architected so raw snapshots, secrets, and captured request bodies stay inside your cloud.
            </span>
          </h2>
          <div className="mt-auto grid grid-cols-2 gap-10 pt-16">
            <div>
              <CylinderIcon play={play} />
              <div className="mt-4 text-[34px] font-semibold tracking-tight">Fail closed</div>
              <p className="mt-1 text-[14px] text-black/50">Unknown egress, unresolved secrets, or incomplete cleanup blocks the run.</p>
            </div>
            <div>
              <StackIcon play={play} />
              <div className="mt-4 text-[34px] font-semibold tracking-tight">Customer-hosted</div>
              <p className="mt-1 text-[14px] text-black/50">
                The data plane stays in your account. The control plane never sees raw production state.
              </p>
            </div>
          </div>
        </div>

        <div className="relative flex min-h-screen flex-col px-10 py-12 lg:px-12">
          <div
            className="noise pointer-events-none absolute inset-0 opacity-40"
            style={{
              background:
                "radial-gradient(ellipse at 100% 0%, rgba(40,70,55,0.18), transparent 55%)",
            }}
          />
          <div className="relative flex items-center gap-2 text-[11px] tracking-[0.16em] text-black/55">
            <RedTriangle />
            THE JOB TO BE DONE
          </div>
          <blockquote className="relative mt-auto max-w-[420px] font-mono text-[15px] leading-7">
            “Before I merge or deploy a risky change, show me whether it will break under{" "}
            <mark className="relative bg-[#cfe8d8] text-black">
              production-shaped conditions
              <motion.span
                className="absolute inset-x-0 bottom-0 h-px bg-black/35"
                initial={{ scaleX: 0 }}
                animate={{ scaleX: play ? 1 : 0 }}
                transition={{ duration: 0.7, delay: 0.35, ease: EASE }}
                style={{ transformOrigin: "left" }}
              />
            </mark>{" "}
            and explain exactly why.”
            <footer className="mt-6 text-[13px]">
              <div className="font-medium">Platform engineer</div>
              <div className="text-black/55">Composite voice, B2B SaaS</div>
            </footer>
          </blockquote>
        </div>
      </div>
    </section>
  );
}
