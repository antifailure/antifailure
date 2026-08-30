import type { ReactNode } from "react";
import Link from "next/link";
import { cn } from "@/lib/cn";
import { SectionLabel } from "@/components/layout/SectionLabel";
import { CheckRow, MonoLabel, StatusPill } from "@/components/home/visuals/primitives";
import { CompactSwap } from "@/components/pages/kit";
import { LockChartMobile } from "@/components/home/media/LockChart";
import { MigrationScene } from "@/components/home/visuals/MigrationScene";

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
        "grid grid-cols-2 items-start gap-x-16 gap-y-10 text-left max-xl:grid-cols-1",
        reverse && "[&>*:first-child]:max-xl:order-2",
      )}
    >
      <div className={cn("min-w-0", reverse && "xl:order-2")}>{children}</div>
      <div className="min-w-0">{visual}</div>
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

export function SpecRows({ rows }: { rows: [string, string][] }) {
  return (
    <ul className="divide-y divide-black/12 border-y border-black/12 text-left">
      {rows.map(([k, v]) => (
        <li key={k} className="grid grid-cols-2 gap-x-8 py-4 max-sm:grid-cols-1 max-sm:gap-y-1">
          <span className="text-[14px] font-medium tracking-extra-tight text-black">{k}</span>
          <span className="text-[14px] leading-6 tracking-extra-tight text-gray-new-40">{v}</span>
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

export function Verdicts({
  items,
}: {
  items: { tone: "PASS" | "WARN" | "BLOCK"; title: string; body: string }[];
}) {
  return (
    <ul className="grid grid-cols-3 gap-x-16 gap-y-10 text-left max-xl:grid-cols-1">
      {items.map((item) => (
        <li key={item.tone} className="min-w-0 border-t border-black/12 pt-6">
          <StatusPill tone={item.tone} />
          <h3 className="mt-4 text-[18px] leading-snug tracking-extra-tight text-black">{item.title}</h3>
          <p className="mt-2 max-w-[320px] text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
            {item.body}
          </p>
        </li>
      ))}
    </ul>
  );
}

export function PolicyList({
  items,
}: {
  items: { tone: "PASS" | "WARN" | "BLOCK"; text: string }[];
}) {
  return (
    <ul className="divide-y divide-black/12 border-y border-black/12 text-left">
      {items.map((item) => (
        <li key={item.text} className="flex items-start gap-4 py-4">
          <StatusPill tone={item.tone} className="mt-1 shrink-0" />
          <span className="text-[16px] leading-7 tracking-extra-tight text-gray-new-40">{item.text}</span>
        </li>
      ))}
    </ul>
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

export function TenantSubsetScene() {
  return (
    <div className="bg-[#f4f7f5] px-8 py-8 text-left max-md:px-5 max-md:py-6" aria-hidden>
      <MonoLabel className="uppercase">Tenant subset</MonoLabel>
      <ul className="mt-6 divide-y divide-black/8 border-y border-black/8">
        {[
          ["acme-prod", "12.4k seats"],
          ["northwind", "3.1k seats"],
          ["helix", "890 seats"],
        ].map(([name, seats]) => (
          <li key={name} className="flex items-center gap-6 py-3">
            <CheckRow ok>{name}</CheckRow>
            <span className="font-mono text-[12px] tracking-extra-tight text-black/45">{seats}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

export function WorkersScene() {
  return (
    <div className="bg-[#f4f7f5] px-8 py-8 text-left max-md:px-5 max-md:py-6" aria-hidden>
      <MonoLabel className="uppercase">twin · marketplace workers</MonoLabel>
      <ul className="mt-6 divide-y divide-black/8 border-y border-black/8">
        {([
          ["matching.worker", "RUNNING", true as const],
          ["notify.worker", "RUNNING", true as const],
          ["settle.worker", "RUNNING", true as const],
          ["api.partners.test", "BLOCKED", false as const],
        ] as const).map(([name, status, ok]) => (
          <li key={name} className="flex items-center gap-6 py-3">
            <CheckRow ok={ok}>{name}</CheckRow>
            <span
              className={cn(
                "font-mono text-[11px] tracking-extra-tight",
                ok ? "text-black/45" : "text-red-700",
              )}
            >
              {status}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

export function MigrationHero({ tab }: { tab: 0 | 1 }) {
  return (
    <CompactSwap
      desktop={<MigrationScene tab={tab} playId={0} />}
      compact={<LockChartMobile state={tab} always />}
    />
  );
}
