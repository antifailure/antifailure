import type { ReactNode } from "react";
import { Container } from "@/components/layout/Container";
import { PageHero, PageShell } from "@/components/pages/kit";
import {
  type Block,
  type Category,
  type Release,
  type Span,
  entryCount,
  formatDay,
  releases,
} from "@/lib/changelog";
import { pageMetadata } from "@/lib/seo";

export const metadata = pageMetadata("/changelog");

/**
 * The changelog.
 *
 * Read by somebody asking one question, "what changed since I last looked", so
 * the date is the loudest thing on the page and everything else is quiet
 * around it. On a wide screen the date holds its own column and stays put
 * while its entries scroll past, because the day a batch of work landed is the
 * context you lose first in a long list and the one you need most.
 *
 * No cards. A changelog is a list of paragraphs with labels, and a border
 * around each one would add ninety-one boxes and no information. Separation is
 * a hairline and space.
 */
export default function ChangelogPage() {
  const all = releases();
  const total = all.reduce((n, release) => n + entryCount(release), 0);

  return (
    <PageShell>
      <PageHero
        path="/changelog"
        eyebrow="Changelog"
        title="Everything that has changed, and what it means if you are using it."
        lead="Each entry is written when the change is made, by whoever made it, and says what it does rather than that it exists. Newest first."
        actions={null}
      />

      {/* Not PageSection. Its py-28 put a 112px void between the lead and the
          first thing on the page, and overriding padding on it is not
          available: cn is a plain join, so both classes land on the element
          and the stylesheet's own order decides which one wins. */}
      <section className="safe-paddings pb-28 max-xl:pb-20 max-md:pb-14">
        <Container size="1600">
          {/* Where the dates and the grouping come from. A changelog that does
              not say this leaves a reader to assume the dates were typed, and
              these were not. */}
          <div className="max-w-[720px] border-t border-black/12 pt-8 text-[16px] leading-7 tracking-extra-tight text-gray-new-40 max-md:pt-6">
            <p>
              A date here is the day the entry landed on the main branch, read from the commit that
              brought it there. Nothing on this page is typed by hand or backfilled.
            </p>
            <p className="mt-4">
              Changes with nothing a user of Antifailure could observe are recorded in the
              repository and left off this page: a lockfile drift, a test that was measuring
              nothing, a build artifact that needed ignoring. Every change to anything you can see
              is here, and CI refuses one that arrives without an entry.
            </p>
          </div>

          {total === 0 ? (
            <p className="mt-14 max-w-[720px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
              Nothing has been recorded yet. The first entry appears here when the first change
              lands.
            </p>
          ) : (
            all.map((release) => (
              <ReleaseSection key={release.tag ?? "unreleased"} release={release} />
            ))
          )}
        </Container>
      </section>
    </PageShell>
  );
}

function ReleaseSection({ release }: { release: Release }) {
  const count = entryCount(release);
  const name = release.tag ?? "Unreleased";

  return (
    <section className="mt-20 border-t border-black/12 pt-10 first-of-type:mt-16 max-md:mt-14 max-md:pt-8">
      <div className="flex flex-wrap items-baseline gap-x-6 gap-y-2">
        <h2 className="text-[36px] leading-dense tracking-tighter text-black max-lg:text-[30px] max-md:text-[26px]">
          {name}
        </h2>
        <p className="font-mono text-[12px] uppercase tracking-[0.12em] text-gray-new-40">
          {release.date ? (
            <>
              Released <time dateTime={release.date}>{formatDay(release.date.slice(0, 10))}</time>
            </>
          ) : (
            "Not released yet"
          )}
          {count > 0 ? ` · ${count} ${count === 1 ? "entry" : "entries"}` : null}
        </p>
      </div>

      {release.emptyBecause ? (
        <p className="mt-6 max-w-[720px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40 max-md:text-[16px]">
          {release.emptyBecause === "predates the convention"
            ? "Cut before this repository started writing a fragment for every change. Nothing was recorded at the time, and nothing has been invented for it since."
            : "Everything in this release was internal: real changes with nothing a user could observe. They are kept in the repository and left off this page."}
        </p>
      ) : null}

      {release.days.map((day) => (
        <div
          key={day.date ?? "undated"}
          className="mt-12 grid grid-cols-[180px_minmax(0,1fr)] gap-x-16 border-t border-black/12 pt-8 max-xl:grid-cols-[150px_minmax(0,1fr)] max-xl:gap-x-10 max-lg:grid-cols-1 max-lg:gap-x-0 max-md:mt-8 max-md:pt-6"
        >
          <div className="self-start lg:sticky lg:top-24">
            {day.date ? (
              <time
                dateTime={day.date}
                className="block text-[20px] leading-snug tracking-extra-tight text-black max-lg:text-[18px]"
              >
                {formatDay(day.date)}
              </time>
            ) : (
              <span className="block text-[20px] leading-snug tracking-extra-tight text-black max-lg:text-[18px]">
                Undated
              </span>
            )}
            <p className="mt-2 font-mono text-[11px] uppercase tracking-[0.12em] text-gray-new-40">
              {day.entries.length} {day.entries.length === 1 ? "entry" : "entries"}
            </p>
            {day.date ? null : (
              <p className="mt-3 max-w-[280px] text-[14px] leading-6 tracking-extra-tight text-gray-new-40">
                This build could not read the commit that landed these, so their date is left blank
                rather than guessed.
              </p>
            )}
          </div>

          <ol className="min-w-0 max-lg:mt-8">
            {day.entries.map((entry, index) => (
              <li
                key={entry.slug}
                className={index === 0 ? "" : "mt-10 border-t border-black/12 pt-10 max-md:mt-8 max-md:pt-8"}
              >
                <article id={entry.slug} className="scroll-mt-24 max-xl:scroll-mt-20">
                  <div className="flex min-h-11 flex-wrap items-center gap-x-5 gap-y-1">
                    {/* One category, one label. An entry that carries two
                        gets them above their own paragraphs instead, where
                        they say which half of the entry they belong to;
                        repeating them here as well would print the word
                        "fixed" twice in eight inches and mean nothing extra. */}
                    {entry.sections.length === 1 ? (
                      <CategoryLabel category={entry.sections[0].category} />
                    ) : null}
                    <a
                      href={`#${entry.slug}`}
                      // inline-block with vertical padding, cancelled by an
                      // equal negative margin: the anchor's own hit area is
                      // 45px on a phone without the row growing. The row's
                      // min-h-11 does not help, because a 17px anchor inside a
                      // 44px row is still a 17px target.
                      className="-my-3.5 inline-block min-w-0 py-3.5 font-mono text-[11px] tracking-snug text-gray-new-40 underline decoration-black/25 underline-offset-4 [overflow-wrap:anywhere] hover:text-black hover:decoration-black"
                    >
                      {entry.slug}
                    </a>
                  </div>

                  {entry.sections.map((section, sectionIndex) => (
                    <div key={section.category} className={sectionIndex === 0 ? "mt-3" : "mt-7"}>
                      {entry.sections.length > 1 ? (
                        <p className="mb-3">
                          <CategoryLabel category={section.category} />
                        </p>
                      ) : null}
                      {section.blocks.map((block, blockIndex) => (
                        <Prose key={blockIndex} block={block} first={blockIndex === 0} />
                      ))}
                    </div>
                  ))}
                </article>
              </li>
            ))}
          </ol>
        </div>
      ))}
    </section>
  );
}

/**
 * The category, as a word.
 *
 * Security is the one that carries a colour, in the red this site already uses
 * for a blocked request, because it is the entry somebody scanning the page
 * needs to find without reading. The word is there either way: colour is never
 * the only thing saying what this is.
 */
function CategoryLabel({ category }: { category: Category }) {
  return (
    <span
      className={
        category === "security"
          ? "font-mono text-[11px] font-medium uppercase tracking-[0.14em] text-[#C43D3D]"
          : "font-mono text-[11px] font-medium uppercase tracking-[0.14em] text-gray-new-20"
      }
    >
      {category}
    </span>
  );
}

function Prose({ block, first }: { block: Block; first: boolean }) {
  const spacing = first ? "" : "mt-5";
  // The same `overflow-wrap: anywhere` the inline code carries, on the prose
  // that holds it, because a bare address in a sentence is one word to the
  // browser exactly as a metric name is. `antifailure.dev/docs/enterprise/
  // licensing,` is 296px in a 280px column at 320, and ran to the screen edge
  // with its trailing comma against the glass. The code span's own rule cannot
  // help there: this token is not in a code span.
  //
  // It is on the block rather than on a wrapper so that it also fixes the
  // block's min-content width, which is what a grid measures when it decides
  // how wide a column has to be.
  const prose = `${spacing} max-w-[720px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40 [overflow-wrap:anywhere] max-md:text-[16px]`;
  if (block.kind === "ul") {
    return (
      <ul className={`${prose} list-disc space-y-3 pl-5 marker:text-black/30`}>
        {block.items.map((item, index) => (
          <li key={index}>{item.map(renderSpan)}</li>
        ))}
      </ul>
    );
  }
  return <p className={prose}>{block.spans.map(renderSpan)}</p>;
}

function renderSpan(span: Span, index: number): ReactNode {
  switch (span.kind) {
    case "code":
      return (
        <code
          key={index}
          // overflow-wrap: anywhere, not break-words, because a metric name is
          // one word to the browser and break-word will not split it.
          // `af_ingest_events_total{outcome="unprojected"}` renders 378px wide
          // in a 350px column at 390, and pushed the whole page sideways by
          // 12px, which is the one defect a phone pass found here.
          className="rounded-[3px] bg-black/[0.07] px-1 py-0.5 font-mono text-[0.87em] tracking-snug text-gray-new-10 [overflow-wrap:anywhere]"
        >
          {span.text}
        </code>
      );
    case "strong":
      return (
        <strong key={index} className="font-medium text-black">
          {span.text}
        </strong>
      );
    case "link":
      return (
        <a
          key={index}
          href={span.href}
          className="text-black underline decoration-black/25 underline-offset-4 hover:decoration-black"
        >
          {span.text}
        </a>
      );
    default:
      return <span key={index}>{span.text}</span>;
  }
}
