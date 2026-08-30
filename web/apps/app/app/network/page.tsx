// The egress policy: what an environment may reach, in the order that decides.
//
// Order is the whole point of this page. A policy is a list of rules where the
// most specific match wins and organization-wide rules beat repository ones at
// equal specificity, and a page that shows them alphabetically or by creation
// time shows a list nobody can predict from. So they are shown exactly as the
// engine compiled them, numbered, with the default at the end where it
// actually sits.
//
// Proposing a rule does not change any environment. It records the intent, and
// the rule takes effect by being committed to the repository and read by the
// engine. That is said on the form rather than left to be discovered, because
// a control plane that silently changed what a sealed environment may reach
// would be a hole in the thing this product is for.

import type { Metadata } from "next";
import { Suspense } from "react";
import { redirect } from "next/navigation";
import { Chrome } from "../../components/Chrome";
import {
  Empty,
  Failure,
  Field,
  ModeChip,
  MODES,
  Mono,
  Page,
  PageHead,
  Panel,
  TableFrame,
  TableSkeleton,
  Td,
  Th,
  Tr,
  inputClass,
  selectClass,
} from "../../components/ui";
import {
  ApiError,
  mutate,
  query,
  type EffectivePolicy,
  type Explanation,
  type Mode,
} from "../../lib/api";
import { requireActor } from "../../lib/guard";
import { isNavigation } from "../../lib/navigation";
import { NoOrganization, Unreachable } from "../../components/Screens";

export const metadata: Metadata = { title: "Network" };
export const dynamic = "force-dynamic";

const MODE_NAMES: Mode[] = ["block", "allow", "capture", "mock", "sandbox", "synth"];

function back(params: Record<string, string | undefined>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value) search.set(key, value);
  }
  const query = search.toString();
  return query ? `/network?${query}` : "/network";
}

async function proposeRule(formData: FormData): Promise<void> {
  "use server";

  const repository = String(formData.get("repository") ?? "").trim();
  const host = String(formData.get("host") ?? "").trim();
  const mode = String(formData.get("mode") ?? "").trim();
  const note = String(formData.get("note") ?? "").trim();

  if (!repository || !host || !MODE_NAMES.includes(mode as Mode)) {
    redirect(back({ repository, refused: "A repository, a host, and a mode are all needed." }));
  }

  try {
    await mutate("network.propose", {
      repository,
      host,
      mode,
      ...(note ? { note } : {}),
    });
  } catch (err) {
    if (isNavigation(err)) throw err;
    // The API compiles the rule before it stores it, so a rule the engine
    // would refuse is refused here with the engine's own words. Passing that
    // sentence through is the whole value of it.
    redirect(
      back({
        repository,
        refused: err instanceof ApiError ? err.message : "The rule could not be proposed.",
      }),
    );
  }

  redirect(back({ repository, proposed: `${mode} ${host}` }));
}

export default async function NetworkPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = await searchParams;
  const one = (key: string): string => {
    const value = params[key];
    const found = Array.isArray(value) ? value[0] : value;
    return typeof found === "string" ? found : "";
  };

  let actor: Awaited<ReturnType<typeof requireActor>>;
  try {
    actor = await requireActor("/network");
  } catch (err) {
    // A redirect is not a failure. Next.js signals one by throwing, and
    // catching it here is what turned "go and sign in" into a full page
    // error saying the control plane did not answer.
    if (isNavigation(err)) throw err;
    return <Unreachable detail={err instanceof ApiError ? err.message : String(err)} />;
  }
  if (actor === "no-organization") return <NoOrganization />;

  const repository = one("repository");
  const askedHost = one("host");
  const askedPath = one("path");
  const askedMethod = one("method");

  const mayEdit = actor.role === "owner" || actor.role === "admin";

  return (
    <Chrome current="/network" who={actor.label} org={actor.orgSlug} role={actor.role}>
      <Page>
        <PageHead
          title="Network policy"
          lede="What an environment may reach, in the order the engine reads it. The most specific rule wins; an organization rule beats a repository rule at equal specificity."
        />

        {one("proposed") ? (
          <div className="mb-4 rounded-xl border border-pass/25 bg-pass-tint px-4 py-3.5 sm:px-5">
            <p className="text-[13.5px] font-semibold tracking-snug text-pass">
              Proposed: {one("proposed")}
            </p>
            <p className="mt-1 max-w-[76ch] text-[13px] leading-[1.55] text-[#1c5407]">
              Recorded, and no environment has changed. A rule takes effect by being committed to
              the repository and read by the engine, which is what keeps a sealed environment
              sealed against this application as well as against everything else.
            </p>
          </div>
        ) : null}

        {one("refused") ? (
          <div className="mb-4">
            <Failure title="That rule was not stored" detail={one("refused")} />
          </div>
        ) : null}

        <form className="mb-4 flex flex-col gap-1.5" method="get">
          <div className="flex flex-wrap items-end gap-2.5">
            <div className="flex min-w-[200px] flex-1 flex-col gap-1.5 sm:max-w-[300px]">
              <label htmlFor="repository" className="text-[12.5px] font-medium tracking-snug text-ink">
                Repository
              </label>
              <input
                id="repository"
                name="repository"
                defaultValue={repository}
                placeholder="owner/name"
                spellCheck={false}
                className={inputClass}
              />
            </div>
            <button
              type="submit"
              className="inline-flex h-9 items-center rounded-lg bg-ink px-3.5 text-[13px] font-medium tracking-snug text-white transition-colors hover:bg-[#1c1c1c] active:translate-y-px"
            >
              Show
            </button>
          </div>
          <p className="text-[12px] leading-[1.45] text-faint">
            Left empty, only the organization-wide rules are shown.
          </p>
        </form>

        <Suspense
          key={repository}
          fallback={
            <Panel title="Effective policy">
              <TableSkeleton rows={5} columns={4} />
            </Panel>
          }
        >
          <Effective repository={repository} />
        </Suspense>

        <div className="mt-6 grid grid-cols-1 gap-6 lg:grid-cols-2">
          <Suspense
            fallback={
              <Panel title="What happens to one request">
                <div className="flex flex-col gap-3.5 px-4 py-4 sm:px-5" aria-hidden>
                  <div className="skeleton h-9 w-full" />
                  <div className="skeleton h-9 w-full" />
                  <div className="skeleton h-9 w-28" />
                </div>
              </Panel>
            }
          >
            <Explain
              repository={repository}
              host={askedHost}
              path={askedPath}
              method={askedMethod}
            />
          </Suspense>

          <Panel
            title="Propose a rule"
            note={mayEdit ? undefined : `${actor.role ?? "this role"} may not edit policy`}
          >
            <form action={proposeRule} className="flex flex-col gap-3.5 px-4 py-4 sm:px-5">
              <Field
                label="Repository"
                htmlFor="propose-repository"
                hint="A rule is proposed against one repository. Organization-wide rules are set in the manifest."
              >
                <input
                  id="propose-repository"
                  name="repository"
                  defaultValue={repository}
                  required
                  disabled={!mayEdit}
                  placeholder="owner/name"
                  spellCheck={false}
                  className={inputClass}
                />
              </Field>
              <Field label="Host" htmlFor="propose-host" hint="A name, or a suffix such as .stripe.com.">
                <input
                  id="propose-host"
                  name="host"
                  required
                  disabled={!mayEdit}
                  placeholder="api.stripe.com"
                  spellCheck={false}
                  className={inputClass}
                />
              </Field>
              <Field label="Mode" htmlFor="propose-mode" hint={MODES[MODE_NAMES[0]!]!.means}>
                <select
                  id="propose-mode"
                  name="mode"
                  defaultValue="block"
                  disabled={!mayEdit}
                  className={selectClass}
                >
                  {MODE_NAMES.map((mode) => (
                    <option key={mode} value={mode}>
                      {mode}: {MODES[mode]!.means}
                    </option>
                  ))}
                </select>
              </Field>
              <Field
                label="Note"
                htmlFor="propose-note"
                hint="Why this host is named. Read by whoever reviews the change."
              >
                <input
                  id="propose-note"
                  name="note"
                  disabled={!mayEdit}
                  placeholder="payment intents, answered from the pack"
                  className={inputClass}
                />
              </Field>
              <button
                type="submit"
                disabled={!mayEdit}
                className="inline-flex h-9 items-center justify-center rounded-lg bg-ink px-3.5 text-[13px] font-medium tracking-snug text-white transition-colors hover:bg-[#1c1c1c] active:translate-y-px disabled:cursor-not-allowed disabled:bg-neutral disabled:opacity-60"
              >
                Propose rule
              </button>
              <p className="text-[12px] leading-[1.5] text-faint">
                This records the intent and changes nothing that is running.
              </p>
            </form>
          </Panel>
        </div>
      </Page>
    </Chrome>
  );
}

// ---------------------------------------------------------------------------

async function Effective({ repository }: { repository: string }) {
  let policy: EffectivePolicy;
  try {
    policy = await query<EffectivePolicy>("network.effective", {
      ...(repository ? { repository } : {}),
    });
  } catch (err) {
    if (isNavigation(err)) throw err;
    if (err instanceof ApiError && err.code === "NOT_FOUND") {
      return (
        <Failure
          title={`No repository named ${repository} in this organization`}
          detail="Either the name is wrong or it belongs to somebody else. The API answers the same way for both, on purpose: telling them apart would be a way to ask whether another organization has a repository by that name."
        />
      );
    }
    return (
      <Failure
        title="The policy could not be read"
        detail={err instanceof ApiError ? err.message : String(err)}
      />
    );
  }

  if (policy.rules.length === 0) {
    return (
      <Panel title="Effective policy" note={`default ${policy.default}`}>
        <Empty
          title={`Nothing is named, so everything is ${policy.default}ed`}
          says="No rule is stored for this scope, which means the default decides every request. That is a working policy and usually the right starting one: name a host only when something has to reach it."
        />
      </Panel>
    );
  }

  return (
    <Panel
      title="Effective policy"
      note={`${policy.rules.length} rules, then the ${policy.default} default`}
    >
      <TableFrame>
        <thead>
          <tr>
            <Th numeric className="w-[56px]">
              #
            </Th>
            <Th>Host</Th>
            <Th>Mode</Th>
            <Th>Narrowed to</Th>
            <Th>Note</Th>
          </tr>
        </thead>
        <tbody>
          {policy.rules.map((rule, i) => (
            <Tr key={`${rule.host}-${i}`}>
              <Td numeric className="text-faint">
                {i + 1}
              </Td>
              <Td>
                <Mono>{rule.host}</Mono>
              </Td>
              <Td>
                <ModeChip value={rule.mode} />
              </Td>
              <Td className="text-[12.5px] text-muted">
                {rule.paths?.length || rule.methods?.length ? (
                  <span className="flex flex-col gap-0.5">
                    {rule.methods?.length ? <span>{rule.methods.join(", ")}</span> : null}
                    {rule.paths?.length ? (
                      <Mono className="text-faint">{rule.paths.join(" ")}</Mono>
                    ) : null}
                  </span>
                ) : (
                  <span className="text-faint">the whole host</span>
                )}
              </Td>
              <Td className="max-w-[28ch] text-[12.5px] text-muted">
                {rule.note ?? <span className="text-faint">&mdash;</span>}
              </Td>
            </Tr>
          ))}
          <tr className="border-t border-edge bg-sunken/60">
            <Td numeric className="text-faint">
              &mdash;
            </Td>
            <Td className="text-[12.5px] text-muted">everything else</Td>
            <Td>
              <ModeChip value={policy.default} />
            </Td>
            <Td className="text-[12.5px] text-faint" >the default, which decides when nothing above matched</Td>
            <Td>{null}</Td>
          </tr>
        </tbody>
      </TableFrame>
    </Panel>
  );
}

// ---------------------------------------------------------------------------

async function Explain({
  repository,
  host,
  path,
  method,
}: {
  repository: string;
  host: string;
  path: string;
  method: string;
}) {
  let explanation: Explanation | null = null;
  let problem: string | null = null;

  if (host) {
    try {
      explanation = await query<Explanation>("network.explain", {
        ...(repository ? { repository } : {}),
        host,
        ...(path ? { path } : {}),
        ...(method ? { method } : {}),
      });
    } catch (err) {
    if (isNavigation(err)) throw err;
      problem = err instanceof ApiError ? err.message : String(err);
    }
  }

  return (
    <Panel title="What happens to one request">
      <form method="get" className="flex flex-col gap-3.5 border-b border-hair px-4 py-4 sm:px-5">
        <input type="hidden" name="repository" value={repository} />
        <Field label="Host" htmlFor="explain-host" hint="The name a service in the environment would look up.">
          <input
            id="explain-host"
            name="host"
            defaultValue={host}
            placeholder="api.stripe.com"
            spellCheck={false}
            className={inputClass}
          />
        </Field>
        <div className="grid grid-cols-1 gap-3.5 sm:grid-cols-[110px_1fr]">
          <Field label="Method" htmlFor="explain-method">
            <input
              id="explain-method"
              name="method"
              defaultValue={method}
              placeholder="POST"
              spellCheck={false}
              className={inputClass}
            />
          </Field>
          <Field label="Path" htmlFor="explain-path">
            <input
              id="explain-path"
              name="path"
              defaultValue={path}
              placeholder="/v1/payment_intents"
              spellCheck={false}
              className={inputClass}
            />
          </Field>
        </div>
        <button
          type="submit"
          className="inline-flex h-9 items-center justify-center rounded-lg border border-edge bg-surface px-3.5 text-[13px] font-medium tracking-snug text-ink transition-colors hover:bg-sunken"
        >
          Explain
        </button>
      </form>

      {problem ? (
        <div className="px-4 py-4 sm:px-5">
          <Failure title="That could not be explained" detail={problem} />
        </div>
      ) : !explanation ? (
        <Empty
          title="Name a host"
          says="The answer is which rule decides and why, which is the question worth asking before a rule is added rather than after an environment refuses something."
        />
      ) : (
        <div className="flex flex-col gap-4 px-4 py-4 sm:px-5">
          <div className="flex flex-wrap items-center gap-2">
            <ModeChip value={explanation.decision.mode} />
            <span className="text-[13px] text-muted">
              {explanation.decision.allowed
                ? "and it reaches the real destination"
                : "and nothing leaves the environment"}
            </span>
          </div>
          <p className="max-w-[70ch] text-[13px] leading-[1.6] text-ink">
            {explanation.decision.reason}
          </p>

          <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-[12.5px]">
            <Pair label="Decided by">
              {explanation.decision.matched ? (
                <Mono>{explanation.decision.ruleHost}</Mono>
              ) : (
                <span className="text-muted">the default</span>
              )}
            </Pair>
            <Pair label="TLS is terminated">
              {explanation.inspectsHost ? "yes, so paths and methods are read" : "no, tunnelled untouched"}
            </Pair>
            {explanation.decision.rateLimit ? (
              <Pair label="Rate limit">
                <Mono>{explanation.decision.rateLimit}</Mono>
              </Pair>
            ) : null}
            {explanation.decision.credential ? (
              <Pair label="Credential swapped in">
                <Mono>{explanation.decision.credential}</Mono>
              </Pair>
            ) : null}
          </dl>

          {explanation.chain.length > 0 ? (
            <details>
              <summary className="cursor-pointer text-[12.5px] text-muted hover:text-ink">
                {explanation.chain.length} rules were considered, in this order
              </summary>
              <ol className="mt-2 flex list-none flex-col gap-1.5">
                {explanation.chain.map((match, i) => (
                  <li key={i} className="flex gap-2.5 text-[12.5px] leading-[1.55]">
                    <span className="numeric shrink-0 text-faint">
                      {String(i + 1).padStart(2, "0")}
                    </span>
                    <span className="min-w-0">
                      <Mono>{match.rule.host}</Mono>
                      <span className="text-faint"> · specificity {match.specificity}</span>
                      <span className="block text-muted">{match.why}</span>
                    </span>
                  </li>
                ))}
              </ol>
            </details>
          ) : null}
        </div>
      )}
    </Panel>
  );
}

function Pair({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-[11.5px] uppercase tracking-[0.06em] text-faint">{label}</dt>
      <dd className="mt-0.5 text-ink">{children}</dd>
    </div>
  );
}
