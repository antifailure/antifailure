"use client";

import { useEffect, useRef, useState } from "react";
import { Container } from "@/components/layout/Container";
import { Heading } from "@/components/layout/Heading";
import { MigrationScene } from "@/components/home/visuals/MigrationScene";

const CAPTIONS = [
  "An exclusive lock on subscriptions stalls checkout. The twin reports BLOCK before it ships.",
  "Expand-and-contract keeps checkout live. Lock 0.4s, rollback feasible, PASS.",
] as const;

const VEIL_EASE = "cubic-bezier(0.16, 1, 0.3, 1)";

export function Migrations() {
  const [active, setActive] = useState<0 | 1>(0);
  const [playId, setPlayId] = useState(0);
  const [veil, setVeil] = useState(0);
  const busy = useRef(false);
  const timers = useRef<number[]>([]);

  useEffect(() => () => timers.current.forEach((id) => window.clearTimeout(id)), []);

  function cutTo(index: 0 | 1) {
    if (busy.current || index === active) return;
    busy.current = true;
    setVeil(1);
    const cut = window.setTimeout(() => {
      setActive(index);
      setPlayId((n) => n + 1);
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
          <div className="relative z-10 mt-14 w-full min-w-0 max-xl:mt-12 max-lg:mt-10">
            <div className="relative">
              <MigrationScene tab={active} playId={playId} onTab={cutTo} />
              <div
                className="pointer-events-none absolute inset-0 z-30 rounded-[16px] bg-[#E4F1EB]"
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
