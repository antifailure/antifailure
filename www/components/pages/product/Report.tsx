import { PageHeading, PageHero, PageSection, PageShell, Split } from "@/components/pages/kit";
import { ReportScene } from "@/components/home/visuals/ReportScene";
import { Illustrative } from "@/components/layout/Illustrative";
import { PRP01, PRP02 } from "@/components/pages/figures/product";
import { MonoLabel, StatusPill } from "@/components/home/visuals/primitives";

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
        <Split
          visual={
            <div className="w-full max-w-[560px]">
              <ReportScene />
              <Illustrative>
                One shaped run, played through. The sections, the order and the headline sentence are the
                ones <code className="font-mono text-[12px] text-black/70">af ci</code> writes. The
                numbers in it were chosen, not measured.
              </Illustrative>
            </div>
          }
        >
          <PageHeading
            kicker="Required check"
            title="<strong>It looks like a GitHub check</strong> because that is the product surface."
          />
        </Split>
      </PageSection>

      <PageSection tone="ruled">
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
        <Split visual={<PRP02 />}>
          <PageHeading title="<strong>Attached to the pull request.</strong> Not a dataset. Not a preview URL." />
          <p className="mt-6 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            "Error rate increased" is insufficient. When an invariant does not hold the comment carries
            the offending rows, because a reader who has to go and run the query has been told there is
            a problem and not what it is. When a workflow fails it carries the steps, the Playwright
            trace and the video.
          </p>
        </Split>
        <div className="mt-12 grid grid-cols-3 gap-x-16 gap-y-12 max-xl:grid-cols-1">
          {GATES.map((gate) => (
            <PRP01 key={gate.pr} {...gate} />
          ))}
        </div>
        <Illustrative label="Example finding">
          An invariant violation in the shape the comment renders one: the statement's name, its
          description, and the rows it returned. The accounts and the identifiers are invented.
        </Illustrative>
      </PageSection>

      <PageSection tone="ruled">
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

      <PageSection tone="panel">
        <Split
          visual={
            <div className="border border-black/12 bg-[#f7f7f5] px-5 py-5">
              <div className="flex items-center gap-2">
                <StatusPill tone="PASS" />
                <StatusPill tone="FAIL" />
                <StatusPill tone="UNVERIFIED" />
              </div>
              <p className="mt-6 text-[16px] leading-7 tracking-extra-tight text-gray-new-40">
                Every workflow and report answers one question: whether this deployment is safe to ship
                against real data, real concurrency, real workers, and the deploy itself. If the product
                becomes a bundle of tools, it has failed.
              </p>
            </div>
          }
        >
          <PageHeading title="<strong>Center the deployment decision.</strong> Environment creation, data, agents, and load are supporting systems." />
        </Split>
      </PageSection>

    </PageShell>
  );
}
