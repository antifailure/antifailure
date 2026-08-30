import {
  Callout,
  FeatureGrid,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
  Split,
  Stage,
} from "@/components/pages/kit";
import { TrustBoundaryScene } from "@/components/home/visuals/TrustBoundaryScene";
import { CheckRow, Hairline, MonoLabel, Panel } from "@/components/home/visuals/primitives";
import { cn } from "@/lib/cn";

type MaskRule = "MASK" | "DELETE";
type SubsetFate = "KEEP" | "DROP" | "DELETE";

const RECORDS: {
  id: string;
  fields: { key: string; production: string; twin: string; rule: MaskRule }[];
}[] = [
  {
    id: "u_8f2a",
    fields: [
      { key: "email", production: "ajay@acme.com", twin: "n8w1@c7h0.io", rule: "MASK" },
      { key: "session", production: "tok_live_8f2", twin: "deleted", rule: "DELETE" },
      { key: "api_key", production: "sk_live_51Hq", twin: "deleted", rule: "DELETE" },
    ],
  },
  {
    id: "u_91c0",
    fields: [
      { key: "email", production: "lea@acme.com", twin: "p2f6@d1v5.co", rule: "MASK" },
      { key: "session", production: "sess_9c1a", twin: "deleted", rule: "DELETE" },
      { key: "api_key", production: "sk_live_7Kx2", twin: "deleted", rule: "DELETE" },
    ],
  },
];

const SUBSET_ROWS: { table: string; row: string; parent: string; fate: SubsetFate; reason: string }[] = [
  { table: "users", row: "u_8f2a", parent: "—", fate: "KEEP", reason: "long-tail past_due" },
  { table: "users", row: "u_91c0", parent: "—", fate: "KEEP", reason: "malformed created_at" },
  { table: "users", row: "u_bb12", parent: "—", fate: "DROP", reason: "sampled out" },
  { table: "orders", row: "o_441", parent: "u_8f2a", fate: "KEEP", reason: "parent kept" },
  { table: "orders", row: "o_902", parent: "u_bb12", fate: "DROP", reason: "parent dropped" },
  { table: "sessions", row: "*", parent: "—", fate: "DELETE", reason: "secrets, not masked" },
];

const LIFECYCLE = [
  "discover",
  "snapshot",
  "restore",
  "sanitize",
  "augment",
  "apply_migrations",
  "observe",
  "destroy",
] as const;

const SPEC: [string, string][] = [
  ["Boundary", "Masking runs in the customer cloud. Control plane receives evidence, not records."],
  ["Supabase", "Auth, roles, RLS handled explicitly. Storage objects and Edge Functions excluded unless declared."],
  ["Output", "Not a dataset. The product output is a deployment-safety decision."],
];

const MONO = "var(--font-geist-mono), ui-monospace, SFMono-Regular, monospace";

function RuleChip({ rule }: { rule: MaskRule | SubsetFate }) {
  return (
    <span
      className={cn(
        "inline-flex items-center px-1.5 py-0.5 font-mono text-[10px] tracking-extra-tight uppercase ring-1",
        rule === "MASK" && "text-[#285D49] ring-[#33bf00]/50",
        rule === "KEEP" && "text-[#285D49] ring-[#33bf00]/50",
        rule === "DELETE" && "text-red-700 ring-red-600/50",
        rule === "DROP" && "text-black/40 ring-black/15",
      )}
    >
      {rule}
    </span>
  );
}

function MaskingPanel() {
  return (
    <Panel className="rounded-[12px] bg-white">
      <div className="flex items-center justify-between gap-3 px-4 py-2.5">
        <MonoLabel className="uppercase">public.users</MonoLabel>
        <div className="flex items-center gap-2">
          <MonoLabel className="uppercase">before → after</MonoLabel>
          <MonoLabel className="uppercase text-[#285D49]">unique</MonoLabel>
        </div>
      </div>
      <Hairline />
      <div className="flex min-w-0">
        <div className="min-w-0 flex-1">
          <div className="px-4 py-2">
            <MonoLabel className="uppercase">production</MonoLabel>
          </div>
          <Hairline />
          {RECORDS.map((record, i) => (
            <div key={`prod-${record.id}`}>
              {i > 0 ? <Hairline /> : null}
              <div className="px-4 py-2.5">
                <div className="font-mono text-[11px] tracking-extra-tight text-black/45">{record.id}</div>
                <ul className="mt-1.5 space-y-1">
                  {record.fields.map((field) => (
                    <li key={field.key} className="flex items-baseline justify-between gap-3">
                      <MonoLabel className="shrink-0">{field.key}</MonoLabel>
                      <span
                        className={cn(
                          "truncate font-mono text-[12px] tracking-extra-tight",
                          field.rule === "DELETE" ? "text-red-700" : "text-black",
                        )}
                      >
                        {field.production}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            </div>
          ))}
        </div>
        <Hairline vertical />
        <div className="min-w-0 flex-1 shadow-[inset_2px_0_0_#33bf00]">
          <div className="flex items-center justify-between px-4 py-2">
            <MonoLabel className="uppercase text-[#285D49]">twin</MonoLabel>
            <MonoLabel className="uppercase">sanitized</MonoLabel>
          </div>
          <Hairline />
          {RECORDS.map((record, i) => (
            <div key={`twin-${record.id}`}>
              {i > 0 ? <Hairline /> : null}
              <div className="px-4 py-2.5">
                <div className="font-mono text-[11px] tracking-extra-tight text-black/45">{record.id}</div>
                <ul className="mt-1.5 space-y-1">
                  {record.fields.map((field) => (
                    <li key={field.key} className="flex items-baseline justify-between gap-3">
                      <span
                        className={cn(
                          "truncate font-mono text-[12px] tracking-extra-tight",
                          field.rule === "DELETE" ? "text-black/35" : "text-black",
                        )}
                      >
                        {field.twin}
                      </span>
                      <RuleChip rule={field.rule} />
                    </li>
                  ))}
                </ul>
              </div>
            </div>
          ))}
        </div>
      </div>
      <Hairline />
      <div className="flex items-center justify-between gap-3 px-4 py-2">
        <MonoLabel className="uppercase">notes.body</MonoLabel>
        <MonoLabel>free-text PII</MonoLabel>
      </div>
      <div className="grid grid-cols-2 px-4 pb-3">
        <p className="pr-3 font-mono text-[11px] leading-5 tracking-extra-tight text-black/70">
          email ajay@acme.com if the card fails
        </p>
        <p className="border-l border-black/10 pl-4 font-mono text-[11px] leading-5 tracking-extra-tight text-black/70">
          email [redacted] if the card fails
        </p>
      </div>
      <Hairline />
      <div className="flex flex-wrap gap-x-5 gap-y-1.5 px-4 py-2.5">
        <CheckRow ok>uniqueness preserved</CheckRow>
        <CheckRow ok>tokens deleted, not masked</CheckRow>
        <CheckRow ok>evidence, not records</CheckRow>
      </div>
    </Panel>
  );
}

function DotStrip({ kept, total }: { kept: number; total: number }) {
  return (
    <div className="flex flex-wrap gap-[3px]" aria-hidden>
      {Array.from({ length: total }, (_, i) => (
        <span
          key={i}
          className={cn("size-1.5 rounded-full", i < kept ? "bg-[#33bf00]" : "bg-black/12")}
        />
      ))}
    </div>
  );
}

function SubsetGraph() {
  return (
    <svg viewBox="0 0 360 168" className="h-[168px] w-full" aria-hidden>
      <rect x="118" y="8" width="124" height="36" fill="#f7f7f5" stroke="#111" strokeOpacity="0.45" />
      <text x="180" y="24" textAnchor="middle" fill="#111" fontSize="10" fontFamily={MONO}>
        users
      </text>
      <text x="180" y="38" textAnchor="middle" fill="rgba(0,0,0,0.45)" fontSize="8" fontFamily={MONO}>
        12% kept
      </text>
      <path d="M180 44 V72" stroke="rgba(0,0,0,0.28)" strokeWidth="1" />
      <path d="M72 72 H288" stroke="rgba(0,0,0,0.28)" strokeWidth="1" />
      <path d="M72 72 V92" stroke="rgba(0,0,0,0.28)" strokeWidth="1" />
      <path d="M288 72 V92" stroke="rgba(0,0,0,0.28)" strokeWidth="1" />
      <rect x="8" y="92" width="128" height="36" fill="#f7f7f5" stroke="#111" strokeOpacity="0.45" />
      <text x="72" y="108" textAnchor="middle" fill="#111" fontSize="10" fontFamily={MONO}>
        orders
      </text>
      <text x="72" y="122" textAnchor="middle" fill="rgba(0,0,0,0.45)" fontSize="8" fontFamily={MONO}>
        FK intact
      </text>
      <rect x="224" y="92" width="128" height="36" fill="#f7f7f5" stroke="#111" strokeOpacity="0.45" />
      <text x="288" y="108" textAnchor="middle" fill="#111" fontSize="10" fontFamily={MONO}>
        subscriptions
      </text>
      <text x="288" y="122" textAnchor="middle" fill="rgba(0,0,0,0.45)" fontSize="8" fontFamily={MONO}>
        long-tail retained
      </text>
      <circle cx="64" cy="148" r="3" fill="#33bf00" />
      <circle cx="76" cy="148" r="3" fill="#33bf00" />
      <circle cx="88" cy="148" r="3" fill="none" stroke="rgba(0,0,0,0.22)" />
      <circle cx="100" cy="148" r="3" fill="none" stroke="rgba(0,0,0,0.22)" />
      <circle cx="276" cy="148" r="3" fill="#33bf00" />
      <circle cx="288" cy="148" r="3" fill="#33bf00" />
      <circle cx="300" cy="148" r="3" fill="#33bf00" />
      <circle cx="312" cy="148" r="3" fill="none" stroke="rgba(0,0,0,0.22)" />
      <text x="180" y="164" textAnchor="middle" fill="rgba(0,0,0,0.4)" fontSize="8" fontFamily={MONO}>
        filled kept · open dropped · 0 broken FKs
      </text>
    </svg>
  );
}

function SubsetPanel() {
  return (
    <Panel className="rounded-[12px] bg-white">
      <div className="flex items-center justify-between gap-3 px-4 py-2.5">
        <MonoLabel className="uppercase">referential subset</MonoLabel>
        <MonoLabel className="uppercase text-[#285D49]">12% · joins valid</MonoLabel>
      </div>
      <Hairline />
      <div className="px-4 pt-3 pb-1">
        <SubsetGraph />
      </div>
      <div className="space-y-2 px-4 pb-3">
        <div className="flex items-center justify-between gap-3">
          <MonoLabel>users</MonoLabel>
          <DotStrip kept={3} total={24} />
        </div>
        <div className="flex items-center justify-between gap-3">
          <MonoLabel>orders</MonoLabel>
          <DotStrip kept={4} total={32} />
        </div>
        <div className="flex items-center justify-between gap-3">
          <MonoLabel>subscriptions</MonoLabel>
          <DotStrip kept={3} total={24} />
        </div>
      </div>
      <Hairline />
      <div className="overflow-x-auto">
        <table className="w-full text-left">
          <thead>
            <tr className="border-b border-black/8">
              {["table", "row", "parent", "fate", "reason"].map((col) => (
                <th key={col} className="px-4 py-2 font-mono text-[10px] font-normal tracking-extra-tight text-black/40">
                  {col}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {SUBSET_ROWS.map((row) => (
              <tr key={`${row.table}-${row.row}`} className="border-b border-black/6 last:border-0">
                <td className="px-4 py-2 font-mono text-[11px] tracking-extra-tight text-black/45">{row.table}</td>
                <td className="px-4 py-2 font-mono text-[11px] tracking-extra-tight text-black">{row.row}</td>
                <td className="px-4 py-2 font-mono text-[11px] tracking-extra-tight text-black/45">{row.parent}</td>
                <td className="px-4 py-2">
                  <RuleChip rule={row.fate} />
                </td>
                <td className="px-4 py-2 font-mono text-[11px] tracking-extra-tight text-black/55">{row.reason}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Panel>
  );
}

function LifecyclePanel() {
  return (
    <Panel className="rounded-[12px] bg-white">
      <div className="flex items-center justify-between gap-3 px-5 py-3">
        <MonoLabel className="uppercase">Postgres adapter</MonoLabel>
        <MonoLabel>logical restore · CoW when supported</MonoLabel>
      </div>
      <Hairline />
      <div className="flex flex-wrap items-center gap-x-1.5 gap-y-2 px-5 py-4">
        {LIFECYCLE.map((step, i) => (
          <span key={step} className="flex items-center gap-1.5">
            <span
              className={cn(
                "font-mono text-[12px] tracking-extra-tight",
                step === "sanitize" && "bg-[#33bf00]/10 px-1.5 py-0.5 text-[#285D49] ring-1 ring-[#33bf00]/40",
                step !== "sanitize" && "text-black",
              )}
            >
              {step}
            </span>
            {i < LIFECYCLE.length - 1 ? (
              <span className="font-mono text-[11px] text-black/25" aria-hidden>
                →
              </span>
            ) : null}
          </span>
        ))}
      </div>
      <Hairline />
      <table className="w-full text-left">
        <tbody>
          {SPEC.map(([k, v], i) => (
            <tr key={k} className={cn(i < SPEC.length - 1 && "border-b border-black/8")}>
              <th className="w-[28%] px-5 py-3.5 align-top font-mono text-[11px] font-normal tracking-extra-tight text-black">
                {k}
              </th>
              <td className="px-5 py-3.5 text-[14px] leading-6 tracking-extra-tight text-gray-new-40">{v}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Panel>
  );
}

export function SafeStatePage() {
  return (
    <PageShell>
      <PageHero
        path="/product/safe-state"
        eyebrow="Safe State Engine"
        title="Production-shaped Postgres without production identities."
        lead="Snapshot restore, referentially consistent subsetting, and deterministic masking inside the customer boundary. Tokens, sessions, and secrets are deleted — not disguised. The output is a sanitization evidence report, not a dataset."
        visual={<MaskingPanel />}
      />
      <PageSection>
        <PageHeading title="<strong>Realistic enough to fail for the right reasons.</strong> Toy fixtures miss the row that breaks the constraint." />
        <FeatureGrid
          items={[
            { title: "Snapshot restore", body: "Logical restore for portability, or provider-native copy-on-write branches when supported." },
            { title: "Referential subsets", body: "Keep joins valid. Long-tail and malformed historical state stay in the subset." },
            { title: "Deterministic masking", body: "Format-preserving replacement with uniqueness preserved, inside the customer boundary." },
            { title: "Delete, don’t mask", body: "Tokens, sessions, secrets, and credentials are deleted rather than disguised." },
            { title: "Free-text PII", body: "Scan for emails, cards, phones, and keys that schema rules miss." },
            { title: "Evidence report", body: "Distribution validation, schema-drift handling, and a signed sanitization attestation." },
          ]}
        />
      </PageSection>
      <PageSection tone="white">
        <Split visual={<SubsetPanel />}>
          <PageHeading title="<strong>A 12% subset that still joins.</strong> Dropped parents take their children. Rare rows stay." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Subsetting is the default instead of full copying. The twin keeps referential integrity, long-tail
            billing states, and malformed history — the records that actually break migrations — while
            volume stays bounded.
          </p>
          <div className="mt-8">
            <Callout label="Unverified goldens" tone="block">
              An unverified golden cannot be branched. Sanitization evidence is required before a snapshot
              becomes a reusable golden.
            </Callout>
          </div>
        </Split>
      </PageSection>
      <PageSection>
        <Split reverse visual={<LifecyclePanel />}>
          <PageHeading title="<strong>Postgres first.</strong> Deep enterprise data platforms can be an external provider, not a rebuild." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            The built-in engine covers common Postgres cases: restore, subset, mask, delete credentials,
            validate distribution, then destroy. Matching a dedicated test-data platform’s connector depth
            is not the wedge. The product output is a deployment-safety decision, not a dataset.
          </p>
        </Split>
      </PageSection>
      <PageSection tone="sage">
        <Split
          visual={
            <Stage>
              <TrustBoundaryScene />
            </Stage>
          }
        >
          <PageHeading
            kicker="Customer-hosted masking"
            title="<strong>Masking never leaves your cloud.</strong> The control plane receives evidence, not records."
          />
          <p className="mt-6 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Deterministic masking runs inside the customer-hosted data plane. Raw snapshots, secrets, and
            captured request bodies do not enter the hosted control plane.
          </p>
          <div className="mt-8">
            <Callout label="Customer boundary">
              Production data stays in the customer boundary. What crosses the trust boundary is a
              sanitization attestation — hashes, coverage, and a pass or block — not the rows.
            </Callout>
          </div>
        </Split>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/firewall", title: "Side-Effect Firewall", description: "The twin cannot act on the real world." },
          { href: "/security", title: "Security", description: "Fail closed. Data stays in your boundary." },
          { href: "/product/fidelity", title: "Fidelity Graph", description: "Volume and distribution actually restored." },
        ]}
      />
    </PageShell>
  );
}
