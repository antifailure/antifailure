import { PageHeading, PageHero, PageSection, PageShell, RelatedGrid } from "@/components/pages/kit";
import { ReportScene } from "@/components/home/visuals/ReportScene";
import { Illustrative } from "@/components/layout/Illustrative";
import { Hairline, MonoLabel, Panel, StatusPill } from "@/components/home/visuals/primitives";
import { cn } from "@/lib/cn";

type Tone = "PASS" | "FAIL" | "UNVERIFIED";

/**
 * The five verdicts a workflow can return, in the engine's own words.
 *
 * They are the product's vocabulary, not a marketing one: the strings come
 * from runner/src/verdict.ts and the sentences from report.go's Headline. The
 * page used to say "pass, warning, or block", which was two words the engine
 * has never produced and one that means the opposite of what it said here.
 */
const VERDICTS: { verdict: string; tone: Tone; title: string; body: string }[] = [
  {
    verdict: "pass",
    tone: "PASS",
    title: "It did what you said it would",
    body: "The workflow reached the outcome the manifest declared, and every invariant held.",
  },
  {
    verdict: "fail",
    tone: "FAIL",
    title: "The change broke something",
    body: "An expectation was not met, or the application errored. This is the only verdict that exits non-zero.",
  },
  {
    verdict: "flaky",
    tone: "UNVERIFIED",
    title: "It passed only sometimes",
    body: "It passed on some attempts and failed on others. Something is wrong, and it is not reliable enough to call either way.",
  },
  {
    verdict: "blocked",
    tone: "UNVERIFIED",
    title: "It could not be carried through",
    body: "The runner could not drive the application, or the environment owed the workflow something it never got. Never the application's fault, and the check says so.",
  },
  {
    verdict: "unverified",
    tone: "UNVERIFIED",
    title: "It ran without proving anything",
    body: "The workflow touched a response a model invented, so the result cannot be trusted either way.",
  },
];

/** The sections af ci actually writes, in the order it writes them. */
const CONTENTS: { label: string; value: string; tone: Tone; pill?: string }[] = [
  {
    label: "Workflows",
    value: "4 passed, 1 failed, with the trace and the video behind the failure",
    tone: "FAIL",
  },
  {
    label: "Invariants",
    value: "no account has two active subscriptions: 3 rows returned, listed in full",
    tone: "FAIL",
  },
  {
    label: "Reproduction",
    value: "the steps to see the failure yourself, folded under the comment",
    tone: "PASS",
    pill: "PASS",
  },
  {
    label: "Outbound",
    value: "18 allowed, 2 captured, 1 mocked, 1 refused host nothing in the manifest mentions",
    tone: "PASS",
    pill: "PASS",
  },
  {
    label: "Load",
    value: "2,140 requests at 18 a second, p95 412ms on a route production serves in 180ms",
    tone: "PASS",
    pill: "PASS",
  },
  {
    label: "Golden",
    value: "branched from a verified golden, which is the only kind that can be branched",
    tone: "PASS",
    pill: "PASS",
  },
];

const CI_CHECKS: { name: string; time: string }[] = [
  { name: "Lint / typecheck", time: "12s" },
  { name: "Unit tests", time: "1m 04s" },
  { name: "Docker build", time: "2m 11s" },
];

const GATES: {
  tone: Tone;
  pr: string;
  title: string;
  evidence: string;
  merge: string;
}[] = [
  {
    tone: "PASS",
    pr: "pr/182",
    title: "expand-and-contract",
    evidence: "5 workflows passed, 2 invariants held",
    merge: "Merge ready",
  },
  {
    tone: "FAIL",
    pr: "pr/184",
    title: "widen plan_id to bigint",
    evidence: "checkout failed, 3 rows broke an invariant",
    merge: "Check failed, exit 1",
  },
  {
    tone: "UNVERIFIED",
    pr: "pr/185",
    title: "index on access_tier",
    evidence: "the twin never came up, nothing counts against the change",
    merge: "Exit 0, nothing proven",
  },
];

function ToneDot({ tone }: { tone: Tone }) {
  return (
    <span
      className={cn(
        "size-1.5 shrink-0 rounded-full",
        tone === "PASS" && "bg-[#33bf00]",
        tone === "UNVERIFIED" && "bg-black/30",
        tone === "FAIL" && "bg-red-600",
      )}
      aria-hidden
    />
  );
}

function GateCard({ tone, pr, title, evidence, merge }: (typeof GATES)[number]) {
  return (
    <Panel className="rounded-[12px] bg-white">
      <div className="flex items-center justify-between gap-3 px-4 py-2.5">
        <span className="font-mono text-[11px] tracking-extra-tight text-black/60">{pr}</span>
        <StatusPill tone={tone} />
      </div>
      <Hairline />
      <div className="px-4 py-4">
        <div className="font-mono text-[13px] tracking-extra-tight text-black">{title}</div>
        <p className="mt-1.5 font-mono text-[11px] leading-4 tracking-extra-tight text-black/50">{evidence}</p>
      </div>
      <Hairline />
      <div
        className={cn(
          "px-4 py-2.5 font-mono text-[10px] tracking-extra-tight",
          tone === "PASS" && "text-[#285D49]",
          tone === "UNVERIFIED" && "text-black/60",
          tone === "FAIL" && "text-red-700",
        )}
      >
        {merge}
      </div>
    </Panel>
  );
}

function PrCheckChrome() {
  return (
    <Panel className="rounded-[12px] bg-white">
      <div className="flex items-center justify-between gap-4 px-4 py-2.5">
        <div className="flex min-w-0 items-center gap-2.5">
          <span className="inline-flex items-center border border-black/[0.08] px-1.5 py-0.5 font-mono text-[10px] tracking-extra-tight text-black/70">
            pr/184
          </span>
          <span className="truncate font-mono text-[13px] tracking-extra-tight text-black">add access_tier</span>
        </div>
        <div className="flex items-center gap-2">
          <span className="font-mono text-[10px] tracking-extra-tight text-black/55">required · 1 of 4</span>
          <StatusPill tone="FAIL">FAIL</StatusPill>
        </div>
      </div>
      <Hairline />

      <ul>
        {CI_CHECKS.map((check) => (
          <li key={check.name} className="flex items-center justify-between gap-3 px-4 py-2">
            <div className="flex min-w-0 items-center gap-2 font-mono text-[12px] tracking-extra-tight text-black/55">
              <ToneDot tone="PASS" />
              <span className="truncate">{check.name}</span>
            </div>
            <div className="flex items-center gap-2">
              <span className="font-mono text-[10px] tabular-nums tracking-extra-tight text-black/30">{check.time}</span>
              <StatusPill tone="PASS" />
            </div>
          </li>
        ))}
      </ul>

      <Hairline />

      <div className="px-4 py-3">
        <div className="flex items-start justify-between gap-3">
          <div className="flex min-w-0 items-start gap-2">
            <ToneDot tone="FAIL" />
            <div className="min-w-0">
              <div className="font-mono text-[12px] tracking-extra-tight text-black">
                Antifailure / deployment safety
              </div>
              <div className="mt-0.5 font-mono text-[10px] tracking-extra-tight text-black/55">
                env-08f2 · 4m 12s
              </div>
            </div>
          </div>
          <StatusPill tone="FAIL">FAIL</StatusPill>
        </div>

        <p className="mt-4 font-mono text-[13px] tracking-extra-tight text-black">
          1 workflow failed, and 1 invariant did not hold.
        </p>
        <p className="mt-2 max-w-[640px] font-mono text-[11px] leading-5 tracking-extra-tight text-black/65">
          Invariant `one_active_subscription` does not hold. No account has more than one active
          subscription row.
        </p>
        <div className="mt-3 overflow-x-auto">
          <table className="w-full min-w-[380px] text-left font-mono text-[11px] tracking-extra-tight tabular-nums">
            <thead>
              <tr className="text-black/55">
                <th className="py-1 pr-6 font-normal">account_id</th>
                <th className="py-1 pr-6 font-normal">active</th>
                <th className="py-1 font-normal">latest</th>
              </tr>
            </thead>
            <tbody className="text-black/70">
              {[
                ["acct_00418", "2", "sub_9c41"],
                ["acct_02277", "2", "sub_a180"],
                ["acct_09903", "3", "sub_b774"],
              ].map((row) => (
                <tr key={row[0]} className="border-t border-black/[0.06]">
                  <td className="py-1 pr-6">{row[0]}</td>
                  <td className="py-1 pr-6">{row[1]}</td>
                  <td className="py-1">{row[2]}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className="mt-3">
          <div className="font-mono text-[10px] uppercase tracking-[0.14em] text-black/60">
            How to see it yourself
          </div>
          <p className="mt-1 font-mono text-[11px] leading-5 tracking-extra-tight text-black">
            open /settings/billing · click Upgrade · submit · trace.zip · video.webm
          </p>
        </div>
      </div>

      <Hairline />

      <div className="px-4 py-2">
        <div className="font-mono text-[10px] uppercase tracking-[0.14em] text-black/60">In the report</div>
      </div>
      <Hairline />
      {CONTENTS.map((row, i) => (
        <div key={row.label}>
          {i > 0 ? <Hairline /> : null}
          <div className="flex items-center justify-between gap-4 px-4 py-2.5">
            <div className="min-w-0">
              <div className="font-mono text-[12px] tracking-extra-tight text-black">{row.label}</div>
              <div className="mt-0.5 font-mono text-[11px] tracking-extra-tight text-black/60">{row.value}</div>
            </div>
            <StatusPill tone={row.tone}>{row.pill ?? row.tone}</StatusPill>
          </div>
        </div>
      ))}

      <Hairline />
      <div className="flex items-center justify-between gap-3 px-4 py-3">
        <div>
          <div className="font-mono text-[12px] tracking-extra-tight text-black/35">Merge pull request</div>
          <div className="mt-0.5 font-mono text-[10px] tracking-extra-tight text-black/30">
            inert · required check failed
          </div>
        </div>
        <span className="border border-black/[0.08] bg-white px-2.5 py-1 font-mono text-[10px] tracking-extra-tight text-black/30">
          Merge
        </span>
      </div>
    </Panel>
  );
}

export function ReportPage() {
  return (
    <PageShell>
      <PageHero
        path="/product/report"
        eyebrow="Safety Report and Release Gate"
        title="Pass or fail, with evidence."
        lead="The overall decision, every workflow's verdict, the rows behind an invariant that did not hold, the steps and the trace and the video to see a failure yourself, a summary of every outbound attempt including the denials, and latency against the p95 production serves each route in."
      />

      <PageSection>
        <PageHeading
          kicker="Required check"
          title="<strong>It looks like a GitHub check</strong> because that is the product surface."
        />
        <ReportScene />
        <Illustrative>
          One shaped run, played through. The sections, the order and the headline sentence are the
          ones <code className="font-mono text-[12px] text-black/70">af ci</code> writes. The
          numbers in it were chosen, not measured.
        </Illustrative>
      </PageSection>

      <PageSection tone="white">
        <PageHeading
          kicker="Five verdicts"
          title="<strong>A run that could not answer says so</strong> instead of blaming the change."
        />
        <p className="mt-6 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          A gate with two outcomes has to call our own outage your bug. This one does not. Only
          <span className="text-black"> fail</span> exits non-zero, and only fail means the change
          broke something. The other three say, in the comment, exactly which of them happened.
        </p>
        <ul className="mt-14 grid grid-cols-3 gap-x-16 gap-y-12 max-xl:grid-cols-2 max-xl:gap-x-10 max-md:grid-cols-1 max-md:gap-y-8">
          {VERDICTS.map((item) => (
            <li key={item.verdict} className="min-w-0">
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <span className="font-mono text-[13px] tracking-extra-tight text-black">{item.verdict}</span>
                <StatusPill tone={item.tone} />
              </div>
              <h3 className="mt-4 text-[18px] leading-snug tracking-extra-tight text-black">{item.title}</h3>
              <p className="mt-2 max-w-[320px] text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
                {item.body}
              </p>
            </li>
          ))}
        </ul>
      </PageSection>

      <PageSection>
        <PageHeading title="<strong>Attached to the pull request.</strong> Not a dataset. Not a preview URL." />
        <p className="mt-6 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          "Error rate increased" is insufficient. When an invariant does not hold the comment carries
          the offending rows, because a reader who has to go and run the query has been told there is
          a problem and not what it is. When a workflow fails it carries the steps, the Playwright
          trace and the video.
        </p>
        <div className="mt-12 grid grid-cols-3 gap-5 max-xl:grid-cols-1">
          {GATES.map((gate) => (
            <GateCard key={gate.pr} {...gate} />
          ))}
        </div>
        <div className="mt-5">
          <PrCheckChrome />
        </div>
        <Illustrative label="Example finding">
          An invariant violation in the shape the comment renders one: the statement's name, its
          description, and the rows it returned. The accounts and the identifiers are invented.
        </Illustrative>
      </PageSection>

      <PageSection tone="white">
        <PageHeading title="<strong>What the report does not contain</strong> is written down too." />
        <p className="mt-6 max-w-[640px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          A passing run that hides its own gaps is worth less than a failing one. Migration findings
          come from <code className="font-mono text-[15px] text-black">af insights</code>, which is a
          command you run rather than a section of this check. A load threshold produces a listed
          regression here and exits non-zero under{" "}
          <code className="font-mono text-[15px] text-black">af load</code>; the check's verdict comes
          from the workflows and the invariants. A route the traffic source could not measure carries
          no baseline, and the report says &ldquo;no baseline&rdquo; rather than inventing one.
        </p>
        <div className="mt-12 max-w-[720px] border-t border-black/10 pt-8">
          <MonoLabel tone="reader" className="uppercase tracking-[0.14em]">How close the twin got</MonoLabel>
          <p className="mt-5 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
            The report carries an inventory of what this environment reproduced and what it did
            not, dimension by dimension: services, data, third-party hosts, personas, runtime and
            traffic. Every input is something the run already measured. Nothing is estimated, and
            nothing is averaged into a single grade, because an average is how a dimension that is
            genuinely weak disappears behind five that are not.
          </p>
          <p className="mt-4 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
            A component nobody could measure is named and left out of the arithmetic. It is never
            counted as a pass. That is also why the count travels with the percentage everywhere it
            is printed: a score drawn from four measured components and a score drawn from forty
            are not the same claim, and a bare percentage hides which one you are reading. When
            nothing in an environment could be measured there is no score at all, and the report
            says so rather than printing a zero that would read like a failing grade.
          </p>
          <p className="mt-4 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
            Third-party fidelity is the low one today, and the report says it rather than smoothing
            it out. One offline pack ships, and it is Stripe.
          </p>
        </div>

        <div className="mt-12 max-w-[720px] border-t border-black/10 pt-8">
          <MonoLabel tone="reader" className="uppercase tracking-[0.14em]">Thresholds that exist</MonoLabel>
          <ul className="mt-5 space-y-3 text-[15px] leading-6 tracking-extra-tight text-gray-new-40">
            <li>
              <span className="font-mono text-[14px] text-black">p95_increase</span>, default 0.25
              under a trace export. Applied per route, against production&rsquo;s own p95 for that
              route, and never to a route with no baseline. An access log carries no durations, so
              the manifest refuses the threshold there rather than listing one that cannot fire.
            </li>
            <li>
              <span className="font-mono text-[14px] text-black">error_rate</span>, default 0.01.
              Applied to the run as a whole.
            </li>
          </ul>
        </div>
      </PageSection>

      <PageSection tone="sage">
        <PageHeading title="<strong>Center the deployment decision.</strong> Environment creation, data, agents, and load are supporting systems." />
        <div className="mt-8 flex items-center gap-2">
          <StatusPill tone="PASS" />
          <StatusPill tone="FAIL" />
          <StatusPill tone="UNVERIFIED" />
        </div>
        <p className="mt-8 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          Every workflow and report answers one question: whether this deployment is safe to ship
          against real data, real concurrency, real workers, and the deploy itself. If the product
          becomes a bundle of tools, it has failed.
        </p>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/product/load", title: "Load", description: "Where a latency regression is measured." },
          { href: "/product/twins", title: "Isolated Twin", description: "What the run is carried out inside." },
          { href: "/product/migrations", title: "Migration Safety", description: "The lock that a rehearsal finds." },
        ]}
      />
    </PageShell>
  );
}
