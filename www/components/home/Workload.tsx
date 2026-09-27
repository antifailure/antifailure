import { Container } from "@/components/layout/Container";
import { Heading } from "@/components/layout/Heading";
import { Illustrative } from "@/components/layout/Illustrative";
import { WorkloadIdeStage } from "./visuals/WorkloadIdeStage";

export function Workload() {
  return (
    <section
      className="relative scroll-mt-[60px] overflow-hidden pt-10 pb-10 max-xl:pt-8 max-xl:pb-8 max-lg:pt-7 max-lg:pb-7 max-md:pt-6 max-md:pb-6 safe-paddings max-lg:scroll-mt-0"
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
            title="<strong>Test the routes your users rely on.</strong> Replay your production traffic mix against the new build."
          />
          <div className="relative mt-14 min-w-0 max-xl:mt-12 max-lg:mt-10 max-md:mt-8 max-sm:mt-11">
            <WorkloadIdeStage />
            <Illustrative className="mt-6">
              Example project using the Antifailure CLI.
            </Illustrative>
          </div>
        </div>
      </Container>
    </section>
  );
}
