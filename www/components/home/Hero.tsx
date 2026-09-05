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
        <div className="mt-8 flex gap-x-5 max-lg:mt-7 max-lg:gap-x-4 max-md:flex-col max-md:gap-y-3 max-md:[&_a]:w-full">
          <Button href="/docs/getting-started/quickstart" theme="filled">
            Start the quickstart
          </Button>
          <Button href="/signup" theme="outlined">
            Request hosted access
          </Button>
        </div>
        <CopyCodeButton
          variant="white"
          className="mt-5 w-auto max-w-full border border-black/12 bg-white px-4 hover:bg-[#f7f7f5] max-xl:w-auto max-lg:w-full"
        />
        {/* The state of the product, on the page that sends the most people to
            /signup. This button said "Get started" and led to an invitation
            wall, and the only page that admitted it was /pricing, which most
            visitors never open. Worded to match that page rather than beside
            it: two descriptions of one product state is how the first of them
            goes stale.

            The two sentences are now in the order a visitor needs them: what
            they can have, then what they cannot have yet. */}
        <p className="mt-6 max-w-[760px] text-[15px] leading-6 tracking-extra-tight text-gray-new-40 max-lg:mt-5 max-lg:max-w-[520px] max-md:text-[14px]">
          The engine is open source and runs in your own continuous integration
          today, with no account.
          {/* Broken at the sentence, the same way the h1 above is, rather than
              left to text-balance, which put the first sentence's "The" alone
              at the end of a line. */}
          <br className="max-lg:hidden" />{" "}
          The hosted control plane is invitation only while it is in development.
        </p>
        <div className="relative mt-36 max-md:mt-24 max-sm:mt-16">
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
