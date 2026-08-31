"use client";

import { useState } from "react";
import { mutate, query, useApi } from "@/lib/api";
import { may } from "@/lib/roles";
import { useSessionContext } from "@/components/session";
import {
  Badge,
  Button,
  Card,
  Empty,
  Loaded,
  Page,
  Row,
  Table,
  TableSkeleton,
  TableWrap,
  Td,
  Th,
} from "@/components/ui";

interface Verdict {
  allowed: boolean;
  current: number;
  limit: number;
  reason: string;
}

interface Plan {
  name: string;
  current: boolean;
  quota: { environments: number; goldens: number; artifactGigabytes: number };
  environments: Verdict;
  goldens: Verdict;
}

interface PlanState {
  plan: string;
  plans: Plan[];
  holding: { environments: number; goldens: number };
  takesPayment: boolean;
}

/**
 * The plan, and the quota it decides.
 *
 * This page changes the plan and takes no money. There is no card on file, no
 * subscription and no invoice behind it, and the page says so rather than
 * implying a payment flow that does not exist. What it is for is that the quota
 * enforcement has worked for a long time against a plan nothing could change,
 * so an organization was on whatever it was seeded as forever.
 *
 * Every plan is shown with what this organization is holding against it, not
 * just the current one. A change control that shows only where you are makes
 * somebody guess whether the plan they are about to choose is smaller than what
 * they already have, and the answer to that decides whether their next
 * environment is refused.
 */
function Billing() {
  const session = useSessionContext();
  const state = useApi<PlanState>(() => query("billing.get"), []);
  const csrf = session.data?.csrfToken ?? "";
  const mayManage = may(session.data?.role, "billing.manage");
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  if (!mayManage) {
    return (
      <Card title="Plan">
        <Empty title="Your role cannot see this">
          The plan decides this organization&rsquo;s quotas, and changing it needs
          the billing.manage permission, which only an owner holds.
        </Empty>
      </Card>
    );
  }

  async function choose(plan: string) {
    setBusy(plan);
    setError(null);
    try {
      await mutate("billing.set", { plan }, csrf);
      state.reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : "That did not work.");
    } finally {
      setBusy(null);
    }
  }

  return (
    <Loaded state={state} skeleton={<TableSkeleton rows={3} cols={4} />}>
      {(data) => (
        <div className="space-y-6">
          <Card
            title="Plan"
            note={
              data.takesPayment
                ? undefined
                : "Changing the plan changes the quotas. It does not take payment: there is no card, no subscription and no invoice behind this."
            }
            actions={
              error ? (
                <span
                  role="alert"
                  className="min-w-0 max-w-[46ch] break-words text-left text-[12px] leading-4 text-fail sm:text-right"
                >
                  {error}
                </span>
              ) : null
            }
          >
            <dl className="grid gap-x-8 gap-y-4 px-4 py-4 sm:grid-cols-3">
              <div>
                <dt className="text-[11px] uppercase tracking-[0.08em] text-dim">Current plan</dt>
                <dd className="mt-1 text-[13px] text-ink">
                  <Badge tone="pass">{data.plan}</Badge>
                </dd>
              </div>
              <div>
                <dt className="text-[11px] uppercase tracking-[0.08em] text-dim">Environments</dt>
                <dd className="tnum mt-1 text-[13px] text-ink">{data.holding.environments}</dd>
              </div>
              <div>
                <dt className="text-[11px] uppercase tracking-[0.08em] text-dim">Goldens</dt>
                <dd className="tnum mt-1 text-[13px] text-ink">{data.holding.goldens}</dd>
              </div>
            </dl>
          </Card>

          <Card
            title="What each plan allows"
            note="A plan that is already over its limit refuses the next environment and removes nothing that exists."
          >
            <TableWrap>
              <Table>
                <thead>
                  <tr>
                    <Th>Plan</Th>
                    <Th numeric>Environments</Th>
                    <Th numeric>Goldens</Th>
                    <Th numeric>Artifacts</Th>
                    <Th>Room</Th>
                    <Th>Choose</Th>
                  </tr>
                </thead>
                <tbody>
                  {data.plans.map((p) => (
                    <Row key={p.name}>
                      <Td>
                        {p.name}
                        {p.current ? <span className="ml-2 text-dim">current</span> : null}
                      </Td>
                      <Td numeric>{p.quota.environments}</Td>
                      <Td numeric>{p.quota.goldens}</Td>
                      <Td numeric>{p.quota.artifactGigabytes} GiB</Td>
                      <Td>
                        {p.environments.allowed ? (
                          <Badge tone="pass">room for more</Badge>
                        ) : (
                          <Badge tone="warn">
                            {p.environments.current} of {p.environments.limit} held
                          </Badge>
                        )}
                      </Td>
                      <Td>
                        {p.current ? (
                          <span className="text-dim">--</span>
                        ) : (
                          <Button busy={busy === p.name} onClick={() => choose(p.name)}>
                            {busy === p.name ? "Changing" : `Move to ${p.name}`}
                          </Button>
                        )}
                      </Td>
                    </Row>
                  ))}
                </tbody>
              </Table>
            </TableWrap>
          </Card>
        </div>
      )}
    </Loaded>
  );
}

export default function PlanPage() {
  return (
    <Page
      title="Plan"
      lede="What this organization may hold at once, and the plan that decides it. Nothing here takes payment."
    >
      <Billing />
    </Page>
  );
}
