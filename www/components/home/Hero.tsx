import { Button } from "@/components/layout/Button";
import { Container } from "@/components/layout/Container";
import { SectionLabel } from "@/components/layout/SectionLabel";
import { HeroFilm } from "./media/HeroFilm";
import { HeroServices } from "./HeroServices";

export function Hero() {
  return (
    <section className="hero relative mt-16 safe-paddings max-lg:mt-14">
      <Container className="relative z-30 pt-96 pb-2 max-xl:pt-54 max-lg:pt-52 max-md:pt-53" size="1600">
        <SectionLabel>Pre-production deployment safety</SectionLabel>
        <h1 className="mt-5 max-w-280 text-[68px] leading-dense tracking-tighter max-xl:max-w-215 max-xl:text-[60px] max-lg:max-w-180 max-lg:text-[48px] max-md:mt-4 max-md:text-[42px] max-sm:text-[32px]">
          Know what happens before you deploy,
          <br />
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
        <div className="relative mt-16 max-md:mt-14 max-sm:mt-12">
          <HeroServices />
        </div>
      </Container>

      <div className="pointer-events-none absolute inset-0 z-10 overflow-hidden">
        <HeroFilm />
      </div>
      <div className="absolute bottom-0 z-20 h-22 w-full bg-[linear-gradient(0deg,#f7f7f5_0%,rgba(247,247,245,0.00)_100%)] max-xl:h-41 max-lg:h-39 max-sm:h-64.5" />
    </section>
  );
}
