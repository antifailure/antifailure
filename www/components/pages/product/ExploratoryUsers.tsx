import {
  Callout,
  CodePanel,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  Prose,
  RelatedGrid,
  SpecTable,
  Steps,
} from "@/components/pages/kit";

const SCENARIO_IR = `identity_fixture: returning_pro_user
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
  - confirmation_visible`;

const PERSONAS: [string, string][] = [
  [
    "Impatient — grounded in analytics",
    "Retries Upgrade, submits twice, and refreshes while the request is in flight. Timing is the personality.",
  ],
  [
    "Multi-tab — synthetic hypothesis",
    "Abandons a checkout, opens it again, and submits both. Parallel sessions share stored state.",
  ],
  [
    "Slow mobile — grounded in analytics",
    "High RTT, truncated DOM, and taps that land before the intended control exists.",
  ],
  [
    "Adversarial — synthetic hypothesis",
    "Pushes invalid quantities, odd encodings, and client retries against 429s — not a fuzzing product.",
  ],
  ["Happy path", "New customer following the intended flow."],
  ["Power user", "Returning account with complex stored state."],
  ["Accessibility", "Keyboard, assistive tech, reduced motion."],
  ["Abandon & resume", "Leaves mid-flow and comes back later."],
  ["API client", "Retry storms and idempotency edges."],
];

export function ExploratoryUsersPage() {
  return (
    <PageShell inset>
      <PageHero
        eyebrow="Workload Studio"
        title="AI discovers. Deterministic systems prove."
        lead="Exploratory users live in Workload Studio, beside observed and deterministic traffic. Agents pursue goals, find unanticipated paths, and compile them into versioned scenarios the runner can scale."
      />

      <PageSection>
        <PageHeading title="<strong>Goals, not selectors.</strong> Personality changes timing and decisions — not merely prompt wording." />
        <Prose className="mt-6">
          <p>
            Exploratory users receive a goal, a synthetic identity, and behavioral traits. Personas
            are grounded in product analytics, or labeled as synthetic hypotheses. They live here as
            one traffic source among observed patterns and deterministic journeys.
          </p>
        </Prose>
        <div className="mt-14">
          <SpecTable rows={PERSONAS} />
        </div>
      </PageSection>

      <PageSection tone="white">
        <PageHeading title="<strong>Compile, then prove.</strong> No LLM at each step." />
        <Prose className="mt-6">
          <p>
            Useful discoveries become a versioned intermediate representation. The deterministic
            runner executes it at controlled concurrency. AI may intervene only when the interface
            changes or an unexplained state appears.
          </p>
        </Prose>
        <div className="mt-10 max-w-[720px]">
          <CodePanel label="scenario: impatient_upgrade">{SCENARIO_IR}</CodePanel>
        </div>
        <Steps
          items={[
            { title: "Explore", body: "Goal, synthetic account, and traits — not a fixed CSS path." },
            { title: "Discover", body: "Unanticipated workflows, friction, and functional failures." },
            { title: "Compile", body: "Versioned scenario IR the runner can replay without an LLM." },
            { title: "Prove", body: "Scale the journey. The oracle compares baseline and candidate." },
          ]}
        />
      </PageSection>

      <PageSection tone="sage">
        <PageHeading title="<strong>Not a synthetic-user company.</strong> Exploratory users are a traffic source, not the category." />
        <div className="mt-12 max-w-[720px]">
          <Callout label="Do not position exploration as more personalities">
            That feature can be copied. The defensible system links exploratory behavior to
            infrastructure and database evidence. We will not claim thousands of AI agents behave
            exactly like humans. Charge for deployments protected — not for the number of AI
            personalities.
          </Callout>
        </div>
        <div className="mt-10">
          <SpecTable
            rows={[
              ["AI QA platform", "Verification is a layer. The product is deployment safety."],
              [
                "Synthetic-user company",
                "They live inside Workload Studio, beside observed traffic.",
              ],
              ["More personalities", "A copied prompt library is not a durable wedge."],
              [
                "Human-identical agents",
                "Exploration is useful. Exact human mimicry is not a claim.",
              ],
            ]}
          />
        </div>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/product/workload", title: "Workload Studio", description: "Observed, deterministic, and exploratory traffic." },
          { href: "/product/oracle", title: "Differential Oracle", description: "Where compiled journeys become baseline-versus-candidate evidence." },
          { href: "/product", title: "Product", description: "The company is not a synthetic-user company." },
        ]}
      />
    </PageShell>
  );
}
