"use client";

import { Card, CardSkeleton, Loaded } from "@/components/ui";
import { AdminPage } from "@/components/admin/primitives";
import { useMcpSurface } from "@/lib/admin-platform";

/**
 * MCP Management, and the honest answer to what there is to manage: nothing
 * here.
 *
 * WHY THIS PAGE IS NOT A FLEET OF SERVERS. `af mcp` is a real, shipped engine
 * feature, and it runs entirely on the developer's machine. It binds one
 * checkout, speaks the protocol on standard input and output, and keeps its
 * runs in that project's own state directory. It opens no connection to this
 * control plane, presents no credential and sends no event. So there is no
 * server list, no connection count, no last seen time and no adoption figure,
 * and a page showing one would be showing numbers nobody measured. This
 * repository has a check called figurecheck because an invented number shipped
 * once already.
 *
 * WHAT THE PAGE IS FOR INSTEAD. One question an operator is actually asked,
 * usually by a customer's security reviewer: can an agent use this to make a
 * check easier on itself. The answer is no, and it is provable rather than a
 * promise, because the refusal is a property of the tool schemas rather than a
 * rule the tools ask a model to respect. Every claim below carries the engine
 * file and symbol it came from, and admin-platform.test.ts opens each file and
 * looks for each symbol, so a tool renamed or unregistered fails the suite
 * rather than leaving this page describing a product that moved.
 *
 * THE ABSENCE IS SENT BY THE SERVER, not assumed here. `recordsAnything` is a
 * field on the route, so the day a write path exists this page changes because
 * the server changed rather than because somebody remembered to edit it.
 */
export default function PlatformMcpPage() {
  const state = useMcpSurface();

  return (
    <AdminPage
      href="/admin/platform/mcp"
      lede="What this control plane knows about the MCP server, and where the answers actually live."
    >
      <Loaded state={state} framed skeleton={<CardSkeleton count={3} />}>
        {(surface) => (
          <div className="space-y-5">
            <Card title="This console holds no MCP record">
              <div className="max-w-[72ch] space-y-3 px-4 py-4 text-[13px] leading-6 text-muted">
                {surface.recordsAnything ? (
                  // Unreachable today and deliberately written anyway. If a
                  // write path is ever added, this page must stop claiming an
                  // absence rather than keep asserting one that has ended.
                  <p className="text-ink">
                    This control plane now records something about MCP. This page has not been
                    rebuilt for it, so read the routes rather than trusting this text.
                  </p>
                ) : (
                  <>
                    <p>{surface.why}</p>
                    <p>
                      Nothing on this page is a measurement, because there is nothing here to
                      measure. What follows is the tool surface the engine serves, which is checked
                      in and can be read from the source.
                    </p>
                  </>
                )}
              </div>
            </Card>

            <Card
              title="The tools an agent can call"
              note="Served by the engine on the developer's machine, not by this control plane."
            >
              <ul className="divide-y divide-rule">
                {surface.tools.map((tool) => (
                  <li key={tool.name} className="px-4 py-4">
                    <p className="font-mono text-[13px] font-medium text-ink">{tool.name}</p>
                    <p className="mt-1.5 max-w-[72ch] text-[13px] leading-6 text-muted">
                      {tool.does}
                    </p>
                    <p className="mt-2 max-w-[72ch] text-[13px] leading-6 text-ink">
                      <span className="font-medium">What it refuses:</span> {tool.refuses}
                    </p>
                    {/* The provenance, in the smallest type on the card and
                        never hidden. It is what makes the two paragraphs above
                        checkable rather than a claim, and a reader who wants to
                        verify one should not have to ask where to look. */}
                    <p className="mt-2 text-[11.5px] leading-5 text-dim">
                      Served by <span className="break-all font-mono">{tool.servedBy}</span>
                    </p>
                  </li>
                ))}
              </ul>
            </Card>

            <Card title="Why an agent cannot weaken a check">
              <div className="max-w-[72ch] space-y-3 px-4 py-4 text-[13px] leading-6 text-muted">
                <p>
                  There is no argument on any tool that disables sanitization, widens the egress
                  policy, lowers a threshold, names a database or skips the rehearsal. That is a
                  property of the schemas rather than a convention the tools ask an agent to
                  respect, and it holds because an argument the server does not know is refused
                  rather than ignored.
                </p>
                <p className="text-[11.5px] leading-5 text-dim">
                  The refusal is at{" "}
                  <span className="break-all font-mono">{surface.unknownFieldRefusal}</span>
                </p>
                <p>
                  Thresholds come from the policy block of the project&apos;s manifest, and the
                  verdict is decided by the same evaluator the pull request check uses, so a tool
                  call and a check cannot disagree about the same change.
                </p>
                <p className="text-[11.5px] leading-5 text-dim">
                  The four tools above are the four registered in{" "}
                  <span className="break-all font-mono">{surface.registeredIn}</span>, which is the
                  whole set an agent can reach.
                </p>
              </div>
            </Card>

            <Card title="Where MCP is actually managed">
              <div className="max-w-[72ch] space-y-3 px-4 py-4 text-[13px] leading-6 text-muted">
                <p>
                  On the developer&apos;s machine, by their MCP client. The server is started by the
                  client rather than typed by a person, with{" "}
                  <span className="font-mono text-[12.5px] text-ink">{surface.command}</span> in the
                  repository it is to serve.
                </p>
                <p>
                  Running it in a terminal looks like it has hung. That is the protocol waiting for
                  a client, and it is the first thing to say to somebody who reports it as broken.
                </p>
                <p>
                  {/* An ordinary link to the docs site, which is served from the
                      same installation. The one thing this page can usefully do
                      is send the reader somewhere that has the answer. */}
                  <a
                    className="font-medium text-ink underline underline-offset-2"
                    href={surface.documentation}
                  >
                    The MCP server reference
                  </a>{" "}
                  documents every tool, the three verdicts, and what makes a result inconclusive.
                </p>
              </div>
            </Card>
          </div>
        )}
      </Loaded>
    </AdminPage>
  );
}
