import { Container } from "@/components/layout/Container";
import { Heading } from "@/components/layout/Heading";
import { MigrationBento } from "@/components/home/visuals/MigrationBento";

export function Migrations() {
  return (
    <section
      className="relative scroll-mt-16 pt-32 pb-12 safe-paddings max-xl:pt-[88px] max-xl:pb-10 max-lg:scroll-mt-0 max-lg:pt-20 max-lg:pb-8 max-md:pt-16 max-md:pb-8"
      id="migrations"
    >
      <Container
        className="relative grid grid-cols-[224px_1fr] items-start gap-x-32 before:block max-xl:grid-cols-1 max-xl:px-16 max-xl:before:hidden max-lg:px-16 max-md:px-5"
        size="1600"
      >
        <div className="min-w-0">
          <Heading title="<strong>Migration Safety.</strong> Catch exclusive locks before they take checkout down. Locks, plans, and rollback feasibility before it ships." />
          <div className="relative mt-14 min-w-0 max-xl:mt-12 max-lg:mt-10 max-md:mt-8 max-sm:mt-11">
            <MigrationBento />
          </div>
        </div>
      </Container>
    </section>
  );
}
