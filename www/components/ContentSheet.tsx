"use client";

import { CopyCli } from "./Pills";

export type SheetId =
  | "pricing"
  | "community"
  | "brief"
  | "security"
  | "privacy"
  | "terms"
  | "platform-terms"
  | "data-boundary";

const SHEETS: Record<
  SheetId,
  { title: string; lead: string; points: string[]; cta?: "cli" | "waitlist" | "migration" }
> = {
  pricing: {
    title: "Design-partner access",
    lead: "There is no public SKU yet. The offer is the brief’s design-partner pilot, not a checkout form.",
    points: [
      "Give us one deployment your team is nervous about. We create an isolated production-shaped test and show what staging missed.",
      "The pilot centers on an actual upcoming migration, not a generic demo.",
      "Open-core later: a free local engine on your infrastructure, then usage for environment minutes when a hosted control plane exists.",
      "Unlimited free hosted compute is not the model. Production data stays in your boundary.",
    ],
    cta: "waitlist",
  },
  community: {
    title: "Open-source surface",
    lead: "The engine is open source and the repository is public. The pieces that touch production data run inside your own boundary.",
    points: [
      "Planned open-source surface: customer agent, local CLI, Postgres adapter, sanitization, egress gateway, simulators, cleanup controller.",
      "The installer is one command and the engine runs entirely on your machine.",
      "The hosted control plane is invitation only. Request access if you want it to connect a repository.",
    ],
    cta: "cli",
  },
  brief: {
    title: "Product brief",
    lead: "A disposable production twin that proves whether a deployment is safe before it ships.",
    points: [
      "Category: pre-production deployment safety. Not AI QA, synthetic-user, staging, or load-testing.",
      "Twin, safe state, side-effect firewall, load, judgment, evidence, cleanup.",
      "Agents drive the workflows the manifest declares, and return a verdict with a trace and a video.",
      "The promise is evidence, not a mathematical guarantee that production cannot fail.",
    ],
    cta: "migration",
  },
  security: {
    title: "Security",
    lead: "Fail closed. Raw snapshots, secrets, and captured request bodies stay inside the customer boundary.",
    points: [
      "Default architecture: customer-hosted data plane. The control plane never sees raw production state.",
      "No default internet route. Mandatory egress gateway, clone-local DNS, fail-closed policies, attempted-effect ledger.",
      "Unknown outbound destinations, unresolved secrets, or missing isolation block the run.",
      "Cleanup is a safety property: every resource journaled as it is made, replayed in reverse, counted afterwards.",
    ],
    cta: "waitlist",
  },
  privacy: {
    title: "Privacy Notice",
    lead: "This marketing site takes a waitlist address. No production data is collected.",
    points: [
      "A waitlist address is sent to a server and stored, so that the sentence next to the form is true. Your browser keeps a copy as a convenience, which clearing site data removes.",
      "When a control plane exists, production-derived state is processed inside the customer boundary by default.",
      "Passwords entered in this mock form are not stored.",
    ],
  },
  terms: {
    title: "Terms of Use",
    lead: "This page is a product demonstration. It does not create an account or a service contract.",
    points: [
      "Pre-production deployment safety is a category, not a guarantee that every production incident is predicted.",
      "The engine is open source under Apache 2.0. The enterprise edition is separately licensed.",
      "Do not treat simulated Stripe, email, or Slack effects on this page as a live integration.",
    ],
  },
  "platform-terms": {
    title: "Platform Terms",
    lead: "When the product ships, the customer-hosted agent, isolation policy, and cleanup proof are the contract surface.",
    points: [
      "A run that cannot contain side effects, resolve secrets, or prove cleanup should block rather than proceed.",
      "What a run could not measure is stated. The platform does not claim a perfect clone of every cloud topology.",
      "Design-partner work is a pilot on a real upcoming migration, not unlimited hosted compute.",
    ],
  },
  "data-boundary": {
    title: "Data boundary",
    lead: "Raw snapshots, secrets, and captured request bodies do not enter a hosted control plane by default.",
    points: [
      "Sanitization and masking execute inside the customer boundary.",
      "Simulators replace Stripe, email, and webhooks so the twin cannot charge cards or message users.",
      "Fail closed on unknown egress. The ledger records attempted effects, including denies.",
    ],
    cta: "migration",
  },
};

export function ContentSheet({
  open,
  id,
  onClose,
}: {
  open: boolean;
  id: SheetId;
  onClose: () => void;
}) {
  if (!open) return null;
  const sheet = SHEETS[id];

  return (
    <div
      className="fixed inset-0 z-[80] flex items-center justify-center bg-black/70 px-4 max-sm:px-3 max-sm:py-6"
      onClick={onClose}
    >
      <div
        className="max-h-[min(88dvh,720px)] w-full max-w-[480px] overflow-y-auto rounded-2xl border border-black/10 bg-white p-6 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="text-[15px] font-medium">{sheet.title}</div>
        <p className="mt-1 text-[13px] leading-5 text-black/50">{sheet.lead}</p>
        <ul className="mt-5 space-y-2.5 text-[13px] leading-5 text-black/75">
          {sheet.points.map((p) => (
            <li key={p} className="border-l border-black/15 pl-3">
              {p}
            </li>
          ))}
        </ul>
        {sheet.cta === "cli" ? (
          <div className="mt-6">
            <CopyCli variant="light" />
          </div>
        ) : null}
        {sheet.cta === "waitlist" ? (
          <a
            href="/signup"
            className="mt-6 inline-flex h-10 w-full items-center justify-center rounded-full bg-black text-[13px] font-medium text-white"
          >
            Request access
          </a>
        ) : null}
        {sheet.cta === "migration" ? (
          <a
            href="/#migration"
            onClick={onClose}
            className="mt-6 inline-flex h-10 w-full items-center justify-center rounded-full bg-black text-[13px] font-medium text-white"
          >
            See the migration demo
          </a>
        ) : null}
        <button
          type="button"
          className="mt-3 w-full text-center text-[12px] text-black/55"
          onClick={onClose}
        >
          Close
        </button>
      </div>
    </div>
  );
}
