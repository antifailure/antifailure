import { Container } from "@/components/layout/Container";
import { Heading } from "@/components/layout/Heading";
import { MigrationEvidence } from "@/components/home/visuals/MigrationEvidence";

export function Migrations() {
  return (
    <section
      className="relative scroll-mt-16 pt-10 pb-10 max-xl:pt-8 max-xl:pb-8 max-lg:pt-7 max-lg:pb-7 max-md:pt-6 max-md:pb-6 safe-paddings max-lg:scroll-mt-0"
      id="migrations"
    >
      <Container
        className="relative grid grid-cols-[224px_1fr] items-start gap-x-32 before:block max-xl:grid-cols-1 max-xl:px-16 max-xl:before:hidden max-lg:px-16 max-md:px-5"
        size="1600"
      >
        <div className="min-w-0">
          <Heading
            icon="migrations"
            label="Migration Safety"
            title="<strong>Catch the migration that stalls your app.</strong> Your agent can rehearse it before you deploy."
          />
          <div className="relative mt-14 min-w-0 max-xl:mt-12 max-lg:mt-10 max-md:mt-8 max-sm:mt-11">
            <MigrationEvidence />
          </div>
        </div>
      </Container>
    </section>
  );
}
