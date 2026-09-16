// The terminal surface driver: drives a command line program the way the web
// driver drives a browser.
//
// A terminal's rendered text is its accessibility tree, so the same workflow
// model transfers: send input, read what the program rendered, and judge it
// against the words a person would look for. The program's output IS the live
// cast, which is why a terminal agent needs no frame pump: its steps are the
// stream, rendered directly by `af watch` and the console.
//
// Line oriented rather than a full pseudo-terminal. A raw PTY that a curses UI
// needs is a native dependency the runner does not carry, and it is the
// documented next step for this driver; a great many CLIs are line oriented
// (they read a line, print lines, exit), and this drives those end to end
// today: real process, real stdin, real stdout, a real verdict.

import { spawn } from 'node:child_process';
import { judgeAll } from '../workflow.ts';
import { classify, type Attempt, type Cause } from '../verdict.ts';
import { nullSink, type LiveSink } from '../live.ts';
import type { WorkflowResult } from '../execute.ts';

/** A terminal workflow: a program to run, what to type at it, and the words its
 *  output must show for the workflow to have passed. */
export interface TerminalWorkflow {
  readonly name: string;
  readonly command: string;
  readonly args?: readonly string[];
  /** input lines written to the program's standard input, in order. */
  readonly input?: readonly string[];
  /** expect are the strings that must all appear in the output. */
  readonly expect: readonly string[];
  /** maxMs bounds the run; the program is killed past it and the workflow is
   *  blocked rather than judged, the same rule the web driver follows. */
  readonly maxMs?: number;
}

/** What one terminal run needs. */
export interface TerminalJob {
  readonly workflows: readonly TerminalWorkflow[];
  readonly cwd?: string;
  readonly live?: LiveSink;
}

const DEFAULT_MAX_MS = 30_000;

/** runTerminal drives every terminal workflow and returns a result for each,
 *  in the same shape the web driver produces so the counting and the report do
 *  not care which surface a run used. */
export async function runTerminal(job: TerminalJob): Promise<WorkflowResult[]> {
  const sink = job.live ?? nullSink();
  for (const workflow of job.workflows) {
    sink.agent({ id: workflow.name, workflow: workflow.name, surface: 'terminal' }, 'pending');
  }
  const results: WorkflowResult[] = [];
  for (const workflow of job.workflows) {
    results.push(await runOneTerminal(workflow, job.cwd, sink));
  }
  return results;
}

async function runOneTerminal(
  workflow: TerminalWorkflow, cwd: string | undefined, sink: LiveSink,
): Promise<WorkflowResult> {
  const started = Date.now();
  const desc = { id: workflow.name, workflow: workflow.name, surface: 'terminal' as const };
  const steps: string[] = [];
  const record = (text: string, action?: string) => {
    steps.push(text);
    sink.step(desc.id, { text, ...(action ? { action } : {}) });
  };

  sink.agent(desc, 'connecting');
  const invocation = [workflow.command, ...(workflow.args ?? [])].join(' ');
  record(`Run ${invocation}`, 'spawn');

  const outcome = await new Promise<{ cause: Cause; detail: string; output: string }>((resolve) => {
    let child;
    try {
      child = spawn(workflow.command, [...(workflow.args ?? [])], {
        cwd,
        stdio: ['pipe', 'pipe', 'pipe'],
      });
    } catch (err) {
      // The program could not be started. That is the runner's own failure,
      // not evidence about the application, so it is blocked.
      resolve({
        cause: 'runner-failure',
        detail: err instanceof Error ? err.message : String(err),
        output: '',
      });
      return;
    }
    sink.agent(desc, 'live');

    let output = '';
    let carry = '';
    // Each finished output line is a step, which is what makes the terminal
    // cast live: a watcher sees the program's output as it prints.
    const onChunk = (chunk: Buffer) => {
      const text = chunk.toString('utf8');
      output += text;
      carry += text;
      const lines = carry.split('\n');
      carry = lines.pop() ?? '';
      for (const line of lines) {
        if (line.trim()) record(line, 'output');
      }
    };
    child.stdout?.on('data', onChunk);
    child.stderr?.on('data', onChunk);

    const timer = setTimeout(() => {
      child.kill('SIGKILL');
      resolve({
        cause: 'budget-exhausted',
        detail: `The program was still running after its budget of ${workflow.maxMs ?? DEFAULT_MAX_MS} ms and was stopped.`,
        output,
      });
    }, workflow.maxMs ?? DEFAULT_MAX_MS);
    timer.unref?.();

    child.on('error', (err) => {
      clearTimeout(timer);
      resolve({ cause: 'runner-failure', detail: err.message, output });
    });
    child.on('close', (code) => {
      clearTimeout(timer);
      if (carry.trim()) record(carry, 'output');
      // The same three way check the web driver uses, with the exit code
      // playing the part HTTP status plays there: a nonzero exit is a program
      // that failed, which outranks an unread expectation, exactly as a page
      // answering 500 does in finalJudgement. Met always wins: a program that
      // showed what was expected passed however it exited.
      const verdict = judgeAll(workflow.expect, output);
      if (verdict === 'met') {
        resolve({ cause: 'succeeded', detail: 'Every expectation appeared in the output.', output });
      } else if (code !== 0) {
        resolve({
          cause: 'application-error',
          detail: `The program exited ${code} and the output did not show what was expected.`,
          output,
        });
      } else if (verdict === 'unmet') {
        resolve({
          cause: 'expectation-not-met',
          detail: 'The output showed a failure rather than what was expected.',
          output,
        });
      } else {
        resolve({
          cause: 'page-unreadable',
          detail: 'Nothing in the output confirmed or contradicted what was expected.',
          output,
        });
      }
    });

    // Type the input, then close stdin so a program waiting on end of input
    // proceeds. Written after the handlers are attached so no early output is
    // missed.
    for (const line of workflow.input ?? []) {
      child.stdin?.write(line + '\n');
      record(`Type ${JSON.stringify(line)}`, 'input');
    }
    child.stdin?.end();
  });

  const attempt: Attempt = {
    cause: outcome.cause,
    detail: outcome.detail,
    durationMs: Date.now() - started,
  };
  const classified = classify([attempt]);
  sink.agent(desc, 'ended', classified.verdict);
  return {
    workflow: workflow.name,
    outcome: classified,
    steps,
    evidence: { console: [], failed: [] },
    durationMs: Date.now() - started,
    startedAt: new Date(started).toISOString(),
    finishedAt: new Date().toISOString(),
  };
}
