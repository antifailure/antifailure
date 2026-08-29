"use client";

import { useEffect, useRef, useState } from "react";
import { joinWaitlist, rememberedEmail } from "@/lib/waitlist";

/**
 * The in-page version of the waitlist form. Same substance as the full screen:
 * the address goes to the server, and nothing here claims an account was
 * created, because none is. The password field this used to carry is gone for
 * the reason described in AuthScreen.
 */
export function AuthModal({
  open,
  onClose,
}: {
  open: boolean;
  mode?: "login" | "signup";
  onClose: () => void;
  onMode?: (mode: "login" | "signup") => void;
}) {
  const [email, setEmail] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState<string | null>(null);
  const [already, setAlready] = useState(false);
  const dialogRef = useRef<HTMLDivElement>(null);
  const firstFieldRef = useRef<HTMLInputElement>(null);
  const restoreFocusTo = useRef<Element | null>(null);

  useEffect(() => {
    if (!open) return;
    setError("");
    setBusy(false);
    const known = rememberedEmail();
    setDone(known);
    setAlready(Boolean(known));
    setEmail(known ?? "");
    // Send focus into the dialog and put it back where it came from on close,
    // so a keyboard user is not left tabbing through the page behind this.
    restoreFocusTo.current = document.activeElement;
    const t = window.setTimeout(() => firstFieldRef.current?.focus(), 0);
    return () => {
      window.clearTimeout(t);
      (restoreFocusTo.current as HTMLElement | null)?.focus?.();
    };
  }, [open]);

  // Keep Tab inside the dialog while it is open.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Tab") return;
      const root = dialogRef.current;
      if (!root) return;
      const focusable = root.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])',
      );
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open]);

  if (!open) return null;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError("");
    const result = await joinWaitlist(email, "modal");
    setBusy(false);
    if (!result.ok) {
      setError(result.message);
      return;
    }
    setAlready(result.alreadyJoined);
    setDone(result.email);
  };

  return (
    <div
      className="fixed inset-0 z-[80] flex items-center justify-center bg-black/60 px-4"
      onMouseDown={(e) => {
        // mousedown rather than click, so a drag that starts inside the panel
        // and ends outside it does not dismiss the dialog mid-selection.
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="waitlist-title"
        className="w-full max-w-[400px] rounded-2xl border border-black/10 bg-white p-6 shadow-2xl"
      >
        {done ? (
          <>
            <div id="waitlist-title" className="text-[15px] font-medium text-black">
              {already ? "You are already on the list" : "You are on the list"}
            </div>
            <p className="mt-2 text-[13px] leading-5 text-gray-new-40">
              We have {done}. You will hear from us when there is a hosted
              environment to connect a repository to. The engine itself is open
              source and runs locally today.
            </p>
            <a
              href="/docs/getting-started/quickstart"
              className="mt-5 flex h-10 w-full items-center justify-center rounded-full bg-black text-[13px] font-medium text-white hover:bg-[#292929]"
            >
              Read the quickstart
            </a>
            <button
              type="button"
              className="mt-3 w-full cursor-pointer text-center text-[12px] text-gray-new-40 hover:text-black"
              onClick={onClose}
            >
              Close
            </button>
          </>
        ) : (
          <form onSubmit={submit} noValidate>
            <div id="waitlist-title" className="text-[15px] font-medium text-black">
              Join the waitlist
            </div>
            <p className="mt-2 text-[13px] leading-5 text-gray-new-40">
              There is no hosted control plane to sign in to yet. Leave an
              address and we will tell you when there is. We store the address
              and nothing else.
            </p>
            <label className="mt-5 block text-[12px] text-gray-new-40" htmlFor="waitlist-email">
              Email
            </label>
            <input
              id="waitlist-email"
              ref={firstFieldRef}
              value={email}
              onChange={(e) => {
                setEmail(e.target.value);
                setError("");
              }}
              className="mt-1.5 h-10 w-full rounded-lg border border-black/10 bg-white px-3 text-[13px] text-black outline-none placeholder:text-black/30 focus:border-black/40"
              placeholder="you@company.com"
              autoComplete="email"
              type="email"
              required
              aria-invalid={error ? true : undefined}
              aria-describedby={error ? "waitlist-modal-error" : undefined}
            />
            {error ? (
              <p id="waitlist-modal-error" className="mt-2 text-[12px] text-[#b32d18]" role="alert">
                {error}
              </p>
            ) : null}
            <button
              type="submit"
              disabled={busy}
              className="mt-5 h-10 w-full cursor-pointer rounded-full bg-black text-[13px] font-medium text-white hover:bg-[#292929] disabled:cursor-not-allowed disabled:opacity-60"
            >
              {busy ? "Adding you" : "Join the waitlist"}
            </button>
            <button
              type="button"
              className="mt-3 w-full cursor-pointer text-center text-[12px] text-gray-new-40 hover:text-black"
              onClick={onClose}
            >
              Close
            </button>
          </form>
        )}
      </div>
    </div>
  );
}
