"use client";

import { useState } from "react";
import { useApi } from "@/lib/api";
import { may } from "@/lib/roles";
import { useSessionContext } from "@/components/session";
import {
  Badge,
  Button,
  Card,
  Empty,
  Row,
  Table,
  TableSkeleton,
  TableWrap,
  Td,
  Th,
  When,
} from "@/components/ui";
import { LoadError } from "@/components/load/states";
import {
  count,
  listExplorations,
  promoteDiscovery,
  seconds,
  type Discovery,
  type Exploration,
} from "@/lib/load";

/**
 * Exploration, on the Load page and deliberately not presented as load.
 *
 * `af explore` drives a real browser from a seed and compiles what it reached
 * into a manifest WORKFLOW, through `explore.Compile`, which returns a
 * `schema.Workflow`. It does not produce a load scenario and it never has.
 *
 * It is on this page because this is where somebody looking for "where does
 * the traffic come from" arrives, and finding nothing about exploration here
 * would send them away with the wrong model. It is in its own card, with its
 * own heading and an explicit sentence, because putting it in the sources
 * table beside observed and deterministic would teach the opposite of the
 * truth. An earlier draft of this console did exactly that, and modelled
 * promotion as turning a discovery into a load scenario, which would have been
 * a lie carried in the type system where every screen inherits it.
 */

/**
 * Promotion, applied optimistically and rolled back if the server refuses.
 *
 * A discovery is a row somebody is looking at when they press the button, so
 * the row changes immediately and reverts with the reason if the call fails.
 * The alternative, a spinner for a whole round trip on a list of ten rows, is
 * how a console comes to feel slow while being fast.
 *
 * The rollback is the part worth writing rather than assuming. An optimistic
 * update with no failure path is not optimism, it is a lie that usually gets
 * away with it.
 */
function useOptimisticPromotion(reload: () => void) {
  const session = useSessionContext();
  const csrf = session.data?.csrfToken ?? "";
  // Keyed by discovery id: the name it optimistically became.
  const [applied, setApplied] = useState<Record<string, string>>({});
  const [pending, setPending] = useState<string | null>(null);
  const [error, setError] = useState<{ id: string; message: string } | null>(null);

  async function promote(explorationId: string, discovery: Discovery, name: string) {
    setPending(discovery.id);
    setError(null);
    // Applied before the request, which is the whole point.
    setApplied((a) => ({ ...a, [discovery.id]: name }));
    try {
      const { workflowName } = await promoteDiscovery(
        { explorationId, discoveryId: discovery.id, name },
        csrf,
      );
      // Take the server's name over the one that was typed: it may have
      // normalised it, and showing the typed one would be the console
      // disagreeing with the manifest about what exists.
      if (workflowName) setApplied((a) => ({ ...a, [discovery.id]: workflowName }));
      reload();
    } catch (e) {
      // Roll back to exactly the previous state, which is the absence of a key
      // rather than an empty string: an empty string would render as a promoted
      // workflow with no name.
      setApplied((a) => {
        const next = { ...a };
        delete next[discovery.id];
        return next;
      });
      setError({
        id: discovery.id,
        message: e instanceof Error ? e.message : "That discovery could not be promoted.",
      });
    } finally {
      setPending(null);
    }
  }

  return { applied, pending, error, promote, clearError: () => setError(null) };
}

function Discoveries({
  exploration,
  canPromote,
  reload,
}: {
  exploration: Exploration;
  canPromote: boolean;
  reload: () => void;
}) {
  const { applied, pending, error, promote, clearError } = useOptimisticPromotion(reload);
  const [naming, setNaming] = useState<string | null>(null);
  const [name, setName] = useState("");

  if (exploration.discoveries.length === 0) {
    return (
      <Empty title="This exploration found nothing">
        The agent did not reach anything it could report. That is a result
        rather than a failure: it means the seed and the budget did not take it
        anywhere new.
      </Empty>
    );
  }

  return (
    <>
      {error ? (
        <p role="alert" className="border-b border-rule px-4 py-2.5 text-[12.5px] leading-6 text-fail">
          {error.message}{" "}
          <button
            type="button"
            onClick={clearError}
            className="underline decoration-[rgba(179,38,30,0.4)] underline-offset-4 hover:decoration-fail"
          >
            Dismiss
          </button>
        </p>
      ) : null}
      <TableWrap>
        <Table>
          <thead>
            <tr>
              <Th>What it reached</Th>
              <Th>Persona</Th>
              <Th numeric>Steps</Th>
              <Th>Reached</Th>
              <Th>Workflow</Th>
            </tr>
          </thead>
          <tbody>
            {exploration.discoveries.map((d) => {
              const workflow = applied[d.id] ?? d.workflowName;
              return (
                <Row key={d.id}>
                  <Td>{d.title}</Td>
                  <Td label="Persona">{d.persona ?? "--"}</Td>
                  <Td label="Steps" numeric>
                    {count(d.steps)}
                  </Td>
                  <Td label="Reached">
                    <When value={d.reachedAt} />
                  </Td>
                  <Td label="Workflow">
                    {workflow ? (
                      <span className="flex flex-wrap items-center gap-2">
                        <Badge tone="pass">In the manifest</Badge>
                        <code className="font-mono text-[12px] text-ink">{workflow}</code>
                      </span>
                    ) : !canPromote ? (
                      <span className="text-[12.5px] text-dim">Your role cannot promote</span>
                    ) : naming === d.id ? (
                      <form
                        className="flex flex-wrap items-center gap-2"
                        onSubmit={(e) => {
                          e.preventDefault();
                          if (name.trim() === "") return;
                          setNaming(null);
                          void promote(exploration.id, d, name.trim());
                          setName("");
                        }}
                      >
                        <label className="sr-only" htmlFor={`promote-${d.id}`}>
                          Workflow name for the discovery {d.title}
                        </label>
                        <input
                          id={`promote-${d.id}`}
                          autoFocus
                          className="h-9 w-[22ch] rounded-md border border-rule bg-card px-2.5 text-[13px] text-ink outline-none placeholder:text-dim focus:border-rule-strong"
                          placeholder="upgrade-a-plan"
                          value={name}
                          onChange={(e) => setName(e.target.value)}
                        />
                        <Button type="submit" variant="primary" disabled={name.trim() === ""}>
                          Add to the manifest
                        </Button>
                        <Button
                          onClick={() => {
                            setNaming(null);
                            setName("");
                          }}
                        >
                          Cancel
                        </Button>
                      </form>
                    ) : (
                      <Button
                        busy={pending === d.id}
                        onClick={() => {
                          setNaming(d.id);
                          setName("");
                        }}
                      >
                        {pending === d.id ? "Adding" : "Make a workflow"}
                      </Button>
                    )}
                  </Td>
                </Row>
              );
            })}
          </tbody>
        </Table>
      </TableWrap>
    </>
  );
}

export function Explorations() {
  const session = useSessionContext();
  const canPromote = may(session.data?.role, "agents.run");
  const state = useApi<Exploration[]>(() => listExplorations(), []);

  return (
    <Card
      title="Exploration"
      note="An agent choosing its own way through the product, from a seed."
    >
      {/* Said before the table, not after it. Somebody arriving at the Load
          page and seeing "exploration" will assume it is a third kind of
          traffic unless told otherwise in the first sentence they read. */}
      <p className="border-b border-rule px-4 py-3 text-[12.5px] leading-6 text-muted">
        This does not produce load. `af explore` drives a real browser and
        compiles what it reached into a workflow for your manifest, which{" "}
        <a
          className="text-ink underline decoration-[rgba(16,16,16,0.25)] underline-offset-4 hover:decoration-ink"
          href="/runs"
        >
          af test runs
        </a>
        . It is here because it is the other way a route nobody wrote down gets
        found, and a discovery is worth nothing until somebody commits it.
      </p>

      {state.status === "error" && state.error ? (
        <LoadError error={state.error} retry={state.reload} />
      ) : state.status === "loading" || state.data === null ? (
        <TableSkeleton rows={3} cols={5} />
      ) : state.data.length === 0 ? (
        <Empty title="No explorations recorded">
          Run `af explore` against an environment and what it reaches appears
          here, with the workflow each discovery became.
        </Empty>
      ) : (
        <div className="divide-y divide-rule">
          {state.data.map((e) => (
            <div key={e.id}>
              <dl className="grid gap-x-8 gap-y-3 px-4 py-3 sm:grid-cols-3">
                <div className="min-w-0">
                  <dt className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
                    Entry point
                  </dt>
                  <dd className="mt-1 break-all font-mono text-[12.5px] text-ink">
                    {e.entryUrl ?? "not recorded"}
                  </dd>
                </div>
                <div className="min-w-0">
                  <dt className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
                    Seed
                  </dt>
                  <dd className="mt-1 font-mono text-[12.5px] text-ink">{e.seed ?? "not recorded"}</dd>
                </div>
                <div className="min-w-0">
                  <dt className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
                    Budget
                  </dt>
                  <dd className="mt-1 text-[13px] text-ink">{seconds(e.budgetSeconds)}</dd>
                </div>
              </dl>
              <Discoveries exploration={e} canPromote={canPromote} reload={state.reload} />
            </div>
          ))}
        </div>
      )}
    </Card>
  );
}
