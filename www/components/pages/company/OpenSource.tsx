import {
  Callout,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  Prose,
  RelatedGrid,
  Stage,
} from "@/components/pages/kit";
import { TrustBoundaryScene } from "@/components/home/visuals/TrustBoundaryScene";
import {
  CheckRow,
  Hairline,
  MonoLabel,
  Node,
  Panel,
} from "@/components/home/visuals/primitives";

const COMPARE: { axis: string; open: string; commercial: string }[] = [
  {
    axis: "Execution",
    open: "Customer agent, local CLI, provisioning contracts, Postgres adapter, Docker Compose.",
    commercial: "Hosted control plane, historical analytics, managed updates, large-scale orchestration.",
  },
  {
    axis: "State",
    open: "Sanitization engine and policy format. Masking and subsetting run inside your cloud.",
    commercial: "Customer-managed keys and data-residency controls.",
  },
  {
    axis: "Containment",
    open: "Egress gateway, core provider simulators, deterministic runner, cleanup controller.",
    commercial: "Private connectivity around the same fail-closed model — not a paid allow-list.",
  },
  {
    axis: "Evidence",
    open: "Local report format. Pass, warning, or block from the same safety engine.",
    commercial: "Immutable audit retention, SIEM integrations, evidence exports.",
  },
  {
    axis: "Control",
    open: "Inspectable policy format. Fail closed is the default, not a paid pack.",
    commercial: "SAML/OIDC SSO, SCIM, advanced RBAC, approval workflows, organization-wide policy packs.",
  },
  {
    axis: "Operations",
    open: "Bring-your-own infrastructure and model keys. Community simulators.",
    commercial: "Fleet, multi-account orchestration, cost allocation, premium connectors, certification, and SLAs.",
  },
];

export function OpenSourcePage() {
  return (
    <PageShell>
      <PageHero
        path="/open-source"
        eyebrow="Open source"
        title="The pieces that sit in your boundary should be inspectable."
        lead="The software handles cloud permissions, production-derived state, secrets, networking, and expensive infrastructure. Inspectability matters inside the trust boundary — it does not bypass compliance."
        visual={
          <Stage className="min-h-[320px]">
            <TrustBoundaryScene />
          </Stage>
        }
      />

      <PageSection>
        <PageHeading
          kicker="Open core"
          title="<strong>Open surface versus /ee.</strong> A split of responsibility, not a paywall on safety."
        />
        <div className="mt-14 overflow-hidden rounded-[12px] ring-1 ring-black/10">
          <div className="grid grid-cols-[minmax(7.5rem,0.55fr)_1.15fr_1.15fr] max-lg:hidden">
            <div className="bg-black/[0.02] px-6 py-6" />
            <div className="bg-[#E4F1EB] px-6 py-6">
              <MonoLabel>Open surface</MonoLabel>
              <h3 className="mt-2 text-[22px] leading-tight tracking-tighter text-black">Inspectable</h3>
              <p className="mt-1.5 max-w-[280px] text-[13px] leading-5 tracking-extra-tight text-gray-new-40">
                Agent, adapters, sanitization, egress, runner, cleanup — inside your cloud.
              </p>
            </div>
            <div className="bg-white px-6 py-6">
              <MonoLabel>Commercial /ee</MonoLabel>
              <h3 className="mt-2 text-[22px] leading-tight tracking-tighter text-black">Governed</h3>
              <p className="mt-1.5 max-w-[280px] text-[13px] leading-5 tracking-extra-tight text-gray-new-40">
                Governance, organizational control, evidence, scale, and managed operations.
              </p>
            </div>
          </div>

          <div className="grid grid-cols-2 lg:hidden">
            <div className="bg-[#E4F1EB] px-5 py-5">
              <MonoLabel>Open surface</MonoLabel>
              <h3 className="mt-2 text-[18px] tracking-tighter text-black">Inspectable</h3>
            </div>
            <div className="px-5 py-5">
              <MonoLabel>Commercial /ee</MonoLabel>
              <h3 className="mt-2 text-[18px] tracking-tighter text-black">Governed</h3>
            </div>
          </div>

          {COMPARE.map((row) => (
            <div
              key={row.axis}
              className="grid grid-cols-[minmax(7.5rem,0.55fr)_1.15fr_1.15fr] border-t border-black/10 max-lg:grid-cols-1"
            >
              <div className="flex items-center bg-black/[0.02] px-6 py-5 max-lg:px-5 max-lg:pb-0 max-lg:pt-5">
                <MonoLabel className="text-black/55">{row.axis}</MonoLabel>
              </div>
              <div className="bg-[#E4F1EB]/50 px-6 py-5 max-lg:bg-[#E4F1EB]/35 max-lg:px-5">
                <MonoLabel className="mb-2 hidden max-lg:block">Open</MonoLabel>
                <p className="text-[15px] leading-6 tracking-extra-tight text-black/80">{row.open}</p>
              </div>
              <div className="px-6 py-5 max-lg:px-5 max-lg:pt-0">
                <MonoLabel className="mb-2 hidden max-lg:block">/ee</MonoLabel>
                <p className="text-[15px] leading-6 tracking-extra-tight text-gray-new-40">{row.commercial}</p>
              </div>
            </div>
          ))}
        </div>
      </PageSection>

      <PageSection tone="sage">
        <PageHeading title="<strong>Safety is not an enterprise add-on.</strong> The open product must still be safe." />
        <p className="mt-8 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          Fail-closed egress, sanitization inside the customer boundary, and attested cleanup ship in
          the open surface. Enterprise code adds governance, evidence retention, scale, and managed
          operations — not the only trustworthy security controls.
        </p>
        <div className="mt-12 grid grid-cols-2 gap-x-16 max-lg:grid-cols-1 max-lg:gap-y-8">
          <Panel className="rounded-[12px] p-6">
            <MonoLabel>Same safety floor</MonoLabel>
            <Hairline className="my-4" />
            <div className="flex flex-col gap-2.5">
              <CheckRow ok>fail-closed egress is not a paid pack</CheckRow>
              <CheckRow ok>cleanup controller is inspectable</CheckRow>
              <CheckRow ok>sanitization runs in your cloud</CheckRow>
              <CheckRow ok>local reports still pass, warn, or block</CheckRow>
            </div>
          </Panel>
          <Panel className="rounded-[12px] p-6">
            <MonoLabel>What /ee adds</MonoLabel>
            <Hairline className="my-4" />
            <div className="flex flex-col gap-2">
              <Node label="SSO · SCIM · RBAC" lit />
              <Node label="audit retention · SIEM" />
              <Node label="CMK · residency · private net" />
              <Node label="fleet · SLAs · certified connectors" />
            </div>
          </Panel>
        </div>
        <div className="mt-10">
          <Callout label="Security controls are not paywalled">
            Do not put the only trustworthy security controls behind /ee. Open source does not bypass
            compliance — it makes the data-plane components inspectable.
          </Callout>
        </div>
      </PageSection>

      <PageSection>
        <Prose>
          <p>
            A standard license will be chosen with counsel. MIT maximizes adoption; Apache 2.0 adds an
            explicit patent grant. There is no public repository yet. The hosted control plane can remain
            commercial even when the execution engine is open.
          </p>
        </Prose>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/product/architecture", title: "Architecture", description: "What stays in the customer cloud." },
          { href: "/pricing", title: "Pricing", description: "Community, team cloud, and enterprise." },
          { href: "/docs/contributing/provider-authoring", title: "Provider authoring", description: "Writing a provider against the open interface." },
        ]}
      />
    </PageShell>
  );
}
