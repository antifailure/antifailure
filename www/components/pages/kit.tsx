import type { ReactNode } from "react";
import Link from "next/link";
import { Button } from "@/components/layout/Button";
import { Container } from "@/components/layout/Container";
import { SiteLayout } from "@/components/layout/SiteLayout";
import { SectionLabel } from "@/components/layout/SectionLabel";
import { Cta } from "@/components/home/Cta";
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
}: {
  eyebrow: string;
  title: string;
  lead: string;
  visual?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <section className="pt-28 pb-20 safe-paddings max-lg:pt-16 max-md:pt-12 max-md:pb-12">
      <Container size="1600">
        <div
          className={cn(
            visual && "grid grid-cols-2 items-end gap-x-16 max-xl:gap-x-10 max-lg:grid-cols-1 max-lg:gap-y-10",
          )}
        >
          <div className="min-w-0">
            <SectionLabel>{eyebrow}</SectionLabel>
            <h1 className="mt-5 max-w-4xl font-title text-[64px] font-medium leading-none tracking-extra-tight max-xl:text-[52px] max-lg:text-[44px] max-md:text-[34px]">
              {title}
            </h1>
            <p className="mt-8 max-w-[640px] text-[20px] leading-snug tracking-extra-tight text-gray-new-40 max-md:text-[17px]">
              {lead}
            </p>
            <div className="mt-8 flex gap-x-5 max-sm:flex-col max-sm:gap-y-3">
              {actions ?? (
                <>
                  <Button href="/signup">Get started</Button>
                  <Button href="/docs" theme="outlined">
                    Read the docs
                  </Button>
                </>
              )}
            </div>
          </div>
          {visual ? <div className="min-w-0 max-lg:mt-2">{visual}</div> : null}
        </div>
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
        tone === "white" && "bg-white py-28 max-xl:py-20 max-md:py-14",
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
    <div className={cn("max-w-[920px]", wide && "max-w-none")}>
      {kicker ? <SectionLabel className="mb-6">{kicker}</SectionLabel> : null}
      <h2
        className="text-[44px] leading-dense tracking-tighter text-gray-new-40 max-xl:text-[36px] max-lg:text-[28px] max-md:text-[24px] [&>strong]:font-normal [&>strong]:text-black"
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
    <ul className="relative mt-16 grid grid-cols-3 gap-x-16 gap-y-14 max-lg:grid-cols-2 max-md:mt-10 max-md:grid-cols-1 max-md:gap-y-8">
      {items.map((item) => (
        <li key={item.title} className="min-w-0">
          <div className="mb-3 size-2 rounded-full bg-black" />
          <h3 className="text-[18px] leading-snug tracking-extra-tight text-black">{item.title}</h3>
          <p className="mt-2 max-w-[320px] text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
            {item.body}
          </p>
        </li>
      ))}
    </ul>
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
        "relative overflow-hidden rounded-[12px] bg-[#f4f7f5] ring-1 ring-black/10",
        className,
      )}
    >
      {children}
    </div>
  );
}

export function SpecTable({ rows }: { rows: [string, string][] }) {
  return (
    <div className="overflow-hidden rounded-[12px] ring-1 ring-black/10">
      <table className="w-full text-left">
        <tbody>
          {rows.map(([k, v]) => (
            <tr key={k} className="border-b border-black/8 last:border-0">
              <th className="w-[38%] bg-black/[0.02] px-5 py-4 text-[14px] font-medium tracking-extra-tight text-black">
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
    <div className="overflow-hidden rounded-[12px] bg-[#151617] text-white ring-1 ring-black/20">
      {label ? (
        <div className="border-b border-white/10 px-5 py-2.5 font-mono text-[11px] tracking-extra-tight text-white/45">
          {label}
        </div>
      ) : null}
      <pre className="overflow-x-auto p-5 font-mono text-[13px] leading-6 text-white/85">{children}</pre>
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
    <div
      className={cn(
        "border-l-2 px-5 py-4 text-[16px] leading-7 tracking-extra-tight",
        tone === "green" && "border-[#33bf00] bg-[#33bf00]/10 text-black/80",
        tone === "block" && "border-red-600 bg-red-50 text-black/80",
        tone === "warn" && "border-amber-600 bg-amber-50 text-black/80",
      )}
    >
      <div
        className={cn(
          "mb-1 font-mono text-[11px] font-medium uppercase tracking-[0.14em]",
          tone === "green" && "text-[#1f7a3a]",
          tone === "block" && "text-red-700",
          tone === "warn" && "text-amber-800",
        )}
      >
        {label}
      </div>
      {children}
    </div>
  );
}

export function Steps({ items }: { items: { title: string; body: string }[] }) {
  return (
    <ol className="grid grid-cols-4 gap-8 max-xl:grid-cols-2 max-md:grid-cols-1">
      {items.map((item, i) => (
        <li key={item.title} className="relative min-w-0">
          <div className="font-mono text-[12px] tracking-extra-tight text-[#33bf00]">{String(i + 1).padStart(2, "0")}</div>
          <h3 className="mt-3 text-[18px] tracking-extra-tight text-black">{item.title}</h3>
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
      <div className="mb-8 font-mono text-[10px] font-medium uppercase tracking-snug text-gray-new-50">
        Keep reading
      </div>
      <ul className="grid grid-cols-3 gap-5 max-lg:grid-cols-2 max-md:grid-cols-1">
        {items.map((item) => (
          <li key={item.href}>
            <Link
              href={item.href}
              className="block h-full rounded-[12px] bg-white p-6 ring-1 ring-black/10 transition-colors hover:ring-black/25"
            >
              <span className="block text-[18px] tracking-extra-tight text-black">{item.title}</span>
              <span className="mt-2 block text-[14px] leading-6 tracking-extra-tight text-gray-new-40">
                {item.description}
              </span>
              <span className="mt-5 inline-block text-[13px] text-black/70">Read →</span>
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
