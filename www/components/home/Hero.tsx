"use client";

import { CmsMedia, CmsSection, CmsText } from "@/components/cms/Editable";
import { useCmsString } from "@/components/cms/CmsProvider";
import { Button } from "@/components/layout/Button";
import { Container } from "@/components/layout/Container";
import { SectionLabel } from "@/components/layout/SectionLabel";
import { CopyCodeButton } from "./media/CopyCodeButton";
import { HeroDemoVideo } from "./HeroDemoVideo";
import { HeroFilm } from "./media/HeroFilm";
import { HeroServices } from "./HeroServices";
import { SectionGroup } from "@/components/cms/SectionGroup";

export function Hero() {
  const demoHref = useCmsString("hero.demo.href", "/request-demo", { label: "Demo destination", sectionId: "hero", kind: "url" });
  const install = useCmsString("hero.install.command", "curl -fsSL https://antifailure.dev/install.sh | sh", { label: "Install command", sectionId: "hero" });
  return (
    <CmsSection sectionId="hero" label="Hero" group="page" className="hero relative mt-16 safe-paddings max-xl:mt-14">
      <Container className="relative z-30 pt-[var(--hero-top)] pb-10 max-xl:pt-54 max-lg:pt-52 max-md:pt-53 max-md:pb-8" size="1600">
        <SectionLabel><CmsText cmsKey="hero.eyebrow" label="Hero label" sectionId="hero" defaultValue="Deployment testing for coding agents" /></SectionLabel>
        <CmsText as="h1" cmsKey="hero.heading" label="Headline" sectionId="hero" defaultValue="Know what happens before you deploy, on a disposable production twin." className="mt-5 max-w-[1240px] text-[68px] leading-dense tracking-tighter max-xl:max-w-[1100px] max-xl:text-[60px] max-lg:max-w-[920px] max-lg:text-[48px] max-md:mt-4 max-md:max-w-full max-md:text-[42px] max-sm:text-[32px]">
          <span className="whitespace-nowrap max-xl:whitespace-normal">
            Know what happens before you deploy,
          </span>
          <br className="max-xl:hidden" />{" "}
          on a disposable production twin.
        </CmsText>
        <div className="mt-8 flex flex-wrap items-center gap-x-5 gap-y-3 max-lg:mt-7 max-lg:gap-x-4 max-md:flex-col max-md:items-stretch max-md:gap-y-3 max-md:[&_a]:w-full">
          <Button cmsKey="hero.demo.label" href={demoHref} theme="filled">
            <CmsText cmsKey="hero.demo.label" label="Demo button" sectionId="hero" defaultValue="Request a demo" />
          </Button>
          <CopyCodeButton cmsKey="hero.install.command" code={install} copyText={install} variant="white" className="w-auto max-w-full max-xl:w-auto max-lg:w-auto max-md:w-full" />
        </div>
        <CmsText as="p" cmsKey="hero.description" label="Introduction" sectionId="hero" defaultValue="Give your coding agent a production twin through MCP. Test migrations, user flows, and load on masked data, then use the findings to fix the change before you ship." className="mt-5 max-w-[760px] text-base leading-6 tracking-extra-tight text-gray-new-20 max-lg:mt-4 max-lg:max-w-[520px]">
          Give your coding agent a production twin through MCP. Test migrations,
          user flows, and load on masked data, then use the findings to fix the
          change before you ship.
        </CmsText>
        <SectionGroup group="hero" source={[
          { id: "hero-services", label: "Feature overview", group: "hero", content: <div className="relative mt-20 max-lg:mt-16 max-md:mt-14 max-sm:mt-12"><HeroServices /></div> },
          { id: "hero-demo", label: "Product film", group: "hero", content: <HeroDemoVideo /> },
        ]} />
      </Container>

      <div className="pointer-events-none absolute inset-0 z-10 overflow-hidden">
        <CmsMedia cmsKey="hero.background" label="Hero background" sectionId="hero" className="h-full w-full"><HeroFilm /></CmsMedia>
      </div>
      <div className="absolute bottom-0 z-20 h-96 w-full bg-[linear-gradient(0deg,#f7f7f5_0%,#f7f7f5_42%,rgba(247,247,245,0)_100%)] max-xl:h-80 max-lg:h-72 max-sm:h-80" />
    </CmsSection>
  );
}
