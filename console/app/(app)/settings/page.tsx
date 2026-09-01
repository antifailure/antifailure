"use client";

import { useState } from "react";
import { mutate, query, useApi } from "@/lib/api";
import { useSessionContext } from "@/components/session";
import { may } from "@/lib/roles";
import {
  CloseAccount,
  Deleting,
  ExportCard,
  Fact,
  Said,
  Sessions,
  saidWrong,
  type Deletion,
} from "@/components/exits";
import {
  Button,
  Card,
  CardSkeleton,
  Field,
  Loaded,
  Page,
  When,
  inputClass,
} from "@/components/ui";

/* -------------------------------------------------------------------------
 * Shapes, matched to what the routes actually return.
 * ---------------------------------------------------------------------- */

interface Settings {
  slug: string;
  name: string;
  githubLogin: string | null;
  plan: string;
  createdAt: string;
  suspended: boolean;
  suspendedReason: string | null;
  counts: {
    members: number;
    repositories: number;
    environments: number;
    openInvitations: number;
  };
  deletion: Deletion | null;
  exportRetentionDays: number;
}

interface BillingContact {
  contact: { email: string; name: string | null; updatedBy: string; updatedAt: string } | null;
  onFileWithStripe: string | null;
  hasCustomer: boolean;
}

/* -------------------------------------------------------------------------
 * Small pieces
 * ---------------------------------------------------------------------- */

/* -------------------------------------------------------------------------
 * The organization
 * ---------------------------------------------------------------------- */

function Organization({
  settings,
  csrf,
  mayEdit,
  onSaved,
}: {
  settings: Settings;
  csrf: string;
  mayEdit: boolean;
  onSaved: () => void;
}) {
  const [name, setName] = useState(settings.name);
  const [busy, setBusy] = useState(false);
  const [said, setSaid] = useState<{ tone: "ok" | "bad"; text: string } | null>(null);

  const changed = name.trim() !== settings.name && name.trim().length > 0;

  return (
    <Card
      title="Organization"
      note={
        mayEdit
          ? "The name is what people see. The identifier is what links and commands use, and it does not change."
          : "Changing the name needs owner or admin."
      }
    >
      <div className="space-y-5 px-4 py-4">
        <dl className="grid grid-cols-2 gap-x-4 gap-y-4 sm:grid-cols-4">
          <Fact label="Identifier">
            <span className="font-mono text-[12px]">{settings.slug}</span>
          </Fact>
          <Fact label="Plan">{settings.plan}</Fact>
          <Fact label="People">{settings.counts.members}</Fact>
          <Fact label="Repositories">{settings.counts.repositories}</Fact>
        </dl>

        {settings.suspended ? (
          <div className="rounded-md border border-[rgba(138,90,0,0.3)] bg-[rgba(138,90,0,0.06)] px-3.5 py-3">
            <p className="text-[13px] font-medium text-ink">
              This organization cannot create anything new
            </p>
            <p className="mt-1 text-[12px] leading-5 text-muted">
              What is already running keeps running and can still be read.
            </p>
            {/* The reason on its own line rather than welded to the sentence
                above it. It is a value somebody typed, or the phrase a deletion
                writes, and neither ends in a full stop, so joining them read as
                "deletion requested What is already running". */}
            <p className="mt-1 text-[12px] leading-5 text-dim">
              Reason: {settings.suspendedReason ?? "none was recorded"}.
            </p>
          </div>
        ) : null}

        {mayEdit ? (
          <form
            className="flex flex-wrap items-end gap-3"
            onSubmit={async (e) => {
              e.preventDefault();
              setBusy(true);
              setSaid(null);
              try {
                await mutate("org.rename", { name: name.trim() }, csrf);
                setSaid({ tone: "ok", text: "Saved." });
                onSaved();
              } catch (err) {
                setSaid({ tone: "bad", text: saidWrong(err) });
              } finally {
                setBusy(false);
              }
            }}
          >
            <div className="w-full max-w-[320px]">
              <Field label="Name">
                <input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  maxLength={100}
                  className={inputClass}
                />
              </Field>
            </div>
            <Button type="submit" variant="primary" disabled={!changed} busy={busy}>
              Save
            </Button>
            {said ? <Said tone={said.tone}>{said.text}</Said> : null}
          </form>
        ) : (
          <Fact label="Name">{settings.name}</Fact>
        )}
      </div>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * The billing contact
 * ---------------------------------------------------------------------- */

function BillingContactCard({ csrf }: { csrf: string }) {
  const state = useApi<BillingContact>(() => query("org.billingContact"), []);
  const [email, setEmail] = useState<string | null>(null);
  const [name, setName] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [said, setSaid] = useState<{ tone: "ok" | "bad"; text: string } | null>(null);

  return (
    <Card
      title="Billing contact"
      note="Where invoices and payment notices go. This is not anybody's sign-in address."
    >
      <Loaded state={state} framed skeleton={<CardSkeleton count={1} />}>
        {(loaded) => {
          const current = email ?? loaded.contact?.email ?? "";
          const currentName = name ?? loaded.contact?.name ?? "";
          const disagrees =
            loaded.hasCustomer &&
            loaded.contact !== null &&
            loaded.onFileWithStripe !== null &&
            loaded.onFileWithStripe !== loaded.contact.email;

          return (
            <div className="space-y-4 px-4 py-4">
              {disagrees ? (
                <div className="rounded-md border border-[rgba(138,90,0,0.3)] bg-[rgba(138,90,0,0.06)] px-3.5 py-3">
                  <p className="text-[13px] font-medium text-ink">
                    Invoices are still going to a different address
                  </p>
                  <p className="mt-1 text-[12px] leading-5 text-muted">
                    Stripe has{" "}
                    <span className="font-mono text-[11.5px]">{loaded.onFileWithStripe}</span> on
                    file. Save the address again to send it across.
                  </p>
                </div>
              ) : null}

              <form
                className="flex flex-wrap items-end gap-3"
                onSubmit={async (e) => {
                  e.preventDefault();
                  setBusy(true);
                  setSaid(null);
                  try {
                    const saved = await mutate<{ pushedToStripe: boolean; note: string | null }>(
                      "org.setBillingContact",
                      { email: current.trim(), name: currentName.trim() || undefined },
                      csrf,
                    );
                    setSaid({
                      tone: "ok",
                      text: saved.pushedToStripe
                        ? "Saved, and sent to Stripe."
                        : (saved.note ?? "Saved."),
                    });
                    state.reload();
                  } catch (err) {
                    setSaid({ tone: "bad", text: saidWrong(err) });
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                <div className="w-full max-w-[320px]">
                  <Field label="Email address">
                    <input
                      type="email"
                      value={current}
                      onChange={(e) => setEmail(e.target.value)}
                      className={inputClass}
                      placeholder="accounts@yourcompany.com"
                    />
                  </Field>
                </div>
                <div className="w-full max-w-[240px]">
                  <Field label="Name (optional)">
                    <input
                      value={currentName}
                      onChange={(e) => setName(e.target.value)}
                      className={inputClass}
                      placeholder="Accounts payable"
                    />
                  </Field>
                </div>
                <Button
                  type="submit"
                  variant="primary"
                  disabled={current.trim().length < 3}
                  busy={busy}
                >
                  Save
                </Button>
              </form>

              {said ? <Said tone={said.tone}>{said.text}</Said> : null}
              {loaded.contact ? (
                <p className="text-[12px] leading-5 text-dim">
                  Last changed by {loaded.contact.updatedBy},{" "}
                  <When value={loaded.contact.updatedAt} />.
                </p>
              ) : null}
            </div>
          );
        }}
      </Loaded>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * The page
 * ---------------------------------------------------------------------- */

export default function SettingsPage() {
  const session = useSessionContext();
  const state = useApi<Settings>(() => query("org.settings"), []);
  const csrf = session.data?.csrfToken ?? "";
  const role = session.data?.role ?? null;

  const mayEditSettings = may(role, "organization.settings");
  const mayBill = may(role, "billing.manage");
  const maySessions = may(role, "sessions.manage");
  const mayExport = may(role, "data.export");
  const mayDelete = may(role, "organization.delete");

  return (
    <Page
      title="Settings"
      lede="Everything about this organization that is not about one repository: who can reach it, where the bills go, and how to leave."
    >
      <Loaded state={state} framed skeleton={<CardSkeleton count={3} />}>
        {(settings) => (
          <div className="space-y-6">
            <Organization
              settings={settings}
              csrf={csrf}
              mayEdit={mayEditSettings}
              onSaved={state.reload}
            />
            {mayBill ? <BillingContactCard csrf={csrf} /> : null}
            {maySessions ? <Sessions csrf={csrf} /> : null}
            {mayExport ? <ExportCard csrf={csrf} slug={settings.slug} /> : null}
            <Deleting
              org={settings}
              csrf={csrf}
              mayDelete={mayDelete}
              onChanged={state.reload}
            />
            <CloseAccount csrf={csrf} label={session.data?.label ?? ""} />
          </div>
        )}
      </Loaded>
    </Page>
  );
}
