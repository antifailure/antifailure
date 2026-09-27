import { Container } from "@/components/layout/Container";
import { SectionLabel } from "@/components/layout/SectionLabel";

const ITEMS = [
  { title: "Mask data in your infrastructure", body: "Replace customer details and credentials before a test environment uses the database. A signed attestation records the masking checks." },
  { title: "Control external calls", body: "Choose sandboxes, mocks, or captured messages for each integration. The gateway blocks destinations you have not configured." },
  { title: "Track the environment to teardown", body: "Every created resource is recorded in a journal. Cleanup uses that record to remove the environment and report the outcome." },
];

export function Trust() {
  return (
    <section id="trust" className="border-y border-stroke bg-sage py-24 safe-paddings max-md:py-14">
      <Container size="1344">
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)] gap-20 max-lg:grid-cols-1 max-lg:gap-10">
          <div>
            <SectionLabel>Built for your infrastructure</SectionLabel>
            <h2 className="mt-6 max-w-[15ch] text-[48px] leading-dense tracking-tighter text-gray-new-10 max-md:text-[34px]">
              Realistic tests.<br />A clear boundary.
            </h2>
            <p className="mt-6 max-w-[42ch] text-[18px] leading-8 text-gray-new-20">
              Keep production data in your cloud while your team reviews results in the control plane.
            </p>
            <a href="/docs/security/data-boundary" className="mt-8 inline-flex min-h-11 items-center text-base font-medium text-gray-new-10 underline decoration-black/25 underline-offset-4 hover:decoration-black focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-black">
              Explore the data boundary →
            </a>
          </div>
          <ul className="divide-y divide-black/15 border-y border-black/15">
            {ITEMS.map((item, index) => (
              <li key={item.title} className="grid grid-cols-[28px_minmax(0,1fr)] gap-5 py-7 max-md:gap-3">
                <span className="pt-1 font-mono text-xs text-gray-new-40">{String(index + 1).padStart(2, "0")}</span>
                <div>
                  <h3 className="text-[21px] leading-7 tracking-extra-tight text-gray-new-10">{item.title}</h3>
                  <p className="mt-3 text-base leading-7 text-gray-new-40">{item.body}</p>
                </div>
              </li>
            ))}
          </ul>
        </div>
      </Container>
    </section>
  );
}
