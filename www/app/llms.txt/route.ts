import { INDEXABLE_ROUTES, type RouteSection } from "@/lib/routes";
import {
  DOCS_URL,
  REPO_URL,
  SITE_DESCRIPTION_LONG,
  SITE_NAME,
  SITE_URL,
  absoluteUrl,
} from "@/lib/site";

/**
 * /llms.txt
 *
 * A markdown index of the site, for anything that would rather read structure
 * than parse a 300KB rendered page to recover eight hundred words.
 *
 * Worth being honest about what this does and does not do. No major AI
 * provider has confirmed that its crawler reads llms.txt, and measurements of
 * AI bot traffic put requests for it at a rounding error. It is not a ranking
 * signal and treating it as one is how it gets oversold.
 *
 * What actually consumes it today is the other half of the audience: coding
 * agents and IDE assistants. Cursor, Claude Code, Copilot and MCP servers
 * fetch llms.txt when a developer points them at a tool they are trying to
 * use. For a product whose users are developers holding an assistant, that is
 * the case worth serving, and it is why this file leads with the commands
 * rather than the marketing.
 *
 * `force-static` is required: the site is an export, so this is written to
 * out/llms.txt at build time and served as a file.
 */
export const dynamic = "force-static";

const SECTION_TITLES: Record<RouteSection, string> = {
  root: "Start here",
  product: "Product",
  solutions: "Solutions",
  company: "Company",
  writing: "Writing",
  legal: "Legal",
  utility: "Utility",
};

const ORDER: RouteSection[] = ["root", "product", "solutions", "writing", "company", "legal"];

export function GET() {
  const lines: string[] = [];

  lines.push(`# ${SITE_NAME}`);
  lines.push("");
  lines.push(`> ${SITE_DESCRIPTION_LONG}`);
  lines.push("");
  lines.push(
    "Antifailure answers one question: if this pull request merged, what would break? It",
    "answers it by building a disposable copy of the production stack, not by running a",
    "test suite against fixtures.",
  );
  lines.push("");

  lines.push("## The whole thing in four commands");
  lines.push("");
  lines.push("```bash");
  lines.push("curl -fsSL https://antifailure.dev/install.sh | sh");
  lines.push("af init          # reads your repo, writes antifailure.yaml");
  lines.push("af up            # masked database branch, built services, sealed network");
  lines.push("af test          # agents run your workflows and return verdicts with evidence");
  lines.push("af down          # every resource it created, gone");
  lines.push("```");
  lines.push("");

  lines.push("## What a run produces");
  lines.push("");
  lines.push(
    "- A masked Postgres branch. Masking is compiled to SQL and executed in resumable",
    "  chunks, deterministic so the same customer maps to the same fake customer across",
    "  every table and every refresh. A scanner then reads back every column of every",
    "  table looking for anything that still parses as an email, a card, a phone number",
    "  or a key, and signs an attestation. An unverified golden cannot be branched, and",
    "  that is enforced in code rather than in a checklist.",
    "- A sealed network. Every environment gets a sidecar that owns its network",
    "  namespace. Each host gets one of five modes: BLOCK refuses with a readable",
    "  decision, ALLOW passes with a rate limit, SANDBOX swaps in test credentials and",
    "  trips a wire if a live key appears, CAPTURE records mail and SMS into a",
    "  searchable inbox, MOCK answers from a stateful offline pack.",
    "- Agent verdicts. Workflows are written as sentences. The runner drives a real",
    "  browser through the accessibility tree, signs in the way a person does, and",
    "  returns pass, fail, flaky, blocked or unverified with a video, a trace and",
    "  reproduction steps. A failure caused by the runner is classified as such and is",
    "  never counted against the application.",
    "- A migration rehearsal. Pending migrations run on a fresh branch with",
    "  per-statement timing and the strongest lock held per table, pg_stat_statements",
    "  diffed between main and the branch, and query plans compared.",
  );
  lines.push("");

  for (const section of ORDER) {
    const routes = INDEXABLE_ROUTES.filter((r) => r.section === section);
    if (routes.length === 0) continue;
    lines.push(`## ${SECTION_TITLES[section]}`);
    lines.push("");
    for (const route of routes) {
      lines.push(`- [${route.title.replace(/\s+—\s+Antifailure$/, "")}](${absoluteUrl(route.path)}): ${route.summary}`);
    }
    lines.push("");
  }

  lines.push("## Elsewhere");
  lines.push("");
  lines.push(`- [Documentation](${DOCS_URL}): installation, concepts, guides, provider setup, and the full reference.`);
  lines.push(
    `- [Full text of the documentation](${DOCS_URL}/llms-full.txt): all 41 documentation pages as one plain-text file. ` +
      `Start here if you are answering a question about how to use Antifailure.`,
  );
  lines.push(`- [Source](${REPO_URL}): the engine, the runner, the adapters, and the masking catalog.`);
  lines.push(`- [Full text of this site](${SITE_URL}/llms-full.txt): every page above, concatenated, for a single fetch.`);
  lines.push("");

  return new Response(lines.join("\n"), {
    headers: {
      "content-type": "text/plain; charset=utf-8",
    },
  });
}
