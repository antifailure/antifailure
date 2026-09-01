"use client";

import { useState } from "react";
import { query } from "@/lib/api";
import { may } from "@/lib/roles";
import { useSessionContext } from "@/components/session";
import { Button, Card, Empty, Field, TableSkeleton, inputClass } from "@/components/ui";
import { Dropped, ManifestBlock } from "@/components/load/bodies";
import { Denied, LoadError } from "@/components/load/states";
import { useLive } from "@/components/load/polling";
import { promoteExploration, type Promotion } from "@/lib/load";

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
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<Promotion | null>(null);

  if (!canEdit) return <Denied what="Promote an exploration" />;

  // Parsed here as well as on the server, so a paste that is not JSON is said
  // beside the field rather than coming back as a refusal about the whole
  // request. The server parses it again and is the one that decides: this is a
  // courtesy, not a validation.
  let parsed: unknown = null;
  let parseError: string | null = null;
  if (document.trim() !== "") {
    try {
      parsed = JSON.parse(document);
    } catch {
      parseError =
        "That is not JSON. It should be exactly what af explore --json printed, braces and all.";
    }
  }

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
                if (parsed === null || parseError !== null) return;
                setBusy(true);
                setError(null);
                setResult(null);
                try {
                  const promotion = await promoteExploration(
                    {
                      repository: chosen,
                      exploration: parsed,
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
                  disabled={parsed === null || parseError !== null}
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
          somebody reads it before trusting the workflow. */}
      {result.dropped.length > 0 ? <Dropped dropped={result.dropped} /> : null}

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
