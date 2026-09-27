import { Button } from "@/components/layout/Button";
import { Container } from "@/components/layout/Container";
import { CtaWorkflow } from "./visuals/CtaWorkflow";

export function Cta() {
  return (
    <section className="cta overflow-hidden bg-forest text-gray-new-90 safe-paddings">
      <Container size="1600" className="grid grid-cols-[minmax(0,1.05fr)_minmax(0,0.95fr)] items-center gap-16 py-24 max-lg:grid-cols-1 max-lg:gap-10 max-md:py-14">
        <div>
          <p className="font-mono text-xs text-green-52">Antifailure + your coding agent</p>
          <h2 className="mt-6 max-w-[18ch] text-[64px] leading-dense tracking-tighter max-xl:text-[54px] max-md:text-[38px]">
            Test the change.<br />Then make the call.
          </h2>
          <p className="mt-6 max-w-[43ch] text-lg leading-8 text-gray-new-80 max-md:text-base max-md:leading-7">
            See Antifailure rehearse a change from your stack. Review the findings,
            then decide what to ship.
          </p>
          <div className="mt-9 flex flex-wrap gap-4 max-sm:flex-col max-sm:[&_a]:w-full">
            <Button href="/request-demo" theme="white">Request a demo</Button>
            <Button href="/docs/reference/mcp" theme="outlined-inverse">Connect your agent</Button>
          </div>
        </div>
        <CtaWorkflow />
      </Container>
    </section>
  );
}
