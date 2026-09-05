"use client";

import { useEffect, useState } from "react";
import { mutate, query, useApi } from "@/lib/api";
import { useSessionContext } from "@/components/session";
import { may } from "@/lib/roles";
import {
  Badge,
  Bar,
  Button,
  Card,
  CommandBlock,
  Empty,
  LinkButton,
  Loaded,
  Page,
  Row,
  Table,
  TableSkeleton,
  TableWrap,
  Td,
  Th,
  When,
} from "@/components/ui";

/**
 * How somebody gets from this browser tab to a working command line.
 *
 * WHY THIS PAGE EXISTS. Everything a person actually does with this product
 * happens in a terminal: the engine builds environments on their machine and in
 * their CI, and this control plane is where the result is recorded. Until this
 * screen there was nothing anywhere in the console that said so. A new
 * organization landed on an environments list with three empty cards, each of
 * which explained that something appears "when the engine reports one", and no
 * screen in the product said how to get an engine, sign it in, or run it. The
 * install command was on the marketing site and in the documentation, which is
 * where somebody who has not signed up yet is, not where somebody who just did
 * is.
 *
 * THE ADDRESS IN THE LOGIN COMMAND IS NOT A DETAIL. `af login` with no flag
 * signs in to the hosted instance, because that is the sensible default for the
 * people there are most of. On any other control plane, which is every self
 * hosted installation and every preview, a plain `af login` sends somebody's
 * terminal to a company they may not have an account with and stores a
 * credential for an origin nothing else they run will ever talk to. The command
 * shown here names the control plane this console is being served from, and
 * says nothing when that is already the default, so the shortest correct
 * command is always the one on the screen.
 *
 * The tokens below are the other half of the same relationship. `af login`
 * grants a credential that is good for ninety days, and until this table
 * existed there was no screen anywhere that listed one or took one away: both
 * procedures were written, permissioned and audited, and nothing in any console
 * called either of them. A credential you cannot see is a credential you cannot
 * revoke.
 */

/** The hosted instance, which is what `af login` uses when nothing says
 *  otherwise. The same string as controlplane.DefaultBaseURL in the engine. */
const HOSTED = "https://app.antifailure.dev";

const INSTALL = "curl -fsSL https://antifailure.dev/install.sh | sh";

/**
 * The origin this console is served from, once there is a window to ask.
 *
 * In an effect rather than at first render, because the console is a static
 * export: the HTML is built with no window at all, and reading one during the
 * first client render would make the markup React hydrated disagree with the
 * markup it was given. The wait is one frame and the command's place is held
 * rather than left empty, so nothing moves when it arrives.
 */
function useOrigin(): string | null {
  const [origin, setOrigin] = useState<string | null>(null);
  useEffect(() => setOrigin(window.location.origin), []);
  return origin;
}

interface TokenRow {
  id: string;
  name: string;
  prefix: string;
  kind: string;
  created_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
  expires_at: string | null;
}

function Install() {
  return (
    <Card
      title="Install the engine"
      note="Install the command line and prepare its browser runner."
    >
      <div className="space-y-3 px-4 py-4">
        <CommandBlock command={INSTALL} said="Install command copied to the clipboard" />
        <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
          Downloads and verifies the release for your machine, then installs
          <code className="font-mono"> af</code> and its runner under
          <code className="font-mono"> ~/.antifailure</code>. Run this in a
          terminal on the machine where you will test your application.
        </p>
        <CommandBlock command="af runner install" said="Runner setup command copied to the clipboard" />
        <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
          With Node.js installed, this prepares the browser runner that executes
          your workflows. Docker runs the isolated environment.
        </p>
      </div>
    </Card>
  );
}

function SignIn({ origin }: { origin: string | null }) {
  // Null while the origin is still being read, and the plain command once it
  // turns out to be the hosted instance. Both render the shortest command that
  // is correct for the reader.
  const command =
    origin === null ? null : origin === HOSTED ? "af login" : `af login --control-plane ${origin}`;

  return (
    <Card
      title="Sign this terminal in"
      note="Connect your terminal to the account you are using here."
    >
      <div className="space-y-3 px-4 py-4">
        {command === null ? (
          // The shape of the command, held so the card does not resize under
          // somebody's eyes when the address arrives.
          <div className="flex items-center gap-3" aria-hidden>
            <div className="min-w-0 flex-1 rounded-md border border-rule bg-[rgba(16,16,16,0.03)] px-3 py-2.5">
              <Bar className="h-4 w-[22ch] max-w-full" />
            </div>
          </div>
        ) : (
          <CommandBlock command={command} said="Sign-in command copied to the clipboard" />
        )}
        <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
          Your terminal opens an approval page. Match its code to the one in
          your terminal, then approve. The credential is stored securely on
          your machine, so you do not need to copy a secret.
        </p>
        <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
          This lets the terminal report environments and results to your
          organization. Managing members, policy and provider keys requires
          separate permissions.
        </p>
        <div className="flex flex-wrap gap-2 pt-1">
          <LinkButton href="/device" variant="secondary">
            Approve a terminal
          </LinkButton>
        </div>
        <p className="max-w-[74ch] text-[12px] leading-5 text-dim">
          On a machine with no browser of its own, over ssh for instance, run{" "}
          <code className="font-mono">af login --no-browser</code> and bring the
          code here.
        </p>
      </div>
    </Card>
  );
}

function FirstRun() {
  // The signed-in content appears after the document's initial hash lookup.
  // Complete a direct link once this target exists, without smooth motion.
  useEffect(() => {
    if (window.location.hash === "#first-run") {
      document.getElementById("first-run")?.scrollIntoView();
    }
  }, []);
  return (
    <div id="first-run" className="scroll-mt-6">
    <Card
      title="Run your first check"
      note="Run these in your application checkout. The environment runs on your machine; a signed-in terminal reports its results here."
    >
      <div className="space-y-5 px-4 py-4">
        <div className="space-y-2">
          <h3 className="text-[14px] font-medium text-ink">1. Describe your app</h3>
          <CommandBlock command="af init" said="af init copied to the clipboard" />
          <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
            Reads your repository and writes{" "}
            <code className="font-mono">antifailure.yaml</code>. Review the
            services, database setup and network rules it detects, plus the test
            accounts and workflows it proposes. A workflow describes something
            a user should be able to do in your app.
          </p>
        </div>
        <div className="space-y-2">
          <h3 className="text-[14px] font-medium text-ink">2. Check what is ready</h3>
          <CommandBlock command="af start" said="af start copied to the clipboard" />
          <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
            Checks your machine and configuration, then names the next step.
            Resolve the prerequisites it reports before continuing. You can
            run this again whenever you are unsure what to do next.
          </p>
        </div>
        <div className="space-y-2">
          <h3 className="text-[14px] font-medium text-ink">3. Create an isolated environment</h3>
          <CommandBlock command="af up" said="af up copied to the clipboard" />
          <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
            Builds and starts a copy of your app with its own database and
            network policy. If you use production data, review the masking
            rules first: masking replaces sensitive values in the copy before
            tests use it. The source database is read, not rewritten.
          </p>
          <LinkButton href="https://antifailure.dev/docs/concepts/masking" variant="secondary">
            Understand masking
          </LinkButton>
        </div>
        <div className="space-y-2">
          <h3 className="text-[14px] font-medium text-ink">4. Execute your workflows</h3>
          <CommandBlock command="af test" said="af test copied to the clipboard" />
          <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
            A run is one execution of the workflows in your manifest. The runner
            uses your app in a browser and reports each verdict with evidence.
            Open Runs to inspect the result. A blocked or unverified workflow
            needs attention; it is not a successful test.
          </p>
          <LinkButton href="/runs" variant="secondary">View run results</LinkButton>
        </div>
        <div className="space-y-2">
          <h3 className="text-[14px] font-medium text-ink">5. Remove the environment</h3>
          <CommandBlock command="af down" said="af down copied to the clipboard" />
          <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
            Stops and removes the resources created for this environment. Use
            this after inspecting a result, including after a failed startup.
          </p>
        </div>
        <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
          Once a signed in terminal runs an environment, it appears under
          Environments and its runs under Runs. Until then those screens are
          empty because nothing has happened yet, which is a different thing
          from being broken.
        </p>
      </div>
    </Card>
    </div>
  );
}

/**
 * The credential CI needs, which is a different credential and is deliberately
 * not mintable from here.
 *
 * `POST /v1/tokens` answers a bearer token and has no cookie path at all, so no
 * screen in this console can reach it. That is the design rather than a gap:
 * the mint produces a credential, so it is gated on a terminal already holding
 * one that was approved for `tokens.manage` by name, on a screen that showed
 * the words. A browser session that could mint would be a credential factory
 * behind a cookie.
 *
 * What was missing was anybody being told. The two commands are the only way to
 * get a token into CI, `af token --help` has always said so, and nothing a
 * person could see while thinking "how do I put this in my pipeline" mentioned
 * either of them.
 */
function ForCI({ origin }: { origin: string | null }) {
  const plane = origin === null || origin === HOSTED ? "" : ` --control-plane ${origin}`;
  return (
    <Card
      title="Wire it into CI"
      note="Only where GitHub cannot vouch for the job. A different credential, and it comes last."
    >
      <div className="space-y-5 px-4 py-4">
        <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
          A GitHub Actions job needs none of this. Give the job{" "}
          <code className="font-mono">permissions: id-token: write</code> and the
          engine trades the identity GitHub signs for that job for a short lived
          credential of its own, so nothing is stored in your repository and
          nothing has to be rotated. What follows is for an engine running
          somewhere GitHub will not vouch for it: a self hosted runner outside
          Actions, another CI system, or a machine that is nobody&rsquo;s laptop.
        </p>
        <div className="space-y-2">
          {plane === "" && origin === null ? (
            <div className="flex items-center gap-3" aria-hidden>
              <div className="min-w-0 flex-1 rounded-md border border-rule bg-[rgba(16,16,16,0.03)] px-3 py-2.5">
                <Bar className="h-4 w-[28ch] max-w-full" />
              </div>
            </div>
          ) : (
            <CommandBlock
              command={`af login --scope tokens.manage${plane}`}
              said="Scoped sign-in command copied to the clipboard"
            />
          )}
          <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
            Minting is a capability, so it is asked for by name and approved on
            the screen that shows the words. A token from a plain{" "}
            <code className="font-mono">af login</code> cannot make another one,
            which is deliberate: a credential that can make credentials is a
            credential worth stealing twice.
          </p>
        </div>
        <div className="space-y-2">
          <CommandBlock command="af token create ci" said="Mint command copied to the clipboard" />
          <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
            Prints the token once and stores nothing but its hash, so nothing
            can show it again. It goes in{" "}
            <code className="font-mono">AF_CONTROL_PLANE_TOKEN</code> wherever
            that engine runs. It belongs to the organization rather than to you,
            so it keeps working after you leave, and it carries no identity: it
            can send events and read an environment back, and it cannot reach a
            provider key, a member, or another token.
          </p>
        </div>
        <p className="max-w-[74ch] text-[12px] leading-5 text-dim">
          No screen here can mint one. The route answers a terminal holding a
          credential and never a browser holding a cookie, so a stolen session
          cannot become a permanent one. It appears in the list below the moment
          it exists.
        </p>
      </div>
    </Card>
  );
}

/**
 * Every credential this organization has handed out, of both kinds.
 *
 * The two are shown together and told apart, because they are one question with
 * two answers: what can act as us. A terminal is a person who ran af login; an
 * engine token is a secret somebody pasted into a build machine. Revoking
 * either takes effect on the next request, since every route re-reads the row.
 */
function Tokens({ mayManage, csrf }: { mayManage: boolean; csrf: string }) {
  const state = useApi<TokenRow[]>(() => query<TokenRow[]>("tokens.list"), []);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  if (!mayManage) {
    return (
      <Card title="Terminals and tokens">
        <p className="max-w-[74ch] px-4 py-4 text-[13px] leading-6 text-muted">
          Seeing which machines can act as this organization, and taking one
          away, is an owner&rsquo;s or an administrator&rsquo;s. Your role is
          not one of those, so this list is not shown to you. Signing your own
          terminal in above still works.
        </p>
      </Card>
    );
  }

  return (
    <Card
      title="Terminals and tokens"
      note="Everything that can act as this organization from outside a browser."
    >
      {error ? (
        <p role="alert" className="border-b border-rule px-4 py-3 text-[12.5px] text-fail">
          {error}
        </p>
      ) : null}
      <Loaded state={state} skeleton={<TableSkeleton rows={3} cols={5} />}>
        {(rows) =>
          rows.length === 0 ? (
            <Empty title="No terminal is signed in">
              Nothing outside a browser can act as this organization yet. Run
              the sign-in command above on a machine and it appears here.
            </Empty>
          ) : (
            <TableWrap>
              {/* Five columns, one of them a button. The kind rides under the
                  name rather than taking a column of its own, which is what the
                  members table does with a login and a display name: six
                  columns did not fit at a tablet width, and the one that got
                  squeezed was the one holding Revoke. */}
              <Table className="sm:min-w-[600px]">
                <thead>
                  <tr>
                    <Th>Name</Th>
                    <Th>Prefix</Th>
                    <Th>Last used</Th>
                    <Th>State</Th>
                    <Th>
                      <span className="sr-only">Actions</span>
                    </Th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((t) => {
                    const revoked = Boolean(t.revoked_at);
                    const expired =
                      !revoked &&
                      Boolean(t.expires_at) &&
                      new Date(t.expires_at as string).getTime() <= Date.now();
                    return (
                      <Row key={t.id}>
                        <Td>
                          <span className="block text-ink">{t.name}</span>
                          <span className="block text-[12px] text-dim">
                            {t.kind === "cli" ? "terminal" : t.kind === "mcp" ? "MCP client" : t.kind === "oidc" ? "workflow identity" : "engine token"}
                          </span>
                        </Td>
                        <Td label="Prefix" mono>
                          {t.prefix}
                        </Td>
                        <Td label="Last used">
                          {t.last_used_at ? <When value={t.last_used_at} /> : "never"}
                        </Td>
                        <Td label="State">
                          {revoked ? (
                            <Badge tone="fail">revoked</Badge>
                          ) : expired ? (
                            <Badge tone="neutral">expired</Badge>
                          ) : (
                            <Badge tone="pass">live</Badge>
                          )}
                        </Td>
                        <Td className="w-px whitespace-nowrap">
                          {revoked || expired ? null : (
                            <Button
                              variant="danger"
                              busy={busy === t.id}
                              onClick={async () => {
                                setBusy(t.id);
                                setError(null);
                                try {
                                  await mutate("tokens.revoke", { id: t.id }, csrf);
                                  state.reload();
                                } catch (e) {
                                  setError(
                                    e instanceof Error
                                      ? e.message
                                      : "The control plane refused it.",
                                  );
                                } finally {
                                  setBusy(null);
                                }
                              }}
                            >
                              Revoke
                            </Button>
                          )}
                        </Td>
                      </Row>
                    );
                  })}
                </tbody>
              </Table>
            </TableWrap>
          )
        }
      </Loaded>
      <p className="max-w-[74ch] border-t border-rule px-4 py-3 text-[12px] leading-5 text-dim">
        Revoking takes effect on the next request that machine makes, because
        every route reads the row rather than trusting the token. It does not
        reach the machine itself: whatever is stored there stays stored and
        stops working.
      </p>
    </Card>
  );
}

export default function CliPage() {
  const session = useSessionContext();
  const origin = useOrigin();
  const role = session.data?.role ?? null;
  const csrf = session.data?.csrfToken ?? "";

  return (
    <Page
      title="Command line"
      lede="Install the engine, connect your terminal to this account, then create an isolated environment and run your workflows. The steps below take you through a result and cleanup."
    >
      <div className="space-y-6">
        <Install />
        <SignIn origin={origin} />
        <FirstRun />
        <details>
          <summary className="cursor-pointer rounded-md px-1 py-3 text-[13px] font-medium text-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ink">
            Next: run checks in CI
          </summary>
          <div className="mt-3"><ForCI origin={origin} /></div>
        </details>
        <Tokens mayManage={may(role, "tokens.manage")} csrf={csrf} />
      </div>
    </Page>
  );
}
