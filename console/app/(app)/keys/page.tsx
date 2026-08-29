"use client";

import { useState } from "react";
import { ago, usd, when } from "@/lib/format";
import { rest, useApi } from "@/lib/api";
import { useSessionContext } from "@/components/session";
import {
  Badge,
  Button,
  Card,
  Field,
  Loaded,
  Page,
  Table,
  TableSkeleton,
  TableWrap,
  Td,
  Th,
  inputClass,
} from "@/components/ui";

interface StoredKey {
  provider: string;
  last4: string;
  fingerprint: string;
  createdAt: string;
  rotatedAt: string | null;
}

interface Budget {
  provider: string;
  period: string;
  capUsd: number;
  spentUsd: number;
  remainingUsd: number;
}

interface Providers {
  sealing: boolean;
  mayManage: boolean;
  role: string | null;
  keys: StoredKey[];
  budgets: Budget[];
}

const PROVIDERS = [
  { id: "anthropic", label: "Anthropic" },
  { id: "openai", label: "OpenAI" },
];

type Notice = { tone: "ok" | "bad" | "warn"; title: string; body: string } | null;

function NoticeBar({ notice }: { notice: Notice }) {
  if (!notice) return null;
  const tone =
    notice.tone === "ok"
      ? "border-[rgba(30,122,58,0.3)] bg-[rgba(30,122,58,0.06)]"
      : notice.tone === "bad"
        ? "border-[rgba(179,38,30,0.3)] bg-[rgba(179,38,30,0.06)]"
        : "border-[rgba(138,90,0,0.3)] bg-[rgba(138,90,0,0.06)]";
  return (
    <div
      role="status"
      className={`mb-5 rounded-[6px] border px-4 py-3 ${tone}`}
    >
      <p className="text-[13px] font-medium text-ink">{notice.title}</p>
      <p className="mt-1 text-[12.5px] leading-5 text-muted">{notice.body}</p>
    </div>
  );
}

function ProviderCard({
  provider,
  label,
  data,
  csrf,
  onDone,
}: {
  provider: string;
  label: string;
  data: Providers;
  csrf: string;
  onDone: (notice: Notice) => void;
}) {
  const stored = data.keys.find((k) => k.provider === provider) ?? null;
  const budget = data.budgets.find((b) => b.provider === provider) ?? null;
  const [key, setKey] = useState("");
  const [cap, setCap] = useState(budget ? String(budget.capUsd) : "");
  const [busy, setBusy] = useState<"key" | "cap" | "revoke" | null>(null);
  const [capError, setCapError] = useState<string | null>(null);

  async function run(what: "key" | "cap" | "revoke", fn: () => Promise<Notice>) {
    setBusy(what);
    try {
      onDone(await fn());
    } catch (e) {
      onDone({
        tone: "bad",
        title: "That did not work",
        body: e instanceof Error ? e.message : "The control plane refused it.",
      });
    } finally {
      setBusy(null);
    }
  }

  return (
    <Card
      title={label}
      note={
        stored ? (
          <>
            Stored, ending <span className="font-mono">{stored.last4}</span>
            {stored.rotatedAt ? ` · rotated ${ago(stored.rotatedAt)}` : ` · added ${ago(stored.createdAt)}`}
          </>
        ) : (
          "No key stored. Runs on this provider cannot spend."
        )
      }
      actions={<Badge tone={stored ? "pass" : "neutral"}>{stored ? "set" : "not set"}</Badge>}
    >
      <div className="space-y-5 px-4 py-4">
        <div>
          <p className="text-[12px] uppercase tracking-[0.08em] text-dim">This month</p>
          {budget ? (
            <>
              <p className="mt-1.5 text-[13px] text-ink">
                <span className="tnum">{usd(budget.spentUsd)}</span> spent of{" "}
                <span className="tnum">{usd(budget.capUsd)}</span>
                <span className="text-muted"> · {usd(budget.remainingUsd)} left</span>
              </p>
              <div
                className="mt-2 h-1.5 w-full max-w-[420px] overflow-hidden rounded-full bg-[rgba(16,16,16,0.08)]"
                role="img"
                aria-label={`${usd(budget.spentUsd)} of ${usd(budget.capUsd)} spent`}
              >
                <div
                  className="h-full rounded-full bg-ink"
                  style={{
                    width: `${budget.capUsd > 0 ? Math.min(100, (budget.spentUsd / budget.capUsd) * 100) : 0}%`,
                  }}
                />
              </div>
            </>
          ) : (
            <p className="mt-1.5 max-w-[62ch] text-[13px] leading-6 text-muted">
              No cap is set, and a missing cap reads as zero rather than as
              unlimited. Nothing can be spent on this provider until a cap
              exists.
            </p>
          )}
        </div>

        {data.mayManage ? (
          <>
            <form
              onSubmit={(e) => {
                e.preventDefault();
                const typed = key.trim();
                if (!typed) {
                  onDone({
                    tone: "bad",
                    title: "No key was given",
                    body: "Paste the key into the field before saving.",
                  });
                  return;
                }
                run("key", async () => {
                  const out = await rest<{ last4: string; replaced: boolean; sameAsBefore: boolean }>(
                    `/console/api/providers/${provider}`,
                    { method: "PUT", body: { key: typed }, csrf },
                  );
                  setKey("");
                  return out.sameAsBefore
                    ? {
                        tone: "warn",
                        title: "That is the key that was already stored",
                        body: "Nothing changed. To rotate, create a new key at the provider first.",
                      }
                    : {
                        tone: "ok",
                        title: out.replaced ? `The ${label} key is rotated` : `The ${label} key is stored`,
                        body: `It ends ${out.last4}. It will not be shown again.`,
                      };
                });
              }}
            >
              <div className="flex flex-wrap items-end gap-3">
                <div className="min-w-[260px] flex-1">
                  <Field label={stored ? "Replace the key" : "Store a key"}>
                    <input
                      type="password"
                      className={inputClass}
                      value={key}
                      autoComplete="off"
                      spellCheck={false}
                      placeholder={provider === "anthropic" ? "sk-ant-..." : "sk-..."}
                      onChange={(e) => setKey(e.target.value)}
                    />
                  </Field>
                </div>
                <Button type="submit" variant="primary" busy={busy === "key"} disabled={!data.sealing}>
                  {stored ? "Rotate" : "Store"}
                </Button>
                {stored ? (
                <Button
                  variant="danger"
                  busy={busy === "revoke"}
                  onClick={() =>
                    run("revoke", async () => {
                      const out = await rest<{ revoked: boolean }>(
                        `/console/api/providers/${provider}`,
                        { method: "DELETE", csrf },
                      );
                      return {
                        tone: out.revoked ? "ok" : "warn",
                        title: out.revoked ? `The ${label} key is removed` : "There was nothing to remove",
                        body: out.revoked
                          ? "It cannot be used from here again. Revoke it at the provider too, because this does not reach them."
                          : `No ${label} key was stored.`,
                      };
                    })
                  }
                >
                  Remove
                </Button>
                ) : null}
              </div>
              <p className="mt-2 max-w-[62ch] text-[12px] leading-5 text-dim">
                Sent once, sealed with a secret held outside the database, and
                never returned by any route.
              </p>
            </form>

            <form
              onSubmit={(e) => {
                e.preventDefault();
                // Not Number(cap): Number('') is 0, so an empty field would
                // set a cap of zero dollars rather than complain, and nothing
                // would look wrong until every run refused as overspent.
                const typed = cap.trim();
                const value = typed === "" ? NaN : Number(typed);
                if (!Number.isFinite(value) || value < 0) {
                  setCapError("Give a number of US dollars, zero or more.");
                  return;
                }
                setCapError(null);
                run("cap", async () => {
                  const out = await rest<Budget>(`/console/api/providers/${provider}/budget`, {
                    method: "PUT",
                    body: { capUsd: value },
                    csrf,
                  });
                  return {
                    tone: "ok",
                    title: `The ${label} cap is now ${usd(out.capUsd)} a month`,
                    body: `${usd(out.spentUsd)} has been spent so far this month.`,
                  };
                });
              }}
            >
              <div className="flex flex-wrap items-end gap-3">
                <div className="w-[220px]">
                  <Field label="Monthly cap, USD" error={capError}>
                    <input
                      className={inputClass}
                      inputMode="decimal"
                      value={cap}
                      placeholder="25"
                      onChange={(e) => {
                        setCap(e.target.value);
                        setCapError(null);
                      }}
                    />
                  </Field>
                </div>
                <Button type="submit" busy={busy === "cap"}>
                  Set the cap
                </Button>
              </div>
              <p className="mt-2 max-w-[62ch] text-[12px] leading-5 text-dim">
                Checked before the key is decrypted, so an exhausted budget
                never touches the key.
              </p>
            </form>
          </>
        ) : (
          <p className="max-w-[62ch] text-[13px] leading-6 text-muted">
            You are {data.role ?? "not a member"} in this organization, so you
            can see which keys are set and cannot change them.
          </p>
        )}

        {stored ? (
          <TableWrap>
            <Table>
              <thead>
                <tr>
                  <Th>Fingerprint</Th>
                  <Th>Added</Th>
                  <Th>Rotated</Th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <Td mono className="max-w-[24ch] truncate">
                    {stored.fingerprint}
                  </Td>
                  <Td>{when(stored.createdAt)}</Td>
                  <Td>{stored.rotatedAt ? when(stored.rotatedAt) : "never"}</Td>
                </tr>
              </tbody>
            </Table>
          </TableWrap>
        ) : null}
      </div>
    </Card>
  );
}

function Keys() {
  const session = useSessionContext();
  const state = useApi<Providers>(() => rest<Providers>("/console/api/providers"), []);
  const [notice, setNotice] = useState<Notice>(null);
  const csrf = session.data?.csrfToken ?? "";

  return (
    <Page
      title="Provider keys"
      lede="Your own Anthropic and OpenAI keys, sealed at rest and capped per month. No route in this product returns a stored key, including this page."
    >
      <NoticeBar notice={notice} />
      <Loaded state={state} skeleton={<TableSkeleton rows={4} cols={3} />}>
        {(data) => (
          <div className="space-y-6">
            {!data.sealing ? (
              <div className="rounded-[6px] border border-[rgba(138,90,0,0.3)] bg-[rgba(138,90,0,0.06)] px-4 py-3">
                <p className="text-[13px] font-medium text-ink">
                  This installation cannot store a key
                </p>
                <p className="mt-1 max-w-[70ch] text-[12.5px] leading-5 text-muted">
                  AF_PROVIDER_KEY_SECRET is not set, so there is nothing to seal
                  a key with. Storing one in the clear is refused rather than
                  done quietly.
                </p>
              </div>
            ) : null}
            {PROVIDERS.map((p) => (
              <ProviderCard
                key={p.id}
                provider={p.id}
                label={p.label}
                data={data}
                csrf={csrf}
                onDone={(n) => {
                  setNotice(n);
                  state.reload();
                }}
              />
            ))}
          </div>
        )}
      </Loaded>
    </Page>
  );
}

export default function KeysPage() {
  return <Keys />;
}
