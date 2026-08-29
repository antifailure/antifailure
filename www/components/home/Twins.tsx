import { Container } from "@/components/layout/Container";
import { Heading } from "@/components/layout/Heading";
import { TwinIdeStage } from "./visuals/TwinIdeStage";

const FEATURES = [
  {
    title: "Isolated networking",
    description: "Clone-local DNS, no default public egress, TTL and budget on every resource.",
    icon: (
      <svg viewBox="0 0 20 20" className="size-[18px]" fill="none" aria-hidden>
        <circle cx="10" cy="10" r="6.5" stroke="currentColor" strokeWidth="1.4" />
        <path d="M10 3.5v13M3.5 10h13" stroke="currentColor" strokeWidth="1.4" />
        <ellipse cx="10" cy="10" rx="3.2" ry="6.5" stroke="currentColor" strokeWidth="1.4" />
      </svg>
    ),
  },
  {
    title: "Safe credentials",
    description: "Production secrets are replaced. The twin cannot reach live keys.",
    icon: (
      <svg viewBox="0 0 20 20" className="size-[18px]" fill="none" aria-hidden>
        <circle cx="7.2" cy="10" r="3.1" stroke="currentColor" strokeWidth="1.4" />
        <path d="M10.2 10h6.3M14.2 10v2.4M16.5 10v2.4" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      </svg>
    ),
  },
  {
    title: "Cleanup proof",
    description: "Every resource is journaled, destroyed, and attested. Nothing outlives the run.",
    icon: (
      <svg viewBox="0 0 20 20" className="size-[18px]" fill="none" aria-hidden>
        {Array.from({ length: 9 }, (_, i) => (
          <circle
            key={i}
            cx={5 + (i % 3) * 5}
            cy={5 + Math.floor(i / 3) * 5}
            r="1.15"
            fill="currentColor"
          />
        ))}
      </svg>
    ),
  },
];

export function Twins() {
  return (
    <section
      className="relative scroll-mt-[60px] overflow-hidden pt-8 pb-4 safe-paddings max-xl:pt-6 max-xl:pb-4 max-lg:scroll-mt-0 max-lg:pt-5 max-lg:pb-3 max-md:pt-4 max-md:pb-2"
      id="twins"
    >
      <Container
        className="relative grid grid-cols-[224px_1fr] gap-x-32 before:block max-xl:grid-cols-1 max-xl:px-16 max-xl:before:hidden max-lg:px-16 max-md:px-5"
        size="1600"
      >
        <div className="min-w-0">
          <Heading
            icon="twins"
            title="<strong>A disposable production twin.</strong> Build the candidate, restore safe state, contain side effects, and destroy everything when the report is done."
          />
          <div className="relative mt-14 min-w-0 max-xl:mt-12 max-lg:mt-10 max-md:mt-8 max-sm:mt-11">
            <TwinIdeStage />
          </div>
          <ul className="mt-10 grid grid-cols-3 gap-x-16 max-xl:mt-8 max-xl:grid-cols-1 max-xl:gap-y-7 max-lg:mt-10">
            {FEATURES.map((item) => (
              <li key={item.title}>
                <div className="text-black">{item.icon}</div>
                <h3 className="mt-3 text-base font-medium tracking-tight text-black max-lg:mt-2.5 max-lg:text-[14px]">
                  {item.title}
                </h3>
                <p className="mt-2 text-base tracking-tight text-gray-new-50 max-lg:text-[14px] max-md:mt-1.5">
                  {item.description}
                </p>
              </li>
            ))}
          </ul>
        </div>
      </Container>
    </section>
  );
}
