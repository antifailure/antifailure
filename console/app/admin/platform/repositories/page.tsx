"use client";

import { Suspense, useState } from "react";
import { useSearchParams } from "next/navigation";
import {
  Badge,
  Card,
  CardSkeleton,
  CellLink,
  Loaded,
  TableSkeleton,
  When,
} from "@/components/ui";
import {
  AdminPage,
  DataTable,
  Drawer,
  EmptyList,
  Facts,
  FilterBar,
  StatusChip,
  type Column,
} from "@/components/admin/primitives";
import { More } from "@/components/pagination";
import {
  approvalWording,
  shortSha,
  useGenerations,
  usePullRequests,
  useRepositories,
  useRepository,
  type AdminGeneration,
  type AdminPullRequest,
  type AdminRepository,
} from "@/lib/admin-platform";

/**
 * The repositories this installation answers on, and the pull requests it is
 * answering.
 *
 * THE QUESTION THIS SCREEN ANSWERS: a customer says the check on one pull
 * request never appeared. Support has a repository name and a number, and the
 * answer is in the generation behind that pull request, where `state` and
 * `detail` together say what happened in one sentence. So the path is
 * repository, then pull requests, then that pull request's generations, and
 * each step is one query rather than a page that pretends to know everything.
 *
 * A QUERY STRING RATHER THAN A DYNAMIC SEGMENT. The console is a static export,
 * so a detail view is /admin/platform/repositories?id=<uuid> and never
 * /admin/platform/repositories/[id], which cannot be exported without knowing
 * every id at build time. next.config.ts says so and the rest of the console
 * already follows it.
 */
export default function PlatformRepositoriesPage() {
  return (
    <Suspense
      fallback={
        <AdminPage href="/admin/platform/repositories">
          <Card>
            <TableSkeleton rows={6} cols={5} />
          </Card>
        </AdminPage>
      }
    >
      <Screen />
    </Suspense>
  );
}

function Screen() {
  const params = useSearchParams();
  const id = params.get("id");
  return id ? <Detail repositoryId={id} /> : <RepositoryList />;
}

/* -------------------------------------------------------------------------
 * The list
 * ---------------------------------------------------------------------- */

function RepositoryList() {
  const [search, setSearch] = useState("");
  const state = useRepositories(search);

  const columns: Column<AdminRepository>[] = [
    {
      key: "repository",
      header: "Repository",
      cell: (r) => (
        <>
          {/* A link on the name rather than a clickable row: a row that
              navigates on click has no keyboard equivalent and no address to
              copy, and CellLink is already 44px tall under a thumb. */}
          <CellLink href={`/admin/platform/repositories?id=${encodeURIComponent(r.id)}`}>
            <span className="block truncate font-mono text-[12.5px] font-medium text-ink">
              {r.fullName}
            </span>
          </CellLink>
          <span className="block truncate font-mono text-[12px] text-muted">{r.orgSlug}</span>
        </>
      ),
    },
    {
      key: "branch",
      header: "Default branch",
      mono: true,
      cell: (r) => r.defaultBranch,
    },
    {
      key: "open",
      header: "Open PRs",
      numeric: true,
      cell: (r) => r.openPullRequests.toLocaleString(),
    },
    {
      key: "activity",
      header: "Last pull request",
      cell: (r) =>
        // Never and zero open are different facts. A repository that has never
        // had a pull request recorded has never exercised this product at all,
        // which is worth telling apart from one whose pull requests are closed.
        r.lastPullRequestAt === null ? (
          <span className="text-dim">None recorded</span>
        ) : (
          <When value={r.lastPullRequestAt} />
        ),
    },
    {
      key: "standing",
      header: "Standing",
      cell: (r) => (
        <>
          {r.archived ? <Badge tone="neutral">archived</Badge> : <Badge tone="pass">active</Badge>}
          {r.private ? null : (
            <span className="mt-1 block text-[12px] text-muted">Public repository</span>
          )}
        </>
      ),
    },
  ];

  return (
    <AdminPage href="/admin/platform/repositories">
      <Card>
        <FilterBar
          search={{
            value: search,
            onChange: setSearch,
            label: "Search repositories by name or organization",
            placeholder: "owner/name or organization",
          }}
        />
        <Loaded state={state} skeleton={<TableSkeleton rows={6} cols={5} />}>
          {(rows) => (
            <DataTable
              columns={columns}
              rows={rows}
              keyOf={(r) => r.id}
              empty={
                <EmptyList
                  title={search ? "No repository matches that" : "No repositories are connected"}
                >
                  {search
                    ? "Nothing on this installation has that name, and no organization has that slug. Clear the search to see every repository."
                    : "No customer has connected a repository yet. A repository appears here the first time the GitHub App delivers an event about it."}
                </EmptyList>
              }
              footer={
                <More
                  shown={rows.length}
                  noun={{ one: "repository", many: "repositories" }}
                  hasMore={state.hasMore}
                  busy={state.busy}
                  error={state.moreError}
                  onMore={state.more}
                />
              }
            />
          )}
        </Loaded>
      </Card>
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * One repository
 * ---------------------------------------------------------------------- */

function Detail({ repositoryId }: { repositoryId: string }) {
  const repository = useRepository(repositoryId);
  const [state, setState] = useState("open");
  const pulls = usePullRequests(repositoryId, state);
  const [open, setOpen] = useState<AdminPullRequest | null>(null);

  return (
    <Loaded
      state={repository}
      framed
      skeleton={
        <AdminPage title="Repository">
          <CardSkeleton count={2} />
        </AdminPage>
      }
    >
      {(repo) => (
        <AdminPage
          title={repo.fullName}
          lede={
            <>
              In <span className="font-mono">{repo.orgSlug}</span>, tracked since{" "}
              <When value={repo.createdAt} />
            </>
          }
          actions={
            <CellLink href="/admin/platform/repositories">Back to every repository</CellLink>
          }
        >
          <div className="space-y-5">
            <Card title="Repository">
              <Facts
                facts={[
                  { label: "Organization", value: repo.orgName },
                  { label: "Slug", value: repo.orgSlug, mono: true },
                  { label: "Default branch", value: repo.defaultBranch, mono: true },
                  { label: "Visibility", value: repo.private ? "Private" : "Public" },
                  { label: "GitHub id", value: repo.githubId, mono: true },
                  {
                    label: "Standing",
                    value: repo.archived ? "Archived on GitHub" : "Active",
                  },
                  {
                    label: "App installation",
                    value:
                      repo.installations.length === 0 ? (
                        // The first thing to check when a customer reports that
                        // nothing arrives. A repository row survives the
                        // installation being removed, so this really can be
                        // empty on a repository that used to work.
                        <span className="text-warn">
                          None. No event about this repository can reach the control plane.
                        </span>
                      ) : (
                        repo.installations
                          .map(
                            (i) => `${i.accountLogin}${i.suspended ? " (suspended)" : ""}`,
                          )
                          .join(", ")
                      ),
                  },
                ]}
              />
            </Card>

            <Card
              title="Pull requests"
              note="The newest generation for each, which is where a check that never appeared is explained."
            >
              <FilterBar
                filters={[
                  {
                    label: "State",
                    value: state,
                    onChange: setState,
                    options: [
                      { value: "open", label: "Open" },
                      { value: "merged", label: "Merged" },
                      { value: "closed", label: "Closed" },
                      { value: "", label: "Every state" },
                    ],
                  },
                ]}
              />
              <Loaded state={pulls} skeleton={<TableSkeleton rows={5} cols={4} />}>
                {(rows) => (
                  <DataTable
                    columns={pullRequestColumns(setOpen)}
                    rows={rows}
                    keyOf={(r) => r.id}
                    empty={
                      <EmptyList
                        title={
                          state === "open"
                            ? "No open pull requests"
                            : state === ""
                              ? "No pull requests recorded"
                              : `No ${state} pull requests`
                        }
                      >
                        {state === ""
                          ? "This control plane has never recorded a pull request on this repository. Either none has been opened since the app was installed, or its deliveries are not arriving. The Integrations section shows which."
                          : "Change the state filter above to see the ones in other states."}
                      </EmptyList>
                    }
                    footer={
                      <More
                        shown={rows.length}
                        noun={{ one: "pull request", many: "pull requests" }}
                        hasMore={pulls.hasMore}
                        busy={pulls.busy}
                        error={pulls.moreError}
                        onMore={pulls.more}
                      />
                    }
                  />
                )}
              </Loaded>
            </Card>
          </div>

          <Generations pullRequest={open} onClose={() => setOpen(null)} />
        </AdminPage>
      )}
    </Loaded>
  );
}

function pullRequestColumns(
  onOpen: (pr: AdminPullRequest) => void,
): Column<AdminPullRequest>[] {
  return [
    {
      key: "number",
      header: "Pull request",
      cell: (r) => (
        <>
          <button
            type="button"
            onClick={() => onOpen(r)}
            // A real button, so it is in the tab order, activates on Enter and
            // Space, and is announced as a control. A div with an onClick here
            // would be invisible to a keyboard.
            className="block max-w-full truncate text-left font-medium text-ink underline decoration-rule-strong underline-offset-2"
          >
            #{r.number} {r.title ?? "Untitled"}
          </button>
          <span className="block truncate font-mono text-[12px] text-muted">
            {r.headRef} into {r.baseRef}
          </span>
        </>
      ),
    },
    {
      key: "state",
      header: "State",
      cell: (r) => (
        <>
          <StatusChip value={r.state} />
          {r.draft ? <span className="mt-1 block text-[12px] text-muted">Draft</span> : null}
        </>
      ),
    },
    {
      key: "origin",
      header: "Approval",
      cell: (r) => {
        const approval = approvalWording(r);
        return (
          <>
            <Badge tone={approval.tone}>{approval.label}</Badge>
            {r.fromFork ? (
              <span className="mt-1 block truncate font-mono text-[12px] text-muted">
                fork: {r.headRepository}
              </span>
            ) : null}
          </>
        );
      },
    },
    {
      key: "generation",
      header: "Newest check",
      cell: (r) =>
        r.latestGeneration === null ? (
          // The answer to "the check never appeared", stated rather than left
          // as a blank cell for the reader to interpret.
          <span className="text-warn">No check has ever been created</span>
        ) : (
          <>
            <StatusChip value={r.latestGeneration.state} />
            <span className="mt-1 block font-mono text-[12px] text-muted">
              {shortSha(r.latestGeneration.headSha)} attempt {r.latestGeneration.attempt}
            </span>
          </>
        ),
    },
  ];
}

/* -------------------------------------------------------------------------
 * One pull request's generations
 * ---------------------------------------------------------------------- */

function Generations({
  pullRequest,
  onClose,
}: {
  pullRequest: AdminPullRequest | null;
  onClose: () => void;
}) {
  const state = useGenerations(pullRequest?.id ?? "");

  return (
    <Drawer
      open={pullRequest !== null}
      title={pullRequest ? `#${pullRequest.number} check history` : "Check history"}
      onClose={onClose}
    >
      <Loaded state={state} skeleton={<CardSkeleton count={2} />}>
        {(rows) =>
          rows.length === 0 ? (
            <EmptyList title="No check has been created for this pull request">
              Nothing was ever queued against it. That usually means the delivery that would have
              started one did not arrive, or the head is from a fork nobody has approved.
            </EmptyList>
          ) : (
            <ul className="divide-y divide-rule">
              {rows.map((g) => (
                <li key={g.id} className="px-4 py-4">
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    <StatusChip value={g.state} />
                    <span className="font-mono text-[12px] text-muted">
                      {shortSha(g.headSha)} attempt {g.attempt}
                    </span>
                  </div>
                  {g.detail ? (
                    // The one sentence the generation carries. 0021 keeps it to
                    // a sentence on purpose, so it is shown whole rather than
                    // truncated into something that reads like a different one.
                    <p className="mt-2 break-words text-[13px] leading-6 text-ink">{g.detail}</p>
                  ) : null}
                  <dl className="mt-2 grid grid-cols-[minmax(0,110px)_minmax(0,1fr)] gap-x-4 gap-y-1 text-[12px] leading-5">
                    <GenerationFact label="Queued" value={<When value={g.queuedAt} />} />
                    {g.startedAt ? (
                      <GenerationFact label="Started" value={<When value={g.startedAt} />} />
                    ) : null}
                    {g.finishedAt ? (
                      <GenerationFact label="Finished" value={<When value={g.finishedAt} />} />
                    ) : null}
                    {g.finishedAt === null && g.deadlineAt ? (
                      <GenerationFact label="Gives up" value={<When value={g.deadlineAt} />} />
                    ) : null}
                    {g.envId ? (
                      <GenerationFact
                        label="Environment"
                        value={<span className="font-mono">{g.envId}</span>}
                      />
                    ) : null}
                    {g.reportedBy ? (
                      <GenerationFact label="Reported by" value={g.reportedBy} />
                    ) : null}
                    {g.checkRunId === null ? (
                      // Null while the installation does not hold checks:write.
                      // Saying which permission is missing is the whole value of
                      // showing this at all.
                      <GenerationFact
                        label="Check run"
                        value={
                          <span className="text-warn">
                            None. The installation does not hold checks: write, so only the comment
                            was posted.
                          </span>
                        }
                      />
                    ) : (
                      <GenerationFact
                        label="Check run"
                        value={<span className="font-mono">{g.checkRunId}</span>}
                      />
                    )}
                    {g.supersededBy ? (
                      <GenerationFact
                        label="Superseded"
                        value="A newer generation replaced this one."
                      />
                    ) : null}
                  </dl>
                </li>
              ))}
            </ul>
          )
        }
      </Loaded>
    </Drawer>
  );
}

function GenerationFact({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <>
      <dt className="text-dim">{label}</dt>
      <dd className="min-w-0 break-words text-muted">{value}</dd>
    </>
  );
}
