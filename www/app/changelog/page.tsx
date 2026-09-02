import type { ReactNode } from "react";
import { Container } from "@/components/layout/Container";
import { PageHero, PageShell } from "@/components/pages/kit";
import {
  type Block,
  type Category,
  type Entry,
  type Group,
  type Release,
  type Span,
  entryCount,
  formatDay,
  formatShortDay,
  formatSpan,
  groupAnchor,
  releases,
} from "@/lib/changelog";
import { pageMetadata } from "@/lib/seo";
import { ChangelogControls } from "./Controls";

export const metadata = pageMetadata("/changelog");

/**
 * The changelog.
 *
 * Two readers, and they want opposite things. One wants to know what a release
 * means and will read a few hundred words; the other is looking for one change
 * and wants it in seconds. The page used to serve neither. Every entry was open
 * on one list, so v1.0.0 rendered as two hundred entries in a column 136,766
 * pixels tall on a desktop and 264,772 on a phone, one entry fully on screen in
 * the whole of it, and no way to see the shape of the release at all.
 *
 * So: a release states its size and its dates and links to its categories,
 * every entry is one scannable line headed by its author's own opening
 * sentence, and opening one is a `details` element. The first reader gets a
 * release they can take in without scrolling; the second gets two hundred
 * headlines to run an eye down, eight of them on a screen, four category
 * jumps, and a search that reads the entries the page is not showing.
 *
 * Collapsed is not hidden. The prose is in the HTML, which is what a crawler,
 * an answer engine and the markdown twin all read, and what the search here
 * reads too. A reader with no JavaScript gets the same page minus the search
 * box, because `details` needs no script to open.
 *
 * No cards. A changelog is a list of paragraphs with labels, and a border
 * around each one would add two hundred boxes and no information.
 * Separation is a hairline and space.
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
          {total === 0 ? (
            <p className="max-w-[720px] border-t border-black/12 pt-8 text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
              Nothing has been recorded yet. The first entry appears here when the first change
              lands.
            </p>
          ) : (
            <>
              {/* The search comes before the releases and the provenance note
                  comes after all of them. Both were the other way round, which
                  put two paragraphs about how the page is built between a
                  reader and the release they came for, and left nothing but
                  preamble on the first screen. How the dates are read is worth
                  saying and it is not what anybody opened this page to find
                  out. */}
              {/* The space the search takes is reserved here rather than
                  claimed when it mounts. The control renders nothing until it
                  has, so without this the whole page moved down by its own
                  height on hydration. The hairline belongs to the control
                  rather than to this, because two rules 69px apart with
                  nothing between them is what a reader with JavaScript off
                  would otherwise be looking at, and empty space is not. */}
              <div className="min-h-[69px] max-md:min-h-[124px]">
                <ChangelogControls total={total} />
              </div>
              {all.map((release) => (
                <ReleaseSection key={release.tag ?? "unreleased"} release={release} />
              ))}
              <Provenance />
            </>
          )}
        </Container>
      </section>
    </PageShell>
  );
}

/**
 * Where the dates and the grouping come from.
 *
 * A changelog that does not say this leaves a reader to assume the dates were
 * typed, and these were not. It sits at the foot of the page because it
 * answers a question asked after reading rather than before it.
 */
function Provenance() {
  return (
    <section className="mt-20 border-t border-black/12 pt-8 max-md:mt-14 max-md:pt-6">
      <h2 className="font-mono text-[11px] font-medium uppercase tracking-[0.14em] text-gray-new-20">
        How this page is made
      </h2>
      <div className="mt-5 max-w-[720px] text-[16px] leading-7 tracking-extra-tight text-gray-new-40">
        <p>
          A date here is the day the entry landed on the main branch, read from the commit that
          brought it there. Nothing on this page is typed by hand or backfilled.
        </p>
        <p className="mt-4">
          Changes with nothing a user of Antifailure could observe are recorded in the repository
          and left off this page: a lockfile drift, a test that was measuring nothing, a build
          artifact that needed ignoring. Every change to anything you can see is here, and CI
          refuses one that arrives without an entry.
        </p>
      </div>
    </section>
  );
}

function ReleaseSection({ release }: { release: Release }) {
  const count = entryCount(release);
  const name = release.tag ?? "Unreleased";

  return (
    <section
      data-release
      className="mt-20 border-t border-black/12 pt-10 first-of-type:mt-14 max-md:mt-14 max-md:pt-8"
    >
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
          {count > 0 ? (
            <>
              {" · "}
              <span data-release-count>{count}</span> entries
            </>
          ) : null}
          {release.span ? ` · landed ${formatSpan(release.span.from, release.span.to)}` : null}
        </p>
      </div>

      {release.emptyBecause ? (
        <p className="mt-6 max-w-[720px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40 max-md:text-[16px]">
          {release.emptyBecause === "predates the convention"
            ? "Cut before this repository started writing a fragment for every change. Nothing was recorded at the time, and nothing has been invented for it since."
            : "Everything in this release was internal: real changes with nothing a user could observe. They are kept in the repository and left off this page."}
        </p>
      ) : null}

      {release.undated > 0 ? (
        <p className="mt-6 max-w-[720px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40 max-md:text-[16px]">
          {release.undated === 1 ? "One entry here carries" : `${release.undated} entries here carry`}{" "}
          no date. This build could not read the commit that landed them, so the day is left blank
          rather than guessed.
        </p>
      ) : null}

      {/* The shape of the release, before any of it. Four numbers say whether
          this was a release of new work or of repairs, and whether there is a
          security entry in it, which is the one question a reader who is not
          upgrading yet still wants answered. They are links rather than
          filters: an index that scrolls needs no JavaScript, and the search
          above is the control that does something an index cannot. */}
      {release.groups.length > 0 ? (
        <nav aria-label={`${name} by category`} className="mt-8 flex flex-wrap gap-2">
          {release.groups.map((group) => (
            <a
              key={group.category}
              href={`#${groupAnchor(release, group.category)}`}
              data-chip={groupAnchor(release, group.category)}
              // Named, because the two spans inside concatenate with no space
              // when a screen reader computes a name from them, and "added49"
              // is not a thing anybody says. Controls.tsx rewrites this along
              // with the number beside it, so a search narrowing the page
              // narrows what this announces too.
              aria-label={`${group.category}, ${group.entries.length} entries`}
              // [&[hidden]]:hidden, because `hidden` is an attribute selector
              // in the browser's own stylesheet and inline-flex is a class in
              // this one, so the class wins and a chip hidden from script goes
              // on showing. The entries and the sections carry no display
              // class, which is why only this element needs it said.
              className="inline-flex min-h-11 items-center gap-x-2.5 rounded-full border border-black/15 bg-white px-4 font-mono text-[11px] font-medium uppercase tracking-[0.14em] text-gray-new-20 transition-colors [&[hidden]]:hidden hover:border-black/40 hover:text-black focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-black/60"
            >
              <span className={group.category === "security" ? "text-[#C43D3D]" : undefined}>
                {group.category}
              </span>
              <span className="text-gray-new-50" data-chip-count>
                {group.entries.length}
              </span>
            </a>
          ))}
        </nav>
      ) : null}

      {release.groups.map((group) => (
        <CategoryGroup key={group.category} release={release} group={group} />
      ))}
    </section>
  );
}

function CategoryGroup({ release, group }: { release: Release; group: Group }) {
  const anchor = groupAnchor(release, group.category);

  return (
    <section data-group className="mt-16 max-md:mt-12">
      {/* The heading holds its own column on a wide screen and stays there
          while its entries scroll past, for the reason the day used to: the
          label you lose first in a long list is the one you need most. */}
      <div className="grid grid-cols-[180px_minmax(0,1fr)] gap-x-16 border-t border-black/12 pt-8 max-xl:grid-cols-[150px_minmax(0,1fr)] max-xl:gap-x-10 max-lg:grid-cols-1 max-lg:gap-x-0 max-md:pt-6">
        <div className="self-start lg:sticky lg:top-24">
          <h3
            id={anchor}
            className="scroll-mt-24 text-[20px] leading-snug tracking-extra-tight text-black capitalize max-xl:scroll-mt-20 max-lg:text-[18px]"
          >
            {group.category}
          </h3>
          <p className="mt-2 font-mono text-[11px] uppercase tracking-[0.12em] text-gray-new-40">
            <span data-group-count>{group.entries.length}</span> entries
          </p>
          <p className="mt-3 max-w-[280px] text-[14px] leading-6 tracking-extra-tight text-gray-new-40 max-lg:hidden">
            {BLURBS[group.category]}
          </p>
        </div>

        {/* Not an `ol`. scripts/markdown-twins.mjs takes headings, paragraphs
            and list items out of the built HTML, and a list item is taken
            whole: wrapping each entry in an `li` collapsed all of its
            paragraphs into one bullet, so the twin an answer engine reads was
            one unbroken line per entry with the heading inside it.
            As sibling `details` the same content comes out as a heading and
            its paragraphs, which is what the file is for. */}
        <div className="min-w-0 max-w-[1040px] max-lg:mt-6">
          {group.entries.map((entry) => (
            <EntryRow key={entry.slug} entry={entry} />
          ))}
        </div>
      </div>
    </section>
  );
}

/**
 * What a category means here, said once beside the heading rather than in a
 * label on every entry under it. Dropped below 1024, where the heading stops
 * holding a column of its own and the line would sit between the reader and
 * the entries instead of beside them.
 */
const BLURBS: Record<Category, string> = {
  added: "Something the product could not do before.",
  changed: "Behaviour that already existed and now works differently.",
  fixed: "Something that claimed to work and did not.",
  security: "A defect with a consequence for the safety of your data or your build.",
};

/**
 * One entry, closed.
 *
 * `summary` is the whole row rather than a wrapper inside it, because its
 * content model is phrasing and heading content: a `div` in there is invalid
 * and a heading is not. Making the summary the grid itself keeps the markup
 * legal and takes the browser's own disclosure triangle away, which
 * `display: grid` does on its own. The marker below is drawn instead, so it
 * looks the same in every browser rather than like a Safari triangle here and
 * a Chrome one there.
 *
 * The headline is an `h4` and not a styled span. It is the level under the
 * category `h3`, so the page has a real outline for anything reading it as a
 * document, and scripts/markdown-twins.mjs takes headings and paragraphs,
 * which means a span here would have dropped every entry's opening line out of
 * the twin that answer engines read.
 */
function EntryRow({ entry }: { entry: Entry }) {
  return (
    <details
      id={entry.slug}
      data-entry
      className="group scroll-mt-24 border-b border-black/12 max-xl:scroll-mt-20 [&[open]]:pb-8"
    >
      <summary className="grid cursor-pointer grid-cols-[132px_minmax(0,1fr)_16px] items-start gap-x-8 py-5 marker:content-none hover:bg-black/[0.02] focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-black/60 max-lg:grid-cols-[minmax(0,1fr)_16px] max-lg:gap-x-4 max-lg:py-4">
        <span className="col-start-1 row-start-1 block pt-0.5 font-mono text-[11px] uppercase tracking-[0.12em] text-gray-new-40">
          {entry.landed ? (
            <time dateTime={entry.landed.slice(0, 10)}>
              {formatShortDay(entry.landed.slice(0, 10))}
            </time>
          ) : (
            "Undated"
          )}
          {entry.categories.length > 1 ? (
            <span className="mt-1.5 block">
              {entry.categories.map((category, index) => (
                <span key={category}>
                  {index === 0 ? null : <span className="text-gray-new-60"> · </span>}
                  <span
                    className={
                      category === "security" ? "font-medium text-[#C43D3D]" : "text-gray-new-20"
                    }
                  >
                    {category}
                  </span>
                </span>
              ))}
            </span>
          ) : null}
        </span>{" "}
        {/* A space, and it is not decoration: a whitespace only text node
            makes no grid item and changes nothing on screen, and without it
            the name a screen reader computes for this row runs the date
            into the sentence as "1 Sept 2026af start says where you are". */}

        {/* Clamped to two lines closed and released when open. The opening
            sentences run from 12 to 315 characters, so a fixed height would
            either clip the ordinary ones or leave a hole under them. */}
        <h4 className="col-start-2 row-start-1 min-w-0 text-[17px] leading-7 tracking-extra-tight text-black [overflow-wrap:anywhere] group-open:line-clamp-none max-lg:col-start-1 max-lg:row-start-2 max-lg:mt-2 max-lg:text-[16px] max-md:leading-6 line-clamp-2">
          {entry.headline.map(renderSpan)}
        </h4>

        <span
          aria-hidden="true"
          className="col-start-3 row-start-1 mt-2 block size-2 -rotate-45 border-r border-b border-black/40 transition-transform duration-200 group-open:rotate-45 max-lg:col-start-2 max-lg:row-span-2"
        />
      </summary>

      <div className="grid grid-cols-[132px_minmax(0,1fr)] gap-x-8 max-lg:grid-cols-1 max-lg:gap-x-0">
        <div className="col-start-2 min-w-0 max-lg:col-start-1">
          {/* Keyed by position, not by category. `migration-findings-in-the-check`
              opens two `# added` sections and one `# fixed`, so keying on the
              word gave two siblings the same key: React warns, and it is free
              to reuse the wrong one of them when the list changes. */}
          {entry.sections.map((section, sectionIndex) => (
            <div key={sectionIndex} className={sectionIndex === 0 ? "" : "mt-7"}>
              {/* One category, one label, and the group heading above already
                  carries it. An entry that declares two gets them over their
                  own paragraphs, where they say which half of the entry they
                  belong to. */}
              {entry.sections.length > 1 ? (
                <p className="mb-3">
                  <span
                    className={
                      section.category === "security"
                        ? "font-mono text-[11px] font-medium uppercase tracking-[0.14em] text-[#C43D3D]"
                        : "font-mono text-[11px] font-medium uppercase tracking-[0.14em] text-gray-new-20"
                    }
                  >
                    {section.category}
                  </span>
                </p>
              ) : null}
              {section.blocks.map((block, blockIndex) => (
                <Prose key={blockIndex} block={block} first={blockIndex === 0} />
              ))}
            </div>
          ))}

          <p className="mt-6">
            {/* inline-block with vertical padding, cancelled by an equal
                negative margin: the anchor's own hit area is 45px on a phone
                without the row growing. */}
            <a
              href={`#${entry.slug}`}
              className="-my-3.5 inline-block max-w-full py-3.5 font-mono text-[11px] tracking-snug text-gray-new-40 underline decoration-black/25 underline-offset-4 [overflow-wrap:anywhere] hover:text-black hover:decoration-black focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-black/60"
            >
              {entry.slug}
            </a>
          </p>
        </div>
      </div>
    </details>
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
          {span.spans.map(renderSpan)}
        </strong>
      );
    case "em":
      return (
        <em key={index} className="italic">
          {span.spans.map(renderSpan)}
        </em>
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
