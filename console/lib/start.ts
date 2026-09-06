// The one question asked after the first sign-in, and what each answer leads to.
//
// A new organization used to land on the environments list, which is three
// empty cards, each explaining that something appears "when the engine reports
// one". Somebody who came for a pull request check, somebody who came to run
// an environment from a terminal, and somebody who came to point a coding agent
// at this control plane all got the same three empty cards, and the next step
// for each of them was on a different page, or on no page at all: the hosted
// MCP address was shown to operators and to nobody else.
//
// So the root asks which of the three they are, once, and shows that one's next
// step. Everything on this page is the CONTENT of that flow, kept out of the
// component so it is testable without a renderer: which answers exist, how the
// remembered answer is read back, and the exact command, address or link each
// answer leads to. The page decides how it looks. This decides what it says.
//
// WHERE THE ANSWER IS KEPT. In this browser, under a key that names the
// organization, and nowhere else. There is no per-person preferences table on
// the control plane and this is not worth one: the answer changes which page is
// shown next, it is not tenant data, and the cost of losing it is seeing one
// question again on a new machine. Per organization rather than per browser,
// because one person in two organizations is starting twice. A value the
// current console does not recognise, which is what a removed path would
// become, reads as no answer at all rather than as a crash or as a silent skip.

export const PATHS = ["ci", "terminal", "hosted"] as const;
export type Path = (typeof PATHS)[number];

/** A remembered answer: one of the paths, or the person having said not now. */
export type Choice = Path | "skipped";

export function isPath(value: unknown): value is Path {
  return typeof value === "string" && (PATHS as readonly string[]).includes(value);
}

/**
 * What was stored, read back defensively.
 *
 * Anything that is not a current path or the dismissal is treated as never
 * having been answered, which is the only safe reading of a word this build
 * does not know: an older console may have written it, or a path may have
 * been removed, and either way the person should be asked rather than sent
 * on the strength of a value nobody can act on.
 */
export function readChoice(raw: string | null | undefined): Choice | null {
  if (raw === "skipped") return "skipped";
  return isPath(raw) ? raw : null;
}

/** The storage key for one organization. */
export function storageKey(orgId: string): string {
  return `af.start.${orgId}`;
}

/**
 * Where the root sends somebody. No answer yet means the question; any answer,
 * including "not now", means the environments list, which is where the root
 * always went before the question existed.
 */
export function destinationFor(choice: Choice | null): "/start" | "/environments" {
  return choice === null ? "/start" : "/environments";
}

/** The hosted instance, which is what `af login` uses when nothing says
 *  otherwise. The same string as controlplane.DefaultBaseURL in the engine and
 *  as HOSTED on the command line page. */
export const HOSTED = "https://app.antifailure.dev";

export const INSTALL = "curl -fsSL https://antifailure.dev/install.sh | sh";

/** The sign-in command for the control plane this console is served from.
 *  Plain on the hosted instance, addressed everywhere else, for the reason
 *  written at length on the command line page: a plain `af login` on a self
 *  hosted plane signs the terminal in to a company you may have no account
 *  with. */
export function loginCommand(origin: string): string {
  return origin === HOSTED ? "af login" : `af login --control-plane ${origin}`;
}

/** The hosted MCP endpoint, which is the origin and `/mcp`. The same rule as
 *  hostedMcpEndpoint in the control plane's auth/mcp.ts. */
export function mcpEndpoint(origin: string): string {
  return `${origin.replace(/\/+$/, "")}/mcp`;
}

/** One of the three answers, as it appears on the button. */
export interface Option {
  path: Path;
  title: string;
  detail: string;
}

export const OPTIONS: readonly Option[] = [
  {
    path: "ci",
    title: "Checks on pull requests",
    detail: "Every pull request gets its own environment and a check, reported on the pull request itself.",
  },
  {
    path: "terminal",
    title: "From my terminal",
    detail: "An environment on this machine in a few minutes, reported back here.",
  },
  {
    path: "hosted",
    title: "Hosted access for an agent",
    detail: "Connect Claude Code, Cursor or another MCP client to this control plane.",
  },
];

export function optionFor(path: Path): Option {
  return OPTIONS.find((option) => option.path === path)!;
}

/** The two answers that were not given, in their usual order, so the page can
 *  say the other doors are still open. */
export function otherPaths(path: Path): Path[] {
  return PATHS.filter((candidate) => candidate !== path);
}

/** Something to copy: a command, or an address. */
export interface Copyable {
  value: string;
  /** What the copy button says at rest. Absent means "Copy". */
  label?: string;
  /** The sentence read to a screen reader when it is copied. */
  said: string;
  /** The line under it, when one is worth reading. */
  note: string | null;
}

/** A link, when the next step is a page rather than a command. */
export interface Step {
  href: string;
  label: string;
  note: string | null;
}

/**
 * The tailored answer for one path.
 *
 * Every value here is read from the code or the documentation it describes,
 * and the sources are named in the test beside this file. Nothing is invented,
 * and a claim about a permission is a claim about what the App, the workflow
 * or the token actually needs.
 */
export interface Guide {
  path: Path;
  /** The heading, which acknowledges what this person came for. */
  heading: string;
  /** The paragraph under it, speaking to their situation. */
  ack: string;
  /** A page to open first, when the step is a page. The GitHub App install. */
  step: Step | null;
  /** Things to copy, in order. */
  copy: Copyable[];
  /** The permissions this path needs, in one line, so nobody grants
   *  something without having read the words. */
  permissions: string;
  /** Where the primary button goes once they have what they came for. */
  next: { href: string; label: string; note: string };
  /** The documentation page that says the rest. */
  docs: { href: string; label: string };
}

/**
 * @param origin the control plane this console is served from, once a window
 *        exists to ask. The command line page holds the command's place until
 *        it does; this page does the same.
 * @param installUrl the GitHub App's installation address, when this control
 *        plane has been told one. Optional on the session for the reasons
 *        written on the no-organization screen, and the CI path has to offer a
 *        real next step either way.
 */
export function guideFor(path: Path, origin: string, installUrl: string | null | undefined): Guide {
  switch (path) {
    case "ci":
      return {
        path,
        heading: "The check on the pull request, then.",
        ack:
          "You care about what a reviewer sees, so the repository is the thing to connect and the check is the thing to watch. Nothing runs in it until a pull request you merge says so.",
        step: installUrl
          ? {
              href: installUrl,
              label: "Install the GitHub App",
              note:
                'Installing it on a repository opens a pull request titled "Check every pull request with Antifailure". It adds one file, .github/workflows/antifailure.yml, on a branch of its own. Merge it and the next pull request gets a check.',
            }
          : null,
        copy: installUrl
          ? []
          : [
              {
                value: "af init",
                said: "af init copied to the clipboard",
                note:
                  "This control plane has not been told where its GitHub App installs, so the workflow comes from your checkout instead. Run this where the repository has a github.com remote and it writes .github/workflows/antifailure.yml beside the manifest. Commit both and the next pull request gets a check.",
              },
            ],
        permissions:
          "The App needs Contents: write on the installation to open that pull request. The workflow itself declares contents: read, pull-requests: write and id-token: write, and the last is how a job proves who it is without a stored secret.",
        next: {
          href: "/environments",
          label: "Open environments",
          note: "Each repository is listed there while its pull request is open, and drops off the list once the workflow is merged.",
        },
        docs: {
          href: "https://antifailure.dev/docs/getting-started/pull-requests",
          label: "An environment per pull request",
        },
      };
    case "terminal":
      return {
        path,
        heading: "Running in a few minutes, then.",
        ack:
          "You want to be running before you read anything else, so this is the shortest correct path. Three commands, in a terminal on the machine your application lives on, in this order.",
        step: null,
        copy: [
          {
            value: INSTALL,
            said: "Install command copied to the clipboard",
            note: "Downloads and verifies the release for your machine and puts af under ~/.antifailure.",
          },
          {
            value: loginCommand(origin),
            said: "Sign-in command copied to the clipboard",
            note: "Prints a short code and opens a browser. Approve it here, signed in as you are now, and the credential goes straight into your machine's credential store. No secret to copy.",
          },
          {
            value: "af up",
            said: "af up copied to the clipboard",
            note: "In your application checkout. If it has no antifailure.yaml yet, af up says so and names af init, which writes one from what it finds in the repository.",
          },
        ],
        permissions:
          "The credential af login stores can read environments and runs and write events, and nothing else unless you ask for more with --scope. It is good for ninety days and af logout revokes it everywhere.",
        next: {
          href: "/cli#first-run",
          label: "Open the command line page",
          note: "The whole first run is there: the environment, the workflows, the run results and the teardown, plus every credential this organization has handed out.",
        },
        docs: {
          href: "https://antifailure.dev/docs/getting-started/quickstart",
          label: "The quickstart",
        },
      };
    case "hosted":
      return {
        path,
        heading: "The endpoint, and what it may do.",
        ack:
          "You want an agent to reach this control plane, so what you need is the address and a straight account of what a credential on it can and cannot do.",
        step: null,
        copy: [
          {
            value: mcpEndpoint(origin),
            label: "Copy address",
            said: "Endpoint address copied to the clipboard",
            note: "Add it in a client that supports Streamable HTTP and OAuth with PKCE. The client opens a browser, you sign in here, check the client name and the permissions it asks for, and approve. Declining creates no credential, and there is no API key to paste anywhere.",
          },
        ],
        permissions:
          "Two permissions exist. mcp:read reads projects, environments, runs and reported network activity. mcp:write starts environments and test workflows and requests cleanup. Neither grants more than your role here already does, and the credential expires after ninety days.",
        next: {
          href: "/environments",
          label: "Open environments",
          note: "What the agent lists and requests is what this console shows. A new environment it asks for is dispatched through the repository's own workflow, so a repository has to be connected first.",
        },
        docs: {
          href: "https://antifailure.dev/docs/reference/mcp#connecting-to-the-control-plane",
          label: "Connecting to the control plane",
        },
      };
  }
}
