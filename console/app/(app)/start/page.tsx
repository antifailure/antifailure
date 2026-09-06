"use client";

import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import { useSessionContext } from "@/components/session";
import { mutate, query, useApi, type ApiError } from "@/lib/api";
import { may } from "@/lib/roles";
import { useStartChoice } from "@/lib/start-choice";
import { useSetup } from "@/lib/onboarding-state";
import { OPTIONS, guideFor, type Guide, type Path } from "@/lib/start";
import { describeSetup, type RepositorySetup } from "@/lib/setup";
import {
  BENEFITS,
  CONTACT,
  LIMIT_LABELS,
  STEPS,
  STEP_TITLES,
  TIERS,
  canContinue,
  choiceFor,
  connectionSummary,
  effectiveTier,
  formatLimit,
  nextStep,
  previousStep,
  selectedUses,
  tierAvailability,
  toggleDone,
  toggleUse,
  type Benefit,
  type BillingFacts,
  type BillingRead,
  type Setup,
  type Step,
  type TierName,
} from "@/lib/onboarding";
import { Bar, Button, Card, CommandBlock, LinkButton } from "@/components/ui";
import {
  GitHubMark,
  IconAudit,
  IconCheck,
  IconEnvironments,
  IconMachine,
  IconMcp,
  IconPullRequest,
  IconRuns,
  IconTerminal,
  LogoMark,
} from "@/components/icons";

/**
 * The walk after the first sign-in: four steps in the column the sign-in
 * screen's button was in, beside the cover pane a person arrived through.
 *
 * Everything this page says lives in lib/onboarding.ts and lib/start.ts, with
 * the source of each command, permission, limit and benefit named in the tests
 * beside them. This file is the shape: the cover, the progress bars, one step
 * at a time, and the ways out. Nothing advances on its own; Continue does, and
 * Back goes back without forgetting anything. Skip for now and Escape both
 * land on the environments list and are not asked again, because a question
 * that cannot be declined is a wall.
 *
 * Native inputs, on purpose. The step one cards are real checkboxes and the
 * tier cards are real radios, so a screen reader hears "checkbox, checked"
 * rather than a button with a state bolted on, arrow keys move between the
 * radios the way they do in every form, and the console's one focus ring lands
 * on the input without any of it being written here. The card around each is
 * a label, so the whole card is the target.
 */

/** The origin this console is served from, once there is a window to ask.
 *  The same wait as the command line page, for the same reason: the export is
 *  built with no window, and a command that names the origin holds its place
 *  for a frame rather than rendering wrong and then correcting itself. */
function useOrigin(): string | null {
  const [origin, setOrigin] = useState<string | null>(null);
  useEffect(() => setOrigin(window.location.origin), []);
  return origin;
}

const USE_ICONS: Record<Path, (props: { className?: string }) => ReactNode> = {
  ci: IconPullRequest,
  terminal: IconTerminal,
  hosted: IconMcp,
};

const BENEFIT_ICONS: Record<Benefit["key"], (props: { className?: string }) => ReactNode> = {
  outlives: IconEnvironments,
  history: IconRuns,
  degrades: IconMachine,
  audit: IconAudit,
};

/* -------------------------------------------------------------------------
 * The frame
 * ---------------------------------------------------------------------- */

/**
 * The cover, byte for byte the one behind the sign-in and sign-up screens on
 * the marketing site: the same two radial washes, the same honeycomb and the
 * same sentence. A person arrives here from that screen and the pane they
 * just looked at is the pane they should see. Sticky at the desktop width, so
 * a long third step scrolls past a cover that stays put.
 */
function Cover() {
  return (
    <div className="relative hidden overflow-hidden bg-paper lg:sticky lg:top-0 lg:block lg:h-dvh">
      <div
        className="absolute inset-0"
        style={{
          background:
            "radial-gradient(ellipse 90% 70% at 6% 4%, rgba(51, 191, 0, 0.16) 0%, transparent 52%), radial-gradient(ellipse 85% 70% at 96% 98%, rgba(16, 16, 20, 0.10) 0%, transparent 54%)",
        }}
      />
      <div className="auth-honeycomb absolute inset-0 opacity-80" />
      <div className="relative z-10 flex h-full flex-col items-center justify-center px-16 text-center">
        <LogoMark className="h-14 w-14" />
        <p className="mt-8 max-w-[320px] text-[32px] font-normal leading-dense tracking-tighter text-ink">
          Know what happens before you deploy.
        </p>
      </div>
    </div>
  );
}

/** Four bars and the words. Static: the bars that are filled are the steps
 *  behind and under the reader, and nothing on this page moves on its own. */
function Progress({ step }: { step: Step }) {
  const index = STEPS.indexOf(step);
  return (
    <div>
      <ol aria-label="Steps" className="flex items-center gap-1.5">
        {STEPS.map((s, i) => (
          <li
            key={s}
            aria-current={s === step ? "step" : undefined}
            className={`h-1 flex-1 rounded-sm ${i <= index ? "bg-ink" : "bg-[rgba(16,16,16,0.1)]"}`}
          >
            <span className="sr-only">{STEP_TITLES[s]}</span>
          </li>
        ))}
      </ol>
      <p className="mt-2.5 text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
        Step {index + 1} of {STEPS.length}
      </p>
    </div>
  );
}

/** The command's place, held while the origin is being read. */
function HeldCommand() {
  return (
    <div className="flex items-center gap-3" aria-hidden>
      <div className="min-w-0 flex-1 rounded-md border border-rule bg-[rgba(16,16,16,0.03)] px-3 py-2.5">
        <Bar className="h-4 w-[26ch] max-w-full" />
      </div>
    </div>
  );
}

function Permissions({ children }: { children: ReactNode }) {
  return (
    <dl className="rounded-md border border-rule bg-[rgba(16,16,16,0.02)] px-3.5 py-3">
      <dt className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">Permissions</dt>
      <dd className="mt-1 max-w-[74ch] text-[12.5px] leading-5 text-ink">{children}</dd>
    </dl>
  );
}

const cardClass = (on: boolean, enabled = true) =>
  `flex items-start gap-3.5 rounded-lg border bg-card px-4 py-4 transition-colors ${
    on ? "border-ink shadow-[inset_0_0_0_1px_#101010]" : "border-rule"
  } ${enabled ? "cursor-pointer hover:border-rule-strong" : "cursor-not-allowed"}`;

/* -------------------------------------------------------------------------
 * Step one: where
 * ---------------------------------------------------------------------- */

function UsesStep({ setup, onToggle }: { setup: Setup; onToggle: (path: Path) => void }) {
  return (
    <fieldset>
      <legend className="sr-only">Where you will use Antifailure. Tick every one that applies.</legend>
      <div className="grid gap-3">
        {OPTIONS.map((option) => {
          const on = setup.uses.includes(option.path);
          const Icon = USE_ICONS[option.path];
          return (
            <label key={option.path} className={cardClass(on)}>
              <input
                type="checkbox"
                name="uses"
                value={option.path}
                checked={on}
                onChange={() => onToggle(option.path)}
                className="mt-[3px] h-4 w-4 shrink-0 accent-ink"
              />
              <Icon className="mt-px h-[18px] w-[18px] shrink-0 text-ink" />
              <span className="min-w-0">
                <span className="block text-[14px] font-medium leading-5 tracking-extra-tight text-ink">
                  {option.title}
                </span>
                <span className="mt-1 block text-[12.5px] leading-5 text-muted">{option.detail}</span>
              </span>
            </label>
          );
        })}
      </div>
    </fieldset>
  );
}

/* -------------------------------------------------------------------------
 * Step two: the tier
 * ---------------------------------------------------------------------- */

function TierStep({
  setup,
  context,
  billingError,
  retryBilling,
  onPick,
}: {
  setup: Setup;
  context: Parameters<typeof tierAvailability>[1];
  billingError: ApiError | null;
  retryBilling: () => void;
  onPick: (tier: TierName) => void;
}) {
  const id = useId();
  const chosen = effectiveTier(setup.tier, context);
  return (
    <div className="space-y-4">
      <fieldset>
        <legend className="sr-only">The plan to start on</legend>
        <div className="grid gap-3">
          {TIERS.map((tier) => {
            const availability = tierAvailability(tier.name, context);
            const on = chosen === tier.name;
            const current = context.currentPlan === tier.name;
            const waiting = tier.name !== "free" && context.mayBill && context.billing.status === "loading";
            return (
              <label key={tier.name} className={cardClass(on, availability.selectable)}>
                <input
                  type="radio"
                  name="tier"
                  value={tier.name}
                  checked={on}
                  disabled={!availability.selectable}
                  onChange={() => onPick(tier.name)}
                  aria-describedby={`${id}-${tier.name}`}
                  className="mt-[3px] h-4 w-4 shrink-0 accent-ink"
                />
                <span className="min-w-0 flex-1">
                  <span className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                    <span
                      className={`text-[14px] font-medium leading-5 tracking-extra-tight ${
                        availability.selectable ? "text-ink" : "text-muted"
                      }`}
                    >
                      {tier.label}
                    </span>
                    {current ? (
                      <span className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
                        Current plan
                      </span>
                    ) : null}
                  </span>
                  <span className="mt-1 block text-[12.5px] leading-5 text-muted">{tier.detail}</span>
                  <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2.5 sm:grid-cols-3">
                    {LIMIT_LABELS.map((limit) => (
                      <div key={limit.key} className="min-w-0">
                        <dt className="text-[11px] uppercase tracking-[0.08em] text-dim">{limit.label}</dt>
                        <dd className="tnum mt-0.5 text-[13px] text-ink">{formatLimit(tier, limit.key)}</dd>
                      </div>
                    ))}
                  </dl>
                  <span id={`${id}-${tier.name}`} className="mt-3 block text-[12px] leading-5 text-dim">
                    {waiting ? (
                      <>
                        <Bar className="h-3 w-[34ch] max-w-full" />
                        <span className="sr-only">{availability.note}</span>
                      </>
                    ) : (
                      availability.note
                    )}
                  </span>
                </span>
              </label>
            );
          })}
        </div>
      </fieldset>
      {billingError ? (
        <div
          role="alert"
          className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border border-rule bg-[rgba(138,90,0,0.12)] px-4 py-3"
        >
          <p className="min-w-0 flex-1 text-[12.5px] leading-5 text-ink">
            Could not read whether this control plane takes payment. {billingError.message}
          </p>
          <Button onClick={retryBilling}>Try again</Button>
        </div>
      ) : null}
    </div>
  );
}

/* -------------------------------------------------------------------------
 * Step three: setup
 * ---------------------------------------------------------------------- */

/**
 * The live line under the App install button: how many repositories the
 * installation reached and what the App did about each one's workflow. The
 * same two reads the environments page makes, so the sentence here and the
 * card there cannot disagree. Loading is a bar the shape of the sentence, a
 * failure is a sentence and a retry, and nothing connected yet is said in
 * words rather than as a count of zero.
 */
function ConnectionLine() {
  const state = useApi(async () => {
    const [repositories, rows] = await Promise.all([
      query<{ id: string }[]>("repositories.list", { includeArchived: false }),
      query<RepositorySetup[]>("repositories.setup"),
    ]);
    return { repositories: repositories.length, rows };
  }, []);

  if (state.status === "loading") {
    return (
      <p role="status" className="text-[12.5px] leading-5 text-muted">
        <Bar className="h-3 w-[38ch] max-w-full" />
        <span className="sr-only">Checking which repositories are connected</span>
      </p>
    );
  }
  if (state.status === "error") {
    return (
      <div role="alert" className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <p className="min-w-0 flex-1 text-[12.5px] leading-5 text-muted">
          Could not check which repositories are connected. {state.error.message}
        </p>
        <Button onClick={state.reload}>Try again</Button>
      </div>
    );
  }
  const summary = connectionSummary(state.data.repositories, state.data.rows);
  const pending = state.data.rows.filter((row) => row.state !== "present");
  return (
    <div className="space-y-2">
      <p role="status" className="text-[12.5px] leading-5 text-ink">
        {summary ?? "No repository connected yet. Installing the App on one lists it here."}
      </p>
      {pending.length > 0 ? (
        <ul className="space-y-1">
          {pending.map((row) => {
            const line = describeSetup(row);
            return (
              <li key={row.id} className="flex flex-wrap items-baseline gap-x-2 text-[12px] leading-5">
                <span className="font-mono text-ink">{row.repository}</span>
                {line.href ? (
                  <a
                    href={line.href}
                    rel="noreferrer noopener"
                    className="inline-flex min-h-11 items-center text-ink underline decoration-[rgba(16,16,16,0.25)] underline-offset-4 hover:decoration-ink sm:min-h-0"
                  >
                    {line.label}
                  </a>
                ) : (
                  <span className="text-muted">{line.label}</span>
                )}
              </li>
            );
          })}
        </ul>
      ) : null}
    </div>
  );
}

function SetupCard({
  path,
  guide,
  origin,
  done,
  onToggleDone,
}: {
  path: Path;
  guide: Guide;
  origin: string | null;
  done: boolean;
  onToggleDone: () => void;
}) {
  const option = OPTIONS.find((o) => o.path === path)!;
  const Icon = USE_ICONS[path];
  return (
    <Card
      title={option.title}
      note={guide.heading}
      actions={
        <Button variant={done ? "primary" : "secondary"} pressed={done} onClick={onToggleDone}>
          {done ? (
            <>
              <IconCheck className="h-4 w-4" />
              Done
            </>
          ) : (
            "Mark done"
          )}
        </Button>
      }
    >
      <div className="space-y-5 px-4 py-4">
        <div className="flex items-start gap-3">
          <Icon className="mt-0.5 h-[18px] w-[18px] shrink-0 text-ink" />
          <p className="max-w-[70ch] text-[13px] leading-6 text-muted">{guide.ack}</p>
        </div>
        {guide.step ? (
          <div className="space-y-3">
            <div>
              <LinkButton href={guide.step.href}>
                <GitHubMark />
                {guide.step.label}
              </LinkButton>
            </div>
            {guide.step.note ? (
              <p className="max-w-[74ch] text-[13px] leading-6 text-muted">{guide.step.note}</p>
            ) : null}
          </div>
        ) : null}
        {guide.copy.map((item) => (
          <div key={item.value} className="space-y-2">
            {origin === null ? (
              <HeldCommand />
            ) : (
              <CommandBlock command={item.value} label={item.label} said={item.said} />
            )}
            {item.note ? <p className="max-w-[74ch] text-[13px] leading-6 text-muted">{item.note}</p> : null}
          </div>
        ))}
        {path === "ci" ? <ConnectionLine /> : null}
        <Permissions>{guide.permissions}</Permissions>
      </div>
    </Card>
  );
}

function SetupStep({
  setup,
  origin,
  installUrl,
  onToggleDone,
}: {
  setup: Setup;
  origin: string | null;
  installUrl: string | null | undefined;
  onToggleDone: (path: Path) => void;
}) {
  const uses = selectedUses(setup);
  return (
    <div className="space-y-5">
      <p className="text-[12.5px] leading-5 text-dim" role="status">
        {setup.done.length} of {uses.length} marked done
      </p>
      <div className="space-y-5">
        {uses.map((path) => (
          <SetupCard
            key={path}
            path={path}
            guide={guideFor(path, origin ?? "", installUrl)}
            origin={origin}
            done={setup.done.includes(path)}
            onToggleDone={() => onToggleDone(path)}
          />
        ))}
      </div>
    </div>
  );
}

/* -------------------------------------------------------------------------
 * Step four: what the control plane gives you
 * ---------------------------------------------------------------------- */

function BenefitsStep() {
  return (
    <ul className="divide-y divide-rule rounded-lg border border-rule bg-card">
      {BENEFITS.map((benefit) => {
        const Icon = BENEFIT_ICONS[benefit.key];
        return (
          <li key={benefit.key} className="flex items-start gap-4 px-4 py-4">
            <Icon className="mt-0.5 h-[18px] w-[18px] shrink-0 text-ink" />
            <div className="min-w-0">
              <p className="text-[14px] font-medium leading-5 tracking-extra-tight text-ink">{benefit.title}</p>
              <p className="mt-1 max-w-[62ch] text-[12.5px] leading-5 text-muted">{benefit.detail}</p>
            </div>
          </li>
        );
      })}
    </ul>
  );
}

/* -------------------------------------------------------------------------
 * The page
 * ---------------------------------------------------------------------- */

const LEDES: Record<Step, string> = {
  uses: "Tick every one that applies. Each gets its own setup card two steps from here, and Settings keeps this changeable.",
  tier: "Free is what the organization already has. A paid plan is bought after setup, not before it, so nothing is charged until something is connected.",
  setup: "One card for each place you named. Mark a card done when it is, or come back to it from Settings. Nothing here blocks.",
  benefits: "The engine works without it. This is what changes once it reports here.",
};

export default function StartPage() {
  const router = useRouter();
  const session = useSessionContext();
  const origin = useOrigin();
  const me = session.data;
  const orgId = me?.orgId;
  const { choice, ready: choiceReady, remember } = useStartChoice(orgId);
  const { setup, setSetup } = useSetup(orgId);
  const [step, setStep] = useState<Step>("uses");
  // Whether an answer existed when the page opened, which decides what the
  // corner link says: Skip for now on a first visit, Back to environments on
  // a return from Settings, where skipping has nothing left to skip.
  const [hadAnswer, setHadAnswer] = useState<boolean | null>(null);
  const [checkoutBusy, setCheckoutBusy] = useState(false);
  const [checkoutError, setCheckoutError] = useState<string | null>(null);
  const headingRef = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    if (!choiceReady) return;
    setHadAnswer((had) => (had === null ? choice !== null : had));
  }, [choiceReady, choice]);

  const mayBill = may(me?.role, "billing.manage");
  // subscriptions.current answers under billing.manage only. Asked when this
  // person holds it and resolved to nothing when they do not, so a member is
  // told the plan is an owner's to do rather than shown a refusal.
  const billing = useApi<BillingFacts | null>(async () => {
    if (!mayBill) return null;
    const current = await query<{ configured: boolean; plans: string[]; arrangedPlans?: string[] }>(
      "subscriptions.current",
    );
    return {
      configured: current.configured,
      plans: current.plans ?? [],
      arrangedPlans: current.arrangedPlans ?? [],
    };
  }, [mayBill]);
  const billingRead: BillingRead = !mayBill
    ? { status: "not-asked" }
    : billing.status === "error"
      ? { status: "error" }
      : billing.status === "ready" && billing.data
        ? { status: "ready", facts: billing.data }
        : { status: "loading" };
  const context = { currentPlan: me?.plan, role: me?.role, mayBill, billing: billingRead };

  const leave = () => {
    if (choice === null) remember("skipped");
    router.push("/environments");
  };

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") leave();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [choice]);

  const go = (next: Step) => {
    setStep(next);
    setCheckoutError(null);
    // The heading takes focus so a screen reader hears the new step and a
    // keyboard is at the top of it; the window follows, without smooth motion.
    requestAnimationFrame(() => {
      window.scrollTo(0, 0);
      headingRef.current?.focus({ preventScroll: true });
    });
  };

  const forward = () => {
    // The one word start.ts has always kept is written when step one is
    // answered, so the root, Settings and an older console all read it, and
    // a person who stops after this step is not asked again.
    if (step === "uses") remember(choiceFor(setup.uses));
    const next = nextStep(step);
    if (next) go(next);
  };

  const back = () => {
    const previous = previousStep(step);
    if (previous) go(previous);
  };

  const tier = effectiveTier(setup.tier, context);
  const ending = tierAvailability(tier, context).ends;

  async function checkout() {
    setCheckoutBusy(true);
    setCheckoutError(null);
    try {
      const result = await mutate<{ url: string }>(
        "subscriptions.checkout",
        {
          plan: tier,
          successUrl: `${window.location.origin}/plan?checkout=success`,
          cancelUrl: `${window.location.origin}/plan`,
        },
        me?.csrfToken ?? "",
      );
      window.location.assign(result.url);
    } catch (e) {
      setCheckoutError(e instanceof Error ? e.message : "Could not open the checkout page.");
      setCheckoutBusy(false);
    }
  }

  let body: ReactNode;
  switch (step) {
    case "uses":
      body = (
        <UsesStep
          setup={setup}
          onToggle={(path) =>
            setSetup((current) => {
              const uses = toggleUse(current.uses, path);
              return { ...current, uses, done: current.done.filter((p) => uses.includes(p)) };
            })
          }
        />
      );
      break;
    case "tier":
      body = (
        <TierStep
          setup={setup}
          context={context}
          billingError={billing.status === "error" ? billing.error : null}
          retryBilling={billing.reload}
          onPick={(picked) => setSetup((current) => ({ ...current, tier: picked }))}
        />
      );
      break;
    case "setup":
      body = (
        <SetupStep
          setup={setup}
          origin={origin}
          installUrl={me?.githubAppInstallUrl}
          onToggleDone={(path) =>
            setSetup((current) => ({ ...current, done: toggleDone(current.done, path) }))
          }
        />
      );
      break;
    case "benefits":
      body = <BenefitsStep />;
      break;
  }

  const last = step === "benefits";
  const noteUnderActions = last
    ? ending === "checkout"
      ? "Stripe's checkout opens in this tab and comes back to the Plan page. Nothing is charged until you finish it there."
      : ending === "contact"
        ? "A person arranges the Enterprise plan for this organization. The free plan applies until then."
        : null
    : step === "uses" && setup.uses.length === 0
      ? "Tick at least one to continue."
      : null;

  return (
    // minmax(0, ...) on every track, and min-w-0 on the column. A grid track's
    // default minimum is its content's minimum width, and a command block is
    // one unbreakable line, so at 390 the whole column was 475 wide and the
    // page scrolled sideways under it. The command scrolls inside its own
    // box; the column must not grow to fit it.
    <div className="grid min-h-dvh w-full grid-cols-[minmax(0,1fr)] bg-paper lg:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
      <Cover />
      <div className="relative flex min-w-0 flex-col px-5 py-6 sm:px-8 lg:px-16 lg:py-8 max-sm:pb-[max(2rem,env(safe-area-inset-bottom))]">
        <div className="flex items-center justify-between gap-4">
          <div className="flex min-w-0 items-center gap-3">
            <LogoMark className="h-6 w-6 shrink-0 lg:hidden" />
            <span className="min-w-0 truncate text-[12.5px] text-muted">Signed in as {me?.label}</span>
          </div>
          <button
            type="button"
            onClick={leave}
            className="inline-flex h-11 shrink-0 items-center gap-1.5 rounded-md px-2 text-[13px] text-muted hover:text-ink"
          >
            {hadAnswer ? "Back to environments" : "Skip for now"}
            <svg viewBox="0 0 16 16" className="h-3.5 w-3.5" fill="none" aria-hidden>
              <path d="M6 3l5 5-5 5" stroke="currentColor" strokeWidth="1.4" />
            </svg>
          </button>
        </div>

        <div className="flex flex-1 items-start justify-center py-8 lg:items-center lg:py-10">
          <div className="w-full max-w-[560px]">
            <Progress step={step} />
            <h1
              ref={headingRef}
              tabIndex={-1}
              className="mt-5 text-[28px] font-semibold leading-dense tracking-tighter text-ink outline-none max-sm:text-[26px]"
            >
              {STEP_TITLES[step]}
            </h1>
            <p className="mt-3 max-w-[58ch] text-[13.5px] leading-6 text-muted">{LEDES[step]}</p>

            <div className="mt-7">{body}</div>

            <div className="mt-8 flex flex-wrap items-center gap-3">
              {last ? (
                ending === "checkout" ? (
                  <Button variant="primary" busy={checkoutBusy} onClick={checkout}>
                    {checkoutBusy ? "Opening checkout" : "Continue to checkout"}
                  </Button>
                ) : (
                  <LinkButton href="/environments">Open environments</LinkButton>
                )
              ) : (
                <Button variant="primary" disabled={!canContinue(step, setup.uses)} onClick={forward}>
                  Continue
                </Button>
              )}
              {last && ending === "contact" ? (
                <LinkButton href={CONTACT} variant="secondary">
                  Talk to us about Enterprise
                </LinkButton>
              ) : null}
              {step !== "uses" ? <Button onClick={back}>Back</Button> : null}
            </div>
            {checkoutError ? (
              <p role="alert" className="mt-3 max-w-[60ch] text-[12.5px] leading-5 text-fail">
                {checkoutError}
              </p>
            ) : noteUnderActions ? (
              <p className="mt-3 max-w-[60ch] text-[12.5px] leading-5 text-muted">{noteUnderActions}</p>
            ) : null}
          </div>
        </div>
      </div>
    </div>
  );
}
