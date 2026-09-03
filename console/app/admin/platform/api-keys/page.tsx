"use client";

import { useState } from "react";
import {
  Badge,
  Button,
  Card,
  Confirm,
  Field,
  Loaded,
  TableSkeleton,
  When,
  inputClass,
} from "@/components/ui";
import {
  AdminPage,
  DataTable,
  EmptyList,
  FilterBar,
  StatusChip,
  type Column,
} from "@/components/admin/primitives";
import { More } from "@/components/pagination";
import { operatorMay, useAdminContext } from "@/lib/admin";
import {
  revokeBinding,
  revokeCredential,
  standingTone,
  useBindings,
  useCredentials,
  type AdminBinding,
  type AdminCredential,
  type ApiError,
} from "@/lib/admin-platform";

/**
 * Every credential that can act as a customer, and the one thing to do about
 * one.
 *
 * WHAT THIS SCREEN IS FOR. Somebody reports a key in a public repository, or an
 * account is being closed, or a pipeline stopped and nobody knows whether the
 * credential behind it is still alive. All three are answered here, and the
 * third is why revoked and expired credentials are listed rather than hidden:
 * the reader is usually checking that the one they killed is the one that
 * stopped working.
 *
 * WHY THERE IS NO ROTATE BUTTON, said on the page rather than left as an
 * absence for somebody to file a bug about. Rotation means minting a
 * replacement, a replacement is a secret, and a route that minted one would
 * have to return it through this portal. Only the hash of a credential is
 * stored, so nothing in this product can show one after the moment it is
 * created, and the operator portal must not become the exception. Revoke is the
 * honest action; the customer creates the replacement.
 *
 * THE VALUE IS NOT HERE AND CANNOT BE. Every row carries a prefix, which is the
 * first twelve characters and exactly what the customer sees in their own
 * console, so an operator and a customer can agree which credential they are
 * discussing without either of them holding one.
 */

const KIND_WORDS: Record<string, string> = {
  engine: "engine",
  cli: "person",
  oidc: "workflow",
};

const KIND_HINTS: Record<string, string> = {
  engine:
    "Belongs to the organization rather than to whoever made it, so it keeps working after that person leaves.",
  cli: "Minted by af login for one person's machine, and it acts as them.",
  oidc: "Minted per workflow job by trading a GitHub identity. Short lived, and tied to a binding.",
};

export default function PlatformApiKeysPage() {
  return (
    <AdminPage
      href="/admin/platform/api-keys"
      lede="Every credential that can act as a customer, across every organization. Values are stored only as hashes and are never shown here."
    >
      <div className="space-y-5">
        <Credentials />
        <Bindings />
      </div>
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * Credentials
 * ---------------------------------------------------------------------- */

function Credentials() {
  const { me } = useAdminContext();
  const [search, setSearch] = useState("");
  const [kind, setKind] = useState("");
  const [live, setLive] = useState("");
  const state = useCredentials(search, kind, live === "live");
  const [revoking, setRevoking] = useState<AdminCredential | null>(null);

  const mayRevoke = operatorMay(me, "admin.keys.revoke");

  const columns: Column<AdminCredential>[] = [
    {
      key: "name",
      header: "Credential",
      cell: (r) => (
        <>
          <span className="block truncate font-medium text-ink">{r.name}</span>
          <span className="block truncate font-mono text-[12px] text-muted">{r.prefix}</span>
        </>
      ),
    },
    {
      key: "org",
      header: "Organization",
      cell: (r) => <span className="font-mono text-[12px]">{r.orgSlug}</span>,
    },
    {
      key: "kind",
      header: "Kind",
      cell: (r) => (
        <>
          <Badge tone="neutral">{KIND_WORDS[r.kind] ?? r.kind}</Badge>
          {r.bindingRepository ? (
            <span className="mt-1 block truncate font-mono text-[12px] text-muted">
              {r.bindingRepository}
            </span>
          ) : null}
        </>
      ),
    },
    {
      key: "standing",
      header: "Standing",
      cell: (r) => (
        <>
          <StatusChip value={r.standing} tone={standingTone(r.standing)} />
          {r.standing === "expired" && r.expiresAt ? (
            <span className="mt-1 block text-[12px] text-muted">
              <When value={r.expiresAt} />
            </span>
          ) : null}
        </>
      ),
    },
    {
      key: "lastUsed",
      header: "Last used",
      cell: (r) =>
        // Never used and used a year ago are different facts, and a blank cell
        // reads as a value that failed to load rather than as either of them.
        r.lastUsedAt === null ? (
          <span className="text-dim">Never used</span>
        ) : (
          <When value={r.lastUsedAt} />
        ),
    },
    {
      key: "actor",
      header: "Acts as",
      cell: (r) =>
        r.actsAs ? (
          <span className="truncate">{r.actsAs}</span>
        ) : (
          // Null on a machine token on purpose. Saying "a machine" rather than
          // leaving it empty is the difference between a stated fact and a
          // field somebody thinks is broken.
          <span className="text-dim">A machine</span>
        ),
    },
  ];

  if (mayRevoke) {
    columns.push({
      key: "revoke",
      header: "Action",
      cell: (r) =>
        // A button only where pressing it changes something observable.
        //
        // An expired credential is ALREADY refused: authenticateEngine compares
        // expires_at against the clock on every request before it does anything
        // else, so revoking one would move a column and stop nothing. A control
        // whose effect cannot be observed is the shape of a feature that looks
        // built and is not, and this list is not a queue of rows to tidy.
        r.standing === "revoked" ? (
          <span className="text-dim">Revoked</span>
        ) : r.standing === "expired" ? (
          <span className="text-dim">Expired, already refused</span>
        ) : (
          <Button variant="danger" onClick={() => setRevoking(r)}>
            Revoke
            {/* The row's own credential inside the button's accessible name.
                Six buttons all announced as "Revoke" are six buttons a screen
                reader user cannot tell apart, and this is the list where
                pressing the wrong one stops the wrong customer working. A span
                rather than an aria-label because Button takes children and not
                arbitrary attributes, and an aria-label passed to it would be
                dropped silently. */}
            <span className="sr-only">
              {" "}
              {r.name}, {r.prefix}, in {r.orgSlug}
            </span>
          </Button>
        ),
    });
  }

  return (
    <Card
      title="Credentials"
      note={
        mayRevoke
          ? "Revoking is immediate and cannot be undone. There is no rotate: only the hash is stored, so nothing here can produce a replacement. The customer makes one with af token create."
          : "Your role can read this list and not change it."
      }
    >
      <FilterBar
        search={{
          value: search,
          onChange: setSearch,
          label: "Search credentials by organization, name or prefix",
          placeholder: "Organization, name or prefix",
        }}
        filters={[
          {
            label: "Kind",
            value: kind,
            onChange: setKind,
            options: [
              { value: "", label: "Every kind" },
              { value: "engine", label: "Engine" },
              { value: "cli", label: "Person" },
              { value: "oidc", label: "Workflow" },
            ],
          },
          {
            label: "Standing",
            value: live,
            onChange: setLive,
            options: [
              { value: "", label: "Including revoked and expired" },
              { value: "live", label: "Live only" },
            ],
          },
        ]}
      />
      <Loaded state={state} skeleton={<TableSkeleton rows={6} cols={6} />}>
        {(rows) => (
          <DataTable
            columns={columns}
            rows={rows}
            keyOf={(r) => r.id}
            empty={
              <EmptyList
                title={
                  search || kind || live
                    ? "No credential matches that"
                    : "No credentials have been created"
                }
              >
                {search || kind || live
                  ? "Nothing on this installation matches those filters. Clearing them shows every credential, including revoked and expired ones."
                  : "Nobody has created an engine token, signed in with af login, or connected a workflow identity yet. The first one appears here as soon as they do."}
              </EmptyList>
            }
            footer={
              <More
                shown={rows.length}
                noun={{ one: "credential", many: "credentials" }}
                hasMore={state.hasMore}
                busy={state.busy}
                error={state.moreError}
                onMore={state.more}
              />
            }
          />
        )}
      </Loaded>

      <RevokeCredential
        credential={revoking}
        onClose={() => setRevoking(null)}
        onDone={state.reload}
      />
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * The confirmation
 * ---------------------------------------------------------------------- */

/** The shortest reason the route will take. Checked here as well so the button
 *  says why it is disabled rather than the server refusing after the press. */
const MIN_REASON = 8;

function RevokeCredential({
  credential,
  onClose,
  onDone,
}: {
  credential: AdminCredential | null;
  onClose: () => void;
  onDone: () => void;
}) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function run() {
    if (!credential) return;
    setBusy(true);
    setError(null);
    try {
      await revokeCredential(credential.id, reason.trim());
      setReason("");
      onClose();
      onDone();
    } catch (err) {
      setError((err as ApiError).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Confirm
      open={credential !== null}
      title={credential ? `Revoke ${credential.name}?` : "Revoke"}
      // The credential's own prefix, not the word "delete". Typing the thing's
      // name is what makes somebody read the row they are about to act on, and
      // in a list of six similar credentials that is the whole safeguard.
      phrase={credential?.prefix}
      confirmLabel="Revoke"
      busy={busy}
      error={error ?? (reason.trim().length > 0 && reason.trim().length < MIN_REASON ? `The reason needs at least ${MIN_REASON} characters.` : null)}
      onCancel={() => {
        setReason("");
        setError(null);
        onClose();
      }}
      onConfirm={() => void run()}
    >
      <p>
        <strong className="font-medium text-ink">What this does:</strong> the next request
        presenting this credential is refused. Anything already running keeps running until it next
        calls the control plane.
      </p>
      <p>
        There is no way back. Only a hash of the value is stored, so nobody, including us, can
        recover it. {credential ? <span className="font-mono">{credential.orgSlug}</span> : null}{" "}
        creates a replacement themselves with <span className="font-mono">af token create</span>.
      </p>
      {credential ? (
        <p className="text-[12.5px] text-dim">{KIND_HINTS[credential.kind] ?? ""}</p>
      ) : null}
      <Field
        label="Reason"
        hint="Recorded in the platform audit chain and in the customer's own audit log, where they will read it. Required."
      >
        <input
          className={inputClass}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          maxLength={500}
          required
        />
      </Field>
    </Confirm>
  );
}

/* -------------------------------------------------------------------------
 * OIDC bindings
 * ---------------------------------------------------------------------- */

function Bindings() {
  const { me } = useAdminContext();
  const [search, setSearch] = useState("");
  const state = useBindings(search);
  const [revoking, setRevoking] = useState<AdminBinding | null>(null);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<string | null>(null);

  const mayRevoke = operatorMay(me, "admin.keys.revoke");

  async function run() {
    if (!revoking) return;
    setBusy(true);
    setError(null);
    try {
      const done = await revokeBinding(revoking.id, reason.trim());
      // The route's own words, including the count it actually revoked, rather
      // than this page guessing from the number it rendered a moment ago.
      setResult(
        done.alreadyRevoked
          ? done.effect
          : `${done.repository} can no longer trade a workflow identity for a token. ${done.tokensRevoked} live token${done.tokensRevoked === 1 ? "" : "s"} stopped working.`,
      );
      setReason("");
      setRevoking(null);
      state.reload();
    } catch (err) {
      setError((err as ApiError).message);
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<AdminBinding>[] = [
    {
      key: "repository",
      header: "Repository",
      cell: (r) => <span className="block truncate font-mono text-[12px] text-ink">{r.repository}</span>,
    },
    {
      key: "org",
      header: "Organization",
      cell: (r) => <span className="font-mono text-[12px]">{r.orgSlug}</span>,
    },
    {
      key: "standing",
      header: "Standing",
      cell: (r) =>
        r.revokedAt === null ? (
          <Badge tone="pass">live</Badge>
        ) : (
          <Badge tone="neutral">revoked</Badge>
        ),
    },
    {
      key: "tokens",
      header: "Live tokens",
      numeric: true,
      cell: (r) => r.liveTokens.toLocaleString(),
    },
    {
      key: "lastUsed",
      header: "Last minted",
      cell: (r) =>
        r.lastUsedAt === null ? (
          <span className="text-dim">Never</span>
        ) : (
          <When value={r.lastUsedAt} />
        ),
    },
  ];

  if (mayRevoke) {
    columns.push({
      key: "revoke",
      header: "Action",
      cell: (r) =>
        r.revokedAt !== null ? (
          <span className="text-dim">Revoked</span>
        ) : (
          <Button variant="danger" onClick={() => setRevoking(r)}>
            Revoke
            <span className="sr-only">
              {" "}
              the binding for {r.repository} in {r.orgSlug}
            </span>
          </Button>
        ),
    });
  }

  return (
    <Card
      title="GitHub workflow identities"
      note="A binding lets workflows in one repository trade their own GitHub identity for a short lived token. Revoking one also revokes every token it has already minted."
    >
      <FilterBar
        search={{
          value: search,
          onChange: setSearch,
          label: "Search bindings by repository or organization",
          placeholder: "Repository or organization",
        }}
      />
      {result ? (
        <p role="status" className="border-b border-rule px-4 py-3 text-[12.5px] leading-5 text-ink">
          {result}
        </p>
      ) : null}
      <Loaded state={state} skeleton={<TableSkeleton rows={4} cols={5} />}>
        {(rows) => (
          <DataTable
            columns={columns}
            rows={rows}
            keyOf={(r) => r.id}
            empty={
              <EmptyList
                title={search ? "No binding matches that" : "No workflow identities are bound"}
              >
                {search
                  ? "No repository or organization on this installation matches that. Clear the search to see every binding."
                  : "No customer has connected a GitHub workflow identity yet. Until one does, their workflows authenticate with a static engine token instead, which is in the list above."}
              </EmptyList>
            }
            footer={
              <More
                shown={rows.length}
                noun={{ one: "binding", many: "bindings" }}
                hasMore={state.hasMore}
                busy={state.busy}
                error={state.moreError}
                onMore={state.more}
              />
            }
          />
        )}
      </Loaded>

      <Confirm
        open={revoking !== null}
        title={revoking ? `Revoke the binding for ${revoking.repository}?` : "Revoke"}
        phrase={revoking?.repository}
        confirmLabel="Revoke"
        busy={busy}
        error={error}
        onCancel={() => {
          setRevoking(null);
          setReason("");
          setError(null);
        }}
        onConfirm={() => void run()}
      >
        <p>
          <strong className="font-medium text-ink">What this does:</strong> no workflow in that
          repository can trade its identity for a token again, and the{" "}
          {revoking?.liveTokens ?? 0} token
          {(revoking?.liveTokens ?? 0) === 1 ? "" : "s"} it has already minted stop working on their
          next request.
        </p>
        <p>
          A revocation that left the issued tokens alive would not be a revocation, which is why
          both happen together.
        </p>
        <Field
          label="Reason"
          hint="Recorded in the platform audit chain and in the customer's own audit log. Required."
        >
          <input
            className={inputClass}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            maxLength={500}
            required
          />
        </Field>
      </Confirm>
    </Card>
  );
}
