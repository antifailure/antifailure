"use client";

import { useEffect, useRef, useState } from "react";
import { LayoutGroup, motion } from "motion/react";
import { Container } from "@/components/layout/Container";
import { Heading } from "@/components/layout/Heading";
import { cn } from "@/lib/cn";
import { EASE } from "@/lib/easing";
import { MigrationScene, type MigrationBar } from "@/components/home/visuals/MigrationScene";

const TABS = ["Catch exclusive locks", "Safer expand-and-contract"] as const;
const VEIL_EASE = "cubic-bezier(0.16, 1, 0.3, 1)";
const CAPTIONS = [
  "An exclusive lock on subscriptions stalls checkout. The twin reports BLOCK before it ships.",
  "Expand-and-contract keeps checkout live. Lock 0.4s, rollback feasible, PASS.",
] as const;
const EVIDENCE = [
  "ACCESS EXCLUSIVE 27.4s · checkout p99 820ms→6.9s · 11.8% upgrade timeouts · rollback unsafe",
  "expand → backfill → contract · lock 0.4s · blocked 0 · p99 834ms · rollback feasible",
] as const;

export function Migrations() {
  const [active, setActive] = useState(0);
  const [playId, setPlayId] = useState(0);
  const [veil, setVeil] = useState(0);
  const [bar, setBar] = useState<MigrationBar>({
    verdict: "BLOCK",
    slam: false,
    decided: false,
    passGlow: false,
  });
  const busy = useRef(false);
  const timers = useRef<number[]>([]);

  useEffect(() => () => timers.current.forEach((id) => window.clearTimeout(id)), []);

  function cutTo(index: number) {
    if (busy.current || index === active) return;
    busy.current = true;
    setVeil(1);
    const cut = window.setTimeout(() => {
      setActive(index);
      setPlayId((n) => n + 1);
      setBar({
        verdict: index === 0 ? "BLOCK" : "PASS",
        slam: false,
        decided: false,
        passGlow: false,
      });
    }, 90);
    const clear = window.setTimeout(() => {
      setVeil(0);
      busy.current = false;
    }, 200);
    timers.current = [cut, clear];
  }

  return (
    <section
      className="relative scroll-mt-16 bg-[#E4F1EB] pt-32 pb-40 safe-paddings max-xl:py-[136px] max-lg:scroll-mt-0 max-lg:pt-20 max-lg:pb-[104px] max-md:pt-16 max-md:pb-20"
      id="migrations"
    >
      <Container
        className="relative grid grid-cols-[224px_1fr] items-start gap-x-32 before:block max-xl:grid-cols-1 max-xl:px-16 max-xl:before:hidden max-lg:px-16 max-md:px-5"
        size="1600"
      >
        <div className="min-w-0">
          <Heading
            icon="migrations"
            theme="light"
            title="<strong>Migration safety first.</strong> Measure locks, plans, pool pressure, and rollback feasibility on a production-shaped twin before the change ships."
          />
          <LayoutGroup id="mig-home-tabs">
          <div className="group relative z-20 mt-10 flex w-fit max-xl:mt-9 max-lg:mt-8 max-md:mt-7">
            {TABS.map((item, index) => (
              <button
                className={cn(
                  "relative h-11 min-w-[134px] px-4 py-3 whitespace-nowrap",
                  "font-medium leading-none tracking-extra-tight",
                  "border border-gray-new-10 even:border-l-0",
                  "max-xl:h-10 max-xl:min-w-[130px] max-lg:h-9 max-lg:min-w-[124px] max-lg:px-3 max-lg:py-2.5 max-md:text-[14px]",
                  index === active ? "text-gray-new-10" : "text-gray-new-10/80 hover:text-gray-new-10",
                )}
                key={item}
                type="button"
                onClick={() => cutTo(index)}
              >
                {index === active ? (
                  <motion.span
                    layoutId="mig-home-tab"
                    className="absolute inset-0 bg-white"
                    transition={{ duration: 0.38, ease: EASE }}
                  />
                ) : null}
                <span className="relative z-10">{item}</span>
              </button>
            ))}
          </div>
          </LayoutGroup>
          <div className="relative z-10 mt-8 w-full min-w-0 max-lg:mt-6">
            <div className="relative">
              <MigrationScene tab={active as 0 | 1} playId={playId} onBar={setBar} />
              <div className="relative z-20 border-x border-b border-gray-new-10 bg-[#CAE6D9] px-5 py-3 max-md:px-4">
                <p className="font-mono text-[13px] leading-5 tracking-extra-tight text-pretty text-[#285D49] max-xl:text-[12px] max-md:text-[12px] max-md:leading-5">
                  <span
                    className={cn(
                      "font-semibold uppercase tabular-nums",
                      bar.verdict === "BLOCK" && bar.decided && "text-red-600",
                      bar.verdict === "PASS" && bar.passGlow && "text-green-45",
                    )}
                    style={{
                      letterSpacing: bar.slam ? "0.05em" : "0em",
                      transition: bar.slam ? "none" : `letter-spacing 220ms ${VEIL_EASE}`,
                    }}
                  >
                    {bar.verdict}
                  </span>
                  <span className="ml-2 font-medium normal-case">{EVIDENCE[active]}</span>
                </p>
              </div>
              <div
                className="pointer-events-none absolute inset-0 z-30 bg-[#E4F1EB]"
                style={{
                  opacity: veil,
                  transition: `opacity 90ms ${VEIL_EASE}`,
                }}
                aria-hidden
              />
            </div>
          </div>
          <p className="relative z-20 mt-10 max-w-[640px] text-[18px] leading-normal tracking-extra-tight text-black max-xl:mt-8 max-md:mt-7 max-md:text-[15px]">
            {CAPTIONS[active]}
          </p>
          <p className="relative z-20 mt-4 max-w-[640px] text-[18px] leading-normal tracking-extra-tight text-gray-new-40 max-md:text-[15px]">
            The first wedge is automated safety validation for risky Postgres-backed web deployments,
            especially schema migrations.
          </p>
        </div>
      </Container>
    </section>
  );
}
