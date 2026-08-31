#!/usr/bin/env node
// af-runner reads a job on standard input and writes results to standard
// output, as JSON.
//
// A subprocess with a JSON boundary rather than a library, because the engine
// is Go and the browser automation that works is TypeScript, and pretending
// otherwise would mean either a worse browser driver or a foreign function
// interface. The boundary is one document in and one document out, which is
// also the boundary a person can drive by hand when they want to.

import { readFileSync } from 'node:fs';
import { mkdirSync } from 'node:fs';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { run, type Job, type WorkflowResult } from './execute.ts';
import { explore, type Exploration, type Goal } from './explore.ts';
import { CommandInbox } from './inbox.ts';
import { exitCodeFor } from './verdict.ts';
import { callModel, fromEnvironment, type ModelConfig } from './model.ts';
import { cassetteFromEnvironment } from './cassette.ts';
import type { Persona } from './login.ts';
import type { Workflow } from './workflow.ts';

const exec = promisify(execFile);

/** The document the engine sends. */
interface JobDocument {
  readonly base_url: string;
  readonly artifacts: string;
  readonly workflows: readonly Workflow[];
  readonly personas: readonly Persona[];
  /** goals are exploratory runs. Present for 'af explore', absent for
   *  'af test'. One entry point rather than two binaries, because the browser,
   *  the sign in and the evidence capture are the same in both and a second
   *  main is a second place for them to drift. */
  readonly goals?: readonly Goal[];
  /** af is the path to the engine binary, used to read the inbox. Absent
   *  means no inbox, and a workflow needing one is blocked rather than
   *  failed. */
  readonly af?: string;
  readonly work_dir?: string;
  readonly attempts?: number;
  readonly headless?: boolean;
}

/** The document the engine reads back. */
interface ResultDocument {
  readonly results: readonly WorkflowResult[];
  readonly explorations: readonly Exploration[];
  readonly passed: number;
  readonly failed: number;
  readonly flaky: number;
  readonly blocked: number;
  readonly unverified: number;
}

async function main(): Promise<number> {
  const raw = readFileSync(0, 'utf8');
  if (!raw.trim()) {
    process.stderr.write('af-runner: expected a job document on standard input\n');
    return 2;
  }
  const doc = JSON.parse(raw) as JobDocument;
  mkdirSync(doc.artifacts, { recursive: true });

  // A cassette, if one is configured, and the model configuration that goes
  // with it. The two interact in one way worth spelling out: in replay mode
  // there does not have to be a key at all. That is the whole point. A
  // scheduled run reads answers off disk, reaches no network, and costs
  // nothing, and it must not silently become a deterministic run just because
  // nobody set ANTHROPIC_API_KEY on the schedule.
  const cassette = cassetteFromEnvironment(process.env);
  let model = fromEnvironment(process.env);
  if (!model && cassette?.mode === 'replay') {
    model = replayOnlyConfig(process.env);
  }

  // In replay the network is not merely unused, it is unreachable: a miss
  // throws rather than falling through to this, and this exists so that a
  // future edit which removes that guard fails loudly instead of spending.
  const complete = cassette
    ? cassette.wrap(
        cassette.mode === 'record'
          ? callModel
          : () => {
              throw new Error(
                'a replaying cassette tried to call the model, which it must never do',
              );
            },
      )
    : undefined;

  if (model) {
    const how = cassette
      ? `${cassette.mode === 'record' ? 'recording' : 'replaying'} ${cassette.size()} answers in ${cassette.dir}`
      : 'live';
    process.stderr.write(
      `af-runner: reading pages with ${model.provider}/${model.model}, ${how}\n`,
    );
  }

  const job: Job = {
    baseURL: doc.base_url,
    artifacts: doc.artifacts,
    workflows: doc.workflows,
    personas: doc.personas,
    ...(doc.attempts === undefined ? {} : { attempts: doc.attempts }),
    // Read from this process's environment rather than sent in the job, so a
    // key never passes through a file the engine wrote or a document anybody
    // logged.
    ...(model ? { model } : {}),
    ...(complete ? { complete } : {}),
    ...(doc.headless === undefined ? {} : { headless: doc.headless }),
    ...(doc.af
      ? {
          inbox: new CommandInbox(async (args) => {
            const { stdout } = await exec(doc.af!, [...args], {
              cwd: doc.work_dir ?? process.cwd(),
              maxBuffer: 32 * 1024 * 1024,
            });
            return stdout;
          }),
        }
      : {}),
  };

  const results = doc.workflows.length > 0 ? await run(job) : [];
  const explorations = doc.goals?.length
    ? await explore({
        baseURL: doc.base_url,
        artifacts: doc.artifacts,
        goals: doc.goals,
        personas: doc.personas,
        ...(job.inbox ? { inbox: job.inbox } : {}),
        ...(doc.headless === undefined ? {} : { headless: doc.headless }),
      })
    : [];


  if (cassette) {
    process.stderr.write(
      `af-runner: cassette ${cassette.mode}: ${cassette.read} replayed, ${cassette.written} recorded\n`,
    );
  }
  const counted = { passed: 0, failed: 0, flaky: 0, blocked: 0, unverified: 0 };
  for (const verdict of [
    ...results.map((r) => r.outcome.verdict),
    ...explorations.map((e) => e.outcome.verdict),
  ]) {
    switch (verdict) {
      case 'pass': counted.passed++; break;
      case 'fail': counted.failed++; break;
      case 'flaky': counted.flaky++; break;
      case 'blocked': counted.blocked++; break;
      case 'unverified': counted.unverified++; break;
    }
  }
  const out: ResultDocument = { results, explorations, ...counted };
  process.stdout.write(JSON.stringify(out, replaceRegExp, 2) + '\n');
  return exitCodeFor([
    ...results.map((r) => r.outcome),
    ...explorations.map((e) => e.outcome),
  ]);
}

/** The model to replay as, when there is no key to read one from.
 *
 * A replaying run never sends a request, so the key is not merely unused, it
 * is not needed. The provider and the model name still are: they are part of
 * the key a recording is filed under, so replaying as the wrong model finds
 * nothing rather than finding the wrong thing.
 */
function replayOnlyConfig(env: Record<string, string | undefined>): ModelConfig {
  const provider = env.AF_MODEL_PROVIDER === 'openai' ? 'openai' : 'anthropic';
  return {
    provider,
    // Deliberately empty. Nothing reads it in replay, and a placeholder that
    // looked like a key would be the kind of string that ends up in a log.
    apiKey: '',
    model: env.AF_MODEL ?? (provider === 'anthropic' ? 'claude-sonnet-5' : 'gpt-4.1'),
  };
}

/** replaceRegExp makes the patterns readable in the output document.
 *
 * A RegExp serialises to {} by default, so a step that says which control it
 * pressed would come out empty, which is exactly the field somebody reads.
 */
function replaceRegExp(_key: string, value: unknown): unknown {
  return value instanceof RegExp ? value.source : value;
}

main().then(
  (code) => process.exit(code),
  (err: unknown) => {
    // A crash here is the runner's own, and it says so. The engine reports it
    // as blocked rather than as a failing test, because a runner that could
    // not start is not evidence about the application.
    process.stderr.write(
      `af-runner: ${err instanceof Error ? (err.stack ?? err.message) : String(err)}\n`,
    );
    process.exit(1);
  },
);
