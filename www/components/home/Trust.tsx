import { Container } from "@/components/layout/Container";
import { SectionLabel } from "@/components/layout/SectionLabel";
import { TrustIcon } from "@/components/home/media/icons";
import { cn } from "@/lib/cn";

const ITEMS = [
  {
    icon: "failclosed" as const,
    title: "Fail closed",
    description: "Unknown destinations, missing sanitization evidence, or failed cleanup cannot ship.",
    className: "w-[216px] max-xl:w-48",
  },
  {
    icon: "hosted" as const,
    title: "Customer-hosted",
    description: "Production data stays inside the customer boundary. The control plane never needs a copy.",
    className: "w-72 max-xl:w-64",
  },
];

export function Trust() {
  return (
    <section
      id="trust"
      className="relative overflow-hidden bg-[#cadcc4] pt-40 pb-[168px] text-black safe-paddings max-xl:py-[136px] max-lg:py-[88px] max-md:py-14"
    >
      <Container className="z-10" size="1344">
        <div className="relative z-10 flex gap-16 max-xl:flex-col max-xl:gap-10">
          <div className="flex-1 border-l border-gray-new-50 px-8 max-xl:pr-0 max-xl:pl-6 max-lg:pl-[18px] max-sm:border-none max-sm:pl-0">
            <SectionLabel className="mb-5 max-md:mb-4">Trust model</SectionLabel>
            <h2
              className={cn(
                "text-[44px] leading-dense tracking-tighter text-gray-new-40",
                "max-xl:text-[36px] max-lg:text-2xl max-md:text-xl",
                "[&>strong]:font-normal [&>strong]:text-black",
              )}
            >
              <strong>Fail closed. Customer-hosted.</strong> Production data stays in your
              boundary. Cleanup is a first-class safety property, not a best-effort script.
            </h2>
            <ul className="mt-16 flex gap-[92px] max-xl:mt-12 max-xl:gap-10 max-md:flex-col max-md:gap-7">
              {ITEMS.map((item) => (
                <li className={cn(item.className, "max-lg:w-auto max-lg:max-w-[280px] max-md:max-w-none max-md:w-full")} key={item.title}>
                  <TrustIcon name={item.icon} className="mb-5 text-black max-xl:mb-4 max-lg:mb-3.5" />
                  <h3 className="text-4xl leading-dense tracking-tighter max-xl:text-[36px] max-lg:text-[28px] max-md:text-[24px]">
                    {item.title}
                  </h3>
                  <p className="mt-1.5 tracking-extra-tight text-gray-new-40 max-xl:text-sm max-xl:leading-snug max-lg:mt-1">
                    {item.description}
                  </p>
                </li>
              ))}
            </ul>
          </div>
          <div
            className={cn(
              "flex w-[480px] flex-col border-l border-gray-new-50 px-8",
              "max-xl:w-full max-xl:border-none max-xl:px-0",
            )}
          >
            <SectionLabel className="mb-5 max-md:mb-4">What we will not claim</SectionLabel>
            <blockquote className="font-mono text-[15px] leading-7 text-black/70">
              Zero rollback. No deployment can ever fail. Thousands of AI agents behave exactly like
              humans. One click perfectly clones every cloud. Use measurable evidence instead.
            </blockquote>
            <div className="mt-8 text-[13px] tracking-extra-tight text-gray-new-40">
              Product brief, section 18
            </div>
          </div>
        </div>
      </Container>
    </section>
  );
}
