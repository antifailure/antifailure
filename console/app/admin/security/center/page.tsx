"use client";

import { useState } from "react";
import Link from "next/link";
import { Card, Loaded, TableSkeleton, When } from "@/components/ui";
import {
  AdminPage,
  DataTable,
  EmptyList,
  FilterBar,
  MetricRow,
  StatusChip,
  type Column,
} from "@/components/admin/primitives";
import { More } from "@/components/pagination";
import {
  useCredentials,
  usePosture,
  useSso,
  type Credential,
  type CredentialFlag,
  type CredentialState,
  type SsoConnection,
} from "@/lib/admin-security";

/**
 * Security Center: the standing set of ways into this installation.
 *
 * THE QUESTION THIS PAGE ANSWERS. Not "are we secure", which is not a question
 * a page can answer, but "what can currently act against this installation,
 * who holds it, and which of those has nobody touched in a long time". Engine
 * tokens, provider keys and OIDC repository bindings are three tables and one
 * question: three separate lists would each look short and reassuring, and the
 * reader would have to remember the other two exist.
 *
 * WHAT IS DELIBERATELY NOT ON IT, because it does not exist. There is no
 * vulnerability table, no security finding table, no threat feed and no device
 * inventory anywhere in this schema, so there is no scan result, no risk score
 * and no alert count here. A number on a security page is read by somebody
 * deciding whether to escalate, and one that came from nowhere is worse than a
 * page that says the capability is not wired.
 *
 * EGRESS IS NOT DUPLICATED HERE EITHER. The firewall analysis on System Health
 * already answers which egress rules across every organization are configured
 * in a way that cannot do what they claim, including the rule nobody ever
 * approved. A second, shallower copy of that would disagree with it the first
 * time either changed, so this page links to it instead.
 */

/** What a flag means, in the words that say what to do about it. A word like
 *  "stale" on its own puts the reader in the position of guessing what the
 *  system meant, which on a security page is where mistakes get made. */
const FLAG_COPY: Record<CredentialFlag, string> = {
  never_used: "Never used",
  idle: "Not used recently",
  unrotated: "Never rotated",
};

const KINDS = [
  { value: "", label: "All credentials" },
  { value: "engine_token", label: "Engine tokens" },
  { value: "provider_key", label: "Provider keys" },
  { value: "oidc_binding", label: "OIDC bindings" },
];

const STATES = [
  { value: "flagged", label: "Worth a look" },
  { value: "live", label: "Live" },
  { value: "expired", label: "Expired" },
  { value: "revoked", label: "Revoked" },
];

const KIND_LABEL: Record<string, string> = {
  engine_token: "Engine token",
  provider_key: "Provider key",
  oidc_binding: "OIDC binding",
};

export default function SecurityCenterPage() {
  const posture = usePosture();

  return (
    <AdminPage
      href="/admin/security/center"
      lede="Every standing credential on this installation, how each organization signs in, and who holds an operator account. Egress rules are on System Health, which analyses them across every organization."
    >
      {/* framed, because this Loaded's content is cards rather than rows and
          its failure branch replaces the whole page. Without it the refusal a
          role without admin.security.read gets sits on the bare page
          background, which reads as a screen that half rendered rather than as
          an answer. The provider keys page shipped that way once. */}
      <Loaded state={posture} skeleton={<PostureSkeleton />} framed>
        {(data) => (
          <div className="grid gap-6">
            <section aria-labelledby="credentials-heading">
              <h2
                id="credentials-heading"
                className="mb-3 text-[13px] font-semibold tracking-extra-tight text-ink"
              >
                Standing credentials
              </h2>
              <MetricRow
                metrics={[
                  {
                    label: "Engine tokens",
                    value: data.engineTokens.live,
                    note: `${data.engineTokens.neverExpiring.toLocaleString()} never expire`,
                  },
                  {
                    label: "Provider keys",
                    value: data.providerKeys.live,
                    note: "Customer keys this installation holds sealed",
                  },
                  {
                    label: "OIDC bindings",
                    value: data.oidcBindings.live,
                    note: "Repositories trusted without a token",
                  },
                  {
                    label: "Worth a look",
                    value:
                      data.engineTokens.neverUsed +
                      data.engineTokens.idle +
                      data.oidcBindings.neverUsed +
                      data.oidcBindings.idle +
                      data.providerKeys.unrotated,
                    note: `Never used, unused for ${data.thresholds.staleDays} days, or never rotated`,
                  },
                ]}
              />
              <p className="mt-2.5 max-w-[72ch] text-[12px] leading-5 text-dim">
                An expired token is not counted as worth a look. Both authentication paths compare
                the expiry against the clock and refuse before they compare the hash, so an expired
                credential is already dead. There are {data.engineTokens.expired.toLocaleString()} of
                them, and they are tidying rather than exposure.
              </p>
            </section>

            <Credentials />

            <section aria-labelledby="identity-heading">
              <h2
                id="identity-heading"
                className="mb-3 text-[13px] font-semibold tracking-extra-tight text-ink"
              >
                How people sign in
              </h2>
              {/* A STATEMENT WHEN THERE IS NOTHING, AND THE REAL PANEL WHEN
                  THERE IS. Migration 0014 built a complete single sign-on and
                  SCIM schema and nothing in the product reads or writes any of
                  it: no route configures a connection and no sign-in path
                  consults one. So on every real installation these counts are
                  zero, and four zeroes over an empty table is the most
                  expensive thing this portal could ship, because an operator
                  reads it as an answer. The day something writes the table this
                  becomes the panel underneath with no edit here. The exemption
                  and its reason are recorded in web/packages/db/test/writers
                  .test.ts, which fails if either direction drifts. */}
              {data.sso.connections === 0 ? <SsoNotWired /> : <SsoConfigured data={data} />}
            </section>

            <section aria-labelledby="operators-heading">
              <h2
                id="operators-heading"
                className="mb-3 text-[13px] font-semibold tracking-extra-tight text-ink"
              >
                Who runs this installation
              </h2>
              <MetricRow
                metrics={[
                  { label: "Operator accounts", value: data.operators.total },
                  {
                    label: "Not provisioned",
                    value: data.operators.unprovisioned,
                    note: "No password has ever been set, so they cannot sign in",
                  },
                  { label: "Suspended", value: data.operators.suspended },
                  {
                    label: "Sessions open",
                    value: data.operators.liveSessions,
                    note: `${data.operators.impersonatingSessions.toLocaleString()} acting as a customer`,
                  },
                ]}
              />

              <Card
                className="mt-4"
                title="Sessions currently acting as a customer"
                note="An operator inside a customer's account, right now. The count above is not an answer to this; who, as whom, and why is."
              >
                {data.impersonations.length === 0 ? (
                  <EmptyList title="Nobody is impersonating anybody">
                    No operator session is currently acting as a customer. Starting one is itself an
                    operator action and is recorded in the log, so this fills the moment somebody
                    does.
                  </EmptyList>
                ) : (
                  <DataTable
                    columns={IMPERSONATION_COLUMNS}
                    rows={data.impersonations}
                    keyOf={(r) => r.id}
                    empty={null}
                  />
                )}
              </Card>
            </section>

            <Card
              title="The last severe entries in the operator log"
              note="High and critical only. Everything else is on Audit Logs."
              actions={
                <Link
                  href="/admin/security/audit"
                  className="inline-flex min-h-11 items-center text-[13px] text-ink underline decoration-[rgba(16,16,16,0.25)] underline-offset-4 hover:decoration-ink sm:min-h-0"
                >
                  Open the log
                </Link>
              }
            >
              {data.severeEvents.length === 0 ? (
                <EmptyList title="Nothing severe has happened">
                  No operator action has been recorded at high or critical severity. Suspensions,
                  revocations and operator role changes land here when they happen.
                </EmptyList>
              ) : (
                <DataTable
                  columns={SEVERE_COLUMNS}
                  rows={data.severeEvents}
                  keyOf={(r) => String(r.seq)}
                  empty={null}
                />
              )}
            </Card>
          </div>
        )}
      </Loaded>
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * The credential list
 * ---------------------------------------------------------------------- */

function Credentials() {
  // Opens on "worth a look" rather than on everything. The list of every
  // credential on the installation is a reference; the ones carrying a flag are
  // the reason somebody opened this page.
  const [kind, setKind] = useState("");
  const [state, setState] = useState<CredentialState>("flagged");
  const rows = useCredentials(kind, state);

  const columns: Column<Credential>[] = [
    {
      key: "label",
      header: "Credential",
      cell: (c) => (
        <>
          <span className="block truncate font-medium text-ink">{c.label}</span>
          <span className="block truncate font-mono text-[12px] text-muted">
            {KIND_LABEL[c.kind] ?? c.kind}
            {c.handle ? ` ${c.handle}` : ""}
          </span>
        </>
      ),
    },
    { key: "organization", header: "Organization", cell: (c) => c.organization },
    {
      key: "flags",
      header: "Finding",
      cell: (c) =>
        c.flags.length === 0 ? (
          <span className="text-dim">None</span>
        ) : (
          <span className="flex flex-wrap gap-1">
            {c.flags.map((f) => (
              <StatusChip key={f} value={FLAG_COPY[f]} tone="warn" />
            ))}
          </span>
        ),
    },
    {
      key: "lastUsed",
      header: "Last used",
      cell: (c) =>
        c.kind === "provider_key" ? (
          // Not unknown and not never: the table records no use at all for a
          // provider key, and printing a dash would let the reader infer the
          // key has not been used.
          <span className="text-dim">Not recorded</span>
        ) : c.lastUsedAt === null ? (
          <span className="text-muted">Never</span>
        ) : (
          <When value={c.lastUsedAt} />
        ),
    },
    { key: "createdAt", header: "Created", cell: (c) => <When value={c.createdAt} /> },
    {
      key: "createdBy",
      header: "Created by",
      cell: (c) => c.createdBy ?? <span className="text-dim">Not recorded</span>,
    },
  ];

  return (
    <Card>
      <FilterBar
        filters={[
          { label: "Kind", value: kind, onChange: setKind, options: KINDS },
          {
            label: "State",
            value: state,
            onChange: (next) => setState(next as CredentialState),
            options: STATES,
          },
        ]}
      />
      <Loaded state={rows} skeleton={<TableSkeleton rows={6} cols={6} />}>
        {(data) => (
          <DataTable
            columns={columns}
            rows={data}
            keyOf={(c) => c.id}
            empty={
              state === "flagged" ? (
                <EmptyList title="Nothing is worth a look">
                  Every live credential has been used inside the staleness window, and every
                  provider key has been rotated. Choose another state to see the full inventory.
                </EmptyList>
              ) : (
                // The copy names BOTH filters. "Nothing is in that state" is a
                // false claim when a kind filter is also on: there may be
                // plenty of revoked credentials and none of this kind, and a
                // reader who acts on the wider claim acts on nothing.
                <EmptyList
                  title={`No ${state} ${kind ? (KIND_LABEL[kind] ?? kind).toLowerCase() + "s" : "credentials"}`}
                >
                  {kind
                    ? "Nothing of that kind is in that state. Clear the kind filter to see the other two."
                    : "Nothing on this installation is in that state. Engine tokens are minted from the keys page, provider keys from an organization's settings, and OIDC bindings when a repository is trusted."}
                </EmptyList>
              )
            }
            footer={
              <More
                shown={data.length}
                noun={{ one: "credential", many: "credentials" }}
                hasMore={rows.hasMore}
                busy={rows.busy}
                error={rows.moreError}
                onMore={rows.more}
              />
            }
          />
        )}
      </Loaded>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * Single sign-on
 * ---------------------------------------------------------------------- */

/**
 * What "how people sign in" answers today.
 *
 * Every account on this installation arrives through GitHub or an email link,
 * because there is no other way in: the single sign-on tables exist and nothing
 * writes them. Saying that is a real answer to the question the section asks.
 * A row of zeroes over an empty table would be the same fact rendered as though
 * somebody had measured it and found nothing, which is what an operator would
 * act on.
 */
function SsoNotWired() {
  return (
    <Card title="Single sign-on is not wired">
      <div className="px-4 py-4">
        <p className="max-w-[72ch] text-[13px] leading-6 text-muted">
          Every account on this installation signs in through GitHub or an email link. The tables
          for single sign-on exist, with connections, break-glass codes and a replay guard for
          assertions, and nothing in the product reads or writes any of them: no route configures a
          connection and no sign-in path consults one. So no customer can turn it on, and there is
          nothing here to show rather than nothing found.
        </p>
        <p className="mt-3 max-w-[72ch] text-[13px] leading-6 text-muted">
          What is missing is the configuration route and the sign-in path, not a screen. This
          section fills in on its own the moment a connection is written, and shows which
          organizations enforce it and which have it enabled and bypassable.
        </p>
      </div>
    </Card>
  );
}

/** The panel this becomes once single sign-on exists. */
function SsoConfigured({ data }: { data: Posture }) {
  return (
    <>
      <MetricRow
        metrics={[
          { label: "SSO connections", value: data.sso.connections },
          {
            label: "Enabled",
            value: data.sso.enabled,
            note: "Configured completely enough to be used",
          },
          {
            label: "Not enforced",
            value: data.sso.bypassable,
            note: "Members can still sign in the old way",
          },
          {
            label: "Break-glass codes",
            value: data.sso.breakGlassOutstanding,
            note: `${data.sso.breakGlassUsed.toLocaleString()} ${data.sso.breakGlassUsed === 1 ? "has" : "have"} been used`,
          },
        ]}
      />
      <Sso />
    </>
  );
}

function Sso() {
  const rows = useSso();

  const columns: Column<SsoConnection>[] = [
    {
      key: "organization",
      header: "Organization",
      cell: (c) => (
        <>
          <span className="block truncate font-medium text-ink">{c.organization}</span>
          <span className="block truncate text-[12px] text-muted">{c.displayName}</span>
        </>
      ),
    },
    { key: "kind", header: "Kind", cell: (c) => c.kind.toUpperCase() },
    {
      key: "state",
      header: "State",
      cell: (c) =>
        !c.enabled ? (
          <StatusChip value="Not enabled" tone="neutral" />
        ) : c.enforced ? (
          <StatusChip value="Enforced" tone="pass" />
        ) : (
          // The state worth naming. The connection works and every member can
          // still sign in the old way, so the organization believes it has
          // single sign-on and does not have it.
          <StatusChip value="Bypassable" tone="warn" />
        ),
    },
    { key: "defaultRole", header: "Default role", cell: (c) => c.defaultRole },
    {
      key: "certificates",
      header: "Certificates",
      numeric: true,
      cell: (c) => (c.kind === "saml" ? c.certificates.toLocaleString() : "--"),
    },
    {
      key: "breakGlass",
      header: "Break-glass",
      numeric: true,
      cell: (c) => `${c.breakGlassOutstanding.toLocaleString()} of ${(c.breakGlassOutstanding + c.breakGlassUsed).toLocaleString()}`,
    },
    { key: "updatedAt", header: "Changed", cell: (c) => <When value={c.updatedAt} /> },
  ];

  return (
    <Card className="mt-4">
      <Loaded state={rows} skeleton={<TableSkeleton rows={4} cols={6} />}>
        {(data) => (
          <DataTable
            columns={columns}
            rows={data}
            keyOf={(c) => c.id}
            empty={
              <EmptyList title="No organization has configured single sign-on">
                Every account on this installation signs in through GitHub or an email link. A
                customer configures single sign-on from their own settings; there is no operator
                route that configures one for them.
              </EmptyList>
            }
            footer={
              <More
                shown={data.length}
                noun={{ one: "connection", many: "connections" }}
                hasMore={rows.hasMore}
                busy={rows.busy}
                error={rows.moreError}
                onMore={rows.more}
              />
            }
          />
        )}
      </Loaded>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * Columns for the two lists the posture read already carries
 * ---------------------------------------------------------------------- */

const IMPERSONATION_COLUMNS: Column<Posture["impersonations"][number]>[] = [
  {
    key: "operator",
    header: "Operator",
    cell: (r) => <span className="font-medium text-ink">{r.operator}</span>,
  },
  {
    key: "actingAs",
    header: "Acting as",
    cell: (r) => r.actingAs ?? <span className="text-dim">Account deleted</span>,
  },
  {
    key: "reason",
    header: "Reason",
    // A blank reason is impossible: a database constraint refuses an
    // impersonation without one, which is why this cell never has to explain an
    // empty value.
    cell: (r) => <span className="break-words">{r.reason}</span>,
  },
  { key: "startedAt", header: "Started", cell: (r) => <When value={r.startedAt} /> },
  { key: "expiresAt", header: "Ends", cell: (r) => <When value={r.expiresAt} /> },
];

const SEVERE_COLUMNS: Column<Posture["severeEvents"][number]>[] = [
  {
    key: "action",
    header: "Action",
    cell: (r) => <span className="font-medium text-ink">{r.action}</span>,
  },
  { key: "actor", header: "Operator", cell: (r) => r.actor },
  {
    key: "organization",
    header: "Organization",
    cell: (r) => r.organization ?? <span className="text-muted">Platform-wide</span>,
  },
  {
    key: "severity",
    header: "Severity",
    cell: (r) => <StatusChip value={r.severity} tone="fail" />,
  },
  { key: "seq", header: "Seq", numeric: true, cell: (r) => r.seq.toLocaleString() },
  { key: "when", header: "When", cell: (r) => <When value={r.occurredAt} /> },
];

/** The wait, shaped like what is coming: three bands of numbers and a table. A
 *  spinner in this space would say nothing about the size of the answer, and
 *  the layout would jump when it landed. */
function PostureSkeleton() {
  return (
    <div className="grid gap-6">
      <MetricRow
        metrics={[
          { label: "Engine tokens", value: null },
          { label: "Provider keys", value: null },
          { label: "OIDC bindings", value: null },
          { label: "Worth a look", value: null },
        ]}
      />
      <Card>
        <TableSkeleton rows={6} cols={6} />
      </Card>
    </div>
  );
}

type Posture = NonNullable<ReturnType<typeof usePosture>["data"]>;
