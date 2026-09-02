import { Button } from "@/components/layout/Button";
import { Container } from "@/components/layout/Container";
import { CopyCodeButton } from "./media/CopyCodeButton";
import { CtaBackdrop } from "./visuals/CtaBackdrop";

/**
 * The closing panel, on every page.
 *
 * Two things were wrong here and both were visible on every single page of the
 * site. The backdrop rendered nothing, leaving a tall black void with a stray
 * block of monospace journal text sitting on top of the paragraph; and the
 * layout used a fixed aspect ratio, which is what created the void in the
 * first place, because 1920/944 at a 1440 viewport reserves 708px of height
 * for two lines of text and three buttons.
 *
 * The height now comes from the content, with a floor so the panel still reads
 * as a full-width moment rather than a strip.
 */
export function Cta() {
  return (
    <section className="cta relative isolate overflow-hidden bg-black safe-paddings">
      <CtaBackdrop className="pointer-events-none absolute inset-0 -z-10 h-full w-full" />
      <div className="pointer-events-none absolute inset-x-0 bottom-0 z-0 h-[58%] bg-[linear-gradient(0deg,#000_0%,#000_28%,rgba(0,0,0,0.55)_62%,rgba(0,0,0,0)_100%)] max-lg:h-[52%]" />

      <Container
        className="relative z-10 flex min-h-[520px] flex-col justify-between gap-y-16 py-20 max-lg:min-h-0 max-lg:gap-y-12 max-lg:py-16 max-md:gap-y-10 max-md:py-14"
        size="1920"
      >
        <h2 className="font-title text-[80px] leading-none tracking-tighter text-balance text-white max-xl:text-[64px] max-lg:text-[44px] max-md:text-[34px] max-sm:text-[30px]">
          Know what happens
          <br className="max-sm:hidden" /> before you deploy.
        </h2>

        <div className="flex items-end justify-between gap-x-14 max-lg:flex-col max-lg:items-start max-lg:gap-y-8">
          <p className="max-w-[720px] text-[28px] leading-tight tracking-tighter text-balance text-white/75 max-xl:max-w-[480px] max-xl:text-[24px] max-lg:max-w-[560px] max-lg:text-[20px] max-md:text-[17px]">
            Create a disposable production twin for every risky change. Catch
            migration failures before they reach customers.
          </p>

          {/* Two actions and, under them, the install line. The command is the
              widest element in the row and it is not a third call to action, so
              it sits quietly beneath rather than competing with the buttons for
              the remaining width. Sized to its content: the shared white
              variant is a fixed percentage of the viewport, which truncated a
              fifty character command down to "curl -fsS…". */}
          <div className="flex shrink-0 flex-col items-end gap-y-4 max-lg:w-full max-lg:items-stretch">
            <div className="flex items-center gap-4 max-md:flex-col max-md:items-stretch max-md:gap-y-3 max-md:[&_a]:w-full">
              <Button href="/signup" theme="white">
                Request access
              </Button>
              <Button href="/docs" theme="outlined-inverse">
                Read the docs
              </Button>
            </div>
            <CopyCodeButton variant="terminal" className="max-lg:w-full" />
          </div>
        </div>
      </Container>
    </section>
  );
}
