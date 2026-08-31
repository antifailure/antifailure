"use client";

import { Suspense, useState } from "react";
import { WithRepository } from "@/components/RepositoryPicker";
import { query, useApi } from "@/lib/api";
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
  selectClass,
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
          <select className={`mt-1.5 w-full ${selectClass}`} value={method} onChange={(e) => setMethod(e.target.value)}>
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
      <Card
        title="Effective policy"
        note={
          effective.status === "ready" && effective.data ? (
            <>
              In evaluation order. Anything that matches no rule falls to the
              default, which is{" "}
              <Badge tone={modeTone(effective.data.default)}>{effective.data.default}</Badge>
            </>
          ) : (
            "In evaluation order. Anything that matches no rule falls to the default."
          )
        }
      >
        <Loaded state={effective} skeleton={<TableSkeleton rows={5} cols={6} />}>
          {(policy) =>
            policy.rules.length === 0 ? (
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
                        <Td label="Order" numeric>{i + 1}</Td>
                        <Td mono>{r.host}</Td>
                        <Td label="Mode">
                          <Badge tone={modeTone(r.mode)}>{r.mode}</Badge>
                        </Td>
                        <Td label="Paths" mono>{r.paths?.length ? r.paths.join(", ") : "any"}</Td>
                        <Td label="Methods" mono>{r.methods?.length ? r.methods.join(", ") : "any"}</Td>
                        <Td label="Note" className="max-w-[30ch]">{r.note ?? "--"}</Td>
                      </Row>
                    ))}
                  </tbody>
                </Table>
              </TableWrap>
            )
          }
        </Loaded>
      </Card>

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
                        <Td label="Mode">
                          <Badge tone={modeTone(d.mode)}>{d.mode ?? "unknown"}</Badge>
                        </Td>
                        <Td label="Requests" numeric>{Number(d.requests).toLocaleString()}</Td>
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
    <Suspense
      fallback={
        <Page title="Network">
          <Card title="Effective policy">
            <TableSkeleton rows={5} cols={6} />
          </Card>
        </Page>
      }
    >
      <Page
        title="Network"
        lede="The egress policy in the order it evaluates, a way to ask it about one request, and what the proxy has actually decided."
      >
        <WithRepository>{(repository) => <Network repository={repository} />}</WithRepository>
      </Page>
    </Suspense>
  );
}
