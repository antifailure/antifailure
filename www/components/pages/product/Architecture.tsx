import { Callout, PageHeading, PageHero, PageSection, PageShell, RelatedGrid, SpecTable, Split, Steps } from "@/components/pages/kit";
import { Illustrative } from "@/components/layout/Illustrative";
import { PAR01, PAR02, PAR03 } from "@/components/pages/figures/product";

/** In force today. Each of these is something a run will refuse to start without. */
const ISOLATION_MINIMUMS = [
  "No production write credentials",
  "No production database route",
  "No default internet route",
  "Separate DNS policy",
  "Separate secrets namespace",
] as const;

/**
 * Designed and not built.
 *
 * This list used to sit beside the one above under the same heading, so a
 * reader had no way to tell which five were enforced. Grepping the engine and
 * the control plane for admission, reaper, budget, workload identity and
 * resource expiry finds no implementation of any of them.
 */
const ISOLATION_PLANNED = [
  "Temporary workload identity",
  "Resource tags with an expiry a controller enforces",
  "Hard per-run cost ceiling",
  "Admission policy that rejects unowned resources",
  "An independent cleanup controller",
] as const;

/**
 * What keeps a run cheap today, and what does not exist yet.
 *
 * The three cost rows at the bottom used to read "Not built" and all three had
 * been built for weeks, in web/apps/api/src/costs.ts, wired into the dispatch
 * router and the costs query. Under-claiming is as false as over-claiming and
 * it is harder to catch, because nobody audits a page for being too modest.
 *
 * They are described in the unit the code actually measures. There is no price
 * list per runtime, per region and per service size in this control plane, so
 * a cap in dollars would be decoration; the cap is arithmetic over created_at
 * and torn_down_at, and the page says so rather than implying a bill.
 */
const COST_ROWS: [string, string][] = [
  ["Subset", "A referential subset instead of a full copy. Built, and off until you name a seed table."],
  ["Cache", "Goldens are branched rather than restored per run. Built."],
  ["BYOC", "The engine runs in your own CI on your own compute. Built."],
  ["Sweep", "af env prune removes environments past a cutoff you pass. Built."],
  ["Per-run cap", "The most environment-hours one creation may commit to, refused before anything is provisioned. Built, on environments the hosted control plane dispatches."],
  ["Daily cap", "A ceiling on what an organization may accrue in any rolling twenty four hours. Reaching it refuses the next creation and destroys nothing. Built, on environments the hosted control plane dispatches."],
  ["Attribution", "Environment-hours per environment, named by repository and branch with a run count, ordered by hours. Built. It does not yet attribute to a pull request or a team."],
  ["Estimate", "A cost computed before anything is provisioned. Not built."],
];

/** Stated by counting the rows rather than by remembering. The sentence beside
 *  this table said four when the answer was four, and stayed at four when
 *  three more landed. */
const COST_ROWS_BUILT = COST_ROWS.filter(([, body]) => !body.includes("Not built")).length;
const COUNT_WORDS = ["no", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine"] as const;

export function ArchitecturePage() {
  return (
    <PageShell>
      <PageHero
        path="/product/architecture"
        eyebrow="Architecture"
        title="Hosted control plane. Customer-hosted data plane."
        lead="Organizations, policy, and reports live in the control plane. Snapshots, secrets, sanitization, provisioning, egress, and cleanup stay in the customer boundary. The agent dials out and authenticates with a bearer token over TLS."
        visual={<PAR01 />}
        framed={false}
      />

      <PageSection>
        <Split visual={<PAR02 />}>
          <PageHeading
            kicker="What lives where"
            title="<strong>The control plane never needs a copy of production data.</strong> Evidence crosses the boundary. Records do not."
          />
        </Split>
        <Illustrative>
          The split is architectural and enforced today: masking and verification run in your data
          plane, and the control plane's ingest takes events rather than records. The control plane
          is deployed at app.antifailure.dev and is invitation only, so the boundary now has a live
          consumer on the far side of it rather than a planned one.
        </Illustrative>
      </PageSection>

      <PageSection tone="panel">
        <Split visual={<PAR03 inForce={ISOLATION_MINIMUMS} planned={ISOLATION_PLANNED} />}>
          <PageHeading title="<strong>Dedicated account, or a strongly isolated network.</strong> When practical, the clone is its own account, subscription, or project." />
          <p className="mt-6 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            No production write credentials. No production database route. No default internet route.
            Isolation is a ship condition, not a later hardening pass. The list beside this one is
            split on purpose: five of these are enforced today and five are design.
          </p>
          <div className="mt-8">
            <Callout label="Fail closed">
              An unverified golden cannot be branched, and an unresolved secret stops the run. Inside
              the twin the network has no route out and DNS is intercepted, so a client that ignores
              its proxy variables has nowhere to send the packet.
            </Callout>
          </div>
        </Split>
      </PageSection>

      <PageSection tone="ruled">
        <PageHeading
          kicker="Cost controls"
          title="<strong>What keeps a run cheap,</strong> and what is still only a plan."
        />
        <p className="mt-6 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          Unlimited free hosted compute is not viable. Today the engine runs on your own compute with
          your own credentials, and the {COUNT_WORDS[COST_ROWS_BUILT]} controls below marked built are
          what hold the cost down. The rest are named here because they are designed, not because they
          are shipping.
        </p>
        <div className="mt-12">
          <SpecTable rows={COST_ROWS} />
        </div>
      </PageSection>

      <PageSection>
        <PageHeading title="<strong>Outbound-only from the agent</strong> where possible, authenticated with a bearer token rather than a certificate." />
        <p className="mt-6 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          The customer agent dials out. There is no inbound hole into the data plane. Raw production
          data stays inside the customer boundary by default. The control plane receives evidence, not
          records.
        </p>
        <div className="mt-14">
          <Steps
            items={[
              { title: "Dial out", body: "The agent initiates. The control plane does not open a path in." },
              { title: "Authenticate", body: "A bearer token in an authorization header. The client refuses any address that is not https." },
              { title: "Evidence", body: "Reports, hashes and the verdict cross. Records do not." },
              { title: "Tear down", body: "The journal is replayed in reverse, and what was removed is counted." },
            ]}
          />
        </div>
        <div className="mt-12">
          <Callout label="The credential, stated plainly">
            It is a bearer token, not a client certificate, so this is ordinary TLS rather than
            mutual TLS. The token is issued by a device authorization grant, kept in the operating
            system keyring, valid for ninety days, and revocable at any time. What is enforced in
            code is the transport: the client refuses to build against any control plane address
            that is not https, other than localhost, so the token is never sent in the clear.
          </Callout>
        </div>
        <div className="mt-12">
          <Callout label="Recoverable">
            Every lifecycle transition is idempotent, and a resource is journaled the moment it
            exists rather than after the run succeeds. A run that dies halfway still leaves a list of
            what it made, which is what af down replays.
          </Callout>
        </div>
      </PageSection>


      <RelatedGrid
        items={[
          { href: "/product/firewall", title: "Firewall", description: "How side effects are contained." },
          { href: "/docs", title: "Docs", description: "How a twin run works." },
          { href: "/docs/concepts/journal", title: "Journal docs", description: "Lifecycle and isolation in full." },
        ]}
      />
    </PageShell>
  );
}
