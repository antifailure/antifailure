import type { ReactNode } from "react";
import Link from "next/link";
import { Button } from "@/components/layout/Button";
import { Container } from "@/components/layout/Container";
import { SiteLayout } from "@/components/layout/SiteLayout";
import { SectionLabel } from "@/components/layout/SectionLabel";
import { Cta } from "@/components/home/Cta";
import { Breadcrumbs } from "@/components/layout/Breadcrumbs";
import { FaqJsonLd, PageJsonLd } from "@/lib/jsonld";
import { cn } from "@/lib/cn";

export function PageShell({ children }: { children: ReactNode }) {
  return (
    <SiteLayout overlay={false}>
      {children}
      <Cta />
    </SiteLayout>
  );
}

export function PageHero({
  eyebrow,
  title,
  lead,
  visual,
  actions,
  framed = true,
  path,
}: {
  eyebrow: string;
  title: string;
  lead: string;
  visual?: ReactNode;
  actions?: ReactNode;
  framed?: boolean;
  /**
   * The route this page is served at, e.g. "/product/twins".
   *
   * One prop, in the one component every page renders exactly once, that
   * produces three things: the visible breadcrumb trail, the WebPage node and
   * the BreadcrumbList node. Optional so an unfinished page still renders, but
   * `npm run check:seo` fails on any published route that omits it.
   */
  path?: string;
}) {
  return (
    <section className="pt-28 pb-16 safe-paddings max-lg:pt-16 max-md:pt-12 max-md:pb-10">
      <Container size="1600">
        {path ? <PageJsonLd path={path} /> : null}
        {path ? <Breadcrumbs path={path} /> : null}
        <SectionLabel>{eyebrow}</SectionLabel>
        <h1 className="mt-5 max-w-[1100px] text-[64px] leading-dense tracking-tighter max-xl:max-w-[920px] max-xl:text-[52px] max-lg:text-[44px] max-md:text-[34px] max-sm:text-[32px]">
          {title}
        </h1>
        <p className="mt-8 max-w-[640px] text-[20px] leading-snug tracking-extra-tight text-gray-new-40 max-md:text-[17px]">
          {lead}
        </p>
        <div className="mt-8 flex gap-x-5 max-lg:mt-7 max-sm:flex-col max-sm:gap-y-3">
          {actions ?? (
            <>
              <Button href="/signup" theme="filled">
                Get started
              </Button>
              <Button href="/docs" theme="outlined">
                Read the docs
              </Button>
            </>
          )}
        </div>
        {visual ? (
          framed ? (
            <div className="relative mt-16 max-md:mt-12">
              <div className="overflow-hidden rounded-[12px] border border-black/[0.08] bg-white">
                {visual}
              </div>
              <div
                className="pointer-events-none absolute inset-0 rounded-[12px]"
                style={{
                  background:
                    "linear-gradient(to right, transparent 72%, #f7f7f5 100%), linear-gradient(to bottom, transparent 78%, #f7f7f5 100%)",
                }}
                aria-hidden
              />
            </div>
          ) : (
            <div className="mt-16 max-md:mt-12">{visual}</div>
          )
        ) : null}
      </Container>
    </section>
  );
}

export function PageSection({
  children,
  className,
  tone = "plain",
}: {
  children: ReactNode;
  className?: string;
  tone?: "plain" | "sage" | "white";
}) {
  return (
    <section
      className={cn(
        "safe-paddings",
        tone === "plain" && "py-28 max-xl:py-20 max-md:py-14",
        tone === "sage" && "bg-[#E4F1EB] py-32 max-xl:py-24 max-md:py-16",
        tone === "white" && "border-t border-black/12 py-28 max-xl:py-20 max-md:py-14",
        className,
      )}
    >
      <Container size="1600">{children}</Container>
    </section>
  );
}

export function PageHeading({
  kicker,
  title,
  wide,
}: {
  kicker?: string;
  title: string;
  wide?: boolean;
}) {
  return (
    <div className={cn("max-w-[960px]", wide && "max-w-none")}>
      {kicker ? <SectionLabel className="mb-8 max-lg:mb-6">{kicker}</SectionLabel> : null}
      <h2
        className="indent-24 text-[48px] font-normal leading-dense tracking-tighter text-pretty text-gray-new-40 max-xl:text-[40px] max-lg:indent-16 max-lg:text-[28px] max-md:indent-0 max-md:text-[24px] [&>strong]:font-normal [&>strong]:text-black"
        dangerouslySetInnerHTML={{ __html: title }}
      />
    </div>
  );
}

export function FeatureGrid({
  items,
}: {
  items: { title: string; body: string }[];
}) {
  return (
    <div className="relative mt-16 max-md:mt-10">
      <ul className="grid grid-cols-3 gap-x-16 gap-y-14 max-lg:grid-cols-2 max-md:grid-cols-1 max-md:gap-y-8">
        {items.map((item) => (
          <li key={item.title} className="min-w-0">
            <svg viewBox="0 0 16 16" className="mb-4 size-4 text-black" fill="none" aria-hidden>
              <rect x="1.5" y="1.5" width="13" height="13" stroke="currentColor" strokeWidth="1.2" />
            </svg>
            <h3 className="text-[18px] leading-snug tracking-extra-tight text-black">{item.title}</h3>
            <p className="mt-2 max-w-[320px] text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
              {item.body}
            </p>
          </li>
        ))}
      </ul>
      <span className="pointer-events-none absolute inset-y-0 left-[calc(33.333%-32px)] w-px bg-black/12 max-lg:left-1/2 max-md:hidden" />
      <span className="pointer-events-none absolute inset-y-0 right-[calc(33.333%-32px)] w-px bg-black/12 max-lg:hidden" />
    </div>
  );
}

export function Split({
  children,
  visual,
  reverse,
}: {
  children: ReactNode;
  visual: ReactNode;
  reverse?: boolean;
}) {
  return (
    <div
      className={cn(
        "grid grid-cols-2 items-center gap-x-16 gap-y-10 max-lg:grid-cols-1",
        reverse && "[&>*:first-child]:max-lg:order-2",
      )}
    >
      <div className={cn("min-w-0", reverse && "lg:order-2")}>{children}</div>
      <div className="min-w-0">{visual}</div>
    </div>
  );
}

export function Stage({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div
      className={cn(
        "relative overflow-hidden rounded-[12px] border border-black/[0.08] bg-white",
        className,
      )}
    >
      {children}
    </div>
  );
}

export function SpecTable({ rows }: { rows: [string, string][] }) {
  return (
    <div className="overflow-hidden rounded-[12px] border border-black/[0.08] bg-white">
      <table className="w-full text-left">
        <tbody>
          {rows.map(([k, v]) => (
            <tr key={k} className="border-b border-black/[0.08] last:border-0">
              <th className="w-[38%] px-5 py-4 text-[14px] font-medium tracking-extra-tight text-black">
                {k}
              </th>
              <td className="px-5 py-4 text-[14px] leading-6 tracking-extra-tight text-gray-new-40">{v}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function CodePanel({ label, children }: { label?: string; children: string }) {
  return (
    <div className="overflow-hidden rounded-[12px] border border-black/[0.08] bg-white">
      {label ? (
        <div className="border-b border-black/[0.08] px-5 py-2.5 font-mono text-[11px] tracking-extra-tight text-[#6B6F76]">
          {label}
        </div>
      ) : null}
      <pre className="overflow-x-auto p-5 font-mono text-[13px] leading-6 tracking-extra-tight text-[#1A1A1A]">
        {children}
      </pre>
    </div>
  );
}

export function Callout({
  label,
  children,
  tone = "green",
}: {
  label: string;
  children: ReactNode;
  tone?: "green" | "block" | "warn";
}) {
  return (
    <div className="rounded-[12px] border border-black/[0.08] bg-white px-5 py-4 text-[16px] leading-7 tracking-extra-tight text-gray-new-40">
      <div
        className={cn(
          "mb-2 font-mono text-[11px] font-medium uppercase tracking-snug",
          tone === "green" && "text-[#1A1A1A]",
          tone === "block" && "text-[#C43D3D]",
          tone === "warn" && "text-[#8A6A12]",
        )}
      >
        {label}
      </div>
      <div className="text-black/80">{children}</div>
    </div>
  );
}

export function Steps({ items }: { items: { title: string; body: string }[] }) {
  return (
    <ol className="relative mt-12 grid grid-cols-4 gap-x-16 gap-y-10 max-xl:grid-cols-2 max-md:grid-cols-1">
      {items.map((item) => (
        <li key={item.title} className="min-w-0">
          <div className="mb-4 size-2 rounded-full bg-black" />
          <h3 className="text-[18px] leading-snug tracking-extra-tight text-black">{item.title}</h3>
          <p className="mt-2 text-[14px] leading-6 tracking-extra-tight text-gray-new-40">{item.body}</p>
        </li>
      ))}
    </ol>
  );
}

export function RelatedGrid({
  items,
}: {
  items: { href: string; title: string; description: string }[];
}) {
  return (
    <PageSection>
      <div className="mb-8">
        <SectionLabel>Keep reading</SectionLabel>
      </div>
      <ul className="grid grid-cols-3 gap-x-16 gap-y-10 max-lg:grid-cols-2 max-md:grid-cols-1">
        {items.map((item) => (
          <li key={item.href}>
            <Link href={item.href} className="group block min-w-0">
              <span className="block text-[18px] tracking-extra-tight text-black">{item.title}</span>
              <span className="mt-2 block text-[14px] leading-6 tracking-extra-tight text-gray-new-40">
                {item.description}
              </span>
              <span className="mt-4 inline-block text-[13px] tracking-extra-tight text-black/50 transition-colors group-hover:text-black">
                Read
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </PageSection>
  );
}

export function Prose({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div
      className={cn(
        "max-w-[720px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40 [&_a]:text-black [&_a]:underline [&_a]:decoration-black/20 [&_a]:underline-offset-4 [&_strong]:font-medium [&_strong]:text-black",
        className,
      )}
    >
      {children}
    </div>
  );
}

export type FaqItem = { question: string; answer: string };

/**
 * A frequently-asked-questions block, and the FAQPage markup that describes it.
 *
 * Both come from the same array, which is the point. Question-and-answer
 * structured data is the highest-value type for being quoted by an answer
 * engine, because each pair is an individually addressable candidate rather
 * than a paragraph somebody has to decide how to cut. It is also the type most
 * often marked up with text the reader cannot see, which gets it discarded.
 * Deriving the markup and the rendering from one source means they cannot
 * disagree.
 *
 * Rendered as a <dl>. A question and its answer are a term and a definition,
 * and that is the element that says so. It is not an accordion: an answer
 * hidden behind a click is an answer some crawlers never see, and there are
 * eight of these, not eighty.
 */
export function Faq({ path, items }: { path: string; items: FaqItem[] }) {
  return (
    <>
      <FaqJsonLd path={path} entries={items} />
      <dl className="mt-14 grid grid-cols-2 gap-x-12 gap-y-10 border-t border-black/12 pt-12 max-lg:grid-cols-1 max-lg:gap-y-8 max-md:mt-10 max-md:pt-8">
        {items.map((item) => (
          <div key={item.question}>
            <dt className="text-[19px] leading-snug tracking-extra-tight text-black max-md:text-[17px]">
              {item.question}
            </dt>
            <dd className="mt-3 text-[16px] leading-relaxed tracking-extra-tight text-gray-new-40 max-md:text-[15px]">
              {item.answer}
            </dd>
          </div>
        ))}
      </dl>
    </>
  );
}
