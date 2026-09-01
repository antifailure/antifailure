import type { ReactNode } from "react";
import Link from "next/link";
import { Button } from "@/components/layout/Button";
import { FaqJsonLd, PageJsonLd } from "@/lib/jsonld";
import { Breadcrumbs } from "@/components/layout/Breadcrumbs";
import { Container } from "@/components/layout/Container";
import { SiteLayout } from "@/components/layout/SiteLayout";
import { SectionLabel } from "@/components/layout/SectionLabel";
import { CopyCodeButton } from "@/components/home/media/CopyCodeButton";
import { cn } from "@/lib/cn";

/**
 * Every page that is not the homepage.
 *
 * This used to take an `inset` prop, and eight product pages passed it while
 * the six legal pages, the four solutions pages and pricing did not. It put a
 * 10% margin on the whole page and then reached inside the shared container
 * with `!max-w-none !px-0` to strip the measure and the gutter it had just
 * been given.
 *
 * That produced the defect somebody reported from the live site. A tinted
 * section paints its own background edge to edge of whatever box it is in. Put
 * that section inside a 10% margin and the tint stops 10% short of the page,
 * which is the "green margin". Take the container's padding away and the
 * text inside starts at exactly the tint's edge, touching it, while the other
 * side is left with several hundred pixels of empty colour. On /product/fidelity
 * at 1920 the band ran from 192 to 1728 and the heading, the verdict chips and
 * the paragraph all began at 192 with nothing between them and the edge.
 *
 * The two measures were also the same number where it mattered: `mx-[10%]` of
 * 1920 is 1536, and `max-w-[1600px] px-8` at 1920 is also 1536. The prop was
 * buying nothing above 1600 and only narrowing the page below it. So there is
 * one measure now, owned here, and no page overrides it.
 */
export function PageShell({ children }: { children: ReactNode }) {
  return (
    <SiteLayout overlay={false}>
      {children}
      <PagesClose />
    </SiteLayout>
  );
}

/**
 * Closing band on every page that is not the homepage.
 *
 * The homepage keeps the cinematic aurora panel. Inner pages were inheriting
 * that same tall photo, which sat as a green wash under product and solutions
 * copy that otherwise lives on cream. This is the same words and the same
 * buttons, set as a split like the rest of those pages.
 */
function PagesClose() {
  return (
    <section className="border-t border-black/12 bg-[#f7f7f5] safe-paddings">
      <Container size="1600" className="py-24 max-xl:py-20 max-md:py-14">
        <div className="grid grid-cols-2 items-end gap-x-16 gap-y-12 max-xl:grid-cols-1">
          <div className="min-w-0">
            <SectionLabel>Next</SectionLabel>
            <h2 className="mt-5 text-[48px] font-normal leading-dense tracking-tighter text-pretty text-gray-new-40 max-xl:text-[40px] max-lg:text-[28px] max-md:text-[24px]">
              <span className="text-black">Know what happens</span> before you deploy.
            </h2>
            <p className="mt-6 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
              Create a disposable production twin for every risky change. Catch migration failures
              before they reach customers.
            </p>
          </div>
          <div className="flex min-w-0 flex-col items-start gap-4 max-lg:w-full">
            <div className="flex gap-x-5 max-sm:w-full max-sm:flex-col max-sm:gap-y-3 max-sm:[&_a]:w-full max-sm:[&_button]:w-full">
              <Button href="/signup" theme="filled">
                Get started
              </Button>
              <Button href="/docs" theme="outlined">
                Read the docs
              </Button>
            </div>
            <CopyCodeButton
              variant="white"
              className="w-auto max-w-full border border-black/12 bg-white px-4 hover:bg-[#f7f7f5] max-xl:w-auto max-lg:w-full"
            />
          </div>
        </div>
      </Container>
    </section>
  );
}

export function PageHero({
  path,
  eyebrow,
  title,
  lead,
  visual,
  actions,
  framed = true,
}: {
  /**
   * The route this hero heads, when the page is one the route table knows.
   *
   * Passing it is what gives the page its breadcrumb trail and its WebPage
   * structured data, both of which are read by crawlers rather than people and
   * so are easy to leave off without anybody noticing. It is optional because
   * a hero on a route that is deliberately not indexed should not claim a
   * place in a trail.
   */
  path?: string;
  eyebrow: string;
  title: string;
  lead: string;
  visual?: ReactNode;
  /**
   * `undefined` renders the default pair of buttons. `null` renders no action
   * row at all, and no empty flex container leaving a gap where one was.
   *
   * The distinction is needed because `actions ?? default` treats both the
   * same, so the only way to say "none" is an empty fragment, which still
   * renders the wrapper and its margin.
   */
  actions?: ReactNode | null;
  framed?: boolean;
}) {
  const copy = (
    <>
      <SectionLabel>{eyebrow}</SectionLabel>
      <h1
        className={cn(
          "mt-5 text-[64px] leading-dense tracking-tighter max-xl:text-[52px] max-lg:text-[44px] max-md:text-[34px] max-sm:text-[32px]",
          visual ? "max-w-none" : "max-w-[1100px] max-xl:max-w-[920px]",
        )}
      >
        {title}
      </h1>
      <p className="mt-8 max-w-[640px] text-[20px] leading-snug tracking-extra-tight text-gray-new-40 max-md:text-[17px]">
        {lead}
      </p>
      {actions === null ? null : (
        <div className="mt-8 flex gap-x-5 max-lg:mt-7 max-sm:flex-col max-sm:gap-y-3 max-sm:[&_a]:w-full max-sm:[&_button]:w-full">
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
      )}
    </>
  );

  const figure = visual ? (
    framed ? (
      <div className="overflow-hidden rounded-[12px] border border-black/[0.08] bg-white">{visual}</div>
    ) : (
      visual
    )
  ) : null;

  return (
    <section className="pt-28 pb-16 safe-paddings max-lg:pt-16 max-md:pt-12 max-md:pb-10">
      <Container size="1600">
        {path ? <PageJsonLd path={path} /> : null}
        {path ? <Breadcrumbs path={path} /> : null}
        {figure ? (
          <div className="grid grid-cols-2 items-start gap-x-16 gap-y-12 max-xl:grid-cols-1">
            <div className="min-w-0">{copy}</div>
            <div className="min-w-0 max-w-[560px]">{figure}</div>
          </div>
        ) : (
          copy
        )}
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
        tone === "sage" &&
          "border-t border-black/12 bg-white py-32 max-xl:py-24 max-md:py-16",
        tone === "white" && "border-t border-black/12 py-28 max-xl:py-20 max-md:py-14",
        className,
      )}
    >
      <Container size="1600">
        {children}
      </Container>
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
      <ul className="grid grid-cols-3 gap-x-16 gap-y-14 max-xl:grid-cols-2 max-xl:gap-x-10 max-md:grid-cols-1 max-md:gap-y-8">
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
      <span className="pointer-events-none absolute inset-y-0 left-[calc(33.333%-32px)] w-px bg-black/12 max-xl:left-1/2 max-md:hidden" />
      <span className="pointer-events-none absolute inset-y-0 right-[calc(33.333%-32px)] w-px bg-black/12 max-xl:hidden" />
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
        "grid grid-cols-2 items-start gap-x-16 gap-y-12 max-xl:grid-cols-1",
        reverse && "[&>*:first-child]:max-xl:order-2",
      )}
    >
      <div className={cn("min-w-0", reverse && "xl:order-2")}>{children}</div>
      <div className="min-w-0 max-w-[560px]">{visual}</div>
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
            <tr key={k} className="border-b border-black/[0.08] last:border-0 max-md:flex max-md:flex-col">
              <th className="w-[38%] px-5 py-4 text-[14px] font-medium tracking-extra-tight text-black max-md:w-full max-md:pb-0">
                {k}
              </th>
              <td className="px-5 py-4 text-[14px] leading-6 tracking-extra-tight text-gray-new-40 max-md:pt-1">
                {v}
              </td>
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

/**
 * A fact nobody has supplied yet, rendered as an obvious blank.
 *
 * The legal pages need a handful of values that no part of this repository
 * knows: a registered entity, an address, a governing law. The wrong answer is
 * a template token, which builds and publishes looking like finished prose to
 * everybody except the one person who knows what it was meant to say. This
 * reads as a blank on the rendered page, in the same amber the warn callout
 * already uses, so a page shipped with one in it is visibly unfinished.
 */
export function Blank({ children }: { children: string }) {
  return (
    <span className="rounded-[4px] border-b border-dashed border-[#8A6A12]/70 bg-[#8A6A12]/[0.08] px-1.5 font-mono text-[0.82em] tracking-snug text-[#8A6A12]">
      {children} to be supplied
    </span>
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
      <ul className="grid grid-cols-3 gap-x-16 gap-y-10 max-xl:grid-cols-2 max-xl:gap-x-10 max-md:grid-cols-1">
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

export function CompactSwap({ desktop, compact }: { desktop: ReactNode; compact: ReactNode }) {
  return (
    <>
      <div className="max-xl:hidden">{desktop}</div>
      <div className="hidden max-xl:block">{compact}</div>
    </>
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
 * `FaqJsonLd` existed before this did and had no callers at all, so the site
 * carried a complete FAQPage emitter and published no FAQPage anywhere.
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
