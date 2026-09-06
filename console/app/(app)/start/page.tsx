"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useSessionContext } from "@/components/session";
import { useStartChoice } from "@/lib/start-choice";
import {
  OPTIONS,
  guideFor,
  optionFor,
  otherPaths,
  type Guide,
  type Path,
} from "@/lib/start";
import { Bar, Button, Card, CommandBlock, LinkButton, Page } from "@/components/ui";
import { GitHubMark } from "@/components/icons";

/**
 * One question, three answers, one tailored next step.
 *
 * Everything this page says lives in lib/start.ts, with the sources for each
 * command and each permission named in its test. This file is the shape of it:
 * the three buttons, the guide that appears under the one pressed, and the two
 * ways out. It never advances on its own. Pressing an answer shows its step
 * and remembers the answer; the person leaves when they press the button that
 * names where they are going, or Not now, or Escape, all three of which land
 * on the environments list and none of which asks again.
 *
 * Buttons rather than a radio group, on purpose. A radio group is one tab stop
 * with arrow keys between the options, which is right for a form field and
 * wrong for three large targets that are the whole page: somebody tabbing
 * through should reach each one, and Enter or Space on it should be the whole
 * gesture. aria-pressed carries which one is chosen.
 */

/**
 * The origin this console is served from, once there is a window to ask.
 * The same wait as the command line page, for the same reason: the export is
 * built with no window, and a command that names the origin holds its place
 * for a frame rather than rendering wrong and then correcting itself.
 */
function useOrigin(): string | null {
  const [origin, setOrigin] = useState<string | null>(null);
  useEffect(() => setOrigin(window.location.origin), []);
  return origin;
}

function Choices({ picked, onPick }: { picked: Path | null; onPick: (path: Path) => void }) {
  return (
    <div role="group" aria-label="How you want to start" className="grid gap-3 sm:grid-cols-3">
      {OPTIONS.map((option) => {
        const on = picked === option.path;
        return (
          <button
            key={option.path}
            type="button"
            aria-pressed={on}
            onClick={() => onPick(option.path)}
            className={`min-h-11 rounded-lg border bg-card px-4 py-4 text-left transition-colors ${
              on
                ? "border-ink shadow-[inset_0_0_0_1px_#101010]"
                : "border-rule hover:border-rule-strong"
            }`}
          >
            <span className="block text-[14px] font-medium leading-5 tracking-extra-tight text-ink">
              {option.title}
            </span>
            <span className="mt-1.5 block text-[12.5px] leading-5 text-muted">{option.detail}</span>
          </button>
        );
      })}
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

function GuideCard({
  guide,
  origin,
  onSwitch,
}: {
  guide: Guide;
  origin: string | null;
  onSwitch: (path: Path) => void;
}) {
  const others = otherPaths(guide.path);
  return (
    <Card title={guide.heading} note={guide.ack}>
      <div className="space-y-5 px-4 py-4">
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
            {item.note ? (
              <p className="max-w-[74ch] text-[13px] leading-6 text-muted">{item.note}</p>
            ) : null}
          </div>
        ))}
        <dl className="rounded-md border border-rule bg-[rgba(16,16,16,0.02)] px-3.5 py-3">
          <dt className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">Permissions</dt>
          <dd className="mt-1 max-w-[74ch] text-[12.5px] leading-5 text-ink">{guide.permissions}</dd>
        </dl>
        {/* The other two doors, as a sentence rather than as two more buttons:
            the full size targets are the three at the top of the page, and this
            line exists so the person is told they are still open. The inline
            style, because the phone rule in globals.css gives every button a
            44px minimum and it is unlayered, so no utility class outranks it,
            and an inline button 44px tall breaks a sentence into ragged rows. */}
        <p className="max-w-[74ch] text-[12.5px] leading-5 text-dim">
          Also here for{" "}
          <button
            type="button"
            onClick={() => onSwitch(others[0]!)}
            style={{ minHeight: 0 }}
            className="text-ink underline decoration-[rgba(16,16,16,0.25)] underline-offset-4 hover:decoration-ink"
          >
            {optionFor(others[0]!).title.toLowerCase()}
          </button>{" "}
          or{" "}
          <button
            type="button"
            onClick={() => onSwitch(others[1]!)}
            style={{ minHeight: 0 }}
            className="text-ink underline decoration-[rgba(16,16,16,0.25)] underline-offset-4 hover:decoration-ink"
          >
            {optionFor(others[1]!).title.toLowerCase()}
          </button>
          ? Both stay open, and Settings keeps this answer changeable.
        </p>
      </div>
    </Card>
  );
}

export default function StartPage() {
  const router = useRouter();
  const session = useSessionContext();
  const origin = useOrigin();
  const { choice, ready, remember } = useStartChoice(session.data?.orgId);
  const [picked, setPicked] = useState<Path | null>(null);
  // Whether an answer existed when the page opened, which decides what the
  // second button is: Not now for a first visit, and Back for a return from
  // Settings, where dismissing has nothing left to dismiss.
  const [hadAnswer, setHadAnswer] = useState<boolean | null>(null);
  const guideRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!ready) return;
    setHadAnswer((had) => (had === null ? choice !== null : had));
    // The stored answer, from this tab or from another one, becomes the
    // pressed button. "skipped" presses nothing.
    setPicked(choice !== null && choice !== "skipped" ? choice : null);
  }, [ready, choice]);

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

  const pick = (path: Path) => {
    setPicked(path);
    remember(path);
    // The guide appears below the fold at a phone width. Bring it up so the
    // press has a visible consequence, without smooth motion.
    requestAnimationFrame(() => guideRef.current?.scrollIntoView({ block: "nearest" }));
  };

  const guide = picked ? guideFor(picked, origin ?? "", session.data?.githubAppInstallUrl) : null;

  return (
    <Page
      title="How do you want to start?"
      lede="Pick the one that matches today. It decides which next step you see, and you can change it in Settings whenever your situation does."
    >
      <div className="space-y-6">
        <Choices picked={picked} onPick={pick} />
        <div ref={guideRef} className="scroll-mt-6">
          {guide ? (
            <GuideCard guide={guide} origin={origin} onSwitch={pick} />
          ) : null}
        </div>
        <div className="space-y-3">
          <div className="flex flex-wrap items-center gap-3">
            {guide ? (
              <>
                <LinkButton href={guide.next.href}>{guide.next.label}</LinkButton>
                <LinkButton href={guide.docs.href} variant="secondary">
                  {guide.docs.label}
                </LinkButton>
              </>
            ) : null}
            <Button onClick={leave}>{hadAnswer ? "Back to environments" : "Not now"}</Button>
          </div>
          {guide ? (
            <p className="max-w-[74ch] text-[12.5px] leading-5 text-muted">{guide.next.note}</p>
          ) : null}
        </div>
      </div>
    </Page>
  );
}
