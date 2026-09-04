"use client";

import Link from "next/link";
import {
  Badge,
  Card,
  CardSkeleton,
  Loaded,
  Table,
  TableWrap,
  Td,
  Th,
  When,
} from "@/components/ui";
import { AdminPage, Facts } from "@/components/admin/primitives";
import { useAdminInstallation, type AdminInstallation } from "@/lib/admin-administration";

/**
 * How this installation is configured, and which settings were set rather than
 * defaulted.
 *
 * WHAT IT REPORTS IS RESOLVED CAPABILITY, NOT ENVIRONMENT VARIABLES. The
 * process reads its environment once at boot and builds a context; a page that
 * read the variables back would report the intent rather than the outcome, and
 * the two differ exactly when somebody has made the mistake this page would be
 * opened to find. So every line here is read off the context the running server
 * is actually serving requests with.
 *
 * NO CREDENTIAL VALUE APPEARS ON THIS PAGE, in full or truncated or
 * fingerprinted. What is shown is whether a capability resolved and the NAME of
 * the variable that would enable it. A name is what somebody needs in order to
 * fix it; a value is what they need in order to leak it.
 *
 * THE SWITCHES ARE SHOWN AND NOT THROWN. Engaging and releasing a control lives
 * on Incidents & Kill Switches, which owns admin.emergency.engage. This page
 * carries their state because "how is this installation configured" is
 * incomplete without "and is any of it currently paused", and it links there
 * rather than repeating the button. Two pages with the same engage control is
 * how a control gets released twice and read as broken.
 *
 * THERE IS NO GENERAL SETTINGS TABLE, and this page does not pretend to be one.
 * Configuration in this product is environment variables read at boot, which no
 * page can write, plus three rows in platform_controls. A form here offering to
 * change a setting would be a control with nothing behind it.
 */
export default function AdministrationConfigurationPage() {
  const state = useAdminInstallation();

  return (
    <AdminPage
      href="/admin/administration/configuration"
      lede="Read from the running control plane rather than from its environment, so it says what resolved rather than what was intended."
    >
      <Loaded state={state} skeleton={<CardSkeleton count={3} />} framed>
        {(data) => (
          <div className="grid gap-5">
            <Card title="This installation">
              <Facts
                facts={[
                  { label: "Product name", value: data.productName },
                  { label: "Application address", value: data.appBaseUrl, mono: true },
                  {
                    label: "Hosted plan requirement",
                    // Null is a real answer here and Facts renders it as "Not
                    // set", which is exactly right: a self-hosted installation
                    // requires no plan and that is not a missing value.
                    value: data.hostedRequiredPlan,
                  },
                  {
                    label: "Schema",
                    value: data.schema ? (
                      <>
                        <span className="font-mono text-[12px]">{data.schema.version}</span>
                        <span className="mt-0.5 block text-[12px] text-muted">
                          {data.schema.applied} migrations applied, the last{" "}
                          <When value={data.schema.appliedAt} />
                        </span>
                      </>
                    ) : (
                      // Reported as absent rather than as version zero, which
                      // would read as a fresh installation that is fine.
                      <span className="text-fail">
                        No schema_migrations rows. Migrations have never run against this database.
                      </span>
                    ),
                  },
                  {
                    label: "Registered runtimes",
                    value: `${data.runtimes.registered} across ${data.runtimes.providers} ${
                      data.runtimes.providers === 1 ? "provider" : "providers"
                    }`,
                  },
                  { label: "Read at", value: <When value={data.at} /> },
                ]}
              />
            </Card>

            <Card
              title="Capabilities"
              note="What this installation can do, and the variable behind each. Names only: no value of any credential appears on this page."
            >
              <Capabilities capabilities={data.capabilities} />
            </Card>

            <Card
              title="Installation switches"
              note="State only. The switches themselves are on Incidents & Kill Switches, which is the section that holds the permission to throw them."
              actions={
                <Link
                  href="/admin/operations/incidents"
                  className="inline-flex min-h-11 items-center text-[13px] text-muted underline decoration-transparent underline-offset-4 hover:text-ink hover:decoration-[rgba(16,16,16,0.35)] sm:min-h-0"
                >
                  Open the switches
                </Link>
              }
            >
              <Controls controls={data.controls} />
            </Card>

            <NotConfigurable />
          </div>
        )}
      </Loaded>
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * Capabilities
 * ---------------------------------------------------------------------- */

/**
 * One row per capability, saying what is true right now rather than only
 * whether a variable is present.
 *
 * The consequence sentence changes with the state instead of a single
 * description sitting beside a yes or a no. "Outbound email: off" leaves the
 * reader to work out what that means for an invitation; "Nothing is sent, and
 * every route that would have mailed a link hands it back to the caller
 * instead" answers the question they actually have.
 */
function Capabilities({ capabilities }: { capabilities: AdminInstallation["capabilities"] }) {
  return (
    <ul>
      {capabilities.map((c) => (
        <li key={c.name} className="border-b border-rule px-4 py-3.5 last:border-b-0">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1.5">
            <span className="text-[13px] font-medium text-ink">{c.name}</span>
            {/* The word carries the meaning and the colour agrees with it.
                Colour is never the only signal on this page. */}
            <Badge tone={c.ready ? "pass" : "neutral"}>
              {c.ready ? "configured" : "not configured"}
            </Badge>
          </div>
          <p className="mt-1.5 max-w-[74ch] text-[13px] leading-6 text-muted">
            {c.ready ? c.whenReady : c.whenNot}
          </p>
          <p className="mt-1 text-[12px] leading-5 text-dim">
            {c.ready ? "Enabled by" : "Set"} <span className="font-mono">{c.enabledBy}</span>
            {c.ready ? "" : " to turn this on."}
          </p>
        </li>
      ))}
    </ul>
  );
}

/* -------------------------------------------------------------------------
 * Switches
 * ---------------------------------------------------------------------- */

function Controls({ controls }: { controls: AdminInstallation["controls"] }) {
  const engaged = controls.filter((c) => c.engaged);
  return (
    <>
      {engaged.length > 0 ? (
        <p role="status" className="border-b border-rule px-4 py-3 text-[13px] leading-6 text-fail">
          {engaged.length === 1
            ? `${engaged[0]!.title} is engaged, so the product is refusing work on purpose.`
            : `${engaged.length} switches are engaged, so the product is refusing work on purpose.`}
        </p>
      ) : null}
      <TableWrap>
        <Table>
          <thead>
            <tr>
              <Th>Switch</Th>
              <Th>State</Th>
              <Th>Reason</Th>
              <Th>Enforced by</Th>
            </tr>
          </thead>
          <tbody>
            {controls.map((c) => (
              <tr key={c.name}>
                <Td>
                  <span className="block font-medium text-ink">{c.title}</span>
                  <span className="mt-1 block max-w-[52ch] text-[12px] leading-5 text-muted">
                    {c.effect}
                  </span>
                </Td>
                <Td label="State">
                  {c.engaged ? (
                    <>
                      <Badge tone="fail">engaged</Badge>
                      <span className="mt-1 block text-[12px] text-muted">
                        by {c.engagedBy ?? "somebody unrecorded"}
                        {c.engagedAt ? (
                          <>
                            {", "}
                            <When value={c.engagedAt} />
                          </>
                        ) : null}
                      </span>
                    </>
                  ) : (
                    <Badge tone="pass">released</Badge>
                  )}
                </Td>
                <Td label="Reason">
                  {c.reason ?? <span className="text-dim">Not engaged</span>}
                </Td>
                <Td label="Enforced by" mono>
                  {/* The file and the symbol that actually refuses. A test opens
                      this exact file and greps for this exact symbol, so a
                      control cannot claim an enforcement it does not have. */}
                  {c.enforcedBy}
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      </TableWrap>
    </>
  );
}

/* -------------------------------------------------------------------------
 * The gap, said out loud
 * ---------------------------------------------------------------------- */

function NotConfigurable() {
  return (
    <Card title="What cannot be changed from here">
      <div className="px-4 py-4">
        <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
          Everything above except the three switches is an environment variable read once when the
          control plane starts. There is no settings table, no environment-variable store, and no
          provider configuration table in this schema, so there is nothing here for a form to
          write. Changing any of it means changing the deployment and restarting the process.
        </p>
        <p className="mt-3 max-w-[74ch] text-[13px] leading-6 text-muted">
          This page exists to say what the running process resolved, which is the question that
          actually gets asked: not what the deployment intended, but what it got.
        </p>
      </div>
    </Card>
  );
}
