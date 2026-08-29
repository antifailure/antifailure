"use client";

import { Suspense, useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import { useSession, rest, ApiError } from "@/lib/api";
import { LogoMark } from "@/components/icons";
import { Badge, Button, Card, Field, inputClass } from "@/components/ui";
import { when } from "@/lib/format";

interface Pending {
  userCode: string;
  clientLabel: string;
  scopes: string[];
  expiresAt: string;
  organization: string | null;
}

/** Eight characters, grouped, the way the terminal prints it. */
function normalise(raw: string): string {
  const bare = raw.toUpperCase().replace(/[^A-Z0-9]/g, "");
  return bare.slice(0, 8);
}

function Approve() {
  const params = useSearchParams();
  const session = useSession();
  const [code, setCode] = useState(normalise(params.get("code") ?? ""));
  const [pending, setPending] = useState<Pending | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<"approved" | "denied" | null>(null);

  const signedIn = session.status === "ready" && session.data?.signedIn;
  const csrf = session.data?.csrfToken ?? "";

  async function look(value: string) {
    setError(null);
    setPending(null);
    if (value.length !== 8) {
      setError("A code is eight characters.");
      return;
    }
    setBusy(true);
    try {
      setPending(await rest<Pending>(`/auth/device/pending?code=${encodeURIComponent(value)}`));
    } catch (e) {
      setError(
        e instanceof ApiError
          ? e.message
          : "That code has expired, been used, or was never issued.",
      );
    } finally {
      setBusy(false);
    }
  }

  // A code arriving in the link is looked up straight away. Somebody who
  // clicked through from their terminal has already typed it once.
  useEffect(() => {
    const asked = normalise(params.get("code") ?? "");
    if (signedIn && asked.length === 8) void look(asked);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [signedIn]);

  if (session.status === "loading") {
    return (
      <div className="grid min-h-dvh place-items-center" role="status">
        <LogoMark className="h-8 w-8 opacity-40" />
        <span className="sr-only">Loading</span>
      </div>
    );
  }

  if (!signedIn) {
    const back = code ? `/device?code=${encodeURIComponent(code)}` : "/device";
    return (
      <main className="grid min-h-dvh place-items-center px-5 py-10">
        <div className="w-full max-w-[400px]">
          <LogoMark className="h-9 w-9" />
          <h1 className="mt-7 text-[28px] font-semibold leading-dense tracking-tighter text-ink">
            Sign in to approve
          </h1>
          <p className="mt-3 text-[13.5px] leading-6 text-muted">
            A terminal is asking for access to your organization. Sign in first
            and you will come straight back here with the code intact.
          </p>
          <a
            href={`/auth/github?redirect_to=${encodeURIComponent(back)}`}
            className="mt-6 flex h-11 w-full items-center justify-center rounded-[6px] bg-ink text-[14px] font-medium text-white hover:bg-[#2b2b2b]"
          >
            Continue with GitHub
          </a>
        </div>
      </main>
    );
  }

  if (outcome) {
    return (
      <main className="grid min-h-dvh place-items-center px-5 py-10">
        <div className="w-full max-w-[420px]">
          <LogoMark className="h-9 w-9" />
          <h1 className="mt-7 text-[28px] font-semibold leading-dense tracking-tighter text-ink">
            {outcome === "approved" ? "The terminal is signed in" : "Refused"}
          </h1>
          <p className="mt-3 text-[13.5px] leading-6 text-muted">
            {outcome === "approved"
              ? "You can close this page. The terminal has its token and will not ask again."
              : "Nothing was granted. The terminal will report that the request was refused."}
          </p>
        </div>
      </main>
    );
  }

  return (
    <main className="grid min-h-dvh place-items-center px-5 py-10">
      <div className="w-full max-w-[440px]">
        <LogoMark className="h-9 w-9" />
        <h1 className="mt-7 text-[28px] font-semibold leading-dense tracking-tighter text-ink">
          Approve a terminal
        </h1>
        <p className="mt-3 text-[13.5px] leading-6 text-muted">
          Check that the code below is the one your terminal printed. If you did
          not start this, refuse it.
        </p>

        <form
          className="mt-6 flex items-end gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            void look(code);
          }}
        >
          <div className="flex-1">
            <Field label="Code" error={error}>
              <input
                className={`${inputClass} font-mono text-[15px] tracking-[0.18em] uppercase`}
                value={code}
                autoComplete="off"
                spellCheck={false}
                onChange={(e) => {
                  setCode(normalise(e.target.value));
                  setError(null);
                }}
                placeholder="ABCD1234"
              />
            </Field>
          </div>
          <Button type="submit" busy={busy}>
            Look it up
          </Button>
        </form>

        {pending ? (
          <div className="mt-6">
            <Card title="This is what it is asking for">
              <dl className="space-y-3 px-4 py-4">
                <div>
                  <dt className="text-[11px] uppercase tracking-[0.08em] text-dim">Client</dt>
                  <dd className="mt-1 text-[13px] text-ink">{pending.clientLabel}</dd>
                </div>
                <div>
                  <dt className="text-[11px] uppercase tracking-[0.08em] text-dim">Scopes</dt>
                  <dd className="mt-1.5 flex flex-wrap gap-1.5">
                    {pending.scopes.map((s) => (
                      <Badge key={s}>{s}</Badge>
                    ))}
                  </dd>
                </div>
                <div>
                  <dt className="text-[11px] uppercase tracking-[0.08em] text-dim">Expires</dt>
                  <dd className="mt-1 text-[13px] text-ink">{when(pending.expiresAt)}</dd>
                </div>
              </dl>
              <div className="flex flex-wrap gap-2 border-t border-rule px-4 py-3">
                <Button
                  variant="primary"
                  busy={busy}
                  onClick={async () => {
                    setBusy(true);
                    setError(null);
                    try {
                      await rest("/auth/device/approve", {
                        method: "POST",
                        body: { user_code: pending.userCode },
                        csrf,
                      });
                      setOutcome("approved");
                    } catch (e) {
                      setError(e instanceof Error ? e.message : "That did not work.");
                    } finally {
                      setBusy(false);
                    }
                  }}
                >
                  Approve
                </Button>
                <Button
                  variant="danger"
                  busy={busy}
                  onClick={async () => {
                    setBusy(true);
                    setError(null);
                    try {
                      await rest("/auth/device/deny", {
                        method: "POST",
                        body: { user_code: pending.userCode },
                        csrf,
                      });
                      setOutcome("denied");
                    } catch (e) {
                      setError(e instanceof Error ? e.message : "That did not work.");
                    } finally {
                      setBusy(false);
                    }
                  }}
                >
                  Refuse
                </Button>
              </div>
            </Card>
          </div>
        ) : null}
      </div>
    </main>
  );
}

export default function DevicePage() {
  return (
    <Suspense
      fallback={
        <div className="grid min-h-dvh place-items-center" role="status">
          <LogoMark className="h-8 w-8 opacity-40" />
          <span className="sr-only">Loading</span>
        </div>
      }
    >
      <Approve />
    </Suspense>
  );
}
