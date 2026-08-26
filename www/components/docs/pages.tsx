import Link from "next/link";
import type { ReactNode } from "react";
import { CopyCli } from "@/components/Pills";
import type { DocSlug } from "@/lib/docs";
import { Callout, H1, H2, Lead, P, Pre, Table, Ul } from "./Prose";

export function IntroPage() {
  return (
    <>
      <H1>Introduction</H1>
      <Lead>
        Antifailure is a pre-production deployment safety platform. Connect a repository and a cloud
        environment. For every risky change, it creates an isolated production twin, fills it with safe
        production-shaped state, exercises it with representative and adversarial behavior, and reports
        whether the deployment is safe to ship.
      </Lead>
      <H2>The question that matters</H2>
      <P>
        Teams can create preview frontends easily. They cannot easily answer: what will happen when this
        exact change meets the real system — data shape, concurrency, user behavior, queues, workers,
        integrations, and the deployment process.
      </P>
      <P>
        Staging fails that question because it differs from production in too many dimensions at once:
        volume, long-tail records, timing, rolling deploys, schema coexistence, third-party integrations,
        secrets, capacity, and background jobs. A change can pass unit tests, integration tests, end-to-end
        tests, and a manual staging check, then still fail in production.
      </P>
      <H2>What Antifailure does</H2>
      <Ul
        items={[
          "Twin — an isolated, temporary copy of the relevant application stack.",
          "State — a safe, referentially consistent, production-shaped dataset.",
          "Containment — no charging cards, emailing users, invoking production webhooks, or writing production systems.",
          "Behavior — captured workload patterns, deterministic scenarios, and exploratory AI users.",
          "Comparison — current and proposed versions against equivalent state and behavior.",
          "Judgment — functional, database, performance, integration, and behavioral regressions.",
          "Evidence — an auditable pass, warning, or block on the pull request.",
          "Cleanup — destroy temporary resources and prove that cleanup completed.",
        ]}
      />
      <H2>The wedge</H2>
      <P>
        The first wedge is not universal multicloud cloning. It is automated safety validation for risky
        Postgres-backed web deployments, especially schema migrations.
      </P>
      <P>
        Crowdi is a feature inside{" "}
        <Link href="/docs/workload" className="text-white underline decoration-white/25 underline-offset-4">
          Workload Studio
        </Link>
        . It contributes exploratory AI users. It is not the product category, the buyer, or the main value.
      </P>
      <H2>The promise</H2>
      <P>
        Every meaningful change gets a realistic, disposable proving ground. The promise is not that the
        platform mathematically guarantees a deployment cannot fail. That claim would be indefensible.
      </P>
      <Callout label="What we will not claim">
        Zero rollback. No deployment can ever fail. Thousands of AI agents behave exactly like humans. One
        click perfectly clones every cloud. Open source bypasses compliance. Use measurable evidence
        instead.
      </Callout>
    </>
  );
}

export function QuickstartPage() {
  return (
    <>
      <H1>Quickstart</H1>
      <Lead>
        The standard path is: connect GitHub, install a customer-hosted runner, attach Postgres, approve
        isolation policy, then run a baseline. The platform generates repository config. You should not have
        to hand-author YAML to get a first useful run.
      </Lead>
      <H2>Init command</H2>
      <P>
        The CLI is the intended local entry. It is not a published package. Copying it does not install
        anything today.
      </P>
      <div className="mt-5">
        <CopyCli variant="dark" />
      </div>
      <H2>Initial setup</H2>
      <Ul
        items={[
          "Install the GitHub application and select a repository.",
          "Install a customer-hosted runner, or grant a narrowly scoped cloud role.",
          "Connect a Postgres source or an approved backup.",
          "Review automatically detected sensitive fields and discovered outbound services.",
          "Approve isolation, cost, and retention policies.",
          "Run a baseline validation against the current production version.",
        ]}
      />
      <H2>Create twin</H2>
      <P>
        Every customer sees the same action: Create Wind Tunnel. Behind it, the platform chooses the
        adapter and fidelity level. The system must disclose what it reproduced and what it could not. A
        clear fidelity score is more trustworthy than pretending every cloud topology can be cloned
        perfectly.
      </P>
      <H2>What the first complete run covers</H2>
      <P>
        For a GitHub-hosted Next.js application backed by Postgres or Supabase: an isolated candidate
        environment, a sanitized snapshot, Stripe and email contained, the proposed migration under
        representative traffic, and a useful safety report on the pull request.
      </P>
      <Callout label="Not in the first scope">
        Arbitrary Kubernetes topologies, full AWS/Azure/GCP parity, MongoDB, ClickHouse, native mobile,
        every third-party API, synchronous live-traffic diversion, or a guarantee that every production
        incident is predicted.
      </Callout>
    </>
  );
}

export function PullRequestsPage() {
  return (
    <>
      <H1>Pull requests</H1>
      <Lead>
        When a pull request contains a risky change, Antifailure proposes a validation plan, runs an
        isolated twin, and attaches pass, warning, or block — then destroys the environment.
      </Lead>
      <H2>Flow</H2>
      <Ul
        items={[
          "Analyze the code, infrastructure, and migration diff.",
          "Assign a risk profile and propose a validation plan.",
          "The developer presses Create Wind Tunnel, or policy triggers it automatically.",
          "Provision an isolated environment and restore a safe snapshot or subset.",
          "Execute migrations and deployment steps exactly as planned for production.",
          "Give baseline and candidate equivalent workloads.",
          "Exploratory agents probe changed and critical workflows; useful journeys become deterministic scenarios.",
          "Compare database, application, queue, network, and infrastructure signals.",
          "Attach the report. Policy marks pass, warning, or block.",
          "Destroy the environment when the run expires or the pull request closes.",
        ]}
      />
      <H2>What a useful result looks like</H2>
      <Pre>{`BLOCKED: unsafe schema migration

Migration 20260824_add_billing_status held an ACCESS EXCLUSIVE
lock on subscriptions for 27.4 seconds. During equivalent traffic,
checkout p99 increased from 820 ms to 6.9 seconds and 11.8% of
upgrade attempts timed out. The previous application version also
failed to deserialize rows written by the candidate version, so
rolling rollback is unsafe.

Suggested action: add the nullable column without a default,
backfill in batches, deploy dual-read compatibility, then enforce
the constraint in a later migration.`}</Pre>
      <H2>Preview URL</H2>
      <P>
        Each twin may receive an authenticated preview endpoint such as{" "}
        <span className="font-mono text-[13px] text-white/90">fix-billing-184.preview.company.com</span>.
        It is private by default. Credentials must never be shared across agent sessions unless they
        represent an intentionally reusable synthetic identity.
      </P>
      <P>
        The output is the report, not a preview URL alone. A green preview does not mean the migration is
        safe to ship.
      </P>
    </>
  );
}

export function ConceptsPage() {
  return (
    <>
      <H1>How it works</H1>
      <Lead>
        Antifailure unifies the minimum pieces required to validate a real deployment. None of the pieces
        alone is the product.
      </Lead>
      <H2>Change intelligence</H2>
      <P>
        Reads the pull request and decides which services, migrations, infrastructure, APIs, event schemas,
        and workflows matter — then recommends twin fidelity and a validation plan. The point is to test
        what matters rather than blindly reproducing everything.
      </P>
      <H2>Twin orchestrator</H2>
      <P>
        Builds candidate and baseline, creates isolated networking, injects clone-specific configuration,
        replaces production credentials, provisions temporary domains, tracks ownership, enforces TTL and
        budget, and tears everything down.
      </P>
      <H2>Safe state</H2>
      <P>
        Snapshot restore, referentially consistent subsetting, deterministic masking, uniqueness
        preservation, deletion of tokens and secrets, free-text PII detection, and a sanitization evidence
        report. Common Postgres cases are in-product. Deep enterprise data platforms can be an external
        provider rather than rebuilt in version one.
      </P>
      <H2>Containment, behavior, comparison</H2>
      <Ul
        items={[
          <>
            <Link href="/docs/firewall" className="text-white underline decoration-white/25 underline-offset-4">
              Side-effect firewall
            </Link>{" "}
            — no default public egress; simulators instead of the real world.
          </>,
          <>
            <Link href="/docs/workload" className="text-white underline decoration-white/25 underline-offset-4">
              Workload Studio
            </Link>{" "}
            — observed patterns, deterministic scenarios, Crowdi exploration.
          </>,
          "Differential oracle — equivalent state and workloads against baseline and candidate, with declared expected differences so intended product changes are not false regressions.",
        ]}
      />
      <H2>Fail closed</H2>
      <P>
        Unknown outbound destinations, unresolved secrets, incomplete cleanup policies, or missing
        isolation should block a run by default. Convenience must not silently override containment.
      </P>
      <H2>Cleanup is a safety property</H2>
      <P>
        Resource deletion is not a background convenience. Every environment has a TTL, a cost ceiling, an
        independent cleanup path, and a verifiable destruction record.
      </P>
    </>
  );
}

export function MigrationSafetyPage() {
  return (
    <>
      <H1>Migration safety</H1>
      <Lead>
        The flagship module. Schema migrations create failures conventional tests miss: exclusive locks,
        table rewrites, pool exhaustion, plan regressions, rare constraint failures, old/new incompatibility,
        and rollback that is no longer safe.
      </Lead>
      <H2>What it measures</H2>
      <Ul
        items={[
          "Lock acquisition and duration; blocked statements and cumulative blocked time.",
          "Long-running transactions, table rewrites, index-build behavior, constraint failures.",
          "Query-plan changes and latency distributions; connection-pool pressure.",
          "CPU, memory, IOPS, disk, network; replication lag; write-ahead log growth.",
          "Old/new application compatibility; expand-and-contract safety; rollback feasibility.",
          "Candidate writes that old code cannot read.",
        ]}
      />
      <H2>Killer demo</H2>
      <P>
        A defaulted column on <span className="font-mono text-[13px]">subscriptions</span> holds ACCESS
        EXCLUSIVE for 27.4 seconds. Under equivalent traffic, checkout p99 moves from 820 ms to 6.9 s.
        Duplicate billing can reach the Stripe simulator when an impatient Upgrade double-click is compiled
        into a scenario. Baseline stays healthy. The GitHub check blocks and explains both defects.
      </P>
      <Callout label="Safer pattern">
        Add the nullable column without a default, backfill in batches, deploy dual-read compatibility,
        then enforce the constraint in a later migration.
      </Callout>
      <H2>Why this is the wedge</H2>
      <P>
        Migration failures are measurable, have a technical buyer, and staging systematically misses them.
        The first supported stack should be exceptional. A broad compatibility list with unreliable
        connectors would destroy trust.
      </P>
    </>
  );
}

export function FirewallPage() {
  return (
    <>
      <H1>Side-effect firewall</H1>
      <Lead>
        Cloned applications must not act on the real world. There is no default public egress. Unknown
        destinations deny.
      </Lead>
      <H2>Controls</H2>
      <Ul
        items={[
          "Clone-local DNS and a mandatory egress gateway.",
          "Domain, IP, protocol, method, and operation policies.",
          "Detection of direct-IP bypass attempts.",
          "Stateful provider simulators; read-only forwarding only for explicitly approved endpoints.",
          "Request and response redaction; an attempted-effect ledger.",
        ]}
      />
      <H2>Example behavior</H2>
      <Table
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
      <Callout label="Fail closed">
        Unknown outbound destinations, unresolved secrets, or missing isolation block the run. The ledger
        records attempted effects, including denies.
      </Callout>
    </>
  );
}

export function WorkloadPage() {
  return (
    <>
      <H1>Workload Studio</H1>
      <Lead>
        Three traffic sources. AI discovers journeys. Deterministic systems prove them at scale. Crowdi is
        a feature here, not a standalone category.
      </Lead>
      <H2>Observed production patterns</H2>
      <P>
        Derived from ingress traces, API telemetry, OpenTelemetry, session recordings, analytics, or
        customer-provided samples. Requests are redacted and normalized before storage or replay.
      </P>
      <H2>Deterministic scenarios</H2>
      <P>
        Versioned journeys that reproduce known critical workflows at controlled concurrency. These provide
        repeatability, statistical comparison, and CI enforcement.
      </P>
      <Pre>{`scenario: impatient_upgrade
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
  - confirmation_visible`}</Pre>
      <P>
        The deterministic runner executes this representation without an LLM call at each step. AI can
        intervene when the interface changes or an unexplained state is encountered.
      </P>
      <H2>Crowdi exploration</H2>
      <P>
        Crowdi-style agents receive goals, context, synthetic accounts, and behavioral traits. They
        navigate the application, discover paths, test ambiguous states, and explain friction. Useful
        discoveries compile into candidate regression scenarios.
      </P>
      <P>
        Personalities should affect decision-making and timing, not merely prompt wording. They should be
        grounded in product analytics or explicitly labeled as synthetic hypotheses. Do not position Crowdi
        as “more personalities.” The defensible system links exploratory behavior to infrastructure and
        database evidence.
      </P>
      <Callout label="Production traffic">
        A mix control must not imply that production requests are synchronously diverted. Shadowing is
        asynchronous and must never delay or alter production responses.
      </Callout>
    </>
  );
}

export function ReportPage() {
  return (
    <>
      <H1>Safety report</H1>
      <Lead>
        The output is an evidence-backed pass, warning, or block — not a dataset or a preview URL alone.
        Reports must be causal and actionable. “Error rate increased” is insufficient.
      </Lead>
      <H2>What the report contains</H2>
      <Ul
        items={[
          "Overall decision; confidence and fidelity indicators.",
          "Migration findings; functional, performance, behavioral, and UX findings.",
          "Attempted external effects; data-sanitization status.",
          "Resource and cost summary; cleanup proof.",
          "Videos, traces, queries, reproduction steps, and suggested remediations.",
        ]}
      />
      <H2>Release policy examples</H2>
      <Ul
        items={[
          "Block if candidate error rate exceeds baseline by 1 percentage point.",
          "Block if an exclusive lock exceeds 2 seconds on a critical table.",
          "Block if unknown external egress is attempted.",
          "Warn if p95 latency increases by more than 15%.",
          "Require approval if fidelity is below 80%.",
        ]}
      />
      <H2>Fidelity graph</H2>
      <P>
        Fidelity is not a single magical truth score. It is a transparent summary of what the twin
        reproduced: services, databases, queues, third-party providers, capacity, and traffic shape.
      </P>
      <Pre>{`Environment fidelity: 87%

Application services       8/8 reproduced
Postgres state             sanitized 12% referential subset
Background workers         3/3 active
Queues                     2/2 simulated
Third-party providers      7/9 simulated
Infrastructure capacity    scaled to 25% with normalized thresholds
Observed traffic coverage  81% of production endpoint volume

Missing: Twilio voice callbacks, internal recommendations service`}</Pre>
      <P>
        AI agents are excellent at exploration. They are not economical or reproducible load generators.
        The platform uses AI to discover journeys and conventional execution engines to reproduce them at
        scale.
      </P>
    </>
  );
}

export function ArchitecturePage() {
  return (
    <>
      <H1>Architecture</H1>
      <Lead>
        Default enterprise architecture: a hosted control plane and a customer-hosted data plane. Raw
        snapshots, secrets, and captured request bodies stay inside the customer boundary.
      </Lead>
      <H2>Trust boundary</H2>
      <P>
        The control plane holds organizations, GitHub and CI integration, run planning, policy, fleet
        status, aggregated reports, history, and billing. The data plane holds discovery, snapshot access,
        sanitization, provisioning, secret replacement, traffic capture, egress enforcement, workload
        execution, raw logs, and cleanup.
      </P>
      <P>
        Communication is outbound-only from the customer agent where possible, authenticated with
        short-lived mTLS credentials.
      </P>
      <H2>Environment lifecycle</H2>
      <Pre>{`REQUESTED
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
  -> DESTROYED`}</Pre>
      <P>
        Every state transition is idempotent and recoverable. A separate TTL reaper operates independently
        from the main orchestrator.
      </P>
      <H2>Isolation</H2>
      <Ul
        items={[
          "No production write credentials or production database route.",
          "No default internet route; separate DNS policy and secrets namespace.",
          "Temporary workload identity; resource tags with run ID and expiration.",
          "Hard cost ceiling; admission policy that rejects unowned resources.",
          "Independent cleanup controller.",
        ]}
      />
      <H2>Postgres</H2>
      <P>
        Adapters expose a common lifecycle: discover, snapshot, restore, sanitize, augment,
        apply_migrations, observe, destroy. Initial implementation: PostgreSQL logical restore, provider-native
        snapshots or copy-on-write branches when supported, deterministic masking inside the customer
        boundary, referential subsetting, and lock/statement/plan instrumentation.
      </P>
      <P>
        Supabase support must account for more than the public schema. A database clone can include Auth
        records and roles while excluding Storage objects, Edge Functions, and other project configuration.
      </P>
    </>
  );
}

export function OpenSourcePage() {
  return (
    <>
      <H1>Open source</H1>
      <Lead>
        The software handles cloud permissions, production-derived state, secrets, networking, and
        expensive infrastructure. Inspectability matters for the components inside the customer trust
        boundary. There is no public GitHub yet.
      </Lead>
      <H2>Planned open-source surface</H2>
      <Ul
        items={[
          "Customer agent and local CLI.",
          "Provisioning contracts and Postgres adapter.",
          "Sanitization engine and policy format.",
          "Egress gateway and core provider simulators.",
          "Deterministic runner and cleanup controller.",
          "Local report format and Docker Compose development mode.",
        ]}
      />
      <H2>What stays commercial</H2>
      <P>
        Governance, SSO, advanced RBAC, approval workflows, audit retention, private connectivity,
        fleet management, historical analytics, managed updates, and large-scale orchestration. Unlimited
        free hosted compute is not the model.
      </P>
      <Callout label="Safety is not an enterprise add-on">
        Do not put the only trustworthy security controls behind a paid gate. The open product must still
        be safe. Enterprise code adds governance, organizational control, evidence, scale, and managed
        operations.
      </Callout>
      <H2>CLI</H2>
      <P>The init command is shown for when the package exists. It is not published.</P>
      <div className="mt-5">
        <CopyCli variant="dark" />
      </div>
    </>
  );
}

export const DOC_PAGES: Record<DocSlug, () => ReactNode> = {
  quickstart: QuickstartPage,
  "pull-requests": PullRequestsPage,
  concepts: ConceptsPage,
  "migration-safety": MigrationSafetyPage,
  firewall: FirewallPage,
  workload: WorkloadPage,
  report: ReportPage,
  architecture: ArchitecturePage,
  "open-source": OpenSourcePage,
};
