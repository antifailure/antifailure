"use client";

import { useState } from "react";
import { joinWaitlist } from "@/lib/waitlist";

/**
 * The waitlist form in the footer.
 *
 * This column previously held a copy of the install command that already
 * appears in the panel immediately above it, truncated to
 * "curl -fsSL https://antifailure.dev/i…" because the column is 320px wide. A
 * duplicated call to action, cut off mid-URL, is worse than the empty space it
 * was put there to fill.
 *
 * It posts to the same endpoint as the sign-up page and reports only what the
 * server actually confirmed.
 */
export function FooterWaitlist() {
  const [email, setEmail] = useState("");
  const [state, setState] = useState<"idle" | "busy" | "done" | "error">("idle");
  const [message, setMessage] = useState("");

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (state === "busy") return;
    setState("busy");
    const result = await joinWaitlist(email, "footer");
    if (!result.ok) {
      setMessage(result.message);
      setState("error");
      return;
    }
    setMessage(
      result.alreadyJoined ? "You were already on the list." : "You are on the list.",
    );
    setState("done");
  };

  if (state === "done") {
    return (
      <p
        className="mt-7 flex items-start gap-x-2.5 text-[14px] leading-normal tracking-extra-tight text-gray-new-40"
        role="status"
      >
        <span className="mt-1.5 h-2 w-2 shrink-0 rounded-full bg-[#33bf00]" />
        <span>
          {message} We will write when there is a hosted environment to connect a
          repository to.
        </span>
      </p>
    );
  }

  return (
    <form onSubmit={submit} noValidate className="mt-7 w-full max-w-[340px]">
      <label
        className="block text-[13px] tracking-extra-tight text-gray-new-40"
        htmlFor="footer-waitlist"
      >
        Get told when the hosted product exists
      </label>
      <div className="mt-2.5 flex gap-x-2">
        <input
          id="footer-waitlist"
          type="email"
          required
          value={email}
          placeholder="you@company.com"
          autoComplete="email"
          aria-invalid={state === "error" ? true : undefined}
          aria-describedby={state === "error" ? "footer-waitlist-error" : undefined}
          onChange={(e) => {
            setEmail(e.target.value);
            if (state === "error") setState("idle");
          }}
          className="h-10 min-w-0 flex-1 rounded-full border border-gray-new-90 bg-white px-4 text-[14px] tracking-extra-tight text-black outline-none placeholder:text-gray-new-60 focus:border-gray-new-40"
        />
        <button
          type="submit"
          disabled={state === "busy"}
          className="h-10 shrink-0 cursor-pointer rounded-full bg-black px-5 text-[14px] font-medium tracking-extra-tight text-white transition-colors duration-200 hover:bg-[#292929] disabled:cursor-not-allowed disabled:opacity-60"
        >
          {state === "busy" ? "Adding" : "Join"}
        </button>
      </div>
      {state === "error" ? (
        <p
          id="footer-waitlist-error"
          role="alert"
          className="mt-2 text-[13px] tracking-extra-tight text-[#b32d18]"
        >
          {message}
        </p>
      ) : null}
    </form>
  );
}
