import Link from "next/link";
import type { MarketingContent } from "@/lib/marketing-content";
import { PageCallout, PageTable } from "@/components/layout/MarketingPage";

export const SECURITY_PAGE: MarketingContent = {
  eyebrow: "Security",
  title: "Fail closed. Data stays in your boundary.",
  lead: "The twin runs where production data already lives. Unknown egress is blocked. Cleanup is proven. There is no claim that open source bypasses compliance.",
  description: "Fail closed. Production data stays in the customer boundary.",
  features: [
    { title: "Customer-hosted data plane", body: "Raw snapshots, secrets, and captured request bodies do not enter the hosted control plane." },
    { title: "Fail closed", body: "Unknown destinations, unresolved secrets, incomplete cleanup, or missing isolation block the run." },
    { title: "Cleanup as safety", body: "TTL, cost ceiling, independent cleanup path, verifiable destruction record." },
    { title: "Open and still safe", body: "Do not put the only trustworthy security controls behind a paid gate." },
  ],
  related: [
    { href: "/product/architecture", title: "Architecture", description: "Control plane vs data plane." },
    { href: "/product/firewall", title: "Firewall", description: "How side effects are contained." },
    { href: "/privacy", title: "Privacy", description: "What we collect and what we never take." },
  ],
  body: (
    <>
      <h2>Trust boundary</h2>
      <p>
        Production data does not leave the customer cloud for sanitization theater. Masking,
        subsetting, and attestation happen inside that boundary. The control plane receives
        evidence, not records. Communication is outbound-only from the customer agent where
        possible, authenticated with short-lived mTLS credentials.
      </p>
      <h2>Fail closed</h2>
      <ul>
        <li>No default public egress from the twin</li>
        <li>Unknown destinations blocked and ledgered</li>
        <li>Unresolved secrets block the run</li>
        <li>Unverified goldens cannot be branched</li>
        <li>Failed cleanup fails the run</li>
      </ul>
      <h2>Sanitization risk</h2>
      <p>
        If sanitization misses sensitive data, the mitigation is: process inside the customer
        boundary, delete credentials rather than mask them, combine schema rules with scanning,
        validate outputs, and support external enterprise data providers.
      </p>
      <h2>Enterprise path</h2>
      <p>
        Architected for enterprise from the beginning — customer-managed keys, private
        connectivity, data-residency controls, SIEM, evidence exports — but sold first to
        mid-market teams. Enterprise distrust of a young team is mitigated by a customer-hosted
        architecture, an open-source data plane, a published threat model, signed releases, and
        narrow claims backed by demonstrations. We will not claim open source bypasses compliance.
      </p>
      <PageCallout label="Data boundary">
        Default: production-derived state stays in the customer cloud. The hosted control plane
        holds organizations, policy, aggregated reports, and billing — not snapshots.
      </PageCallout>
    </>
  ),
};

export const OPEN_SOURCE_PAGE: MarketingContent = {
  eyebrow: "Open source",
  title: "The pieces that sit in your boundary should be inspectable.",
  lead: "The software handles cloud permissions, production-derived state, secrets, networking, and expensive infrastructure. Open source materially improves inspectability for components inside the customer trust boundary.",
  description: "Customer agent, adapters, sanitization, egress, and cleanup — the planned open-source surface.",
  features: [
    { title: "Agent and CLI", body: "Customer agent, local CLI, and Docker Compose development mode." },
    { title: "Adapters", body: "Provisioning contracts, Postgres adapter, sanitization engine, policy format." },
    { title: "Containment", body: "Egress gateway, core provider simulators, deterministic runner, cleanup controller." },
    { title: "Reports", body: "Local report format. Safety is not an enterprise add-on." },
  ],
  related: [
    { href: "/product/architecture", title: "Architecture", description: "What stays in the customer cloud." },
    { href: "/pricing", title: "Pricing", description: "Community, team cloud, and enterprise." },
    { href: "/docs/open-source", title: "Open-source docs", description: "Planned surface in full." },
  ],
  body: (
    <>
      <h2>Recommended open-source surface</h2>
      <ul>
        <li>Customer agent and local CLI</li>
        <li>Provisioning contracts and Postgres adapter</li>
        <li>Sanitization engine and policy format</li>
        <li>Egress gateway and core provider simulators</li>
        <li>Deterministic runner and cleanup controller</li>
        <li>Local report format and Docker Compose development mode</li>
      </ul>
      <h2>Recommended commercial surface</h2>
      <p>Standard enterprise functionality can live in a separate commercial layer, including:</p>
      <ul>
        <li>SAML/OIDC SSO and SCIM</li>
        <li>Advanced RBAC and delegated administration</li>
        <li>Approval workflows and immutable audit retention</li>
        <li>Policy packs and organization-wide enforcement</li>
        <li>Customer-managed keys and private connectivity</li>
        <li>Data-residency controls, SIEM, evidence exports</li>
        <li>Fleet management, multi-account orchestration, advanced cost allocation</li>
        <li>Premium connectors, compatibility certification, support and SLAs</li>
      </ul>
      <p>
        The hosted control plane, historical analytics, managed updates, and large-scale
        orchestration can also be commercial even when the underlying execution engine is open.
      </p>
      <h2>Licensing</h2>
      <p>
        MIT maximizes adoption but permits unrestricted commercial hosting. Apache 2.0 adds an
        explicit patent grant. The team should choose a standard license with counsel. There is no
        hosted control plane yet. The engine is open source and runs on your own machine today.
      </p>
      <PageCallout label="Safety is not an enterprise add-on">
        Do not put the only trustworthy security controls behind a paid gate. The open product must
        still be safe. Enterprise code adds governance, organizational control, evidence, scale, and
        managed operations.
      </PageCallout>
    </>
  ),
};

export const PRICING_PAGE: MarketingContent = {
  eyebrow: "Pricing",
  title: "Charge for the operational value controlled, not for AI personalities.",
  lead: "Community is the local engine. Team cloud is a platform fee plus run usage. Enterprise is governance, scale, and support. Unlimited free hosted compute is not the model.",
  description: "Community, team, and enterprise pricing for pre-production deployment safety.",
  features: [
    { title: "Community", body: "Free open-source local engine, Docker Compose, basic Postgres, BYO infrastructure and model keys." },
    { title: "Team cloud", body: "Base platform fee, included run credits, usage for environment minutes, data volume, and workload execution." },
    { title: "Enterprise", body: "Governance, residency, fleet, evidence, and support. Architected from day one, sold when references exist." },
  ],
  related: [
    { href: "/open-source", title: "Open source", description: "What stays inspectable." },
    { href: "/design-partners", title: "Design partners", description: "Start with one nervous deploy." },
    { href: "/company", title: "About", description: "Why the company exists." },
  ],
  body: (
    <>
      <h2>Illustrative early pricing</h2>
      <PageTable
        headers={["Plan", "Range", "What it is for"]}
        rows={[
          ["Team", "$500 to $2,000 per month", "One application, included run credits, hosted control plane"],
          ["Growth", "$2,000 to $8,000 per month", "More repositories, more run volume, organization policy"],
          [
            "Enterprise",
            "$30,000 to $250,000+ annually",
            "Scale, governance, residency, fleet, support — depending on scope",
          ],
        ]}
      />
      <p>
        These figures are illustrative, not a quote. Customer-cloud execution exists to control
        margin and data exposure.
      </p>
      <h2>Value metrics</h2>
      <p>Useful value metrics include:</p>
      <ul>
        <li>Applications protected</li>
        <li>Deployment runs</li>
        <li>Environment execution</li>
        <li>Data volume</li>
        <li>Peak workload scale</li>
        <li>Governance and compliance scope</li>
        <li>Support level</li>
      </ul>
      <p>
        Do not charge primarily for the number of AI personalities. Exploratory users are a
        Workload Studio feature. The product is the deployment decision.
      </p>
      <h2>Free usage</h2>
      <p>
        Unlimited free hosted compute is not viable. Free usage should have strict credits or
        require the user’s own cloud and model credentials.
      </p>
    </>
  ),
};

export const COMPANY_PAGE: MarketingContent = {
  eyebrow: "Company",
  title: "Answer one question better than any individual tool.",
  lead: "Is this deployment safe to ship under the conditions that actually matter? That is why the company exists. Exploratory users, sanitization, E2E execution, and preview deploy are components. None of them alone defines the company.",
  description: "Antifailure is an open-core pre-production deployment safety platform.",
  features: [
    { title: "Category", body: "Pre-production deployment safety. Not AI QA, not staging, not load testing." },
    { title: "Wedge", body: "Postgres migration and deployment safety for modern SaaS teams." },
    { title: "Architecture", body: "Open-core. Customer-hosted data plane. Fail closed." },
  ],
  related: [
    { href: "/product", title: "Product", description: "The modules that make a decision." },
    { href: "/design-partners", title: "Design partners", description: "The recommended next action." },
    { href: "/security", title: "Security", description: "Trust boundary and fail closed." },
  ],
  body: (
    <>
      <h2>Final product definition</h2>
      <p>
        Production Wind Tunnel is an open-core pre-production deployment safety platform. It
        connects to a repository and customer cloud, creates a temporary isolated twin of the
        application, restores a sanitized production-shaped database, replaces dangerous
        integrations with stateful simulators, and exercises the baseline and candidate releases
        with observed workloads, deterministic tests, and exploratory AI users. It analyzes
        application behavior, database migrations, queues, workers, performance, external effects,
        and user outcomes, then issues an evidence-backed pass, warning, or block result before
        automatically destroying the environment.
      </p>
      <p>
        Its initial wedge is Postgres migration and deployment safety for modern SaaS teams. Its
        long-term platform expands into a universal proving ground for application, infrastructure,
        database, and cloud migrations.
      </p>
      <h2>Messaging</h2>
      <ul>
        <li>
          Homepage: Know what happens before you deploy.
        </li>
        <li>
          Developer: One click turns your pull request into a private, production-shaped
          environment with its own safe database and integrations.
        </li>
        <li>
          Platform: Replace fragile shared staging with policy-controlled, ephemeral deployment
          validation inside your cloud.
        </li>
        <li>
          Executive: Reduce the probability and blast radius of high-risk releases by validating
          them under production-shaped conditions before rollout.
        </li>
      </ul>
      <h2>Team</h2>
      <p>
        An initial two-founder division can split infrastructure and safety from behavior and
        verification. Both founders should participate in design-partner deployments. Repeated
        manual work must become reusable adapters, not permanent consulting. Before combining
        pre-existing code, ownership, licenses, equity, and decision rights must be documented.
      </p>
      <h2>What we will not claim</h2>
      <p>
        Zero rollback guarantee. No deployment can ever fail. Thousands of AI agents behave exactly
        like humans. One click perfectly clones every cloud. Open source bypasses compliance. Use
        measurable, verifiable language instead.
      </p>
      <PageCallout label="Recommended next action">
        Not building the universal platform. Securing one real risky migration and building the
        smallest complete wind tunnel that can make a correct, useful decision about it.
      </PageCallout>
    </>
  ),
};

export const DESIGN_PARTNERS_PAGE: MarketingContent = {
  eyebrow: "Design partners",
  title: "Give us one deployment your team is nervous about.",
  lead: "We will create an isolated production-shaped test, run the change, and show you what your existing staging process misses. The pilot centers on an actual upcoming migration, not a generic demo.",
  description: "Design-partner offer: one real risky migration, a complete wind tunnel, a useful decision.",
  features: [
    { title: "One session to connect", body: "Success means three partners can connect in one working session." },
    { title: "One finding staging missed", body: "At least one finding their normal process did not surface." },
    { title: "Deterministic reproduce", body: "The finding can be replayed. Containment holds. Cleanup completes." },
  ],
  related: [
    { href: "/solutions/migrations", title: "Schema migrations", description: "The intended first pilot." },
    { href: "/pricing", title: "Pricing", description: "What paid use looks like after the pilot." },
    { href: "/signup", title: "Sign up", description: "Join the waitlist." },
  ],
  body: (
    <>
      <h2>The offer</h2>
      <p>
        Give us one deployment your team is nervous about. We will create an isolated
        production-shaped test, run the change, and show you what your existing staging process
        misses.
      </p>
      <h2>MVP success criteria</h2>
      <p>The MVP is successful if three design partners can:</p>
      <ul>
        <li>Connect within one working session</li>
        <li>Run the product against a real upcoming deployment</li>
        <li>Receive at least one finding their normal process did not surface</li>
        <li>Reproduce the finding deterministically</li>
        <li>Confirm that no production data or real-world action escaped containment</li>
        <li>Repeat the process on a later pull request with less founder assistance</li>
        <li>Express willingness to pay for continued use</li>
      </ul>
      <h2>Proof points we will measure</h2>
      <ul>
        <li>Incidents prevented and unsafe migrations detected</li>
        <li>False-positive rate</li>
        <li>Time from installation to first useful run</li>
        <li>Environment cleanup reliability</li>
        <li>Reduction in staging maintenance</li>
        <li>Percentage of runs repeated without founder support</li>
        <li>Dollar value or engineering time associated with caught failures</li>
      </ul>
      <p>
        Initial acquisition is open-source Postgres migration safety, technical incident write-ups,
        GitHub application, founder outreach to platform engineers, and partnerships with Supabase
        and Postgres communities — not a broad AI QA campaign.
      </p>
      <p>
        <Link href="/signup">Join the waitlist</Link> if you have a migration you do not want to
        learn about in production.
      </p>
    </>
  ),
};

export const PRIVACY_PAGE: MarketingContent = {
  eyebrow: "Privacy Notice",
  title: "Production data stays in the customer boundary.",
  lead: "The hosted control plane holds organizations, policy, aggregated reports, and billing. Raw snapshots, secrets, and captured request bodies stay in your cloud by default.",
  description: "Privacy notice for Antifailure: what the control plane holds and what it never takes.",
  related: [
    { href: "/security", title: "Security", description: "Trust boundary and fail closed." },
    { href: "/terms", title: "Terms of Use", description: "How the product may be used." },
    { href: "/open-source", title: "Open source", description: "Inspect the data-plane components." },
  ],
  body: (
    <>
      <h2>What the control plane stores</h2>
      <ul>
        <li>Organization and project metadata</li>
        <li>Account emails used to sign in</li>
        <li>GitHub installation and repository identifiers</li>
        <li>Run planning, policy configuration, and aggregated reports</li>
        <li>Billing and entitlements</li>
      </ul>
      <h2>What stays in your boundary</h2>
      <ul>
        <li>Raw database snapshots</li>
        <li>Secrets and production credentials</li>
        <li>Captured request bodies, until redacted inside the data plane</li>
        <li>Raw logs and traces from the twin</li>
      </ul>
      <h2>Sanitization</h2>
      <p>
        Masking, subsetting, and credential deletion execute inside the customer-hosted data plane.
        Tokens, sessions, secrets, and credentials are deleted rather than disguised. Free-text PII
        detection is part of the evidence report.
      </p>
      <p>
        This notice describes product intent from the August 2026 brief. It is not a substitute for
        a counsel-reviewed privacy policy once the hosted control plane is generally available.
      </p>
    </>
  ),
};

export const TERMS_PAGE: MarketingContent = {
  eyebrow: "Terms of Use",
  title: "A proving ground, not a guarantee.",
  lead: "The product reports whether a deployment is safe to ship under the conditions it could observe and reproduce. It does not mathematically guarantee that a deployment cannot fail.",
  description: "Terms of use for Antifailure. The promise is evidence, not zero-failure.",
  related: [
    { href: "/privacy", title: "Privacy Notice", description: "What we collect and never take." },
    { href: "/security", title: "Security", description: "Fail closed and the data boundary." },
    { href: "/company", title: "About", description: "What the company will not claim." },
  ],
  body: (
    <>
      <h2>The promise, stated as a limit</h2>
      <p>
        Before a deployment reaches users, reproduce the highest-risk production conditions we can
        observe, measure how the proposed system behaves, and expose dangerous differences with
        concrete evidence. That is the promise. These are not the promise:
      </p>
      <ul>
        <li>Zero rollback guarantee</li>
        <li>No deployment can ever fail</li>
        <li>Thousands of AI agents behave exactly like humans</li>
        <li>One click perfectly clones every cloud</li>
        <li>Open source bypasses compliance</li>
      </ul>
      <h2>Your cloud, your data</h2>
      <p>
        Default architecture is a customer-hosted data plane. You remain responsible for cloud
        permissions, production access you grant the agent, and the isolation policies you approve.
        Fail-closed defaults exist so convenience cannot silently override containment.
      </p>
      <h2>Accounts</h2>
      <p>
        Sign-in exists so design partners and waitlisted teams can join. There is no public
        production control plane yet. These terms will be replaced by counsel-reviewed terms before
        paid general availability.
      </p>
    </>
  ),
};
