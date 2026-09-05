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
import { BACKUP_RECOVERY, LOG_RETENTION } from "@/lib/legal-facts";
import {
  NOT_ENGAGED,
  SUBPROCESSOR_CHANGES,
  SUBPROCESSORS,
  SUBPROCESSORS_REVIEWED,
} from "@/lib/subprocessors";

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

/** A dated entry in a document's own history, newest first. */
function ChangeLog({ entries }: { entries: { date: string; change: string }[] }) {
  return (
    <ul className="mt-12 flex flex-col border-t border-black/10 max-md:mt-8">
      {entries.map((entry) => (
        <li
          key={`${entry.date}-${entry.change}`}
          className="flex gap-x-8 border-b border-black/10 py-4 max-md:flex-col max-md:gap-y-1"
        >
          <span className="w-[160px] shrink-0 font-mono text-[13px] leading-6 tracking-snug text-black max-md:w-full">
            {entry.date}
          </span>
          <span className="text-[16px] leading-7 tracking-extra-tight text-gray-new-40">
            {entry.change}
          </span>
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


/** A published number spelled as a word, at the start of a sentence. The words
 *  live in legal-facts.ts so a test can hold them to the Terraform that sets
 *  them, and they are lower case there because most of their uses are mid
 *  sentence. */
function cap(word: string): string {
  return word.charAt(0).toUpperCase() + word.slice(1);
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
            <Button href="/dpa" theme="outlined">
              Data Processing Agreement
            </Button>
            <Button href="/data-retention" theme="outlined">
              Retention and deletion
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
            integration, and it is active only where <code>AF_STRIPE_SECRET_KEY</code> and{" "}
            <code>AF_STRIPE_WEBHOOK_SECRET</code> are set. Where they are, Stripe holds the
            customer, subscription and invoice records for that deployment and is a processor for
            it, listed on the <Link prefetch={false} href="/subprocessors">subprocessor page</Link>. Where they are
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
          title="<strong>It counts page views itself,</strong> and it will stop if you say so."
        />
        <Prose className="mt-10">
          <p>
            There is no Google Analytics here, no PostHog, no Sentry, and no script from any other
            origin. What there is, is a counter this repository wrote, sending to this project&rsquo;s
            own control plane. It exists so that the question &ldquo;does anybody read the docs&rdquo;
            has an answer, and it is built to answer that question and no other.
          </p>
          <p>
            Five things leave your browser: a page shape from a closed list, a channel from a closed
            list, a random identifier for one browsing session, a timestamp, and a campaign tag when
            you followed a link carrying one. The referrer and the URL are turned into those bounded
            values <em>in your browser</em>, so the address you came from is never put on the
            network at all. There is no cookie. The session identifier lives in{" "}
            <code>sessionStorage</code>, ends after thirty minutes of inactivity and after a day
            whatever happens, and nothing here can join two of your visits together.
          </p>
          <p>
            Global Privacy Control and Do Not Track are both honoured without asking. The switch
            below is for everybody else, and it takes effect on the page you are reading rather than
            on the next one: anything captured and not yet sent is thrown away with it.
          </p>
        </Prose>
        <MeasurementSwitch />
      </PageSection>
      <PageSection>
        <CounselNotice>
          This notice describes the architecture and the code as they stand. It is not a
          counsel-reviewed privacy policy, and it names no legal entity, because there is not yet a
          generally available control plane for one to contract about.
        </CounselNotice>
        <Prose className="mt-10">
          <p>
            Signing in creates a session record and grants membership of the organization the
            GitHub App was installed on. The three documents that a security review asks for
            by name are now drafted rather than promised: the{" "}
            <Link prefetch={false} href="/dpa">Data Processing Agreement</Link>, the{" "}
            <Link prefetch={false} href="/subprocessors">subprocessor list</Link>, and the{" "}
            <Link prefetch={false} href="/data-retention">retention and deletion commitments</Link>. Read them as the
            current shape of the answer, not as a signed one.
          </p>
        </Prose>
      </PageSection>
      <RelatedGrid
        items={[
          {
            href: "/dpa",
            title: "Data Processing Agreement",
            description: "Roles, security measures, and what we cannot yet do.",
          },
          {
            href: "/subprocessors",
            title: "Subprocessors",
            description: "Who receives data, and who deliberately does not.",
          },
          {
            href: "/data-retention",
            title: "Retention and deletion",
            description: "How long each thing is kept, and how it goes away.",
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
            <Button href="/sla" theme="outlined">
              Service levels
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
                "Nothing here is a service level commitment. There is none, and the reasons are set out on the service levels page rather than left for a customer to discover.",
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
            more than is true: the verification scan reads the column types that can hold a
            sentence, and samples rows rather than reading every row. It is a check that a masking
            rule missed a column entirely, which is the failure it is built for. It is not a proof
            that no personal data survives anywhere in a schema, and it is not offered as one.
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
          { href: "/sla", title: "Service levels", description: "There is no SLA. Here is what there is." },
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
          { href: "/sla", title: "Service levels", description: "There is no SLA. Here is what there is." },
        ]}
      />
    </PageShell>
  );
}

export function DpaPage() {
  return (
    <PageShell>
      <PageHero
        path="/dpa"
        eyebrow="Data Processing Agreement"
        title="The terms under which we would process data on your behalf."
        lead="A draft, published before there is anything to sign, so that a security review can read it now and tell us where it is wrong. Its subprocessor annex is a separate page because that is the part that goes stale."
        actions={
          <>
            <Button href="/subprocessors" theme="outlined">
              Subprocessors
            </Button>
            <Button href="/data-retention" theme="outlined">
              Retention and deletion
            </Button>
          </>
        }
      />
      <PageSection>
        <CounselNotice>
          No lawyer has read this. It is drafted from the code rather than from a template, which
          makes it accurate about what the system does and says nothing about whether it is
          enforceable. It must be reviewed before anybody relies on it.
        </CounselNotice>
      </PageSection>
      <PageSection tone="ruled">
        <PageHeading kicker="Parties" title="<strong>Who is agreeing, and under which law.</strong>" />
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              ["Processor", "Antifailure, trading as the legal entity named on the signed copy."],
              ["Controller", "The customer organization named on the order form."],
              [
                "Governing law",
                "Not yet chosen. A jurisdiction stated here before it is decided would be a guess in a contract.",
              ],
              [
                "Contact for data protection",
                "No address at antifailure.dev can receive mail: the domain publishes no mail exchanger. Security reports go through GitHub private vulnerability reporting, and the contact page lists the routes that resolve today. A privacy address will be published with the signed copy.",
              ],
            ]}
          />
        </div>
        <Prose className="mt-10">
          <p>
            Four values are missing because no part of this product knows them: the registered
            entity <Blank>entity name</Blank>, its address <Blank>registered address</Blank>, the
            governing law and venue <Blank>jurisdiction</Blank>, and the privacy contact address{" "}
            <Blank>privacy contact</Blank>. They are left visibly blank rather than filled with a
            plausible default, because a plausible default in a contract is the kind of error nobody
            catches on a second reading.
          </p>
        </Prose>
      </PageSection>
      <PageSection>
        <PageHeading
          kicker="Roles"
          title="<strong>Who controls what.</strong> Not everything here is yours, and saying so is part of the agreement."
        />
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              [
                "You control, we process",
                "Account records, organization and repository metadata, policy configuration, run events, and audit entries for your organization. We act only on your instructions, which are the product's own documented operations.",
              ],
              [
                "You control, we never receive",
                "Production snapshots, secrets, captured request bodies before redaction, and raw logs from the twin. These stay inside your cloud by architecture, not by a promise in this document.",
              ],
              [
                "We control",
                "The account and session records signing in creates, what somebody leaves on the contact form, and our own operational records about running the service.",
              ],
              [
                "Purpose limitation",
                "The data is used to run the service, to keep it secure, and to bill for it if there is ever billing. It is not used to train any model, and no path in this product sends it anywhere that would.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="panel">
        <PageHeading
          kicker="Security"
          title="<strong>The measures that exist</strong>, described as what the code does rather than as a category."
        />
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              [
                "Tenant isolation",
                "Every tenant table carries a row-level security policy keyed on the organization, enforced by the database rather than by a query the application has to remember to filter. The event partitions carry the policy too, so naming one directly is isolated by the same rule.",
              ],
              [
                "Least privilege in the database",
                "The application role cannot run DDL. Partition maintenance runs as a separate migration role on a connection opened for the pass and closed after it, because a role that can alter a table can drop the policies that isolate tenants.",
              ],
              [
                "Secrets",
                "Database URLs, the sign-in client secret, and the GitHub App private key live in Azure Key Vault and are read by a managed identity. The storage account for masked dumps has shared key access disabled, so every read is attributable to an identity.",
              ],
              [
                "Model provider keys",
                "Encrypted at rest with a key the control plane holds separately, decrypted for the length of a single request, and never written to a log. Without that key configured, the control plane refuses to store a provider key at all rather than storing it weakly.",
              ],
              [
                "Sessions",
                "Stored as a hash, never as the token. Absolute lifetime of thirty days with no extension, and expired rows are deleted by a sweep that runs every five minutes.",
              ],
              [
                "Audit log",
                "Hash chained, so an entry cannot be altered or removed without breaking the verification of every entry after it.",
              ],
              [
                "Transport",
                "HTTPS only. The engine's control plane client refuses a non-HTTPS endpoint outright, and refuses to send at all unless a redactor is attached.",
              ],
              [
                "Recoverability",
                `${cap(BACKUP_RECOVERY.production.words)} days of point-in-time recovery on the production database, with geo-redundant backup storage. Staging keeps ${BACKUP_RECOVERY.staging.words} days in one region. A restore is verified against a manifest taken at backup time and then asked, through the unprivileged role, to refuse a cross-tenant read.`,
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection>
        <PageHeading title="<strong>And the measures that do not exist yet.</strong>" />
        <Ledger
          items={[
            "No SOC 2 report, no ISO 27001 certificate, and no third-party penetration test. None is claimed anywhere on this site.",
            "Monitoring we will not vouch for from here. Alert rules, an availability test and runbooks are written and version controlled, and production is configured to create them (alerting_enabled is true in infra/terraform/stacks/control-plane/production.tfvars). Whether that configuration has been applied to the live subscription is not something you can check from outside this company, and it is not something this page will assert on your behalf. Ask for the evidence and it will be produced or the claim withdrawn.",
            "No self-service account or organization deletion. Closing an account or removing an organization is carried out by hand against the database by somebody who can reach it. Two things you can do yourself: export your audit log, which is an endpoint, and delete a stored model provider key, which is also an endpoint. There is no self-service export of anything else.",
            "No SLA, no support commitment, and no published uptime history. Production is deployed and answering, at app.antifailure.dev, with a separate staging deployment at app.dev.antifailure.dev. Access is invitation only. What does not exist is anything you could hold us to about how long it stays up.",
          ]}
        />
        <Prose className="mt-10">
          <p>
            The point of listing these is that a security review will find every one of them. Better
            it finds them here, next to the measures that are real, than in a questionnaire answer
            that has to be walked back. The <Link prefetch={false} href="/sla">service levels page</Link> sets out what
            would have to change first.
          </p>
          <p>
            One of them had to be walked back here first. This list read &ldquo;No production
            deployment. What exists is a staging control plane behind a sign-in allowlist,&rdquo;
            and production was deployed and answering the whole time:{" "}
            <code>app.antifailure.dev/readyz</code> returns ready, and staging is a separate, newer
            deployment at <code>app.dev.antifailure.dev</code> serving a different commit. A
            reviewer checking the address our own README gives them disproves that sentence in
            thirty seconds, and then has cause to doubt every other line on a page whose only asset
            is that it can be checked. It is corrected above rather than deleted, because a page
            that quietly drops the item it got wrong is worth less than one that says which item it
            was.
          </p>
        </Prose>
      </PageSection>
      <PageSection tone="ruled">
        <PageHeading
          kicker="Obligations"
          title="<strong>What we would owe you</strong>, and how quickly the honest answer is a range."
        />
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              [
                "Subprocessors",
                "The current list is published and dated. Thirty days written notice before a new subprocessor begins processing, with a right to object during that period. See the subprocessor page for how the notice actually reaches you today.",
              ],
              [
                "Breach notification",
                "Without undue delay after becoming aware of a personal data breach. The qualifier matters and is not boilerplate: with no continuous monitoring in place, becoming aware can lag the event, and no number here can honestly say by how much.",
              ],
              [
                "Data subject requests",
                "Assistance with access, correction, and erasure requests. Executed by hand, because there is no self-service path, so the practical turnaround is days rather than seconds.",
              ],
              [
                "Audit and information",
                "The data plane is open source and can be read rather than described. For the control plane, the answer today is a conversation and this documentation, not an audit report.",
              ],
              [
                "Return and deletion",
                "On termination, control plane records for the organization are deleted on request. Backups age out on their own schedule, which is set out on the retention page.",
              ],
              [
                "International transfers",
                "Processing happens in the United States, in the Azure Central US region, which the infrastructure code enforces rather than assumes. A transfer mechanism for customers outside the United States is not yet in place.",
              ],
            ]}
          />
        </div>
        <Prose className="mt-10">
          <p>
            The transfer mechanism is the fifth missing value:{" "}
            <Blank>transfer mechanism</Blank>. Standard contractual clauses are the usual answer and
            none have been executed, so this document does not claim them.
          </p>
        </Prose>
      </PageSection>
      <RelatedGrid
        items={[
          {
            href: "/subprocessors",
            title: "Subprocessors",
            description: "The annex to this agreement, kept on its own page.",
          },
          {
            href: "/data-retention",
            title: "Retention and deletion",
            description: "The periods this agreement refers to.",
          },
          { href: "/privacy", title: "Privacy Notice", description: "What we collect and never take." },
        ]}
      />
    </PageShell>
  );
}

export function SubprocessorsPage() {
  const always = SUBPROCESSORS.filter((s) => s.engagement === "always");
  const conditional = SUBPROCESSORS.filter((s) => s.engagement === "conditional");

  return (
    <PageShell>
      <PageHero
        path="/subprocessors"
        eyebrow="Subprocessors"
        title="Everyone who receives data, and everyone who deliberately does not."
        lead={`Established by reading the code that talks to each vendor, not by recalling what a product like this usually uses. Last checked against the code on ${SUBPROCESSORS_REVIEWED}.`}
        actions={
          <>
            <Button href="/dpa" theme="outlined">
              Data Processing Agreement
            </Button>
            <Button href="/privacy" theme="outlined">
              Privacy Notice
            </Button>
          </>
        }
      />
      <PageSection>
        <PageHeading
          kicker="Engaged for every organization"
          title="<strong>Two vendors receive data from every deployment.</strong>"
        />
        {always.map((vendor) => (
          <div key={vendor.name} className="mt-14 max-md:mt-10">
            <h3 className="mb-5 text-[22px] leading-snug tracking-extra-tight text-black max-md:text-[19px]">
              {vendor.name}
            </h3>
            <SpecTable
              rows={[
                ["Services", vendor.service],
                ["Purpose", vendor.purpose],
                ["Data received", vendor.data],
                ["Where", vendor.location],
                ["Established from", vendor.evidence],
              ]}
            />
          </div>
        ))}
      </PageSection>
      <PageSection tone="panel">
        <PageHeading
          kicker="Engaged only under a condition"
          title="<strong>Model providers receive nothing unless you give us a key.</strong>"
        />
        <Prose className="mt-10">
          <p>
            Model-driven planning is optional. With no provider key stored, the engine plans
            deterministically and no request leaves for either vendor. When the engine calls a model
            directly from your own continuous integration with your own key, that is your
            relationship with the vendor rather than ours. These two are listed as our subprocessors
            for the case that matters to a review: a request that transits the hosted control plane.
          </p>
        </Prose>
        {conditional.map((vendor) => (
          <div key={vendor.name} className="mt-14 max-md:mt-10">
            <h3 className="mb-5 text-[22px] leading-snug tracking-extra-tight text-black max-md:text-[19px]">
              {vendor.name}
            </h3>
            <SpecTable
              rows={[
                ["Services", vendor.service],
                ["Purpose", vendor.purpose],
                ["Data received", vendor.data],
                ["Where", vendor.location],
                ["Engaged when", vendor.condition ?? ""],
                ["Established from", vendor.evidence],
              ]}
            />
          </div>
        ))}
      </PageSection>
      <PageSection tone="ruled">
        <PageHeading
          kicker="Not engaged"
          title="<strong>The vendors a reviewer asks about next</strong>, and why each one is absent."
        />
        <div className="mt-14 max-md:mt-10">
          <SpecTable rows={NOT_ENGAGED} />
        </div>
      </PageSection>
      <PageSection>
        <PageHeading kicker="Notice" title="<strong>How you find out when this list changes.</strong>" />
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              [
                "Today",
                "This page and the log below. The list is a single file in a public repository, so every change to it is a public commit with a date and an author, and can be watched without asking us.",
              ],
              [
                "Under a signed agreement",
                "Thirty days written notice to the address on the order form before a new subprocessor begins processing, and a right to object within that period.",
              ],
              [
                "Why the difference",
                "Nothing in this product can send an email. That is a fact about the code rather than a policy, and promising a notification that nothing could deliver is the failure this page exists to avoid.",
              ],
            ]}
          />
        </div>
        <Prose className="mt-10">
          <p>
            The thirty day period is a drafted commitment rather than a negotiated one. It is the
            common term and it is written here so that a review has something concrete to accept or
            push back on.
          </p>
        </Prose>
      </PageSection>
      <PageSection tone="ruled">
        <PageHeading title="<strong>Change log.</strong>" />
        <ChangeLog entries={SUBPROCESSOR_CHANGES} />
      </PageSection>
      <RelatedGrid
        items={[
          {
            href: "/dpa",
            title: "Data Processing Agreement",
            description: "The agreement this list annexes.",
          },
          { href: "/privacy", title: "Privacy Notice", description: "What we collect and never take." },
          {
            href: "/data-retention",
            title: "Retention and deletion",
            description: "How long each thing is kept.",
          },
        ]}
      />
    </PageShell>
  );
}

export function ServiceLevelsPage() {
  return (
    <PageShell>
      <PageHero
        path="/sla"
        eyebrow="Service levels"
        title="There is no service level agreement."
        lead="A control plane is deployed and it is invitation only, so there is nothing generally available to make an agreement about. Rather than leave a security review to discover that, this page says what is not committed, what holds anyway, and what would have to be true before a number here meant anything."
        actions={
          <>
            <Button href="/terms" theme="outlined">
              Terms of Use
            </Button>
            <Button href="/docs" theme="outlined">
              Read the docs
            </Button>
          </>
        }
      />
      <PageSection>
        <PageHeading kicker="Not committed" title="<strong>None of this is promised.</strong>" />
        <Ledger
          items={[
            "No uptime target, and no measured uptime to quote instead.",
            "No support response time, and no support tier to attach one to.",
            "No service credits, because there is nothing to credit against.",
            "No status page.",
            "No on-call rotation. An outage today reaches a person when a person happens to look.",
          ]}
        />
      </PageSection>
      <PageSection tone="panel">
        <PageHeading
          kicker="True anyway"
          title="<strong>The part that survives our outage.</strong> The thing that gates your pull request does not run here."
        />
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              [
                "The verdict is local",
                "The engine runs inside your own continuous integration and reaches a verdict there. No control plane is required to run it, and none is configured on most runs.",
              ],
              [
                "An outage cannot fail your build",
                "Events buffer in memory, spill to a durable spool on disk, and are delivered by a later command. When the buffer is full the oldest events are dropped and the count is reported, because an environment must not stall because a dashboard is down.",
              ],
              [
                "Proven, not asserted",
                "A chaos test runs a real command through the real orchestrator against a control plane that is genuinely unreachable, asserts the command did not fail, and asserts the control plane really did receive nothing.",
              ],
              [
                "Nothing phones home",
                "There is no license server and no activation call. The enterprise edition reads a key from the environment. Nothing expires.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="ruled">
        <PageHeading
          kicker="What is deployed"
          title="<strong>A production control plane and a staging one</strong>, and only two people can sign in to either."
        />
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              [
                "Environment",
                "Two: production at app.antifailure.dev and staging at app.dev.antifailure.dev, in separate resource groups, with separate databases and separate GitHub OAuth applications. Both are behind a sign-in allowlist naming the same two accounts. Production is reached only by promoting the exact image digest staging tested, behind an approval on a GitHub environment.",
              ],
              [
                "Redundancy",
                "Production is configured for two application replicas and a zone-redundant database standby. Staging runs one replica and no high availability, deliberately, because a post-deploy health probe measuring a cold start measures nothing. The figures on this row are the ones the production stack declares, in infra/terraform/stacks/control-plane/production.tfvars.",
              ],
              [
                "Backups",
                `Production is configured for ${BACKUP_RECOVERY.production.words} days of point-in-time recovery with geo-redundant backup storage, so a region losing its storage does not take the backups with it. Staging keeps ${BACKUP_RECOVERY.staging.words} days in one region with geo-redundancy off. A standby is not a backup: a bad migration reaches it instantly.`,
              ],
              [
                "Monitoring",
                "Metric alert rules and an action group are in the infrastructure and are enabled on production and off on staging, on purpose, because staging is meant to break several times a week and a page for that is a page somebody learns to ignore. Each rule's description carries the URL of its own runbook. Nobody is on call, so an alert reaches a mailbox rather than a person who is awake.",
              ],
              [
                "Recovery time",
                "The restore drill now runs weekly against a real Postgres. It has reported under two seconds on a continuous integration runner and up to 160 seconds on a loaded laptop, and neither number is a recovery time objective: the only one that would mean anything is measured on the hardware you would actually recover onto.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection>
        <PageHeading title="<strong>What has to be true before there is an SLA.</strong>" />
        <Ledger
          items={[
            "A production environment, separate from staging, with its own credentials and its own sign-in application. In place.",
            "High availability on the database and more than one application replica. Configured on production.",
            "Geo-redundant backup, and a restore drill that runs on a schedule and fails loudly. Configured, and the drill runs weekly.",
            "Alerting that reaches a person, and a runbook per alert that the alert actually points at. The rules and the runbooks exist; who they reach is a mailbox, not a rotation.",
            "On-call, even if it is one person with a phone. Not yet.",
            "A status page, and enough measured history behind it for a number to mean something. Not yet: the probe runs, and its output is not published anywhere.",
          ]}
        />
        <div className="mt-14 max-md:mt-10">
          <CounselNotice>
            An availability commitment is a contractual term, not a documentation change. This page
            describes the current state so that nobody has to infer it. It is not itself a
            commitment, and the wording of any future one needs counsel.
          </CounselNotice>
        </div>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/terms", title: "Terms of Use", description: "The promise is evidence, not zero-failure." },
          {
            href: "/dpa",
            title: "Data Processing Agreement",
            description: "The security measures that do and do not exist.",
          },
          { href: "/pricing", title: "Pricing", description: "Community, team, and enterprise." },
        ]}
      />
    </PageShell>
  );
}

export function DataRetentionPage() {
  return (
    <PageShell>
      <PageHero
        path="/data-retention"
        eyebrow="Retention and deletion"
        title="How long each thing is kept, and how it goes away."
        lead="Every period below is one the running system already enforces, or one this page says plainly that it does not. A retention promise the code cannot keep is worse than no promise, because somebody plans around it."
        actions={
          <>
            <Button href="/privacy" theme="outlined">
              Privacy Notice
            </Button>
            <Button href="/dpa" theme="outlined">
              Data Processing Agreement
            </Button>
          </>
        }
      />
      <PageSection>
        <PageHeading kicker="Periods" title="<strong>What is kept, and for how long.</strong>" />
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              [
                "Run events",
                "A whole number of months, set per deployment. The staging control plane keeps twelve. Unset would keep everything forever, and this is stated because it is the default the software ships with.",
              ],
              [
                "Account and organization records",
                "For as long as the account exists. There is no automatic expiry, and pretending otherwise would be inventing a sweep that nothing runs.",
              ],
              [
                "Audit entries",
                "For the life of the organization. The log is hash chained, so removing one entry breaks the verification of every entry after it. Selective deletion from the audit log is therefore not offered rather than quietly unreliable.",
              ],
              [
                "Sessions",
                "Thirty days at the most, with no extension. Expired rows are deleted by a sweep every five minutes. The stored value is a hash, so an expired row does not hold a usable token even before it is swept.",
              ],
              [
                "Command line sign-in codes",
                "Fifteen minutes of validity, and the row is removed twenty four hours after it expires. Expiry is checked on every read, so a late sweep costs table size and nothing else.",
              ],
              [
                "Model provider keys",
                "Removal is an endpoint you can call, not a request you have to send us, and it stops the key working immediately. It marks the record revoked rather than deleting the row, so the encrypted value remains until the row is removed with the organization. Rotating a key does the same to the one it replaces.",
              ],
              [
                "Contact form messages",
                "Kept until you ask for removal, which is carried out by hand: the role serving public requests holds insert and no select on that table, so there is deliberately no endpoint that reads one back or deletes one. An operator reads the queue on a separate credential and marks each one handled, which is how a request for removal reaches somebody.",
              ],
              [
                "Careers applications",
                "Removed from the live database by the scheduled maintenance pass once they are older than 180 days, whether reviewed or not. An operator can remove an application sooner. If maintenance fails, removal waits for the next successful pass. Audit records retain a record identifier and action, not the applicant's answers. Existing backups expire on their separate recovery schedule.",
              ],
              [
                "Database backups",
                `${cap(BACKUP_RECOVERY.production.words)} days of point-in-time recovery on production, ${BACKUP_RECOVERY.staging.words} on staging. A deletion is reflected in every backup only after that window has passed.`,
              ],
              [
                "Analytics events",
                "Raw analytics events are kept for as long as the deployment sets, and the daily counts computed from them outlive that: a count of page views by channel has nothing in it that identifies anybody. An event carries a keyed hash of the organization rather than its identifier, so the store can count organizations and cannot name one.",
              ],
              [
                "Operational logs",
                `${cap(LOG_RETENTION.production.words)} days on production and ${LOG_RETENTION.staging.words} on staging, in Azure Monitor. They hold request paths, status codes and timings, and never a request body, a token or a snapshot.`,
              ],
              [
                "Masked dumps",
                "No deployment stores any today. The storage account for them is not created unless it is explicitly enabled. If it is enabled, a deleted blob is recoverable for thirty days.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="panel">
        <PageHeading
          kicker="Precision"
          title="<strong>What the event retention actually does</strong>, including the part that is not exact."
        />
        <Prose className="mt-10">
          <p>
            The events table is partitioned by month on the timestamp the sender stamped, and
            retention drops whole partitions. A daily pass creates the months ahead first and
            unconditionally, because a partitioned table with no partition for an incoming row does
            not slow down, it fails. Only then does it drop what the retention window has condemned.
          </p>
          <p className="mt-5">
            Two consequences follow, and both are stated because a customer who plans around a
            precise number would be planning around the wrong one.
          </p>
        </Prose>
        <Ledger
          items={[
            "Granularity is a month, not a day. An event survives until the whole month it occurred in falls outside the window, so it can outlive a twelve month retention by up to a month and a day.",
            "A drop is permanent. Archiving a month to a file before dropping it is supported by the code and is not switched on in any deployment, so a dropped month is gone rather than moved.",
            "A late event that arrived after its month was already gone lands in a default partition and is pruned by age, a bounded number of rows per pass, rather than dropped with its month.",
            "If the daily pass fails, nothing is dropped. An archive that fails costs a retention run rather than the events.",
          ]}
        />
      </PageSection>
      <PageSection tone="ruled">
        <PageHeading kicker="Deletion" title="<strong>How to have data deleted, and what happens then.</strong>" />
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              [
                "How to ask",
                "Write to the privacy contact once it is published. Until then, open a GitHub private vulnerability report, which reaches a person who can act on it without posting anything publicly. Mail is not a route: the domain publishes no mail exchanger, so a request sent to any address at antifailure.dev is delivered nowhere.",
              ],
              [
                "What happens",
                "Somebody with database access carries the request out by hand. There is no account deletion endpoint and no organization deletion endpoint, so this page does not describe a self-service path that does not exist.",
              ],
              [
                "How long it takes",
                "Days rather than seconds, and no shorter commitment is made while the work is manual.",
              ],
              [
                "Backups",
                `A deletion applies to the live database immediately and to backups only as they age out, over the ${BACKUP_RECOVERY.production.words} day recovery window on production and ${BACKUP_RECOVERY.staging.words} on staging. Restoring a backup within that window restores the deleted rows, and any deletion request is applied again afterwards.`,
              ],
              [
                "The audit log",
                "Entries about an organization are removed with the organization. They are not removed individually, for the chaining reason above.",
              ],
              [
                "A person who asks to be removed",
                "Their personal fields are erased and the account row is kept. The row is kept by choice, not because the database refuses: the audit log references it with ON DELETE SET NULL and the delete would succeed. What it would also do is set a column that is inside the hash chain to null, so every entry that person ever wrote would stop hashing to its recorded hash and the organization\u2019s audit log would report itself as altered. Erasing the fields removes the personal data; deleting the row would remove the ability to prove nothing else had been changed.",
              ],
            ]}
          />
        </div>
        <Prose className="mt-10">
          <p>
            The privacy contact is the one value this page cannot supply:{" "}
            <Blank>privacy contact</Blank>. It is left blank rather than pointed at an address
            nobody monitors.
          </p>
        </Prose>
      </PageSection>
      <PageSection>
        <CounselNotice>
          These are the periods the software enforces today, written so that a review can check them
          against the running system. Whether they satisfy a particular regulation is a question for
          counsel, and no compliance claim is made here.
        </CounselNotice>
        <Prose className="mt-10">
          <p>
            An operator running their own control plane sets these periods themselves. The variables
            and what each one does are in the{" "}
            <a href="/docs/reference/control-plane">control plane reference</a>.
          </p>
        </Prose>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/privacy", title: "Privacy Notice", description: "What we collect and never take." },
          {
            href: "/dpa",
            title: "Data Processing Agreement",
            description: "Roles, security measures, and obligations.",
          },
          {
            href: "/subprocessors",
            title: "Subprocessors",
            description: "Who receives data, and who deliberately does not.",
          },
        ]}
      />
    </PageShell>
  );
}
