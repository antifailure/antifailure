// The four steps after the first sign-in, and every rule they follow.
//
// The one question page in start.ts asked which of three people you were and
// showed that one's next step. It was right about the content and wrong about
// the shape: somebody who uses pull request checks AND a terminal had to pick
// one, the plan was a page they would find later or never, and nothing told
// them what the control plane was for once they had connected something. This
// is the same content, the same commands and the same sources, arranged as a
// short walk: where you will use it, which plan you start on, the setup for
// each place you named, and what the control plane gives you once it is there.
//
// Everything here is CONTENT and RULES, kept out of the component so it is
// testable without a renderer. The page decides how it looks. This decides
// what it says, what may be chosen, and where it ends.
//
// WHAT IS REMEMBERED, and where. Two keys in this browser, per organization.
// `af.start.<orgId>` keeps the single word start.ts has always kept, so the
// root's routing, the Settings card and an older console all keep reading the
// same value. `af.start.setup.<orgId>` keeps the rest as JSON: the uses that
// were ticked, the tier, and which setup cards were marked done. Both are read
// defensively: a word or a shape this build does not know is the person not
// having answered, never a crash and never a silent skip.

import { PATHS, isPath, type Choice, type Path } from "./start.ts";
import type { RepositorySetup } from "./setup.ts";

/* -------------------------------------------------------------------------
 * Step one: where
 * ---------------------------------------------------------------------- */

/**
 * The one word start.ts remembers, chosen from several ticked uses.
 *
 * Pull request checks first, because a repository connected there is what
 * every other path lists and dispatches through; then the terminal; then the
 * agent, which reads what the other two report. The order is PATHS, so the two
 * lists cannot disagree.
 */
export function primaryPath(uses: readonly Path[]): Path | null {
  for (const path of PATHS) if (uses.includes(path)) return path;
  return null;
}

/* -------------------------------------------------------------------------
 * Step two: the tier
 * ---------------------------------------------------------------------- */

export const TIER_NAMES = ["free", "team", "enterprise"] as const;
export type TierName = (typeof TIER_NAMES)[number];

export function isTier(value: unknown): value is TierName {
  return typeof value === "string" && (TIER_NAMES as readonly string[]).includes(value);
}

/**
 * The limits a plan enforces, as the control plane enforces them.
 *
 * A COPY of PLAN_QUOTAS in web/apps/api/src/limits.ts, PLAN_COST_CAPS in
 * costs.ts and the seats and retentionDays entries of ENTITLEMENTS in
 * entitlements.ts, for the same reason roles.ts copies ROLE_PERMISSIONS: the
 * console is a separate build with its own lockfile. The test beside this file
 * reads those three sources and fails when any number here drifts, so the tier
 * step cannot quietly show a limit the control plane no longer applies.
 *
 * No price. The pricing page's bands are marked illustrative and Stripe holds
 * the real one, so the number a person sees is the one on the checkout page.
 */
export interface Tier {
  name: TierName;
  label: string;
  /** Two or three words of metadata beside the name: how the plan is come by.
   *  Quiet on purpose, and never a price, for the reason above. */
  tag: string;
  detail: string;
  limits: {
    environments: number;
    goldens: number;
    artifactGigabytes: number;
    seats: number;
    retentionDays: number;
    perRunHours: number;
  };
}

export const TIERS: readonly Tier[] = [
  {
    name: "free",
    label: "Free",
    tag: "No card",
    detail: "What every organization starts on. No card, and the limits are enforced from the first environment.",
    limits: { environments: 3, goldens: 2, artifactGigabytes: 1, seats: 5, retentionDays: 30, perRunHours: 24 },
  },
  {
    name: "team",
    label: "Team",
    tag: "Bought after setup",
    detail: "A flat fee per organization, not per person. Bought here through Stripe, after setup.",
    limits: { environments: 25, goldens: 10, artifactGigabytes: 50, seats: 50, retentionDays: 90, perRunHours: 168 },
  },
  {
    name: "enterprise",
    label: "Enterprise",
    tag: "Arranged with a person",
    detail: "Agreed with a person, because the scope, the term and the price are set for each organization.",
    limits: {
      environments: 500,
      goldens: 100,
      artifactGigabytes: 1000,
      seats: 1000,
      retentionDays: 365,
      perRunHours: 720,
    },
  },
];

export function tierFor(name: TierName): Tier {
  return TIERS.find((tier) => tier.name === name)!;
}

/** A limit as a person reads it, with its unit. The same words as the Plan
 *  page's limits table, so the two screens describe one thing one way. */
export const LIMIT_LABELS: readonly { key: keyof Tier["limits"]; label: string; unit: string }[] = [
  { key: "environments", label: "Live environments", unit: "" },
  { key: "goldens", label: "Golden snapshots", unit: "" },
  { key: "artifactGigabytes", label: "Artifact storage", unit: "GB" },
  { key: "seats", label: "Seats", unit: "" },
  { key: "retentionDays", label: "History kept", unit: "days" },
  { key: "perRunHours", label: "Longest single run", unit: "hours" },
];

export function formatLimit(tier: Tier, key: keyof Tier["limits"]): string {
  const entry = LIMIT_LABELS.find((item) => item.key === key)!;
  const n = tier.limits[key].toLocaleString("en-US");
  return entry.unit ? `${n} ${entry.unit}` : n;
}

/**
 * What subscriptions.current says about this control plane, reduced to the
 * three facts the tier step needs. `plans` are the paid plans it can sell,
 * meaning a Stripe price exists; `arrangedPlans` are the paid plans it knows
 * and does not sell, which is Enterprise on the hosted plane.
 */
export interface BillingFacts {
  configured: boolean;
  plans: readonly string[];
  arrangedPlans: readonly string[];
}

/** The billing read, in the states the page can be in while it is asked. */
export type BillingRead =
  | { status: "loading" }
  | { status: "error" }
  | { status: "ready"; facts: BillingFacts }
  /** Not asked, because this person may not: subscriptions.current answers
   *  only under billing.manage, and asking anyway would be a refusal shown
   *  to three of the four roles. */
  | { status: "not-asked" };

export interface TierAvailability {
  /** Whether the radio may be chosen at all. */
  selectable: boolean;
  /** What happens if it is, or why it cannot be, in one sentence. */
  note: string;
  /** How the walk ends when this tier is the one chosen. */
  ends: "environments" | "checkout" | "contact";
}

/** Whether a plan name is one somebody paid for, by the control plane's own
 *  rule: free is what an organization has when no subscription is live. */
export function isPaidPlan(plan: string | null | undefined): boolean {
  return plan === "team" || plan === "enterprise";
}

/**
 * Whether one tier may be chosen, by whom, and where choosing it leads.
 *
 * The order of the checks is the order in which each fact overrides the next.
 * An organization already on a paid plan changes it through Stripe's portal
 * on the Plan page, so nothing here may start a second subscription. Free is
 * always available because it is what the organization already holds. A paid
 * plan needs billing.manage, which only an owner has, and a control plane that
 * takes payment, and a price for that plan; each missing fact is said in the
 * words a person can act on rather than as a disabled control with no reason.
 */
export function tierAvailability(
  tier: TierName,
  context: { currentPlan: string | null | undefined; role: string | null | undefined; mayBill: boolean; billing: BillingRead },
): TierAvailability {
  const { currentPlan, role, mayBill, billing } = context;

  if (isPaidPlan(currentPlan)) {
    return tier === currentPlan
      ? {
          selectable: true,
          note: "Your organization is already on this plan. Changes go through Stripe's portal on the Plan page.",
          ends: "environments",
        }
      : { selectable: false, note: "Plan changes go through the Plan page.", ends: "environments" };
  }

  if (tier === "free") {
    return {
      selectable: true,
      note: "Nothing to pay and nothing to enter. Move up later from the Plan page.",
      ends: "environments",
    };
  }

  if (!mayBill) {
    return {
      selectable: false,
      note: `Subscribing is an owner's to do, and your role is ${role ?? "unknown"}. Ask an owner to open the Plan page.`,
      ends: "environments",
    };
  }

  switch (billing.status) {
    case "not-asked":
    case "loading":
      return { selectable: false, note: "Checking whether this control plane takes payment.", ends: "environments" };
    case "error":
      return {
        selectable: false,
        note: "Could not read whether this control plane takes payment. You can continue on the free plan and choose again from the Plan page.",
        ends: "environments",
      };
    case "ready": {
      const { facts } = billing;
      if (!facts.configured) {
        return {
          selectable: false,
          note: "This control plane takes no payment, so it runs on the free plan and whoever operates it sets plans by hand.",
          ends: "environments",
        };
      }
      if (facts.plans.includes(tier)) {
        return {
          selectable: true,
          note: "The price and the card are on Stripe's checkout page, which opens after setup. Nothing is charged before then.",
          ends: "checkout",
        };
      }
      if (facts.arrangedPlans.includes(tier)) {
        return {
          selectable: true,
          note: "Not bought here. The last step links the contact page and a person arranges it for this organization.",
          ends: "contact",
        };
      }
      return { selectable: false, note: "Not offered on this control plane.", ends: "environments" };
    }
  }
}

/**
 * The tier the walk actually proceeds with. A remembered tier that is no
 * longer selectable, because billing was switched off since or a different
 * person is reading, falls back to free rather than to a checkout that would
 * be refused.
 */
export function effectiveTier(
  chosen: TierName,
  context: Parameters<typeof tierAvailability>[1],
): TierName {
  // An organization already on a paid plan is on that plan whatever this
  // browser remembers: the remembered word came from before the purchase, or
  // from somebody else's walk, and the only tier the step may show checked is
  // the one the control plane says the organization holds.
  if (isTier(context.currentPlan) && isPaidPlan(context.currentPlan)) return context.currentPlan;
  return tierAvailability(chosen, context).selectable ? chosen : "free";
}

/** Where the enterprise plan is arranged. The same address the control plane's
 *  own checkout refusal names, at the form on that page. */
export const CONTACT = "https://antifailure.dev/contact#enterprise";

/* -------------------------------------------------------------------------
 * Step three: setup
 * ---------------------------------------------------------------------- */

/**
 * "Installed on 2 repositories, pull request open on 1", from what the
 * control plane reports. Null when nothing is connected yet, so the card can
 * say that in a sentence of its own rather than as a count of zero.
 *
 * The state words are the ones describeSetup in setup.ts reads, and they are
 * grouped by what the person should do about them: an opened pull request
 * wants merging, a present workflow wants nothing, queued and leased want a
 * moment, and the rest want attention on the environments page.
 */
export function connectionSummary(repositories: number, rows: readonly RepositorySetup[]): string | null {
  if (repositories <= 0) return null;
  const count = (state: (s: string) => boolean) => rows.filter((row) => state(row.state)).length;
  const opened = count((s) => s === "opened");
  const present = count((s) => s === "present");
  const opening = count((s) => s === "queued" || s === "leased");
  const stuck = count((s) => s === "needs_permission" || s === "failed");
  const parts = [`Installed on ${plural(repositories, "repository", "repositories")}`];
  if (opened > 0) parts.push(`pull request open on ${opened}`);
  if (present > 0) parts.push(`workflow merged on ${present}`);
  if (opening > 0) parts.push(`opening a pull request on ${opening}`);
  if (stuck > 0) parts.push(`${stuck} need${stuck === 1 ? "s" : ""} attention on the environments page`);
  return parts.join(", ");
}

function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

/** "1 of 3 done", the line over the setup cards. A count rather than a
 *  percentage, because three cards is a number a person can hold. */
export function doneLine(done: number, total: number): string {
  return `${done} of ${total} done`;
}

/* -------------------------------------------------------------------------
 * Step four: what the control plane gives you
 * ---------------------------------------------------------------------- */

/**
 * Four things, each one sentence, each read from the documentation named
 * beside it in the test. Nothing here is a number and nothing is a promise
 * about the future.
 */
export interface Benefit {
  key: "outlives" | "history" | "degrades" | "audit";
  title: string;
  detail: string;
}

export const BENEFITS: readonly Benefit[] = [
  {
    key: "outlives",
    title: "Environments that outlive the job",
    detail:
      "An environment the engine reports stays after the CI job that built it has ended, and a reviewer can open it.",
  },
  {
    key: "history",
    title: "History, a queue and quotas",
    detail:
      "Every environment and run the engine reports is listed here, scheduled across a queue and counted against the plan.",
  },
  {
    key: "degrades",
    title: "Nothing breaks without it",
    detail:
      "If it cannot be reached, environments keep running, events are buffered until it returns, and teardown reads the local journal.",
  },
  {
    key: "audit",
    title: "An audit log nobody can edit",
    detail:
      "Append only, enforced by database grants rather than by code, and hash chained so a removed entry leaves a break anybody can detect.",
  },
];

/* -------------------------------------------------------------------------
 * The steps, and what is remembered between visits
 * ---------------------------------------------------------------------- */

export const STEPS = ["uses", "tier", "setup", "benefits"] as const;
export type Step = (typeof STEPS)[number];

export const STEP_TITLES: Record<Step, string> = {
  uses: "Where will you use Antifailure?",
  tier: "Start on",
  setup: "Setting up Antifailure for you",
  benefits: "What the control plane gives you",
};

/** One word per step, under the progress bars, so the four bars say where
 *  they lead rather than only how many are left. */
export const STEP_LABELS: Record<Step, string> = {
  uses: "Where",
  tier: "Plan",
  setup: "Setup",
  benefits: "What you get",
};

/**
 * What the cover pane says at each step.
 *
 * The cover was the same on all four steps: the sign-in screen's sentence,
 * byte for byte, which is right on arrival and wallpaper by the third step.
 * It carries the step now. The first headline is still the sign-in
 * sentence, because a person has just come from the pane that says it, and
 * each one after is a claim about that step that the step then has to keep.
 * Nothing here is a number and nothing is a promise about the future.
 */
export interface CoverCopy {
  headline: string;
  line: string;
}

export const COVER: Record<Step, CoverCopy> = {
  uses: {
    headline: "Know what happens before you deploy.",
    line: "Start with where the checks will run. Everything after this is shaped by that answer.",
  },
  tier: {
    headline: "Read the limits before you meet them.",
    line: "Every number on this step is the one the control plane enforces. Free is already yours.",
  },
  setup: {
    headline: "Connected in the time it takes to read this.",
    line: "One card for each place you named, with the exact command and what it may do.",
  },
  benefits: {
    headline: "The engine runs without it. This is what changes when it reports here.",
    line: "Environments that outlive the job, a history, and an audit log nobody can edit.",
  },
};

/** Step one needs at least one use. Nothing else blocks. */
export function canContinue(step: Step, uses: readonly Path[]): boolean {
  return step !== "uses" || uses.length > 0;
}

export function nextStep(step: Step): Step | null {
  const i = STEPS.indexOf(step);
  return i >= 0 && i < STEPS.length - 1 ? STEPS[i + 1]! : null;
}

export function previousStep(step: Step): Step | null {
  const i = STEPS.indexOf(step);
  return i > 0 ? STEPS[i - 1]! : null;
}

export interface Setup {
  uses: Path[];
  tier: TierName;
  done: Path[];
}

export const EMPTY_SETUP: Setup = { uses: [], tier: "free", done: [] };

export function setupKey(orgId: string): string {
  return `af.start.setup.${orgId}`;
}

/**
 * What was stored, read back defensively.
 *
 * Anything that is not an object with the expected shape reads as nothing
 * having been set up. Inside it, each field is filtered rather than trusted:
 * a use this build does not know is dropped, an unknown tier becomes free, and
 * the done list keeps only uses that are also ticked, since a card that is not
 * shown cannot be done.
 */
export function readSetup(raw: string | null | undefined): Setup {
  if (!raw) return EMPTY_SETUP;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return EMPTY_SETUP;
  }
  if (typeof parsed !== "object" || parsed === null) return EMPTY_SETUP;
  const record = parsed as Record<string, unknown>;
  const uses = PATHS.filter((path) => Array.isArray(record.uses) && record.uses.includes(path));
  const tier = isTier(record.tier) ? record.tier : "free";
  const done = uses.filter((path) => Array.isArray(record.done) && record.done.includes(path));
  return { uses, tier, done };
}

export function writeSetup(setup: Setup): string {
  return JSON.stringify({
    uses: PATHS.filter((path) => setup.uses.includes(path)),
    tier: setup.tier,
    done: PATHS.filter((path) => setup.uses.includes(path) && setup.done.includes(path)),
  });
}

/** Ticking or unticking one use, kept in PATHS order. */
export function toggleUse(uses: readonly Path[], path: Path): Path[] {
  const next = uses.includes(path) ? uses.filter((p) => p !== path) : [...uses, path];
  return PATHS.filter((p) => next.includes(p));
}

/** Marking one setup card done, or not done again. */
export function toggleDone(done: readonly Path[], path: Path): Path[] {
  const next = done.includes(path) ? done.filter((p) => p !== path) : [...done, path];
  return PATHS.filter((p) => next.includes(p));
}

/** The uses whose cards step three shows, in the order they are always shown. */
export function selectedUses(setup: Setup): Path[] {
  return PATHS.filter((path) => setup.uses.includes(path));
}

/** The remembered single word, from the ticked uses: the primary path, or
 *  the dismissal when nothing was ticked and the person left. */
export function choiceFor(uses: readonly Path[]): Choice {
  return primaryPath(uses) ?? "skipped";
}

export { isPath };
