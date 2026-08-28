import { Container } from "@/components/layout/Container";
import { Heading } from "@/components/layout/Heading";
import { WorkloadIdeStage } from "./visuals/WorkloadIdeStage";

export function Workload() {
  return (
    <section
      className="relative scroll-mt-[60px] overflow-hidden pt-40 pb-32 safe-paddings max-xl:pt-[137px] max-xl:pb-24 max-lg:scroll-mt-0 max-lg:pt-[120px] max-lg:pb-20 max-md:pt-24 max-md:pb-16"
      id="workload"
    >
      <Container
        className="relative grid grid-cols-[224px_1fr] items-start gap-x-32 before:block max-xl:grid-cols-1 max-xl:px-16 max-xl:before:hidden max-lg:px-16 max-md:px-5"
        size="1600"
      >
        <div className="min-w-0">
          <Heading title="<strong>Workload Studio for the change that matters.</strong> Observed patterns, deterministic journeys, and exploratory users. Not production traffic diverted." />
          <div className="relative mt-14 max-xl:mt-12 max-xl:-mr-8 max-lg:-mx-8 max-lg:mt-10 max-lg:px-0 max-md:mx-0 max-sm:mt-11">
            <WorkloadIdeStage />
          </div>
        </div>
      </Container>
    </section>
  );
}
