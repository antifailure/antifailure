"use client";

/**
 * The pieces twenty two operator sections are built out of.
 *
 * WHY THIS FILE EXISTS AT ALL, given components/ui.tsx already holds the
 * console's design system. Because the alternative, on a portal this size, is
 * twenty two tables with twenty two ideas about where the checkbox goes. These
 * are not a second design system and they do not restyle anything: every one of
 * them is assembled out of ui.tsx's own Card, Table, Th, Td, Badge, Button and
 * the three states, using the console's tokens, so a section built with them
 * looks like it was always there.
 *
 * WHAT IS DELIBERATELY NOT HERE. Nothing that decides what a page means. A
 * component that fetched, or that knew which permission a section needs, would
 * be a place for twenty two pages to disagree about the answer.
 *
 * ONE RULE RUNS THROUGH ALL OF IT: a component never invents a value it was not
 * given. `Metric` says a number is not measured rather than printing a zero,
 * `DataTable` refuses to offer a sort it cannot actually perform, and `Planned`
 * says a section is not built rather than showing an empty dashboard that looks
 * like a platform with nothing on it. A console whose blank means two things is
 * a console nobody can act on.
 */

import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import Link from "next/link";
import {
  Badge,
  Button,
  Card,
  Empty,
  Page,
  Table,
  TableWrap,
  Td,
  Th,
  toneFor,
  inputClass,
  selectClass,
  type Tone,
} from "@/components/ui";
import { navItemFor, type AdminNavItem } from "@/lib/admin-nav";

/* -------------------------------------------------------------------------
 * The top of a section
 * ---------------------------------------------------------------------- */

/**
 * The heading every operator section wears, taken from the navigation.
 *
 * Pass the section's own route and the title comes from the SAME line that
 * drew the rail entry, so the two cannot disagree. That is the whole reason
 * this wrapper exists rather than each page passing a string: three of the
 * pages that already existed were titled "Tenants", "Operator log" and
 * "Operators" while the rail beside them said something else, and a reader
 * cannot tell whether they arrived where they clicked.
 *
 * A page with no navigation entry of its own, such as the detail of one
 * record, passes `title` instead. It is not in the rail, so there is nothing
 * for it to drift from.
 */
export function AdminPage({
  href,
  title,
  lede,
  actions,
  children,
}: {
  /** The section's route, exactly as declared in lib/admin-nav.ts. */
  href?: string;
  /** For a page the navigation does not list. Ignored when `href` resolves. */
  title?: string;
  /** Overrides the navigation's one line summary. */
  lede?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
}) {
  const item = href ? navItemFor(href) : null;
  return (
    <Page
      title={item?.label ?? title ?? "Operator portal"}
      lede={lede ?? item?.summary}
      actions={actions}
    >
      {children}
    </Page>
  );
}

/* -------------------------------------------------------------------------
 * A section nobody has built yet
 * ---------------------------------------------------------------------- */

/**
 * What stands at a route whose section has not been written.
 *
 * Every entry in the navigation opens something from the first commit, because
 * a rail with a dead link in it teaches the reader to distrust the rail, and
 * they cannot tell a section that is coming from one that is broken. So this
 * says which of the two it is, out loud, and names what will live here so the
 * reader knows whether to wait or to go somewhere else.
 *
 * It is NOT an empty dashboard. A page of zeroes and empty tables is the single
 * most expensive thing this portal could ship: an operator during an incident
 * reads "0 failed runs" as an answer, and it was a placeholder.
 */
export function Planned({ item }: { item: AdminNavItem }) {
  return (
    <Card>
      <div className="px-6 py-12 text-center">
        <p className="text-[14px] font-medium text-ink">This section is not built yet</p>
        <p className="mx-auto mt-2 max-w-[52ch] text-[13px] leading-6 text-muted">{item.summary}</p>
        <p className="mx-auto mt-4 max-w-[52ch] text-[12.5px] leading-5 text-dim">
          Nothing is missing and nothing has failed. The route exists so the navigation has no dead
          entry in it while the section is written. Reading it will need the{" "}
          <code className="font-mono text-[12px] text-muted">{item.permission}</code> permission.
        </p>
      </div>
    </Card>
  );
}

/**
 * A whole section that has not been written: its heading and its explanation.
 *
 * The entire body of nineteen page files, so that replacing one with a real
 * section is deleting a line rather than unpicking a layout, and so that all
 * nineteen say the same thing in the same place until they are.
 *
 * A route with no navigation entry cannot reach this, and it says so rather
 * than rendering an untitled page: an href that does not match the navigation
 * is a typo in one of the two, and a silent blank is how it survives review.
 */
export function PlannedSection({ href }: { href: string }) {
  const item = navItemFor(href);
  if (!item) {
    return (
      <AdminPage title="Unknown section">
        <Card>
          <div className="px-6 py-12 text-center" role="alert">
            <p className="text-[14px] font-medium text-ink">This route is not in the navigation</p>
            <p className="mx-auto mt-2 max-w-[52ch] text-[13px] leading-6 text-muted">
              Nothing declares <code className="font-mono text-[12px]">{href}</code> in
              lib/admin-nav.ts, so the portal cannot say what belongs here. One of the two is
              misspelled.
            </p>
          </div>
        </Card>
      </AdminPage>
    );
  }
  return (
    <AdminPage href={href}>
      <Planned item={item} />
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * A record's fields
 * ---------------------------------------------------------------------- */

export interface Fact {
  label: string;
  /** Anything renderable. Pass `null` for a field the record genuinely does
   *  not have, and it says so rather than leaving a blank the reader has to
   *  interpret. */
  value: ReactNode;
  /** Identifiers, hashes and anything else read character by character. */
  mono?: boolean;
}

/**
 * The fields of one record, as a real description list.
 *
 * A `dl` rather than a two column table, because these are not rows of the same
 * kind of thing and a screen reader announces the pairing for free. Two columns
 * above the phone breakpoint and stacked below it, which is the same decision
 * the console's tables make at the same width.
 */
export function Facts({ facts }: { facts: Fact[] }) {
  return (
    <dl className="grid gap-x-8 gap-y-3.5 px-4 py-4 sm:grid-cols-[minmax(0,180px)_minmax(0,1fr)]">
      {facts.map((f) => (
        <div key={f.label} className="contents">
          <dt className="text-[12px] font-medium leading-5 text-dim sm:pt-px">{f.label}</dt>
          <dd
            className={`min-w-0 break-words text-[13px] leading-5 text-ink ${
              f.mono ? "font-mono text-[12px]" : ""
            }`}
          >
            {f.value === null || f.value === undefined ? (
              <span className="text-dim">Not set</span>
            ) : (
              f.value
            )}
          </dd>
        </div>
      ))}
    </dl>
  );
}

/* -------------------------------------------------------------------------
 * A status
 * ---------------------------------------------------------------------- */

/**
 * A state, coloured by what the word means.
 *
 * `Badge` with `toneFor` applied, which is what every screen in the console was
 * already writing by hand and getting subtly different: some passed a tone and
 * some did not, so "failed" was red on one page and grey on the next. Colour is
 * never the only signal here; the word is, and the colour agrees with it.
 *
 * Nothing in it animates. A live status does not earn a pulse: a dot that
 * throbs while the reader is doing nothing is the loudest thing on the page and
 * it says exactly as much as a still one.
 */
export function StatusChip({ value, tone }: { value: string | null | undefined; tone?: Tone }) {
  if (value === null || value === undefined || value === "") {
    return <span className="text-dim">Unknown</span>;
  }
  return <Badge tone={tone ?? toneFor(value)}>{value.replace(/_/g, " ")}</Badge>;
}

/* -------------------------------------------------------------------------
 * Numbers
 * ---------------------------------------------------------------------- */

export interface MetricSpec {
  label: string;
  /**
   * The measurement, or null when there is not one.
   *
   * Null is not the same as zero and this component is the place that refuses
   * to confuse them. A count of zero failing runs is an answer; a count nobody
   * measured is not, and printing it as 0 turns a gap in the instrumentation
   * into a reassurance.
   */
  value: number | string | null | undefined;
  /** Appended to a number, never to a string. "runs", "GB", "%". */
  unit?: string;
  /** One short line under the number, for what it is counted over. */
  note?: string;
}

/** One measurement. Tabular figures, so a row of them lines up. */
export function Metric({ label, value, unit, note }: MetricSpec) {
  const missing = value === null || value === undefined;
  return (
    <div className="min-w-0 px-4 py-3.5">
      <p className="text-[11.5px] font-medium uppercase tracking-[0.08em] text-dim">{label}</p>
      {missing ? (
        <p className="mt-1.5 text-[13px] leading-6 text-muted">Not measured</p>
      ) : (
        <p className="mt-1 flex items-baseline gap-1.5">
          <span className="tnum text-[22px] font-semibold leading-dense tracking-tighter text-ink">
            {typeof value === "number" ? value.toLocaleString() : value}
          </span>
          {unit ? <span className="text-[12.5px] text-muted">{unit}</span> : null}
        </p>
      )}
      {note ? <p className="mt-1 text-[12px] leading-5 text-dim">{note}</p> : null}
    </div>
  );
}

/**
 * A row of measurements on one surface.
 *
 * A grid that reflows rather than a fixed set of columns, so three metrics and
 * seven both look deliberate, and so it stacks to one column under the phone
 * breakpoint instead of squeezing four numbers into 320 pixels.
 */
export function MetricRow({ metrics }: { metrics: MetricSpec[] }) {
  if (metrics.length === 0) return null;
  return (
    <div className="grid grid-cols-1 divide-y divide-rule overflow-hidden rounded-lg border border-rule bg-card sm:grid-cols-2 sm:divide-y-0 lg:grid-cols-[repeat(auto-fit,minmax(170px,1fr))]">
      {metrics.map((m, i) => (
        <div
          key={m.label}
          // The divider between columns rather than around every cell, so the
          // row reads as one surface. Suppressed on the first, or the leftmost
          // metric gets a rule against the card's own border.
          className={i > 0 ? "sm:border-l sm:border-rule" : ""}
        >
          <Metric {...m} />
        </div>
      ))}
    </div>
  );
}

/* -------------------------------------------------------------------------
 * Filtering
 * ---------------------------------------------------------------------- */

export interface FilterSpec {
  label: string;
  value: string;
  onChange: (next: string) => void;
  options: { value: string; label: string }[];
}

/**
 * The search box and the filters above a list.
 *
 * The search input is a real `type="search"` inside a real `form` with a
 * `role="search"` landmark, so it is reachable by landmark navigation and the
 * on-screen keyboard offers Search rather than Return. Its label is visually
 * hidden rather than absent: a placeholder is not a name, and a field whose
 * only name is placeholder text has no name at all once somebody types in it.
 *
 * SUBMITTED RATHER THAN DEBOUNCED. Every filter here narrows a SERVER side
 * query, and a keystroke that fires one is a query per character against a list
 * of every organization on the installation. The reader also gets to finish
 * typing an account name before the table moves under them.
 */
export function FilterBar({
  search,
  filters = [],
  actions,
}: {
  search?: {
    value: string;
    onChange: (next: string) => void;
    /** What is being searched, for the field's accessible name. */
    label: string;
    placeholder?: string;
  };
  filters?: FilterSpec[];
  actions?: ReactNode;
}) {
  const id = useId();
  const [draft, setDraft] = useState(search?.value ?? "");

  // The committed value is the source of truth. When a page resets it, such as
  // after clearing a filter, the box follows rather than keeping a stale word
  // that no longer describes what is on screen.
  useEffect(() => {
    setDraft(search?.value ?? "");
  }, [search?.value]);

  if (!search && filters.length === 0 && !actions) return null;

  return (
    <div className="flex flex-wrap items-end gap-3 border-b border-rule px-4 py-3">
      {search ? (
        <form
          role="search"
          className="flex min-w-0 flex-1 basis-[240px] items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            search.onChange(draft.trim());
          }}
        >
          <div className="min-w-0 flex-1">
            <label htmlFor={`${id}-q`} className="sr-only">
              {search.label}
            </label>
            <input
              id={`${id}-q`}
              type="search"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              placeholder={search.placeholder ?? "Search"}
              // mt-0 undoes inputClass's spacing, which exists for a field
              // sitting under its own visible label. This one has none.
              className={`${inputClass} mt-0`}
              autoComplete="off"
              spellCheck={false}
            />
          </div>
          <Button type="submit">Search</Button>
        </form>
      ) : null}

      {filters.map((f) => (
        <div key={f.label} className="min-w-0">
          <label
            htmlFor={`${id}-${f.label}`}
            className="mb-1.5 block text-[12px] font-medium text-muted"
          >
            {f.label}
          </label>
          <select
            id={`${id}-${f.label}`}
            className={selectClass}
            value={f.value}
            onChange={(e) => f.onChange(e.target.value)}
          >
            {f.options.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </div>
      ))}

      {actions ? <div className="flex items-center gap-2">{actions}</div> : null}
    </div>
  );
}

/* -------------------------------------------------------------------------
 * A list
 * ---------------------------------------------------------------------- */

export type SortDirection = "asc" | "desc";

export interface Column<T> {
  /** Stable name, sent to the server when this column is sorted on. */
  key: string;
  header: string;
  cell: (row: T) => ReactNode;
  /** Right aligned with tabular figures, which is the only way a column of
   *  numbers can be compared down its length. */
  numeric?: boolean;
  mono?: boolean;
  /** Set only on columns the SERVER can order by. See the note on `sort`. */
  sortable?: boolean;
}

export interface Selection<T> {
  /** The ids currently ticked. Held by the page, because what the page does
   *  with a selection is the page's business. */
  selected: ReadonlySet<string>;
  onChange: (next: ReadonlySet<string>) => void;
  idOf: (row: T) => string;
  /** Singular and plural, for the count beside the checkbox. */
  noun: { one: string; many: string };
}

/**
 * A table of rows, with the four things every list in this portal needs.
 *
 * SORTING IS SERVER SIDE OR IT IS ABSENT, and that is the load bearing decision
 * in this component. A page here shows fifty rows out of an installation with
 * thousands, so sorting the rows in the browser would reorder the fifty and
 * present the result as the top of the list. It is a confident wrong answer, of
 * exactly the kind that gets acted on during an incident. So `onSort` is what
 * makes a header a button: pass it and the column headers sort by asking the
 * server again; leave it out and no header claims to sort. There is no branch
 * in here that reorders anything locally.
 *
 * SELECTION IS PER PAGE and says so. The header checkbox ticks the rows that
 * are loaded, and the label counts them, because "select all" over a keyset
 * paged list is a promise this component cannot keep.
 *
 * The column heading is written into each cell's `data-label`, so the stacked
 * phone layout in globals.css labels every field with the exact string in the
 * header above it. The two cannot drift, because there is only one of them.
 */
export function DataTable<T>({
  columns,
  rows,
  keyOf,
  sort,
  onSort,
  selection,
  href,
  empty,
  footer,
}: {
  columns: Column<T>[];
  rows: T[];
  keyOf: (row: T) => string;
  sort?: { key: string; direction: SortDirection };
  /** Present means the sortable columns are buttons that re-query. */
  onSort?: (key: string, direction: SortDirection) => void;
  selection?: Selection<T>;
  /** Makes the first cell a link, which is what gives the row one focusable,
   *  announced, Enter-activated target. A row that is only clickable is
   *  invisible to a keyboard. */
  href?: (row: T) => string;
  empty: ReactNode;
  /** Usually `More` from components/pagination.tsx. */
  footer?: ReactNode;
}) {
  const selectAllId = useId();

  if (rows.length === 0) {
    return (
      <>
        {empty}
        {footer}
      </>
    );
  }

  const ids = selection ? rows.map(selection.idOf) : [];
  const allTicked = selection !== undefined && ids.length > 0 && ids.every((id) => selection.selected.has(id));
  const someTicked = selection !== undefined && ids.some((id) => selection.selected.has(id));

  function toggleAll(next: boolean) {
    if (!selection) return;
    const set = new Set(selection.selected);
    for (const id of ids) {
      if (next) set.add(id);
      else set.delete(id);
    }
    selection.onChange(set);
  }

  function toggleOne(id: string, next: boolean) {
    if (!selection) return;
    const set = new Set(selection.selected);
    if (next) set.add(id);
    else set.delete(id);
    selection.onChange(set);
  }

  return (
    <>
      {selection ? (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-rule px-4 py-2.5">
          <span className="flex items-center gap-2">
            <input
              id={selectAllId}
              type="checkbox"
              className="h-4 w-4 accent-ink"
              checked={allTicked}
              ref={(el) => {
                // Partly ticked is a third state the DOM has and the attribute
                // does not, so it is set here rather than declared.
                if (el) el.indeterminate = !allTicked && someTicked;
              }}
              onChange={(e) => toggleAll(e.target.checked)}
            />
            <label htmlFor={selectAllId} className="text-[12.5px] text-muted">
              {/* Counts the rows that are LOADED, because those are the ones
                  this checkbox can tick. A list that is still paging cannot
                  honestly offer to select what it has not fetched. */}
              Select the {rows.length}{" "}
              {rows.length === 1 ? selection.noun.one : selection.noun.many} shown
            </label>
          </span>
          {selection.selected.size > 0 ? (
            <span aria-live="polite" className="text-[12.5px] text-ink">
              {selection.selected.size} selected
            </span>
          ) : null}
        </div>
      ) : null}

      <TableWrap>
        <Table>
          <thead>
            <tr>
              {selection ? (
                <th scope="col" className="w-10 border-b border-rule px-4 py-2.5">
                  <span className="sr-only">Selected</span>
                </th>
              ) : null}
              {columns.map((c) => (
                <SortableTh
                  key={c.key}
                  column={c}
                  sort={sort}
                  onSort={onSort}
                />
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => {
              const id = keyOf(row);
              const ticked = selection ? selection.selected.has(selection.idOf(row)) : false;
              return (
                <tr key={id} className="transition-colors hover:bg-[rgba(16,16,16,0.035)]">
                  {selection ? (
                    <td className="border-b border-rule px-4 py-2.5 align-middle">
                      <span className="af-cell">
                        <input
                          type="checkbox"
                          className="h-4 w-4 accent-ink"
                          checked={ticked}
                          onChange={(e) => toggleOne(selection.idOf(row), e.target.checked)}
                          aria-label={`Select ${id}`}
                        />
                      </span>
                    </td>
                  ) : null}
                  {columns.map((c, i) => (
                    <Td
                      key={c.key}
                      // The first column names the row, so it carries no
                      // heading when the table stacks: it leads the record the
                      // way it leads the row.
                      label={i === 0 ? undefined : c.header}
                      numeric={c.numeric}
                      mono={c.mono}
                    >
                      {i === 0 && href ? (
                        <Link
                          href={href(row)}
                          className="-mx-1 -my-2 inline-flex min-h-11 items-center px-1 py-2 underline decoration-transparent underline-offset-4 hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
                        >
                          {c.cell(row)}
                        </Link>
                      ) : (
                        c.cell(row)
                      )}
                    </Td>
                  ))}
                </tr>
              );
            })}
          </tbody>
        </Table>
      </TableWrap>
      {footer}
    </>
  );
}

/** One heading, which is a button only when the server can actually sort on
 *  it. `aria-sort` is on the `th` rather than the button, because the sort
 *  describes the column and not the control. */
function SortableTh<T>({
  column,
  sort,
  onSort,
}: {
  column: Column<T>;
  sort?: { key: string; direction: SortDirection };
  onSort?: (key: string, direction: SortDirection) => void;
}) {
  const active = sort?.key === column.key;
  if (!onSort || !column.sortable) {
    return <Th numeric={column.numeric}>{column.header}</Th>;
  }
  const next: SortDirection = active && sort.direction === "asc" ? "desc" : "asc";
  return (
    <th
      scope="col"
      aria-sort={active ? (sort.direction === "asc" ? "ascending" : "descending") : "none"}
      className={`whitespace-nowrap border-b border-rule px-4 py-2.5 text-[11px] font-medium uppercase tracking-[0.08em] text-dim ${
        column.numeric ? "text-right" : ""
      }`}
    >
      <button
        type="button"
        onClick={() => onSort(column.key, next)}
        className={`inline-flex items-center gap-1 text-[11px] font-medium uppercase tracking-[0.08em] ${
          active ? "text-ink" : "text-dim hover:text-muted"
        }`}
      >
        {column.header}
        {/* The caret is present only on the sorted column. An outline on every
            heading is three shapes competing with the one that means
            something, and aria-sort already says "none" for the rest. */}
        {active ? (
          <svg viewBox="0 0 10 10" className="h-2.5 w-2.5" fill="none" aria-hidden>
            <path
              d={sort.direction === "asc" ? "M2 6.4 5 3.4l3 3" : "M2 3.6 5 6.6l3-3"}
              stroke="currentColor"
              strokeWidth="1.4"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        ) : null}
      </button>
    </th>
  );
}

/** A list that came back with nothing, said in the words of the thing that is
 *  missing and with the one action that would fill it. */
export function EmptyList({
  title,
  children,
  action,
}: {
  title: string;
  children?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <Empty title={title} action={action}>
      {children}
    </Empty>
  );
}

/* -------------------------------------------------------------------------
 * One record, beside the list it came from
 * ---------------------------------------------------------------------- */

/**
 * A side panel over the list, for the detail of one row.
 *
 * The native `dialog` element, opened with `showModal`, for the reason
 * `Confirm` in ui.tsx gives: it brings focus containment, Escape, the inert
 * background and `aria-modal` with it, and every hand rolled panel gets at
 * least one of those wrong. What is added on top is the position, because a
 * dialog defaults to the middle of the screen and a detail panel belongs
 * against the edge the row is on.
 *
 * Under the phone breakpoint it is the whole screen instead of a 460px rail.
 * A drawer that keeps its width on a 320px viewport is a column of wrapped
 * words with a sliver of the list behind it.
 */
export function Drawer({
  open,
  title,
  onClose,
  actions,
  children,
}: {
  open: boolean;
  title: string;
  onClose: () => void;
  actions?: ReactNode;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const id = useId();

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (open && !el.open) el.showModal();
    if (!open && el.open) el.close();
  }, [open]);

  return (
    <dialog
      ref={ref}
      aria-labelledby={`${id}-title`}
      onCancel={(e) => {
        // Escape goes through the parent's state rather than closing behind
        // its back, so the two cannot disagree about whether this is open.
        e.preventDefault();
        onClose();
      }}
      // A click on the backdrop is a click on the dialog element itself, since
      // the backdrop is its pseudo element. Anything inside stops here.
      onClick={(e) => {
        if (e.target === ref.current) onClose();
      }}
      // ml-auto rather than m-auto: this one belongs against the right edge,
      // and Tailwind's preflight zeroes the margin the user agent would have
      // used to centre it.
      className="m-0 ml-auto h-dvh max-h-dvh w-full max-w-full overflow-y-auto border-l border-rule bg-card p-0 text-ink backdrop:bg-[rgba(16,16,16,0.5)] sm:w-[min(100vw-3rem,460px)]"
    >
      <div className="sticky top-0 flex items-start justify-between gap-3 border-b border-rule bg-card px-4 py-3">
        <h2 id={`${id}-title`} className="min-w-0 text-[14px] font-semibold tracking-extra-tight">
          {title}
        </h2>
        <button
          type="button"
          onClick={onClose}
          aria-label="Close the panel"
          className="-mr-1.5 -mt-1.5 grid h-11 w-11 shrink-0 place-items-center rounded-md text-muted hover:bg-[rgba(16,16,16,0.05)] hover:text-ink sm:h-9 sm:w-9"
        >
          <svg viewBox="0 0 20 20" className="h-4 w-4" fill="none" aria-hidden>
            <path
              d="M5 5l10 10M15 5 5 15"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
            />
          </svg>
        </button>
      </div>
      <div className="pb-4">{children}</div>
      {actions ? (
        <div className="sticky bottom-0 flex flex-wrap justify-end gap-2 border-t border-rule bg-card px-4 py-3">
          {actions}
        </div>
      ) : null}
    </dialog>
  );
}
