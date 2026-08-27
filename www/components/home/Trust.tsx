import { Container } from "@/components/layout/Container";
import { SectionLabel } from "@/components/layout/SectionLabel";
import { cn } from "@/lib/cn";
import { TrustBoundaryScene } from "@/components/home/visuals/TrustBoundaryScene";

const ITEMS = [
  {
    title: "Fail closed",
    description: "Unknown destinations, missing sanitization evidence, or failed cleanup cannot ship.",
    className: "w-[216px] max-xl:w-48",
  },
  {
    title: "Customer-hosted",
    description: "Production data stays inside the customer boundary. The control plane never needs a copy.",
    className: "w-72 max-xl:w-64",
  },
];

export function Trust() {
  return (
    <section
      id="trust"
      className="relative overflow-hidden bg-[#E4F1EB] pt-40 pb-[168px] text-black safe-paddings max-xl:py-[136px] max-lg:py-[88px] max-md:py-14"
    >
      <Container className="z-10" size="1344">
        <div className="relative z-10 flex gap-16 max-xl:gap-[108px] max-lg:gap-8 max-md:gap-5 max-sm:flex-col max-sm:gap-20">
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
            <ul className="mt-[216px] flex gap-[92px] max-xl:mt-[136px] max-xl:gap-16 max-lg:gap-8 max-md:gap-5 max-sm:mt-9 max-sm:flex-col max-sm:gap-7">
              {ITEMS.map((item) => (
                <li className={cn(item.className, "max-lg:w-40 max-sm:w-[220px]")} key={item.title}>
                  <div className="mb-5 size-8 rounded-full bg-black max-xl:mb-4 max-lg:mb-3.5 max-lg:size-7 max-md:size-6" />
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
              "flex w-[480px] flex-col justify-between border-l border-gray-new-50 px-8",
              "max-xl:w-[340px] max-xl:pr-0 max-xl:pl-5 max-lg:w-64 max-lg:pl-[18px] max-sm:w-full max-sm:border-none max-sm:pl-0",
            )}
          >
            <SectionLabel className="max-md:mb-4">What we will not claim</SectionLabel>
            <div>
              <blockquote className="font-mono text-[15px] leading-7 text-black/70">
                Zero rollback. No deployment can ever fail. Thousands of AI agents behave exactly like
                humans. One click perfectly clones every cloud. Use measurable evidence instead.
              </blockquote>
              <div className="mt-8 text-[13px] tracking-extra-tight text-gray-new-40">
                Product brief, section 18
              </div>
              <div className="mt-8 overflow-hidden rounded-[12px] bg-[#f4f7f5] ring-1 ring-black/10 max-xl:mt-6 max-md:mt-5">
                <TrustBoundaryScene />
              </div>
            </div>
          </div>
        </div>
      </Container>
    </section>
  );
}
