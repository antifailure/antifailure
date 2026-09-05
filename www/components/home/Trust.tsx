import { Container } from "@/components/layout/Container";
import { TrustIcon } from "@/components/home/media/icons";
import { cn } from "@/lib/cn";
import type { ReactNode } from "react";

const ITEMS = [
  {
    icon: "failclosed" as const,
    title: "Fail closed",
    description: "An unverified golden cannot be branched, and inside the twin there is no route out.",
  },
  {
    icon: "hosted" as const,
    title: "Customer-hosted",
    description: "Production data stays inside the customer boundary. The control plane never needs a copy.",
  },
];

/**
 * The label that opens each of the two columns.
 *
 * Not `SectionLabel`. That component marks a section and draws its glyph in
 * brand green, which is why it vanished here: green sits at 2.10:1 on this
 * band's mint ground, under the 3:1 a meaningful graphic needs, and its label
 * is set at `text-black/70`, which reads grey rather than as a heading. Both
 * are correct on the site's near-white ground and wrong on this one. Changing
 * the shared component to suit one band would restyle the eyebrow on eleven
 * other places that are fine, so the variant lives here instead.
 */
function ColumnLabel({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={cn("flex items-center gap-2.5 max-md:gap-2", className)}>
      <svg viewBox="0 0 10 10" className="size-2.5 flex-none text-ochre max-md:size-2" aria-hidden>
        <path d="M1 0.6 9 5 1 9.4Z" fill="currentColor" />
      </svg>
      <span className="font-mono text-xs font-medium uppercase leading-none tracking-[0.12em] text-black max-md:text-[11px]">
        {children}
      </span>
    </div>
  );
}

export function Trust() {
  return (
    <section
      id="trust"
      className="relative overflow-hidden bg-sage pt-40 pb-[168px] text-black safe-paddings max-xl:py-[136px] max-lg:py-[88px] max-md:py-14"
    >
      {/* One light source, high and to the right, and the only gradient in the
          section. The band was a flat #cadcc4 with nothing in it, which sat
          heavier than the black panel that follows it and pushed the grey half
          of the headline down to 4.10:1. Lightening the ground to the sage the
          rest of the site already uses fixed the contrast; this restores the
          depth that a flat fill loses. Kept wide and low in contrast on
          purpose: it should read as the corner being lit, not as a gradient.

          The peak is 0.68 and not higher because the page above this band is
          #f7f7f5, which is warmer and lighter than sage. Measured at 0.95 the
          bloom passed straight through it: the band's top edge stepped down on
          the left, vanished around x=720 where the two met, and stepped UP on
          the right, so one edge read in two directions. Under 0.70 the red
          channel never reaches the page's, so the band stays cooler than what
          it sits under across the whole width and the edge reads once. */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 bg-[radial-gradient(92%_74%_at_80%_0%,rgba(255,255,255,0.68)_0%,rgba(255,255,255,0.51)_22%,rgba(255,255,255,0.24)_47%,rgba(255,255,255,0)_74%)]"
      />

      <Container className="z-10" size="1344">
        <div className="relative z-10 flex gap-16 max-xl:flex-col max-xl:gap-12 max-lg:gap-10">
          <div className="flex-1 border-l border-black/12 pl-8 max-lg:pl-6 max-sm:border-none max-sm:pl-0">
            <ColumnLabel className="mb-6 max-md:mb-5">Trust model</ColumnLabel>
            <h2
              className={cn(
                "text-[44px] leading-dense tracking-tighter text-gray-new-40",
                "max-xl:text-[36px] max-lg:text-[32px] max-md:text-[26px]",
                "[&>strong]:font-normal [&>strong]:text-black",
              )}
            >
              <strong>Fail closed. Customer-hosted.</strong> Production data stays in your
              boundary. Cleanup is journaled as it happens, not reconstructed afterwards.
            </h2>
            {/* Equal columns rather than the two hand-set widths this used to
                carry. Those were measured against one viewport and left 230px
                of the row unused at 1440, which is what made the block read as
                huddled into the left edge. */}
            <ul className="mt-16 grid max-w-[660px] grid-cols-2 gap-x-14 max-xl:mt-12 max-xl:max-w-none max-lg:gap-x-10 max-md:mt-10 max-md:grid-cols-1 max-md:gap-y-10">
              {ITEMS.map((item) => (
                <li key={item.title}>
                  <TrustIcon name={item.icon} className="mb-5 text-black max-lg:mb-4" />
                  <h3 className="text-[28px] leading-dense tracking-tighter max-lg:text-[26px] max-md:text-[24px]">
                    {item.title}
                  </h3>
                  <p className="mt-2 text-[15px] leading-relaxed tracking-extra-tight text-pretty text-gray-new-40 max-md:text-base">
                    {item.description}
                  </p>
                </li>
              ))}
            </ul>
          </div>

          {/* The quote sits at the foot of the column, not the head of it. Top
              aligned it left the whole lower right quadrant empty with a
              hairline running down the side of the emptiness, while the left
              column carried everything. Bottom aligned, the two columns share
              a baseline and the eye crosses the band at two heights. The rule
              moved onto this group for the same reason: it now measures the
              content instead of pointing at the gap above it. */}
          <div className="flex w-[480px] flex-col border-l border-black/12 pl-8 max-lg:pl-6 max-xl:w-full max-sm:border-none max-sm:pl-0">
            {/* The eyebrow belongs at the TOP of this column, level with the
                one across the band, and the quote belongs at the FOOT of it,
                level with the two items across the band. It had the label and
                the quote welded together as one block floating at the bottom,
                so the two columns opened at two different heights and the rule
                beside this one started halfway down a section it was supposed
                to measure. Label at the top, `mt-auto` on the figure, rule on
                the column rather than on the figure: two eyebrows on one line,
                two blocks on one baseline, two rules the full height. */}
            <ColumnLabel className="mb-6 max-md:mb-5">What counts as proof</ColumnLabel>
            <figure className="mt-auto max-xl:mt-10">
              {/* This was four claims we decline to make, read left to right as
                  "zero rollback, no deployment can ever fail", which is the
                  opposite of what it meant. A block whose first four sentences
                  are things that are not true asks the reader to hold a
                  negation across sixty characters before it pays out, and the
                  one line that said what we do instead was the fifth. It says
                  the positive thing now and the marked phrase closes it.

                  The mark is `sage-2`, the band's own darker sibling, rather
                  than the ochre wash it carried. A warm brown at 20 percent
                  over a mint ground composites to olive, which is why it read
                  as a stain rather than as a highlighter. Measured off the
                  render: the mark computes to rgb(202,230,217) on a band of
                  rgb(228,241,235), black on it is 15.83:1, and the mark is a
                  1.15x luminance step down from the band. The step is small on
                  purpose. The marked words also go from the blockquote's
                  black/60 to full black, and that is what carries the
                  emphasis; the fill only has to say where the phrase starts
                  and stops, so it does not need a border either. */}
              <blockquote className="max-w-[64ch] font-mono text-[15px] leading-7 text-black/60 max-md:text-base max-md:leading-[1.85]">
                A verdict is only worth what it carries. Every result on a pull request arrives
                with the rows it read, the trace it took and the recording of the attempt, so a
                reviewer can check the finding instead of trusting it.{" "}
                <mark className="box-decoration-clone rounded-[2px] bg-sage-2 px-1.5 py-1 text-black">
                  Measurable evidence, or no claim.
                </mark>
              </blockquote>
              <figcaption className="mt-7 font-mono text-[13px] tracking-extra-tight text-gray-new-40">
                <cite className="not-italic">Product brief, section 18</cite>
              </figcaption>
            </figure>
          </div>
        </div>
      </Container>
    </section>
  );
}
