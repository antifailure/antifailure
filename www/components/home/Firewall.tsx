import { Container } from "@/components/layout/Container";
import { Heading } from "@/components/layout/Heading";
import { FailClosedScene } from "./visuals/FailClosedScene";

export function Firewall() {
  return (
    <section
      className="relative scroll-mt-[60px] pt-10 pb-10 max-xl:pt-8 max-xl:pb-8 max-lg:pt-7 max-lg:pb-7 max-md:pt-6 max-md:pb-6 safe-paddings max-lg:scroll-mt-0"
      id="firewall"
    >
      <Container
        className="relative grid grid-cols-[224px_1fr] items-center gap-x-32 before:block max-xl:grid-cols-1 max-xl:px-16 max-xl:before:hidden max-lg:px-16 max-md:px-5"
        size="1600"
      >
        <div className="min-w-0 border-t border-black/12 pt-9 max-lg:pt-7">
          <Heading
            icon="firewall"
            label="Side-Effect Firewall"
            title="<strong>Fail closed on side effects.</strong> The twin cannot charge cards, email users, or invoke production webhooks. Unknown destinations are blocked."
          />
          <div className="mt-8 max-xl:mt-6 max-lg:mt-5">
            <FailClosedScene />
          </div>
        </div>
      </Container>
    </section>
  );
}
