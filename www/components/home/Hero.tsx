import { Button } from "@/components/layout/Button";
import { Container } from "@/components/layout/Container";
import { SectionLabel } from "@/components/layout/SectionLabel";
import { HeroFilm } from "./media/HeroFilm";
import { HeroServices } from "./HeroServices";

export function Hero() {
  return (
    <section className="hero relative mt-16 safe-paddings max-lg:mt-14">
      <Container className="relative z-30 pt-96 pb-10 max-xl:pt-54 max-lg:pt-52 max-md:pt-53 max-md:pb-8" size="1600">
        <SectionLabel>Pre-production deployment safety</SectionLabel>
        <h1 className="mt-5 max-w-[1240px] text-[68px] leading-dense tracking-tighter max-xl:max-w-[1100px] max-xl:text-[60px] max-lg:max-w-[920px] max-lg:text-[48px] max-md:mt-4 max-md:max-w-full max-md:text-[42px] max-sm:text-[32px]">
          <span className="whitespace-nowrap max-xl:whitespace-normal">
            Know what happens before you deploy,
          </span>
          <br className="max-xl:hidden" />{" "}
          on a disposable production twin.
        </h1>
        <div className="mt-8 flex gap-x-5 max-lg:mt-7 max-lg:gap-x-4">
          <Button href="/signup" theme="filled">
            Get started
          </Button>
          <Button href="/docs" theme="outlined">
            Read the docs
          </Button>
        </div>
        <div className="relative mt-36 max-md:mt-24 max-sm:mt-16">
          <HeroServices />
        </div>
      </Container>

      <div className="pointer-events-none absolute inset-0 z-10 overflow-hidden">
        <HeroFilm />
      </div>
      <div className="absolute bottom-0 z-20 h-96 w-full bg-[linear-gradient(0deg,#f7f7f5_0%,#f7f7f5_42%,rgba(247,247,245,0)_100%)] max-xl:h-80 max-lg:h-72 max-sm:h-80" />
    </section>
  );
}
