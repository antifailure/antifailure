import { Container } from "@/components/layout/Container";
import { Heading } from "@/components/layout/Heading";
import { SafetyCards } from "./visuals/SafetyCards";

export function Features() {
  return (
    <section
      className="relative scroll-mt-[60px] pb-8 safe-paddings max-xl:pb-6 max-lg:scroll-mt-0 max-lg:pb-5 max-md:pb-4"
      id="features"
    >
      <Container
        className="relative grid grid-cols-[224px_1fr] items-center gap-x-32 before:block max-xl:grid-cols-1 max-xl:px-16 max-xl:before:hidden max-lg:px-16 max-md:px-5"
        size="1600"
      >
        <div className="min-w-0 border-t border-black/12 pt-9 max-lg:pt-7">
          <Heading
            icon="features"
            label="Safety properties"
            title="<strong>Safety properties, not slogans.</strong> The platform answers whether this deployment is safe to ship under the conditions that actually matter."
          />
          <SafetyCards className="mt-16 max-xl:mt-12 max-lg:mt-10 max-md:mt-8" />
        </div>
      </Container>
    </section>
  );
}
