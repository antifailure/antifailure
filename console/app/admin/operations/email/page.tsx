"use client";

/**
 * Email & Notifications.
 *
 * THE ONE FACT THIS PAGE EXISTS FOR: whether this installation can send email
 * at all. It is the thing operators most often get wrong about this product,
 * and no query over the database can answer it. A sign-in request writes its
 * token row whether or not a mailer is configured, so the row that proves a
 * message was COMPOSED looks identical on an installation that sends and one
 * that silently sends nothing. Only the process knows, so the route reads the
 * context and this page leads with the answer.
 *
 * WHAT IS NOT DRAWN, AND WHY. There is no send log, no delivery record, no
 * bounce, no complaint, no open, no click, no template table and no
 * notification preference table anywhere in this schema. So there is no
 * deliverability dashboard here. What exists is the sign-in link ledger, and
 * the page is careful about what that ledger proves: a row means a message was
 * composed, and a consumed row means somebody clicked the link. A link issued
 * and never used before it expired is the only trace a delivery failure leaves
 * in this product, and the page says exactly that rather than calling the
 * column "bounced".
 *
 * DELIVERABILITY IS NOT GUESSED AT EITHER. Whether a receiver accepts a message
 * depends on the domain's SPF, DKIM and DMARC records, which this control plane
 * does not read and this page therefore does not report. What it gives instead
 * is the check an operator can run in ten seconds and what a bad answer looks
 * like. A hardcoded verdict would be right on the day it was written and a lie
 * afterwards.
 */

import { useState } from "react";
import {
  Badge,
  Button,
  Card,
  CardSkeleton,
  Loaded,
  TableSkeleton,
  When,
} from "@/components/ui";
import { More } from "@/components/pagination";
import {
  AdminPage,
  DataTable,
  EmptyList,
  FilterBar,
  MetricRow,
  StatusChip,
  type Column,
} from "@/components/admin/primitives";
import {
  WINDOWS,
  useEmailStatus,
  useSignInLinks,
  type EmailStatus,
  type SignInLink,
} from "@/lib/admin-operations";

export default function OperationsEmailPage() {
  const [hours, setHours] = useState("168");
  const [standing, setStanding] = useState("");
  const [search, setSearch] = useState("");
  const status = useEmailStatus(hours);
  const links = useSignInLinks(hours, standing, search);

  return (
    <AdminPage
      href="/admin/operations/email"
      lede="Whether this installation can send email, and every sign-in link it has issued. There is no delivery record in this product, and the page says where that line falls."
      actions={
        <Button
          onClick={() => {
            status.reload();
            links.reload();
          }}
        >
          Refresh
        </Button>
      }
    >
      <div className="space-y-6">
        <Loaded state={status} skeleton={<CardSkeleton count={2} />} framed>
          {(s) => (
            <div className="space-y-6">
              <CanSend status={s} />
              <MetricRow
                metrics={[
                  {
                    label: "Sign-in links issued",
                    value: s.reach.linksIssued,
                    note: "composed, which is not delivered",
                  },
                  {
                    label: "Used",
                    value: s.reach.linksUsed,
                    note: "somebody clicked the link",
                  },
                  {
                    label: "Expired unused",
                    value: s.reach.linksUnused,
                    note: "the only trace a failure leaves",
                  },
                  {
                    label: "Still live",
                    value: s.reach.linksLive,
                    note: "issued and not yet expired",
                  },
                ]}
              />
              <MetricRow
                metrics={[
                  { label: "Invitations sent", value: s.reach.invitationsSent },
                  { label: "Accepted", value: s.reach.invitationsAccepted },
                  {
                    label: "Still open",
                    value: s.reach.invitationsOpen,
                    note: "not accepted, not revoked, not expired",
                  },
                  {
                    label: "Billing addresses",
                    value: s.reach.billingContacts,
                    note: "on file, nothing is sent to them",
                  },
                ]}
              />
            </div>
          )}
        </Loaded>

        <Card
          title="Sign-in links"
          note="A row means a message was composed. Used means somebody clicked the link, which is the only evidence of delivery anywhere in this product."
        >
          <FilterBar
            search={{
              value: search,
              onChange: setSearch,
              label: "Search sign-in links by address",
              placeholder: "Part of an email address",
            }}
            filters={[
              {
                label: "Window",
                value: hours,
                onChange: setHours,
                options: WINDOWS.map((w) => ({ value: w.value, label: w.label })),
              },
              {
                label: "Standing",
                value: standing,
                onChange: setStanding,
                options: [
                  { value: "", label: "Every link" },
                  { value: "used", label: "Used" },
                  { value: "live", label: "Still live" },
                  { value: "unused", label: "Expired unused" },
                ],
              },
            ]}
          />
          <Loaded state={links} skeleton={<TableSkeleton rows={6} cols={5} />}>
            {(rows) => (
              <DataTable
                columns={LINK_COLUMNS}
                rows={rows}
                keyOf={(l) => l.id}
                empty={
                  <EmptyList
                    title={
                      search
                        ? "No link was issued to an address like that"
                        : standing
                          ? "No link is in that state"
                          : "No sign-in link has been issued"
                    }
                    action={
                      search || standing ? (
                        <Button
                          onClick={() => {
                            setSearch("");
                            setStanding("");
                          }}
                        >
                          Clear the filters
                        </Button>
                      ) : undefined
                    }
                  >
                    {search || standing
                      ? "Nothing in the selected window matches. Clear the filters or widen the window."
                      : "Nobody has asked to sign in with an email link in this window. That is expected on an installation where everybody signs in with GitHub."}
                  </EmptyList>
                }
                footer={
                  <More
                    shown={rows.length}
                    noun={{ one: "link", many: "links" }}
                    hasMore={links.hasMore}
                    busy={links.busy}
                    error={links.moreError}
                    onMore={links.more}
                  />
                }
              />
            )}
          </Loaded>
        </Card>

        <Deliverability />
        <WhatIsNotRecorded />
      </div>
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * Can this installation send at all
 * ---------------------------------------------------------------------- */

/**
 * The lead answer, in a sentence, before any number.
 *
 * Three states rather than two, and the third is the one worth the component.
 * A recording mailer accepts every message, reports success, and delivers
 * nothing: from the database, from the logs and from every metric on this page
 * it is indistinguishable from a working installation. It is a test double, and
 * a test double in production is a silent outage.
 */
function CanSend({ status }: { status: EmailStatus }) {
  const broken = !status.canSend || status.recordingOnly;
  return (
    <section
      role="status"
      className={`rounded-lg border border-rule px-4 py-4 ${
        broken ? "bg-[rgba(179,38,30,0.1)]" : "bg-card"
      }`}
    >
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <p className="text-[14px] font-semibold tracking-extra-tight text-ink">
          {!status.canSend
            ? "This installation cannot send email."
            : status.recordingOnly
              ? "Messages are being recorded and delivered to nobody."
              : "This installation has a mail provider configured."}
        </p>
        <StatusChip
          value={status.canSend ? (status.recordingOnly ? "recording" : "configured") : "not configured"}
          tone={broken ? "fail" : "pass"}
        />
      </div>
      <p className="mt-1.5 max-w-[70ch] text-[13px] leading-6 text-muted">
        {!status.canSend
          ? "No mail provider is set on this process. Sign-in links are still issued and still recorded below, and nobody receives one, so an email sign-in cannot be completed. Setting AF_RESEND_API_KEY, AF_MAIL_FROM and a public URL together is what turns it on; setting some but not all of them makes the control plane refuse to start rather than run half configured."
          : status.recordingOnly
            ? "The mailer on this process keeps messages in memory instead of sending them. That is the test double, and on a real installation it is a silent outage: every count on this page will look healthy while no message has left the building."
            : `Messages go out through ${status.provider}. That is the provider accepting them, which is not the same as a recipient receiving them; see the deliverability check below.`}
      </p>
    </section>
  );
}

/* -------------------------------------------------------------------------
 * The ledger
 * ---------------------------------------------------------------------- */

const LINK_COLUMNS: Column<SignInLink>[] = [
  {
    key: "email",
    header: "Address",
    cell: (l) => (
      <>
        <span className="block truncate font-medium text-ink">{l.email}</span>
        {l.ip ? (
          <span className="block truncate font-mono text-[12px] text-muted">{l.ip}</span>
        ) : null}
      </>
    ),
  },
  {
    key: "standing",
    header: "Standing",
    cell: (l) => (
      <StatusChip
        value={
          l.standing === "used"
            ? "used"
            : l.standing === "live"
              ? "still live"
              : "expired unused"
        }
        // Expired unused is a warning rather than a failure. It genuinely can
        // be somebody who asked twice and used the second link, and colouring
        // every one of them red would make a normal page look like an outage.
        tone={l.standing === "used" ? "pass" : l.standing === "live" ? "neutral" : "warn"}
      />
    ),
  },
  { key: "created", header: "Issued", cell: (l) => <When value={l.createdAt} /> },
  {
    key: "consumed",
    header: "Used",
    cell: (l) =>
      l.consumedAt === null ? <span className="text-dim">never</span> : <When value={l.consumedAt} />,
  },
  { key: "expires", header: "Expires", cell: (l) => <When value={l.expiresAt} /> },
];

/* -------------------------------------------------------------------------
 * Deliverability
 * ---------------------------------------------------------------------- */

/**
 * The half of the answer that lives in DNS.
 *
 * A provider accepting a message and a recipient receiving one are different
 * events, and the gap between them is entirely in three DNS records that this
 * control plane does not read. So this does not print a verdict it cannot
 * verify. It prints the check, which stays true, and what a wrong answer looks
 * like, which is what an operator needs at the moment they are wondering why
 * nobody is getting their link.
 */
function Deliverability() {
  return (
    <Card title="Whether a receiver will accept what is sent">
      <div className="space-y-3 px-4 py-4 text-[13px] leading-6 text-muted">
        <p className="max-w-[70ch]">
          A configured provider means messages are accepted for sending. Whether they are then
          accepted for delivery is decided by three DNS records on the sending domain, and this
          control plane does not read DNS, so nothing on this page can tell you. Run the check
          yourself against the domain in your From address.
        </p>
        <pre className="scroll-x rounded-md border border-rule bg-paper px-3 py-2.5 font-mono text-[12px] leading-6 text-ink">
{`dig +short TXT example.com
dig +short TXT resend._domainkey.example.com
dig +short TXT _dmarc.example.com`}
        </pre>
        <ul className="max-w-[70ch] list-disc space-y-1.5 pl-5">
          <li>
            An SPF record of <code className="font-mono text-[12px]">v=spf1 -all</code> is the
            correct setting for a domain that sends nothing and a hard fail instruction the moment
            it sends anything.
          </li>
          <li>
            A DKIM record whose <code className="font-mono text-[12px]">p=</code> tag is empty is a
            revoked key, not a missing one. Receivers treat every signature against it as
            permanently invalid.
          </li>
          <li>
            A DMARC policy of <code className="font-mono text-[12px]">p=reject</code> with{" "}
            <code className="font-mono text-[12px]">adkim=s</code> means the signing domain must
            equal the From domain exactly. Verifying a subdomain and sending from the apex fails
            silently and completely.
          </li>
          <li>
            No MX record on the domain means the addresses this product publishes for people to
            write to do not accept mail either, whatever the sending side does.
          </li>
        </ul>
        <p className="max-w-[70ch] text-dim">
          Every one of those combinations is silent. Nothing bounces back into this product,
          because there is nowhere for a bounce to be recorded, so the only symptom is a column of
          sign-in links above that were issued and never used.
        </p>
      </div>
    </Card>
  );
}

function WhatIsNotRecorded() {
  return (
    <Card title="What this page cannot show you">
      <div className="space-y-3 px-4 py-4 text-[13px] leading-6 text-muted">
        <p className="max-w-[70ch]">
          This product keeps no send log, no delivery record, no bounce, no complaint, no open and
          no click. It has no email template table and no notification preference table. Nothing
          above is a delivery statistic, and none of it has been dressed up as one.
        </p>
        <p className="max-w-[70ch]">
          There are exactly two things that put a message in somebody&apos;s inbox: the sign-in
          link, and an organization invitation sent by a member. Anything else this product calls a
          notification is not email.
        </p>
        <p className="max-w-[70ch] text-dim">
          <Badge tone="neutral">what would change this</Badge> A delivery record would need a table
          written when a message is handed to the provider, a webhook endpoint for the
          provider&apos;s delivery and bounce callbacks, and a signature check on it. That is a
          schema change and a new inbound route, not a page.
        </p>
      </div>
    </Card>
  );
}
