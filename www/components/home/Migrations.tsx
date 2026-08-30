import { Container } from "@/components/layout/Container";
import { Heading } from "@/components/layout/Heading";
import { Illustrative } from "@/components/layout/Illustrative";
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
          <Heading
            icon="migrations"
            label="Migration Safety"
            title="<strong>Migration Safety.</strong> Catch exclusive locks before they take checkout down. The strongest lock held per table, what queued behind it, and how the plans moved."
          />
          <div className="relative mt-14 min-w-0 max-xl:mt-12 max-lg:mt-10 max-md:mt-8 max-sm:mt-11">
            <MigrationBento />
            <Illustrative label="Example finding" className="mt-6">
              One migration rehearsed, with the numbers chosen. What is measured is the strongest
              lock mode and its hold time, what queued behind it, whether the table was rewritten,
              and the query plans before and after.
            </Illustrative>
          </div>
        </div>
      </Container>
    </section>
  );
}
