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
import { Button, Card, Field, Loaded, TableSkeleton, When } from "@/components/ui";
import { ApiError } from "@/lib/api";
import { operatorMay, useAdminContext } from "@/lib/admin";
import { handleLead, useLeads, type Lead } from "@/lib/admin-leads";

/**
 * The private queue of people who asked to buy.
 *
 * THIS AND APPLICATIONS ARE THE ONLY SCREENS IN THE PORTAL THAT HOLD SOMEBODY
 * WHO IS NOT A CUSTOMER. A lead filled in the "talk to us" form: no account, no
 * organization, just a name, a company and a message on the understanding that
 * an operator reads it and replies. So the two decisions here follow from that
 * rather than from how the tenant lists work:
 *
 *   THE MESSAGE IS IN THE PANEL, NOT IN THE TABLE. A message runs to four
 *   thousand characters. Putting it in a cell would truncate the thing the
 *   operator is here to read or make one row taller than the screen, and it
 *   would put a stranger's name, company and prose on a dashboard left open
 *   beside somebody. The table carries what is needed to choose a row; opening
 *   one is a deliberate act.
 *
 *   MARKING HANDLED MAILS NOBODY, and the page says so. It records that an
 *   operator has answered this lead, so the person who has waited longest is not
 *   answered twice while another is answered never. There is no mailer on this
 *   path; a screen that implied one would be the waitlist defect the public form
 *   was built to end, wearing an operator's clothes. The reply goes to the
 *   address in the panel, by hand, from wherever this operator answers mail.
 *
 * THE FOUR STATES ARE ALL BUILT. `Loaded` renders the skeleton, the failed
 * first load with a retry, and a failed reload over rows still worth reading;
 * `DataTable`'s `empty` says which queue is empty and why that is normal; `More`
 * says whether the list is complete rather than vanishing at the end; and a
 * refused handle renders where the button was pressed rather than at the top of
 * a page the operator is no longer looking at.
 *
 * THERE IS NO UN-HANDLE AND NO REMOVE. A lead is a request to buy, and marking
 * it answered is a fact about what an operator did, not a state to toggle. The
 * server refuses a second handle with the reason and tells the operator to
 * refresh, which is more use than any wording invented here.
 */

const QUEUES = [
  { value: "waiting", label: "Waiting" },
  { value: "handled", label: "Handled" },
];

export default function LeadsPage() {
  const { me } = useAdminContext();
  const mayWrite = operatorMay(me, "admin.leads.write");
  const [queue, setQueue] = useState("waiting");
  const [open, setOpen] = useState<Lead | null>(null);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const state = useLeads(queue === "handled");

  async function act(lead: Lead) {
    if (busy) return;
    setBusy(true);
    setFailure(null);
    try {
      await handleLead(lead.id, note.trim() || undefined);
      setOpen(null);
      setNote("");
      state.reload();
    } catch (error) {
      // The server's own sentence where there is one. The handle procedure
      // refuses a lead already answered, naming the reason and telling the
      // operator to refresh, which is more use than any wording this page could
      // invent.
      setFailure(
        error instanceof ApiError
          ? error.message
          : "The control plane could not be reached, so nothing was changed. Refresh the queue and try again.",
      );
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Lead>[] = [
    {
      key: "company",
      header: "Company",
      // A real button, so the panel opens from the keyboard and is announced as
      // something that does a thing. The applications and audit pages use the
      // same shape for the same reason.
      cell: (l) => (
        <button
          type="button"
          onClick={() => {
            setFailure(null);
            setNote("");
            setOpen(l);
          }}
          className="max-w-full truncate text-left text-ink underline decoration-rule-strong underline-offset-2 hover:decoration-ink"
        >
          {l.company}
        </button>
      ),
    },
    { key: "name", header: "Who asked", cell: (l) => l.name },
    {
      key: "seats",
      header: "Seats",
      cell: (l) =>
        l.seats === null ? <span className="text-dim">Not stated</span> : String(l.seats),
    },
    { key: "received", header: "Received", cell: (l) => <When value={l.createdAt} /> },
  ];

  return (
    <AdminPage
      href="/admin/administration/leads"
      lede="Enterprise and demo requests, oldest first, so the person who has waited longest is at the top. This is not customer data: a lead has no account and is joined to no organization, no analytics event and no mailing list."
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
          Marking a lead handled records that an operator answered it. It sends no email; reply to
          the address in the panel from wherever you answer mail.
        </p>
        {state.refreshError ? (
          <p role="alert" className="border-b border-rule px-4 py-2.5 text-[12px] leading-5 text-fail">
            {state.refreshError.message}
          </p>
        ) : null}
        {failure && open === null ? (
          <p role="alert" className="border-b border-rule px-4 py-2.5 text-[12px] leading-5 text-fail">
            {failure}
          </p>
        ) : null}
        <Loaded state={state} skeleton={<TableSkeleton rows={6} cols={4} />}>
          {(rows) => (
            <DataTable
              columns={columns}
              rows={rows}
              keyOf={(l) => l.id}
              empty={
                <EmptyList
                  title={queue === "handled" ? "Nothing has been handled yet" : "No lead is waiting"}
                >
                  {queue === "handled"
                    ? "No lead has been marked handled. Answering one moves it out of the waiting queue and into this one."
                    : "Nothing has come in through the talk-to-us form since the last one was handled. This is what an empty queue looks like, not a failed load."}
                </EmptyList>
              }
              footer={
                <More
                  shown={rows.length}
                  noun={{ one: "lead", many: "leads" }}
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
        title={open ? open.company : "Lead"}
        onClose={() => setOpen(null)}
        actions={
          open && mayWrite && open.handledAt === null ? (
            <Button variant="primary" busy={busy} onClick={() => void act(open)}>
              Mark handled
            </Button>
          ) : null
        }
      >
        {open ? (
          <>
            <Facts
              facts={[
                { label: "Who asked", value: open.name },
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
                { label: "Company", value: open.company },
                { label: "Seats", value: open.seats === null ? null : String(open.seats) },
                { label: "Came from", value: open.source },
                { label: "Received", value: <When value={open.createdAt} /> },
                {
                  label: "Handled",
                  value: open.handledAt === null ? null : <When value={open.handledAt} />,
                },
                {
                  label: "Handled note",
                  value: open.handledNote,
                },
                { label: "Reference", value: open.id, mono: true },
              ]}
            />
            <div className="border-t border-rule px-4 py-4">
              <h3 className="text-[12px] font-medium leading-5 text-dim">What they asked</h3>
              {/* whitespace-pre-wrap, because the paragraph breaks are theirs.
                  Collapsing them turns a structured message into one block and
                  reads as carelessness on our side. */}
              <p className="mt-2 whitespace-pre-wrap break-words text-[13px] leading-6 text-ink">
                {open.message}
              </p>
            </div>
            {mayWrite && open.handledAt === null ? (
              <div className="border-t border-rule px-4 py-4">
                <Field label="Note (optional)" hint="Kept beside the lead, for whoever reads it next. It is not emailed to anybody.">
                  <textarea
                    value={note}
                    onChange={(e) => setNote(e.target.value.slice(0, 2000))}
                    rows={3}
                    placeholder="How you answered, or what to do next."
                    className="mt-1.5 min-h-[4.5rem] w-full rounded-md border border-rule bg-card px-2.5 py-2 text-[13px] leading-6 text-ink outline-none placeholder:text-dim focus:border-rule-strong"
                  />
                </Field>
              </div>
            ) : null}
            {failure ? (
              <p role="alert" className="px-4 pb-1 text-[12px] leading-5 text-fail">
                {failure}
              </p>
            ) : null}
          </>
        ) : null}
      </Drawer>
    </AdminPage>
  );
}
