"use client";

import { useEffect, useState } from "react";

const WAITLIST_KEY = "wt-waitlist";

function readWaitlist(): string | null {
  try {
    const raw = localStorage.getItem(WAITLIST_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as { email?: string };
    return parsed.email ?? null;
  } catch {
    return null;
  }
}

function writeWaitlist(email: string) {
  localStorage.setItem(WAITLIST_KEY, JSON.stringify({ email, at: Date.now() }));
}

export function AuthModal({
  open,
  mode,
  onClose,
  onMode,
}: {
  open: boolean;
  mode: "login" | "signup";
  onClose: () => void;
  onMode: (mode: "login" | "signup") => void;
}) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [done, setDone] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setError("");
    setPassword("");
    const existing = readWaitlist();
    setDone(existing);
    setEmail(existing ?? "");
  }, [open, mode]);

  if (!open) return null;

  const jumpToStart = () => {
    onClose();
    document.getElementById("from-pr")?.scrollIntoView({ behavior: "smooth" });
  };

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const value = email.trim().toLowerCase();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value)) {
      setError("Enter a work email.");
      return;
    }
    writeWaitlist(value);
    setPassword("");
    setDone(value);
  };

  return (
    <div
      className="fixed inset-0 z-[80] flex items-center justify-center bg-black/70 px-4"
      onClick={onClose}
    >
      <div
        className="w-full max-w-[380px] rounded-2xl border border-black/10 bg-white p-6 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        {done ? (
          <>
            <div className="text-[15px] font-medium">You’re on the waitlist</div>
            <p className="mt-1 text-[13px] text-black/50">
              We’ll email {done} when the control plane can connect a repo. Nothing is created on a
              server from this page.
            </p>
            <button
              type="button"
              className="mt-5 h-10 w-full rounded-full bg-black text-[13px] font-medium text-white"
              onClick={jumpToStart}
            >
              See how it works
            </button>
            <button
              type="button"
              className="mt-3 w-full text-center text-[12px] text-black/50"
              onClick={onClose}
            >
              Close
            </button>
          </>
        ) : (
          <form onSubmit={submit}>
            <div className="text-[15px] font-medium">
              {mode === "login" ? "Request access" : "Join waitlist"}
            </div>
            <p className="mt-1 text-[13px] text-black/50">
              No account is created. This stores a waitlist email on this device until the product
              ships.
            </p>
            <label className="mt-5 block text-[12px] text-black/50">Email</label>
            <input
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="mt-1.5 h-10 w-full rounded-lg border border-black/10 bg-[#f7f7f5] px-3 text-[13px] text-black outline-none focus:border-black/30"
              placeholder="you@company.com"
              autoComplete="email"
              type="email"
            />
            <label className="mt-3 block text-[12px] text-black/50">Password</label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="mt-1.5 h-10 w-full rounded-lg border border-black/10 bg-[#f7f7f5] px-3 text-[13px] text-black outline-none focus:border-black/30"
              placeholder="••••••••"
              autoComplete="off"
            />
            {error ? <p className="mt-2 text-[12px] text-red-400">{error}</p> : null}
            <button
              type="submit"
              className="mt-5 h-10 w-full rounded-full bg-black text-[13px] font-medium text-white"
            >
              {mode === "login" ? "Request access" : "Join waitlist"}
            </button>
            <button
              type="button"
              className="mt-3 w-full text-center text-[12px] text-black/50"
              onClick={() => onMode(mode === "login" ? "signup" : "login")}
            >
              {mode === "login" ? "Join the waitlist instead" : "Request access instead"}
            </button>
          </form>
        )}
      </div>
    </div>
  );
}
