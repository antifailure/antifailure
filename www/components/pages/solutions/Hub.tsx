import Link from "next/link";
import { PageHeading, PageHero, PageSection, PageShell, RelatedGrid } from "@/components/pages/kit";

const ICP = [
  { value: "20–300", label: "engineers. Fast-growing SaaS and internet companies, not a first sale into regulated enterprise." },
  { value: "Postgres", label: "backed applications with enough production data that toy fixtures are misleading." },
  { value: "Daily / weekly", label: "production deploys, a real CI/CD process, and a platform engineer who owns reliability." },
] as const;

const TEAMS = [
  {
    href: "/solutions/saas",
    title: "B2B SaaS",
    body: "Daily deploys, expanding schemas, and staging that drifted years ago. Tenant-shaped state without production identities.",
    metric: "Seats · billing · coexistence",
    span: "col-span-7 max-lg:col-span-1",
    chip: "saas" as const,
  },
  {
    href: "/solutions/fintech",
    title: "Fintech",
    body: "Billing, ledgers, and side effects that must never hit live processors. Simulators, not production APIs.",
    metric: "Stripe simulated · fail closed",
    span: "col-span-5 max-lg:col-span-1",
    chip: "fintech" as const,
  },
  {
    href: "/solutions/ecommerce",
    title: "E-commerce",
    body: "Checkout, inventory, and promotions under production-shaped load. The SKU that breaks the constraint is never in the fixture dump.",
    metric: "Checkout p99 · orders locks",
    span: "col-span-4 max-lg:col-span-1",
    chip: "ecommerce" as const,
  },
  {
    href: "/solutions/marketplaces",
    title: "Marketplaces",
    body: "Queues, workers, dual-writes, and matching logic staging never reproduces. Timing is the bug.",
    metric: "Workers · webhooks blocked",
    span: "col-span-4 max-lg:col-span-1",
    chip: "marketplaces" as const,
  },
  {
    href: "/solutions/devtools",
    title: "Developer tools",
    body: "Schema changes on large tables. Users notice p99 immediately. Locks, plans, and pool pressure show up here first.",
    metric: "Locks · plans · pools",
    span: "col-span-4 max-lg:col-span-1",
    chip: "devtools" as const,
  },
];

const JOBS = [
  {
    href: "/solutions/platform",
    title: "Platform engineering",
    body: "Ephemeral twins from the pull request. No shared-staging ticket.",
    chip: "platform" as const,
  },
  {
    href: "/solutions/migrations",
    title: "Schema migrations",
    body: "Exclusive locks, rewrites, and rollback that is no longer safe.",
    chip: "migrations" as const,
  },
  {
    href: "/solutions/release-gates",
    title: "Release gates",
    body: "Evidence-backed pass, warning, or block — not a preview URL.",
    chip: "gates" as const,
  },
  {
    href: "/solutions/workflow",
    title: "Workflow products",
    body: "Workers, schedules, and long-tail state the twin actually runs.",
    chip: "workflow" as const,
  },
];

function TeamChip({ kind }: { kind: (typeof TEAMS)[number]["chip"] }) {
  if (kind === "saas") {
    return (
      <div className="mb-8 overflow-hidden rounded-[10px] bg-[#E4F1EB] p-5 ring-1 ring-black/8" aria-hidden>
        <div className="font-mono text-[10px] font-medium uppercase tracking-[0.14em] text-black/45">Tenant subset</div>
        <ul className="mt-3 space-y-2">
          {[
            ["acme-prod", "12.4k seats"],
            ["northwind", "3.1k seats"],
            ["helix", "890 seats"],
          ].map(([name, seats]) => (
            <li key={name} className="flex items-center justify-between rounded-md bg-white/80 px-3 py-2 ring-1 ring-black/8">
              <span className="flex items-center gap-2 text-[13px] tracking-extra-tight text-black">
                <span className="size-1.5 rounded-full bg-[#33bf00]" />
                {name}
              </span>
              <span className="font-mono text-[11px] text-black/45">{seats}</span>
            </li>
          ))}
        </ul>
      </div>
    );
  }
  if (kind === "fintech") {
    return (
      <div className="mb-8 overflow-hidden rounded-[10px] bg-[#151617] p-5 text-white" aria-hidden>
        <div className="font-mono text-[10px] font-medium uppercase tracking-[0.14em] text-white/40">Egress firewall</div>
        <ul className="mt-3 space-y-2 font-mono text-[12px]">
          <li className="flex items-center justify-between">
            <span className="text-white/70">Stripe /v1/charges</span>
            <span className="text-[#34d59a]">simulated</span>
          </li>
          <li className="flex items-center justify-between">
            <span className="text-white/70">SendGrid</span>
            <span className="text-white/50">captured</span>
          </li>
          <li className="flex items-center justify-between">
            <span className="text-white/70">api.stripe.com</span>
            <span className="text-red-400">blocked</span>
          </li>
        </ul>
      </div>
    );
  }
  if (kind === "ecommerce") {
    return (
      <div className="mb-8 overflow-hidden rounded-[10px] bg-[#f4f7f5] p-5 ring-1 ring-black/8" aria-hidden>
        <div className="flex items-baseline justify-between">
          <span className="font-mono text-[10px] font-medium uppercase tracking-[0.14em] text-black/45">Checkout p99</span>
          <span className="font-mono text-[11px] text-red-600">6.9s</span>
        </div>
        <div className="mt-4 space-y-2">
          <div className="h-2 w-full rounded-full bg-red-500/80" />
          <div className="h-2 w-[22%] rounded-full bg-black/25" />
        </div>
        <div className="mt-3 flex justify-between font-mono text-[11px] text-black/45">
          <span>candidate · lock</span>
          <span>baseline 820ms</span>
        </div>
      </div>
    );
  }
  if (kind === "marketplaces") {
    return (
      <div className="mb-8 overflow-hidden rounded-[10px] bg-white p-5 ring-1 ring-black/8" aria-hidden>
        <div className="grid grid-cols-[1fr_auto_1fr] items-center gap-3">
          <div className="rounded-md bg-[#E4F1EB] px-3 py-4 text-center text-[12px] tracking-extra-tight text-black">Buyers</div>
          <div className="font-mono text-[10px] text-black/40">queue</div>
          <div className="rounded-md bg-[#f4f7f5] px-3 py-4 text-center text-[12px] tracking-extra-tight text-black">Sellers</div>
        </div>
        <div className="mt-3 flex items-center justify-between font-mono text-[11px] text-black/45">
          <span>matching worker</span>
          <span className="text-red-600">webhook blocked</span>
        </div>
      </div>
    );
  }
  return (
    <div className="mb-8 overflow-hidden rounded-[10px] bg-[#151617] p-5 text-white" aria-hidden>
      <div className="flex items-baseline justify-between">
        <span className="font-mono text-[10px] font-medium uppercase tracking-[0.14em] text-white/40">ACCESS EXCLUSIVE</span>
        <span className="font-mono text-[12px] text-red-400">4.8s</span>
      </div>
      <div className="mt-4 h-2 overflow-hidden rounded-full bg-white/10">
        <div className="h-full w-[82%] rounded-full bg-red-500" />
      </div>
      <div className="mt-3 flex justify-between font-mono text-[11px] text-white/40">
        <span>events · 12M rows</span>
        <span>plan: Seq Scan</span>
      </div>
    </div>
  );
}

function JobChip({ kind }: { kind: (typeof JOBS)[number]["chip"] }) {
  if (kind === "platform") {
    return (
      <div className="mb-6 flex h-[88px] items-center justify-between rounded-[10px] bg-white/70 px-4 ring-1 ring-black/8" aria-hidden>
        <span className="text-[13px] tracking-extra-tight text-black">Create Wind Tunnel</span>
        <span className="font-mono text-[11px] text-black/45">ticket: none</span>
      </div>
    );
  }
  if (kind === "migrations") {
    return (
      <div className="mb-6 flex h-[88px] items-end justify-between rounded-[10px] bg-white/70 px-4 py-3 ring-1 ring-black/8" aria-hidden>
        <span className="font-title text-[36px] leading-none tracking-tighter text-black">27.4s</span>
        <span className="pb-1 font-mono text-[11px] text-black/45">lock</span>
      </div>
    );
  }
  if (kind === "gates") {
    return (
      <div className="mb-6 flex h-[88px] items-center gap-2 rounded-[10px] bg-white/70 px-4 ring-1 ring-black/8" aria-hidden>
        <span className="rounded-full bg-[#33bf00]/15 px-2.5 py-1 font-mono text-[10px] font-medium text-[#1f7a3a]">PASS</span>
        <span className="rounded-full bg-amber-500/15 px-2.5 py-1 font-mono text-[10px] font-medium text-amber-800">WARN</span>
        <span className="rounded-full bg-red-500/15 px-2.5 py-1 font-mono text-[10px] font-medium text-red-700">BLOCK</span>
      </div>
    );
  }
  return (
    <div className="mb-6 flex h-[88px] items-center gap-2 rounded-[10px] bg-white/70 px-4 ring-1 ring-black/8" aria-hidden>
      {["00", "15", "30", "45"].map((t, i) => (
        <span
          key={t}
          className={`flex size-10 items-center justify-center rounded-md font-mono text-[11px] ${i === 2 ? "bg-black text-white" : "bg-black/5 text-black/50"}`}
        >
          {t}
        </span>
      ))}
    </div>
  );
}

export function SolutionsHubPage() {
  return (
    <PageShell>
      <PageHero
        path="/solutions"
        eyebrow="Solutions"
        title="The same question, for the teams who feel it first."
        lead="Before a risky change meets production, prove it on a disposable twin. Built for teams who already feel migration anxiety, staging drift, and release incidents."
      />

      <PageSection>
        <div className="font-mono text-[10px] font-medium uppercase tracking-snug text-gray-new-50">Ideal customer</div>
        <ul className="mt-10 grid grid-cols-3 gap-x-16 gap-y-10 max-lg:grid-cols-1">
          {ICP.map((item) => (
            <li key={item.value} className="min-w-0 border-t border-black/12 pt-6">
              <div className="font-title text-[44px] leading-none tracking-tighter text-black max-xl:text-[36px] max-md:text-[32px]">
                {item.value}
              </div>
              <p className="mt-4 max-w-[320px] text-[15px] leading-6 tracking-extra-tight text-gray-new-40">{item.label}</p>
            </li>
          ))}
        </ul>
        <p className="mt-12 text-[14px] tracking-extra-tight text-black/45">
          Cloud-native or containerized · customer-hosted agent · history of migration anxiety · production-shaped traffic
        </p>
      </PageSection>

      <PageSection tone="white">
        <PageHeading title="<strong>Teams.</strong> Postgres, frequent deploys, and a platform engineer who owns reliability." />
        <ul className="mt-14 grid grid-cols-12 gap-5 max-lg:grid-cols-1">
          {TEAMS.map((item) => (
            <li key={item.href} className={item.span}>
              <Link
                href={item.href}
                className="group flex h-full flex-col rounded-[12px] bg-[#f7f7f5] p-8 ring-1 ring-black/10 transition-colors hover:bg-white hover:ring-black/25 max-md:p-6"
              >
                <TeamChip kind={item.chip} />
                <div className="mt-auto">
                  <div className="text-[26px] leading-tight tracking-extra-tight text-black max-md:text-[22px]">{item.title}</div>
                  <p className="mt-3 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">{item.body}</p>
                  <div className="mt-6 flex items-center justify-between">
                    <span className="font-mono text-[11px] text-black/40">{item.metric}</span>
                    <span className="text-[13px] text-black/60 transition-colors group-hover:text-black">Open →</span>
                  </div>
                </div>
              </Link>
            </li>
          ))}
        </ul>
      </PageSection>

      <PageSection tone="sage">
        <PageHeading title="<strong>Jobs.</strong> Isolated environments, migration evidence, release policy." />
        <ul className="mt-12 grid grid-cols-4 gap-5 max-xl:grid-cols-2 max-md:grid-cols-1">
          {JOBS.map((item) => (
            <li key={item.href}>
              <Link
                href={item.href}
                className="group flex h-full flex-col rounded-[12px] bg-[#d7ebe2] p-6 ring-1 ring-black/8 transition-colors hover:bg-white hover:ring-black/20"
              >
                <JobChip kind={item.chip} />
                <div className="text-[18px] tracking-extra-tight text-black">{item.title}</div>
                <p className="mt-2 flex-1 text-[14px] leading-6 tracking-extra-tight text-black/55">{item.body}</p>
                <span className="mt-5 text-[13px] text-black/50 transition-colors group-hover:text-black">Open →</span>
              </Link>
            </li>
          ))}
        </ul>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/design-partners", title: "Design partners", description: "One real upcoming migration, not a generic demo." },
          { href: "/product/migrations", title: "Migration Safety", description: "The failure mode these teams feel first." },
          { href: "/product", title: "Product", description: "How a twin run actually decides." },
        ]}
      />
    </PageShell>
  );
}
