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
import { fromEnvironment } from './model.ts';
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

  const model = fromEnvironment(process.env);
  if (model) {
    process.stderr.write(
      `af-runner: reading pages with ${model.provider}/${model.model}\n`,
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
