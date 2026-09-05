import type { ReactNode } from "react";
import { Button } from "@/components/layout/Button";
import { Breadcrumbs } from "@/components/layout/Breadcrumbs";
import { Container } from "@/components/layout/Container";
import { SectionLabel } from "@/components/layout/SectionLabel";
import { PageJsonLd } from "@/lib/jsonld";
import { cn } from "@/lib/cn";

// `radius` is a prop for the same reason `chrome` below is one. A caller that
// wants a tighter corner cannot get it from className: `cn` is a plain join, so
// the well's own rounded-[32px] lands on the element beside it and the cascade,
// not the caller, decides. Only one radius class is ever written from here.
const wellRadius = {
  32: "rounded-[32px]",
  24: "rounded-[24px]",
} as const;

export function SageWell({
  children,
  className,
  compact = false,
  radius = 32,
}: {
  children: ReactNode;
  className?: string;
  compact?: boolean;
  radius?: keyof typeof wellRadius;
}) {
  return (
    <div
      className={cn(
        "relative flex flex-col justify-center overflow-hidden bg-sage",
        wellRadius[radius],
        compact
          ? "min-h-0 max-h-[380px] px-5 py-5 max-md:max-h-[300px] max-md:px-4 max-md:py-4 md:px-7 md:py-6"
          : "min-h-[520px] px-6 py-8 max-md:min-h-[360px] max-md:px-4 max-md:py-5 md:px-10 md:py-12",
        className,
      )}
    >
      {children}
    </div>
  );
}

// `chrome` is a prop rather than something a caller cancels from className.
// `cn` is a plain join, so a class passed in does not replace the one already
// here: both land on the element and the cascade picks, and the receipt tape
// spent three `!` utilities that were never emitted trying to win that race.
// Only one of these two strings is ever written, so there is no race.
const windowRadius = {
  16: "rounded-[16px]",
  13: "rounded-[13px]",
} as const;

export function FloatWindow({
  children,
  className,
  chrome = true,
  radius = 16,
}: {
  children: ReactNode;
  className?: string;
  chrome?: boolean;
  radius?: keyof typeof windowRadius;
}) {
  return (
    <div
      className={cn(
        chrome &&
          cn(windowRadius[radius], "bg-white shadow-[0_24px_64px_rgba(0,0,0,0.10),0_2px_8px_rgba(0,0,0,0.04)]"),
        className,
      )}
    >
      {children}
    </div>
  );
}

function ArrowList({ items }: { items: { title: string; body?: string }[] }) {
  return (
    <ul className="mt-8 space-y-3.5">
      {items.map((item) => (
        <li key={item.title} className="flex gap-3">
          <span className="mt-px shrink-0 text-[15px] leading-6 text-[#33bf00]" aria-hidden>
            →
          </span>
          <p className="text-[16px] leading-6 text-gray-new-40">
            {item.body ? (
              <>
                <span className="font-medium text-black">{item.title}. </span>
                {item.body}
              </>
            ) : (
              item.title
            )}
          </p>
        </li>
      ))}
    </ul>
  );
}

function FeatureCopy({
  kicker,
  title,
  items,
}: {
  kicker: string;
  title: string;
  items: { title: string; body?: string }[];
}) {
  return (
    <>
      <SectionLabel>{kicker}</SectionLabel>
      <h2 className="mt-4 max-w-[520px] text-[36px] font-medium leading-[1.15] tracking-tighter text-black max-md:text-[28px]">
        {title}
      </h2>
      <ArrowList items={items} />
    </>
  );
}

export function FeatureRow({
  kicker,
  title,
  items,
  visual,
  reverse,
  stack,
}: {
  kicker: string;
  title: string;
  items: { title: string; body?: string }[];
  visual: ReactNode;
  reverse?: boolean;
  stack?: boolean;
}) {
  return (
    <section className="py-24 safe-paddings max-md:py-14">
      <Container size="1600" className="page-measure">
        {stack ? (
          <>
            <div className="max-w-[640px]">
              <FeatureCopy kicker={kicker} title={title} items={items} />
            </div>
            <div className="mt-14 max-md:mt-10">{visual}</div>
          </>
        ) : (
          <div
            className={cn(
              "grid grid-cols-12 items-center gap-x-16 gap-y-12 max-xl:grid-cols-1",
              reverse && "[&>*:first-child]:max-xl:order-2",
            )}
          >
            <div className={cn("col-span-5 max-xl:col-span-1", reverse && "xl:col-start-8 xl:order-2")}>
              <FeatureCopy kicker={kicker} title={title} items={items} />
            </div>
            <div className={cn("col-span-7 max-xl:col-span-1", reverse && "xl:col-start-1 xl:row-start-1")}>
              {visual}
            </div>
          </div>
        )}
      </Container>
    </section>
  );
}

function HeroCopy({
  paragraphs,
}: {
  paragraphs: string[];
}) {
  return (
    <div className="flex h-full min-w-0 flex-col">
      <div className="max-w-[520px] border-t border-black/12 pt-5">
        {paragraphs.map((p, index) => (
          <p
            key={p}
            // One size, one leading and one colour is emitted, never two of
            // any. The lead paragraph asked for text-black beside the shared
            // text-gray-new-40 and lost the cascade, so the paragraph the page
            // is built around rendered in the same grey as the rest.
            className={cn(
              "tracking-extra-tight",
              index === 0
                ? "text-[19px] leading-[1.55] text-black"
                : "mt-4 border-t border-black/[0.07] pt-4 text-[16px] leading-7 text-gray-new-40",
            )}
          >
            {p}
          </p>
        ))}
      </div>
      {/* The same pair as every other hero on the site, in the same order.
          This one offered only the invitation wall, so a solutions page pitched
          the product and then gave a visitor nothing they could do today. */}
      <div className="mt-8 flex flex-wrap gap-3 xl:mt-auto xl:pt-10 max-sm:flex-col max-sm:[&_a]:w-full">
        <Button href="/docs/getting-started/quickstart" theme="filled">
          Start the quickstart
        </Button>
        <Button href="/signup" theme="outlined">
          Request hosted access
        </Button>
      </div>
    </div>
  );
}

function HeroVisual({ children }: { children: ReactNode }) {
  return (
    <div className="min-h-0 overflow-hidden rounded-[28px] [&>*]:!min-h-0 [&>*]:!max-h-none [&>*]:!rounded-[28px]">
      {children}
    </div>
  );
}

export function SplitHero({
  path,
  eyebrow,
  title,
  paragraphs,
  visual,
}: {
  path: string;
  eyebrow: string;
  title: string;
  paragraphs: string[];
  visual: ReactNode;
  flip?: boolean;
  stack?: boolean;
}) {
  return (
    <section className="border-b border-black/12 pt-16 pb-20 safe-paddings max-lg:pt-12 max-lg:pb-16 max-md:pt-10 max-md:pb-12">
      <Container size="1600" className="page-measure">
        <PageJsonLd path={path} />
        <Breadcrumbs path={path} />
        <div className="mt-8 max-md:mt-6">
          <SectionLabel>{eyebrow}</SectionLabel>
          <h1 className="mt-5 max-w-[1080px] text-balance text-[64px] font-normal leading-[1.02] tracking-tighter text-black max-xl:text-[56px] max-lg:text-[46px] max-md:text-[38px] max-sm:text-[34px]">
            {title}
          </h1>
        </div>

        <div className="mt-12 grid grid-cols-12 items-stretch gap-x-16 gap-y-10 max-xl:mt-10 max-xl:grid-cols-1 max-md:mt-8">
          <div className="col-span-5 min-w-0 max-xl:order-2 max-xl:col-span-1">
            <HeroCopy paragraphs={paragraphs} />
          </div>
          <div className="col-span-7 min-w-0 max-xl:order-1 max-xl:col-span-1">
            <HeroVisual>{visual}</HeroVisual>
          </div>
        </div>
      </Container>
    </section>
  );
}
