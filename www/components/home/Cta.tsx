"use client";

import { CmsMedia, CmsSection, CmsText } from "@/components/cms/Editable";
import { useCmsString } from "@/components/cms/CmsProvider";
import { Button } from "@/components/layout/Button";
import { Container } from "@/components/layout/Container";
import { CtaWorkflow } from "./visuals/CtaWorkflow";

export function Cta() {
  const demoHref = useCmsString("cta.demo.href", "/request-demo", { label: "Demo destination", sectionId: "cta", kind: "url" });
  const agentHref = useCmsString("cta.agent.href", "/docs/reference/mcp", { label: "Agent destination", sectionId: "cta", kind: "url" });
  return (
    <CmsSection sectionId="cta" label="Closing call to action" group="page" className="cta overflow-hidden bg-forest text-gray-new-90 safe-paddings">
      <Container size="1600" className="grid grid-cols-[minmax(0,1.05fr)_minmax(0,0.95fr)] items-center gap-16 py-24 max-lg:grid-cols-1 max-lg:gap-10 max-md:py-14">
        <div>
          <CmsText as="p" cmsKey="cta.eyebrow" label="Closing label" sectionId="cta" defaultValue="Antifailure + your coding agent" className="font-mono text-xs text-green-52" />
          <CmsText as="h2" cmsKey="cta.heading" label="Closing headline" sectionId="cta" defaultValue="Test the change. Then make the call." className="mt-6 max-w-[18ch] text-[64px] leading-dense tracking-tighter max-xl:text-[54px] max-md:text-[38px]">
            Test the change.<br />Then make the call.
          </CmsText>
          <CmsText as="p" cmsKey="cta.description" label="Closing description" sectionId="cta" defaultValue="See Antifailure rehearse a change from your stack. Review the findings, then decide what to ship." className="mt-6 max-w-[43ch] text-lg leading-8 text-gray-new-80 max-md:text-base max-md:leading-7">
            See Antifailure rehearse a change from your stack. Review the findings,
            then decide what to ship.
          </CmsText>
          <div className="mt-9 flex flex-wrap gap-4 max-sm:flex-col max-sm:[&_a]:w-full">
            <Button cmsKey="cta.demo.label" href={demoHref} theme="white"><CmsText cmsKey="cta.demo.label" label="Demo button" sectionId="cta" defaultValue="Request a demo" /></Button>
            <Button cmsKey="cta.agent.label" href={agentHref} theme="outlined-inverse"><CmsText cmsKey="cta.agent.label" label="Agent button" sectionId="cta" defaultValue="Connect your agent" /></Button>
          </div>
        </div>
        <CmsMedia cmsKey="cta.visual" label="Agent workflow visual" sectionId="cta"><CtaWorkflow /></CmsMedia>
      </Container>
    </CmsSection>
  );
}
