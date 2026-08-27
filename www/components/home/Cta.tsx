"use client";

import { Button } from "@/components/layout/Button";
import { Container } from "@/components/layout/Container";
import { CopyCodeButton } from "./media/CopyCodeButton";
import { CtaAtmosphere } from "./visuals/CtaAtmosphere";

export function Cta() {
  return (
    <section className="cta relative bg-[#151617] safe-paddings">
      <div className="relative aspect-[1920/944] max-h-[944px] w-full overflow-hidden max-md:aspect-auto max-md:h-[500px]">
        <CtaAtmosphere />
        <div className="absolute inset-0 z-10 pt-14 pb-9 max-xl:pt-12 max-xl:pb-5 max-lg:pt-9 max-md:pt-[52px] max-md:pb-6">
          <Container className="flex h-full flex-col" size="1920">
            <h2 className="font-title text-[80px] leading-none tracking-tighter text-white max-xl:text-[64px] max-lg:text-[44px] max-md:text-[32px]">
              Know what happens
              <br />
              before you deploy.
            </h2>
            <div className="mt-auto flex items-end justify-between gap-x-14 max-lg:flex-col max-lg:items-start max-lg:gap-y-5 max-md:gap-y-6">
              <p className="max-w-[860px] text-[32px] leading-tight tracking-tighter text-white/80 max-xl:max-w-[480px] max-xl:text-[24px] max-lg:max-w-[520px] max-lg:text-[20px] max-md:text-[18px]">
                Create a disposable production twin for every risky change.
                <br className="max-sm:hidden" /> Catch migration failures before they reach customers.
              </p>
              <div className="mb-2 flex items-center gap-5 max-xl:gap-4 max-lg:mb-0 max-md:w-full max-md:flex-col max-md:items-stretch max-md:gap-y-3">
                <Button href="/signup" theme="white">
                  Get started
                </Button>
                <Button
                  href="/docs"
                  theme="outlined"
                  className="border-white/40 bg-white/[0.02] text-white hover:border-white"
                >
                  Read the docs
                </Button>
                <CopyCodeButton variant="green" className="inline-flex items-center gap-x-3 font-mono font-medium" />
              </div>
            </div>
          </Container>
        </div>
      </div>
    </section>
  );
}
