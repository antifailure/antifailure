// The model planner reads the page and decides what a person would do next.
//
// It is what turns "sign up and confirm you land on a signed in page" from a
// sentence the deterministic planner half understands into one it follows.
// The key is yours: nothing here ships a credential, the engine never stores
// one, and with no key set the deterministic planner runs instead. That is not
// a limitation to work around later; it is the only arrangement that makes
// sense for a tool that runs inside your environment against your data.
//
// Two properties matter more than the prompt. The model chooses from a fixed
// set of actions against names that are actually on the page, so it cannot
// invent a button; anything it names that is not there is refused rather than
// attempted. And it never sees the page's raw HTML, only the accessibility
// snapshot, which keeps the request small and keeps whatever is in the DOM out
// of somebody else's logs.

import type { Action, Planner, Snapshot, Workflow } from './workflow.ts';
import { anchored, judgeAll } from './workflow.ts';
import { CassetteMiss } from './cassette.ts';

/** Which provider a key belongs to. */
export type Provider = 'anthropic' | 'openai';

/** How to reach a model. */
export interface ModelConfig {
  readonly provider: Provider;
  readonly apiKey: string;
  readonly model: string;
  readonly baseURL?: string;
  readonly maxTokens?: number;
  readonly timeoutMs?: number;
}

/** fromEnvironment reads a configuration from the usual variables.
 *
 * Returns undefined rather than throwing when there is no key, because no key
 * is the normal case and the deterministic planner handles it.
 */
export function fromEnvironment(env: Record<string, string | undefined>): ModelConfig | undefined {
  if (env.ANTHROPIC_API_KEY) {
    return {
      provider: 'anthropic',
      apiKey: env.ANTHROPIC_API_KEY,
      model: env.AF_MODEL ?? 'claude-sonnet-5',
      ...(env.ANTHROPIC_BASE_URL ? { baseURL: env.ANTHROPIC_BASE_URL } : {}),
    };
  }
  if (env.OPENAI_API_KEY) {
    return {
      provider: 'openai',
      apiKey: env.OPENAI_API_KEY,
      model: env.AF_MODEL ?? 'gpt-4.1',
      ...(env.OPENAI_BASE_URL ? { baseURL: env.OPENAI_BASE_URL } : {}),
    };
  }
  return undefined;
}

/** What the model is asked to return. */
interface Decision {
  readonly action: 'fill' | 'click' | 'done' | 'stuck';
  readonly target?: string;
  readonly value?: string;
  readonly why: string;
}

/** Sends one request to a model and returns its text. */
export type Complete = (prompt: string, config: ModelConfig) => Promise<string>;

/** ModelPlanner decides with a model, and falls back when it cannot. */
export class ModelPlanner implements Planner {
  readonly #config: ModelConfig;
  readonly #complete: Complete;
  readonly #fallback: Planner | undefined;

  constructor(config: ModelConfig, complete: Complete = callModel, fallback?: Planner) {
    this.#config = config;
    this.#complete = complete;
    this.#fallback = fallback;
  }

  async next(
    workflow: Workflow, snapshot: Snapshot, history: readonly Action[],
  ): Promise<Action> {
    // Checked before asking, because a page that already satisfies the
    // workflow does not need a model to say so, and every request that is not
    // made is a second and a fraction of a cent nobody spends.
    if (judgeAll(workflow.expect, snapshot.text) === 'met') {
      return { kind: 'done', why: 'Every expectation is visible on the page.' };
    }

    let raw: string;
    try {
      raw = await this.#complete(prompt(workflow, snapshot, history), this.#config);
    } catch (err) {
      // A cassette with no recording for this page is not a model outage, and
      // falling back would hide it: the run would quietly become a
      // deterministic one, pass, and nobody would learn that the recording had
      // gone stale. So it stops here and the run is blocked, which is a
      // statement about the recording rather than about the application.
      if (err instanceof CassetteMiss) {
        return { kind: 'stuck', why: err.message };
      }
      // A model that is unreachable is not evidence about the application. The
      // deterministic planner carries on if there is one, and if there is not
      // the run is stuck, which is blocked rather than failed.
      if (this.#fallback) return this.#fallback.next(workflow, snapshot, history);
      return {
        kind: 'stuck',
        why: `The model could not be reached: ${err instanceof Error ? err.message : String(err)}`,
      };
    }

    const decision = parseDecision(raw);
    if (!decision) {
      if (this.#fallback) return this.#fallback.next(workflow, snapshot, history);
      return { kind: 'stuck', why: `The model did not answer with an action: ${clip(raw)}` };
    }
    return toAction(decision, snapshot, this.#fallback === undefined);
  }
}

/** toAction turns a decision into something safe to do.
 *
 * The refusal is the important half. A model that names a button which is not
 * on the page gets nothing: the runner does not go looking for something
 * similar, because a click on the wrong control produces a result that looks
 * like an application failure and is not one.
 */
function toAction(decision: Decision, snapshot: Snapshot, strict: boolean): Action {
  switch (decision.action) {
    case 'done':
      return { kind: 'done', why: decision.why };
    case 'stuck':
      return { kind: 'stuck', why: decision.why };
    case 'fill': {
      const field = snapshot.fields.find((f) => f.name === decision.target);
      if (!field || decision.value === undefined) {
        return {
          kind: 'stuck',
          why: `The model asked to fill "${decision.target}", which is not a field on this page. ` +
            `The page offers ${snapshot.fields.map((f) => f.name).join(', ') || 'no fields'}.`,
        };
      }
      return { kind: 'fill', field: anchored(field.name), value: decision.value, why: decision.why };
    }
    case 'click': {
      const control = snapshot.controls.find((c) => c === decision.target);
      if (!control) {
        return {
          kind: 'stuck',
          why: `The model asked to press "${decision.target}", which is not on this page. ` +
            `The page offers ${snapshot.controls.slice(0, 10).join(', ') || 'no controls'}.`,
        };
      }
      return { kind: 'click', control: anchored(control), why: decision.why };
    }
    default:
      void strict;
      return { kind: 'stuck', why: `The model asked for an action the runner does not have.` };
  }
}

/** prompt describes the page and the goal, and nothing else.
 *
 * No HTML, no cookies, no local storage. The accessibility snapshot is what a
 * person navigating with a screen reader gets, it is enough to decide from,
 * and it keeps whatever is in the DOM out of somebody else's logs.
 */
export function prompt(
  workflow: Workflow, snapshot: Snapshot, history: readonly Action[],
): string {
  const done = history
    .slice(-8)
    .map((a) => (a.kind === 'fill' || a.kind === 'click' ? `${a.kind}: ${a.why}` : a.kind))
    .join('\n');

  return [
    `You are driving a web application to carry out one task, the way a person would.`,
    ``,
    `The task: ${workflow.description}`,
    ``,
    `It is finished when all of these are true:`,
    ...workflow.expect.map((e) => `- ${e}`),
    ``,
    `The page you are on is ${snapshot.url}, titled "${snapshot.title}".`,
    ``,
    `Fields you can fill (by exact name):`,
    ...(snapshot.fields.length > 0
      ? snapshot.fields.map((f) => `- ${f.name} (${f.type})${f.filled ? ' already filled' : ''}`)
      : ['- none']),
    ``,
    `Controls you can press (by exact name):`,
    ...(snapshot.controls.length > 0
      ? snapshot.controls.slice(0, 40).map((c) => `- ${c}`)
      : ['- none']),
    ``,
    `Visible text:`,
    clip(snapshot.text, 4000),
    ``,
    ...(done ? [`What you have already done:`, done, ``] : []),
    `Answer with one JSON object and nothing else:`,
    `{"action":"fill","target":"<exact field name>","value":"<what to type>","why":"<one sentence>"}`,
    `{"action":"click","target":"<exact control name>","why":"<one sentence>"}`,
    `{"action":"done","why":"<why the task is finished>"}`,
    `{"action":"stuck","why":"<why nothing here moves the task forward>"}`,
    ``,
    `Use names exactly as listed. A name that is not listed will be refused.`,
    `Use example.test addresses and the card 4242424242424242 for test data.`,
    `Say done only when the page actually shows what the task asked for.`,
  ].join('\n');
}

/** parseDecision reads the model's answer.
 *
 * Tolerant on the way in, because a model wraps JSON in prose often enough
 * that refusing it would waste a step for nothing, and strict about what comes
 * out: an object without a usable action is no decision at all.
 */
export function parseDecision(raw: string): Decision | undefined {
  const candidates: string[] = [];
  const fenced = raw.match(/```(?:json)?\s*([\s\S]*?)```/);
  if (fenced?.[1]) candidates.push(fenced[1]);
  const braced = raw.match(/\{[\s\S]*\}/);
  if (braced?.[0]) candidates.push(braced[0]);
  candidates.push(raw);

  for (const candidate of candidates) {
    try {
      const parsed = JSON.parse(candidate.trim()) as Partial<Decision>;
      if (parsed.action && ['fill', 'click', 'done', 'stuck'].includes(parsed.action)) {
        return {
          action: parsed.action,
          ...(parsed.target === undefined ? {} : { target: String(parsed.target) }),
          ...(parsed.value === undefined ? {} : { value: String(parsed.value) }),
          why: parsed.why ? String(parsed.why) : 'The model gave no reason.',
        };
      }
    } catch {
      continue;
    }
  }
  return undefined;
}

function clip(s: string, max = 400): string {
  return s.length <= max ? s : s.slice(0, max) + `\n... (${s.length - max} more characters)`;
}

/** callModel sends one request, using whichever provider the key belongs to. */
export const callModel: Complete = async (text, config) => {
  const timeoutMs = config.timeoutMs ?? 60_000;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    if (config.provider === 'anthropic') {
      const res = await fetch((config.baseURL ?? 'https://api.anthropic.com') + '/v1/messages', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          'x-api-key': config.apiKey,
          'anthropic-version': '2023-06-01',
        },
        body: JSON.stringify({
          model: config.model,
          max_tokens: config.maxTokens ?? 512,
          messages: [{ role: 'user', content: text }],
        }),
        signal: controller.signal,
      });
      if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
      const body = await res.json() as { content?: { text?: string }[] };
      return body.content?.map((c) => c.text ?? '').join('') ?? '';
    }

    const res = await fetch((config.baseURL ?? 'https://api.openai.com') + '/v1/chat/completions', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        authorization: `Bearer ${config.apiKey}`,
      },
      body: JSON.stringify({
        model: config.model,
        max_tokens: config.maxTokens ?? 512,
        messages: [{ role: 'user', content: text }],
      }),
      signal: controller.signal,
    });
    if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
    const body = await res.json() as { choices?: { message?: { content?: string } }[] };
    return body.choices?.[0]?.message?.content ?? '';
  } finally {
    clearTimeout(timer);
  }
};
