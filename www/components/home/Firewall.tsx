import { Container } from "@/components/layout/Container";
import { Heading } from "@/components/layout/Heading";
import { FirewallScene } from "./visuals/FirewallScene";

export function Firewall() {
  return (
    <section
      className="relative scroll-mt-[60px] pb-32 safe-paddings max-xl:pb-24 max-lg:scroll-mt-0 max-lg:pb-20 max-md:pb-16"
      id="firewall"
    >
      <Container
        className="relative grid grid-cols-[224px_1fr] items-center gap-x-32 before:block max-xl:grid-cols-1 max-xl:px-16 max-xl:before:hidden max-lg:px-16 max-md:px-5"
        size="1600"
      >
        <div className="min-w-0 border-t border-black/12 pt-9 max-lg:pt-7">
          <Heading
            icon="firewall"
            title="<strong>Fail closed on side effects.</strong> The twin cannot charge cards, email users, or invoke production webhooks. Unknown destinations are blocked."
          />
          <div className="mt-8 max-xl:mt-6 max-lg:mt-5">
            <FirewallScene />
          </div>
        </div>
      </Container>
    </section>
  );
}
