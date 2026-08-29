import type { ReactNode } from "react";
import {
  Callout,
  CodePanel,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
  Split,
  Stage,
  Steps,
} from "@/components/pages/kit";
import { WorkloadScene } from "@/components/home/visuals/WorkloadScene";
import {
  CheckRow,
  Hairline,
  MonoLabel,
  Panel,
  QueueChip,
  Receipt,
  StatusPill,
} from "@/components/home/visuals/primitives";
import { cn } from "@/lib/cn";

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

const SUPPORTING = [
  { title: "Happy path", body: "New customer following the intended flow." },
  { title: "Power user", body: "Returning account with complex stored state." },
  { title: "Accessibility", body: "Keyboard, assistive tech, reduced motion." },
  { title: "Abandon & resume", body: "Leaves mid-flow and comes back later." },
  { title: "API client", body: "Retry storms and idempotency edges." },
] as const;

export function ExploratoryUsersPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Workload Studio"
        title="AI discovers. Deterministic systems prove."
        lead="Exploratory users live in Workload Studio, beside observed and deterministic traffic. Agents pursue goals, find unanticipated paths, and compile them into versioned scenarios the runner can scale."
        visual={
          <Stage>
            <WorkloadScene />
          </Stage>
        }
      />

      <PageSection>
        <PageHeading title="<strong>Goals, not selectors.</strong> Personality changes timing and decisions — not merely prompt wording." />
        <p className="mt-6 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          Exploratory users receive a goal, a synthetic identity, and behavioral traits. Personas are
          grounded in product analytics, or labeled as synthetic hypotheses. They live here as one traffic
          source among observed patterns and deterministic journeys.
        </p>

        <div className="mt-16 grid grid-cols-2 gap-5 max-lg:grid-cols-1">
          <PersonaCard
            kicker="impatient"
            source="analytics"
            title="Double-click, retry, refresh"
            body="Retries Upgrade, submits twice, and refreshes while the request is in flight. Timing is the personality."
            visual={<ImpatientVisual />}
          />
          <PersonaCard
            kicker="multi-tab"
            source="hypothesis"
            title="Two tabs, one identity"
            body="Abandons a checkout, opens it again, and submits both. Parallel sessions share stored state."
            visual={<MultiTabVisual />}
          />
          <PersonaCard
            kicker="slow mobile"
            source="analytics"
            title="Tap on an incomplete page"
            body="High RTT, truncated DOM, and taps that land before the intended control exists."
            visual={<SlowMobileVisual />}
          />
          <PersonaCard
            kicker="adversarial"
            source="hypothesis"
            title="Malformed input, then retry"
            body="Pushes invalid quantities, odd encodings, and client retries against 429s — not a fuzzing product."
            visual={<AdversarialVisual />}
          />
        </div>

        <ul className="mt-5 grid grid-cols-5 gap-px bg-black/10 ring-1 ring-black/10 max-lg:grid-cols-2 max-md:grid-cols-1">
          {SUPPORTING.map((item) => (
            <li key={item.title} className="bg-[#f7f7f5] px-5 py-4">
              <MonoLabel>{item.title}</MonoLabel>
              <p className="mt-2 text-[14px] leading-5 tracking-extra-tight text-gray-new-40">{item.body}</p>
            </li>
          ))}
        </ul>
      </PageSection>

      <PageSection tone="white">
        <Split
          visual={<CodePanel label="scenario: impatient_upgrade">{SCENARIO_IR}</CodePanel>}
        >
          <PageHeading title="<strong>Compile, then prove.</strong> No LLM at each step." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Useful discoveries become a versioned intermediate representation. The deterministic runner
            executes it at controlled concurrency. AI may intervene only when the interface changes or
            an unexplained state appears.
          </p>
          <div className="mt-8">
            <Panel className="bg-[#f7f7f5]">
              <div className="flex items-center justify-between px-4 py-2.5">
                <MonoLabel>journey compiler</MonoLabel>
                <QueueChip className="text-[#285D49] ring-[#33bf00]/40">no LLM at scale</QueueChip>
              </div>
              <Hairline />
              <div className="space-y-2 px-4 py-3">
                <CheckRow ok>goal · upgrade to Pro without waiting</CheckRow>
                <CheckRow ok="warn">DOM · duplicate Upgrade click +90ms</CheckRow>
                <CheckRow ok>compiled · impatient_upgrade v3</CheckRow>
                <CheckRow ok>runner · assertions attached</CheckRow>
              </div>
            </Panel>
          </div>
        </Split>
        <div className="mt-16">
          <Steps
            items={[
              { title: "Explore", body: "Goal, synthetic account, and traits — not a fixed CSS path." },
              { title: "Discover", body: "Unanticipated workflows, friction, and functional failures." },
              { title: "Compile", body: "Versioned scenario IR the runner can replay without an LLM." },
              { title: "Prove", body: "Scale the journey. The oracle compares baseline and candidate." },
            ]}
          />
        </div>
      </PageSection>

      <PageSection tone="sage">
        <PageHeading title="<strong>Not a synthetic-user company.</strong> Exploratory users are a traffic source, not the category." />
        <div className="mt-12 grid grid-cols-2 gap-10 max-lg:grid-cols-1">
          <Callout label="Do not position exploration as more personalities">
            That feature can be copied. The defensible system links exploratory behavior to
            infrastructure and database evidence. We will not claim thousands of AI agents behave
            exactly like humans. Charge for deployments protected — not for the number of AI
            personalities.
          </Callout>
          <Panel className="bg-white/70">
            <div className="px-5 py-3">
              <MonoLabel>What exploratory users are not</MonoLabel>
            </div>
            <Hairline />
            <ul>
              {[
                ["AI QA platform", "Verification is a layer. The product is deployment safety."],
                ["Synthetic-user company", "They live inside Workload Studio, beside observed traffic."],
                ["More personalities", "A copied prompt library is not a durable wedge."],
                ["Human-identical agents", "Exploration is useful. Exact human mimicry is not a claim."],
              ].map(([label, body], i) => (
                <li key={label} className={cn(i > 0 && "border-t border-black/8")}>
                  <div className="flex items-start gap-3 px-5 py-3.5">
                    <StatusPill tone="BLOCK" className="mt-0.5 shrink-0">
                      no
                    </StatusPill>
                    <div className="min-w-0">
                      <div className="text-[15px] tracking-extra-tight text-black">{label}</div>
                      <p className="mt-1 text-[13px] leading-5 tracking-extra-tight text-gray-new-40">{body}</p>
                    </div>
                  </div>
                </li>
              ))}
            </ul>
          </Panel>
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

function PersonaCard({
  kicker,
  source,
  title,
  body,
  visual,
}: {
  kicker: string;
  source: "analytics" | "hypothesis";
  title: string;
  body: string;
  visual: ReactNode;
}) {
  return (
    <Panel className="flex flex-col bg-[#f7f7f5]">
      <header className="flex h-9 items-center justify-between gap-3 px-4">
        <MonoLabel className="text-black/70">{kicker}</MonoLabel>
        <QueueChip className={source === "hypothesis" ? "text-amber-800 ring-amber-700/40" : undefined}>
          {source === "analytics" ? "grounded in analytics" : "synthetic hypothesis"}
        </QueueChip>
      </header>
      <Hairline />
      <div className="min-h-[188px]" aria-hidden>
        {visual}
      </div>
      <Hairline />
      <div className="px-4 py-4">
        <h3 className="text-[18px] leading-snug tracking-extra-tight text-black">{title}</h3>
        <p className="mt-2 text-[14px] leading-6 tracking-extra-tight text-gray-new-40">{body}</p>
      </div>
    </Panel>
  );
}

function ImpatientVisual() {
  return (
    <div className="flex h-full flex-col justify-between px-4 py-3">
      <div>
        <div className="flex items-center justify-between">
          <MonoLabel>/settings/billing</MonoLabel>
          <MonoLabel className="tabular-nums">returning_pro_user</MonoLabel>
        </div>
        <div className="mt-3 flex items-center justify-between gap-3 ring-1 ring-black/10 bg-white px-3 py-2.5">
          <span className="text-[13px] tracking-extra-tight text-black">Upgrade to Pro</span>
          <span className="relative inline-flex items-center">
            <span className="px-2 py-1 font-mono text-[10px] tracking-extra-tight ring-1 ring-black/20">
              Upgrade
            </span>
            <Spark className="-right-1 -top-2" />
            <Spark className="-right-3 top-0" />
          </span>
        </div>
      </div>
      <ol className="mt-3 space-y-1 font-mono text-[10px] tabular-nums tracking-extra-tight">
        <li className="flex justify-between text-black/70">
          <span className="text-black/35">0ms</span>
          <span>click Upgrade</span>
        </li>
        <li className="flex justify-between text-black">
          <span className="text-black/35">90ms</span>
          <span>click Upgrade</span>
        </li>
        <li className="flex justify-between text-amber-800">
          <span>300ms</span>
          <span>retry_submit</span>
        </li>
        <li className="flex justify-between text-black/70">
          <span className="text-black/35">450ms</span>
          <span>refresh</span>
        </li>
      </ol>
      <QueueChip className="mt-3 w-fit text-amber-800 ring-amber-700/40">duplicate submit</QueueChip>
    </div>
  );
}

function MultiTabVisual() {
  return (
    <div className="relative flex h-full flex-col justify-center px-4 py-3">
      <Receipt className="relative z-10 mr-6">
        <div className="flex items-center gap-2">
          <span className="size-1.5 rounded-full bg-[#33bf00]" />
          <span>tab A · /checkout</span>
        </div>
        <div className="mt-1.5 flex items-center justify-between text-black">
          <span>Confirm upgrade</span>
          <span className="text-black/40">submitting</span>
        </div>
      </Receipt>
      <Receipt className="relative z-0 -mt-2 ml-6 text-amber-800">
        <div className="flex items-center gap-2">
          <span className="size-1.5 rounded-full bg-amber-600" />
          <span>tab B · /checkout</span>
        </div>
        <div className="mt-1.5 flex items-center justify-between">
          <span>Confirm upgrade</span>
          <span>parallel +1.2s</span>
        </div>
      </Receipt>
      <div className="mt-3 flex items-center justify-between">
        <MonoLabel>shared identity · returning_pro_user</MonoLabel>
        <QueueChip>two writers</QueueChip>
      </div>
    </div>
  );
}

function SlowMobileVisual() {
  return (
    <div className="flex h-full items-center gap-4 px-4 py-3">
      <div className="w-[112px] shrink-0 overflow-hidden bg-white ring-1 ring-black/15">
        <div className="flex h-5 items-center justify-between px-2">
          <MonoLabel className="text-[8px]">3G</MonoLabel>
          <span className="flex gap-px">
            <span className="h-2 w-0.5 bg-black/50" />
            <span className="h-1.5 w-0.5 self-end bg-black/25" />
            <span className="h-1 w-0.5 self-end bg-black/15" />
          </span>
        </div>
        <Hairline />
        <div className="space-y-1.5 px-2 py-2">
          <div className="h-1.5 w-14 bg-black/15" />
          <div className="h-1.5 w-20 bg-black/10" />
          <div className="h-8 w-full bg-black/[0.04] ring-1 ring-black/10" />
          <div className="relative h-6 w-full bg-black/10">
            <span className="absolute inset-y-0 left-0 w-[42%] bg-[#33bf00]/30" />
          </div>
          <div className="h-5 w-[70%] bg-black/8 ring-1 ring-dashed ring-black/15" />
        </div>
      </div>
      <div className="min-w-0 flex-1 space-y-2">
        <div className="flex items-baseline justify-between">
          <MonoLabel>rtt</MonoLabel>
          <span className="font-mono text-[13px] tabular-nums tracking-extra-tight">420ms</span>
        </div>
        <div className="flex items-baseline justify-between">
          <MonoLabel>dom</MonoLabel>
          <span className="font-mono text-[13px] tabular-nums tracking-extra-tight text-amber-800">42%</span>
        </div>
        <CheckRow ok="warn">tap before Upgrade mounts</CheckRow>
        <CheckRow ok="run">image decode stalled</CheckRow>
      </div>
    </div>
  );
}

function AdversarialVisual() {
  return (
    <div className="flex h-full flex-col px-4 py-3">
      <div className="flex items-center justify-between">
        <MonoLabel>POST /api/upgrade</MonoLabel>
        <StatusPill tone="WARN">429</StatusPill>
      </div>
      <pre className="mt-2 flex-1 overflow-hidden bg-white px-3 py-2 font-mono text-[11px] leading-5 tracking-extra-tight text-black/70 ring-1 ring-black/10">
        {`{
  "qty": -1,
  "coupon": "\\x00 OR 1=1"
}`}
      </pre>
      <div className="mt-2 flex flex-wrap items-center gap-1.5">
        <QueueChip className="text-amber-800 ring-amber-700/40">retry 1</QueueChip>
        <QueueChip className="text-amber-800 ring-amber-700/40">retry 2</QueueChip>
        <QueueChip>idempotency-key</QueueChip>
      </div>
    </div>
  );
}

function Spark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 12 12" className={cn("absolute size-3 overflow-visible", className)} aria-hidden>
      <path
        d="M6 0 L6.4 4.2 L11 3.2 L7.2 6 L11 9.2 L6.4 7.6 L6 12 L5.6 7.6 L1 9.2 L4.8 6 L1 3.2 L5.6 4.2 Z"
        fill="#00E599"
      />
    </svg>
  );
}
