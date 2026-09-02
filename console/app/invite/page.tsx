"use client";

import { Suspense, useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import { ApiError, rest, useSession } from "@/lib/api";
import { LogoMark } from "@/components/icons";
import { Badge, Button, Lede, LinkButton, Standalone } from "@/components/ui";

interface Invitation {
  organization: string;
  role: string;
  email: string;
  invitedBy: string;
  expiresAt: string;
  state: "open" | "accepted" | "revoked" | "expired";
}

interface Accepted {
  orgId: string;
  organization: string;
  role: string;
  alreadyMember: boolean;
}

/**
 * The page an invitation link opens.
 *
 * Deliberately outside the (app) group, for the same reason /device is: it is
 * reached by somebody who may not be signed in and, if they are, belongs to no
 * organization yet. Wrapping it in the console chrome would put a navigation
 * rail around a screen whose whole purpose is that there is nothing to
 * navigate to.
 *
 * The invitation is looked up BEFORE sign-in and shown, which is the part that
 * makes this usable. Sending somebody to a sign-in page and only then telling
 * them what they signed in for is how an invitation gets ignored: the person
 * cannot tell it from a phishing attempt until after they have handed over a
 * session.
 */
function Accept() {
  const params = useSearchParams();
  const token = params.get("token") ?? "";
  const session = useSession();

  const [invitation, setInvitation] = useState<Invitation | null>(null);
  const [lookupError, setLookupError] = useState<string | null>(null);
  const [looking, setLooking] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [accepted, setAccepted] = useState<Accepted | null>(null);

  useEffect(() => {
    let alive = true;
    if (!token) {
      setLooking(false);
      setLookupError("This link is missing its token. Ask for a new invitation.");
      return;
    }
    rest<Invitation>(`/auth/invitation?token=${encodeURIComponent(token)}`)
      .then((found) => {
        if (alive) setInvitation(found);
      })
      .catch((err: unknown) => {
        if (!alive) return;
        setLookupError(
          err instanceof ApiError
            ? err.message
            : "The control plane could not be reached. Try the link again in a moment.",
        );
      })
      .finally(() => {
        if (alive) setLooking(false);
      });
    return () => {
      alive = false;
    };
  }, [token]);

  if (looking || session.status === "loading") {
    return (
      <div className="grid min-h-dvh place-items-center" role="status">
        <LogoMark className="h-8 w-8 opacity-40" />
        <span className="sr-only">Loading</span>
      </div>
    );
  }

  if (lookupError || !invitation) {
    return (
      <Standalone title="That invitation is not valid" alert>
        <Lede>
          {lookupError ??
            "This link is not valid. Ask whoever invited you to send a new one; the old link stops working as soon as they do."}
        </Lede>
        <div className="mt-7">
          <LinkButton href="/">Go to the console</LinkButton>
        </div>
      </Standalone>
    );
  }

  if (accepted) {
    return (
      <Standalone title={`You are in ${accepted.organization}`}>
        <Lede>
          {accepted.alreadyMember
            ? `You were already a member of ${accepted.organization}. Nothing changed.`
            : `You joined ${accepted.organization} as ${accepted.role}.`}
        </Lede>
        <div className="mt-7">
          <LinkButton href="/environments" full>
            Open the console
          </LinkButton>
        </div>
      </Standalone>
    );
  }

  if (invitation.state !== "open") {
    const said =
      invitation.state === "accepted"
        ? "This invitation has already been used. If that was you, sign in and you are already a member."
        : invitation.state === "revoked"
          ? "This invitation was withdrawn. Ask whoever invited you to send a new one."
          : "This invitation has expired. Ask whoever invited you to send a new one.";
    return (
      <Standalone title={`Invitation to ${invitation.organization}`} alert>
        <Lede>{said}</Lede>
        <div className="mt-7">
          <LinkButton href="/">Go to the console</LinkButton>
        </div>
      </Standalone>
    );
  }

  const signedIn = session.status === "ready" && session.data?.signedIn;
  const csrf = session.data?.csrfToken ?? "";

  return (
    <Standalone title={`Join ${invitation.organization}`} width={460}>
      <Lede>
        {invitation.invitedBy} invited {invitation.email} to {invitation.organization} on
        Antifailure.
      </Lede>

      <dl className="mt-6 grid grid-cols-2 gap-x-4 gap-y-4 rounded-lg border border-rule bg-card px-4 py-4">
        <div className="min-w-0">
          <dt className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">Role</dt>
          <dd className="mt-1">
            <Badge>{invitation.role}</Badge>
          </dd>
        </div>
        <div className="min-w-0">
          <dt className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
            Link expires
          </dt>
          <dd className="mt-1 text-[13px] text-ink">
            {new Date(invitation.expiresAt).toLocaleDateString()}
          </dd>
        </div>
      </dl>

      {signedIn ? (
        <>
          <div className="mt-7">
            <Button
              variant="primary"
              busy={busy}
              onClick={async () => {
                setBusy(true);
                setError(null);
                try {
                  setAccepted(
                    await rest<Accepted>("/auth/invitation/accept", {
                      method: "POST",
                      body: { token },
                      csrf,
                    }),
                  );
                } catch (err) {
                  setError(
                    err instanceof ApiError
                      ? err.message
                      : "The control plane could not be reached. Try again in a moment.",
                  );
                } finally {
                  setBusy(false);
                }
              }}
            >
              Accept and join
            </Button>
          </div>
          <p className="mt-3 text-[12.5px] leading-5 text-dim">
            Signed in as {session.data?.label}. Accepting adds this account, not the address the
            invitation was sent to.
          </p>
          {error ? (
            <p role="alert" className="mt-3 text-[12.5px] leading-5 text-fail">
              {error}
            </p>
          ) : null}
        </>
      ) : (
        <>
          <div className="mt-7">
            <LinkButton href={`/auth/github?redirect_to=${encodeURIComponent(`/invite?token=${token}`)}`} full>
              Sign in with GitHub to accept
            </LinkButton>
          </div>
          <p className="mt-3 text-[12.5px] leading-5 text-dim">
            Nothing happens until you sign in and accept. You come straight back here.
          </p>
        </>
      )}
    </Standalone>
  );
}

export default function InvitePage() {
  return (
    <Suspense
      fallback={
        <div className="grid min-h-dvh place-items-center" role="status">
          <LogoMark className="h-8 w-8 opacity-40" />
          <span className="sr-only">Loading</span>
        </div>
      }
    >
      <Accept />
    </Suspense>
  );
}
