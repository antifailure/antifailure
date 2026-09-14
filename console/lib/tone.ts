/**
 * The one place this console decides what colour a state word is drawn in.
 *
 * It lives in lib/ and not beside `Badge` in components/ui.tsx for a blunt
 * reason: ui.tsx is a .tsx, `node --test` strips types but cannot parse JSX,
 * and so the single function every screen in the console routes its colours
 * through was the one piece of logic here that no test could import. ui.tsx
 * re-exports it, so every existing caller is unchanged.
 */

export type Tone = "pass" | "fail" | "warn" | "neutral";

export function toneFor(value: string | null | undefined): Tone {
  const v = (value ?? "").toLowerCase();
  if (["pass", "ok", "passed", "ready", "active", "allow", "verified", "approved"].includes(v)) {
    return "pass";
  }
  if (["fail", "failed", "block", "blocked", "denied", "deny", "error", "revoked"].includes(v)) {
    return "fail";
  }
  if (
    [
      "pending",
      "running",
      "waiting",
      "proposed",
      "expiring",
      "warn",
      // In progress. Left out, these fell through to "neutral" and an
      // environment that was still being built looked exactly like one that
      // had been torn down.
      "provisioning",
      "creating",
      "starting",
      "queued",
      "building",
      // The two verdicts that fell through to "neutral", which is how a run
      // that proved nothing came to look like a clean run.
      //
      // Both are real values of `verdict_value` in 0001_init.sql:247, so the
      // tenant runs page was rendering them through here every time one
      // arrived, and grey is what this console draws a thing it has no opinion
      // about in. That is an opinion, and it is the wrong one.
      //
      // `flaky` is amber because it is a FINDING: the same check answered
      // differently on repeat, which loadshapes' VERDICT_FACTS calls conclusive
      // and not a pass, because a result nobody can rely on is not a result.
      //
      // `unverified` is amber for the opposite reason, and amber rather than
      // neutral is the deliberate choice. Neutral is the tone this console uses
      // for a value that needs no reaction, which is exactly the reading that
      // must not be available here: "it finished and nothing could be
      // evaluated" is the one outcome a reader has to act on, because the
      // software was never exercised. There is no fifth tone for "proved
      // nothing", and inventing one would not help: colour cannot carry the
      // difference between "we looked and found something" and "we did not
      // look", so it is not asked to. The WORD in the badge and the banner
      // above the table carry it, which is the same argument
      // components/load/primitives.tsx already settled for `verdictTone`, where
      // blocked and unverified are amber beside flaky and warn for these two
      // opposite reasons. Two views of one vocabulary agreeing is the point.
      "flaky",
      "unverified",
    ].includes(v)
  ) {
    return "warn";
  }
  return "neutral";
}
