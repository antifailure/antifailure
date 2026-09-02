import Link from "next/link";
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
import { CONTACT_POINTS, REPO_URL } from "@/lib/site";
import { CalBooking } from "./CalBooking";

const CONTACT_DETAILS = {
  security: {
    body: "Open a private vulnerability report in GitHub. The report is visible to repository maintainers, not posted as a public issue. Include the affected component, version, impact, and a reproduction when possible.",
    href: `${REPO_URL}/blob/main/SECURITY.md`,
    link: "Read the security policy",
    action: "Open a private report",
  },
  issues: {
    body: "Use the issue chooser for a reproducible bug, a concrete feature request, or a provider request. Search open and closed issues first so an existing answer or active change is not duplicated.",
    href: `${REPO_URL}/issues`,
    link: "Search existing issues",
    action: "Choose an issue template",
  },
  discussions: {
    body: "Use GitHub Discussions for setup questions, ideas, and non-sensitive licensing questions that do not belong in a bug report. Discussions are public, so do not include secrets or private reports.",
    href: `${REPO_URL}/discussions`,
    link: "Browse discussions",
    action: "Start a discussion",
  },
  waitlist: {
    body: "There is no generally available hosted control plane. Leaving an address stores it on a server, where a person reads it when there is a hosted environment to connect a repository to. Nothing mails you on a schedule and there is no date, so book a call above if you need an answer sooner.",
    href: "/docs/getting-started/quickstart",
    link: "Use the engine today",
    action: "Join the waitlist",
  },
} as const;

/**
 * A link that knows which router owns the destination.
 *
 * `/docs` is built by Astro and merged into the published site afterwards, so
 * next/link cannot route there: it prefetches an RSC payload that does not
 * exist and answers the click with a client-side navigation to nothing.
 * components/layout/Button.tsx already makes exactly this distinction, and the
 * two pages here were the only place in the site that did not.
 */
function Route({
  href,
  className,
  children,
}: {
  href: string;
  className: string;
  children: React.ReactNode;
}) {
  const external = !href.startsWith("/") || href === "/docs" || href.startsWith("/docs/");
  if (external) {
    return (
      <a href={href} className={className}>
        {children}
      </a>
    );
  }
  return (
    <Link href={href} className={className}>
      {children}
    </Link>
  );
}

export function ContactPage() {
  return (
    <PageShell>
      <PageHero
        path="/contact"
        eyebrow="Contact"
        title="Use the route that matches the question."
        lead="Antifailure uses GitHub for private vulnerability reports, public product work, and community discussion. A call can be booked on this page, and it is the only route here that reaches a person on a known day. Hosted-product interest goes on a stored list instead, because the project does not publish a working email channel in either direction."
        actions={
          <>
            <Button href="#book" theme="filled">
              Book a call
            </Button>
            <Button href={`${REPO_URL}/issues`} theme="outlined">
              Search product issues
            </Button>
          </>
        }
      />

      {/* First, and above the four written routes, because it is the only one
          that reaches a person rather than a tracker. The four below are the
          right route for almost every technical question and they are slower
          by design: a public issue is searchable and a call is not. */}
      <PageSection>
        <div id="book" className="scroll-mt-24">
          <PageHeading
            kicker="Book a call"
            title="<strong>Thirty minutes with the person building it.</strong> The times below are real openings, not a form."
          />
          <Prose className="mt-8">
            <p>
              Use this for a hosted evaluation, a design partnership, or
              anything commercial that does not belong in a public issue. A
              security report should still go through{" "}
              <a href={`${REPO_URL}/security/advisories/new`}>
                GitHub private vulnerability reporting
              </a>
              , which is confidential, tracked, and read by the maintainers
              rather than by one calendar.
            </p>
          </Prose>
          <div className="mt-12 max-w-[1040px] max-xl:mt-10">
            <CalBooking />
          </div>
        </div>
      </PageSection>

      <PageSection>
        <PageHeading
          kicker="Contact routes"
          title="<strong>Four routes that resolve today.</strong> Choose based on privacy and purpose."
        />
        <ul className="mt-14 grid grid-cols-2 gap-5 max-md:grid-cols-1">
          {CONTACT_POINTS.map((point) => {
            const detail = CONTACT_DETAILS[point.id];
            return (
              <li
                id={point.id}
                key={point.id}
                className="scroll-mt-24 rounded-[8px] bg-white p-7 ring-1 ring-black/10 max-md:p-6"
              >
                <h2 className="text-[22px] leading-snug tracking-tighter text-black">{point.label}</h2>
                <p className="mt-5 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
                  {detail.body}
                </p>
                {/* A column rather than two inline controls separated by a
                    <br />, so the gap is spacing rather than a line break and
                    the pair stays aligned when either label wraps at 320. */}
                <div className="mt-6 flex flex-col items-start">
                  <Route
                    href={point.url}
                    className="inline-flex min-h-11 items-center rounded-full bg-black px-5 text-[14px] font-medium text-white transition-colors hover:bg-[#292929]"
                  >
                    {detail.action}
                  </Route>
                  <Route
                    href={detail.href}
                    className="mt-3 inline-flex min-h-11 items-center text-[14px] text-black underline decoration-black/20 underline-offset-4"
                  >
                    {detail.link}
                  </Route>
                </div>
              </li>
            );
          })}
        </ul>
      </PageSection>

      <PageSection tone="plain">
        <div className="grid grid-cols-[minmax(0,720px)_minmax(260px,420px)] gap-x-20 gap-y-10 max-lg:grid-cols-1">
          <div>
            <PageHeading
              kicker="Product and documentation"
              title="<strong>Search the public record first.</strong> Then add a reproducible report."
            />
            <Prose className="mt-8">
              <p>
                Product bugs and concrete requests belong in the{" "}
                <a href={`${REPO_URL}/issues`}>GitHub issue tracker</a>. Search open and closed issues
                before filing so an existing answer or active change is not duplicated. For a new
                bug, include the component, version, observed behavior, expected behavior, and the
                smallest reproduction you can provide. Questions and ideas belong in{" "}
                <a href={`${REPO_URL}/discussions`}>GitHub Discussions</a> instead.
              </p>
              <p className="mt-6">
                Setup and operation questions may already be answered in the{" "}
                <a href="/docs">documentation</a>. The <a href={REPO_URL}>repository</a> also
                carries the current README, source, status ledger, contribution guide, and history.
              </p>
            </Prose>
          </div>
          <div className="self-start">
            <Callout label="Email is not a contact route" tone="warn">
              The antifailure.dev domain has no mail exchanger, and its SPF policy authorizes no
              outbound senders. Addresses that still appear in repository documents are therefore
              not presented here as working channels. Use GitHub private vulnerability reporting
              for security details, and keep secrets and private reports out of Issues and
              Discussions.
            </Callout>
          </div>
        </div>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/about", title: "About", description: "Project identity, current status, and stated limits." },
          { href: "/privacy", title: "Privacy", description: "Data boundaries, subprocessors, retention, and controls." },
          { href: "/terms", title: "Terms", description: "The product's stated limits and current commitments." },
        ]}
      />
    </PageShell>
  );
}
