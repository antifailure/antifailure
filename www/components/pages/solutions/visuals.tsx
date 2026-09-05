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

export function DirectoryList({
  items,
}: {
  items: { href: string; title: string; body: string; metric?: string }[];
}) {
  return (
    <ul className="divide-y divide-black/12 border-y border-black/12 text-left">
      {items.map((item) => (
        <li key={item.href}>
          <Link prefetch={false} href={item.href} className="group block py-8 max-md:py-6">
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
