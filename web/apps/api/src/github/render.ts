// What a person reads: one comment, one check, both about one commit.
//
// THE FIRST LINE OF THE COMMENT IS A FENCE, not decoration. It carries the
// commit the comment is about, and every writer compares it before writing.
// Without that, the ordering that breaks this is ordinary rather than exotic:
// somebody pushes, the first run is cancelled, the cancellation finishes AFTER
// the second run started, and the comment ends up reporting the commit that is
// no longer the head. The reader has no way to tell, because a comment does not
// say which commit it is about unless somebody puts it there.
//
// A LINK THAT 404s OR LANDS ON A SIGN-IN SCREEN WITH NO WAY BACK IS A DEFECT.
// Two kinds of dead link were already reachable here. An address like
// http://127.0.0.1:46001 is the environment as seen from the runner that built
// it, and GitHub-flavoured Markdown auto-links a bare URL, so the comment
// offered every reader a link to their own machine. And `Trace:` names a file
// on that runner's disk, which is gone the moment the job ends unless the
// workflow uploaded it. Both are rewritten below so they read as what they are.

import type { CheckOutput } from './api.ts'
import type { GenerationState } from './states.ts'
import { checkShapeFor } from './states.ts'

/** The marker that identifies the comment this control plane maintains.
 *
 *  Deliberately NOT the engine's own `<!-- antifailure:report -->`. The
 *  workflow's fallback step writes that one, and two writers sharing one
 *  comment means the one with no SHA fence overwrites the one with it. They are
 *  separate comments and only one of them is ever created, because the workflow
 *  step is skipped when the control plane accepted the report. */
export const COMMENT_MARKER = '<!-- antifailure:pull-request'

/** The check's name. A repository makes THIS string required, so changing it
 *  silently un-requires the check everywhere and every branch protection rule
 *  naming the old one starts waiting for a check that will never arrive. */
export const CHECK_NAME = 'Antifailure'

export type TeardownState = 'none' | 'pending' | 'leased' | 'acknowledged' | 'abandoned'

export interface CommentInput {
  state: GenerationState
  /** True when the state is unverified because the deadline passed rather than
   *  because the run said so. */
  timedOut: boolean
  repository: string
  pullNumber: number
  headSha: string
  attempt: number
  /** One sentence saying why this state, when there is one. */
  detail: string | null
  /** The report `af ci --report` produced, when a run got that far. */
  reportMarkdown: string | null
  envId: string | null
  previewUrl: string | null
  teardown: TeardownState
  /** Where the console lives, so a self-hosted installation links to its own.
   *  Absent means no console link is offered, which is honest: a link to a
   *  console that is not there is worse than no link. */
  consoleBase: string | null
  /** Set when the check could not be published, so the comment says why rather
   *  than leaving somebody looking for a check that is not coming. */
  checksUnavailable: string | null
}

/** The first seven characters, which is what everybody reads a commit as. */
export function shortSha(sha: string): string {
  return sha.slice(0, 7)
}

/**
 * Reads the commit a comment says it is about.
 *
 * Null for a comment written before this fence existed, or by anything else.
 * The caller treats null as "not ours to compare", which is the safe direction:
 * it writes, rather than declining forever over a comment it cannot read.
 */
export function shaOfComment(body: string): string | null {
  const match = /<!-- antifailure:pull-request sha=([0-9a-f]{7,40}) /.exec(body)
  return match ? match[1]! : null
}

function fence(headSha: string, attempt: number): string {
  // The attempt is in the fence as well as the SHA, because pressing Re-run
  // produces a second answer about the SAME commit and the comment has to be
  // allowed to change for it.
  //
  // The marker is the OPENING of the comment rather than a whole HTML comment,
  // so that the string findComment searches for is genuinely present in the
  // body it is searching. A closed marker plus a separate fenced line looks
  // right and finds nothing, and finding nothing here means posting a second
  // comment on every push.
  return `${COMMENT_MARKER} sha=${headSha} attempt=${attempt} -->`
}

export function commentBody(input: CommentInput): string {
  const shape = checkShapeFor(input.state, input.timedOut)
  const lines: string[] = [fence(input.headSha, input.attempt), '']

  lines.push(`### Antifailure: ${shape.title}`)
  lines.push('')
  lines.push(
    `This is about commit \`${shortSha(input.headSha)}\`. ` +
      (shape.status === 'completed'
        ? shape.passes
          ? 'Nothing here blocks the merge.'
          : 'This does not pass, so a branch rule requiring Antifailure will hold the merge.'
        : 'It is not finished, so nothing below is final.'),
  )
  lines.push('')

  if (input.detail) {
    lines.push(input.detail)
    lines.push('')
  }

  if (input.checksUnavailable) {
    lines.push(`> There is no check run for this commit. ${input.checksUnavailable}`, '')
  }

  const environment = environmentLine(input)
  if (environment) {
    lines.push(environment, '')
  }

  if (input.reportMarkdown) {
    lines.push(reachable(stripEngineMarker(input.reportMarkdown)))
    lines.push('')
  }

  lines.push(teardownLine(input.teardown))
  lines.push('')
  lines.push(
    '<details><summary>Run this commit yourself</summary>',
    '',
    '```sh',
    `git fetch origin ${input.headSha}`,
    `git checkout ${input.headSha}`,
    'af ci --report report.md',
    '```',
    '',
    '</details>',
  )
  return lines.join('\n') + '\n'
}

/**
 * The environment, as a link somebody can actually follow.
 *
 * The console link is the one that works: it is served by this control plane,
 * it is behind the reader's own session, and the sign-in path carries the
 * return address so following it while signed out comes back here rather than
 * dumping the reader on a dashboard. The preview address is not offered as a
 * link at all when it is on a runner, because it is not reachable from the
 * reader's machine and a link that is not is worse than a sentence.
 */
function environmentLine(input: CommentInput): string | null {
  if (!input.envId) return null
  const name = `\`${input.envId}\``
  if (!input.consoleBase) {
    return `Environment ${name}.`
  }
  const href = `${input.consoleBase.replace(/\/+$/, '')}/environments?env=${encodeURIComponent(input.envId)}`
  return `Environment [${input.envId}](${href}), with its runs, verdicts and evidence.`
}

function teardownLine(state: TeardownState): string {
  switch (state) {
    case 'none':
      return 'Teardown: nothing to remove.'
    case 'pending':
      return 'Teardown: asked for, not confirmed yet.'
    case 'leased':
      return 'Teardown: in progress.'
    case 'acknowledged':
      return 'Teardown: done. The environment is gone.'
    case 'abandoned':
      return (
        'Teardown: **gave up**. Something may still be running on the runner. ' +
        'Run `af down` against this branch, or `af env prune` on the machine that built it.'
      )
  }
}

/** The engine writes its own marker at the top of the report so the workflow's
 *  own comment step can find it. Two markers in one comment is one too many:
 *  the workflow would then find THIS comment and update it, and the SHA fence
 *  would be gone on the next push. */
function stripEngineMarker(markdown: string): string {
  return markdown.replace(/^<!--\s*antifailure:report\s*-->\s*\n?/, '').trim()
}

const LOOPBACK =
  /\bhttps?:\/\/(?:127(?:\.\d{1,3}){3}|localhost|0\.0\.0\.0|\[::1\]|10(?:\.\d{1,3}){3}|192\.168(?:\.\d{1,3}){2}|172\.(?:1[6-9]|2\d|3[01])(?:\.\d{1,3}){2})(?::\d+)?(?:\/\S*)?/g

/**
 * Turns the addresses and paths that only exist on the runner into text.
 *
 * Two rewrites, and both are about the reader rather than about tidiness.
 *
 * A bare URL is auto-linked by GitHub-flavoured Markdown, so
 * `http://127.0.0.1:46001` in a comment is a clickable link to the reader's own
 * machine. Wrapping it in backticks stops the auto-link, and the sentence after
 * it says whose machine it was.
 *
 * `Trace: ` names a Playwright trace on the runner's disk. That file exists for
 * as long as the job does. Saying so is the difference between a reader
 * wondering why the path does not open and a reader knowing to upload it.
 */
export function reachable(markdown: string): string {
  let out = markdown.replace(LOOPBACK, (url) => `\`${url}\` (on the runner)`)
  out = out.replace(
    /^Trace: `([^`]+)`$/gm,
    'Trace: `$1` on the runner, which keeps it only for the length of the job. ' +
      'Add an upload-artifact step to bring it back.',
  )
  return out
}

/**
 * The check run's output.
 *
 * Deliberately shorter than the comment. A check's summary is read in a list
 * beside the other checks, and the long half is behind a click, so the summary
 * says what happened and where to read more and nothing else.
 */
export function checkOutputFor(input: CommentInput): CheckOutput {
  const shape = checkShapeFor(input.state, input.timedOut)
  const summary: string[] = [`Commit \`${shortSha(input.headSha)}\`.`]
  if (input.detail) summary.push(input.detail)
  if (!shape.passes && shape.status === 'completed') {
    summary.push('This does not pass a branch rule that requires Antifailure.')
  }
  const text = input.reportMarkdown ? reachable(stripEngineMarker(input.reportMarkdown)) : undefined
  return {
    title: shape.title,
    summary: summary.join(' '),
    ...(text ? { text } : {}),
  }
}
