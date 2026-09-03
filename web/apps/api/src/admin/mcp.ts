// What this control plane can truthfully say about the MCP server, which is
// almost nothing, and why that is the honest answer rather than a gap.
//
// THE FACT THIS FILE EXISTS TO STATE. `af mcp` is a shipped engine feature and
// the control plane holds no record of it. That is not an oversight waiting for
// a table. The server binds one checkout, speaks JSON-RPC on standard input and
// output, and keeps its runs in the project's own state directory on the
// developer's disk. It opens no connection to this control plane, presents no
// engine token, and emits no event. There is therefore no fleet of MCP servers,
// no connection count, no last seen time and no per tenant adoption figure, and
// any screen showing one would be showing a number nobody measured.
//
// engine/internal/mcp/project.go says the tenancy model outright: one project,
// fixed at startup, chosen by whoever launched the process, and a project id in
// a call is an assertion rather than a selector. A page that listed servers
// would be inventing a second tenancy model with nothing behind it.
//
// SO WHAT IS THE PAGE FOR. One question an operator is actually asked, usually
// by a customer's security reviewer: can an agent use this to make a check
// easier on itself. The answer is no, and it is provable rather than a promise,
// because the refusal is a property of the tool schemas rather than a rule the
// tools ask a model to respect. This file carries that answer with its
// provenance attached, and mcp.test.ts opens each named file and greps it for
// each named symbol, so a claim here cannot outlive the code it describes.
//
// The technique is admin/controls.ts's `enforcedBy`, for the same reason: a
// bare symbol name proves only that some file declares one, and a description
// with no file behind it is a sentence that stops being true silently.

import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

/**
 * The tools the engine serves, as `path/from/engine:symbol`.
 *
 * The path is from the repository root because the engine is Go and this is
 * TypeScript, so there is no import that could go stale instead. `servedBy`
 * names the constructor, and `registeredIn` names the one file that must call
 * it: a constructor nothing registers is a tool no agent can reach, which is
 * exactly the dead capability shape this project keeps deleting.
 */
export interface McpToolFact {
  /** The name an agent calls, exactly as the engine registers it. */
  name: string
  /** What it does, in the words the reference documentation uses. */
  does: string
  /** What an agent cannot ask it for, which is the answer to the question. */
  refuses: string
  /** `path/from/repository/root:symbol` for the constructor. */
  servedBy: string
}

export const MCP_REGISTRATION_FILE = 'engine/internal/mcp/serve.go'

export const MCP_TOOLS: readonly McpToolFact[] = [
  {
    name: 'rehearse_migration_safety',
    does:
      'Applies the branch\'s pending migrations to a throwaway branch of a sanitized copy of ' +
      'production and reports which statements were slow, which tables Postgres rewrote, which ' +
      'locks were held and for how long, and what the schema linter objected to at production\'s ' +
      'table sizes.',
    refuses:
      'It cannot be pointed at a database, and it cannot be asked to rehearse a subset. Every ' +
      'pending migration runs, because a migration cannot be judged apart from the ones that run ' +
      'before it.',
    servedBy: 'engine/internal/mcp/tools_migration.go:newRehearseMigrationTool',
  },
  {
    name: 'inspect_egress_firewall',
    does:
      'Reports what the environment may reach, what it actually reached, and whether containment ' +
      'held, including whether a live credential was really swapped for a sandbox one on the way ' +
      'out.',
    refuses:
      'It cannot widen the policy, and the count it reports as sandbox_credential_not_substituted ' +
      'always fails. There is no manifest level that turns that one down.',
    servedBy: 'engine/internal/mcp/tools_egress.go:newInspectEgressTool',
  },
  {
    name: 'get_rehearsal_run',
    does:
      'Reads a submitted run\'s status and, once it has finished, its verdict, with evidence ' +
      'references paginated by cursor.',
    refuses:
      'It cannot change a verdict or a status. A run that did not finish reports INCONCLUSIVE, ' +
      'which is not a weaker PASS.',
    servedBy: 'engine/internal/mcp/tools_runs.go:newGetRunTool',
  },
  {
    name: 'cancel_rehearsal_run',
    does:
      'Asks a running rehearsal to stop. The experiment stops at the next point it can do so ' +
      'safely and tears down the environment it created.',
    refuses:
      'It is a request rather than a kill, because an environment abandoned mid run is the leak ' +
      'this product exists to prevent. A cancelled run is INCONCLUSIVE, never PASS.',
    servedBy: 'engine/internal/mcp/tools_runs.go:newCancelRunTool',
  },
]

/**
 * The one sentence that decides whether the tools can be talked into anything,
 * and where it is enforced.
 *
 * Unknown members are refused rather than ignored, which is what makes the
 * published contract and the validator the same contract. Without it an
 * argument the server does not know is an argument the server silently drops,
 * and "there is no field that disables sanitization" becomes "there is no field
 * we implemented", which is a different and much weaker claim.
 */
export const MCP_UNKNOWN_FIELD_REFUSAL = 'engine/internal/mcp/schema.go:FaultUnknownField'

/** Where an operator goes next, because this console is not where MCP is run. */
export const MCP_ELSEWHERE = {
  command: 'af mcp',
  commandDeclaredIn: 'engine/internal/cli/mcp.go:newMCPCommand',
  documentation: '/reference/mcp/',
} as const

/**
 * Resolves a repository path for the test that checks these claims.
 *
 * Exported from here rather than written in the test so that the catalog and
 * the checker agree about what a path in this file means. Five levels up from
 * `web/apps/api/src/admin` is the repository root: src, api, apps, web, root.
 */
export function repositoryPath(relative: string): string {
  const here = path.dirname(fileURLToPath(import.meta.url))
  return path.resolve(here, '..', '..', '..', '..', '..', relative)
}

/** Reads a file named by a `path:symbol` pair and reports whether the symbol is
 *  declared in it. Used by the test, and by nothing at runtime: the page shows
 *  the strings, and the test is what keeps them true. */
export async function declares(reference: string): Promise<boolean> {
  const [file, symbol] = reference.split(':')
  if (!file || !symbol) return false
  const source = await readFile(repositoryPath(file), 'utf8')
  return new RegExp(`\\b${symbol}\\b`).test(source)
}
