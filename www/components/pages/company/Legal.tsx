import type { ReactNode } from "react";
import Link from "next/link";
import { Button } from "@/components/layout/Button";
import { MeasurementSwitch } from "@/components/MeasurementSwitch";
import {
  Blank,
  Callout,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  Prose,
  RelatedGrid,
  SpecTable,
} from "@/components/pages/kit";
import { cn } from "@/lib/cn";
import { CONDITIONAL_PROCESSORS } from "@/lib/legal-facts";

const NOT_CLAIMED = [
  "Zero rollback. No deployment can ever fail.",
  "Perfect clones of every cloud topology.",
  "Open source as a substitute for compliance.",
  "A generally available production control plane.",
];

/**
 * A list of flat statements, one per row.
 *
 * The terms page had this inline. Four pages want it now, and four copies of a
 * border rule is how two of them end up a pixel apart from the other two.
 *
 * `measure` is opt in rather than the default, and the reason is the two
 * different jobs this list does. The original rows are fragments, four or five
 * words each, and a rule that stopped at 720px beside them would read as a
 * short line rather than a considered measure. The acceptable use and developer
 * policy rows are whole sentences, and at the 1600px container they set a line
 * roughly 190 characters long, which is unreadable and looked it. 720px is the
 * width `Prose` and the counsel notice already use, so this is the system's own
 * measure rather than a third one invented here.
 */
function Ledger({ items, measure }: { items: string[]; measure?: boolean }) {
  return (
    <ul
      className={cn(
        "mt-12 flex flex-col border-t border-black/10 max-md:mt-8",
        measure && "max-w-[720px]",
      )}
    >
      {items.map((item) => (
        <li
          key={item}
          className="border-b border-black/10 py-4 text-[17px] leading-snug tracking-extra-tight text-black max-md:text-[16px]"
        >
          {item}
        </li>
      ))}
    </ul>
  );
}

/** The line every one of these pages carries, in one place so they agree. */
function CounselNotice({ children }: { children: ReactNode }) {
  return (
    <div className="max-w-[720px]">
      <Callout label="Drafted, not reviewed by counsel" tone="warn">
        {children}
      </Callout>
    </div>
  );
}


/**
 * The environment variables that switch one conditional processor on, as the
 * page reads them out loud.
 *
 * Typed into the prose by hand until legal-facts.ts lost its last importer and
 * tools/gatecheck said so. A published claim naming a variable, beside a list a
 * test holds to the code that reads it, is two copies of the same fact, and the
 * page was the copy nothing checked. Now there is one copy and the page renders
 * it, so renaming the variable moves the sentence with it.
 */
function switchedOnBy(vendor: string): string[] {
  return CONDITIONAL_PROCESSORS.find((p) => p.vendor === vendor)?.variables ?? [];
}

export function PrivacyPage() {
  return (
    <PageShell>
      <PageHero
        path="/privacy"
        eyebrow="Privacy Notice"
        title="Production data stays in the customer boundary."
        lead="The hosted control plane holds organizations, policy, aggregated reports, and plan limits. Raw snapshots, secrets, and captured request bodies stay in your cloud by default."
        actions={
          <>
            <Button href="/terms" theme="outlined">
              Terms of Use
            </Button>
            <Button href="/docs/security/data-boundary" theme="outlined">
              The data boundary
            </Button>
          </>
        }
      />
      <PageSection>
        <PageHeading
          kicker="Trust boundary"
          title="<strong>Two planes.</strong> Evidence can leave. Records of production should not."
        />
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              [
                "Control plane",
                "Organization metadata, account names and emails, GitHub identifiers, session records including IP address and browser user agent, policy configuration, aggregated reports, historical comparisons, audit entries, and the plan that sets an organization's limits.",
              ],
              [
                "Your boundary",
                "Raw snapshots, secrets, captured request bodies until redacted, raw logs and traces, sanitization, provisioning, egress enforcement, and cleanup.",
              ],
              [
                "Careers applications",
                "Name, email, selected role, an optional work link, your introduction, and acknowledgment of the current compensation. These are stored separately for recruitment review by an authorized operator, not added to customer analytics or a mailing list. No applicant IP address or browser user agent is stored in the application record. Applications expire through scheduled maintenance after 180 days, or an operator can delete them sooner. Backups expire separately. Contact us privately to request removal and include your application reference.",
              ],
              [
                "This site",
                "Nothing, until you use the contact form. That writes your name, work email, company, an optional seat count and your message into the control plane's own database, with the page it came from and the time. The role that serves public requests can insert into that table and cannot read it back, so no request to this site can ever return somebody else's contact details.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="ruled">
        <PageHeading title="<strong>Sanitization happens where the data already lives.</strong>" />
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              [
                "Customer-hosted data plane",
                "Masking, subsetting, and credential deletion execute inside your cloud.",
              ],
              [
                "Outbound-only agent",
                "Communication leaves the customer agent where possible, with short-lived credentials.",
              ],
              [
                "No snapshots in the host",
                "The hosted control plane is not a backup target for production-derived state.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="panel">
        <PageHeading title="<strong>No card ever reaches this product.</strong>" />
        <Prose className="mt-10">
          <p>
            That part is unconditional and it is architectural rather than a promise: checkout and
            the billing portal are pages Stripe hosts, so a card is entered on Stripe&rsquo;s own
            form and never passes through anything here. No card details or billing addresses exist
            anywhere in this product and none can.
          </p>
          <p>
            What is conditional is everything else. The control plane contains a real Stripe
            integration, and it is active only where{" "}
            {switchedOnBy("Stripe").map((name, i, all) => (
              <span key={name}>
                <code>{name}</code>
                {i < all.length - 1 ? (i === all.length - 2 ? " and " : ", ") : ""}
              </span>
            ))}{" "}
            are set. Where they are, Stripe holds the
            customer, subscription and invoice records for that deployment and is a processor for
            it. Where they are
            not, the billing routes refuse and name the missing variables, and an organization
            carries nothing but a plan name, which sets its rate limits and quotas. The control
            plane says which of the two it is on the first line it logs when it starts.
          </p>
          <p>
            This page previously said there was no billing at all. That was true when it was
            written and stopped being true when the billing work landed, which is the reason the
            numbers and capabilities on these pages are now checked against the code by a test
            rather than kept in step by hand.
          </p>
        </Prose>
      </PageSection>
      <PageSection tone="ruled">
        <PageHeading
          kicker="This site"
          title="<strong>It counts page views itself,</strong> PostHog watches the rest, and both stop if you say so."
        />
        <Prose className="mt-10">
          <p>
            There are two measurements on this site and one of them has a vendor in it. The first
            is a counter this repository wrote, sending to this project&rsquo;s own control plane.
            The second is PostHog, for autocapture and session replay, and it is here because the
            first one cannot answer where somebody gave up: it sends no address, no element and no
            ordering, deliberately. There is no Google Analytics, no Datadog, no Sentry and no
            crash reporter in anything this repository wrote. Two scripts are fetched while you
            read, and both are worth naming. PostHog&rsquo;s session replay recorder, which comes
            from the endpoint we run rather than from any vendor address. And the booking widget on
            the contact page, which is cal.com&rsquo;s, and whose frame runs its own error
            reporting to Sentry. That last one is their document doing their thing on their origin,
            and it is named here because your browser makes the connection and a page listing what
            it loads should not stop at the ones it likes.
          </p>
          <p>
            Five things leave your browser for the counter: a page shape from a closed list, a
            channel from a closed list, a random identifier for one browsing session, a timestamp,
            and a campaign tag when you followed a link carrying one. The referrer and the URL are
            turned into those bounded values <em>in your browser</em>, so the address you came from
            is never put on the network at all.
          </p>
          <p>
            PostHog sees more, and this is the whole of it: the address and title of the page you
            are on including its query string, the page you arrived from, each route you move to,
            how far down each one you got before leaving it, the clicks and form submissions you
            make along with the tag, classes and visible label of what you clicked, your browser,
            operating system, device type, screen and window size, browser language and timezone,
            and a recording of the pages you visit. A recording holds their structure and styling,
            your cursor, your clicks and your scrolling. Not your raw browser identification
            string, which is stripped before anything is sent, and not your address.
          </p>
          <p>
            <strong>Every value you type is masked before it leaves your browser.</strong> The
            careers form and the contact form ask for a name, a work email, a company and a
            paragraph in your own words, and a recording of either one shows the fields filling up
            with asterisks and never what you wrote. It is not withheld on receipt and it is not
            deleted afterwards: it is replaced in the page, so there is nothing in the recording
            that could be unmasked later.
          </p>
          <p>
            Neither of them sets a cookie, and neither keeps an identifier that outlives this tab,
            so nothing here can join two of your visits. PostHog would do both by default, for a
            year; it is configured here not to, and that choice is what keeps the sentence before
            this one true.
          </p>
          <p>
            <strong>PostHog, Inc. receives all of that, and we will not dress that up.</strong>{" "}
            Your browser does not talk to a posthog.com host: it talks to an endpoint we run at{" "}
            <code>app.antifailure.dev</code>, which forwards. That changes where the request goes
            and not who reads it, so PostHog is a processor for this website and is named as one
            here. The data is processed in the United States, on PostHog Cloud US. What the
            arrangement genuinely buys you is two things.
            A content blocker does not recognise the request, so the numbers are not quietly half
            missing and nobody here is tempted to guess at the gap. And your IP address is not
            forwarded, so PostHog never receives it, which costs us any real geography on those
            dashboards and is worth it.
          </p>
          <p>
            One claim is unaffected and it is a different claim: your production data never leaves
            your own boundary. That is about the engine and the runner, neither of which has an
            analytics client to leave with. This section is about a website you are reading.
          </p>
          <p>
            Global Privacy Control and Do Not Track are honoured by both, without asking. The
            switch below is for everybody else, and it takes effect on the page you are reading
            rather than on the next one: anything captured and not yet sent is thrown away with
            it, and the recording ends. If you arrive with any of those already set, PostHog&rsquo;s
            code is never fetched at all, so there is no recorder that read this page before
            something told it not to.
          </p>
        </Prose>
        <MeasurementSwitch />
      </PageSection>
      <PageSection>
        <Prose>
          <p>
            Signing in creates a session record and grants membership of the organization the
            GitHub App was installed on. That record, the organization and its policy are what the
            control plane keeps. Everything this notice calls production data stays in your cloud.
          </p>
        </Prose>
      </PageSection>
      <RelatedGrid
        items={[
          {
            href: "/terms",
            title: "Terms of Use",
            description: "What the product is allowed to do to your database.",
          },
          {
            href: "/docs/security/data-boundary",
            title: "The data boundary",
            description: "Which plane holds what, and what can cross.",
          },
          {
            href: "/acceptable-use",
            title: "Acceptable Use",
            description: "The few things you may not point this at.",
          },
        ]}
      />
    </PageShell>
  );
}

export function TermsPage() {
  return (
    <PageShell>
      <PageHero
        path="/terms"
        eyebrow="Terms of Use"
        title="A proving ground, not a guarantee."
        lead="The product reports whether a deployment is safe to ship under the conditions it could observe and reproduce. It does not mathematically guarantee that a deployment cannot fail."
        actions={
          <>
            <Button href="/privacy" theme="outlined">
              Privacy Notice
            </Button>
            <Button href="/acceptable-use" theme="outlined">
              Acceptable Use
            </Button>
          </>
        }
      />
      <PageSection>
        <PageHeading
          kicker="Scope"
          title="<strong>Evidence, with its limits stated.</strong> You remain responsible for the permissions you grant."
        />
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              [
                "The promise",
                "Reproduce the highest-risk production conditions we can observe, measure how the proposed system behaves, and expose dangerous differences with concrete evidence.",
              ],
              [
                "Your cloud",
                "You remain responsible for the cloud permissions you grant the agent, the policies you approve, and the production systems those permissions can reach.",
              ],
              [
                "Accounts",
                "Anybody can create one. Signing in with GitHub creates an organization on the free plan, owned by that account, and installing the GitHub App creates or adopts one the same way. The free plan's limits are enforced against it: reaching one refuses the next creation and tears nothing down.",
              ],
              [
                "Paying",
                "A paid plan is bought through Stripe's own hosted checkout and managed in Stripe's customer portal. No card ever reaches this product. These terms are not a paid-service agreement: the contracting entity, the governing law and the liability cap are all still blank below, and a contract with no party to it is not one. A purchase is governed by whatever is agreed in writing at the time.",
              ],
              [
                "The enterprise edition",
                "The source under ee/ is public to read and audit. Running it in production requires a written agreement with Antifailure and a valid licence, which is arranged through the contact form. Its licence used to accept this page as that agreement; it no longer names it, because this page says it is not one.",
              ],
              [
                "Availability",
                "Nothing here is a service level commitment, and there is none.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="panel">
        <PageHeading title="<strong>What these terms do not say.</strong>" />
        <Ledger items={NOT_CLAIMED} />
      </PageSection>
      <PageSection tone="ruled">
        <PageHeading
          kicker="Your side"
          title="<strong>What the software is allowed to touch,</strong> and what decides that."
        />
        <Prose className="mt-10">
          <p>
            The honest version of a liability section starts here rather than at a cap, because
            what the software can reach is a fact about the code and a cap is a guess about a
            court. Each row below is a limit the engine enforces, not a promise it intends to keep.
          </p>
        </Prose>
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              [
                "Your production database",
                "Named by the variable in source_url_env and read, never written. The golden refresh reaches it only through pg_dump, and the subsetting path opens it inside a transaction Postgres has marked read only, so a connection string with more rights than it needs still cannot be written through.",
              ],
              [
                "Your cloud permissions",
                "You remain responsible for the credentials you give the agent and for everything those credentials can reach. Nothing here can narrow a permission you granted.",
              ],
              [
                "Containers and branches",
                "Teardown removes only resources carrying Antifailure's own labels, and refuses anything else by name. A database branch is created and destroyed through the provider's API under a reserved prefix.",
              ],
              [
                "Masking",
                "Runs on every golden. There is no setting that disables it, and a project with no rules file still gets the built-in set. A golden whose verification scan finds real data is never published, so it can never be branched.",
              ],
            ]}
          />
        </div>
        <Prose className="mt-10">
          <p>
            The limit worth stating next to that last row, because a reader would otherwise assume
            more than is true: the verification scan reads every column it can read as text, names
            in its report the ones it cannot and their types, and samples rows rather than reading
            every row. A column it cannot read, that no masking rule covers, and whose name says it
            holds a secret fails the scan rather than passing it. It is a check that a masking rule
            missed a column entirely, which is the failure it is built for. It is not a proof that
            no personal data survives anywhere in a schema, and it is not offered as one.
          </p>
        </Prose>
      </PageSection>
      <PageSection>
        <PageHeading
          kicker="Warranty"
          title="<strong>The software is provided as it is.</strong> A pass is evidence, not insurance."
        />
        <Prose className="mt-10">
          <p>
            To the extent the law allows, the software is provided without warranty of any kind,
            express or implied, including any implied warranty of merchantability, fitness for a
            particular purpose, or non infringement. A run reports what it could observe and
            reproduce under the conditions it created. It does not certify that a deployment is
            correct, and a passing report is not a representation that production will not fail.
          </p>
          <p className="mt-5">
            Some of that exclusion is unenforceable in some places, and against consumers it is
            unenforceable in most. Which parts survive where is a question for counsel and is
            marked as such below rather than asserted here.
          </p>
        </Prose>
      </PageSection>
      <PageSection tone="panel">
        <PageHeading
          kicker="Liability"
          title="<strong>The shape of the cap, with the numbers left out.</strong>"
        />
        <Prose className="mt-10">
          <p>
            Four values decide this section and none of them exists yet, so they are left visibly
            blank rather than filled with something that reads as settled: the contracting entity{" "}
            <Blank>entity name</Blank>, its registered address <Blank>registered address</Blank>,
            the governing law and venue <Blank>jurisdiction</Blank>, and the figure the cap is set
            at <Blank>liability cap</Blank>. A cap written before a lawyer has chosen the
            jurisdiction it will be read in is a number, not a protection.
          </p>
        </Prose>
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              [
                "Excluded, intended",
                "Indirect, incidental, special and consequential loss, and loss of profit, revenue, goodwill or data, to the extent the law allows.",
              ],
              [
                "Capped, intended",
                "Everything else, at a figure tied to what was paid over a stated period. Both the figure and the period are unset.",
              ],
              [
                "Never excluded",
                "Death or personal injury caused by negligence, fraud and fraudulent misrepresentation, and anything else a governing law refuses to let a contract exclude. This carve out is not a courtesy and cannot be drafted away.",
              ],
              [
                "Not addressed here",
                "Whether any of the above is enforceable against a given customer in a given place, which depends on the jurisdiction, on whether the customer is a business or a consumer, and on whether the harm was caused by our own negligence.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection>
        <CounselNotice>
          This page states product limits so that nobody signing up is sold a zero-failure
          guarantee. It is not a substitute for a counsel-reviewed agreement, and it is not an order
          form.
        </CounselNotice>
        <Prose className="mt-10">
          <p>
            When a hosted control plane is generally available, these pages will be replaced with
            dated legal documents that name a contracting entity and a governing law. The{" "}
            <Link prefetch={false} href="/acceptable-use">acceptable use policy</Link> and the{" "}
            <Link prefetch={false} href="/developer-policy">developer policy</Link> are drafted already, because
            both describe what the software does rather than what a company has decided. Until
            then, treat every safety report as evidence about the conditions the run actually
            reproduced, a pass or a fail, not as insurance.
          </p>
        </Prose>
      </PageSection>
      <RelatedGrid
        items={[
          {
            href: "/acceptable-use",
            title: "Acceptable use",
            description: "What the product may not be pointed at.",
          },
          {
            href: "/developer-policy",
            title: "Developer policy",
            description: "The API, the MCP surface, and the tokens that reach them.",
          },
        ]}
      />
    </PageShell>
  );
}

/**
 * The two lists this page is built from.
 *
 * Kept as data rather than prose because an acceptable use policy is read by
 * somebody checking whether one specific thing is allowed, and a paragraph is
 * the wrong shape for that. The first list is what the product may not be
 * pointed at. The second is the half most acceptable use policies leave out:
 * what we will not do to a customer, which is the only part of the document
 * that costs us anything to write.
 */
const NOT_PERMITTED = [
  "Pointing the agent at a system you are not authorised to test. The product exists to exercise an application until it breaks, and doing that to somebody else's is an attack however it is labelled.",
  "Naming a production database in source_url_env that you do not have the right to copy, including one holding another company's data under a contract that does not allow it.",
  "Using a golden as a way to move production data somewhere it is not allowed to go. A masked copy is still derived from the original, and masking is a reduction of risk rather than a change of jurisdiction.",
  "Running the load generator against a third party you do not control. Containment holds inside the environment; a host you allow through it is a host you are sending real traffic to.",
  "Feeding the product data you are prohibited from processing, including special category personal data, payment card data, and anything under an export control you have not cleared.",
  "Reselling access, or running the hosted control plane as a service for others, without a written agreement that says you may.",
];

const WE_WILL_NOT = [
  "Read your production database. The engine reaches a source only through pg_dump and a read only transaction, and no part of the hosted control plane holds a source connection string.",
  "Use your data to train a model. Nothing in this product sends customer data to a model provider for training, and there is no path that would.",
  "Take a snapshot of production into the hosted control plane. It is not a backup target, and the trust boundary is drawn so that it cannot become one.",
  "Enforce this policy by reading your environments. Enforcement is a conversation, because the alternative is a surveillance capability nobody asked for.",
];

export function AcceptableUsePage() {
  return (
    <PageShell>
      <PageHero
        path="/acceptable-use"
        eyebrow="Acceptable Use"
        title="It rehearses failure. Point it only at systems you are allowed to break."
        lead="A short policy, because a long one is a policy nobody reads before doing the thing it prohibits. The product's whole purpose is to drive an application into its worst conditions, which makes where you aim it the only question that matters."
        actions={
          <>
            <Button href="/terms" theme="outlined">
              Terms of Use
            </Button>
            <Button href="/developer-policy" theme="outlined">
              Developer policy
            </Button>
          </>
        }
      />
      <PageSection>
        <PageHeading
          kicker="Prohibited"
          title="<strong>What the product may not be pointed at.</strong>"
        />
        <Ledger items={NOT_PERMITTED} measure />
      </PageSection>
      <PageSection tone="panel">
        <PageHeading
          kicker="The other direction"
          title="<strong>What we will not do,</strong> which is the half worth writing down."
        />
        <Ledger items={WE_WILL_NOT} measure />
      </PageSection>
      <PageSection tone="ruled">
        <PageHeading kicker="Enforcement" title="<strong>What happens if this is broken.</strong>" />
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              [
                "How we would find out",
                "A report, a bill, or a provider telling us. There is no monitoring of what an environment contains, and building one to enforce this policy would cost more privacy than the policy protects.",
              ],
              [
                "What we would do",
                "Ask first. An organization can be suspended, which stops new work and leaves the data in place, and that mechanism exists in the control plane today.",
              ],
              [
                "Immediate suspension",
                "Reserved for an active attack on somebody else, or a legal demand we have to act on. Everything else gets a conversation before anything stops.",
              ],
              [
                "Appeal",
                "Write to the contact on this site. There is no formal appeal process yet and pretending otherwise would be worse than saying so.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection>
        <CounselNotice>
          No lawyer has read this. It describes what the software does and what we intend, and it
          has not been checked for whether it is enforceable or complete. It must be reviewed
          before the product takes money.
        </CounselNotice>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/terms", title: "Terms of Use", description: "Scope, warranty, and liability." },
          {
            href: "/developer-policy",
            title: "Developer policy",
            description: "The API, the MCP surface, and the tokens that reach them.",
          },
          {
            href: "/privacy",
            title: "Privacy Notice",
            description: "What we collect and never take.",
          },
        ]}
      />
    </PageShell>
  );
}

export function DeveloperPolicyPage() {
  return (
    <PageShell>
      <PageHero
        path="/developer-policy"
        eyebrow="Developer Policy"
        title="Two programmable surfaces, and what each one is allowed to do."
        lead="The control plane has an HTTP API, and the engine serves its tools to a model over the Model Context Protocol. They have different threat models, so they get different rules rather than one paragraph covering both."
        actions={
          <>
            <Button href="/terms" theme="outlined">
              Terms of Use
            </Button>
            <Button href="/acceptable-use" theme="outlined">
              Acceptable use
            </Button>
          </>
        }
      />
      <PageSection>
        <PageHeading kicker="The API" title="<strong>Tokens, limits, and what a token can reach.</strong>" />
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              [
                "Authentication",
                "A token belongs to one organization and carries a role. Nothing in the API is reachable without one, and a token is stored as a hash, so a leaked database does not yield a working credential.",
              ],
              [
                "Rate limits",
                "Every public endpoint has one, declared in a single registry that the middleware reads. An endpoint added without a limit is a build failure rather than an endpoint with no limit, which is the usual way this goes wrong.",
              ],
              [
                "Scope",
                "A token reaches its own organization and no other. This is enforced by row level policy in the database rather than only by a check in the application.",
              ],
              [
                "If a token leaks",
                "Revoke it. Revocation is immediate for the API. A token already inside a running engine keeps working until that run finishes, and this is stated because the opposite would be assumed.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="panel">
        <PageHeading
          kicker="The MCP surface"
          title="<strong>A model driving the engine is still you,</strong> for every purpose in these terms."
        />
        <Prose className="mt-10">
          <p>
            The engine can serve its tools to a model over the Model Context Protocol, which means
            a model can create environments, run workloads and tear them down. The rule that
            follows is the one worth stating plainly: an action a model takes through your engine
            is your action. The acceptable use policy applies to it unchanged, and a model
            misreading an instruction is not a defence any more than a script with a bug would be.
          </p>
          <p className="mt-5">
            The MCP server runs on your machine and speaks over a local transport rather than a
            network port. It has whatever access your shell has. Granting a model that surface is a
            decision with the same weight as giving it your terminal, and it should be made the
            same way.
          </p>
        </Prose>
      </PageSection>
      <PageSection tone="ruled">
        <PageHeading kicker="Building on it" title="<strong>What you may do with the interfaces.</strong>" />
        <Ledger
          measure
          items={[
            "Build whatever you like against the API for your own organization, including things we did not anticipate. That is what an API is.",
            "Automate the CLI in your own pipelines. The exit codes and the report format are the contract, and the report is versioned so that a parser does not break silently.",
            "Do not use the API to work around a limit on your plan, including by spreading one workload across organizations.",
            "Do not present output from this product as a certification, an audit, or a guarantee of correctness to a third party. It is evidence about one run under conditions that run created.",
            "Interfaces marked internal or undocumented may change without notice. The documented API and the report schema will not change incompatibly without a version.",
          ]}
        />
      </PageSection>
      <PageSection>
        <CounselNotice>
          No lawyer has read this. It is drafted from the code, so it is accurate about what the
          interfaces do and silent on whether it is enforceable. It must be reviewed before the
          product takes money.
        </CounselNotice>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/terms", title: "Terms of Use", description: "Scope, warranty, and liability." },
          {
            href: "/acceptable-use",
            title: "Acceptable use",
            description: "What the product may not be pointed at.",
          },
        ]}
      />
    </PageShell>
  );
}
