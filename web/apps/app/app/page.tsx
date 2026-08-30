// The environment matrix.
//
// The question this page answers is "what is running, for which branch, and is
// it healthy". Everything on it is in service of being scannable at a hundred
// rows: one row per environment, state as the only colour, identifiers in a
// mono face so they can be compared down the column, and times written out
// rather than rendered as "3 hours ago", which cannot be checked against the
// log line somebody is holding.

import type { Metadata } from "next";
import { Suspense } from "react";
import { Chrome } from "../components/Chrome";
import {
  Empty,
  Failure,
  LinkButton,
  Mono,
  Page,
  PageHead,
  Panel,
  StateChip,
  TableFrame,
  TableSkeleton,
  Td,
  Th,
  Tr,
  When,
  inputClass,
  selectClass,
} from "../components/ui";
import { ApiError, query, type EnvironmentPage, type OrgStatus } from "../lib/api";
import { requireActor } from "../lib/guard";
import { isNavigation } from "../lib/navigation";
import { NoOrganization, Unreachable } from "../components/Screens";

export const metadata: Metadata = { title: "Environments" };
export const dynamic = "force-dynamic";

const STATES = ["queued", "creating", "running", "sleeping", "failed", "torn_down"] as const;

export default async function EnvironmentsPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = await searchParams;
  const one = (key: string): string => {
    const value = params[key];
    const found = Array.isArray(value) ? value[0] : value;
    return typeof found === "string" ? found : "";
  };

  let actor: Awaited<ReturnType<typeof requireActor>>;
  try {
    actor = await requireActor("/");
  } catch (err) {
    // A redirect is not a failure. Next.js signals one by throwing, and
    // catching it here is what turned "go and sign in" into a full page
    // error saying the control plane did not answer.
    if (isNavigation(err)) throw err;
    return <Unreachable detail={err instanceof ApiError ? err.message : String(err)} />;
  }
  if (actor === "no-organization") return <NoOrganization />;

  const filters = {
    repository: one("repository"),
    branch: one("branch"),
    state: STATES.includes(one("state") as (typeof STATES)[number]) ? one("state") : "",
    cursor: one("cursor"),
  };

  return (
    <Chrome current="/" who={actor.label} org={actor.orgSlug} role={actor.role}>
      <Page>
        <PageHead
          title="Environments"
          lede="Every environment this organization is holding, newest first. One per branch, and each one is gone the moment its pull request is."
        />

        <Suspense fallback={<StatusSkeleton />}>
          <Status />
        </Suspense>

        <form className="mt-6 mb-4 flex flex-wrap items-end gap-2.5" method="get">
          <div className="flex min-w-[180px] flex-1 flex-col gap-1.5 sm:max-w-[260px]">
            <label htmlFor="repository" className="text-[12.5px] font-medium tracking-snug text-ink">
              Repository
            </label>
            <input
              id="repository"
              name="repository"
              defaultValue={filters.repository}
              placeholder="owner/name"
              spellCheck={false}
              className={inputClass}
            />
          </div>
          <div className="flex min-w-[150px] flex-1 flex-col gap-1.5 sm:max-w-[220px]">
            <label htmlFor="branch" className="text-[12.5px] font-medium tracking-snug text-ink">
              Branch
            </label>
            <input
              id="branch"
              name="branch"
              defaultValue={filters.branch}
              placeholder="main"
              spellCheck={false}
              className={inputClass}
            />
          </div>
          <div className="flex min-w-[140px] flex-col gap-1.5">
            <label htmlFor="state" className="text-[12.5px] font-medium tracking-snug text-ink">
              State
            </label>
            <select id="state" name="state" defaultValue={filters.state} className={selectClass}>
              <option value="">Any</option>
              {STATES.map((s) => (
                <option key={s} value={s}>
                  {s.replace("_", " ")}
                </option>
              ))}
            </select>
          </div>
          <button
            type="submit"
            className="inline-flex h-9 items-center rounded-lg bg-ink px-3.5 text-[13px] font-medium tracking-snug text-white transition-colors hover:bg-[#1c1c1c] active:translate-y-px"
          >
            Filter
          </button>
          {filters.repository || filters.branch || filters.state ? (
            <a
              href="/"
              className="inline-flex h-9 items-center rounded-lg border border-edge bg-surface px-3 text-[13px] font-medium tracking-snug text-ink transition-colors hover:bg-sunken"
            >
              Clear
            </a>
          ) : null}
        </form>

        <Suspense
          key={`${filters.repository}|${filters.branch}|${filters.state}|${filters.cursor}`}
          fallback={
            <Panel>
              <TableSkeleton rows={8} columns={6} />
            </Panel>
          }
        >
          <Matrix filters={filters} />
        </Suspense>
      </Page>
    </Chrome>
  );
}

// ---------------------------------------------------------------------------

async function Status() {
  let status: OrgStatus;
  try {
    status = await query<OrgStatus>("org.status");
  } catch {
    // The header is context, not the page. A failure to read the quota is not
    // a reason to refuse to show somebody their environments.
    return null;
  }

  return (
    <>
      {status.suspended ? (
        <div className="mb-4">
          <Failure
            title={`${status.slug} is suspended`}
            detail={
              (status.suspendedReason ?? "No reason was recorded.") +
              " Nothing was torn down: what is running keeps running and can still be read. New environments are refused until this is lifted."
            }
          />
        </div>
      ) : null}

      <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-3">
        <Tile
          label="Environments held"
          value={`${status.quotas.environments.current}`}
          of={`of ${status.quotas.environments.limit}`}
          warn={!status.quotas.environments.allowed}
          note={status.quotas.environments.reason || undefined}
        />
        <Tile
          label="Goldens"
          value={`${status.quotas.goldens.current}`}
          of={`of ${status.quotas.goldens.limit}`}
          warn={!status.quotas.goldens.allowed}
          note={status.quotas.goldens.reason || undefined}
        />
        {/* Spans the row on a phone rather than sitting alone in a half-width
            box beside nothing. */}
        <Tile label="Plan" value={status.plan} of={status.slug} className="col-span-2 sm:col-span-1" />
      </div>
    </>
  );
}

function Tile({
  label,
  value,
  of,
  warn,
  note,
  className,
}: {
  label: string;
  value: string;
  of?: string;
  warn?: boolean;
  note?: string;
  className?: string;
}) {
  return (
    <div
      title={note}
      className={`rounded-xl border border-hair bg-surface px-4 py-3 ${className ?? ""}`}
    >
      <p className="text-[11.5px] uppercase tracking-[0.06em] text-faint">{label}</p>
      <p className="mt-1 flex items-baseline gap-1.5">
        <span
          className={`numeric text-[22px] font-semibold leading-none tracking-tighter ${
            warn ? "text-fail" : "text-ink"
          }`}
        >
          {value}
        </span>
        {of ? <span className="text-[12.5px] text-faint">{of}</span> : null}
      </p>
    </div>
  );
}

function StatusSkeleton() {
  return (
    <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-3" aria-hidden>
      {[0, 1, 2].map((i) => (
        <div key={i} className="rounded-xl border border-hair bg-surface px-4 py-3">
          <div className="skeleton h-3 w-24" />
          <div className="skeleton mt-2.5 h-5 w-16" />
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------

async function Matrix({
  filters,
}: {
  filters: { repository: string; branch: string; state: string; cursor: string };
}) {
  let page: EnvironmentPage;
  try {
    page = await query<EnvironmentPage>("environments.list", {
      ...(filters.repository ? { repository: filters.repository } : {}),
      ...(filters.branch ? { branch: filters.branch } : {}),
      ...(filters.state ? { state: filters.state } : {}),
      ...(filters.cursor ? { cursor: filters.cursor } : {}),
      limit: 50,
    });
  } catch (err) {
    if (isNavigation(err)) throw err;
    return (
      <Failure
        title="The environment list could not be read"
        detail={err instanceof ApiError ? err.message : String(err)}
        action={<LinkButton href="/">Try again</LinkButton>}
      />
    );
  }

  const filtered = Boolean(filters.repository || filters.branch || filters.state);

  if (page.environments.length === 0) {
    return (
      <Panel>
        {filtered ? (
          <Empty
            title="Nothing matches those filters"
            says="No environment in this organization has that repository, branch, and state together. The filters are combined, not alternatives."
            action={<LinkButton href="/">Clear the filters</LinkButton>}
          />
        ) : (
          <Empty
            title="No environments yet"
            says="An environment appears here the first time af up runs against a branch with this organization's engine token set. Nothing is created from this page: the control plane records what your engines do and has no route into a machine of yours."
            action={
              <LinkButton href="https://antifailure.dev/docs/getting-started/quickstart" variant="primary">
                How to bring one up
              </LinkButton>
            }
          />
        )}
      </Panel>
    );
  }

  return (
    <>
      <Panel
        title="Matrix"
        note={`${page.environments.length} shown${page.nextCursor ? ", more after these" : ""}`}
      >
        <TableFrame>
          <thead>
            <tr>
              <Th>Repository and branch</Th>
              <Th>Environment</Th>
              <Th>State</Th>
              <Th>Golden</Th>
              <Th>Created</Th>
              <Th>Expires</Th>
            </tr>
          </thead>
          <tbody>
            {page.environments.map((env) => (
              <Tr key={env.id}>
                <Td>
                  <a
                    href={`/environments/${encodeURIComponent(env.env_id)}`}
                    className="block max-w-[34ch] truncate font-medium tracking-snug text-ink underline-offset-4 hover:underline"
                    title={`${env.repository} — ${env.branch}`}
                  >
                    {env.repository}
                  </a>
                  <span className="mt-0.5 flex items-center gap-1.5 text-[12.5px] text-muted">
                    <span className="max-w-[26ch] truncate">{env.branch}</span>
                    {env.pull_request ? (
                      <span className="numeric shrink-0 text-faint">#{env.pull_request}</span>
                    ) : null}
                  </span>
                </Td>
                <Td>
                  <Mono className="text-muted">{env.env_id}</Mono>
                  {env.preview_url ? (
                    <a
                      href={env.preview_url}
                      rel="noreferrer noopener"
                      className="mt-0.5 block max-w-[28ch] truncate text-[12.5px] text-ink underline underline-offset-4 hover:no-underline"
                    >
                      {env.preview_url}
                    </a>
                  ) : (
                    <span className="mt-0.5 block text-[12.5px] text-faint">no address yet</span>
                  )}
                </Td>
                <Td>
                  <StateChip value={env.state} />
                  {env.runtime ? (
                    <span className="mt-0.5 block text-[12px] text-faint">{env.runtime}</span>
                  ) : null}
                </Td>
                <Td>
                  {env.golden_version ? (
                    <Mono className="text-muted">{env.golden_version}</Mono>
                  ) : (
                    <span className="text-faint">&mdash;</span>
                  )}
                </Td>
                <Td className="text-[12.5px] text-muted">
                  <When at={env.created_at} />
                </Td>
                <Td className="text-[12.5px] text-muted">
                  <When at={env.expires_at} />
                </Td>
              </Tr>
            ))}
          </tbody>
        </TableFrame>
      </Panel>

      {page.nextCursor ? (
        <div className="mt-4 flex justify-center">
          <LinkButton
            href={`/?${new URLSearchParams({
              ...(filters.repository ? { repository: filters.repository } : {}),
              ...(filters.branch ? { branch: filters.branch } : {}),
              ...(filters.state ? { state: filters.state } : {}),
              cursor: page.nextCursor,
            }).toString()}`}
          >
            Older environments
          </LinkButton>
        </div>
      ) : null}
    </>
  );
}
