"use client";

/**
 * The way out.
 *
 * On the hosted service a subscription can lapse, and when it does the plan
 * gate in web/apps/api/src/trpc.ts refuses every operational permission. Five
 * are exempt from it, and the reason is written next to HOSTED_GATE_EXEMPT in
 * web/apps/api/src/hosted.ts: a plan gate may restrict what the product DOES
 * for a customer, and may never restrict their ability to leave, to retrieve
 * what is theirs, or to secure their account.
 *
 * Those five answered over the API and reached no screen. Settings, which
 * holds four of them, is read from `org.settings` under `environments.view`,
 * which is precisely what the gate refuses, and the console sent anybody
 * without hosted access to /plan and rendered nothing else. So the right
 * existed in the code and not in the product, and the sharpest end of it was
 * the confirmation: `deletion.request` compares what you type against the
 * organization's SLUG, and every route that would show you the slug was
 * gated. A lapsed owner was asked to type a string the product refused to
 * show them.
 *
 * This page is that screen. It renders the same four cards Settings renders,
 * imported rather than reimplemented, fed by the one read that survives the
 * gate.
 *
 * It renders for a healthy customer too, and says so rather than pretending
 * to be a lapsed screen. A page that only exists in a failure state is a page
 * nobody has looked at in the state they can actually reach.
 */

import { query, useApi } from "@/lib/api";
import { useSessionContext } from "@/components/session";
import { CardSkeleton, Loaded, Page } from "@/components/ui";
import {
  CloseAccount,
  Deleting,
  ExportCard,
  Fact,
  Sessions,
  type Deletion,
} from "@/components/exits";

/**
 * The one read this screen makes, and the only one it may make.
 *
 * Under `account.close`, which is the single permission EVERY role holds,
 * including viewer. Not `deletion.status`, which is `organization.delete` and
 * therefore owner only: an admin on a lapsed plan holds `data.export` and
 * `sessions.manage` and would have had no page at all.
 *
 * The name is `account.context` because that is the procedure the server
 * registers. This read was written as `account.exits`, a placeholder for a name
 * the enterprise side had not chosen yet, and the enterprise side chose
 * `context`. Nothing ever joined the two, so every render of this page asked for
 * a procedure that does not exist, got a 404, and drew its error card: the data
 * export and the account deletion, on the one screen a lapsed customer needs.
 */
const EXITS_READ = "account.context";

interface Exits {
  organization: { slug: string; name: string; plan: string };
  hostedRequiredPlan: string | null;
  hostedAccess: boolean;
  role: string;
  /** Read from the server rather than from the console's copy of the role
   *  table, so a deployment with a permission resolver installed gets the
   *  answer its resolver gave rather than the built-in one. */
  permissions: string[];
  deletion: Deletion | null;
  exportRetentionDays: number;
  /** A count and not the list, and NULL for anybody without `sessions.manage`.
   *  The server fills this only for a caller who holds that permission and
   *  leaves it null otherwise, which is the case this route exists to answer:
   *  a member sees that sessions exist and not who is signed in or from where. */
  sessions: { count: number } | null;
}

export default function ExitsPage() {
  const session = useSessionContext();
  const state = useApi<Exits>(() => query(EXITS_READ), []);
  const csrf = session.data?.csrfToken ?? "";
  const label = session.data?.label ?? "";

  return (
    <Page
      title="Your data and account"
      lede="Taking a copy of everything, signing a session out, deleting the organization and closing your account. None of these depend on the plan."
    >
      <Loaded state={state} framed skeleton={<CardSkeleton count={3} />}>
        {(exits) => {
          const lapsed = exits.hostedRequiredPlan !== null && !exits.hostedAccess;
          const holds = (permission: string) => exits.permissions.includes(permission);
          return (
            <div className="space-y-6">
              {/* What state you are in, said plainly, before any control.
                  Somebody reading this page has usually just been refused
                  somewhere else and is owed the reason in a sentence. */}
              <section className="overflow-hidden rounded-lg border border-rule bg-card">
                {lapsed ? (
                  <div className="border-b border-rule bg-paper px-4 py-3.5">
                    <p className="text-[13px] font-medium text-ink">
                      This organization is on the {exits.organization.plan} plan, and this
                      control plane serves the {exits.hostedRequiredPlan} plan.
                    </p>
                    <p className="mt-1 max-w-[62ch] text-[12.5px] leading-6 text-muted">
                      Environments, runs, masking, egress and the audit log are closed until a
                      subscription is in place. Everything on this page stays open, because
                      leaving, taking your data with you and securing your account are not
                      things a bill decides.
                    </p>
                  </div>
                ) : (
                  <div className="border-b border-rule bg-paper px-4 py-3.5">
                    <p className="text-[13px] font-medium text-ink">
                      Nothing is wrong with this organization.
                    </p>
                    <p className="mt-1 max-w-[62ch] text-[12.5px] leading-6 text-muted">
                      These four controls are also on Settings, alongside the rest of what an
                      organization can change. This page is the copy of them that keeps working
                      when a hosted plan lapses.
                    </p>
                  </div>
                )}
                <dl className="grid grid-cols-1 gap-4 px-4 py-4 sm:grid-cols-2 lg:grid-cols-4">
                  <Fact label="Organization">{exits.organization.name}</Fact>
                  {/* Shown because it is the confirmation. `deletion.request`
                      refuses anything that is not this exact string, and the
                      only other route carrying it is gated. */}
                  <Fact label="Slug">
                    <span className="font-mono text-[12.5px]">{exits.organization.slug}</span>
                  </Fact>
                  <Fact label="Plan">{exits.organization.plan}</Fact>
                  <Fact label="Your role">{exits.role}</Fact>
                </dl>
              </section>

              {holds("sessions.manage") ? (
                <Sessions csrf={csrf} />
              ) : (
                <SessionsWithoutPermission />
              )}

              {holds("data.export") ? (
                <ExportCard csrf={csrf} slug={exits.organization.slug} />
              ) : null}

              {/* Shown to somebody who can ask for a deletion, and to
                  everybody once one is under way.

                  Not to a member on a healthy organization: six numbered steps
                  describing a procedure they cannot start is not an exit, it
                  is filler on a page whose whole job is to be short and
                  actionable. Once a deletion IS running it goes to every role,
                  because a member whose environments are being torn down is
                  owed the reason, and the card already says who may act on
                  it. */}
              {holds("organization.delete") || exits.deletion !== null ? (
                <Deleting
                  csrf={csrf}
                  org={{
                    slug: exits.organization.slug,
                    name: exits.organization.name,
                    exportRetentionDays: exits.exportRetentionDays,
                    deletion: exits.deletion,
                  }}
                  mayDelete={holds("organization.delete")}
                  onChanged={state.reload}
                />
              ) : null}

              <CloseAccount csrf={csrf} label={label} />
            </div>
          );
        }}
      </Loaded>
    </Page>
  );
}

/**
 * What a member sees where the session table would be.
 *
 * No number, because there is never one to show here. This card renders in
 * exactly the branch where the caller does NOT hold `sessions.manage`, and the
 * server fills the count only for a caller who does, so the count it used to
 * read was null on every single render of this card. That was not a risk of a
 * crash, it was a crash, for every member and every viewer who reached the
 * page. Withholding the number is also the server's decision rather than an
 * omission: reading who is signed in is the permission this caller lacks.
 */
function SessionsWithoutPermission() {
  return (
    <section className="overflow-hidden rounded-lg border border-rule bg-card">
      <div className="border-b border-rule px-4 py-3">
        <h2 className="text-[14px] font-semibold tracking-extra-tight text-ink">Signed in now</h2>
      </div>
      <div className="px-4 py-4">
        <p className="max-w-[62ch] text-[13px] leading-6 text-muted">
          Who is signed in, and from where, needs the sessions.manage permission, which an
          owner or an admin holds. Closing your own account below ends every session of
          yours, whatever your role.
        </p>
      </div>
    </section>
  );
}
