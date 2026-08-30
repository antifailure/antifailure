import Link from "next/link";
import type { ReactNode } from "react";
import { PageCallout, PagePre, PageTable, type PageFeature, type PageRelated } from "@/components/layout/MarketingPage";

export type MarketingContent = {
  eyebrow: string;
  title: string;
  lead: string;
  description: string;
  features?: PageFeature[];
  related?: PageRelated[];
  body: ReactNode;
};

const PRODUCT_RELATED: PageRelated[] = [
  { href: "/product/migrations", title: "Migration Safety", description: "The flagship wedge: locks, plans, rollback." },
  { href: "/product/report", title: "Safety Report", description: "Pass, warning, or block with evidence." },
  { href: "/product/architecture", title: "Architecture", description: "Customer-hosted data plane, fail closed." },
];

export const PRODUCT_OVERVIEW: MarketingContent = {
  eyebrow: "Product",
  title: "A disposable production twin that proves whether a deployment is safe.",
  lead: "Connect a repository and cloud environment. For every risky change, the platform creates an isolated production twin, fills it with safe production-shaped state, exercises it, and reports whether the deployment is safe to ship.",
  description: "Pre-production deployment safety: twin, state, containment, behavior, comparison, judgment, evidence, and cleanup.",
  features: [
    { title: "Twin", body: "An isolated, temporary copy of the relevant application stack." },
    { title: "State", body: "A safe, referentially consistent, production-shaped dataset." },
    { title: "Containment", body: "No charging cards, emailing users, or invoking production webhooks." },
    { title: "Behavior", body: "Captured workload patterns, deterministic scenarios, and exploratory AI users." },
    { title: "Comparison", body: "Current and proposed versions against equivalent state and behavior." },
    { title: "Judgment", body: "Functional, database, performance, integration, and behavioral regressions." },
    { title: "Evidence", body: "An auditable ship, warn, or block report on the pull request." },
    { title: "Cleanup", body: "Destroy temporary resources and prove that cleanup completed." },
  ],
  related: [
    { href: "/product/twins", title: "Isolated Twin", description: "How the orchestrator provisions and tears down." },
    { href: "/product/migrations", title: "Migration Safety", description: "The first complete wedge." },
    { href: "/docs", title: "Docs", description: "How a twin run works, end to end." },
  ],
  body: (
    <>
      <h2>The question staging cannot answer</h2>
      <p>
        Modern engineering teams can create preview frontends easily. They cannot easily answer the
        question that actually matters before a deployment: what will happen when this exact change
        meets the real system, data shape, concurrency, user behavior, queues, workers, integrations,
        and deployment process.
      </p>
      <p>Existing testing is fragmented:</p>
      <ul>
        <li>Preview-environment tools deploy code but usually do not reproduce complete production state or behavior.</li>
        <li>Test-data platforms create safe, realistic datasets but do not decide whether an application deployment is safe.</li>
        <li>End-to-end testing tools validate user flows but usually assume a usable environment already exists.</li>
        <li>Load-testing tools generate throughput but do not understand business workflows, migrations, or external side effects.</li>
        <li>Traffic-mirroring tools copy packets but do not reconstruct application state or safely replay complete user journeys.</li>
        <li>Observability tools explain failures after code is running but do not create an isolated proving ground before release.</li>
      </ul>
      <h2>The category</h2>
      <p>
        The category is pre-production deployment safety. Alternative phrases: production wind tunnel,
        deployment proving ground, release safety platform, production-twin testing. We do not lead with
        AI QA, synthetic users, staging, database replication, or load testing. Those descriptions expose
        only one component and invite comparison on the wrong axis.
      </p>
      <h2>The promise</h2>
      <p>
        Every meaningful change gets a realistic, disposable proving ground. The promise is not that the
        platform mathematically guarantees a deployment cannot fail. That claim would be indefensible.
      </p>
      <PageCallout label="What we will not claim">
        Zero rollback. No deployment can ever fail. Thousands of AI agents behave exactly like humans. One
        click perfectly clones every cloud. Open source bypasses compliance. Use measurable evidence instead.
      </PageCallout>
      <h2>The wedge</h2>
      <p>
        The initial wedge is not universal multicloud cloning. It is automated safety validation for risky
        Postgres-backed web deployments, especially schema migrations. That wedge has a clear failure mode,
        a technical buyer, measurable value, and a natural expansion path.
      </p>
      <p>
        Exploratory AI users live in{" "}
        <Link href="/product/workload">Workload Studio</Link>, beside observed and deterministic traffic.
      </p>
    </>
  ),
};

export const PRODUCT_PAGES: Record<string, MarketingContent> = {
  twins: {
    eyebrow: "Twin Orchestrator",
    title: "A disposable production twin for every risky change.",
    lead: "Build the candidate, deploy a baseline for comparison, isolate the network, inject clone-specific config, and tear everything down when the report is done.",
    description: "A temporary copy of the application stack for every risky change.",
    features: [
      { title: "Candidate and baseline", body: "Deploy both versions so the oracle can compare equivalent state and behavior." },
      { title: "Isolated networking", body: "No production write credentials, no production database route, no default internet route." },
      { title: "TTL and budget", body: "Every environment has a cost ceiling, expiration, and independent cleanup path." },
      { title: "Ownership", body: "Resource tags contain the run ID and expiration. Unowned resources are rejected." },
      { title: "Preview endpoint", body: "Private by default. A URL is not the product — the report is." },
      { title: "One button", body: "Create Wind Tunnel. Behind it, the platform chooses the adapter and fidelity level." },
    ],
    related: PRODUCT_RELATED,
    body: (
      <>
        <h2>What it creates</h2>
        <p>
          The orchestrator uses detected deployment metadata, containers, platform integrations, or IaC.
          It provisions temporary domains and certificates, replaces production credentials, tracks
          resource ownership, and enforces TTL and budget.
        </p>
        <ul>
          <li>Build and deploy candidate code</li>
          <li>Deploy a baseline version for differential comparison</li>
          <li>Create isolated networking and clone-specific configuration</li>
          <li>Replace production credentials with twin identities</li>
          <li>Tear down all resources and prove cleanup completed</li>
        </ul>
        <h2>Environment lifecycle</h2>
        <PagePre>{`REQUESTED
  -> PLANNED
  -> PROVISIONING
  -> SANITIZING
  -> DEPLOYING
  -> VERIFYING_CONTAINMENT
  -> READY
  -> BASELINE_RUNNING
  -> CANDIDATE_RUNNING
  -> ANALYZING
  -> REPORTING
  -> DESTROYING
  -> DESTROYED`}</PagePre>
        <p>
          Every state transition is idempotent and recoverable. A separate TTL reaper operates
          independently from the main orchestrator. Resource deletion is not a background convenience.
        </p>
        <h2>Isolation model</h2>
        <p>
          For serious customers, the clone should use a dedicated account, subscription, project, or
          strongly isolated network boundary when practical.
        </p>
        <ul>
          <li>No production write credentials or production database route</li>
          <li>No default internet route; separate DNS policy and secrets namespace</li>
          <li>Temporary workload identity; resource tags with run ID and expiration</li>
          <li>Hard cost ceiling; admission policy that rejects unowned resources</li>
          <li>Independent cleanup controller</li>
        </ul>
        <h2>What it is not</h2>
        <p>
          A preview URL is not the product. Each twin may receive an authenticated endpoint such as{" "}
          <span className="font-mono text-[14px] text-black">fix-billing-184.preview.company.com</span>.
          It is private by default and can be protected by short-lived tokens, an identity-aware proxy,
          or the customer’s existing access system. Credentials must never be shared across agent
          sessions unless they represent an intentionally reusable synthetic identity.
        </p>
        <PageCallout label="Fidelity, not theater">
          One button is an experience, not a claim of identical internals. The system discloses what it
          reproduced and what it could not. A clear fidelity score is more trustworthy than pretending
          every cloud topology can be cloned perfectly.
        </PageCallout>
      </>
    ),
  },
  "safe-state": {
    eyebrow: "Safe State Engine",
    title: "Production-shaped Postgres without production identities.",
    lead: "Snapshot restore, referentially consistent subsetting, deterministic masking, and a sanitization evidence report. An unverified golden cannot be branched.",
    description: "Sanitized, referentially consistent, production-shaped Postgres.",
    features: [
      { title: "Snapshot restore", body: "Logical restore for portability, or provider-native copy-on-write branches when supported." },
      { title: "Referential subsets", body: "Keep joins valid. Toy fixtures are misleading when production has long-tail records." },
      { title: "Deterministic masking", body: "Format-preserving replacement with uniqueness preserved, executed inside the customer boundary." },
      { title: "Delete, don’t mask", body: "Tokens, sessions, secrets, and credentials are deleted rather than disguised." },
      { title: "Free-text PII", body: "Scan for emails, cards, phones, and keys that schema rules miss." },
      { title: "Evidence report", body: "Distribution validation, schema-drift handling, and a signed sanitization attestation." },
    ],
    related: [
      { href: "/product/firewall", title: "Side-Effect Firewall", description: "The twin cannot act on the real world." },
      { href: "/product/architecture", title: "Architecture", description: "Control plane and customer data plane." },
      { href: "/product/fidelity", title: "Fidelity Graph", description: "What volume and distribution were actually restored." },
    ],
    body: (
      <>
        <h2>Why staging data fails</h2>
        <p>
          Staging environments systematically fail to represent production because they differ in data
          volume, distribution, long-tail records, and malformed historical state. A change can pass
          every fixture-based test and still fail on a rare production-shaped row.
        </p>
        <h2>Capabilities</h2>
        <ul>
          <li>Snapshot restore and schema-drift handling</li>
          <li>Referentially consistent subsetting</li>
          <li>Deterministic masking and format-preserving replacement</li>
          <li>Uniqueness preservation</li>
          <li>Deletion of tokens, sessions, secrets, and credentials</li>
          <li>Free-text PII detection and data-distribution validation</li>
          <li>Volume scaling through synthetic augmentation when a subset is too small</li>
          <li>Sanitization evidence report</li>
        </ul>
        <h2>Postgres strategy</h2>
        <p>
          Provider adapters expose a common lifecycle: discover, snapshot, restore, sanitize, augment,
          apply_migrations, observe, destroy. Masking runs inside the customer boundary. Postgres
          instrumentation covers locks, statements, plans, and resource usage.
        </p>
        <p>
          Supabase support must account for more than the public schema. A database clone can include
          Auth records, roles, and permissions while excluding Storage objects, Edge Functions, and
          other project configuration. The adapter needs explicit handling for sessions, provider
          tokens, Storage metadata, RLS, hooks, secrets, and service-role access.
        </p>
        <h2>What we will not rebuild in version one</h2>
        <p>
          Deep enterprise data-management platforms such as Tonic can be supported as an external
          provider rather than replicated completely. Trying to immediately match connector and
          compliance depth would distract from the migration-safety wedge. The output of this layer is
          not a dataset. The output of the product is a deployment-safety decision.
        </p>
        <PageCallout label="Customer boundary">
          Raw snapshots, secrets, and captured request bodies do not enter the hosted control plane.
          The default enterprise architecture uses a customer-hosted data plane.
        </PageCallout>
      </>
    ),
  },
  firewall: {
    eyebrow: "Side-Effect Firewall",
    title: "The twin cannot act on the real world.",
    lead: "No default public egress. Clone-local DNS. Stateful provider simulators. Unknown destinations are blocked and written to the attempted-effect ledger.",
    description: "Fail-closed egress. Simulators instead of real-world side effects.",
    features: [
      { title: "No default egress", body: "There is no default public internet route from the twin." },
      { title: "Clone-local DNS", body: "Production hostnames do not resolve to production." },
      { title: "Mandatory gateway", body: "Domain, IP, protocol, method, and operation policies are enforced at the edge." },
      { title: "Bypass detection", body: "Direct-IP attempts are detected and blocked." },
      { title: "Stateful simulators", body: "Stripe, email, and webhooks get clone-local ledgers instead of the real world." },
      { title: "Attempted-effect ledger", body: "Every outbound attempt is recorded, including denies." },
    ],
    related: [
      { href: "/product/architecture", title: "Architecture", description: "Fail closed is a product principle, not a slogan." },
      { href: "/product/oracle", title: "Differential Oracle", description: "Third-party effects are compared, not ignored." },
      { href: "/docs/concepts/egress", title: "Egress docs", description: "Controls and example behavior." },
    ],
    body: (
      <>
        <h2>Default behavior</h2>
        <PageTable
          headers={["Outbound action", "Wind Tunnel behavior"]}
          rows={[
            ["Stripe payment creation", "Simulate and store in a clone-local Stripe ledger"],
            ["SendGrid email send", "Render and capture, never deliver"],
            ["Slack webhook", "Store a message preview"],
            ["S3 read", "Read from a clone bucket or approved read-only fixture"],
            ["S3 write", "Write only to the clone bucket"],
            ["Production API hostname", "Block and flag as critical"],
            ["Unknown TCP destination", "Deny by default"],
          ]}
        />
        <h2>Controls</h2>
        <ul>
          <li>No default public egress</li>
          <li>Clone-local DNS and a mandatory egress gateway</li>
          <li>Domain, IP, protocol, method, and operation policies</li>
          <li>Detection of direct-IP bypass attempts</li>
          <li>Stateful provider simulators</li>
          <li>Read-only forwarding only for explicitly approved endpoints</li>
          <li>Request and response redaction</li>
          <li>Unknown-destination blocking</li>
        </ul>
        <h2>Fail closed</h2>
        <p>
          Unknown outbound destinations, unresolved secrets, incomplete cleanup policies, or missing
          isolation should block a run by default. Convenience must not silently override containment.
          The ledger records attempted effects, including denies.
        </p>
        <PageCallout label="Risk this exists to prevent">
          The twin causing real-world effects — charging cards, emailing users, invoking production
          webhooks — is an existential failure. No default internet route, mandatory gateway,
          clone-local DNS, and fail-closed policies are the mitigation, not a best-effort proxy.
        </PageCallout>
      </>
    ),
  },
  workload: {
    eyebrow: "Workload Studio",
    title: "Exercise the twin the way production actually behaves.",
    lead: "Three traffic sources: observed production patterns, deterministic journeys, and exploratory users. Production requests are never synchronously diverted.",
    description: "Observed patterns, deterministic scenarios, and exploratory users.",
    features: [
      { title: "Observed patterns", body: "Ingress traces, API telemetry, OpenTelemetry, or customer-provided samples, redacted first." },
      { title: "Deterministic scenarios", body: "Versioned journeys at controlled concurrency for repeatability and CI." },
      { title: "Exploratory users", body: "Exploratory agents that pursue goals, discover paths, and explain friction." },
      { title: "AI discovers", body: "Agents are excellent at exploration. They are not economical load generators." },
      { title: "Systems prove", body: "Successful journeys compile into deterministic scenarios for scale." },
      { title: "Never in the hot path", body: "Shadowing is asynchronous and must never delay or alter production responses." },
    ],
    related: [
      { href: "/product/exploratory-users", title: "Exploratory users", description: "Exploratory users inside Workload Studio, beside observed and deterministic traffic." },
      { href: "/product/oracle", title: "Differential Oracle", description: "Same workload against baseline and candidate." },
      { href: "/docs/concepts/load", title: "Load docs", description: "Scenario IR and traffic controls." },
    ],
    body: (
      <>
        <h2>Three sources</h2>
        <h3>Observed production patterns</h3>
        <p>
          Derived from ingress traces, API telemetry, OpenTelemetry, session recordings, analytics, or
          customer-provided samples. Requests are redacted and normalized before storage or replay.
        </p>
        <h3>Deterministic scenarios</h3>
        <p>
          Versioned journeys that reproduce known critical workflows at controlled concurrency. These
          provide repeatability, statistical comparison, and CI enforcement.
        </p>
        <PagePre>{`scenario: impatient_upgrade
identity_fixture: returning_pro_user
steps:
  - open: /settings/billing
  - click: upgrade
  - submit: payment_form
  - parallel:
      - retry_submit_after_ms: 300
      - refresh_after_ms: 450
assertions:
  - one_subscription_created
  - at_most_one_payment_attempt
  - confirmation_visible`}</PagePre>
        <p>
          The deterministic runner executes this representation without an LLM call at each step. AI
          can intervene when the interface changes or an unexplained state is encountered.
        </p>
        <h3>Exploratory AI users</h3>
        <p>
          Exploratory agents receive goals, context, synthetic accounts, and behavioral traits. They
          navigate the application, discover new paths, test ambiguous states, and explain friction.
          Useful discoveries compile into candidate regression scenarios.
        </p>
        <h2>Traffic control</h2>
        <p>The UI may present:</p>
        <ul>
          <li>Session rate and concurrent sessions</li>
          <li>Duration and baseline-to-candidate split</li>
          <li>Observed-versus-synthetic behavior mix</li>
          <li>Persona, device, and geography distribution</li>
          <li>Normal, edge-case, and adversarial intensity</li>
        </ul>
        <PageCallout label="Production traffic">
          The percentage control must not imply that production requests are synchronously diverted.
          Capture is asynchronous, redacted before storage, and rate-limited on replay. The product
          must never sit in the synchronous production response path by default.
        </PageCallout>
      </>
    ),
  },
  "exploratory-users": {
    eyebrow: "Workload Studio",
    title: "Exploratory users that discover paths. Deterministic systems that prove them.",
    lead: "Exploratory users live in Workload Studio, beside observed and deterministic traffic. AI discovers journeys. Conventional execution engines reproduce them at scale.",
    description: "Exploratory AI users inside Workload Studio, not a standalone AI QA product.",
    features: [
      { title: "Goals, not selectors", body: "Agents pursue business-relevant goals rather than fixed CSS paths." },
      { title: "Personas with timing", body: "Personality affects decision-making and timing, not merely prompt wording." },
      { title: "Compile to scenarios", body: "Useful discoveries become versioned deterministic journeys." },
      { title: "Grounded or labeled", body: "Personas come from product analytics or are explicitly synthetic hypotheses." },
    ],
    related: [
      { href: "/product/workload", title: "Workload Studio", description: "Observed, deterministic, and exploratory traffic." },
      { href: "/product/oracle", title: "Differential Oracle", description: "Where discoveries become evidence." },
      { href: "/product", title: "Product", description: "The company is not a synthetic-user company." },
    ],
    body: (
      <>
        <h2>Responsibilities</h2>
        <ul>
          <li>Understand the product and the changed feature</li>
          <li>Generate business-relevant personas</li>
          <li>Explore the user interface and pursue goals rather than fixed selectors</li>
          <li>Discover unanticipated workflows</li>
          <li>Identify functional and UX failures</li>
          <li>Convert useful discoveries into candidate regression scenarios</li>
          <li>Explain user impact in natural language</li>
        </ul>
        <h2>Example personas</h2>
        <ul>
          <li>New customer following the intended happy path</li>
          <li>Returning power user with complex stored state</li>
          <li>Impatient user who retries and double-clicks</li>
          <li>Mobile user on a slow connection</li>
          <li>User with accessibility needs</li>
          <li>User who abandons and resumes later</li>
          <li>User using multiple tabs</li>
          <li>Adversarial input explorer</li>
          <li>API client with retry behavior</li>
        </ul>
        <h2>What we will not claim</h2>
        <p>
          Exploratory users must not be positioned merely as “more personalities than Autosana.” That
          feature can be copied. The defensible system links exploratory behavior to infrastructure and
          database evidence. We will not claim that thousands of AI agents behave exactly like humans.
        </p>
        <PageCallout label="Cost and determinism">
          AI users are expensive and nondeterministic. Use AI for discovery. Compile successful
          journeys into deterministic scenarios for scale. That is a product principle, not an
          implementation detail.
        </PageCallout>
      </>
    ),
  },
  migrations: {
    eyebrow: "Migration Safety Engine",
    title: "The flagship: schema changes on real volume.",
    lead: "Measure exclusive locks, blocked time, table rewrites, query-plan regressions, pool exhaustion, and whether old code can still read candidate writes.",
    description: "Locks, query plans, rollback feasibility on a production-shaped twin.",
    features: [
      { title: "Locks", body: "Acquisition, duration, blocked statements, and cumulative blocked time." },
      { title: "Rewrites", body: "Full table rewrites, index builds, and constraint failures on rare rows." },
      { title: "Plans", body: "Query-plan changes and latency distributions under production-shaped data." },
      { title: "Pools", body: "Connection-pool pressure, CPU, memory, IOPS, disk, and WAL growth." },
      { title: "Coexistence", body: "Old and new application versions during rollout." },
      { title: "Rollback", body: "Whether candidate writes make rolling rollback unsafe." },
    ],
    related: [
      { href: "/solutions/migrations", title: "Schema migrations", description: "Why this is the starting wedge." },
      { href: "/product/report", title: "Safety Report", description: "A 27-second lock is a block." },
      { href: "/docs/guides/invariants", title: "Invariants docs", description: "The subscriptions demo in full." },
    ],
    body: (
      <>
        <h2>Failures conventional tests miss</h2>
        <ul>
          <li>Long exclusive locks</li>
          <li>Full table rewrites</li>
          <li>Connection-pool exhaustion</li>
          <li>Query-plan regressions</li>
          <li>Constraint failures on rare production-shaped records</li>
          <li>Incompatibility between old application instances and the new schema</li>
          <li>Duplicate queue or webhook events</li>
          <li>Irreversible writes that make rollback unsafe</li>
          <li>Replication lag or excessive write-ahead log growth</li>
          <li>Disk, IOPS, or memory spikes</li>
        </ul>
        <h2>What it measures</h2>
        <ul>
          <li>Lock acquisition and duration; blocked statements and cumulative blocked time</li>
          <li>Long-running transactions, table rewrites, index-build behavior, constraint failures</li>
          <li>Query-plan changes and latency distributions; connection-pool pressure</li>
          <li>CPU, memory, IOPS, disk, network; replication lag; write-ahead log growth</li>
          <li>Old/new application compatibility; expand-and-contract safety; rollback feasibility</li>
          <li>Candidate writes that old code cannot read</li>
        </ul>
        <h2>What a useful result looks like</h2>
        <PagePre>{`BLOCKED: unsafe schema migration

Migration 20260824_add_billing_status held an ACCESS EXCLUSIVE
lock on subscriptions for 27.4 seconds. During equivalent traffic,
checkout p99 increased from 820 ms to 6.9 seconds and 11.8% of
upgrade attempts timed out. The previous application version also
failed to deserialize rows written by the candidate version, so
rolling rollback is unsafe.

Suggested action: add the nullable column without a default,
backfill in batches, deploy dual-read compatibility, then enforce
the constraint in a later migration.`}</PagePre>
        <h2>Killer demo</h2>
        <p>
          A checkout flow, a Postgres subscriptions table, a background billing worker, Stripe calls,
          and confirmation emails. The candidate adds a risky defaulted column and changes event
          handling. Exploratory users discover an impatient double-click on Upgrade. Deterministic traffic
          scales that behavior. The migration produces a table lock. Duplicate billing events reach
          the Stripe simulator. Baseline stays healthy. The GitHub check blocks and explains both
          defects. Resources are destroyed automatically.
        </p>
        <PageCallout label="Safer pattern">
          Add the nullable column without a default, backfill in batches, deploy dual-read
          compatibility, then enforce the constraint in a later migration.
        </PageCallout>
      </>
    ),
  },
  report: {
    eyebrow: "Safety Report and Release Gate",
    title: "Pass, warning, or block. With evidence.",
    lead: "Overall decision, fidelity, migration findings, functional and performance regressions, attempted external effects, sanitization status, and cleanup proof.",
    description: "Pass, warning, or block with evidence on the pull request.",
    features: [
      { title: "Causal", body: "“Error rate increased” is insufficient. Connect the change, behavior, workflow, evidence, and remediation." },
      { title: "Actionable", body: "Suggested safer patterns, not a dump of logs." },
      { title: "Policy", body: "Teams enforce thresholds on locks, error rate, egress, latency, and fidelity." },
      { title: "Auditable", body: "Videos, traces, queries, reproduction steps, and cleanup proof." },
    ],
    related: [
      { href: "/product/oracle", title: "Differential Oracle", description: "Where comparisons are produced." },
      { href: "/product/fidelity", title: "Fidelity Graph", description: "What the twin actually reproduced." },
      { href: "/solutions/release-gates", title: "Release gates", description: "Organization-wide ship policy." },
    ],
    body: (
      <>
        <h2>What the report contains</h2>
        <ul>
          <li>Overall decision: pass, warning, or block</li>
          <li>Confidence and fidelity indicators</li>
          <li>Migration findings</li>
          <li>Functional, performance, behavioral, and UX findings</li>
          <li>Attempted external effects</li>
          <li>Data-sanitization status</li>
          <li>Resource and cost summary</li>
          <li>Cleanup proof</li>
          <li>Videos, traces, queries, reproduction steps, and suggested remediations</li>
        </ul>
        <h2>What teams can enforce</h2>
        <ul>
          <li>Block if candidate error rate exceeds baseline by 1 percentage point</li>
          <li>Block if an exclusive lock exceeds 2 seconds on a critical table</li>
          <li>Block if unknown external egress is attempted</li>
          <li>Warn if p95 latency increases by more than 15%</li>
          <li>Require approval if fidelity is below 80%</li>
        </ul>
        <h2>Not a dataset</h2>
        <p>
          The output is an auditable gate attached to the pull request, not a preview URL or a dump
          of logs. Reports that are noisy get ignored. Baseline comparisons, expected-difference
          declarations, confidence calibration, and severity policies keep the gate useful.
        </p>
        <PageCallout label="Center the decision">
          Every workflow and report centers on the deployment decision. Environment creation, data,
          agents, and load are supporting systems. If the product becomes a bundle of tools, it has
          failed.
        </PageCallout>
      </>
    ),
  },
  "change-intelligence": {
    eyebrow: "Change Intelligence",
    title: "Test what matters for this change.",
    lead: "Analyze the pull request for affected services, migrations, infrastructure, APIs, events, and critical workflows. Recommend twin fidelity and a validation plan.",
    description: "What to validate for this pull request, and at what fidelity.",
    features: [
      { title: "Services", body: "Which parts of the stack this change actually touches." },
      { title: "Migrations", body: "Database migrations added or modified." },
      { title: "Contracts", body: "API and event-schema changes, plus third-party dependency changes." },
      { title: "Plan", body: "Required twin fidelity and a recommended validation plan." },
    ],
    related: PRODUCT_RELATED,
    body: (
      <>
        <h2>It looks at</h2>
        <ul>
          <li>Services affected</li>
          <li>Database migrations added or modified</li>
          <li>Infrastructure changes</li>
          <li>API and event-schema changes</li>
          <li>Third-party dependency changes</li>
          <li>Critical workflows likely to be affected</li>
          <li>Required twin fidelity</li>
          <li>Recommended validation plan</li>
        </ul>
        <p>
          This module reduces cost by testing what matters rather than blindly reproducing everything.
          Universal integration would consume the company. A narrow provider contract and one complete
          stack first is the strategy. Publish a fidelity model rather than pretending unsupported
          components are reproduced.
        </p>
        <h2>Pull-request flow</h2>
        <ol>
          <li>Analyze the code, infrastructure, and migration diff.</li>
          <li>Assign a risk profile and propose a validation plan.</li>
          <li>The developer presses Create Wind Tunnel, or policy triggers it automatically.</li>
          <li>An isolated environment is provisioned and a safe snapshot restored.</li>
          <li>Migrations and deployment steps execute exactly as planned for production.</li>
          <li>Baseline and candidate receive equivalent workloads.</li>
          <li>A report is attached. The environment is destroyed.</li>
        </ol>
      </>
    ),
  },
  oracle: {
    eyebrow: "Differential Oracle",
    title: "Same state. Same behavior. Two versions.",
    lead: "Compare normalized HTTP, status classes, database writes, events, traces, query plans, latency, journeys, and logs. Declared expected differences do not count as regressions.",
    description: "Baseline vs candidate against equivalent state and behavior.",
    features: [
      { title: "HTTP", body: "Normalized responses, status codes, and error classes." },
      { title: "Data", body: "Database writes, events, and queue emissions." },
      { title: "Effects", body: "Third-party effects captured by the firewall ledger." },
      { title: "Runtime", body: "Trace topology, query count and plans, latency, and resource distributions." },
      { title: "Journeys", body: "User-journey outcomes, logs, and exceptions." },
      { title: "Expected diffs", body: "Intended product changes are declared so they are not false regressions." },
    ],
    related: [
      { href: "/product/report", title: "Safety Report", description: "The oracle’s output is the gate." },
      { href: "/product/workload", title: "Workload Studio", description: "Equivalent behavior for both versions." },
      { href: "/product/twins", title: "Isolated Twin", description: "Baseline and candidate in one run." },
    ],
    body: (
      <>
        <h2>Compared surfaces</h2>
        <ul>
          <li>Normalized HTTP responses</li>
          <li>Status codes and error classes</li>
          <li>Database writes</li>
          <li>Event and queue emissions</li>
          <li>Third-party effects</li>
          <li>Trace topology</li>
          <li>Query count and plans</li>
          <li>Latency and resource distributions</li>
          <li>User-journey outcomes</li>
          <li>Logs and exceptions</li>
        </ul>
        <h2>Linkage</h2>
        <p>
          Every run produces normalized artifacts keyed by change ID, twin ID, baseline or candidate,
          scenario, synthetic identity, trace ID, database transaction ID where available, and
          external-effect ID. The report can trace a user-visible failure back through a service
          call, query, lock, migration operation, and attempted third-party effect.
        </p>
        <p>
          The durable differentiation is this Deployment Safety Oracle: understand the change, select
          the relevant production conditions, reproduce those conditions safely, compare baseline and
          candidate, attribute regressions to causes, recommend a decision, and eventually learn from
          whether predictions matched post-deployment reality.
        </p>
      </>
    ),
  },
  fidelity: {
    eyebrow: "Fidelity Graph",
    title: "Say what the twin actually reproduced.",
    lead: "Application services, databases, volume, queues, jobs, third parties, secrets, network, capacity, and traffic shape. Fidelity is a number you can gate on — not a magical truth score.",
    description: "An explicit model of what the twin reproduced.",
    features: [
      { title: "Inspectable", body: "Each component is listed as reproduced, simulated, subset, or missing." },
      { title: "Gatable", body: "Teams can require approval if fidelity is below a policy threshold." },
      { title: "Honest", body: "Missing Twilio callbacks or an internal service is named, not hidden." },
    ],
    related: PRODUCT_RELATED,
    body: (
      <>
        <h2>Example</h2>
        <PagePre>{`Environment fidelity: 87%

Application services       8/8 reproduced
Postgres state             sanitized 12% referential subset
Background workers         3/3 active
Queues                     2/2 simulated
Third-party providers      7/9 simulated
Infrastructure capacity    scaled to 25% with normalized thresholds
Observed traffic coverage  81% of production endpoint volume

Missing: Twilio voice callbacks, internal recommendations service`}</PagePre>
        <h2>What the graph covers</h2>
        <ul>
          <li>Application services</li>
          <li>Databases, data volume, and distribution</li>
          <li>Queues and streams</li>
          <li>Scheduled jobs</li>
          <li>Third-party providers</li>
          <li>Secrets and identities</li>
          <li>Network topology</li>
          <li>Infrastructure capacity</li>
          <li>Traffic shape</li>
        </ul>
        <p>
          Fidelity is not a single magical truth score. It is a transparent summary with inspectable
          components. Guessing is not a fidelity score. Narrow adapters should feel complete. A broad
          compatibility list with unreliable connectors would destroy trust.
        </p>
      </>
    ),
  },
  architecture: {
    eyebrow: "Architecture",
    title: "Hosted control plane. Customer-hosted data plane.",
    lead: "Organizations, policy, and reports live in the control plane. Snapshots, secrets, sanitization, provisioning, egress, and cleanup stay in the customer boundary.",
    description: "Trust boundary, environment lifecycle, isolation, and Postgres strategy.",
    features: [
      { title: "Outbound-only agent", body: "Communication is outbound from the customer agent where possible, with short-lived mTLS." },
      { title: "Idempotent lifecycle", body: "Every state transition is recoverable. A TTL reaper is independent of the orchestrator." },
      { title: "Cost controls", body: "Estimate before provisioning, per-run and daily caps, subsetting, BYOC, TTL." },
    ],
    related: [
      { href: "/product/firewall", title: "Firewall", description: "How side effects are contained." },
      { href: "/docs", title: "Docs", description: "How a twin run works." },
      { href: "/docs/concepts/journal", title: "Journal docs", description: "Lifecycle and isolation in full." },
    ],
    body: (
      <>
        <h2>Trust boundary</h2>
        <h3>Hosted control plane</h3>
        <ul>
          <li>Organizations and projects</li>
          <li>GitHub and CI integration</li>
          <li>Run planning and policy configuration</li>
          <li>Fleet status, aggregated reports, historical comparisons</li>
          <li>Billing and entitlements</li>
        </ul>
        <h3>Customer-hosted data plane</h3>
        <ul>
          <li>Cloud and database discovery</li>
          <li>Snapshot access, sanitization, provisioning</li>
          <li>Secret replacement</li>
          <li>Traffic capture and redaction</li>
          <li>Egress enforcement and workload execution</li>
          <li>Raw logs, traces, and cleanup</li>
        </ul>
        <h2>Cost controls</h2>
        <ul>
          <li>Estimate before provisioning</li>
          <li>Per-run spending cap and per-organization daily cap</li>
          <li>Maximum agent and deterministic concurrency</li>
          <li>Automatic downscaling when statistical confidence is reached</li>
          <li>Snapshot and build caching</li>
          <li>Subsetting instead of full copying by default</li>
          <li>BYOC execution for expensive enterprise workloads</li>
          <li>TTL and inactivity expiration</li>
          <li>Cost attribution by pull request and team</li>
        </ul>
        <p>
          Unlimited free hosted compute is not viable. Free usage should have strict credits or
          require the user’s own cloud and model credentials.
        </p>
      </>
    ),
  },
};

export const PRODUCT_SLUGS = Object.keys(PRODUCT_PAGES);
