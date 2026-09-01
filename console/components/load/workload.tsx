"use client";

import { useState } from "react";
import { query } from "@/lib/api";
import { may } from "@/lib/roles";
import { useSessionContext } from "@/components/session";
import {
  Badge,
  Button,
  Card,
  CellLink,
  Empty,
  Field,
  Row,
  Table,
  TableSkeleton,
  TableWrap,
  Td,
  Th,
  When,
  inputClass,
} from "@/components/ui";
import { Fact, Facts, KindMark, StateBadge, VerdictBadge } from "@/components/load/primitives";
import {
  BodyFields,
  BodyView,
  Dropped,
  KindHeader,
  KnobNotes,
  ManifestBlock,
  useBodyDraft,
} from "@/components/load/bodies";
import { Denied, LoadError, WorkloadSkeleton } from "@/components/load/states";
import { StaleNotice, useLive } from "@/components/load/polling";
import { WindowFooter, useWindow } from "@/components/load/paging";
import {
  KINDS,
  KIND_FACTS,
  KNOBS,
  addVersion,
  archiveWorkload,
  count,
  createWorkload,
  getWorkload,
  listRuns,
  startRun,
  type Kind,
  type RunRow,
  type Version,
  type WorkloadDetail,
} from "@/lib/load";

interface Environment {
  env_id: string;
  branch: string;
  state: string;
  repository: string;
}

/* -------------------------------------------------------------------------
 * Starting a run
 * ---------------------------------------------------------------------- */

/**
 * Starting a run, which takes a version and an environment and nothing else.
 *
 * There is no scale box here and no duration box, and their absence is the
 * design rather than an omission. Every knob lives in the VERSION, so changing
 * the scale makes a new version, and comparing scale 1 against scale 4 is
 * comparing two versions of one workload. The alternative, a knob on the run,
 * records the setting only in a form somebody has closed: two runs of one
 * definition would differ in a way nothing on the page could explain.
 *
 * The environment has to belong to the same repository as the workload. The
 * control plane refuses the pair with a sentence saying why, and this filters
 * the list to the ones that can work rather than offering a choice that comes
 * back refused.
 */
function Start({
  detail,
  onStarted,
}: {
  detail: WorkloadDetail;
  onStarted: () => void;
}) {
  const session = useSessionContext();
  const csrf = session.data?.csrfToken ?? "";
  const environments = useLive<{ environments: Environment[] }>(
    () => query("environments.list", { limit: 100 }),
    [],
  );

  const [envId, setEnvId] = useState("");
  const [version, setVersion] = useState("latest");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  return (
    <Card
      title="Start a run"
      note="Dispatches your own workflow in your own repository, against an environment that is already up."
    >
      {environments.status === "error" && environments.error ? (
        <LoadError error={environments.error} retry={environments.reload} />
      ) : environments.status === "loading" || environments.data === null ? (
        <TableSkeleton rows={2} cols={3} />
      ) : (
        (() => {
          const usable = environments.data.environments.filter(
            (e) => e.state !== "torn_down" && e.repository === detail.workload.repository,
          );
          if (usable.length === 0) {
            const otherRepos = environments.data.environments.filter(
              (e) => e.state !== "torn_down",
            ).length;
            return (
              <Empty
                title="No environment to run against"
                action={
                  <a
                    className="inline-flex h-11 items-center justify-center rounded-md bg-ink px-4 text-[14px] font-medium text-white transition-colors hover:bg-[#2b2b2b]"
                    href="/environments"
                  >
                    Ask for an environment
                  </a>
                }
              >
                {otherRepos > 0
                  ? `This workload names routes and workflows out of ${detail.workload.repository ?? "its own repository"}'s manifest, so it can only run against an environment of that repository. The ones that are up belong to others.`
                  : "This needs a twin to run against. Ask for an environment, and this fills once the engine reports it is up."}
              </Empty>
            );
          }
          const chosen = usable.find((e) => e.env_id === envId)?.env_id ?? usable[0]!.env_id;
          const latest = detail.versions[0];
          return (
            <form
              className="px-4 py-4"
              onSubmit={async (e) => {
                e.preventDefault();
                setBusy(true);
                setError(null);
                setNote(null);
                try {
                  const started = await startRun(
                    {
                      slug: detail.workload.slug,
                      envId: chosen,
                      ...(version === "latest" ? {} : { version: Number(version) }),
                    },
                    csrf,
                  );
                  setNote(
                    started.dispatched
                      ? `Asked GitHub to run it${started.runId ? `, as ${started.runId}` : ""}. ${started.note ?? ""}`.trim()
                      : (started.note ??
                        "That request had already been made, so this is the run it produced rather than a second one."),
                  );
                  onStarted();
                } catch (err) {
                  setError(err instanceof Error ? err.message : "That did not work.");
                } finally {
                  setBusy(false);
                }
              }}
            >
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="Environment">
                  <select
                    className={inputClass}
                    value={chosen}
                    onChange={(e) => setEnvId(e.target.value)}
                  >
                    {usable.map((e) => (
                      <option key={e.env_id} value={e.env_id}>
                        {e.env_id} ({e.branch})
                      </option>
                    ))}
                  </select>
                </Field>
                <Field
                  label="Version"
                  hint="Latest means whichever is newest when the run starts, which is what a Run button means. Naming one pins it."
                >
                  <select
                    className={inputClass}
                    value={version}
                    onChange={(e) => setVersion(e.target.value)}
                  >
                    <option value="latest">
                      Latest{latest ? ` (v${latest.version})` : ""}
                    </option>
                    {detail.versions.map((v) => (
                      <option key={v.id} value={String(v.version)}>
                        v{v.version}
                        {v.source === "promoted" ? " (promoted)" : ""}
                      </option>
                    ))}
                  </select>
                </Field>
              </div>

              <div className="mt-4 flex flex-wrap items-center gap-3">
                <Button type="submit" variant="primary" busy={busy}>
                  {busy ? "Asking" : "Start the run"}
                </Button>
                {error ? (
                  <p role="alert" className="max-w-[74ch] text-[12.5px] leading-6 text-fail">
                    {error}
                  </p>
                ) : note ? (
                  <p role="status" className="max-w-[74ch] text-[12.5px] leading-6 text-muted">
                    {note}
                  </p>
                ) : null}
              </div>

              {/* Said before anybody presses it rather than in the error. The
                  control plane dispatches a workflow in the customer's own
                  repository, so a run that never appears is usually a workflow
                  file that has not been updated, and GitHub answers an
                  undeclared input with a 422 that looks exactly like the file
                  being missing. */}
              <p className="mt-4 max-w-[74ch] border-t border-rule pt-3 text-[12px] leading-6 text-dim">
                This does not run anything here. It asks GitHub to run{" "}
                <code className="font-mono">.github/workflows/antifailure.yml</code> on the
                environment's own branch, which is what keeps your database and your credentials
                inside your own cloud. A scenario or an exploration needs the inputs the current{" "}
                <code className="font-mono">examples/github-workflow.yml</code> declares; against an
                older copy GitHub refuses the dispatch and the run is recorded here saying so.
              </p>
            </form>
          );
        })()
      )}
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * Editing
 * ---------------------------------------------------------------------- */

/**
 * A new version.
 *
 * Editing does not exist here and cannot: a version is immutable, enforced by
 * a withheld UPDATE grant rather than by a policy, so the only way to change
 * what a workload does is to add a version beside the one before it. Every run
 * records which version it used, which is what makes an old run readable at
 * all.
 *
 * A save that changed nothing is answered with the note the control plane
 * sends rather than a new version, because a form somebody opened and closed
 * is the ordinary case and a history full of identical entries is noise.
 */
function NewVersion({ detail, onSaved }: { detail: WorkloadDetail; onSaved: () => void }) {
  const session = useSessionContext();
  const csrf = session.data?.csrfToken ?? "";
  const head = detail.versions[0] ?? null;
  const draft = useBodyDraft(detail.workload.kind, head?.body ?? null);
  const [notes, setNotes] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const promoted = head?.body?.kind === "browser_workflow" && head.body.manifestBlock !== null;

  return (
    <Card
      title="Add a version"
      note="A version is immutable. Changing what this runs writes a new one beside it."
    >
      <form
        className="px-4 py-4"
        onSubmit={async (e) => {
          e.preventDefault();
          if (draft.body === null) return;
          setBusy(true);
          setError(null);
          setNote(null);
          try {
            const saved = await addVersion(
              {
                slug: detail.workload.slug,
                body: draft.body,
                ...(notes.trim() === "" ? {} : { notes: notes.trim() }),
              },
              csrf,
            );
            setNote(
              saved.created
                ? `Saved as v${saved.version}. The next run uses it unless somebody pins an older one.`
                : (saved.note ?? "Nothing changed, so nothing was added."),
            );
            setNotes("");
            onSaved();
          } catch (err) {
            setError(err instanceof Error ? err.message : "That did not work.");
          } finally {
            setBusy(false);
          }
        }}
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <BodyFields kind={detail.workload.kind} draft={draft} />
          <Field label="Note" hint="Why this changed. Optional, and read next to the version.">
            <input
              className={inputClass}
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              placeholder="four times production's rate"
            />
          </Field>
        </div>

        {promoted ? (
          <p className="mt-3 max-w-[74ch] text-[12px] leading-6 text-dim">
            This workload was promoted from an exploration, so its version carries the manifest
            block a person has to paste into the repository. A version saved here keeps that block
            rather than dropping it.
          </p>
        ) : null}

        <div className="mt-4 flex flex-wrap items-center gap-3">
          <Button
            type="submit"
            variant="primary"
            busy={busy}
            disabled={draft.body === null || draft.unchanged}
          >
            {busy ? "Saving" : "Save a version"}
          </Button>
          {draft.unchanged && draft.body !== null ? (
            <p className="text-[12.5px] leading-6 text-dim">
              This is exactly what v{head?.version} says, so there is nothing to add.
            </p>
          ) : null}
          {error ? (
            <p role="alert" className="max-w-[74ch] text-[12.5px] leading-6 text-fail">
              {error}
            </p>
          ) : note ? (
            <p role="status" className="max-w-[74ch] text-[12.5px] leading-6 text-muted">
              {note}
            </p>
          ) : null}
        </div>

        <KnobNotes kind={detail.workload.kind} />
      </form>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * History
 * ---------------------------------------------------------------------- */

function Versions({ versions }: { versions: Version[] }) {
  const [open, setOpen] = useState<string | null>(null);
  if (versions.length === 0) {
    return (
      <Empty title="No versions">
        A workload cannot be created without one, so an empty history here means
        the record is incomplete rather than that nothing has been written.
      </Empty>
    );
  }
  return (
    <>
      <TableWrap>
        <Table>
          <thead>
            <tr>
              <Th>Version</Th>
              <Th>Where it came from</Th>
              <Th>Created</Th>
              <Th>Note</Th>
              <Th>Definition</Th>
            </tr>
          </thead>
          <tbody>
            {versions.map((v) => (
              <Row key={v.id}>
                <Td>v{v.version}</Td>
                <Td label="Where it came from">
                  {v.source === "promoted" ? (
                    <Badge tone="neutral">Promoted</Badge>
                  ) : (
                    <span className="text-muted">Written here</span>
                  )}
                </Td>
                <Td label="Created">
                  <When value={v.createdAt} />
                </Td>
                <Td label="Note" className="max-w-[40ch]">
                  {v.notes ?? "--"}
                </Td>
                <Td label="Definition">
                  <Button onClick={() => setOpen(open === v.id ? null : v.id)}>
                    {open === v.id ? "Hide" : "Show"}
                  </Button>
                </Td>
              </Row>
            ))}
          </tbody>
        </Table>
      </TableWrap>
      {open !== null
        ? (() => {
            const v = versions.find((x) => x.id === open);
            if (!v) return null;
            return (
              <div className="border-t border-rule">
                <p className="px-4 pt-4 text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
                  What v{v.version} says
                </p>
                <BodyView body={v.body} />
                {v.bodyDigest ? (
                  <p className="px-4 pb-4 text-[12px] leading-5 text-dim">
                    Digest{" "}
                    <code className="break-all font-mono text-[11.5px]">
                      {v.bodyDigest.slice(0, 16)}
                    </code>
                    . Two versions with the same digest say the same thing, which is why saving an
                    unchanged form adds nothing.
                  </p>
                ) : null}
              </div>
            );
          })()
        : null}
    </>
  );
}

function RunsFor({
  slug,
  onOpen,
  nonce,
}: {
  slug: string;
  onOpen: (runId: string) => void;
  nonce: number;
}) {
  const state = useWindow<RunRow>((limit) => listRuns({ slug, limit }), [slug, nonce]);
  return (
    <Card title="Runs" note="Every run of this workload, newest first.">
      {state.status === "error" && state.error ? (
        <LoadError error={state.error} retry={state.reload} />
      ) : state.status === "loading" ? (
        <TableSkeleton rows={4} cols={5} />
      ) : state.items.length === 0 ? (
        <Empty title="Never run">
          This workload is defined and has never been sent anywhere. Start it
          above and the result lands here.
        </Empty>
      ) : (
        <>
          <TableWrap>
            <Table>
              <thead>
                <tr>
                  {/* When, not which id. Every row here is a run of the same
                      workload, so the id distinguishes nothing a person is
                      looking for, and a truncated one leading the row reads as
                      a word that got cut off. */}
                  <Th>Requested</Th>
                  <Th>Verdict</Th>
                  <Th>State</Th>
                  <Th>Version</Th>
                  <Th>Environment</Th>
                  <Th>Finished</Th>
                </tr>
              </thead>
              <tbody>
                {state.items.map((r) => (
                  <Row key={r.id} onClick={() => onOpen(r.id)}>
                    <Td>
                      <CellLink href={`/load?run=${encodeURIComponent(r.id)}`}>
                        <When value={r.requestedAt} />
                      </CellLink>
                    </Td>
                    <Td label="Verdict">
                      <VerdictBadge verdict={r.verdict} />
                    </Td>
                    <Td label="State">
                      <StateBadge state={r.state} />
                    </Td>
                    <Td label="Version">{r.version === null ? "--" : `v${r.version}`}</Td>
                    <Td label="Environment" mono>
                      {r.envId ?? "--"}
                    </Td>
                    <Td label="Finished">
                      {r.finishedAt ? (
                        <When value={r.finishedAt} />
                      ) : (
                        <span className="text-dim">not yet</span>
                      )}
                    </Td>
                  </Row>
                ))}
              </tbody>
            </Table>
          </TableWrap>
          <WindowFooter
            count={state.items.length}
            noun="run"
            more={state.more}
            atCap={state.atCap}
            widening={state.widening}
            widenError={state.widenError}
            onWiden={state.widen}
            narrow="Older runs of this workload are not reachable from here yet."
          />
        </>
      )}
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * The view
 * ---------------------------------------------------------------------- */

function Archive({ slug, onArchived }: { slug: string; onArchived: () => void }) {
  const session = useSessionContext();
  const csrf = session.data?.csrfToken ?? "";
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!confirming) {
    return <Button onClick={() => setConfirming(true)}>Archive</Button>;
  }
  return (
    <div className="flex flex-wrap items-center gap-2">
      {error ? (
        <p role="alert" className="max-w-[54ch] text-[12.5px] leading-6 text-fail">
          {error}
        </p>
      ) : (
        <p className="text-[12.5px] leading-6 text-muted">
          Archived, not deleted: every run of it stays readable.
        </p>
      )}
      <Button
        variant="danger"
        busy={busy}
        onClick={async () => {
          setBusy(true);
          setError(null);
          try {
            await archiveWorkload({ slug }, csrf);
            onArchived();
          } catch (e) {
            setError(e instanceof Error ? e.message : "That did not work.");
          } finally {
            setBusy(false);
          }
        }}
      >
        {busy ? "Archiving" : "Archive it"}
      </Button>
      <Button onClick={() => setConfirming(false)}>Keep it</Button>
    </div>
  );
}

export function WorkloadDetailView({
  slug,
  onClose,
  onOpenRun,
  onPromote,
}: {
  slug: string;
  onClose: () => void;
  onOpenRun: (runId: string) => void;
  onPromote: (slug: string) => void;
}) {
  const session = useSessionContext();
  const canEdit = may(session.data?.role, "workloads.edit");
  const canRun = may(session.data?.role, "workloads.run");
  // useLive rather than useApi: a reload must not blank a screen that is
  // already showing a definition, and a failed reload must not throw it away.
  const state = useLive<WorkloadDetail | null>(() => getWorkload(slug), [slug]);
  const [nonce, setNonce] = useState(0);

  if (state.status === "error" && state.error) {
    return (
      <LoadError
        error={state.error}
        retry={state.reload}
        back={<Button onClick={onClose}>Back to Load</Button>}
      />
    );
  }
  if (state.status === "loading") return <WorkloadSkeleton />;
  const detail = state.data;
  if (detail === null || detail === undefined) {
    return (
      <Empty
        title="That workload is not here"
        action={<Button onClick={onClose}>Back to Load</Button>}
      >
        The address names a workload that does not exist, that has been
        archived, or that belongs to another organization.
      </Empty>
    );
  }

  const head = detail.versions[0] ?? null;
  const knobs = KNOBS[detail.workload.kind];

  return (
    <div className="space-y-6">
      {state.refreshError ? (
        <StaleNotice
          message={state.refreshError}
          updatedAt={state.updatedAt}
          onRetry={state.reload}
          retrying={state.refreshing}
        />
      ) : null}

      <Card
        title={detail.workload.name}
        note={detail.workload.repository ?? undefined}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            {canEdit ? <Archive slug={detail.workload.slug} onArchived={onClose} /> : null}
            <Button onClick={onClose}>Close</Button>
          </div>
        }
      >
        <KindHeader kind={detail.workload.kind} />
        {detail.workload.description ? (
          <p className="border-b border-rule px-4 py-3 text-[13px] leading-6 text-muted">
            {detail.workload.description}
          </p>
        ) : null}
        <Facts columns={3}>
          <Fact label="Name in the manifest">
            <code className="break-all font-mono text-[12.5px]">{detail.workload.slug}</code>
          </Fact>
          <Fact label="Runs">{count(detail.workload.runs)}</Fact>
          <Fact label="Created">
            <When value={detail.workload.createdAt} />
          </Fact>
        </Facts>
        <div className="border-t border-rule">
          <p className="px-4 pt-4 text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
            {head ? `What v${head.version} runs` : "What it runs"}
          </p>
          <BodyView body={head?.body ?? null} />
          {head?.body?.kind === "browser_workflow" && head.body.manifestBlock === null ? (
            <p className="px-4 pb-4 text-[12.5px] leading-6 text-dim">
              The {knobs.selects}s named above have to already be in{" "}
              <code className="font-mono">antifailure.yaml</code>. This version was written here
              rather than promoted, so it carries no block to paste.
            </p>
          ) : null}
        </div>
      </Card>

      {canRun ? (
        <Start detail={detail} onStarted={() => setNonce((n) => n + 1)} />
      ) : (
        <Denied what="Start a run" />
      )}

      <RunsFor slug={detail.workload.slug} onOpen={onOpenRun} nonce={nonce} />

      {canEdit ? (
        <NewVersion detail={detail} onSaved={state.reload} />
      ) : (
        <Denied what="Add a version" />
      )}

      <Card title="Versions" note="Immutable, and a run records which one it used.">
        <Versions versions={detail.versions} />
      </Card>

      {detail.workload.kind === "exploration" ? (
        <Card
          title="Turn what it found into a workflow"
          note="An exploration finds a route. A workflow runs it every time."
        >
          <div className="px-4 py-4">
            <p className="max-w-[74ch] text-[12.5px] leading-6 text-muted">
              Promotion compiles the document{" "}
              <code className="font-mono">af explore --json</code> prints into a browser workflow,
              and says in full what it could not carry across. It is on the Load page rather than
              here because it takes that document, which lives with whoever ran the command.
            </p>
            <div className="mt-3">
              <Button onClick={() => onPromote(detail.workload.slug)}>Promote an exploration</Button>
            </div>
          </div>
        </Card>
      ) : null}

      {head?.body?.kind === "browser_workflow" && head.body.manifestBlock ? (
        <Card title="The manifest block" note="Until this is committed, nothing can run this version.">
          <ManifestBlock block={head.body.manifestBlock} heading="Paste into antifailure.yaml" />
          {head.body.dropped.length > 0 ? <Dropped dropped={head.body.dropped} /> : null}
        </Card>
      ) : null}
    </div>
  );
}

/* -------------------------------------------------------------------------
 * Making one
 * ---------------------------------------------------------------------- */

interface Repository {
  id: string;
  full_name: string;
}

/**
 * A new workload, created with its first version.
 *
 * In one step and not two, because a workload with no version is a workload
 * that cannot be run: the control plane refuses to create one without a body
 * for exactly that reason, and a form that saved a name first would leave
 * somebody looking at a definition with a Run button that could never work.
 *
 * The kind is chosen once and never again. The versions already written are in
 * the old kind's shape, so changing it would leave a history nothing can read,
 * and the control plane refuses it. Saying so under the control is cheaper
 * than saying it in an error.
 */
export function NewWorkload({ onCreated }: { onCreated: (slug: string) => void }) {
  const session = useSessionContext();
  const csrf = session.data?.csrfToken ?? "";
  const repositories = useLive<Repository[]>(() => query("repositories.list", {}), []);

  const [repository, setRepository] = useState("");
  const [slug, setSlug] = useState("");
  const [name, setName] = useState("");
  const [kind, setKind] = useState<Kind>("observed_load");
  const [description, setDescription] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const draft = useBodyDraft(kind, null);

  const slugError =
    slug.trim() === ""
      ? null
      : !/^[a-z0-9][a-z0-9-]*$/.test(slug.trim())
        ? "Lower case letters, digits and hyphens, starting with a letter or a digit."
        : slug.trim().length > 63
          ? "Sixty three characters is the most a name may be."
          : null;
  const ready =
    slug.trim() !== "" && slugError === null && name.trim() !== "" && draft.body !== null;

  return (
    <div className="border-b border-rule">
      {repositories.status === "error" && repositories.error ? (
        <LoadError error={repositories.error} retry={repositories.reload} />
      ) : repositories.status === "loading" || repositories.data === null ? (
        <TableSkeleton rows={2} cols={3} />
      ) : repositories.data.length === 0 ? (
        <Empty title="No repository connected">
          A workload selects out of a repository's manifest, so there is nothing
          for one to name until the GitHub App is installed on at least one.
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
                if (!ready || draft.body === null) return;
                setBusy(true);
                setError(null);
                try {
                  const made = await createWorkload(
                    {
                      repository: chosen,
                      slug: slug.trim(),
                      name: name.trim(),
                      kind,
                      body: draft.body,
                      ...(description.trim() === "" ? {} : { description: description.trim() }),
                    },
                    csrf,
                  );
                  onCreated(made.slug);
                } catch (err) {
                  setError(err instanceof Error ? err.message : "That did not work.");
                } finally {
                  setBusy(false);
                }
              }}
            >
              <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
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
                  label="Kind"
                  hint="Chosen once. A workload cannot change kind afterwards, because the versions already written are in the old kind's shape."
                >
                  <select
                    className={inputClass}
                    value={kind}
                    onChange={(e) => setKind(e.target.value as Kind)}
                  >
                    {KINDS.map((k) => (
                      <option key={k} value={k}>
                        {KIND_FACTS[k].noun} ({KIND_FACTS[k].command})
                      </option>
                    ))}
                  </select>
                </Field>
                <Field
                  label="Name"
                  hint="What this is, for a person reading a list of them."
                >
                  <input
                    className={inputClass}
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="Checkout under Friday traffic"
                  />
                </Field>
                <Field
                  label="Short name"
                  hint="How it is addressed. It appears in the address bar and cannot be changed."
                  error={slugError}
                >
                  <input
                    className={inputClass}
                    value={slug}
                    onChange={(e) => setSlug(e.target.value)}
                    placeholder="checkout-friday"
                  />
                </Field>
                <BodyFields kind={kind} draft={draft} />
                <Field label="Description" hint="Optional.">
                  <input
                    className={inputClass}
                    value={description}
                    onChange={(e) => setDescription(e.target.value)}
                    placeholder="what this is for"
                  />
                </Field>
              </div>

              <p className="mt-3 max-w-[74ch] text-[12.5px] leading-6 text-muted">
                {KIND_FACTS[kind].what}
              </p>

              <div className="mt-4 flex flex-wrap items-center gap-3">
                <Button type="submit" variant="primary" busy={busy} disabled={!ready}>
                  {busy ? "Creating" : "Create it"}
                </Button>
                {error ? (
                  <p role="alert" className="max-w-[74ch] text-[12.5px] leading-6 text-fail">
                    {error}
                  </p>
                ) : null}
              </div>

              <KnobNotes kind={kind} />
            </form>
          );
        })()
      )}
    </div>
  );
}
