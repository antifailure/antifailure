"use client";

import { Suspense, useState } from "react";
import { WithRepository } from "@/components/RepositoryPicker";
import { ago, when } from "@/lib/format";
import { mutate, query, useApi } from "@/lib/api";
import { may } from "@/lib/roles";
import { useSessionContext } from "@/components/session";
import {
  Badge,
  Button,
  Card,
  Empty,
  Field,
  Loaded,
  Page,
  Row,
  Table,
  TableSkeleton,
  TableWrap,
  Td,
  Th,
  inputClass,
} from "@/components/ui";

interface Rule {
  host: string;
  mode: string;
  paths?: string[];
  methods?: string[];
  note?: string;
}

interface Effective {
  default: string;
  rules: Rule[];
  hosts: string[];
}

interface Pending {
  id: string;
  host: string;
  mode: string;
  paths: string[] | null;
  methods: string[] | null;
  note: string | null;
  created_at: string;
  repository: string | null;
  proposed_by: string | null;
}

interface Decision {
  host: string | null;
  mode: string | null;
  requests: string;
}

interface Explanation {
  decision: { mode: string; rule?: unknown; reason?: string } | string;
  chain: { rule: Rule; index: number; specificity: number; why: string }[];
  inspectsHost: boolean;
}

function modeTone(mode: string | null | undefined) {
  const m = (mode ?? "").toLowerCase();
  if (m === "allow") return "pass" as const;
  if (m === "block") return "fail" as const;
  if (m === "capture" || m === "mock" || m === "synth" || m === "sandbox") return "warn" as const;
  return "neutral" as const;
}

/**
 * Ask the policy about one request.
 *
 * The value of this is not the answer, it is the chain: which rules matched,
 * in the order that decides. A policy view that shows only rules makes a
 * reader simulate the engine in their head, which is the step where people
 * get egress wrong.
 */
interface Asked {
  host: string;
  method: string;
  path: string;
}

/**
 * The answer, in its own component.
 *
 * It was a hook in the form with the query short-circuited to
 * `Promise.resolve(null)` until something had been asked, which put the state
 * machine in a position it should never have been able to reach: status
 * "ready" with no data. On the first render after a question was submitted,
 * before the effect ran, the previous "ready" was still current and the render
 * read `.inspectsHost` off null. The page crashed to Next's client-exception
 * screen the first time anybody pressed the button.
 *
 * Mounting the query with the question means there is no such render: the
 * component does not exist until there is something to fetch, and it starts in
 * "loading" like everything else. `Loaded` also refuses to hand a null to its
 * children now, so a future version of this mistake shows a skeleton rather
 * than a white screen.
 */
function Answer({ repository, asked }: { repository: string; asked: Asked }) {
  const state = useApi<Explanation>(
    () =>
      query("network.explain", {
        repository,
        host: asked.host,
        method: asked.method,
        path: asked.path,
      }),
    [asked.host, asked.method, asked.path, repository],
  );

  const decisionMode =
    typeof state.data?.decision === "string"
      ? state.data.decision
      : ((state.data?.decision as { mode?: string })?.mode ?? "");

  return (
    <div className="border-t border-rule">
      <Loaded state={state} skeleton={<TableSkeleton rows={2} cols={3} />}>
        {(data) => (
          <div className="px-4 py-4">
            <p className="flex flex-wrap items-center gap-2 text-[13px] text-ink">
              <span className="font-mono">
                {asked.method} {asked.host}
                {asked.path}
              </span>
              <Badge tone={modeTone(decisionMode)}>{decisionMode || "no decision"}</Badge>
              {data.inspectsHost ? <Badge tone="neutral">TLS inspected</Badge> : null}
            </p>
            {data.chain.length === 0 ? (
              <p className="mt-3 text-[13px] leading-6 text-muted">
                No rule matched, so the default applies.
              </p>
            ) : (
              <ol className="mt-3 space-y-2">
                {data.chain.map((m, i) => (
                  <li key={i} className="text-[13px] text-muted">
                    <span className="font-mono text-ink">{m.rule.host}</span>{" "}
                    <Badge tone={modeTone(m.rule.mode)}>{m.rule.mode}</Badge>{" "}
                    <span className="text-dim">{m.why}</span>
                  </li>
                ))}
              </ol>
            )}
          </div>
        )}
      </Loaded>
    </div>
  );
}

const MODES = ["block", "allow", "capture", "mock", "sandbox", "synth"] as const;

/**
 * Proposing a rule, in the same card as the queue it lands in.
 *
 * Separate from the queue's own state on purpose: a form that clears itself on
 * every reload of the list beside it loses whatever somebody was half way
 * through typing when the poll came back.
 */
function Propose({
  repository,
  csrf,
  onProposed,
}: {
  repository: string;
  csrf: string;
  onProposed: () => void;
}) {
  const [host, setHost] = useState("");
  const [mode, setMode] = useState<string>("allow");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  return (
    <form
      className="border-b border-rule px-4 py-4"
      onSubmit={async (e) => {
        e.preventDefault();
        if (!host.trim()) return;
        setBusy(true);
        setError(null);
        try {
          await mutate(
            "network.propose",
            { repository, host: host.trim(), mode, note: note.trim() || undefined },
            csrf,
          );
          setHost("");
          setNote("");
          onProposed();
        } catch (err) {
          setError(err instanceof Error ? err.message : "That did not work.");
        } finally {
          setBusy(false);
        }
      }}
    >
      {/* The hint and the error live under the row rather than inside a
          Field, because a hint inside one grid cell makes that cell taller and
          items-end then lifts its input clear of the others. */}
      <div className="grid gap-3 sm:grid-cols-[2fr_1fr_2fr_auto] sm:items-end">
        <Field label="Host">
          <input
            className={inputClass}
            value={host}
            onChange={(e) => setHost(e.target.value)}
            placeholder="api.stripe.com"
            required
          />
        </Field>
        <Field label="Mode">
          <select className={inputClass} value={mode} onChange={(e) => setMode(e.target.value)}>
            {MODES.map((m) => (
              <option key={m}>{m}</option>
            ))}
          </select>
        </Field>
        <Field label="Why">
          <input className={inputClass} value={note} onChange={(e) => setNote(e.target.value)} />
        </Field>
        <Button type="submit" busy={busy}>
          {busy ? "Proposing" : "Propose"}
        </Button>
      </div>
      {error ? (
        <p role="alert" className="mt-2.5 text-[12px] leading-5 text-fail">
          {error}
        </p>
      ) : (
        <p className="mt-2.5 text-[12px] leading-5 text-dim">
          A proposed rule is inert. It enforces nothing until somebody approves it.
        </p>
      )}
    </form>
  );
}

/**
 * The approval queue.
 *
 * This is the half of the policy centre that did not exist. A member can
 * propose an egress change and cannot approve one, which is the point: egress
 * and masking are the two settings where a mistake is a data incident. Before
 * this there was no column to hold the difference and no route to approve
 * through, so a proposal was policy the moment it was written and the queue was
 * a page nobody could act on.
 *
 * The rules here are NOT in the effective policy above. That is the whole
 * distinction the screen exists to draw, so it is said in words rather than
 * left to the reader to infer from two tables.
 */
function Queue({
  repository,
  onApproved,
}: {
  repository: string;
  onApproved: () => void;
}) {
  const session = useSessionContext();
  const state = useApi<Pending[]>(() => query("network.pending", { repository }), [repository]);
  const csrf = session.data?.csrfToken ?? "";
  const mayApprove = may(session.data?.role, "network.approve");
  const mayPropose = may(session.data?.role, "network.edit");
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function approve(rule: Pending) {
    setBusy(rule.id);
    setError(null);
    try {
      await mutate("network.approve", { ruleId: rule.id }, csrf);
      state.reload();
      onApproved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "That did not work.");
    } finally {
      setBusy(null);
    }
  }

  return (
    <Card
      title="Waiting for approval"
      note={
        mayApprove
          ? "Proposed and not yet in force. None of these is being enforced anywhere until it is approved."
          : "Proposed and not yet in force. Approving an egress change needs owner or admin."
      }
      actions={
        error ? (
          <span role="alert" className="min-w-0 max-w-[46ch] break-words text-left text-[12px] leading-4 text-fail sm:text-right">
            {error}
          </span>
        ) : null
      }
    >
      {mayPropose ? (
        <Propose repository={repository} csrf={csrf} onProposed={state.reload} />
      ) : null}

      <Loaded state={state} skeleton={<TableSkeleton rows={2} cols={4} />}>
        {(rows) =>
          rows.length === 0 ? (
            <Empty title="Nothing waiting">
              A proposed rule sits here until somebody approves it. An empty
              queue means the effective policy above is the whole of it.
            </Empty>
          ) : (
            <TableWrap>
              <Table>
                <thead>
                  <tr>
                    <Th>Host</Th>
                    <Th>Mode</Th>
                    <Th>Scope</Th>
                    <Th>Proposed by</Th>
                    <Th>When</Th>
                    {mayApprove ? <Th>Approve</Th> : null}
                  </tr>
                </thead>
                <tbody>
                  {rows.map((r) => (
                    <Row key={r.id}>
                      <Td mono>{r.host}</Td>
                      <Td>
                        <Badge tone={modeTone(r.mode)}>{r.mode}</Badge>
                      </Td>
                      <Td>{r.repository ?? "the whole organization"}</Td>
                      <Td>{r.proposed_by ?? "unknown"}</Td>
                      <Td>
                        <span title={when(r.created_at)}>{ago(r.created_at)}</span>
                      </Td>
                      {mayApprove ? (
                        <Td>
                          <Button
                            variant="primary"
                            busy={busy === r.id}
                            onClick={() => approve(r)}
                          >
                            {busy === r.id ? "Approving" : "Approve"}
                          </Button>
                        </Td>
                      ) : null}
                    </Row>
                  ))}
                </tbody>
              </Table>
            </TableWrap>
          )
        }
      </Loaded>
    </Card>
  );
}

function Explain({ repository }: { repository: string }) {
  const [host, setHost] = useState("");
  const [method, setMethod] = useState("GET");
  const [path, setPath] = useState("/");
  const [asked, setAsked] = useState<Asked | null>(null);

  return (
    <Card title="Explain a request" note="What the policy would do, and which rules got it there.">
      <form
        className="grid gap-3 px-4 py-4 sm:grid-cols-[2fr_1fr_2fr_auto] sm:items-end"
        onSubmit={(e) => {
          e.preventDefault();
          if (host.trim()) setAsked({ host: host.trim(), method, path: path || "/" });
        }}
      >
        <Field label="Host">
          <input
            className={inputClass}
            value={host}
            onChange={(e) => setHost(e.target.value)}
            placeholder="api.stripe.com"
            required
          />
        </Field>
        <Field label="Method">
          <select className={inputClass} value={method} onChange={(e) => setMethod(e.target.value)}>
            {["GET", "POST", "PUT", "PATCH", "DELETE"].map((m) => (
              <option key={m}>{m}</option>
            ))}
          </select>
        </Field>
        <Field label="Path">
          <input className={inputClass} value={path} onChange={(e) => setPath(e.target.value)} />
        </Field>
        <Button type="submit" variant="primary">
          Explain
        </Button>
      </form>

      {asked ? <Answer repository={repository} asked={asked} /> : null}
    </Card>
  );
}

function Network({ repository }: { repository: string }) {
  const effective = useApi<Effective>(() => query("network.effective", { repository }), [repository]);
  const decisions = useApi<Decision[]>(() => query("network.decisions", { limit: 100 }), []);

  return (
    <div className="space-y-6">
      <Loaded state={effective} skeleton={<TableSkeleton rows={5} cols={4} />}>
        {(policy) => (
          <Card
            title="Effective policy"
            note={
              <>
                In evaluation order. Anything that matches no rule falls to the
                default, which is <Badge tone={modeTone(policy.default)}>{policy.default}</Badge>
              </>
            }
          >
            {policy.rules.length === 0 ? (
              <Empty title="No rules">
                Every destination falls to the default. That is a working
                policy, not a missing one.
              </Empty>
            ) : (
              <TableWrap>
                <Table>
                  <thead>
                    <tr>
                      <Th numeric>#</Th>
                      <Th>Host</Th>
                      <Th>Mode</Th>
                      <Th>Paths</Th>
                      <Th>Methods</Th>
                      <Th>Note</Th>
                    </tr>
                  </thead>
                  <tbody>
                    {policy.rules.map((r, i) => (
                      <Row key={`${r.host}-${i}`}>
                        <Td numeric>{i + 1}</Td>
                        <Td mono>{r.host}</Td>
                        <Td>
                          <Badge tone={modeTone(r.mode)}>{r.mode}</Badge>
                        </Td>
                        <Td mono>{r.paths?.length ? r.paths.join(", ") : "any"}</Td>
                        <Td mono>{r.methods?.length ? r.methods.join(", ") : "any"}</Td>
                        <Td className="max-w-[30ch]">{r.note ?? "--"}</Td>
                      </Row>
                    ))}
                  </tbody>
                </Table>
              </TableWrap>
            )}
          </Card>
        )}
      </Loaded>

      <Queue repository={repository} onApproved={effective.reload} />

      <Explain repository={repository} />

      <Card
        title="Decisions"
        note="What the proxy actually did, counted from the events it emitted. Across every environment in this organization."
      >
        <Loaded state={decisions} skeleton={<TableSkeleton rows={5} cols={3} />}>
          {(rows) =>
            rows.length === 0 ? (
              <Empty title="Nothing has been decided yet">
                The proxy records a decision per request. None here means no
                environment has made an outbound call.
              </Empty>
            ) : (
              <TableWrap>
                <Table>
                  <thead>
                    <tr>
                      <Th>Host</Th>
                      <Th>Mode</Th>
                      <Th numeric>Requests</Th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((d, i) => (
                      <Row key={`${d.host}-${d.mode}-${i}`}>
                        <Td mono>{d.host ?? "unknown"}</Td>
                        <Td>
                          <Badge tone={modeTone(d.mode)}>{d.mode ?? "unknown"}</Badge>
                        </Td>
                        <Td numeric>{Number(d.requests).toLocaleString()}</Td>
                      </Row>
                    ))}
                  </tbody>
                </Table>
              </TableWrap>
            )
          }
        </Loaded>
      </Card>
    </div>
  );
}

export default function NetworkPage() {
  return (
    <Suspense fallback={<Page title="Network"><TableSkeleton /></Page>}>
      <Page
        title="Network"
        lede="The egress policy in the order it evaluates, a way to ask it about one request, and what the proxy has actually decided."
      >
        <WithRepository>{(repository) => <Network repository={repository} />}</WithRepository>
      </Page>
    </Suspense>
  );
}
