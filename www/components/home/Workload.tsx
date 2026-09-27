import { CmsMedia, CmsSection, CmsText } from "@/components/cms/Editable";
import { Container } from "@/components/layout/Container";
import { Heading } from "@/components/layout/Heading";
import { Illustrative } from "@/components/layout/Illustrative";
import { WorkloadIdeStage } from "./visuals/WorkloadIdeStage";

export function Workload() {
  return (
    <CmsSection sectionId="workload" label="Load testing" group="page"
      className="relative scroll-mt-[60px] overflow-hidden pt-10 pb-10 max-xl:pt-8 max-xl:pb-8 max-lg:pt-7 max-lg:pb-7 max-md:pt-6 max-md:pb-6 safe-paddings max-lg:scroll-mt-0"
      id="workload"
    >
      <Container
        className="relative grid grid-cols-[224px_1fr] items-start gap-x-32 before:block max-xl:grid-cols-1 max-xl:px-16 max-xl:before:hidden max-lg:px-16 max-md:px-5"
        size="1600"
      >
        <div className="min-w-0">
          <Heading
            cmsKey="workload.heading" sectionId="workload"
            icon="workload"
            label="Load"
            title="<strong>Test the routes your users rely on.</strong> Replay your production traffic mix against the new build."
          />
          <div className="relative mt-14 min-w-0 max-xl:mt-12 max-lg:mt-10 max-md:mt-8 max-sm:mt-11">
            <CmsMedia cmsKey="workload.visual" label="Load testing visual" sectionId="workload"><WorkloadIdeStage /></CmsMedia>
            <Illustrative className="mt-6">
              <CmsText cmsKey="workload.caption" label="Visual caption" sectionId="workload" defaultValue="Example project using the Antifailure CLI." />
            </Illustrative>
          </div>
        </div>
      </Container>
    </CmsSection>
  );
}
