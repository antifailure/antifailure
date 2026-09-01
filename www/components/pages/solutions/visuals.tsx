import type { ReactNode } from "react";
import Link from "next/link";
import { cn } from "@/lib/cn";
import { SectionLabel } from "@/components/layout/SectionLabel";

/** Shared gap after a section heading. One value, every page. */
export const AFTER_HEADING = "mt-14 max-md:mt-10";

export function SectionHeading({
  kicker,
  title,
}: {
  kicker?: string;
  title: string;
}) {
  return (
    <div className="max-w-[960px] text-left">
      {kicker ? <SectionLabel className="mb-8 max-lg:mb-6">{kicker}</SectionLabel> : null}
      <h2
        className="text-[48px] font-normal leading-dense tracking-tighter text-pretty text-gray-new-40 max-xl:text-[40px] max-lg:text-[28px] max-md:text-[24px] [&>strong]:font-normal [&>strong]:text-black"
        dangerouslySetInnerHTML={{ __html: title }}
      />
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
        "grid grid-cols-2 items-start gap-x-16 gap-y-12 text-left max-xl:grid-cols-1",
        reverse && "[&>*:first-child]:max-xl:order-2",
      )}
    >
      <div className={cn("min-w-0", reverse && "xl:order-2")}>{children}</div>
      <div className="min-w-0 max-w-[560px]">{visual}</div>
    </div>
  );
}

export function Metrics({
  items,
}: {
  items: { value: string; label: string }[];
}) {
  return (
    <ul className="grid grid-cols-3 gap-x-16 gap-y-10 text-left max-xl:grid-cols-1">
      {items.map((item) => (
        <li key={item.label} className="min-w-0 border-t border-black/12 pt-6">
          <div className="font-title text-[52px] leading-none tracking-tighter text-black max-xl:text-[40px] max-md:text-[36px]">
            {item.value}
          </div>
          <p className="mt-4 max-w-[280px] text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
            {item.label}
          </p>
        </li>
      ))}
    </ul>
  );
}

export function Note({
  label,
  children,
  tone = "plain",
}: {
  label: string;
  children: ReactNode;
  tone?: "plain" | "block" | "warn";
}) {
  return (
    <div className="border-t border-black/12 pt-6 text-left">
      <div
        className={cn(
          "font-mono text-[11px] font-medium uppercase tracking-snug",
          tone === "plain" && "text-black",
          tone === "block" && "text-[#C43D3D]",
          tone === "warn" && "text-[#8A6A12]",
        )}
      >
        {label}
      </div>
      <p className="mt-3 max-w-[640px] text-[16px] leading-7 tracking-extra-tight text-gray-new-40">
        {children}
      </p>
    </div>
  );
}

export function Lead({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <p
      className={cn(
        "mt-6 max-w-[640px] text-left text-[17px] leading-7 tracking-extra-tight text-gray-new-40",
        className,
      )}
    >
      {children}
    </p>
  );
}

export function FeatureList({ items }: { items: { title: string; body: string }[] }) {
  return (
    <ul className="grid grid-cols-3 gap-x-16 gap-y-14 text-left max-xl:grid-cols-2 max-xl:gap-x-10 max-md:grid-cols-1 max-md:gap-y-8">
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
  );
}

export function OpenSteps({ items }: { items: { title: string; body: string }[] }) {
  return (
    <ol className="grid grid-cols-4 gap-x-16 gap-y-10 text-left max-xl:grid-cols-2 max-md:grid-cols-1">
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

export function DirectoryList({
  items,
}: {
  items: { href: string; title: string; body: string; metric?: string }[];
}) {
  return (
    <ul className="divide-y divide-black/12 border-y border-black/12 text-left">
      {items.map((item) => (
        <li key={item.href}>
          <Link href={item.href} className="group block py-8 max-md:py-6">
            {item.metric ? (
              <span className="font-mono text-[11px] tracking-extra-tight text-black/40">{item.metric}</span>
            ) : null}
            <span
              className={cn(
                "block text-[22px] leading-tight tracking-extra-tight text-black max-md:text-[18px]",
                item.metric && "mt-2",
              )}
            >
              {item.title}
            </span>
            <p className="mt-2 max-w-[560px] text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
              {item.body}
            </p>
            <span className="mt-4 inline-block text-[13px] tracking-extra-tight text-black/50 transition-colors group-hover:text-black">
              Open
            </span>
          </Link>
        </li>
      ))}
    </ul>
  );
}
