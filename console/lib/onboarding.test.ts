import { readFileSync } from "node:fs";
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  BENEFITS,
  CONTACT,
  EMPTY_SETUP,
  LIMIT_LABELS,
  STEPS,
  STEP_TITLES,
  TIERS,
  TIER_NAMES,
  canContinue,
  choiceFor,
  connectionSummary,
  effectiveTier,
  formatLimit,
  isPaidPlan,
  isTier,
  nextStep,
  previousStep,
  primaryPath,
  readSetup,
  selectedUses,
  setupKey,
  tierAvailability,
  tierFor,
  toggleDone,
  toggleUse,
  writeSetup,
  type BillingRead,
} from "./onboarding.ts";
import { readChoice, storageKey } from "./start.ts";
import type { RepositorySetup } from "./setup.ts";

// Every number and every sentence below is checked against the place it came
// from, named beside it, so that a change there is a red test here rather than
// a step that quietly shows a limit the control plane no longer applies.

const repo = (path: string) => readFileSync(new URL(`../../${path}`, import.meta.url), "utf8");

/** `name: { a: 1, b: 2 }` out of a TypeScript source, as numbers. */
function numbersFor(source: string, plan: string): Record<string, number> {
  const match = source.match(new RegExp(`\\b${plan}:\\s*\\{([^}]*)\\}`));
  assert.ok(match, `${plan} is defined in the source`);
  const out: Record<string, number> = {};
  for (const pair of match[1]!.split(",")) {
    const [key, value] = pair.split(":").map((s) => s.trim());
    if (key && value) out[key] = Number(value.replace(/_/g, ""));
  }
  return out;
}

/* ------------------------------------------------------------------------
 * Step one
 * --------------------------------------------------------------------- */

test("the remembered word is the first ticked use in PATHS order: ci over terminal over hosted", () => {
  assert.equal(primaryPath(["hosted", "terminal", "ci"]), "ci");
  assert.equal(primaryPath(["hosted", "terminal"]), "terminal");
  assert.equal(primaryPath(["hosted"]), "hosted");
  assert.equal(primaryPath([]), null);
});

test("leaving with nothing ticked is the dismissal, and with something ticked is that path", () => {
  assert.equal(choiceFor([]), "skipped");
  assert.equal(choiceFor(["terminal", "hosted"]), "terminal");
  // What is written is a word start.ts reads back as itself, so the root and
  // Settings keep working on the value this page stores.
  assert.equal(readChoice(choiceFor(["hosted"])), "hosted");
  assert.equal(readChoice(choiceFor([])), "skipped");
});

test("ticking adds in PATHS order and ticking again removes", () => {
  assert.deepEqual(toggleUse([], "hosted"), ["hosted"]);
  assert.deepEqual(toggleUse(["hosted"], "ci"), ["ci", "hosted"]);
  assert.deepEqual(toggleUse(["ci", "hosted"], "hosted"), ["ci"]);
});

test("step one needs at least one use and no other step needs anything", () => {
  assert.equal(canContinue("uses", []), false);
  assert.equal(canContinue("uses", ["ci"]), true);
  assert.equal(canContinue("tier", []), true);
  assert.equal(canContinue("setup", []), true);
  assert.equal(canContinue("benefits", []), true);
});

/* ------------------------------------------------------------------------
 * Step two
 * --------------------------------------------------------------------- */

test("there are exactly three tiers, in the order the plan page lists them, and one card each", () => {
  assert.deepEqual([...TIER_NAMES], ["free", "team", "enterprise"]);
  assert.deepEqual(
    TIERS.map((tier) => tier.name),
    ["free", "team", "enterprise"],
  );
  assert.deepEqual([isTier("team"), isTier("Team"), isTier("growth"), isTier(null)], [true, false, false, false]);
});

test("every tier's quota is the control plane's own, from PLAN_QUOTAS in limits.ts", () => {
  const source = repo("web/apps/api/src/limits.ts");
  for (const tier of TIERS) {
    const quota = numbersFor(source, tier.name);
    assert.equal(tier.limits.environments, quota.environments, `${tier.name} environments`);
    assert.equal(tier.limits.goldens, quota.goldens, `${tier.name} goldens`);
    assert.equal(tier.limits.artifactGigabytes, quota.artifactGigabytes, `${tier.name} artifact storage`);
  }
});

test("every tier's longest run is the control plane's own, from PLAN_COST_CAPS in costs.ts", () => {
  const source = repo("web/apps/api/src/costs.ts");
  for (const tier of TIERS) {
    assert.equal(tier.limits.perRunHours, numbersFor(source, tier.name).perRunHours, `${tier.name} per run hours`);
  }
});

test("every tier's seats and retention are the control plane's own, from ENTITLEMENTS in entitlements.ts", () => {
  const source = repo("web/apps/api/src/entitlements.ts");
  const byPlan = (key: string) => {
    const block = source.slice(source.indexOf(`  ${key}: {`));
    const match = block.match(/byPlan:\s*\{([^}]*)\}/);
    assert.ok(match, `${key} has a byPlan map`);
    return Object.fromEntries(
      match[1]!.split(",").map((pair) => {
        const [k, v] = pair.split(":").map((s) => s.trim());
        return [k, Number(v)];
      }),
    ) as Record<string, number>;
  };
  const seats = byPlan("seats");
  const retention = byPlan("retentionDays");
  for (const tier of TIERS) {
    assert.equal(tier.limits.seats, seats[tier.name], `${tier.name} seats`);
    assert.equal(tier.limits.retentionDays, retention[tier.name], `${tier.name} retention`);
  }
});

test("a limit is written with its unit, and a bare count without one", () => {
  assert.equal(formatLimit(tierFor("team"), "artifactGigabytes"), "50 GB");
  assert.equal(formatLimit(tierFor("enterprise"), "environments"), "500");
  assert.equal(formatLimit(tierFor("enterprise"), "seats"), "1,000");
  assert.equal(formatLimit(tierFor("free"), "retentionDays"), "30 days");
  assert.equal(formatLimit(tierFor("team"), "perRunHours"), "168 hours");
  // Six limits and no price, because the pricing page marks its bands
  // illustrative and Stripe holds the real one.
  assert.equal(LIMIT_LABELS.length, 6);
  assert.ok(!TIERS.some((tier) => /\$/.test(tier.detail)));
});

test("free and team are the plans the control plane sells for money, and free never has a price", () => {
  // billing/plans.ts: PAID_PLANS = ['team', 'enterprise'], and free is what an
  // organization has when no subscription is live.
  assert.deepEqual([isPaidPlan("free"), isPaidPlan("team"), isPaidPlan("enterprise"), isPaidPlan(null)], [
    false,
    true,
    true,
    false,
  ]);
});

const ready = (facts: { configured: boolean; plans?: string[]; arrangedPlans?: string[] }): BillingRead => ({
  status: "ready",
  facts: { configured: facts.configured, plans: facts.plans ?? [], arrangedPlans: facts.arrangedPlans ?? [] },
});
const owner = { currentPlan: "free", role: "owner", mayBill: true };
const hosted = ready({ configured: true, plans: ["team"], arrangedPlans: ["enterprise"] });

test("free is always selectable and ends on the environments list", () => {
  for (const billing of [hosted, ready({ configured: false }), { status: "error" } as BillingRead]) {
    const a = tierAvailability("free", { ...owner, billing });
    assert.equal(a.selectable, true);
    assert.equal(a.ends, "environments");
  }
  const member = tierAvailability("free", { currentPlan: "free", role: "member", mayBill: false, billing: { status: "not-asked" } });
  assert.equal(member.selectable, true);
});

test("a paid tier needs billing.manage, and says whose job it is when the reader lacks it", () => {
  const a = tierAvailability("team", { currentPlan: "free", role: "member", mayBill: false, billing: { status: "not-asked" } });
  assert.equal(a.selectable, false);
  assert.ok(a.note.includes("owner"));
  assert.ok(a.note.includes("member"));
  assert.equal(a.ends, "environments");
});

test("a paid tier waits while billing is being read and is not selectable on a failed read", () => {
  const loading = tierAvailability("team", { ...owner, billing: { status: "loading" } });
  assert.equal(loading.selectable, false);
  const failed = tierAvailability("team", { ...owner, billing: { status: "error" } });
  assert.equal(failed.selectable, false);
  assert.ok(failed.note.includes("free plan"));
});

test("billing off is said as billing off, and the walk continues on free", () => {
  const a = tierAvailability("team", { ...owner, billing: ready({ configured: false }) });
  assert.equal(a.selectable, false);
  assert.ok(a.note.includes("takes no payment"));
  assert.equal(a.ends, "environments");
  assert.equal(effectiveTier("team", { ...owner, billing: ready({ configured: false }) }), "free");
});

test("a plan with a price ends at checkout, a plan without one ends at the contact page, and one this plane does not know is not offered", () => {
  // routers/subscriptions.ts current: `plans` are the paid plans with a Stripe
  // price, `arrangedPlans` the paid plans without one.
  const team = tierAvailability("team", { ...owner, billing: hosted });
  assert.deepEqual([team.selectable, team.ends], [true, "checkout"]);
  assert.ok(team.note.includes("Nothing is charged"));
  const enterprise = tierAvailability("enterprise", { ...owner, billing: hosted });
  assert.deepEqual([enterprise.selectable, enterprise.ends], [true, "contact"]);
  const unknown = tierAvailability("enterprise", { ...owner, billing: ready({ configured: true, plans: ["team"] }) });
  assert.deepEqual([unknown.selectable, unknown.ends], [false, "environments"]);
});

test("an organization already on a paid plan keeps it, and every other tier is a Plan page matter", () => {
  const context = { currentPlan: "team", role: "owner", mayBill: true, billing: hosted };
  const team = tierAvailability("team", context);
  assert.deepEqual([team.selectable, team.ends], [true, "environments"]);
  assert.ok(team.note.includes("already on this plan"));
  assert.equal(tierAvailability("enterprise", context).selectable, false);
  assert.equal(tierAvailability("free", context).selectable, false);
  // So nothing here can start a second subscription, and whatever this
  // browser remembered from before the purchase, the plan shown checked is
  // the one the organization holds.
  assert.equal(effectiveTier("enterprise", context), "team");
  assert.equal(effectiveTier("free", context), "team");
  assert.equal(effectiveTier("team", context), "team");
});

test("a remembered tier that is no longer selectable falls back to free rather than to a refused checkout", () => {
  assert.equal(effectiveTier("team", { ...owner, billing: hosted }), "team");
  assert.equal(effectiveTier("team", { currentPlan: "free", role: "viewer", mayBill: false, billing: { status: "not-asked" } }), "free");
});

test("the contact address is the one the control plane's own checkout refusal names, at the enterprise form", () => {
  // routers/subscriptions.ts checkout: PRECONDITION_FAILED names
  // https://antifailure.dev/contact. www/components/pages/company/Contact.tsx
  // puts the enterprise form under id="enterprise".
  const source = repo("web/apps/api/src/routers/subscriptions.ts");
  const named = source.match(/https:\/\/antifailure\.dev\/contact/);
  assert.ok(named, "the refusal names the contact address");
  assert.ok(CONTACT.startsWith(named![0]));
  assert.ok(repo("www/components/pages/company/Contact.tsx").includes('id="enterprise"'));
  assert.ok(CONTACT.endsWith("#enterprise"));
});

/* ------------------------------------------------------------------------
 * Step three
 * --------------------------------------------------------------------- */

const row = (state: string, id = state): RepositorySetup => ({
  id,
  repository: `acme/${id}`,
  state,
  attempts: 1,
  branch: null,
  pull_request_number: null,
  pull_request_url: null,
  last_error: null,
  requested_at: "2026-09-01T00:00:00Z",
  finished_at: null,
});

test("nothing connected is null, so the card can say so in words rather than as zero", () => {
  assert.equal(connectionSummary(0, []), null);
  assert.equal(connectionSummary(0, [row("opened")]), null);
});

test("the connection line counts repositories and groups the App's states by what to do about them", () => {
  // The state words are the ones describeSetup in setup.ts reads.
  assert.equal(connectionSummary(1, []), "Installed on 1 repository");
  assert.equal(
    connectionSummary(4, [row("opened", "a"), row("present", "b"), row("queued", "c"), row("failed", "d")]),
    "Installed on 4 repositories, pull request open on 1, workflow merged on 1, opening a pull request on 1, 1 needs attention on the environments page",
  );
  assert.equal(
    connectionSummary(2, [row("needs_permission", "a"), row("leased", "b")]),
    "Installed on 2 repositories, opening a pull request on 1, 1 needs attention on the environments page",
  );
});

test("the connection line's state words are the ones setup.ts knows", () => {
  const source = repo("console/lib/setup.ts");
  for (const word of ["opened", "present", "queued", "leased", "needs_permission", "failed"]) {
    assert.ok(source.includes(`case "${word}"`), `setup.ts reads ${word}`);
  }
});

test("marking done adds in PATHS order and marking again undoes", () => {
  assert.deepEqual(toggleDone([], "hosted"), ["hosted"]);
  assert.deepEqual(toggleDone(["hosted"], "ci"), ["ci", "hosted"]);
  assert.deepEqual(toggleDone(["ci", "hosted"], "ci"), ["hosted"]);
});

/* ------------------------------------------------------------------------
 * Step four
 * --------------------------------------------------------------------- */

test("four benefits, each read from the documentation it describes", () => {
  assert.equal(BENEFITS.length, 4);
  const hostedDoc = repo("docs/src/content/docs/getting-started/hosted.md");
  const selfHosting = repo("docs/src/content/docs/self-hosting/control-plane.md");
  // hosted.md: "environments that outlive a CI job, a reviewer who can open
  // one, scheduling across a queue, quotas, and history."
  assert.ok(hostedDoc.includes("outlive a CI job"));
  assert.ok(hostedDoc.includes("a reviewer who"));
  assert.ok(hostedDoc.includes("scheduling across a queue, quotas, and history"));
  // hosted.md, "Nothing breaks without it": events are buffered and delivered
  // when it returns; teardown reads the local journal.
  assert.ok(hostedDoc.includes("Events are buffered"));
  assert.ok(hostedDoc.includes("teardown reads the local journal"));
  // self-hosting/control-plane.md, "The audit log": append only, enforced by
  // the grants, hash chained.
  assert.ok(selfHosting.includes("Append only, enforced by the grants"));
  assert.ok(selfHosting.includes("hash chained"));
  const detail = BENEFITS.map((b) => b.detail).join(" ");
  for (const claim of ["stays after the CI job", "reviewer", "queue", "buffered", "local journal", "hash chained", "Append only"]) {
    assert.ok(detail.includes(claim), `the benefits say ${claim}`);
  }
});

test("no benefit carries a number or a buzzword", () => {
  for (const benefit of BENEFITS) {
    const text = `${benefit.title} ${benefit.detail}`;
    assert.ok(!/\d/.test(text), `${benefit.key} has no number`);
    assert.ok(!/seamless|empower|unlock|effortless|supercharge|elevate/i.test(text), `${benefit.key} has no buzzword`);
    assert.ok(!text.includes("!"), `${benefit.key} has no exclamation`);
  }
});

/* ------------------------------------------------------------------------
 * The steps and the storage
 * --------------------------------------------------------------------- */

test("four steps, in order, each with a title, and Continue and Back walk them", () => {
  assert.deepEqual([...STEPS], ["uses", "tier", "setup", "benefits"]);
  for (const step of STEPS) assert.ok(STEP_TITLES[step].length > 0);
  assert.equal(nextStep("uses"), "tier");
  assert.equal(nextStep("setup"), "benefits");
  assert.equal(nextStep("benefits"), null);
  assert.equal(previousStep("uses"), null);
  assert.equal(previousStep("tier"), "uses");
});

test("the setup key names the organization and is not the word's key", () => {
  assert.notEqual(setupKey("org-a"), setupKey("org-b"));
  assert.notEqual(setupKey("org-a"), storageKey("org-a"));
  assert.ok(setupKey("org-a").startsWith("af.start.setup."));
});

test("what was stored reads back, and what is not the expected shape reads as nothing set up", () => {
  const stored = writeSetup({ uses: ["hosted", "ci"], tier: "team", done: ["ci"] });
  assert.deepEqual(readSetup(stored), { uses: ["ci", "hosted"], tier: "team", done: ["ci"] });
  for (const raw of [null, undefined, "", "not json", "null", "[]", "42", '"ci"']) {
    assert.deepEqual(readSetup(raw), EMPTY_SETUP, `${String(raw)} reads as nothing`);
  }
});

test("a use, a tier or a done mark this build does not know is dropped rather than trusted", () => {
  const raw = JSON.stringify({ uses: ["ci", "kubernetes"], tier: "growth", done: ["ci", "terminal", "kubernetes"] });
  // The done list keeps only uses that are also ticked: a card that is not
  // shown cannot be done.
  assert.deepEqual(readSetup(raw), { uses: ["ci"], tier: "free", done: ["ci"] });
  assert.deepEqual(readSetup(JSON.stringify({ uses: "ci" })), EMPTY_SETUP);
});

test("writing keeps only ticked uses in the done list, so the two cannot disagree on disk", () => {
  assert.equal(
    writeSetup({ uses: ["terminal"], tier: "free", done: ["ci", "terminal"] }),
    JSON.stringify({ uses: ["terminal"], tier: "free", done: ["terminal"] }),
  );
  assert.deepEqual(selectedUses({ uses: ["hosted", "ci"], tier: "free", done: [] }), ["ci", "hosted"]);
});

test("the shell gives /start the whole window, the way the sign-in screen has it", () => {
  // The browser check proves the layout. This keeps the bare branch in Shell
  // when it is edited later, so the rail cannot quietly come back around the
  // cover pane.
  const shell = repo("console/components/Shell.tsx");
  assert.ok(shell.includes('pathname === "/start"'));
  const page = repo("console/app/(app)/start/page.tsx");
  assert.ok(page.includes("auth-honeycomb"), "the page renders the cover pane");
  assert.ok(page.includes("Know what happens before you deploy."), "with the sign-in screen's sentence");
});
