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
  COVER,
  LIMIT_LABELS,
  STEPS,
  STEP_LABELS,
  STEP_TITLES,
  TIERS,
  canContinue,
  choiceFor,
  connectionSummary,
  doneLine,
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
import { Bar, Button, CommandBlock, LinkButton } from "@/components/ui";
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
 * Native inputs, on purpose. The step one tiles are real checkboxes and the
 * tier cards are real radios, so a screen reader hears "checkbox, checked"
 * rather than a button with a state bolted on, and arrow keys move between the
 * radios the way they do in every form. The input itself is visually hidden
 * and the tile draws its state, so the console's one focus ring is written on
 * the tile through :has(:focus-visible) rather than landing on a box nobody
 * can see. The tile around each input is a label, so the whole tile is the
 * target.
 *
 * ONE MOTION, and it is finite. The right column fades in and rises eight
 * pixels over 160ms when the step changes, which is the whole of what moves
 * on this page: no bar animates, no card scales on hover, the cover swaps its
 * words in place. It respects prefers-reduced-motion, and motioncheck reads
 * the built export to confirm nothing on it loops.
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
 * The cover, the one behind the sign-in and sign-up screens on the marketing
 * site: the same two radial washes and the same honeycomb. A person arrives
 * here from that screen and the pane they just looked at is the pane they
 * should see, so the first headline is that screen's sentence. From there the
 * pane carries the step: each one gets its own headline and one line under
 * it, set in the console's display size, so the left half of the window is
 * not wallpaper by step three. Sticky at the desktop width, so a long third
 * step scrolls past a cover that stays put.
 */
function Cover({ step }: { step: Step }) {
  const copy = COVER[step];
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
      <div className="relative z-10 flex h-full flex-col items-center justify-center px-12 text-center xl:px-16">
        <LogoMark className="h-14 w-14" />
        <p className="mt-8 max-w-[400px] text-balance text-[32px] font-normal leading-dense tracking-tighter text-ink">
          {copy.headline}
        </p>
        <p className="mt-4 max-w-[38ch] text-balance text-[15px] leading-6 text-muted">{copy.line}</p>
      </div>
    </div>
  );
}

/** Four bars, a word under each from the sm width, and the count. Static: the
 *  bars that are filled are the steps behind and under the reader, and
 *  nothing on this page moves on its own. */
function Progress({ step }: { step: Step }) {
  const index = STEPS.indexOf(step);
  return (
    <div>
      <p className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
        Step {index + 1} of {STEPS.length}
      </p>
      <ol aria-label="Steps" className="mt-2.5 grid grid-cols-4 gap-1.5">
        {STEPS.map((s, i) => (
          <li key={s} aria-current={s === step ? "step" : undefined} className="min-w-0">
            <span
              aria-hidden
              className={`block h-1 rounded-sm ${i <= index ? "bg-ink" : "bg-[rgba(16,16,16,0.1)]"}`}
            />
            <span
              className={`mt-2 block truncate text-[13px] tracking-snug max-sm:sr-only ${
                s === step ? "font-medium text-ink" : i < index ? "text-muted" : "text-dim"
              }`}
            >
              {STEP_LABELS[s]}
            </span>
          </li>
        ))}
      </ol>
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
      <dd className="mt-1 max-w-[74ch] text-[13px] leading-5 text-ink">{children}</dd>
    </dl>
  );
}

/**
 * The surface a hidden native input sits in. The ink border plus a one pixel
 * inset ring is the chosen state, which is the same two lines the console's
 * pressed buttons use; the focus ring is drawn on the tile when the input
 * inside it has keyboard focus, because the input is off screen.
 */
const tileClass = (on: boolean, enabled = true) =>
  `relative rounded-lg border bg-card transition-colors has-focus-visible:outline-2 has-focus-visible:outline-offset-2 has-focus-visible:outline-ink ${
    on ? "border-ink shadow-[inset_0_0_0_1px_#101010]" : "border-rule"
  } ${enabled ? "cursor-pointer hover:border-rule-strong" : "cursor-not-allowed"}`;

/** The drawn half of a checkbox: an empty ring at rest, a filled disc with the
 *  check when on. aria-hidden, because the input beside it is the truth. */
function CheckMark({ on }: { on: boolean }) {
  return (
    <span
      aria-hidden
      className={`grid h-5 w-5 shrink-0 place-items-center rounded-full border transition-colors ${
        on ? "border-ink bg-ink text-white" : "border-rule-strong bg-card"
      }`}
    >
      {on ? <IconCheck className="h-3 w-3" /> : null}
    </span>
  );
}

/** The drawn half of a radio: a ring, and a dot inside it when chosen. */
function RadioMark({ on, enabled }: { on: boolean; enabled: boolean }) {
  return (
    <span
      aria-hidden
      className={`mt-0.5 grid h-[18px] w-[18px] shrink-0 place-items-center rounded-full border ${
        on ? "border-ink" : enabled ? "border-rule-strong" : "border-rule"
      }`}
    >
      {on ? <span className="h-2 w-2 rounded-full bg-ink" /> : null}
    </span>
  );
}

/* -------------------------------------------------------------------------
 * Step one: where
 * ---------------------------------------------------------------------- */

/**
 * Three tiles. Three across wherever the column is wide enough for three
 * titles to sit on one line each, which is from sm to lg, where the cover is
 * not yet beside the column, and again from xl, where it is and the column
 * still has 640 pixels; one under another on a phone and at the lg width
 * between. The tile's own layout follows: the icon sits above the words when
 * three are across and beside them when they stack, so a stacked tile is a
 * row and not a tall box with a small icon at the top.
 */
function UsesStep({ setup, onToggle }: { setup: Setup; onToggle: (path: Path) => void }) {
  return (
    <fieldset>
      <legend className="sr-only">Where you will use Antifailure. Tick every one that applies.</legend>
      <div className="grid gap-3 sm:grid-cols-3 lg:grid-cols-1 xl:grid-cols-3">
        {OPTIONS.map((option) => {
          const on = setup.uses.includes(option.path);
          const Icon = USE_ICONS[option.path];
          return (
            <label
              key={option.path}
              className={`${tileClass(on)} flex flex-row items-start gap-4 p-4 sm:flex-col sm:gap-0 sm:p-5 lg:flex-row lg:gap-4 lg:p-4 xl:flex-col xl:gap-0 xl:p-5`}
            >
              <input
                type="checkbox"
                name="uses"
                value={option.path}
                checked={on}
                onChange={() => onToggle(option.path)}
                className="sr-only"
              />
              <Icon className="h-7 w-7 shrink-0 text-ink" />
              <span className="min-w-0 flex-1 pr-7 sm:mt-5 sm:pr-0 lg:mt-0 lg:pr-7 xl:mt-5 xl:pr-0">
                <span className="block text-[15px] font-medium leading-5 tracking-extra-tight text-ink">
                  {option.title}
                </span>
                <span className="mt-1.5 block text-[13px] leading-5 text-muted">{option.detail}</span>
              </span>
              <span className="absolute right-4 top-4">
                <CheckMark on={on} />
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
              <label key={tier.name} className={`${tileClass(on, availability.selectable)} block p-5`}>
                <input
                  type="radio"
                  name="tier"
                  value={tier.name}
                  checked={on}
                  disabled={!availability.selectable}
                  onChange={() => onPick(tier.name)}
                  aria-describedby={`${id}-${tier.name}`}
                  className="sr-only"
                />
                <span className="flex items-start gap-3.5">
                  <RadioMark on={on} enabled={availability.selectable} />
                  <span className="min-w-0 flex-1">
                    <span className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                      <span
                        className={`text-[22px] font-semibold leading-none tracking-tighter ${
                          availability.selectable ? "text-ink" : "text-muted"
                        }`}
                      >
                        {tier.label}
                      </span>
                      <span className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
                        {current ? "Current plan" : tier.tag}
                      </span>
                    </span>
                    <span className="mt-2.5 block max-w-[62ch] text-[13px] leading-5 text-muted">
                      {tier.detail}
                    </span>
                    {/* The six limits as a figure grid: the number first and
                        large, the label under it, tabular so the columns
                        line up across the three cards. Two rows of three
                        from sm, three rows of two on a phone. */}
                    <dl className="mt-4 grid grid-cols-2 gap-x-4 gap-y-3.5 border-t border-rule pt-4 sm:grid-cols-3">
                      {LIMIT_LABELS.map((limit) => (
                        <div key={limit.key} className="flex min-w-0 flex-col-reverse">
                          <dt className="mt-1 text-[11px] uppercase tracking-[0.08em] text-dim">{limit.label}</dt>
                          <dd
                            className={`tnum text-[17px] font-medium leading-none tracking-extra-tight ${
                              availability.selectable ? "text-ink" : "text-muted"
                            }`}
                          >
                            {formatLimit(tier, limit.key)}
                          </dd>
                        </div>
                      ))}
                    </dl>
                    <span id={`${id}-${tier.name}`} className="mt-4 block text-[13px] leading-5 text-dim">
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
          <p className="min-w-0 flex-1 text-[13px] leading-5 text-ink">
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
      <p role="status" className="text-[13px] leading-5 text-muted">
        <Bar className="h-3 w-[38ch] max-w-full" />
        <span className="sr-only">Checking which repositories are connected</span>
      </p>
    );
  }
  if (state.status === "error") {
    return (
      <div role="alert" className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <p className="min-w-0 flex-1 text-[13px] leading-5 text-muted">
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
      <p role="status" className="text-[13px] leading-5 text-ink">
        {summary ?? "No repository connected yet. Installing the App on one lists it here."}
      </p>
      {pending.length > 0 ? (
        <ul className="space-y-1">
          {pending.map((row) => {
            const line = describeSetup(row);
            return (
              <li key={row.id} className="flex flex-wrap items-baseline gap-x-2 text-[13px] leading-5">
                <span className="font-mono text-[12.5px] text-ink">{row.repository}</span>
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

/** The card's number on the rail beside it, or the check once it is done. */
function StepNumber({ n, done }: { n: number; done: boolean }) {
  return (
    <span
      aria-hidden
      className={`tnum grid h-7 w-7 shrink-0 place-items-center rounded-full text-[13px] font-medium transition-colors ${
        done ? "bg-ink text-white" : "border border-rule-strong bg-card text-ink"
      }`}
    >
      {done ? <IconCheck className="h-3.5 w-3.5" /> : n}
    </span>
  );
}

/**
 * Done as a check control rather than a button that changes its label: the
 * box ticks, the card behind it goes to the paper tone and its prose to the
 * dim grey, and the number on the rail becomes the same check. Still a real
 * button with aria-pressed, so a screen reader hears a toggle and its state.
 * The dim grey is the console's own dim token, which is 4.6:1 on paper, so
 * a done card is quieter and still readable rather than faded past reading.
 */
function DoneToggle({ done, onToggle }: { done: boolean; onToggle: () => void }) {
  return (
    <button
      type="button"
      aria-pressed={done}
      onClick={onToggle}
      className={`inline-flex h-11 shrink-0 items-center gap-2.5 whitespace-nowrap rounded-md border bg-card px-3 text-[13px] font-medium text-ink transition-colors sm:h-9 ${
        done ? "border-ink" : "border-rule hover:border-rule-strong"
      }`}
    >
      <span
        aria-hidden
        className={`grid h-[18px] w-[18px] place-items-center rounded-sm border transition-colors ${
          done ? "border-ink bg-ink text-white" : "border-rule-strong bg-card"
        }`}
      >
        {done ? <IconCheck className="h-3 w-3" /> : null}
      </span>
      {done ? "Done" : "Mark done"}
    </button>
  );
}

function SetupCard({
  n,
  path,
  guide,
  origin,
  done,
  onToggleDone,
}: {
  n: number;
  path: Path;
  guide: Guide;
  origin: string | null;
  done: boolean;
  onToggleDone: () => void;
}) {
  const id = useId();
  const option = OPTIONS.find((o) => o.path === path)!;
  const Icon = USE_ICONS[path];
  const prose = done ? "text-dim" : "text-muted";
  return (
    <section
      aria-labelledby={id}
      className={`overflow-hidden rounded-lg border border-rule transition-colors ${done ? "bg-paper" : "bg-card"}`}
    >
      <header className="flex flex-wrap items-center justify-between gap-3 border-b border-rule px-4 py-3">
        <div className="flex min-w-0 items-center gap-3">
          <span className="sm:hidden">
            <StepNumber n={n} done={done} />
          </span>
          <div className="min-w-0">
            <h2 id={id} className="text-[14px] font-semibold tracking-extra-tight text-ink">
              {option.title}
            </h2>
            <p className="mt-0.5 text-[13px] leading-5 text-dim">{guide.heading}</p>
          </div>
        </div>
        <DoneToggle done={done} onToggle={onToggleDone} />
      </header>
      <div className="space-y-5 px-4 py-4">
        <div className="flex items-start gap-3">
          <Icon className="mt-0.5 h-[18px] w-[18px] shrink-0 text-ink" />
          <p className={`max-w-[70ch] text-[13px] leading-6 ${prose}`}>{guide.ack}</p>
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
              <p className={`max-w-[74ch] text-[13px] leading-6 ${prose}`}>{guide.step.note}</p>
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
            {item.note ? <p className={`max-w-[74ch] text-[13px] leading-6 ${prose}`}>{item.note}</p> : null}
          </div>
        ))}
        {path === "ci" ? <ConnectionLine /> : null}
        <Permissions>{guide.permissions}</Permissions>
      </div>
    </section>
  );
}

/**
 * The cards in a numbered rail: 1, 2, 3 down the left from the sm width, a
 * hairline between the numbers, and each number becoming a check as its card
 * is marked done. On a phone the rail's 40 pixels are better spent on the
 * command block, so the number moves into the card's own header.
 */
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
    <div>
      <div className="flex items-center gap-3">
        <span aria-hidden className="flex gap-1">
          {uses.map((path) => (
            <span
              key={path}
              className={`block h-1 w-6 rounded-sm transition-colors ${
                setup.done.includes(path) ? "bg-ink" : "bg-[rgba(16,16,16,0.1)]"
              }`}
            />
          ))}
        </span>
        <p className="tnum text-[13px] text-dim" role="status">
          {doneLine(setup.done.length, uses.length)}
        </p>
      </div>
      <ol className="mt-5 space-y-5 sm:space-y-0">
        {uses.map((path, i) => {
          const done = setup.done.includes(path);
          const last = i === uses.length - 1;
          return (
            <li key={path} className="sm:grid sm:grid-cols-[28px_minmax(0,1fr)] sm:gap-x-4">
              <div className="hidden sm:flex sm:flex-col sm:items-center">
                <StepNumber n={i + 1} done={done} />
                {last ? null : <span aria-hidden className="mt-2 w-px flex-1 bg-rule" />}
              </div>
              <div className={last ? "" : "sm:pb-5"}>
                <SetupCard
                  n={i + 1}
                  path={path}
                  guide={guideFor(path, origin ?? "", installUrl)}
                  origin={origin}
                  done={done}
                  onToggleDone={() => onToggleDone(path)}
                />
              </div>
            </li>
          );
        })}
      </ol>
    </div>
  );
}

/* -------------------------------------------------------------------------
 * Step four: what the control plane gives you
 * ---------------------------------------------------------------------- */

function BenefitsStep() {
  return (
    <ul className="divide-y divide-rule border-y border-rule">
      {BENEFITS.map((benefit) => {
        const Icon = BENEFIT_ICONS[benefit.key];
        return (
          <li key={benefit.key} className="grid grid-cols-[28px_minmax(0,1fr)] gap-x-4 py-5">
            <Icon className="mt-px h-6 w-6 text-ink" />
            <div className="min-w-0">
              <p className="text-[15px] font-medium leading-6 tracking-extra-tight text-ink">{benefit.title}</p>
              <p className="mt-1 max-w-[62ch] text-[13px] leading-6 text-muted">{benefit.detail}</p>
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
      <Cover step={step} />
      <div className="relative flex min-w-0 flex-col px-5 py-6 sm:px-8 lg:px-16 lg:py-8 max-sm:pb-[max(2rem,env(safe-area-inset-bottom))]">
        <div className="flex items-center justify-between gap-4">
          <div className="flex min-w-0 items-center gap-3">
            <LogoMark className="h-6 w-6 shrink-0 lg:hidden" />
            <span className="min-w-0 truncate text-[13px] text-muted">Signed in as {me?.label}</span>
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
          <div className="w-full max-w-[640px]">
            <Progress step={step} />
            {/* Keyed by the step, so a change remounts this block and the one
                entrance animation in globals.css plays once: 160ms, eight
                pixels, and off under prefers-reduced-motion. The progress
                above and the cover beside it swap in place. */}
            <div key={step} className="step-enter">
              <h1
                ref={headingRef}
                tabIndex={-1}
                className="mt-6 text-[28px] font-semibold leading-dense tracking-tighter text-ink outline-none max-sm:text-[26px]"
              >
                {STEP_TITLES[step]}
              </h1>
              <p className="mt-3 max-w-[60ch] text-[13.5px] leading-6 text-muted">{LEDES[step]}</p>

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
                <p role="alert" className="mt-3 max-w-[60ch] text-[13px] leading-5 text-fail">
                  {checkoutError}
                </p>
              ) : noteUnderActions ? (
                <p className="mt-3 max-w-[60ch] text-[13px] leading-5 text-muted">{noteUnderActions}</p>
              ) : null}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
