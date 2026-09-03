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
      note="One command, on the machine the work happens on."
    >
      <div className="space-y-3 px-4 py-4">
        <CommandBlock command={INSTALL} said="Install command copied to the clipboard" />
        <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
          Downloads the release for your platform, checks it against the
          published checksum, and puts <code className="font-mono">af</code> and
          its runner under <code className="font-mono">~/.antifailure</code>,
          with that directory added to the file your login shell reads. It is
          POSIX <code className="font-mono">sh</code> rather than bash, so it
          works in a container as well as on a laptop, and it is the same file
          served at that address if you would rather read it first.
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
      note="The device authorization grant. Nothing is pasted and nothing is copied through a clipboard."
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
          It prints an eight character code and opens this control plane in a
          browser. Check that the code on the screen is the code in your
          terminal, approve it, and the token arrives in the terminal over TLS
          and goes straight into the operating system&rsquo;s credential store.
          It is never shown to anybody and never reaches a shell history file.
        </p>
        <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
          The token can read environments and runs and write events, and nothing
          else. It cannot manage members, change policy, or touch a provider
          key. <code className="font-mono">af login --scope providers.write</code>{" "}
          asks for more, and what was asked for is on the approval screen, so a
          capability cannot be granted without being read.
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
  return (
    <Card
      title="Get a first result"
      note="Neither of these needs an account. Both run entirely on your machine."
    >
      <div className="space-y-5 px-4 py-4">
        <div className="space-y-2">
          <CommandBlock command="af init" said="af init copied to the clipboard" />
          <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
            Reads the repository and writes{" "}
            <code className="font-mono">antifailure.yaml</code>: the services it
            found, the port each listens on, the migration command, and a
            network policy derived from the SDKs you depend on. It runs nothing
            from the repository, and everything it reports names the file it
            came from.
          </p>
        </div>
        <div className="space-y-2">
          <CommandBlock command="af start" said="af start copied to the clipboard" />
          <p className="max-w-[74ch] text-[13px] leading-6 text-muted">
            Says where you are and what the single next command is, read off
            that machine as it is right now rather than off a record of what it
            last did. It is the one command worth remembering, and it is safe to
            run at any point because it writes nothing.
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
              <Table>
                <thead>
                  <tr>
                    <Th>Name</Th>
                    <Th>Kind</Th>
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
                        <Td>{t.name}</Td>
                        <Td label="Kind">{t.kind === "cli" ? "terminal" : "engine token"}</Td>
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
                        <Td label="">
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
      lede="The engine runs on your machine and in your CI, never on this control plane. These three commands take you from nothing to a signed in terminal whose runs are recorded here."
    >
      <div className="space-y-6">
        <Install />
        <SignIn origin={origin} />
        <FirstRun />
        <Tokens mayManage={may(role, "tokens.manage")} csrf={csrf} />
      </div>
    </Page>
  );
}
