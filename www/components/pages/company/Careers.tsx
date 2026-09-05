import { Button } from "@/components/layout/Button";
import {
  Callout,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  Prose,
  RelatedGrid,
} from "@/components/pages/kit";
import { REPO_URL } from "@/lib/site";
import { ApplicationForm } from "./ApplicationForm";

/**
 * The hiring page, and the reason the compensation is above the roles.
 *
 * A careers page that describes the work first and the terms last wastes the
 * time of everybody it does not suit, and this one has terms that decide it for
 * most readers: there is no salary for either role currently. Somebody who
 * needs a salary should be able to learn that in the first screen and close the
 * tab, without reading two role descriptions to find out. So the terms band
 * sits directly under the hero, before either role, and the acknowledgment on
 * the form repeats the same two numbers rather than a summary of them.
 *
 * WHAT THIS PAGE MAY NOT SAY. The equity ranges and the absence of salary are
 * the whole of the offer that exists. There is deliberately no date when salary
 * begins, no vesting schedule, no funding claim, no location, no benefit list
 * and no traction number anywhere on it, because none of those has been
 * decided, and a careers page is the worst place to imply one that has not: a
 * candidate reads an implication as an offer and arrives believing it.
 *
 * THE TWO ROLE ANCHORS ARE REAL CONTROLS. "Apply for a founding engineer role"
 * points at `#apply-founding_engineer`, which is the id of that role's radio
 * inside the form below. The fragment scrolls the form into view AND moves
 * focus onto the control the link named, and ApplicationForm reads the same
 * fragment to select it, so following the link leaves the reader with the role
 * chosen rather than at a form that has forgotten which link they clicked.
 * That is why those two ids are fixed strings rather than `useId` values: they
 * are addressed from another component, which a generated id cannot be.
 */

const ROLES = [
  {
    id: "founding_engineer",
    title: "Founding engineer",
    equity: "0.5% to 2% equity",
    work: "Build the path from a developer's repository to a working production rehearsal. Own the engine, the control plane, or the developer experience, and follow your own work all the way to a result somebody can inspect.",
    proof: "Show us something you built, a hard failure you diagnosed, or a system you made dependable. A public repository is welcome and not required.",
  },
  {
    id: "founding_growth",
    title: "Founding growth",
    equity: "0.25% to 2% equity",
    work: "Help developers find Antifailure, understand what it does, and reach their first useful result. Own the experiments in positioning, distribution and activation, with real conversations behind the numbers.",
    proof: "Show us something you helped people discover or adopt. Say what you tried, what changed, and what you learned when it did not work.",
  },
] as const;

export function CareersPage() {
  return (
    <PageShell>
      <PageHero
        path="/careers"
        eyebrow="Careers"
        title="Build the proof before the deploy."
        lead="Two founding roles, one on the product and one on how developers find it. The compensation is on this page rather than behind a conversation, because it decides this for most people and they should not have to ask."
        actions={
          <>
            <Button href="#apply" theme="filled">
              Apply to join
            </Button>
            <Button href={REPO_URL} theme="outlined">
              Read the source first
            </Button>
          </>
        }
      />

      {/* Above both roles, deliberately. See the note at the top of this file. */}
      <PageSection>
        <PageHeading
          kicker="The terms"
          title="<strong>There is no salary for either role currently.</strong> Both equity ranges are written down here."
        />
        <div className="mt-14 grid grid-cols-[minmax(0,720px)_minmax(260px,420px)] gap-x-20 gap-y-10 max-lg:mt-10 max-lg:grid-cols-1">
          <Prose>
            <p>
              These are the ranges, not a finalized offer, and the specific
              number inside a range would be agreed with you. What is fixed is
              the part above: no salary is paid for either role today.
            </p>
            <p>
              Nothing on this page says when that changes, because nothing has
              decided it. There is no vesting schedule here, no funding
              announcement, no location requirement and no benefits list, and
              their absence is the honest state rather than an omission. If you
              need any of those to be true, this is the wrong time to join and
              it costs you nothing to have read one screen.
            </p>
          </Prose>
          <div className="self-start">
            <Callout label="Current compensation" tone="warn">
              Founding engineer: 0.5% to 2% equity.
              <span className="mt-2 block">Founding growth: 0.25% to 2% equity.</span>
              <span className="mt-2 block">No salary for either, currently.</span>
            </Callout>
          </div>
        </div>
      </PageSection>

      <PageSection tone="panel">
        <PageHeading
          kicker="Two roles"
          title="<strong>One builds the product, one builds how developers reach it.</strong> Both are early enough to own an area outright."
        />
        <ul className="mt-14 grid grid-cols-2 gap-5 max-lg:mt-10 max-md:grid-cols-1">
          {ROLES.map((role) => (
            <li
              key={role.id}
              className="flex flex-col rounded-[8px] bg-white p-7 ring-1 ring-black/10 max-md:p-6"
            >
              <h2 className="text-[22px] leading-snug tracking-tighter text-black">
                {role.title}
              </h2>
              <p className="mt-3 text-[15px] leading-6 tracking-extra-tight text-black">
                {role.equity}
                <span className="block text-gray-new-40">No salary currently</span>
              </p>
              <p className="mt-5 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
                {role.work}
              </p>
              <p className="mt-4 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
                {role.proof}
              </p>
              {/* Points at the role's own radio in the form below, which the
                  form then selects from the fragment. */}
              <a
                href={`#apply-${role.id}`}
                className="mt-6 inline-flex min-h-11 items-center self-start rounded-full bg-black px-5 text-[14px] font-medium text-white transition-colors hover:bg-[#292929]"
              >
                Apply for this role
              </a>
            </li>
          ))}
        </ul>
      </PageSection>

      {/* `ruled` rather than `panel`, for the reason /contact gives at the same
          spot: the form is a white surface with a hairline ring, and a white
          card on a white band loses its own boundary. */}
      <PageSection tone="ruled">
        <div id="apply" className="scroll-mt-24">
          <PageHeading
            kicker="Apply"
            title="<strong>Show us your work.</strong> A short introduction is enough."
          />
          <div className="mt-10 grid grid-cols-[minmax(0,720px)_minmax(260px,420px)] gap-x-20 gap-y-10 max-lg:grid-cols-1">
            <div className="min-w-0">
              <ApplicationForm />
            </div>
            <div className="self-start">
              {/* No size override. `Prose` sets text-[17px] and `cn` is a plain
                  join with no tailwind-merge, so a text-[15px] written beside it
                  would be emitted and lose, which is the class that never
                  applies `just classcheck` refuses. */}
              <Prose>
                <p>
                  No account and no resume upload. Please leave out confidential
                  work, credentials, and anything sensitive about you that this
                  does not ask for.
                </p>
                <p>
                  What you send goes to a private review queue that an
                  authorized operator reads. It is not a public issue, a mailing
                  list, or an analytics event. Applications are removed after
                  180 days by scheduled maintenance, and you can ask for yours
                  to be removed sooner.
                </p>
              </Prose>
            </div>
          </div>
        </div>
      </PageSection>

      <RelatedGrid
        items={[
          {
            href: "/about",
            title: "About the project",
            description:
              "What Antifailure is, the category it claims, and the limits it states rather than footnotes.",
          },
          {
            href: "/product/architecture",
            title: "How it is built",
            description:
              "The control plane, the engine, and where the boundary between them falls.",
          },
          {
            href: "/privacy",
            title: "What we hold about you",
            description:
              "Including the application itself: what is stored, who reads it, and when it expires.",
          },
        ]}
      />
    </PageShell>
  );
}
