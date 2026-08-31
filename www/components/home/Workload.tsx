import { Container } from "@/components/layout/Container";
import { Heading } from "@/components/layout/Heading";
import { Illustrative } from "@/components/layout/Illustrative";
import { WorkloadIdeStage } from "./visuals/WorkloadIdeStage";

export function Workload() {
  return (
    <section
      className="relative scroll-mt-[60px] overflow-hidden pt-12 pb-16 safe-paddings max-xl:pt-10 max-xl:pb-14 max-lg:scroll-mt-0 max-lg:pt-8 max-lg:pb-12 max-md:pt-8 max-md:pb-12"
      id="workload"
    >
      <Container
        className="relative grid grid-cols-[224px_1fr] items-start gap-x-32 before:block max-xl:grid-cols-1 max-xl:px-16 max-xl:before:hidden max-lg:px-16 max-md:px-5"
        size="1600"
      >
        <div className="min-w-0">
          <Heading
            icon="workload"
            label="Load"
            title="<strong>Traffic shaped like production's.</strong> The route mix out of your own access log, sent at the twin. Not production traffic diverted."
          />
          <div className="relative mt-14 min-w-0 max-xl:mt-12 max-lg:mt-10 max-md:mt-8 max-sm:mt-11">
            <WorkloadIdeStage />
            <Illustrative className="mt-6">
              An editor with the real files in it. af init, af up and af ci exist and do what the
              terminal shows; the repository and its contents are made up.
            </Illustrative>
          </div>
        </div>
      </Container>
    </section>
  );
}
