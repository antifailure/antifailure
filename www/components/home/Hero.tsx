import { Button } from "@/components/layout/Button";
import { Container } from "@/components/layout/Container";
import { SectionLabel } from "@/components/layout/SectionLabel";
import { CopyCodeButton } from "./media/CopyCodeButton";
import { HeroDemoVideo } from "./HeroDemoVideo";
import { HeroFilm } from "./media/HeroFilm";
import { HeroServices } from "./HeroServices";

export function Hero() {
  return (
    <section className="hero relative mt-16 safe-paddings max-xl:mt-14">
      <Container className="relative z-30 pt-96 pb-10 max-xl:pt-54 max-lg:pt-52 max-md:pt-53 max-md:pb-8" size="1600">
        <SectionLabel>Pre-production deployment safety</SectionLabel>
        <h1 className="mt-5 max-w-[1240px] text-[68px] leading-dense tracking-tighter max-xl:max-w-[1100px] max-xl:text-[60px] max-lg:max-w-[920px] max-lg:text-[48px] max-md:mt-4 max-md:max-w-full max-md:text-[42px] max-sm:text-[32px]">
          <span className="whitespace-nowrap max-xl:whitespace-normal">
            Know what happens before you deploy,
          </span>
          <br className="max-xl:hidden" />{" "}
          on a disposable production twin.
        </h1>
        {/* The free path first, because it is the only one a visitor can take
            today without somebody else's permission. The primary button used
            to be "Request access", which leads to an invitation wall, so the
            page pitched a product and then pointed at a locked door. The
            engine is MIT licensed and the quickstart needs no account, so that
            is the action, and the install line under it is the first command
            of it rather than a third call to action. */}
        {/* THREE CONTROLS ON ONE LINE. The install command sat on its own row
            below the two buttons, square where they are round, which read as a
            leftover rather than as the third thing you can do here. It is the
            first command of the quickstart beside it, so it belongs beside it.
            Below `md` all three stack full width, as the buttons already did. */}
        <div className="mt-8 flex flex-wrap items-center gap-x-5 gap-y-3 max-lg:mt-7 max-lg:gap-x-4 max-md:flex-col max-md:items-stretch max-md:gap-y-3 max-md:[&_a]:w-full">
          <Button href="/docs/getting-started/quickstart" theme="filled">
            Start the quickstart
          </Button>
          <Button href="/signup" theme="outlined">
            Request hosted access
          </Button>
          {/* No fill and no border of its own: the variant now carries Button's
              outlined theme, so this only has to stop being 34.2% of the row. */}
          <CopyCodeButton variant="white" className="w-auto max-w-full max-xl:w-auto max-md:w-full" />
        </div>
        {/* The state of the product, on the page that sends the most people to
            /signup. This button said "Get started" and led to an invitation
            wall, and the only page that admitted it was /pricing, which most
            visitors never open. Worded to match that page rather than beside
            it: two descriptions of one product state is how the first of them
            goes stale.

            The two sentences are now in the order a visitor needs them: what
            they can have, then what they cannot have yet. */}
        <p className="mt-5 max-w-[760px] text-[15px] leading-6 tracking-extra-tight text-gray-new-40 max-lg:mt-4 max-lg:max-w-[520px] max-md:text-[14px]">
          The engine is open source and runs in your own continuous integration
          today, with no account.
          {/* Broken at the sentence, the same way the h1 above is, rather than
              left to text-balance, which put the first sentence's "The" alone
              at the end of a line. */}
          <br className="max-lg:hidden" />{" "}
          The hosted control plane is invitation only while it is in development.
        </p>
        {/* mt-36 was 144 pixels of nothing between two short sentences and the
            five things this product is. The gap is the section rhythm now, and
            the same one the film below it uses. */}
        <div className="relative mt-20 max-lg:mt-16 max-md:mt-14 max-sm:mt-12">
          <HeroServices />
        </div>
        <HeroDemoVideo />
      </Container>

      <div className="pointer-events-none absolute inset-0 z-10 overflow-hidden">
        <HeroFilm />
      </div>
      <div className="absolute bottom-0 z-20 h-96 w-full bg-[linear-gradient(0deg,#f7f7f5_0%,#f7f7f5_42%,rgba(247,247,245,0)_100%)] max-xl:h-80 max-lg:h-72 max-sm:h-80" />
    </section>
  );
}
