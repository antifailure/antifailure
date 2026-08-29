import { Container } from "@/components/layout/Container";
import { Heading } from "@/components/layout/Heading";
import { cn } from "@/lib/cn";
import { FeatureIcon } from "./media/icons";
import { ReportScene } from "./visuals/ReportScene";

const ITEMS = [
  {
    icon: "closed" as const,
    title: "Fail closed.",
    description: "Unknown egress, missing sanitization, or failed cleanup blocks the ship.",
  },
  {
    icon: "boundary" as const,
    title: "Customer boundary.",
    description: "Production data stays in the customer cloud. The twin never leaves that trust boundary.",
  },
  {
    icon: "report" as const,
    title: "Pass, warning, or block.",
    description: "The output is an evidence-backed gate on the pull request, not a preview URL alone.",
  },
  {
    icon: "cleanup" as const,
    title: "Cleanup is a safety property.",
    description: "Resources are journaled before create and compensated on teardown.",
  },
  {
    icon: "oracle" as const,
    title: "AI discovers, systems prove.",
    description: "AI discovers. Deterministic scenarios and the oracle decide.",
  },
  {
    icon: "postgres" as const,
    title: "Postgres wedge.",
    description: "Start with schema migrations on real volume, then expand the twin.",
  },
];

const BORDER = "absolute -bottom-1 -top-2 w-px bg-black/12 max-md:hidden";

export function Features() {
  return (
    <section
      className="relative scroll-mt-[60px] pb-60 safe-paddings max-xl:pb-40 max-lg:scroll-mt-0 max-lg:pb-32 max-md:pb-24"
      id="features"
    >
      <Container
        className="relative grid grid-cols-[224px_1fr] items-center gap-x-32 before:block max-xl:grid-cols-1 max-xl:px-16 max-xl:before:hidden max-lg:px-16 max-md:px-5"
        size="1600"
      >
        <div className="min-w-0 border-t border-black/12 pt-9 max-lg:pt-7">
          <Heading
            icon="features"
            title="<strong>Safety properties, not slogans.</strong> The platform answers whether this deployment is safe to ship under the conditions that actually matter."
          />
          <ReportScene />
          <div className="relative mt-20 max-xl:mt-16 max-lg:mt-14 max-lg:max-w-[800px] max-md:mt-16">
            <ul className="grid grid-cols-3 gap-x-16 gap-y-[72px] max-xl:gap-y-10 max-lg:grid-cols-2 max-lg:gap-x-16 max-lg:gap-y-11 max-md:grid-cols-1 max-md:gap-y-7">
              {ITEMS.map((item) => (
                <li className="flex flex-col gap-y-[18px] max-lg:gap-y-4 max-md:gap-y-3" key={item.title}>
                  <FeatureIcon name={item.icon} />
                  <p
                    className={cn(
                      "max-w-[320px] text-[18px] leading-normal tracking-extra-tight text-pretty text-gray-new-50",
                      "max-xl:w-[256px] max-lg:w-[288px] max-lg:text-base max-lg:leading-snug max-md:w-full max-md:max-w-full max-md:text-[15px]",
                    )}
                  >
                    <span className="text-black">{item.title}</span> {item.description}
                  </p>
                </li>
              ))}
            </ul>
            <span className={cn(BORDER, "-left-8 max-xl:-left-6 max-lg:-left-5")} />
            <span className={cn(BORDER, "left-[calc((100%-128px)/3+32px)] max-xl:left-[calc((100%-128px)/3+38px)] max-lg:left-[calc(100%/2+12px)]")} />
            <span className={cn(BORDER, "right-[calc((100%-128px)/3+32px)] max-xl:right-[calc((100%-128px)/3+24px)] max-lg:hidden")} />
          </div>
        </div>
      </Container>
    </section>
  );
}
