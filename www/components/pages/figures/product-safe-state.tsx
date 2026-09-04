import type { ReactNode } from "react";

const DOT_GRID = {
  backgroundImage: "radial-gradient(rgba(40,93,73,0.14) 0.7px, transparent 0.8px)",
  backgroundSize: "12px 12px",
};

function CheckMark({ className = "size-3.5" }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="none" className={className} aria-hidden="true">
      <circle cx="8" cy="8" r="6.5" fill="#E4F1EB" stroke="#5D9B80" />
      <path d="m4.8 8.1 2 2.1 4.5-4.6" stroke="#285D49" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function CrossMark({ className = "size-3.5" }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="none" className={className} aria-hidden="true">
      <circle cx="8" cy="8" r="6.5" fill="#FAEEEE" stroke="#D59A9A" />
      <path d="m5.6 5.6 4.8 4.8m0-4.8-4.8 4.8" stroke="#A63333" strokeWidth="1.35" strokeLinecap="round" />
    </svg>
  );
}

function DatabaseMark({ className = "size-4" }: { className?: string }) {
  return (
    <svg viewBox="0 0 18 18" fill="none" className={className} aria-hidden="true">
      <ellipse cx="9" cy="4" rx="6.2" ry="2.4" stroke="currentColor" strokeWidth="1.2" />
      <path d="M2.8 4v5c0 1.3 2.8 2.4 6.2 2.4s6.2-1.1 6.2-2.4V4M2.8 9v5c0 1.3 2.8 2.4 6.2 2.4s6.2-1.1 6.2-2.4V9" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}

function Arrow({ direction = "right" }: { direction?: "right" | "down" }) {
  return (
    <svg
      viewBox="0 0 28 28"
      fill="none"
      className={direction === "right" ? "size-7" : "size-6 rotate-90"}
      aria-hidden="true"
    >
      <path d="M3 14h20m-5-5 5 5-5 5" stroke="currentColor" strokeWidth="1.35" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function FigureShell({
  id,
  tab,
  rail,
  caption,
  children,
}: {
  id: string;
  tab: string;
  rail: string;
  caption: string;
  children: ReactNode;
}) {
  const captionId = `${id.toLowerCase()}-caption`;

  return (
    <figure
      className="relative w-full self-start overflow-hidden rounded-[28px] bg-sage p-3.5 sm:rounded-[32px] sm:p-5"
      style={DOT_GRID}
      aria-labelledby={captionId}
    >
      <div className="w-full overflow-hidden rounded-[15px] border border-black/[0.06] bg-white shadow-[0_22px_54px_rgba(0,0,0,0.10),0_2px_7px_rgba(0,0,0,0.04)]">
        <div className="flex items-end justify-between gap-2 border-b border-black/[0.07] bg-[#F7F7F5] px-2.5 pt-2.5 sm:px-3">
          <div className="min-w-0 rounded-t-[8px] bg-sage-2 px-2.5 py-1.5 text-[11px] font-medium text-[#285D49] sm:px-3 sm:py-2 sm:text-[12px]">
            <span className="block max-w-[190px] truncate sm:max-w-[270px]">{tab}</span>
          </div>
          <div className="mb-1.5 flex shrink-0 items-baseline gap-2">
            <span className="border-b-2 border-neon pb-0.5 font-mono text-[9px] font-medium uppercase tracking-[0.12em] text-black sm:text-[10px]">
              {rail}
            </span>
            <span className="hidden font-mono text-[9px] uppercase tracking-[0.12em] text-black/35 sm:inline">
              FIG. {id}
            </span>
          </div>
        </div>
        <div className="p-3 sm:p-4">{children}</div>
      </div>
      <figcaption id={captionId} className="sr-only">
        {caption}
      </figcaption>
    </figure>
  );
}

function SectionKicker({ children }: { children: ReactNode }) {
  return (
    <div className="font-mono text-[9px] font-medium uppercase tracking-[0.13em] text-black/40 sm:text-[10px]">
      {children}
    </div>
  );
}

type EvidenceTone = "pass" | "delete" | "neutral";

function EvidenceLine({ label, value, tone = "neutral" }: { label: string; value: string; tone?: EvidenceTone }) {
  return (
    <div
      className={`flex min-w-0 items-center justify-between gap-3 border-t px-0 py-2 first:border-0 ${
        tone === "delete" ? "border-[#E4B9B9]" : "border-black/[0.07]"
      }`}
    >
      <span className="min-w-0 text-[11px] font-medium tracking-extra-tight text-black/75 sm:text-[12px]">{label}</span>
      <span className={`shrink-0 font-mono text-[10px] ${tone === "delete" ? "text-[#A63333]" : tone === "pass" ? "text-[#285D49]" : "text-black/45"}`}>
        {value}
      </span>
    </div>
  );
}

const sanitizedFields = [
  { label: "Structured identifiers", value: "masked", tone: "pass" as const },
  { label: "Free-text fields", value: "scanned", tone: "pass" as const },
  { label: "Live credentials", value: "deleted", tone: "delete" as const },
];

export function PSS01() {
  return (
    <FigureShell
      id="P-SS-01"
      tab="safe-state / public.users"
      rail="SANITIZE"
      caption="A production snapshot passes through deterministic masking and credential deletion inside the customer boundary, producing a verified sanitization attestation."
    >
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-[10px] bg-[#F1F8F4] px-3 py-2.5 text-[#285D49]">
        <div className="flex min-w-0 items-center gap-2 text-[#285D49]">
          <CheckMark />
          <span className="truncate text-[12px] font-medium tracking-extra-tight sm:text-[13px]">Sanitized branch is safe to share internally</span>
        </div>
        <span className="font-mono text-[10px] uppercase tracking-[0.1em]">verified</span>
      </div>

      <div className="mt-3 grid min-w-0 items-stretch gap-2.5 sm:grid-cols-[minmax(0,0.78fr)_34px_minmax(0,1.22fr)] sm:gap-2.5">
        <section className="min-w-0 rounded-[11px] bg-[#F7F7F5] p-3" aria-label="Production-shaped input">
          <div className="flex items-center gap-2 text-black/70">
            <DatabaseMark />
            <SectionKicker>Snapshot input</SectionKicker>
          </div>
          <div className="mt-3 text-[19px] font-medium leading-6 tracking-extra-tight text-black sm:text-[22px]">
            Production shape, unsafe values.
          </div>
          <div className="mt-3 grid gap-1.5 font-mono text-[10px] text-black/45">
            <span>emails and names present</span>
            <span>secrets still live</span>
            <span>comments may contain PII</span>
          </div>
        </section>

        <div className="flex items-center justify-center text-[#285D49]">
          <span className="sm:hidden"><Arrow direction="down" /></span>
          <span className="hidden sm:inline"><Arrow /></span>
        </div>

        <section className="min-w-0 rounded-[11px] border border-[#B8D8C9] bg-white p-3" aria-label="Applied sanitization policies">
          <div className="flex items-center justify-between gap-3">
            <div>
              <SectionKicker>Sanitization contract</SectionKicker>
              <div className="mt-1 text-[13px] font-medium tracking-extra-tight text-black">Deterministic masking with free-text scanning</div>
            </div>
            <CheckMark className="size-5" />
          </div>
          <div className="mt-3">
            {sanitizedFields.map((row) => (
              <EvidenceLine key={row.label} label={row.label} value={row.value} tone={row.tone} />
            ))}
          </div>
        </section>
      </div>

      <div className="mt-3 grid grid-cols-3 overflow-hidden rounded-[10px] bg-[#F7F7F5]">
        {[
          ["joins", "remain valid"],
          ["secrets", "removed"],
          ["audit", "attested"],
        ].map(([label, value], index) => (
          <div key={label} className={`min-w-0 px-2 py-2.5 sm:px-3 ${index ? "border-l border-black/[0.07]" : ""}`}>
            <div className="font-mono text-[8px] uppercase tracking-[0.1em] text-black/35 sm:text-[9px]">{label}</div>
            <div className="mt-1 truncate text-[10px] font-medium tracking-extra-tight text-black sm:text-[11px]">{value}</div>
          </div>
        ))}
      </div>
    </FigureShell>
  );
}

type RelationRow = {
  id: string;
  detail: string;
  state: "KEEP" | "DROP";
};

function RelationColumn({ title, rows }: { title: string; rows: RelationRow[] }) {
  return (
    <section className="min-w-0 rounded-[11px] bg-white p-3" aria-label={`${title} subset rows`}>
      <div className="font-mono text-[10px] font-medium text-black">{title}</div>
      <div className="mt-2 space-y-1.5">
        {rows.map((row) => (
          <div
            key={row.id}
            className={`flex min-w-0 items-center justify-between gap-2 rounded-[8px] px-2.5 py-2 ${
              row.state === "KEEP" ? "bg-[#F1F8F4] text-[#285D49]" : "bg-[#F0F0ED] text-black/38"
            }`}
          >
            <div className="min-w-0">
              <div className="truncate font-mono text-[10px]">{row.id}</div>
              <div className="mt-0.5 truncate text-[9px] tracking-extra-tight opacity-70">{row.detail}</div>
            </div>
            <span className="shrink-0 font-mono text-[9px] uppercase tracking-[0.08em]">{row.state}</span>
          </div>
        ))}
      </div>
    </section>
  );
}

export function PSS02() {
  return (
    <FigureShell
      id="P-SS-02"
      tab="subset / referential closure"
      rail="KEEP"
      caption="A twelve-percent seed sample expands by foreign-key closure, deliberately keeps a rare billing state, drops children with dropped parents, and deletes live sessions."
    >
      <div className="rounded-[11px] bg-[#F7F7F5] p-3">
        <div className="grid gap-3 sm:grid-cols-[0.88fr_1.12fr] sm:items-end">
          <div>
            <SectionKicker>Seed budget</SectionKicker>
            <div className="mt-1 text-[21px] font-medium leading-6 tracking-extra-tight text-black">12% seed grows only where relationships require it.</div>
          </div>
          <div className="font-mono text-[10px] leading-5 text-black/48 sm:text-right">
            foreign-key closure keeps parents and children consistent; rare billing states stay represented.
          </div>
        </div>
        <div className="mt-3 flex h-2 overflow-hidden rounded-full bg-black/[0.07]" aria-label="Illustrative twelve-percent seed sample">
          <span className="h-full w-[12%] min-w-3 rounded-full bg-[#285D49]" />
          <span className="ml-1 h-full w-5 rounded-full bg-[#9FCAB6]" title="Rows added by referential closure" />
        </div>
        <div className="mt-2 flex items-center gap-4 font-mono text-[8px] uppercase tracking-[0.1em] text-black/35">
          <span className="flex items-center gap-1.5"><span className="size-1.5 rounded-full bg-[#285D49]" /> seed</span>
          <span className="flex items-center gap-1.5"><span className="size-1.5 rounded-full bg-[#9FCAB6]" /> closure</span>
        </div>
      </div>

      <div className="mt-3 grid min-w-0 items-center gap-2.5 rounded-[11px] border border-black/[0.08] bg-[#F7F7F5] p-2.5 sm:grid-cols-[minmax(0,1fr)_60px_minmax(0,1fr)]">
        <RelationColumn
          title="public.users"
          rows={[
            { id: "u_8f2a", detail: "selected seed", state: "KEEP" },
            { id: "u_bb12", detail: "sampled out", state: "DROP" },
          ]}
        />
        <div className="flex flex-col items-center justify-center text-[#285D49]">
          <span className="font-mono text-[8px] uppercase tracking-[0.1em] text-black/35">user_id</span>
          <span className="sm:hidden"><Arrow direction="down" /></span>
          <span className="hidden sm:inline"><Arrow /></span>
          <span className="font-mono text-[8px] uppercase tracking-[0.1em] text-[#285D49]">closure</span>
        </div>
        <RelationColumn
          title="public.orders"
          rows={[
            { id: "o_441", detail: "parent u_8f2a", state: "KEEP" },
            { id: "o_902", detail: "parent u_bb12", state: "DROP" },
          ]}
        />
      </div>

      <div className="mt-3 grid gap-2 sm:grid-cols-2">
        <div className="flex min-w-0 items-center justify-between gap-3 rounded-[10px] bg-[#F1F8F4] px-3 py-2.5">
          <div className="min-w-0">
            <div className="truncate font-mono text-[10px] text-black/75">billing_state · lapsed</div>
            <div className="mt-0.5 text-[9px] text-black/45">rare-state preservation</div>
          </div>
          <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-[#285D49]">keep</span>
        </div>
        <div className="flex min-w-0 items-center justify-between gap-3 rounded-[10px] bg-[#FAEEEE] px-3 py-2.5">
          <div className="min-w-0">
            <div className="truncate font-mono text-[10px] text-black/75">session · live</div>
            <div className="mt-0.5 text-[9px] text-black/45">credential policy</div>
          </div>
          <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-[#A63333]">delete</span>
        </div>
      </div>

      <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-black/[0.08] pt-3">
        <span className="flex items-center gap-2 text-[11px] tracking-extra-tight text-black/65"><CheckMark /> Foreign-key closure verified</span>
        <span className="font-mono text-[9px] uppercase tracking-[0.1em] text-[#285D49]">bounded volume</span>
      </div>
    </FigureShell>
  );
}

const postgresStages = [
  { index: "01", name: "Restore", detail: "logical / native COW" },
  { index: "02", name: "Subset", detail: "FK closure · optional" },
  { index: "03", name: "Sanitize", detail: "mask + delete" },
  { index: "04", name: "Validate", detail: "shape + schema drift" },
  { index: "05", name: "Destroy", detail: "journaled teardown" },
];

export function PSS03() {
  return (
    <FigureShell
      id="P-SS-03"
      tab="adapter / postgres"
      rail="PIPELINE"
      caption="The built-in Postgres adapter restores, optionally subsets, sanitizes, validates, and destroys a branch. External data platforms can implement the same verified snapshot contract."
    >
      <div className="flex flex-wrap items-center justify-between gap-2 rounded-[10px] bg-black px-3 py-2.5 text-white">
        <div className="flex items-center gap-2">
          <DatabaseMark className="size-4 text-white/75" />
          <span className="font-mono text-[10px] font-medium">postgres adapter</span>
        </div>
        <span className="font-mono text-[9px] uppercase tracking-[0.1em] text-white/50">customer-hosted execution</span>
      </div>

      <ol className="relative mt-3 grid gap-2 rounded-[11px] bg-[#F7F7F5] p-2 sm:grid-cols-5 sm:gap-0" aria-label="Safe State Postgres processing stages">
        {postgresStages.map((stage, index) => (
          <li
            key={stage.index}
            className={`relative min-w-0 rounded-[10px] px-3 py-3 sm:rounded-none sm:px-2.5 sm:first:rounded-l-[10px] sm:last:rounded-r-[10px] ${index === 2 ? "bg-[#F1F8F4] text-[#285D49]" : "bg-white text-black"}`}
          >
            <div className="flex items-center justify-between gap-2 sm:block">
              <span className="font-mono text-[9px] text-black/35">{stage.index}</span>
              {index < postgresStages.length - 1 ? (
                <svg viewBox="0 0 12 12" className="size-3 text-black/25 sm:absolute sm:-right-1.5 sm:top-4 sm:z-10" fill="none" aria-hidden="true">
                  <circle cx="6" cy="6" r="5.5" fill="white" stroke="currentColor" />
                  <path d="M4 6h4m-1.5-1.5L8 6 6.5 7.5" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" />
                </svg>
              ) : null}
            </div>
            <div className="mt-1 text-[11px] font-medium leading-4 tracking-extra-tight text-black">{stage.name}</div>
            <div className="mt-1 text-[9px] leading-3.5 tracking-extra-tight text-black/45">{stage.detail}</div>
          </li>
        ))}
      </ol>

      <div className="mt-3 rounded-[11px] bg-[#F7F7F5] p-3">
        <div className="grid items-stretch gap-2 sm:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)]">
          <div className="rounded-[9px] bg-white p-3">
            <SectionKicker>Built in</SectionKicker>
            <div className="mt-1 text-[11px] font-medium tracking-extra-tight text-black">Common Postgres paths</div>
            <div className="mt-2 font-mono text-[10px] leading-5 text-black/45">logical restore<br />provider-native branch</div>
          </div>
          <div className="flex items-center justify-center gap-1 text-[#285D49] sm:flex-col">
            <span className="font-mono text-[8px] uppercase tracking-[0.1em]">same contract</span>
            <svg viewBox="0 0 30 10" className="h-3 w-8 rotate-90 sm:rotate-0" fill="none" aria-hidden="true">
              <path d="M1 5h27m-4-4 4 4-4 4" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          </div>
          <div className="rounded-[9px] border border-dashed border-black/20 bg-white p-3">
            <SectionKicker>Pluggable</SectionKicker>
            <div className="mt-1 text-[11px] font-medium tracking-extra-tight text-black">External data provider</div>
            <div className="mt-2 font-mono text-[10px] leading-5 text-black/45">same evidence contract<br />provider-owned depth</div>
          </div>
        </div>
        <div className="mt-2.5 flex flex-wrap items-center justify-between gap-2 rounded-[8px] bg-[#E4F1EB] px-3 py-2">
          <span className="flex items-center gap-2 text-[11px] font-medium tracking-extra-tight text-[#285D49]"><CheckMark /> verified safe snapshot</span>
          <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-[#285D49]">audit evidence, not copied data</span>
        </div>
      </div>
    </FigureShell>
  );
}

function BoundaryItem({ children, denied = false }: { children: ReactNode; denied?: boolean }) {
  return (
    <div className="flex min-w-0 items-center gap-2 rounded-[8px] bg-white px-2.5 py-2">
      {denied ? <CrossMark /> : <CheckMark />}
      <span className="min-w-0 text-[10px] leading-4 tracking-extra-tight text-black/65">{children}</span>
    </div>
  );
}

export function PSS04() {
  return (
    <FigureShell
      id="P-SS-04"
      tab="trust boundary / sanitization"
      rail="ATTEST"
      caption="Raw snapshots, secrets, and request bodies stay inside the customer-hosted data plane. Only a sanitization attestation containing hashes, coverage, and verification status crosses to the hosted control plane."
    >
      <div className="grid min-w-0 overflow-hidden rounded-[11px] bg-white sm:grid-cols-[1.18fr_0.82fr]">
        <section className="min-w-0 bg-[#F1F8F4] p-3" aria-label="Customer-hosted data plane">
          <div className="flex items-center justify-between gap-2">
            <SectionKicker>Customer cloud</SectionKicker>
            <span className="font-mono text-[10px] uppercase tracking-[0.1em] text-[#285D49]">data plane</span>
          </div>
          <div className="mt-3 grid grid-cols-[32px_minmax(0,1fr)] gap-x-2.5 gap-y-0">
            {[
              ["01", "Raw snapshot", "restored locally"],
              ["02", "Masking worker", "rules execute locally"],
              ["03", "Evidence builder", "signs attestation"],
            ].map(([index, title, detail], rowIndex) => (
              <div key={index} className="contents">
                <div className="relative flex justify-center">
                  <span className="relative z-10 flex size-6 items-center justify-center rounded-full border border-[#9FCAB6] bg-white font-mono text-[8px] text-[#285D49]">{index}</span>
                  {rowIndex < 2 ? <span className="absolute top-6 bottom-0 w-px bg-[#9FCAB6]" aria-hidden="true" /> : null}
                </div>
                <div className={`min-w-0 pb-3 ${rowIndex === 2 ? "pb-0" : ""}`}>
                  <div className="text-[11px] font-medium tracking-extra-tight text-black">{title}</div>
                  <div className="mt-0.5 font-mono text-[9px] text-black/45">{detail}</div>
                </div>
              </div>
            ))}
          </div>
        </section>

        <section className="min-w-0 border-t border-dashed border-black/20 bg-[#F7F7F5] p-3 sm:border-t-0 sm:border-l" aria-label="Hosted control plane">
          <div className="flex items-center justify-between gap-2">
            <SectionKicker>Hosted</SectionKicker>
            <span className="font-mono text-[10px] uppercase tracking-[0.1em] text-black/45">control plane</span>
          </div>
          <div className="mt-3 rounded-[9px] bg-white p-3">
            <div className="font-mono text-[10px] font-medium uppercase tracking-[0.1em] text-black/45">Receives attestation</div>
            <dl className="mt-2">
              {[
                ["ruleset", "hash"],
                ["coverage", "summary"],
                ["status", "verified"],
              ].map(([term, value]) => (
                <div key={term} className="flex items-center justify-between gap-2 border-t border-black/[0.06] py-2 first:border-0">
                  <dt className="font-mono text-[10px] text-black/40">{term}</dt>
                  <dd className="text-[11px] font-medium text-black/70">{value}</dd>
                </div>
              ))}
            </dl>
          </div>
          <div className="mt-2 flex items-center gap-2 rounded-[8px] bg-black px-2.5 py-2 text-white">
            <CheckMark />
            <span className="font-mono text-[9px] uppercase tracking-[0.09em] text-white/75">decision evidence only</span>
          </div>
        </section>
      </div>

      <div className="mt-3 rounded-[10px] bg-[#E4F1EB] p-2.5">
        <div className="flex min-w-0 items-center gap-2 text-[#285D49]">
          <span className="shrink-0 font-mono text-[9px] font-medium uppercase tracking-[0.1em]">Outbound contract</span>
          <span className="h-px min-w-3 flex-1 bg-[#5D9B80]" aria-hidden="true" />
          <svg viewBox="0 0 18 10" className="h-3 w-5 shrink-0" fill="none" aria-hidden="true">
            <path d="M1 5h15m-4-4 4 4-4 4" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
          <span className="min-w-0 text-right font-mono text-[10px] font-medium uppercase tracking-[0.08em]">audit-ready attestation</span>
        </div>
      </div>

      <div className="mt-2 grid gap-2 sm:grid-cols-3" aria-label="Data prohibited from crossing the customer boundary">
        <BoundaryItem denied>raw snapshots stay local</BoundaryItem>
        <BoundaryItem denied>secrets stay local</BoundaryItem>
        <BoundaryItem denied>request bodies stay local</BoundaryItem>
      </div>
    </FigureShell>
  );
}
