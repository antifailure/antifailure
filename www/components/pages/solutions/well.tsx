import type { ReactNode } from "react";
import { Button } from "@/components/layout/Button";
import { Breadcrumbs } from "@/components/layout/Breadcrumbs";
import { Container } from "@/components/layout/Container";
import { SectionLabel } from "@/components/layout/SectionLabel";
import { PageJsonLd } from "@/lib/jsonld";
import { cn } from "@/lib/cn";

export function SageWell({
  children,
  className,
  compact = false,
}: {
  children: ReactNode;
  className?: string;
  compact?: boolean;
}) {
  return (
    <div
      className={cn(
        "relative flex flex-col justify-center overflow-hidden rounded-[32px] bg-sage",
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
export function FloatWindow({
  children,
  className,
  chrome = true,
}: {
  children: ReactNode;
  className?: string;
  chrome?: boolean;
}) {
  return (
    <div
      className={cn(
        chrome &&
          "rounded-[16px] bg-white shadow-[0_24px_64px_rgba(0,0,0,0.10),0_2px_8px_rgba(0,0,0,0.04)]",
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
  eyebrow,
  title,
  paragraphs,
}: {
  eyebrow: string;
  title: string;
  paragraphs: string[];
}) {
  return (
    <>
      <SectionLabel>{eyebrow}</SectionLabel>
      <h1 className="mt-5 max-w-[520px] text-[44px] font-medium leading-[1.15] tracking-tighter text-black max-lg:text-[32px]">
        {title}
      </h1>
      <div className="mt-8 max-w-[480px]">
        {paragraphs.map((p) => (
          <p key={p} className="mt-5 text-[17px] leading-7 text-gray-new-40 first:mt-0">
            {p}
          </p>
        ))}
      </div>
      <div className="mt-8">
        <Button href="/signup" theme="filled">
          Get started
        </Button>
      </div>
    </>
  );
}

function HeroVisual({ children }: { children: ReactNode }) {
  return (
    <div className="max-h-[380px] min-h-0 overflow-hidden max-md:max-h-[300px] [&>*]:!min-h-0 [&>*]:max-h-full">
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
  flip,
  stack,
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
    <section className="pt-16 pb-8 safe-paddings max-lg:pt-12 max-md:pt-10 max-md:pb-6">
      <Container size="1600" className="page-measure">
        <PageJsonLd path={path} />
        <Breadcrumbs path={path} />
        {stack ? (
          <>
            <div className="max-w-[640px]">
              <HeroCopy eyebrow={eyebrow} title={title} paragraphs={paragraphs} />
            </div>
            <div className="mt-8 max-md:mt-6">
              <HeroVisual>{visual}</HeroVisual>
            </div>
          </>
        ) : (
          <div className="grid grid-cols-12 items-start gap-x-16 gap-y-8 max-xl:grid-cols-1">
            <div className={cn("col-span-5 max-xl:col-span-1", flip && "xl:col-start-8 xl:order-2")}>
              <HeroCopy eyebrow={eyebrow} title={title} paragraphs={paragraphs} />
            </div>
            <div className={cn("col-span-7 max-xl:col-span-1", flip && "xl:col-start-1 xl:row-start-1")}>
              <HeroVisual>{visual}</HeroVisual>
            </div>
          </div>
        )}
      </Container>
    </section>
  );
}
