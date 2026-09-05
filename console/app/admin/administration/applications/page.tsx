"use client";

import { useState } from "react";
import {
  AdminPage,
  DataTable,
  Drawer,
  EmptyList,
  Facts,
  FilterBar,
  type Column,
} from "@/components/admin/primitives";
import { More } from "@/components/pagination";
import {
  Button,
  Card,
  Confirm,
  Loaded,
  TableSkeleton,
  When,
} from "@/components/ui";
import { ApiError } from "@/lib/api";
import { operatorMay, useAdminContext } from "@/lib/admin";
import {
  removeApplication,
  reviewApplication,
  roleLabel,
  useApplications,
  type Application,
} from "@/lib/admin-recruitment";

/**
 * The private queue of applications for the founding roles.
 *
 * THIS IS THE ONLY SCREEN IN THE PORTAL THAT HOLDS SOMEBODY WHO IS NOT A
 * CUSTOMER. Every other list here is about an organization that signed up. An
 * applicant has no account, no organization and no relationship with us beyond
 * having answered a form, so the two decisions on this page follow from that
 * rather than from how the other lists work:
 *
 *   THE ANSWERS ARE IN A PANEL, NOT IN THE TABLE. An introduction runs to four
 *   thousand characters. Putting it in a cell would either truncate the thing
 *   the operator is here to read or make one row taller than the screen, and it
 *   would put a stranger's name, address and prose on a dashboard that somebody
 *   leaves open beside them. The table carries what is needed to choose a row;
 *   opening one is a deliberate act.
 *
 *   DELETING ASKS FOR THE ADDRESS. `Confirm` takes the applicant's email as its
 *   phrase, for the reason that component gives about typing an organization's
 *   own slug: typing "delete" proves you can read a label, and the mistake that
 *   actually happens is removing the row next to the one you meant. This
 *   deletion is not recoverable from this page and it destroys the only copy of
 *   what a person wrote to us.
 *
 * MARKING REVIEWED MAILS NOBODY, and the page says so out loud. It is a note
 * that an operator has read the application, nothing more. There is no mailer
 * on this path, so a screen that implied one would be the waitlist defect the
 * public form was built to end, wearing an operator's clothes.
 *
 * THE FOUR STATES ARE ALL BUILT. `Loaded` renders the skeleton, the failed
 * first load with a retry, and a failed reload over rows that are still worth
 * reading; `DataTable`'s `empty` says which queue is empty and why that is
 * normal; `More` says whether the list is complete rather than disappearing at
 * the end; and a refused review or delete renders where the button was pressed
 * rather than at the top of a page the operator is no longer looking at.
 */

const QUEUES = [
  { value: "waiting", label: "Needs review" },
  { value: "reviewed", label: "Reviewed" },
];

export default function ApplicationsPage() {
  const { me } = useAdminContext();
  const mayWrite = operatorMay(me, "admin.recruitment.write");
  const [queue, setQueue] = useState("waiting");
  const [open, setOpen] = useState<Application | null>(null);
  const [confirming, setConfirming] = useState<Application | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const state = useApplications(queue === "reviewed");

  async function act(application: Application, remove: boolean) {
    if (busy) return;
    setBusy(true);
    setFailure(null);
    try {
      if (remove) await removeApplication(application.id);
      else await reviewApplication(application.id);
      setConfirming(null);
      setOpen(null);
      state.reload();
    } catch (error) {
      // The server's own sentence where there is one. Both procedures refuse a
      // row that has already been reviewed or removed, and that refusal names
      // the reason and tells the operator to refresh, which is more use than
      // any wording this page could invent.
      setFailure(
        error instanceof ApiError
          ? error.message
          : "The control plane could not be reached, so nothing was changed. Refresh the queue and try again.",
      );
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Application>[] = [
    {
      key: "name",
      header: "Applicant",
      // A real button, so the panel opens from the keyboard and is announced as
      // something that does a thing. The audit page's first column is the same
      // shape for the same reason.
      cell: (a) => (
        <button
          type="button"
          onClick={() => {
            setFailure(null);
            setOpen(a);
          }}
          className="max-w-full truncate text-left text-ink underline decoration-rule-strong underline-offset-2 hover:decoration-ink"
        >
          {a.name}
        </button>
      ),
    },
    { key: "role", header: "Role", cell: (a) => roleLabel(a.role) },
    {
      key: "link",
      header: "Their work",
      cell: (a) =>
        a.projectUrl ? (
          <a
            href={a.projectUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="text-ink underline decoration-rule-strong underline-offset-2 hover:decoration-ink"
          >
            Open in a new tab
          </a>
        ) : (
          <span className="text-dim">No link</span>
        ),
    },
    { key: "received", header: "Received", cell: (a) => <When value={a.createdAt} /> },
  ];

  return (
    <AdminPage
      href="/admin/administration/applications"
      lede="Applications for the founding roles, oldest first, so the person who has waited longest is at the top. This is recruitment data and it is not joined to any customer, any analytics event or any mailing list."
    >
      <Card>
        <FilterBar
          filters={[{ label: "Queue", value: queue, onChange: setQueue, options: QUEUES }]}
          actions={
            <Button onClick={state.reload} busy={state.refreshing}>
              Refresh
            </Button>
          }
        />
        <p className="border-b border-rule px-4 py-2.5 text-[12px] leading-5 text-dim">
          Marking an application reviewed records that somebody read it. It sends no email.
          Scheduled maintenance removes every application older than 180 days, reviewed or not.
        </p>
        {state.refreshError ? (
          <p role="alert" className="border-b border-rule px-4 py-2.5 text-[12px] leading-5 text-fail">
            {state.refreshError.message}
          </p>
        ) : null}
        {failure && open === null && confirming === null ? (
          <p role="alert" className="border-b border-rule px-4 py-2.5 text-[12px] leading-5 text-fail">
            {failure}
          </p>
        ) : null}
        <Loaded state={state} skeleton={<TableSkeleton rows={6} cols={4} />}>
          {(rows) => (
            <DataTable
              columns={columns}
              rows={rows}
              keyOf={(a) => a.id}
              empty={
                <EmptyList
                  title={
                    queue === "reviewed"
                      ? "Nothing has been reviewed yet"
                      : "No application is waiting"
                  }
                >
                  {queue === "reviewed"
                    ? "No application has been marked reviewed. Marking one moves it out of the waiting queue and into this one."
                    : "Nothing has come in through the careers page since the last one was reviewed or removed. This is what an empty queue looks like, not a failed load."}
                </EmptyList>
              }
              footer={
                <More
                  shown={rows.length}
                  noun={{ one: "application", many: "applications" }}
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

      <Drawer
        open={open !== null}
        title={open ? open.name : "Application"}
        onClose={() => setOpen(null)}
        actions={
          open && mayWrite ? (
            <>
              {open.reviewedAt === null ? (
                <Button
                  variant="primary"
                  busy={busy}
                  onClick={() => void act(open, false)}
                >
                  Mark reviewed
                </Button>
              ) : null}
              <Button
                variant="danger"
                disabled={busy}
                onClick={() => {
                  setFailure(null);
                  setConfirming(open);
                }}
              >
                Delete the application
              </Button>
            </>
          ) : null
        }
      >
        {open ? (
          <>
            <Facts
              facts={[
                { label: "Role applied for", value: roleLabel(open.role) },
                {
                  label: "Email",
                  value: (
                    <a
                      href={`mailto:${open.email}`}
                      className="break-all text-ink underline decoration-rule-strong underline-offset-2 hover:decoration-ink"
                    >
                      {open.email}
                    </a>
                  ),
                },
                {
                  label: "Their work",
                  value: open.projectUrl ? (
                    <a
                      href={open.projectUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="break-all text-ink underline decoration-rule-strong underline-offset-2 hover:decoration-ink"
                    >
                      {open.projectUrl}
                    </a>
                  ) : null,
                },
                { label: "Received", value: <When value={open.createdAt} /> },
                {
                  label: "Reviewed",
                  value: open.reviewedAt === null ? null : <When value={open.reviewedAt} />,
                },
                {
                  label: "Compensation",
                  value: "Acknowledged on submission. The form cannot be sent without it.",
                },
                { label: "Reference", value: open.id, mono: true },
              ]}
            />
            <div className="border-t border-rule px-4 py-4">
              <h3 className="text-[12px] font-medium leading-5 text-dim">
                What they have built or grown, and why this role
              </h3>
              {/* whitespace-pre-wrap, because the applicant's paragraph breaks
                  are theirs. Collapsing them turns a structured answer into one
                  block and reads as carelessness on our side. */}
              <p className="mt-2 whitespace-pre-wrap break-words text-[13px] leading-6 text-ink">
                {open.why}
              </p>
            </div>
            {failure && confirming === null ? (
              <p role="alert" className="px-4 pb-1 text-[12px] leading-5 text-fail">
                {failure}
              </p>
            ) : null}
          </>
        ) : null}
      </Drawer>

      <Confirm
        open={confirming !== null}
        title="Delete this application?"
        phrase={confirming?.email}
        confirmLabel="Delete it"
        busy={busy}
        error={confirming ? failure : null}
        onCancel={() => setConfirming(null)}
        onConfirm={() => {
          if (confirming) void act(confirming, true);
        }}
      >
        <p>
          The name, address and answers are removed from the live database. The audit keeps the
          action and the reference, never what was written.
        </p>
        <p>
          Backups expire on their own recovery schedule, so a row is gone from every copy only
          after that window has passed. There is no undo on this page.
        </p>
      </Confirm>
    </AdminPage>
  );
}
