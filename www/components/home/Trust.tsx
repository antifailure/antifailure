"use client";

import { CmsSection, CmsText } from "@/components/cms/Editable";
import { useCmsCollection, useCmsString } from "@/components/cms/CmsProvider";
import { Container } from "@/components/layout/Container";
import { SectionLabel } from "@/components/layout/SectionLabel";

const ITEMS = [
  { id: "masking", title: "Mask data in your infrastructure", body: "Replace customer details and credentials before a test environment uses the database. A signed attestation records the masking checks." },
  { id: "external-calls", title: "Control external calls", body: "Choose sandboxes, mocks, or captured messages for each integration. The gateway blocks destinations you have not configured." },
  { id: "teardown", title: "Track the environment to teardown", body: "Every created resource is recorded in a journal. Cleanup uses that record to remove the environment and report the outcome." },
];

export function Trust() {
  const items = useCmsCollection("trust.items", "Infrastructure details", "trust", ITEMS);
  const href = useCmsString("trust.link.href", "/docs/security/data-boundary", { label: "Data boundary destination", sectionId: "trust", kind: "url" });
  return (
    <CmsSection sectionId="trust" label="Infrastructure and trust" group="page" id="trust" className="border-y border-stroke bg-sage py-24 safe-paddings max-md:py-14">
      <Container size="1344">
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)] gap-20 max-lg:grid-cols-1 max-lg:gap-10">
          <div>
            <SectionLabel><CmsText cmsKey="trust.eyebrow" label="Trust label" sectionId="trust" defaultValue="Built for your infrastructure" /></SectionLabel>
            <CmsText as="h2" cmsKey="trust.heading" label="Trust headline" sectionId="trust" defaultValue="Realistic tests. A clear boundary." className="mt-6 max-w-[15ch] text-[48px] leading-dense tracking-tighter text-gray-new-10 max-md:text-[34px]">
              Realistic tests.<br />A clear boundary.
            </CmsText>
            <CmsText as="p" cmsKey="trust.description" label="Trust description" sectionId="trust" defaultValue="Keep production data in your cloud while your team reviews results in the control plane." className="mt-6 max-w-[42ch] text-[18px] leading-8 text-gray-new-20">
              Keep production data in your cloud while your team reviews results in the control plane.
            </CmsText>
            <a href={href} className="mt-8 inline-flex min-h-11 items-center text-base font-medium text-gray-new-10 underline decoration-black/25 underline-offset-4 hover:decoration-black focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-black">
              <CmsText cmsKey="trust.link.label" label="Data boundary link" sectionId="trust" defaultValue="Explore the data boundary →" />
            </a>
          </div>
          <ul className="divide-y divide-black/15 border-y border-black/15">
            {items.map((item, index) => (
              <li key={item.id} className="grid grid-cols-[28px_minmax(0,1fr)] gap-5 py-7 max-md:gap-3">
                <CmsText as="span" cmsKey={`trust.items.${item.id}.number`} label={`${item.title} list number`} sectionId="trust" defaultValue={String(index + 1).padStart(2, "0")} className="pt-1 font-mono text-xs text-gray-new-40" />
                <div>
                  <CmsText as="h3" cmsKey={`trust.items.${item.id}.title`} label="Detail title" sectionId="trust" defaultValue={ITEMS.find((source) => source.id === item.id)?.title ?? item.title ?? ""} className="text-[21px] leading-7 tracking-extra-tight text-gray-new-10" />
                  <CmsText as="p" cmsKey={`trust.items.${item.id}.body`} label="Detail description" sectionId="trust" defaultValue={ITEMS.find((source) => source.id === item.id)?.body ?? item.body ?? ""} className="mt-3 text-base leading-7 text-gray-new-40" />
                </div>
              </li>
            ))}
          </ul>
        </div>
      </Container>
    </CmsSection>
  );
}
