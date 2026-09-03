"use client";

import { useCallback, useEffect, useState } from "react";
import { cn } from "@/lib/cn";
import {
  measurementStatus,
  setMeasurement,
  type MeasurementOff,
  type MeasurementStatus,
} from "@/lib/analytics";

/**
 * The control the subprocessor page already promises exists.
 *
 * That page says, of the counting this site does, "if you switch measurement
 * off, a single flag saying so is kept in this browser". Until this component
 * there was nothing to switch it off WITH except a query parameter nobody
 * outside this repository knows about, so the sentence described a capability a
 * reader had no way to reach. A privacy claim that needs a URL trick to be true
 * is the same defect as a gate function with no caller: everything is built
 * except the part that makes it happen.
 *
 * WHY IT RENDERS A REASON RATHER THAN A POSITION.
 *
 * Four different things can stop this reader being counted and only one of them
 * is the switch. A browser sending Global Privacy Control is not counted
 * whatever the switch says, and a build with no endpoint counts nobody. A
 * control that showed a bare "off" for all four would invite somebody with GPC
 * set to press it, see nothing change, and reasonably conclude the whole
 * disclosure is theatre. So the beacon reports WHY, from a closed set, and each
 * member of that set has its own sentence here.
 */

/** Named separately from the render so an unhandled state cannot compile. */
const REASONS: Record<MeasurementOff, { state: string; detail: string }> = {
  reader: {
    state: "Not counting this visit.",
    detail:
      "You switched measurement off in this browser. The only thing kept is the flag that says so, it stays on this device, and it is never sent anywhere.",
  },
  browser: {
    state: "Not counting this visit.",
    detail:
      "Your browser sends Global Privacy Control or Do Not Track, and this site honours it. That decision is the browser’s, so the switch cannot turn counting back on while it stands.",
  },
  automated: {
    state: "Not counting this visit.",
    detail:
      "This browser reports itself as a crawler or an automated session. Those are left out of the numbers rather than counted as readers, which is why a bot cannot inflate them.",
  },
  build: {
    state: "Not counting anybody.",
    detail:
      "This build of the site has no measurement endpoint configured, so nothing here is counting, for anyone, however this switch is set.",
  },
};

const ON = {
  state: "Counting this visit.",
  detail:
    "A page shape and a channel, both from closed lists, and an identifier that lives in this tab and expires after thirty minutes idle. No cookie, no referrer, no address, nothing that joins two visits. Switching it off stops this browser sending immediately, including anything captured and not yet sent.",
};

const READING = {
  state: "Reading this browser.",
  detail:
    "The answer depends on settings only your browser can be asked for, so it is read here rather than guessed on the server.",
};

export function MeasurementSwitch() {
  // Null until mounted. The server cannot know any of this, and rendering a
  // guess and correcting it is how a reader sees "counting" flash before
  // "not counting", which on a privacy page is the one flicker worth avoiding.
  const [status, setStatus] = useState<MeasurementStatus | null>(null);

  useEffect(() => {
    setStatus(measurementStatus());
  }, []);

  const toggle = useCallback(() => {
    setMeasurement(!(status?.measuring ?? false));
    // Read back rather than assumed. If the browser refused the write, or asks
    // not to be tracked, the answer is not the one just requested and this
    // control has to show what is true rather than what was pressed.
    setStatus(measurementStatus());
  }, [status]);

  const on = status?.measuring ?? false;
  const words = status === null ? READING : status.off === null ? ON : REASONS[status.off];
  // Only the reader's own preference is the switch's to change. The other three
  // reasons are the browser's, the crawler's, or this deployment's.
  const changeable = status !== null && (status.off === null || status.off === "reader");

  return (
    <div className="mt-10 max-w-[720px] rounded-[12px] border border-black/[0.08] bg-white p-6 max-md:p-5">
      <div className="flex items-center justify-between gap-6 max-md:gap-4">
        <div>
          <div className="font-mono text-[11px] font-medium uppercase tracking-snug text-[#1A1A1A]">
            This browser
          </div>
          <p className="mt-2 text-[17px] leading-snug tracking-extra-tight text-black max-md:text-[16px]">
            {words.state}
          </p>
        </div>
        {changeable ? (
          <button
            type="button"
            role="switch"
            aria-checked={on}
            aria-label="Count my visits to this site"
            onClick={toggle}
            className="-m-2 flex shrink-0 cursor-pointer items-center justify-center p-2 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-black"
          >
            <span
              className={cn(
                "relative block h-[28px] w-[48px] rounded-full border transition-colors duration-150",
                on ? "border-black bg-black" : "border-black/15 bg-black/10",
              )}
            >
              <span
                className={cn(
                  "absolute left-[3px] top-[3px] block h-[20px] w-[20px] rounded-full bg-white shadow-[0_1px_2px_rgba(0,0,0,0.25)] transition-transform duration-150 motion-reduce:transition-none",
                  on && "translate-x-[20px]",
                )}
              />
            </span>
          </button>
        ) : (
          // NOT A DISABLED SWITCH. A greyed out toggle beside a sentence saying
          // the toggle cannot change anything is the same control twice, and a
          // disabled state that differs from the live one only by opacity is
          // the one people press anyway. Where the decision is not the
          // reader's to make, there is no switch: there is what is true.
          <span className="shrink-0 rounded-full border border-black/12 bg-black/[0.04] px-3 py-1.5 font-mono text-[11px] font-medium uppercase tracking-snug text-gray-new-40">
            {status === null ? "Reading" : "Off"}
          </span>
        )}
      </div>
      <p className="mt-4 text-[15px] leading-7 tracking-extra-tight text-black/70">{words.detail}</p>
    </div>
  );
}
