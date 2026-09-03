"use client";

import Link from "next/link";
import {
  Badge,
  Bar,
  Card,
  CardSkeleton,
  Empty,
  Loaded,
  Table,
  TableSkeleton,
  TableWrap,
  Td,
  Th,
  When,
} from "@/components/ui";
import { AdminPage } from "@/components/admin/primitives";
import { ADMIN_NAV, type AdminNavItem } from "@/lib/admin-nav";
import { operatorMay, useAdminContext, type Operator } from "@/lib/admin";
import {
  useAdminActivity,
  useAdminInstallation,
  useAdminOperators,
  useAdminStanding,
  type AdminActivity,
  type AdminInstallation,
  type AdminStanding,
} from "@/lib/admin-administration";

/**
 * Where an operator lands.
 *
 * WHAT THIS PAGE IS FOR, decided before anything was drawn. An operator opens
 * it to learn three things in the first five seconds: is anything wrong, is
 * anything unusual, and is anything waiting for me. Everything above the fold
 * answers one of those and nothing else is up there.
 *
 * WHAT IT DELIBERATELY IS NOT. A row of four identical cards with round
 * numbers in them. That layout has no focal point, so the eye has to read all
 * four to find out whether any of them matters, which is the opposite of what
 * this page is for. Worse, it invites filling the fourth card with something
 * nobody would act on at any value. So the shape here is one statement, then a
 * work list, then the supporting totals in a column beside it, and the totals
 * are small on purpose: they are the denominator for the statement, not the
 * point of the page.
 *
 * EVERY NUMBER COMES FROM A QUERY. Not one figure on this screen is a
 * constant, an estimate, or a placeholder, and where a measurement genuinely
 * does not exist the page says so in a sentence. An operator during an incident
 * reads a zero as an answer, and a placeholder zero is the most expensive thing
 * this portal could ship.
 *
 * IT COMPOSES FOUR ROUTES BEHIND FOUR PERMISSIONS, and asks only for the ones
 * the role holds. A section is absent rather than refused: a landing page that
 * always shows one "your role cannot see this" panel teaches its reader to
 * ignore refusals, which is the last habit an operator portal should build.
 *
 * THE DIRECTORY AT THE BOTTOM IS KEPT. Twenty two sections is more than anybody
 * holds in their head, and the shell's own argument for it stands: it says what
 * is here, filtered to the role, from the same list the rail draws. What has
 * changed is that it is no longer the whole page, because the sections it
 * points at now exist and can be summarised.
 */
export default function AdminOverviewPage() {
  const { me } = useAdminContext();

  const mayReadTenants = operatorMay(me, "admin.tenants.read");
  const mayReadAudit = operatorMay(me, "admin.audit.read");
  const mayReadOperators = operatorMay(me, "admin.operators.read");
  const mayReadInfra = operatorMay(me, "admin.infra.read");

  const standing = useAdminStanding(mayReadTenants);
  const activity = useAdminActivity(mayReadAudit);
  const operators = useAdminOperators(mayReadOperators);
  const installation = useAdminInstallation(mayReadInfra);

  /*
   * WHETHER EACH CHECK ACTUALLY ANSWERED, not whether it was allowed to be
   * asked. The statement below is assembled from three routes that return at
   * different times, and an earlier version read whichever had arrived. So on
   * a slow control plane the page rendered "Nothing is engaged and nothing is
   * waiting" for as long as the switches took to load, and then changed its
   * mind. A front page that says all clear while maintenance is engaged is the
   * exact failure this page exists to prevent, and it is worse for being
   * momentary: the reader has already gone somewhere else.
   *
   * So a source is CHECKED only when it is ready. Still loading holds the
   * statement back; refused or failed makes it say what it could not read
   * rather than implying it read everything.
   */
  const controlsChecked = mayReadInfra && installation.status === "ready";
  const operatorsChecked = mayReadOperators && operators.status === "ready";
  const waiting =
    (mayReadInfra && installation.status === "loading") ||
    (mayReadOperators && operators.status === "loading");
  const unread = [
    mayReadInfra && installation.status === "error" ? "the installation switches" : null,
    mayReadOperators && operators.status === "error" ? "the operator accounts" : null,
  ].filter((x): x is string => x !== null);

  const groups = ADMIN_NAV.map((group) => ({
    ...group,
    items: group.items.filter((item) => operatorMay(me, item.permission)),
  })).filter((group) => group.items.length > 0);

  return (
    <AdminPage
      href="/admin"
      lede={
        me
          ? `Signed in as ${me.email}, with the ${me.role.replace(/_/g, " ")} role. Everything below is what that role can reach.`
          : undefined
      }
    >
      {mayReadTenants ? (
        <Loaded state={standing} skeleton={<StandingSkeleton />}>
          {(data) =>
            waiting ? (
              <StandingSkeleton />
            ) : (
              <Standing
                standing={data}
                controls={controlsChecked ? (installation.data?.controls ?? []) : null}
                operators={operatorsChecked ? (operators.data ?? []) : null}
                unread={unread}
              />
            )
          }
        </Loaded>
      ) : null}

      <div className="mt-7 grid items-start gap-5 lg:grid-cols-[minmax(0,1fr)_300px]">
        <div className="grid min-w-0 gap-5">
          {mayReadTenants ? (
            <Card title="What needs an operator">
              <Loaded state={standing} skeleton={<CardSkeleton count={2} />}>
                {(data) => (
                  <Attention
                    standing={data}
                    controls={controlsChecked ? (installation.data?.controls ?? []) : null}
                    operators={operatorsChecked ? (operators.data ?? []) : null}
                    checkedOperators={operatorsChecked}
                    checkedControls={controlsChecked}
                  />
                )}
              </Loaded>
            </Card>
          ) : null}

          {mayReadAudit ? (
            <Card
              title="Recent operator actions"
              note="Writes only. Reads are counted and left out."
              actions={
                <Link
                  href="/admin/security/audit"
                  className="inline-flex min-h-11 items-center text-[13px] text-muted underline decoration-transparent underline-offset-4 hover:text-ink hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
                >
                  Full chain
                </Link>
              }
            >
              <Loaded state={activity} skeleton={<TableSkeleton rows={4} cols={4} />}>
                {(data) => <RecentActions activity={data} />}
              </Loaded>
            </Card>
          ) : null}
        </div>

        <div className="grid min-w-0 gap-5">
          {mayReadTenants ? (
            <Card title="The installation">
              <Loaded state={standing} skeleton={<FiguresSkeleton />}>
                {(data) => (
                  <Figures
                    standing={data}
                    activity={mayReadAudit ? activity.data : null}
                    installation={mayReadInfra ? installation.data : null}
                  />
                )}
              </Loaded>
            </Card>
          ) : null}
        </div>
      </div>

      <h2 className="mt-9 text-[13px] font-medium uppercase tracking-[0.08em] text-dim">
        Everything this role can reach
      </h2>
      {groups.length === 0 ? (
        <Card className="mt-3">
          <div className="px-6 py-12 text-center">
            <p className="text-[14px] font-medium text-ink">Your role opens nothing here yet</p>
            <p className="mx-auto mt-2 max-w-[52ch] text-[13px] leading-6 text-muted">
              You can sign in to the portal, and no section is granted to your role. An operator
              who can grant permissions can change that from Admins &amp; Permissions.
            </p>
          </div>
        </Card>
      ) : (
        // items-start, so a card ends where its content ends. Grid items
        // stretch to the tallest in their row by default, and the groups have
        // three entries and five, so Customers was drawn as tall as Product
        // with two hundred pixels of nothing inside it. That reads as a
        // section that failed to load rather than as a short one.
        //
        // mt-3 is the administration lane's, separating the directory from the
        // statement above it that this page now leads with.
        <div className="mt-3 grid items-start gap-5 lg:grid-cols-2">
          {groups.map((group) => (
            <Card key={group.slug} title={group.label}>
              <ul>
                {group.items.map((item) => (
                  <SectionLink key={item.href} item={item} />
                ))}
              </ul>
            </Card>
          ))}
        </div>
      )}
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * The statement
 * ---------------------------------------------------------------------- */

/**
 * The one sentence this page exists to say.
 *
 * A sentence rather than a widget, and set on the page background rather than
 * inside a card, so it reads as the page speaking and carries the weight
 * without a coloured panel around it. The badge beside it is the only colour,
 * and the words say the same thing the colour does, because colour is never the
 * only signal here.
 *
 * The order of the checks is the order an operator escalates through: an
 * installation-wide switch outranks a suspended account, which outranks a
 * deletion that stopped, which outranks an account nobody has provisioned. The
 * page names the worst thing first and counts the rest, rather than listing
 * four things of different sizes in whatever order they were queried.
 */
function Standing({
  standing,
  controls,
  operators,
  unread,
}: {
  standing: AdminStanding;
  /** Null when this check did not answer, which is not the same as an empty
   *  list. The statement below never treats the two alike. */
  controls: AdminInstallation["controls"] | null;
  operators: Operator[] | null;
  /** The checks that were allowed and failed, named so the reader knows the
   *  answer is partial. */
  unread: string[];
}) {
  const engaged = (controls ?? []).filter((c) => c.engaged);
  const unprovisioned = (operators ?? []).filter((o) => !o.provisioned && !o.suspended);
  const { suspended, stuckDeletions } = standing;

  const headline =
    engaged.length > 0
      ? engaged.length === 1
        ? `${engaged[0]!.title} is engaged.`
        : `${engaged.length} installation switches are engaged.`
      : stuckDeletions.length > 0
        ? `${count(stuckDeletions.length, "customer deletion", "customer deletions")} stopped part way.`
        : suspended.length > 0
          ? `${count(suspended.length, "organization is", "organizations are")} suspended.`
          : unprovisioned.length > 0
            ? `${count(unprovisioned.length, "operator account has", "operator accounts have")} never been provisioned.`
            : unread.length > 0
              ? "Nothing found in the checks that answered."
              : "Nothing is engaged and nothing is waiting.";

  // The badge follows the SAME cascade as the headline, and that is not a
  // refactor for tidiness. An earlier version derived the tone from a shorter
  // list, so the page rendered "All clear" in green directly above the sentence
  // "2 operator accounts have never been provisioned" and a work list with an
  // item in it. A summary that disagrees with the thing it is summarising is
  // worse than no summary: the reader believes the green.
  const tone =
    engaged.length > 0 || stuckDeletions.length > 0
      ? "fail"
      : suspended.length > 0 || unprovisioned.length > 0 || unread.length > 0
        ? "warn"
        : "pass";
  const label =
    tone === "fail"
      ? "Needs attention"
      : unread.length > 0
        ? "Partly read"
        : tone === "warn"
          ? "Worth a look"
          : "All clear";

  const found =
    engaged.length > 0
      ? `While a switch is engaged the product refuses work on purpose. ${engaged
          .map((c) => c.title)
          .join(", ")}. Release it from Incidents & Kill Switches.`
      : stuckDeletions.length > 0
        ? "A customer asked to be deleted and the pipeline raised. Nothing about the product looks wrong from the outside while this is true."
        : suspended.length > 0
          ? "A suspended organization cannot start new work. Environments it already has keep running."
          : unprovisioned.length > 0
            ? "An account with no password cannot be signed in to, so it is waiting for somebody rather than in use."
            : `Read across ${count(standing.organizations.total, "organization", "organizations")} and ${count(standing.environments.live, "live environment", "live environments")}.`;

  // The unread clause is appended to EVERY branch, not only to the one where
  // nothing was found. A reader who sees "Partly read" beside a real finding
  // still has to be told which question went unanswered, or the badge is a
  // warning with no subject.
  const supporting =
    unread.length === 0
      ? found
      : `${found} ${sentence(unread.join(" and "))} could not be read, so this is not the whole picture.`;

  return (
    <section aria-labelledby="standing">
      <div className="flex flex-wrap items-center gap-3">
        <Badge tone={tone}>{label}</Badge>
        <span className="text-[12px] text-dim">
          Measured <When value={standing.at} />
        </span>
      </div>
      <p
        id="standing"
        className="mt-2.5 max-w-[26ch] text-[22px] font-semibold leading-dense tracking-extra-tight text-ink sm:max-w-[34ch] sm:text-[26px]"
      >
        {headline}
      </p>
      <p className="mt-2.5 max-w-[64ch] text-[13.5px] leading-6 text-muted">{supporting}</p>
    </section>
  );
}

function StandingSkeleton() {
  return (
    <section>
      <Bar className="h-5 w-24" />
      <div className="mt-3">
        <Bar className="h-6 w-[min(320px,80%)]" />
      </div>
      <div className="mt-3">
        <Bar className="h-4 w-[min(420px,95%)]" />
      </div>
    </section>
  );
}

/* -------------------------------------------------------------------------
 * The work list
 * ---------------------------------------------------------------------- */

interface Item {
  key: string;
  count: number;
  title: string;
  detail: string;
  href: string;
  action: string;
  tone: "fail" | "warn";
}

/**
 * The things somebody has to do something about, and nothing else.
 *
 * An item appears only when its count is above zero, so this list is empty
 * exactly when there is no work, and the empty state says which questions were
 * asked rather than only that the answer was none. "Nothing needs you" with no
 * list of what was checked is indistinguishable from a panel that failed to
 * load.
 *
 * The questions it cannot ask are named too. An operator whose role cannot read
 * operator accounts should not be told the queue is clear when nobody looked.
 */
function Attention({
  standing,
  controls,
  operators,
  checkedOperators,
  checkedControls,
}: {
  standing: AdminStanding;
  controls: AdminInstallation["controls"] | null;
  operators: Operator[] | null;
  checkedOperators: boolean;
  checkedControls: boolean;
}) {
  const engaged = (controls ?? []).filter((c) => c.engaged);
  const unprovisioned = (operators ?? []).filter((o) => !o.provisioned && !o.suspended);

  const items: Item[] = [];

  if (engaged.length > 0) {
    items.push({
      key: "controls",
      count: engaged.length,
      title: engaged.length === 1 ? "An installation switch is engaged" : "Installation switches are engaged",
      detail: engaged
        .map((c) => `${c.title}${c.reason ? `, because ${c.reason}` : ""}`)
        .join(". "),
      href: "/admin/operations/incidents",
      action: "Review the switches",
      tone: "fail",
    });
  }

  if (standing.stuckDeletions.length > 0) {
    items.push({
      key: "deletions",
      count: standing.stuckDeletions.length,
      title:
        standing.stuckDeletions.length === 1
          ? "A customer deletion stopped part way"
          : "Customer deletions stopped part way",
      detail: `${standing.stuckDeletions
        .slice(0, 3)
        .map((d) => `${d.slug} at ${d.step ?? "an unrecorded step"}`)
        .join(", ")}${
        standing.stuckDeletions.length > 3
          ? `, and ${standing.stuckDeletions.length - 3} more`
          : ""
      }. Each one has been retried and is still failing.`,
      href: "/admin/security/data-governance",
      action: "Open data governance",
      tone: "fail",
    });
  }

  if (standing.suspended.length > 0) {
    items.push({
      key: "suspended",
      count: standing.organizations.suspended,
      title:
        standing.organizations.suspended === 1
          ? "An organization is suspended"
          : "Organizations are suspended",
      detail: standing.suspended
        .slice(0, 3)
        .map((o) => `${o.slug}${o.reason ? `, ${o.reason}` : ""}`)
        .join(". "),
      href: "/admin/customers/users",
      action: "Open organizations",
      tone: "warn",
    });
  }

  if (unprovisioned.length > 0) {
    items.push({
      key: "operators",
      count: unprovisioned.length,
      title:
        unprovisioned.length === 1
          ? "An operator account is waiting to be provisioned"
          : "Operator accounts are waiting to be provisioned",
      detail: `${unprovisioned
        .slice(0, 3)
        .map((o) => o.email)
        .join(", ")} cannot sign in until a password is set out of band. There is no default ` +
        "credential anywhere in this product.",
      href: "/admin/administration/admins",
      action: "Open admins",
      tone: "warn",
    });
  }

  if (items.length === 0) {
    return (
      <Empty title="Nothing needs an operator right now">
        Checked: organizations suspended, customer deletions that stopped part way
        {checkedOperators ? ", operator accounts waiting to be provisioned" : ""}
        {checkedControls ? ", and installation switches" : ""}.
        {checkedOperators && checkedControls
          ? ""
          : " Your role cannot read everything on this page, so the questions not listed were not asked."}
      </Empty>
    );
  }

  return (
    <ul>
      {items.map((item) => (
        <li key={item.key} className="border-b border-rule last:border-b-0">
          <div className="flex flex-wrap items-start gap-x-3 gap-y-2 px-4 py-3.5">
            <span className="mt-0.5 shrink-0">
              <Badge tone={item.tone}>{item.count}</Badge>
            </span>
            <span className="min-w-0 flex-1 basis-[16rem]">
              <span className="block text-[13px] font-medium leading-5 text-ink">{item.title}</span>
              <span className="mt-1 block break-words text-[12.5px] leading-5 text-muted">
                {item.detail}
              </span>
            </span>
            <Link
              href={item.href}
              className="inline-flex min-h-11 shrink-0 items-center whitespace-nowrap text-[13px] text-ink underline decoration-[rgba(16,16,16,0.25)] underline-offset-4 hover:decoration-ink sm:min-h-0"
            >
              {item.action}
            </Link>
          </div>
        </li>
      ))}
    </ul>
  );
}

/* -------------------------------------------------------------------------
 * The supporting totals
 * ---------------------------------------------------------------------- */

/**
 * The denominators, in a column rather than a row of cards.
 *
 * Small, right aligned, tabular. These are what the statement above is read
 * against: "three suspended" means something different out of nine than out of
 * nine hundred. A row of large cards would give them the same weight as the
 * statement, which is exactly backwards.
 */
function Figures({
  standing,
  activity,
  installation,
}: {
  standing: AdminStanding;
  activity: AdminActivity | null;
  installation: AdminInstallation | null;
}) {
  const rows: { label: string; value: string; note?: string }[] = [
    { label: "Organizations", value: standing.organizations.total.toLocaleString() },
    { label: "Suspended", value: standing.organizations.suspended.toLocaleString() },
    { label: "Live environments", value: standing.environments.live.toLocaleString() },
  ];

  if (activity) {
    rows.push(
      {
        label: "Operator writes",
        value: activity.writes.toLocaleString(),
        note: `of the last ${activity.readOver.toLocaleString()} recorded actions`,
      },
      { label: "Refusals", value: activity.refusals.toLocaleString() },
    );
  }

  if (installation?.schema) {
    rows.push({
      label: "Schema",
      // The migration file name is the version. Trimmed to its number here
      // because the column is narrow and the full name is on System
      // Configuration, which is the page for it.
      value: installation.schema.version.slice(0, 4),
      note: `${installation.schema.applied} migrations applied`,
    });
  }

  return (
    <dl className="px-4 py-1.5">
      {rows.map((r) => (
        <div
          key={r.label}
          className="flex items-baseline justify-between gap-4 border-b border-rule py-2.5 last:border-b-0"
        >
          <dt className="min-w-0 text-[12.5px] leading-5 text-muted">
            {r.label}
            {r.note ? <span className="mt-0.5 block text-[11.5px] text-dim">{r.note}</span> : null}
          </dt>
          <dd className="tnum shrink-0 text-[15px] font-medium leading-5 text-ink">{r.value}</dd>
        </div>
      ))}
    </dl>
  );
}

function FiguresSkeleton() {
  return (
    <div className="px-4 py-1.5">
      {[0, 1, 2].map((i) => (
        <div key={i} className="flex items-center justify-between gap-4 border-b border-rule py-3 last:border-b-0">
          <Bar className="h-3.5 w-24" />
          <Bar className="h-3.5 w-10" />
        </div>
      ))}
    </div>
  );
}

/* -------------------------------------------------------------------------
 * Recent operator actions
 * ---------------------------------------------------------------------- */

function RecentActions({ activity }: { activity: AdminActivity }) {
  if (activity.recent.length === 0) {
    return (
      <Empty title="No operator has changed anything yet">
        Every write on this installation is recorded here in a hash chain. Reads are recorded too
        and are left out of this list.
      </Empty>
    );
  }
  return (
    <TableWrap>
      <Table>
        <thead>
          <tr>
            <Th>Action</Th>
            <Th>Operator</Th>
            <Th>Organization</Th>
            <Th>When</Th>
          </tr>
        </thead>
        <tbody>
          {activity.recent.map((e) => (
            <tr key={e.seq}>
              <Td>
                <span className="block truncate font-medium text-ink">{e.action}</span>
                {/* The kind of thing, not its identifier. A uuid here is forty
                    characters nobody reads on a summary, and it widened this
                    column until the table scrolled sideways inside its card on
                    a 1280px screen. The full chain carries the id. */}
                <span className="mt-0.5 block truncate text-[12px] text-muted">{e.targetType}</span>
              </Td>
              <Td label="Operator">{e.actor}</Td>
              <Td label="Organization">
                {/* Null means the action was installation-wide, which is the
                    whole reason the operator chain is a separate table. Said in
                    a word rather than left blank, because a blank cell reads as
                    data somebody failed to load. */}
                {e.organization ?? <span className="text-dim">Installation</span>}
              </Td>
              <Td label="When">
                <When value={e.occurredAt} />
              </Td>
            </tr>
          ))}
        </tbody>
      </Table>
    </TableWrap>
  );
}

/* -------------------------------------------------------------------------
 * The directory
 * ---------------------------------------------------------------------- */

/**
 * One section, as a row somebody can hit with a thumb.
 *
 * The whole row is the anchor rather than the label being a link inside it, so
 * there is exactly one focusable target per section and the tab order through
 * this page is the reading order. The icon is the same glyph the rail draws, so
 * finding a section here teaches you where it is there.
 */
function SectionLink({ item }: { item: AdminNavItem }) {
  const { Icon } = item;
  return (
    <li className="border-b border-rule last:border-b-0">
      <Link
        href={item.href}
        className="flex min-h-11 items-start gap-3 px-4 py-3 transition-colors hover:bg-[rgba(16,16,16,0.035)]"
      >
        <Icon className="mt-0.5 h-4 w-4 shrink-0 text-muted" />
        <span className="min-w-0">
          <span className="block text-[13.5px] font-medium leading-5 text-ink">{item.label}</span>
          <span className="mt-0.5 block text-[12.5px] leading-5 text-muted">{item.summary}</span>
        </span>
      </Link>
    </li>
  );
}

/** Starts a sentence with a capital, because these clauses are assembled from
 *  fragments and one of them follows a full stop. */
function sentence(text: string): string {
  return text.charAt(0).toUpperCase() + text.slice(1);
}

/** "1 organization" and "2 organizations", because a count that reads as a
 *  sentence has to agree with itself. */
function count(n: number, one: string, many: string): string {
  return `${n.toLocaleString()} ${n === 1 ? one : many}`;
}
