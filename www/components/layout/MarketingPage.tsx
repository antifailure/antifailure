import type { ReactNode } from "react";
import Link from "next/link";
import { Button } from "@/components/layout/Button";
import { Container } from "@/components/layout/Container";
import { SiteLayout } from "@/components/layout/SiteLayout";
import { SectionLabel } from "@/components/layout/SectionLabel";
import { Cta } from "@/components/home/Cta";

export type PageFeature = { title: string; body: string };
export type PageRelated = { href: string; title: string; description: string };

export function MarketingPage({
  eyebrow,
  title,
  lead,
  features,
  children,
  related,
}: {
  eyebrow: string;
  title: string;
  lead: string;
  features?: PageFeature[];
  children?: ReactNode;
  related?: PageRelated[];
}) {
  return (
    <SiteLayout overlay={false}>
      <section className="pt-24 pb-16 safe-paddings max-lg:pt-16 max-md:pt-12">
        <Container size="1600">
          <SectionLabel>{eyebrow}</SectionLabel>
          <h1 className="mt-5 max-w-4xl font-title text-[64px] font-medium leading-none tracking-extra-tight max-xl:text-[56px] max-lg:text-5xl max-md:text-4xl">
            {title}
          </h1>
          <p className="mt-8 max-w-[720px] text-[20px] leading-snug tracking-extra-tight text-gray-new-40 max-md:text-[17px]">
            {lead}
          </p>
          <div className="mt-8 flex gap-x-5 max-sm:flex-col max-sm:gap-y-3 max-sm:[&_a]:w-full">
            <Button href="/signup">Get started</Button>
            <Button href="/docs" theme="outlined">
              Read the docs
            </Button>
          </div>
        </Container>
      </section>

      {features && features.length > 0 ? (
        <section className="pb-20 safe-paddings max-md:pb-12">
          <Container size="1600">
            <ul className="grid grid-cols-3 gap-x-12 gap-y-12 border-t border-black/12 pt-12 max-lg:grid-cols-2 max-md:grid-cols-1 max-md:gap-y-8">
              {features.map((item) => (
                <li key={item.title}>
                  <h2 className="text-[18px] leading-snug tracking-extra-tight text-black">{item.title}</h2>
                  <p className="mt-2 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">{item.body}</p>
                </li>
              ))}
            </ul>
          </Container>
        </section>
      ) : null}

      {children ? (
        <section className="pb-24 safe-paddings max-md:pb-16">
          <Container size="1600">
            <div className="max-w-[860px] text-[18px] leading-7 tracking-extra-tight text-gray-new-40 [&_a]:text-black [&_a]:underline [&_a]:decoration-black/20 [&_a]:underline-offset-4 [&>h2]:mt-14 [&>h2]:font-title [&>h2]:text-[32px] [&>h2]:font-medium [&>h2]:leading-none [&>h2]:tracking-tighter [&>h2]:text-black [&>h3]:mt-8 [&>h3]:text-[20px] [&>h3]:font-medium [&>h3]:tracking-extra-tight [&>h3]:text-black [&>p]:mt-5 [&>ul]:mt-5 [&>ul]:list-disc [&>ul]:space-y-2 [&>ul]:pl-5 [&>ol]:mt-5 [&>ol]:list-decimal [&>ol]:space-y-2 [&>ol]:pl-5">
              {children}
            </div>
          </Container>
        </section>
      ) : null}

      {related && related.length > 0 ? (
        <section className="pb-28 safe-paddings max-md:pb-16">
          <Container size="1600">
            <div className="mb-8 text-[10px] font-medium uppercase tracking-snug text-gray-new-50">Keep reading</div>
            <ul className="grid grid-cols-3 gap-6 max-lg:grid-cols-2 max-md:grid-cols-1">
              {related.map((item) => (
                <li key={item.href}>
                  <Link
                    href={item.href}
                    className="block h-full rounded-[10px] border border-gray-new-90 bg-white p-6 transition-colors hover:border-black/20"
                  >
                    <span className="block text-[18px] tracking-extra-tight text-black">{item.title}</span>
                    <span className="mt-2 block text-[14px] leading-6 tracking-extra-tight text-gray-new-40">
                      {item.description}
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          </Container>
        </section>
      ) : null}

      <Cta />
    </SiteLayout>
  );
}

export function ContentPage({
  content,
}: {
  content: {
    eyebrow: string;
    title: string;
    lead: string;
    features?: PageFeature[];
    related?: PageRelated[];
    body: ReactNode;
  };
}) {
  return (
    <MarketingPage
      eyebrow={content.eyebrow}
      title={content.title}
      lead={content.lead}
      features={content.features}
      related={content.related}
    >
      {content.body}
    </MarketingPage>
  );
}

export function PageTable({ headers, rows }: { headers: string[]; rows: string[][] }) {
  return (
    <div className="mt-6 overflow-x-auto rounded-[10px] border border-black/10">
      <table className="w-full text-left text-[14px]">
        <thead className="bg-black/[0.03] text-black/50">
          <tr>
            {headers.map((h) => (
              <th key={h} className="px-4 py-2.5 font-medium">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.join("|")} className="border-t border-black/8">
              {row.map((cell, i) => (
                <td key={`${row[0]}-${i}`} className="px-4 py-2.5 text-gray-new-40">
                  {cell}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function PagePre({ children }: { children: string }) {
  return (
    <pre className="mt-6 overflow-x-auto rounded-[10px] border border-black/10 bg-white p-5 font-mono text-[13px] leading-6 text-black/80">
      {children}
    </pre>
  );
}

export function PageCallout({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="mt-8 border-l-2 border-[#33bf00] bg-[#33bf00]/8 px-5 py-4 text-[16px] leading-7 text-black/80">
      <div className="mb-1 font-mono text-[11px] font-medium uppercase tracking-[0.14em] text-[#33bf00]">{label}</div>
      {children}
    </div>
  );
}
