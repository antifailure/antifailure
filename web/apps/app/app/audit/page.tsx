// The audit log, and whether it has been altered.
//
// The chain verification is at the top rather than behind a button, because a
// log whose integrity you have to remember to check is a log that gets checked
// after the incident rather than before it. It reports every break rather than
// the first: an investigation wants the extent of the tampering, and the first
// break only says where it started.
//
// Reading this page is itself not audited. Exporting it is, because an export
// is a file of who did what leaving the system, which is exactly the kind of
// action the log exists to record.

import type { Metadata } from "next";
import { Suspense } from "react";
import { Chrome } from "../../components/Chrome";
import {
  Chip,
  Empty,
  Failure,
  LinkButton,
  Mono,
  Page,
  PageHead,
  Panel,
  TableFrame,
  TableSkeleton,
  Td,
  Th,
  Tr,
  When,
  inputClass,
} from "../../components/ui";
import { ApiError, query, type AuditEntry, type ChainReport } from "../../lib/api";
import { asJson } from "../../lib/json";
import { requireActor } from "../../lib/guard";
import { isNavigation } from "../../lib/navigation";
import { NoOrganization, Unreachable } from "../../components/Screens";

export const metadata: Metadata = { title: "Audit" };
export const dynamic = "force-dynamic";

export default async function AuditPage({
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
    actor = await requireActor("/audit");
  } catch (err) {
    // A redirect is not a failure. Next.js signals one by throwing, and
    // catching it here is what turned "go and sign in" into a full page
    // error saying the control plane did not answer.
    if (isNavigation(err)) throw err;
    return <Unreachable detail={err instanceof ApiError ? err.message : String(err)} />;
  }
  if (actor === "no-organization") return <NoOrganization />;

  const action = one("action");
  const before = one("before");

  return (
    <Chrome current="/audit" who={actor.label} org={actor.orgSlug} role={actor.role}>
      <Page>
        <PageHead
          title="Audit log"
          lede="Every action that changed something, newest first, hash-chained so an alteration anywhere is detectable from anywhere after it."
        />

        <Suspense fallback={<ChainSkeleton />}>
          <Chain />
        </Suspense>

        <form className="mb-4 mt-6 flex flex-wrap items-end gap-2.5" method="get">
          <div className="flex min-w-[220px] flex-1 flex-col gap-1.5 sm:max-w-[320px]">
            <label htmlFor="action" className="text-[12.5px] font-medium tracking-snug text-ink">
              Action
            </label>
            <input
              id="action"
              name="action"
              defaultValue={action}
              placeholder="network.rule_proposed"
              spellCheck={false}
              className={inputClass}
            />
          </div>
          <button
            type="submit"
            className="inline-flex h-9 items-center rounded-lg bg-ink px-3.5 text-[13px] font-medium tracking-snug text-white transition-colors hover:bg-[#1c1c1c] active:translate-y-px"
          >
            Filter
          </button>
          {action ? (
            <a
              href="/audit"
              className="inline-flex h-9 items-center rounded-lg border border-edge bg-surface px-3 text-[13px] font-medium tracking-snug text-ink transition-colors hover:bg-sunken"
            >
              Clear
            </a>
          ) : null}
        </form>

        <Suspense
          key={`${action}|${before}`}
          fallback={
            <Panel>
              <TableSkeleton rows={10} columns={5} />
            </Panel>
          }
        >
          <Entries action={action} before={before} />
        </Suspense>
      </Page>
    </Chrome>
  );
}

// ---------------------------------------------------------------------------

async function Chain() {
  let report: ChainReport;
  try {
    report = await query<ChainReport>("audit.verify");
  } catch (err) {
    if (isNavigation(err)) throw err;
    if (err instanceof ApiError && err.code === "FORBIDDEN") {
      // Verifying is an export-level permission. Saying which permission is
      // missing is more useful than an empty box, and it reveals nothing: the
      // permission catalogue is public.
      return (
        <div className="rounded-xl border border-hair bg-surface px-4 py-3.5 text-[13px] text-muted sm:px-5">
          Verifying the chain needs <Mono>audit.export</Mono>, which this role does not have. The
          entries below are still the whole log.
        </div>
      );
    }
    return (
      <Failure
        title="The chain could not be verified"
        detail={err instanceof ApiError ? err.message : String(err)}
      />
    );
  }

  if (!report.ok) {
    return (
      <Failure
        title={`The chain is broken in ${report.problems.length} place${report.problems.length === 1 ? "" : "s"}`}
        detail={
          "An entry has been altered, removed, or relinked. Every break is listed rather than the first, because the extent is what an investigation needs. " +
          report.problems
            .slice(0, 5)
            .map((p) => `#${p.seq} ${p.kind}: ${p.detail}`)
            .join("; ")
        }
      />
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border border-pass/25 bg-pass-tint px-4 py-3.5 sm:px-5">
      <p className="text-[13.5px] font-semibold tracking-snug text-pass">
        {report.entries.toLocaleString("en")} entries, chain intact
      </p>
      <p className="text-[12.5px] text-[#1c5407]">
        Every entry hashes the one before it, so altering any field of any entry breaks every link
        after it.
      </p>
      {report.head ? (
        <p className="text-[12px] text-[#1c5407]">
          head <Mono className="text-[#1c5407]">{report.head.slice(0, 16)}</Mono>
        </p>
      ) : null}
    </div>
  );
}

function ChainSkeleton() {
  return (
    <div className="rounded-xl border border-hair bg-surface px-4 py-3.5 sm:px-5" aria-hidden>
      <div className="skeleton h-4 w-64" />
    </div>
  );
}

// ---------------------------------------------------------------------------

async function Entries({ action, before }: { action: string; before: string }) {
  let entries: AuditEntry[];
  try {
    entries = await query<AuditEntry[]>("audit.list", {
      ...(action ? { action } : {}),
      ...(before && /^\d+$/.test(before) ? { before: Number(before) } : {}),
      limit: 100,
    });
  } catch (err) {
    if (isNavigation(err)) throw err;
    if (err instanceof ApiError && err.code === "FORBIDDEN") {
      return (
        <Panel>
          <Empty
            title="This role cannot read the audit log"
            says="Reading it needs the audit.read permission. An owner or an admin can grant it, and the permission catalogue says which roles carry it."
          />
        </Panel>
      );
    }
    return (
      <Failure
        title="The log could not be read"
        detail={err instanceof ApiError ? err.message : String(err)}
        action={<LinkButton href="/audit">Try again</LinkButton>}
      />
    );
  }

  if (entries.length === 0) {
    return (
      <Panel>
        {action ? (
          <Empty
            title={`Nothing has done ${action}`}
            says="No entry in this organization carries that action. The filter is an exact match rather than a search, so a partial name finds nothing."
            action={<LinkButton href="/audit">Clear the filter</LinkButton>}
          />
        ) : (
          <Empty
            title="Nothing has been audited yet"
            says="An entry is written when somebody changes something: a rule proposed, a member's role changed, an environment torn down. Reading is not audited, which is why opening this page did not create one."
          />
        )}
      </Panel>
    );
  }

  const oldest = entries[entries.length - 1]?.seq;

  return (
    <>
      <Panel title="Entries" note={`${entries.length} shown, newest first`}>
        <TableFrame>
          <thead>
            <tr>
              <Th numeric className="w-[74px]">
                Seq
              </Th>
              <Th>When</Th>
              <Th>Who</Th>
              <Th>Did what</Th>
              <Th>To</Th>
            </tr>
          </thead>
          <tbody>
            {entries.map((entry) => (
              <Tr key={entry.seq}>
                <Td numeric className="align-top text-faint">
                  {entry.seq}
                </Td>
                <Td className="align-top text-[12.5px] text-muted">
                  <When at={entry.occurred_at} />
                </Td>
                <Td className="align-top">
                  <span className="block max-w-[22ch] truncate font-medium tracking-snug text-ink">
                    {entry.actor_label}
                  </span>
                  <Chip tone="quiet">{entry.origin}</Chip>
                </Td>
                <Td className="align-top">
                  <Mono>{entry.action}</Mono>
                  <Detail detail={entry.detail} />
                </Td>
                <Td className="align-top text-[12.5px] text-muted">
                  <span className="block text-faint">{entry.target_type}</span>
                  <span className="block max-w-[26ch] truncate" title={entry.target_id ?? undefined}>
                    {entry.target_id ?? "—"}
                  </span>
                </Td>
              </Tr>
            ))}
          </tbody>
        </TableFrame>
      </Panel>

      {entries.length >= 100 && oldest ? (
        <div className="mt-4 flex justify-center">
          <LinkButton
            href={`/audit?${new URLSearchParams({
              ...(action ? { action } : {}),
              before: String(oldest),
            }).toString()}`}
          >
            Older entries
          </LinkButton>
        </div>
      ) : null}
    </>
  );
}

/**
 * The detail column of one entry.
 *
 * jsonb, so this is whatever the action wrote. Rendered as pairs when it is an
 * object and as its text otherwise, rather than assumed into a shape: an entry
 * whose detail is a string must not take the page down with it, because the
 * one thing an audit log cannot do is stop being readable.
 */
function Detail({ detail: raw }: { detail: unknown }) {
  const detail = asJson(raw);
  if (detail === null || detail === undefined) return null;
  if (typeof detail !== "object" || Array.isArray(detail)) {
    return <p className="mt-1 text-[12.5px] text-muted">{String(detail)}</p>;
  }
  const pairs = Object.entries(detail as Record<string, unknown>).filter(
    ([, value]) => value !== null && value !== undefined && value !== "",
  );
  if (pairs.length === 0) return null;
  return (
    <p className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-[12.5px] text-muted">
      {pairs.map(([key, value]) => (
        <span key={key}>
          <span className="text-faint">{key} </span>
          {typeof value === "object" ? JSON.stringify(value) : String(value)}
        </span>
      ))}
    </p>
  );
}
