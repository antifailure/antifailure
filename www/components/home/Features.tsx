import { CmsMedia, CmsSection } from "@/components/cms/Editable";
import { Container } from "@/components/layout/Container";
import { Heading } from "@/components/layout/Heading";
import { SafetyCards } from "./visuals/SafetyCards";

export function Features() {
  return (
    <CmsSection sectionId="features" label="Safety properties" group="page"
      className="relative scroll-mt-[60px] pt-10 pb-10 max-xl:pt-8 max-xl:pb-8 max-lg:pt-7 max-lg:pb-7 max-md:pt-6 max-md:pb-6 safe-paddings max-lg:scroll-mt-0"
      id="features"
    >
      <Container
        className="relative grid grid-cols-[224px_1fr] items-center gap-x-32 before:block max-xl:grid-cols-1 max-xl:px-16 max-xl:before:hidden max-lg:px-16 max-md:px-5"
        size="1600"
      >
        <div className="min-w-0 border-t border-black/12 pt-9 max-lg:pt-7">
          <Heading
            cmsKey="features.heading" sectionId="features"
            icon="features"
            label="Safety properties"
            title="<strong>Make the call with evidence.</strong> Review what ran, what failed, and how to reproduce it."
          />
          <CmsMedia cmsKey="features.visual" label="Safety evidence visual" sectionId="features"><SafetyCards className="mt-16 max-xl:mt-12 max-lg:mt-10 max-md:mt-8" /></CmsMedia>
        </div>
      </Container>
    </CmsSection>
  );
}
