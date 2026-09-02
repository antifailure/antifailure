/**
 * What the free plan actually allows, in the numbers the code enforces.
 *
 * These were enforced and published nowhere. `PLAN_QUOTAS` and
 * `PLAN_COST_CAPS` in the control plane decide whether an organization's next
 * environment is created, and a person deciding whether this product fits
 * their team had no way to read either one without cloning the repository. A
 * limit a customer discovers by hitting it is a support ticket that a sentence
 * would have prevented.
 *
 * So the numbers live here, the pricing page renders them from here, and
 * `web/apps/api/test/plan-facts.test.ts` fails the build if this file and the
 * enforcement stop agreeing. That test is the point of the file. Writing the
 * numbers into JSX would have been shorter and would have drifted the first
 * time somebody changed a plan, which is exactly how the seven false legal
 * claims that `legal-facts.test.ts` exists for were made.
 *
 * WHAT THESE ARE NOT. They are not limits on the engine. The engine is MIT
 * licensed, runs on your machine and in your own continuous integration, and
 * has no quota of any kind: nothing in it counts environments or hours. These
 * are what a CONTROL PLANE enforces for an organization that has no live
 * subscription, and they apply the same way whether that control plane is the
 * hosted one or one you run yourself.
 *
 * WHAT IS DELIBERATELY ABSENT. `PLAN_QUOTAS` also declares `goldens` and
 * `artifactGigabytes` for every plan. Neither is enforced anywhere: both are
 * counted for display and nothing refuses a creation over either number. They
 * are not published here because publishing a limit nothing applies would be
 * the same defect in the other direction.
 */

/** One published number, and the words that make it mean something. */
export interface PlanFact {
  value: number;
  unit: string;
  label: string;
  body: string;
}

export const FREE_PLAN: Record<"environments" | "perRunHours" | "perDayHours", PlanFact> = {
  environments: {
    value: 3,
    unit: "at once",
    label: "Live environments",
    body: "Environments that exist and have not been torn down. The fourth is refused until one of the three is gone, and nothing that is already running is ever taken away.",
  },
  perRunHours: {
    value: 24,
    unit: "environment-hours",
    label: "One run may commit to",
    body: "The lifetime a single dispatch may ask for. A day is the default runtime.ttl, so one environment for one branch is never refused, and a thirty day lifetime asked for in one call is.",
  },
  perDayHours: {
    value: 72,
    unit: "environment-hours",
    label: "Any rolling day may accrue",
    body: "The total across a moving twenty four hours, not a calendar day. It is what stops a workflow that creates an environment per push from running up a month of environment time in an afternoon.",
  },
};
