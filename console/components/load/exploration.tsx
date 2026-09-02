"use client";

import { useState } from "react";
import { query } from "@/lib/api";
import { may } from "@/lib/roles";
import { useSessionContext } from "@/components/session";
import { Button, Card, Empty, Field, TableSkeleton, inputClass } from "@/components/ui";
import { Dropped, ManifestBlock } from "@/components/load/bodies";
import { Denied, LoadError } from "@/components/load/states";
import { useLive } from "@/components/load/polling";
import {
  promoteExploration,
  readExplorations,
  type PastedExploration,
  type Promotion,
} from "@/lib/load";

interface Repository {
  id: string;
  full_name: string;
}

/**
 * Turning an exploration into a workflow that runs every time.
 *
 * WHAT THIS IS, BECAUSE IT IS EASY TO OVERSELL. An exploration is a goal and a
 * seed: the agent reads each page, chooses where to go, and records the moves
 * it made. Promotion compiles that into a manifest WORKFLOW, which `af test`
 * runs. It does NOT produce a load scenario and it never has, because nothing
 * in an exploration record carries a rate.
 *
 * TWO THINGS THIS SCREEN MUST NOT SUMMARISE, and they are the reason it is a
 * screen at all rather than a button.
 *
 * The dropped list. A compilation always leaves something behind, starting
 * with the expectation: an exploration knows what it was looking for and does
 * not know what a passing page should say. That list is never empty, and a
 * promotion that returned a name and quietly dropped things would look
 * finished and would not be. Each line is a sentence somebody has to read
 * before deciding whether to keep the promotion.
 *
 * The manifest block. `af test --only <name>` selects out of the customer's
 * own manifest, and the control plane cannot put a file in somebody's
 * repository. So a promoted version is a definition plus an instruction, and a
 * control plane admitting it cannot finish the job is the honest core of the
 * feature.
 *
 * The document comes from the person rather than from the control plane,
 * because that is where it is. `af explore --json` prints it on whichever
 * machine ran the command, and nothing uploads it.
 */
export function Promote({
  fromWorkload,
  onPromoted,
}: {
  /** Prefilled when somebody arrived from an exploration workload, so the
   *  promotion lands on the workload they were looking at rather than on a new
   *  one named after the document. */
  fromWorkload?: string | null;
  onPromoted: (slug: string) => void;
}) {
  const session = useSessionContext();
  const csrf = session.data?.csrfToken ?? "";
  const canEdit = may(session.data?.role, "workloads.edit");
  const repositories = useLive<Repository[]>(() => query("repositories.list", {}), []);

  const [repository, setRepository] = useState("");
  const [slug, setSlug] = useState(fromWorkload ?? "");
  const [fromRunId, setFromRunId] = useState("");
  const [persona, setPersona] = useState("");
  const [document, setDocument] = useState("");
  const [chosen, setChosen] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<Promotion | null>(null);

  if (!canEdit) return <Denied what="Promote an exploration" />;

  // Parsed here as well as on the server, so a paste that is not JSON is said
  // beside the field rather than coming back as a refusal about the whole
  // request. The server parses it again and is the one that decides: this is a
  // courtesy, not a validation.
  //
  // The unwrapping is not a courtesy. `af explore --json` prints an envelope
  // with an explorations array, the promotion route compiles ONE exploration
  // and reads name and goal off the top level of what it is sent, and a run
  // with two goals produces two. So pasting exactly what the command printed,
  // which is what this screen asks for, would be refused for carrying no name.
  let parseError: string | null = null;
  let found: PastedExploration[] = [];
  if (document.trim() !== "") {
    try {
      const read = readExplorations(JSON.parse(document));
      found = read.explorations;
      parseError = read.refusal;
    } catch {
      parseError =
        "That is not JSON. It should be exactly what af explore --json printed, braces and all.";
    }
  }
  const picked = found.find((e) => e.name === chosen) ?? found[0] ?? null;

  return (
    // No title on this card. It is the whole of its page and the page heading
    // already says this; a card repeating its own page's title is the shape of
    // a screen assembled out of parts that were never read together.
    <Card>
      <div className="border-b border-rule px-4 py-3">
        <p className="max-w-[74ch] text-[12.5px] leading-6 text-muted">
          This does not produce load. <code className="font-mono">af explore</code> drives a real
          browser and this compiles what it reached into a workflow for your manifest, which{" "}
          <code className="font-mono">af test</code> runs. Paste the document{" "}
          <code className="font-mono">af explore --json</code> printed: it lives on the machine that
          ran the command, and nothing sends it here on its own.
        </p>
      </div>

      {repositories.status === "error" && repositories.error ? (
        <LoadError
          error={repositories.error}
          retry={repositories.reload}
          reading="the repositories this organization has connected"
          needs="environments.view"
        />
      ) : repositories.status === "loading" || repositories.data === null ? (
        <TableSkeleton rows={2} cols={2} />
      ) : repositories.data.length === 0 ? (
        <Empty title="No repository connected">
          A workflow belongs to a repository's manifest, so there is nowhere to
          put one until the GitHub App is installed on at least one.
        </Empty>
      ) : (
        (() => {
          const chosen =
            repositories.data.find((r) => r.full_name === repository)?.full_name ??
            repositories.data[0]!.full_name;
          return (
            <form
              className="px-4 py-4"
              onSubmit={async (e) => {
                e.preventDefault();
                if (picked === null || parseError !== null) return;
                setBusy(true);
                setError(null);
                setResult(null);
                try {
                  const promotion = await promoteExploration(
                    {
                      repository: chosen,
                      exploration: picked.raw,
                      ...(slug.trim() === "" ? {} : { slug: slug.trim() }),
                      ...(fromRunId.trim() === "" ? {} : { fromRunId: fromRunId.trim() }),
                      ...(persona.trim() === "" ? {} : { persona: persona.trim() }),
                    },
                    csrf,
                  );
                  setResult(promotion);
                  if (promotion.slug) onPromoted(promotion.slug);
                } catch (err) {
                  setError(err instanceof Error ? err.message : "That did not work.");
                } finally {
                  setBusy(false);
                }
              }}
            >
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="Repository">
                  <select
                    className={inputClass}
                    value={chosen}
                    onChange={(e) => setRepository(e.target.value)}
                  >
                    {repositories.data.map((r) => (
                      <option key={r.id} value={r.full_name}>
                        {r.full_name}
                      </option>
                    ))}
                  </select>
                </Field>
                <Field
                  label="Workload"
                  hint="Optional. Left empty, this makes one named after the exploration; naming an existing browser workflow adds a version to it instead."
                >
                  <input
                    className={inputClass}
                    value={slug}
                    onChange={(e) => setSlug(e.target.value)}
                    placeholder="upgrade-a-plan"
                  />
                </Field>
                <Field
                  label="From which run"
                  hint="Optional. The exploration run this document came from, so the version is attributed to it rather than to a paste."
                >
                  <input
                    className={inputClass}
                    value={fromRunId}
                    onChange={(e) => setFromRunId(e.target.value)}
                    placeholder="the run id"
                  />
                </Field>
                <Field label="Persona" hint="Optional. Who the workflow should sign in as.">
                  <input
                    className={inputClass}
                    value={persona}
                    onChange={(e) => setPersona(e.target.value)}
                    placeholder="a returning customer"
                  />
                </Field>
              </div>

              <div className="mt-3">
                <Field
                  label="The exploration"
                  hint="What af explore --json printed."
                  error={parseError}
                >
                  <textarea
                    className={`${inputClass} min-h-[9rem] font-mono text-[12.5px] leading-6`}
                    value={document}
                    onChange={(e) => setDocument(e.target.value)}
                    placeholder='{"name": "upgrade a plan", "goal": "reach the confirmation page", ...}'
                  />
                </Field>
                {found.length > 0 ? <Chooser found={found} picked={picked} onPick={setChosen} /> : null}
                <label className="mt-2 inline-flex min-h-11 items-center gap-2 text-[12.5px] text-muted">
                  <span>Or read it from a file:</span>
                  <input
                    type="file"
                    accept=".json,application/json"
                    className="text-[12.5px] text-muted file:mr-2 file:h-9 file:rounded-md file:border file:border-rule file:bg-card file:px-3 file:text-[13px] file:font-medium file:text-ink"
                    onChange={(e) => {
                      const file = e.target.files?.[0];
                      if (!file) return;
                      // Read in the browser rather than uploaded. There is no
                      // upload endpoint and there should not be one: the
                      // document goes to the control plane inside the call that
                      // compiles it and nowhere else.
                      void file.text().then(setDocument);
                    }}
                  />
                </label>
              </div>

              <div className="mt-4 flex flex-wrap items-center gap-3">
                <Button
                  type="submit"
                  variant="primary"
                  busy={busy}
                  disabled={picked === null || parseError !== null}
                >
                  {busy ? "Compiling" : "Compile a workflow"}
                </Button>
                {error ? (
                  <p role="alert" className="max-w-[74ch] text-[12.5px] leading-6 text-fail">
                    {error}
                  </p>
                ) : null}
              </div>
            </form>
          );
        })()
      )}

      {result ? <Result result={result} /> : null}
    </Card>
  );
}

/**
 * Which exploration out of the pasted document.
 *
 * A run with two goals produces two explorations and only one is promoted at a
 * time, so this is a choice rather than an unwrapping. It shows what each one
 * is worth before the choice is made rather than after: an exploration that
 * never reached its goal still compiles, and the workflow it compiles into
 * asserts something nobody has seen happen. The control plane says that in the
 * dropped list afterwards; saying it here as well is what lets somebody pick
 * the other one instead.
 *
 * `blocked` is the harder case and it is called out rather than refused. The
 * engine's own `af explore --emit-workflow` skips a blocked exploration
 * outright, because nothing was explored and there is no path to compile, so a
 * workflow made from one is made from a walk that did not happen. The control
 * plane does not refuse it, so this screen is the only place that fact exists.
 */
function Chooser({
  found,
  picked,
  onPick,
}: {
  found: PastedExploration[];
  picked: PastedExploration | null;
  onPick: (name: string) => void;
}) {
  const blocked = picked?.verdict === "blocked";
  const unreached = picked?.reached === false;
  return (
    <div className="mt-3">
      <Field
        label={found.length === 1 ? "The exploration" : "Which exploration"}
        hint={
          found.length === 1
            ? "One goal was explored, so this is the one that gets compiled."
            : `${found.length} goals were explored. One workflow is compiled at a time.`
        }
      >
        <select
          className={inputClass}
          value={picked?.name ?? ""}
          onChange={(e) => onPick(e.target.value)}
        >
          {found.map((e) => (
            <option key={e.name} value={e.name}>
              {e.name}
              {e.reached === false ? " (never reached its goal)" : ""}
              {e.verdict === "blocked" ? " (blocked)" : ""}
            </option>
          ))}
        </select>
      </Field>
      {picked?.goal ? (
        <p className="mt-2 max-w-[74ch] text-[12.5px] leading-6 text-muted">
          The goal, which becomes the workflow&apos;s expectation:{" "}
          <span className="text-ink">{picked.goal}</span>
        </p>
      ) : null}
      {blocked || unreached ? (
        <p
          role="status"
          className="mt-2 max-w-[74ch] rounded-md bg-[rgba(138,90,0,0.07)] px-3 py-2 text-[12.5px] leading-6 text-warn"
        >
          {blocked
            ? "This exploration was blocked, so nothing was explored and there is no path to compile. af explore --emit-workflow skips a blocked one outright; a workflow made from it would be made from a walk that never happened."
            : "This exploration never reached its goal, so the workflow will assert something nobody has seen happen. It compiles, and it comes back unverified until the path exists."}
        </p>
      ) : null}
    </div>
  );
}

/**
 * What the promotion produced, and what it did not.
 *
 * The order is the argument. The name comes first because that is what was
 * asked for, then the two admissions, and the block last because it is the
 * thing to go and do. A screen that led with the block would read as though
 * the job were done.
 */
function Result({ result }: { result: Promotion }) {
  return (
    <div className="border-t border-rule">
      <div className="bg-[rgba(30,122,58,0.06)] px-4 py-3">
        <p role="status" className="max-w-[74ch] text-[12.5px] leading-6 text-ink">
          Compiled into{" "}
          <code className="break-all font-mono text-[12.5px]">{result.slug ?? "a workflow"}</code>
          {result.version === null ? "" : ` as v${result.version}`}
          {result.created
            ? ", a workload that did not exist until now."
            : ", added beside what that workload already said."}{" "}
          It cannot run yet. The block below has to be in your repository first.
        </p>
      </div>

      {/* Never summarised into a count. "3 things were dropped" is the shape of
          a notice nobody opens, and the whole value of this list is that
          somebody reads it before trusting the workflow.

          An EMPTY list is not "nothing was dropped": the compiler seeds it
          unconditionally with the note about the expectation being the goal,
          so it cannot legitimately come back empty. Rendering nothing there
          would turn a lost list into a clean bill of health, which is the
          defect this whole area was reworked to stop. */}
      {result.dropped.length > 0 ? (
        <Dropped dropped={result.dropped} />
      ) : (
        <p role="alert" className="border-t border-rule px-4 py-4 text-[12.5px] leading-6 text-warn">
          The control plane returned no notes about what the compilation did not carry. A
          compilation always drops at least the expectation, because an exploration knows what it
          was looking for and not what a passing page should say. An empty list here means
          something lost them rather than that nothing was dropped, so read the compiled workflow
          before trusting it.
        </p>
      )}

      {result.manifestBlock ? (
        <ManifestBlock block={result.manifestBlock} heading="Paste into antifailure.yaml" />
      ) : (
        <p className="px-4 py-4 text-[12.5px] leading-6 text-warn" role="alert">
          The control plane did not return a manifest block for this promotion. Without one there is
          nothing to paste, and <code className="font-mono">af test --only</code> will not find the
          workflow this version selects.
        </p>
      )}
    </div>
  );
}
