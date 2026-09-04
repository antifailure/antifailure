"use client";

import { Suspense, useEffect, useRef, useState } from "react";
import { useSearchParams } from "next/navigation";
import { ApiError, rest, useSession } from "@/lib/api";
import { Bar, Button, Lede, LinkButton, Standalone } from "@/components/ui";
import { approvalDestination, consentParameters, pendingConsent, mcpScopeLabels, type McpConsent } from "@/lib/mcp-consent";

function LoadingRequest() {
  return (
    <Standalone title="Connect an MCP client" width={520}>
      <div className="mt-6 space-y-4" role="status">
        <Bar className="h-5 w-2/3" />
        <Bar className="h-12 w-full" />
        <Bar className="h-12 w-full" />
        <span className="sr-only">Loading connection request</span>
      </div>
    </Standalone>
  );
}

function Connection() {
  const params = useSearchParams();
  const query = params.toString();
  const session = useSession();
  const signedIn = session.status === "ready" && session.data?.signedIn === true;
  const organization = session.data?.orgId ?? "";
  const requestKey = `${query}\n${organization}`;
  const current = useRef(requestKey);
  current.current = requestKey;
  const submitting = useRef(false);
  const [retry, setRetry] = useState(0);
  const [loaded, setLoaded] = useState<{ key: string; pending?: McpConsent; error?: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [writeError, setWriteError] = useState<string | null>(null);
  const [declined, setDeclined] = useState(false);

  useEffect(() => {
    if (!signedIn || !organization) return;
    let active = true;
    setLoaded(null);
    setWriteError(null);
    setDeclined(false);
    try { consentParameters(query); } catch (error) {
      setLoaded({ key: requestKey, error: (error as Error).message });
      return;
    }
    void rest<unknown>(`/auth/mcp/pending?${query}`, { cache: "no-store" })
      .then((value) => {
        const pending = pendingConsent(value, organization);
        if (active) setLoaded({ key: requestKey, pending });
      })
      .catch((error: unknown) => {
        if (active) setLoaded({
          key: requestKey,
          error: error instanceof ApiError || error instanceof Error
            ? error.message
            : "The connection request could not be loaded. Try again.",
        });
      });
    return () => { active = false; };
  }, [query, requestKey, organization, signedIn, retry]);

  if (session.status === "loading") return <LoadingRequest />;
  if (session.status === "error") {
    return (
      <Standalone title="Could not check your account" width={520}>
        <Lede>We could not check whether you are signed in. No client has been connected.</Lede>
        <div className="mt-6"><Button onClick={session.reload}>Try again</Button></div>
      </Standalone>
    );
  }
  if (!signedIn) {
    const back = `/connect-mcp${query ? `?${query}` : ""}`;
    return (
      <Standalone title="Sign in to connect your client" width={520}>
        <Lede>Your MCP client is asking to connect to Antifailure. Sign in to review the request. Signing in does not approve it.</Lede>
        <div className="mt-6">
          <LinkButton href={`/auth/github?redirect_to=${encodeURIComponent(back)}`} full>Continue with GitHub</LinkButton>
        </div>
      </Standalone>
    );
  }
  if (!organization) {
    return (
      <Standalone title="Join an organization first" width={520}>
        <Lede>Your account is signed in but does not belong to an organization. Open the console to finish setup or ask an owner to add you, then return here.</Lede>
        <div className="mt-6 flex flex-wrap gap-3">
          <LinkButton href="/environments">Open the console</LinkButton>
          <Button onClick={session.reload}>Check again</Button>
        </div>
      </Standalone>
    );
  }
  if (declined) {
    return (
      <Standalone title="Connection declined" width={520}>
        <Lede>You have not granted this client access. You can close this page and return to your MCP client.</Lede>
        <div className="mt-6"><LinkButton href="/environments" variant="secondary">Back to Antifailure</LinkButton></div>
      </Standalone>
    );
  }
  if (!loaded || loaded.key !== requestKey) return <LoadingRequest />;
  if (loaded.error || !loaded.pending) {
    return (
      <Standalone title="Could not load this request" width={520}>
        <Lede>{loaded.error ?? "The connection request is incomplete. Nothing has been approved."}</Lede>
        <div className="mt-6 flex flex-wrap gap-3">
          <Button onClick={() => setRetry((n) => n + 1)}>Try again</Button>
          <Button onClick={() => setDeclined(true)}>Decline</Button>
        </div>
      </Standalone>
    );
  }

  const pending = loaded.pending;
  const csrf = session.data?.csrfToken ?? "";

  async function approve() {
    if (submitting.current || !csrf) return;
    submitting.current = true;
    setBusy(true);
    setWriteError(null);
    try {
      const result = await rest<{ redirect?: unknown }>("/auth/mcp/approve", {
        method: "POST", csrf,
        body: Object.fromEntries(consentParameters(query)),
      });
      const destination = approvalDestination(result, pending.redirectUri, params.get("state"));
      if (current.current === requestKey) window.location.assign(destination);
    } catch (error) {
      if (current.current === requestKey) setWriteError(error instanceof Error ? error.message : "The request could not be approved. Try again.");
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  }

  return (
    <Standalone title="Connect your MCP client" width={520}>
      <Lede>Review what this client will be able to do in your current organization. Approve only if you started this connection.</Lede>
      {session.data?.label ? <p className="mt-3 text-[13px] text-muted">Signed in as {session.data.label}</p> : null}
      <dl className="mt-6 divide-y divide-rule border-y border-rule">
        {pending.organizationName ? <div className="py-4">
          <dt className="text-[13px] font-medium text-muted">Organization</dt>
          <dd className="mt-1 break-words text-base text-ink">{pending.organizationName}</dd>
        </div> : null}
        <div className="py-4">
          <dt className="text-[13px] font-medium text-muted">Client requesting access</dt>
          <dd className="mt-1 break-words text-base font-medium text-ink">{pending.clientName}</dd>
        </div>
        <div className="py-4">
          <dt className="text-[13px] font-medium text-muted">Permissions</dt>
          <dd className="mt-2 space-y-3">
            {pending.scopes.map((scope) => (
              <div key={scope}>
                <p className="text-base leading-6 text-ink">{mcpScopeLabels[scope]}</p>
                <p className="mt-1 font-mono text-[12px] text-muted">{scope}</p>
              </div>
            ))}
          </dd>
        </div>
        <div className="py-4">
          <dt className="text-[13px] font-medium text-muted">Access expires</dt>
          <dd className="mt-1 text-base text-ink">In {pending.expiresInDays} days</dd>
        </div>
        <div className="py-4">
          <dt className="text-[13px] font-medium text-muted">Return address after approval</dt>
          <dd className="mt-1 break-all font-mono text-[13px] leading-6 text-ink">{pending.redirectUri}</dd>
        </div>
      </dl>
      {writeError ? <p role="alert" className="mt-4 text-[13px] leading-6 text-fail">{writeError}</p> : null}
      {!csrf ? <p role="alert" className="mt-4 text-[13px] leading-6 text-fail">Your session could not authorize this request. Reload the page to check your session again.</p> : null}
      <div className="mt-6 grid grid-cols-2 gap-3">
        <Button variant="primary" onClick={() => void approve()} busy={busy} disabled={!csrf} full>{busy ? "Approving" : "Approve"}</Button>
        <Button onClick={() => setDeclined(true)} disabled={busy} full>Decline</Button>
      </div>
    </Standalone>
  );
}

export default function ConnectMcpPage() {
  return <Suspense fallback={<LoadingRequest />}><Connection /></Suspense>;
}
