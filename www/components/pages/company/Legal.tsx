import type { ReactNode } from "react";
import Link from "next/link";
import { Button } from "@/components/layout/Button";
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
 */
function Ledger({ items }: { items: string[] }) {
  return (
    <ul className="mt-12 flex flex-col border-t border-black/10 max-md:mt-8">
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
                "This site",
                "One waitlist address per person, stored on a server so that the sentence next to the form is true, with the time it was first and last submitted. There is no way to read the list back through the site.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="white">
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
      <PageSection tone="sage">
        <PageHeading title="<strong>There is no billing, so there are no payment records.</strong>" />
        <Prose className="mt-10">
          <p>
            No payment processor is connected to any part of this product. No card details, billing
            addresses, or invoices exist anywhere in it. An organization carries a plan name, which
            sets its rate limits and quotas, and that is the whole of what a reader might call
            billing data today. If that changes, the processor will appear on the{" "}
            <Link href="/subprocessors">subprocessor list</Link> before it processes anything.
          </p>
        </Prose>
      </PageSection>
      <PageSection>
        <CounselNotice>
          This notice describes the architecture and the code as they stand. It is not a
          counsel-reviewed privacy policy, and it names no legal entity, because there is not yet a
          generally available control plane for one to contract about.
        </CounselNotice>
        <Prose className="mt-10">
          <p>
            Sign-in today is for the waitlist. The three documents that a security review asks for
            by name are now drafted rather than promised: the{" "}
            <Link href="/dpa">Data Processing Agreement</Link>, the{" "}
            <Link href="/subprocessors">subprocessor list</Link>, and the{" "}
            <Link href="/data-retention">retention and deletion commitments</Link>. Read them as the
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
          title="<strong>Evidence under stated fidelity.</strong> You remain responsible for the permissions you grant."
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
                "Sign-in is for the waitlist. There is no public production control plane yet, and these terms are not a paid-service agreement.",
              ],
              [
                "Availability",
                "Nothing here is a service level commitment. There is none, and the reasons are set out on the service levels page rather than left for a customer to discover.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="sage">
        <PageHeading title="<strong>What these terms do not say.</strong>" />
        <Ledger items={NOT_CLAIMED} />
      </PageSection>
      <PageSection>
        <CounselNotice>
          This page states product limits so that waitlist visitors are not sold a zero-failure
          guarantee. It is not a substitute for a counsel-reviewed agreement, and it is not an order
          form.
        </CounselNotice>
        <Prose className="mt-10">
          <p>
            When a hosted control plane is generally available, these pages will be replaced with
            dated legal documents that name a contracting entity, governing law, and acceptable use.
            Until then, treat every safety report as evidence under the fidelity the run disclosed,
            whether that is pass, warning, or block, and not as insurance.
          </p>
        </Prose>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/privacy", title: "Privacy Notice", description: "What we collect and never take." },
          {
            href: "/dpa",
            title: "Data Processing Agreement",
            description: "The terms for data we process on your behalf.",
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
      <PageSection tone="white">
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
                "Security reports go to security@antifailure.dev today. A separate privacy address will be published with the signed copy.",
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
                "The waitlist address a visitor gives this site, and our own operational records about running the service.",
              ],
              [
                "Purpose limitation",
                "The data is used to run the service, to keep it secure, and to bill for it if there is ever billing. It is not used to train any model, and no path in this product sends it anywhere that would.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="sage">
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
                "Fourteen days of point-in-time recovery on the hosted database. A restore is verified against a manifest taken at backup time and then asked, through the unprivileged role, to refuse a cross-tenant read.",
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
            "No continuous monitoring. Alert rules and runbooks are written and version controlled, but nothing loads them, so a failure today reaches a person when a person happens to look.",
            "No self-service account or organization deletion, and no self-service export. Both are carried out by hand against the database by somebody who can reach it. Deleting a stored model provider key is the one exception, and it is an endpoint you can call.",
            "No production deployment. What exists is a staging control plane behind a sign-in allowlist.",
          ]}
        />
        <Prose className="mt-10">
          <p>
            The point of listing these is that a security review will find every one of them. Better
            it finds them here, next to the measures that are real, than in a questionnaire answer
            that has to be walked back. The <Link href="/sla">service levels page</Link> sets out what
            would have to change first.
          </p>
        </Prose>
      </PageSection>
      <PageSection tone="white">
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
      <PageSection tone="sage">
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
      <PageSection tone="white">
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
      <PageSection tone="white">
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
        lead="There is no generally available control plane to make one about. Rather than leave a security review to discover that, this page says what is not committed, what holds anyway, and what would have to be true before a number here meant anything."
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
      <PageSection tone="sage">
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
      <PageSection tone="white">
        <PageHeading
          kicker="What is deployed"
          title="<strong>One staging environment</strong>, and it is configured like one."
        />
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              [
                "Environment",
                "A single staging control plane, behind a sign-in allowlist. The production deploy path is wired end to end and its final job deliberately fails, because there is no production environment to deploy into.",
              ],
              [
                "Redundancy",
                "One replica of the application and no database high availability. A restart is a visible interruption, which is the correct trade for staging and the wrong one for a paid service.",
              ],
              [
                "Backups",
                "Fourteen days of point-in-time recovery, in one region. Geo-redundant backup is off.",
              ],
              [
                "Monitoring",
                "Alert rules with burn-rate windows and a runbook each are written and version controlled. Nothing loads them. There is no metric alert and no action group in the infrastructure, so no alert reaches anybody.",
              ],
              [
                "Recovery time",
                "A restore drill exists and has been run against a real Postgres, reporting under two seconds on a continuous integration runner and up to 160 seconds on a loaded laptop. It is not scheduled, so there is no evidence that today's backup restores, and those numbers are not a recovery time objective.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection>
        <PageHeading title="<strong>What has to be true before there is an SLA.</strong>" />
        <Ledger
          items={[
            "A production environment, separate from staging, with its own credentials and its own sign-in application.",
            "High availability on the database and more than one application replica.",
            "Geo-redundant backup, and a restore drill that runs on a schedule and fails loudly.",
            "Alerting that reaches a person, and a runbook per alert that the alert actually points at.",
            "On-call, even if it is one person with a phone.",
            "A status page, and enough measured history behind it for a number to mean something.",
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
                "Until you delete them. Deletion is an endpoint you can call, not a request you have to send us.",
              ],
              [
                "Waitlist addresses",
                "Until the waitlist is closed or you ask for removal. Signing up twice updates one row rather than adding a second.",
              ],
              [
                "Database backups",
                "Fourteen days of point-in-time recovery. A deletion is reflected in every backup only after that window has passed.",
              ],
              [
                "Masked dumps",
                "No deployment stores any today. The storage account for them is not created unless it is explicitly enabled. If it is enabled, a deleted blob is recoverable for thirty days.",
              ],
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="sage">
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
      <PageSection tone="white">
        <PageHeading kicker="Deletion" title="<strong>How to have data deleted, and what happens then.</strong>" />
        <div className="mt-14 max-md:mt-10">
          <SpecTable
            rows={[
              [
                "How to ask",
                "Write to the privacy contact once it is published. Until then, security@antifailure.dev reaches a person who can act on it.",
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
                "A deletion applies to the live database immediately and to backups only as they age out, over the fourteen day recovery window. Restoring a backup within that window restores the deleted rows, and any deletion request is applied again afterwards.",
              ],
              [
                "The audit log",
                "Entries about an organization are removed with the organization. They are not removed individually, for the chaining reason above.",
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
