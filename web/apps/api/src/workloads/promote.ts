// Turning an exploration into a workflow that runs every time, and being
// honest about the half that does not survive.
//
// WHAT PROMOTION ACTUALLY IS HERE, BECAUSE IT IS EASY TO OVERSELL.
//
// An exploration is a goal and a seed. The runner reads each page, chooses
// where to go, and records the moves it made and the places the application
// cost somebody effort. `engine/internal/explore.Compile` already turns that
// into the manifest block that replays it, and this is the same compilation
// with the same two admissions, written on the side that can version it.
//
// It does NOT produce a load scenario. The audit that started this workstream
// says so plainly and it is worth repeating where somebody might assume
// otherwise: an exploration emits a MANIFEST WORKFLOW. There is no path from a
// wander through a browser to a weighted traffic mix, because the two measure
// different things and nothing in the exploration record carries a rate.
//
// WHAT SURVIVES:
//
//   the name, so the workflow can be selected by it
//   the start path, taken from the first place the browser actually went
//   the journey, as a description in accessible names rather than selectors
//   a step budget with room above what the exploration spent
//   the goal, as the expectation
//
// WHAT DOES NOT, AND WHY EACH IS DROPPED RATHER THAN GUESSED AT:
//
//   The expectation is the goal sentence and not a passing page's words. An
//   exploration knows what it was looking for and does not know what a passing
//   run should say. A workflow whose expectation cannot be read comes back
//   `unverified`, which is the honest answer and not a pass.
//
//   A friction finding is not an expectation. "Pressing Upgrade plan changes
//   nothing" is a defect to fix, not an outcome to assert: a workflow that
//   asserted it would go green the day somebody broke it differently and red
//   the day somebody fixed it.
//
//   The unexplored parts of the application are named and not covered. The
//   compiled workflow walks one route.
//
//   An exploration that never reached its goal still compiles, and the
//   workflow then asserts something nobody has seen happen. That is said out
//   loud rather than silently produced.
//
// AND THE ONE THIS SIDE ADDS, WHICH THE ENGINE'S COMPILER DOES NOT HAVE TO
// SAY: the block has to be pasted into the repository's antifailure.yaml before
// anything can run it. `af test --only <name>` selects out of the manifest, and
// the control plane cannot put a file in somebody's repository. So a promoted
// version is a definition plus an instruction, and the route returns both.

/** What a promotion produced. */
export interface Compiled {
  slug: string
  name: string
  description: string
  /** The browser_workflow body: what to select once the block is in place. */
  body: { select: string[]; manifestBlock: string; dropped: string[] }
  /** One sentence per thing the compilation deliberately did not carry. */
  dropped: string[]
  /** The block to paste into antifailure.yaml. */
  manifestBlock: string
}

export class ExplorationRefused extends Error {}

/** How many journey steps a description enumerates before it says "and n more".
 *  The same twenty the engine's compiler uses, and for the same reason: a forty
 *  step exploration would spend the whole description on a list nobody reads
 *  past the tenth line. */
const MAX_JOURNEY_LINES = 20

interface Move {
  kind: string
  url?: string
  field?: string
  control?: string
}

interface Finding {
  kind: string
  url?: string
  control?: string
}

/**
 * Compiles an `af explore --json` exploration.
 *
 * Tolerant on every list and strict on the two fields that decide what the
 * workflow IS. A journey entry that cannot be read is skipped rather than
 * throwing, because one malformed move must not discard a whole walk; a missing
 * name or goal is refused, because a workflow with neither is not a workflow.
 */
export function compileExploration(raw: unknown, persona: string | null): Compiled {
  const e = object(raw)
  const name = text(e.name, 200)
  const goal = text(e.goal, 2000)
  if (name === null || goal === null) {
    throw new ExplorationRefused(
      'an exploration has to carry a name and a goal; this document carries neither, so there ' +
        'is nothing to compile into a workflow',
    )
  }
  const slug = slugify(name)
  if (slug === null) {
    throw new ExplorationRefused(
      `${name} has no letters or digits in it, so there is no name a manifest could carry`,
    )
  }

  const seed = text(e.seed, 200)
  const reached = e.reached === true
  const journey = moves(e.journey)
  const findings = findingList(e.findings)
  const missing = strings(e.missing, 100, 300)

  const dropped: string[] = [
    'The expectation is the goal, because an exploration knows what it was looking for and not ' +
      'what a passing page should say. Check its words appear on the page the run ends on, or ' +
      'rewrite it: a workflow whose expectation cannot be read comes back unverified rather ' +
      'than as a pass.',
  ]
  if (!reached) {
    dropped.push(
      'This exploration never reached the goal, so the workflow asserts something nobody has ' +
        'seen happen. Expect it to be unverified until the path exists.',
    )
  }
  for (const f of findings) {
    if (f.kind === 'goal_unreached') continue
    const where = f.control ? `${JSON.stringify(f.control)} on ${f.url ?? 'a page'}` : (f.url ?? 'a page')
    dropped.push(
      `The exploration found ${friction(f.kind)} at ${where}, which this workflow will not ` +
        `assert. A friction finding is something to fix, not an outcome to expect.`,
    )
  }
  if (missing.length > 0) {
    dropped.push(
      `${missing.length} ${missing.length === 1 ? 'part' : 'parts'} of the application ` +
        `${missing.length === 1 ? 'was' : 'were'} left unexplored. The compiled workflow covers ` +
        `only the route that was walked.`,
    )
  }
  dropped.push(
    'Nothing runs this until the block below is in the repository. `af test --only` selects out ' +
      'of antifailure.yaml, and a control plane cannot put a file in somebody else’s ' +
      'repository.',
  )

  const description = describe(goal, seed, reached, journey)
  const manifestBlock = block({
    name: slug,
    description,
    persona,
    startPath: startPath(journey),
    expect: sentence(goal),
    steps: journey.length + 10,
  })

  return {
    slug,
    name,
    description,
    body: { select: [slug], manifestBlock, dropped },
    dropped,
    manifestBlock,
  }
}

/** The manifest block, rendered by hand rather than through a YAML library.
 *
 *  Deliberately: the control plane has no YAML dependency, this block is four
 *  keys and a list, and the one thing a library would buy, escaping, is bought
 *  here by quoting every scalar with JSON.stringify, which produces a string a
 *  YAML parser reads identically. A library would also reflow and re-quote what
 *  it was given, and this text is pasted into somebody's file. */
function block(w: {
  name: string
  description: string
  persona: string | null
  startPath: string
  expect: string
  steps: number
}): string {
  const lines = [
    'workflows:',
    `  - name: ${JSON.stringify(w.name)}`,
    `    description: ${JSON.stringify(w.description)}`,
  ]
  if (w.persona) lines.push(`    persona: ${JSON.stringify(w.persona)}`)
  lines.push(
    `    start_path: ${JSON.stringify(w.startPath)}`,
    '    expect:',
    `      - ${JSON.stringify(w.expect)}`,
    '    budget:',
    `      steps: ${w.steps}`,
    '    tags:',
    '      - discovered',
  )
  return lines.join('\n')
}

/**
 * The description: what to do, in sentences, and then the route.
 *
 * The route is in it on purpose and it is accessible names rather than
 * selectors. A description saying "click #signup-btn" breaks when the markup
 * changes; an accessible name survives a redesign and disappears when somebody
 * removes the label a screen reader depends on, which is the failure a workflow
 * should have.
 */
function describe(goal: string, seed: string | null, reached: boolean, journey: Move[]): string {
  let out = sentence(goal)
  out += seed ? ` Found by an exploration from seed ${seed}` : ' Found by an exploration'
  if (journey.length === 0) return `${out}, which did not get anywhere.`
  out += reached ? ', which got there by: ' : ', which walked: '
  const shown = journey.slice(0, MAX_JOURNEY_LINES)
  out += shown.map((m) => lowerFirst(moveSentence(m))).join(', ')
  if (journey.length > shown.length) {
    out += `, and ${journey.length - shown.length} more steps`
  }
  return `${out}.`
}

function moveSentence(m: Move): string {
  switch (m.kind) {
    case 'goto':
      return `Open ${pathOf(m.url ?? '/')}`
    case 'fill':
      return `Fill ${m.field ?? 'a field'}`
    case 'click':
      return `Press ${JSON.stringify(m.control ?? 'a control')}`
    default:
      return m.kind
  }
}

/**
 * Where the exploration began, as a path.
 *
 * From the journey's first goto rather than from the goal, because the journey
 * carries the URL the browser was actually on and a goal whose start path
 * redirected somewhere else would compile a workflow that starts in the wrong
 * place. Every URL in a journey carries the preview environment's host, which
 * is different on every run and belongs to nobody's repository, so it is
 * reduced to a path: a manifest with 127.0.0.1:8731 in it reads as a mistake
 * within a day.
 */
function startPath(journey: Move[]): string {
  for (const m of journey) {
    if (m.kind === 'goto' && m.url) return pathOf(m.url)
  }
  return '/'
}

function pathOf(raw: string): string {
  try {
    // A relative base, so a path that is already relative parses and an
    // absolute one loses its host. A URL the parser refuses is not worth
    // guessing at, and "/" is where a workflow with no start path begins.
    const u = new URL(raw, 'http://environment.invalid')
    return u.pathname === '' ? '/' : u.pathname + u.search
  } catch {
    return '/'
  }
}

/** The six kinds an exploration reports, in the words a person reads. Unknown
 *  passes through, because the engine can add one by releasing and a control
 *  plane that refused it would drop a finding rather than name it. */
function friction(kind: string): string {
  switch (kind) {
    case 'no_effect':
      return 'a control that did nothing'
    case 'dead_end':
      return 'a dead end'
    case 'revisit':
      return 'a path that loops back'
    case 'unnamed_control':
      return 'an unnamed control'
    case 'slow_response':
      return 'something slow to answer'
    default:
      return kind
  }
}

function slugify(name: string): string | null {
  const s = name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 63)
    .replace(/-+$/g, '')
  return /^[a-z0-9]/.test(s) ? s : null
}

function sentence(s: string): string {
  const t = s.trim()
  if (t === '') return t
  return /[.!?]$/.test(t) ? t : `${t}.`
}

function lowerFirst(s: string): string {
  return s === '' ? s : s.slice(0, 1).toLowerCase() + s.slice(1)
}

// ---------------------------------------------------------------------------
// Reading a document this process did not write
// ---------------------------------------------------------------------------

function object(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
}

function text(value: unknown, max: number): string | null {
  if (typeof value !== 'string') return null
  const trimmed = value.trim()
  if (trimmed.length === 0 || trimmed.length > max) return null
  return trimmed
}

function strings(value: unknown, maxItems: number, maxLength: number): string[] {
  if (!Array.isArray(value)) return []
  const out: string[] = []
  for (const item of value.slice(0, maxItems)) {
    const s = text(item, maxLength)
    if (s !== null) out.push(s)
  }
  return out
}

/** The journey, skipping what cannot be read. One malformed move must not
 *  discard the walk: a description that lost every step is far worse than one
 *  that lost the third. */
function moves(value: unknown): Move[] {
  if (!Array.isArray(value)) return []
  const out: Move[] = []
  for (const item of value.slice(0, 400)) {
    const m = object(item)
    const kind = text(m.kind, 40)
    if (kind === null) continue
    out.push({
      kind,
      ...(text(m.url, 2048) ? { url: text(m.url, 2048)! } : {}),
      ...(text(m.field, 300) ? { field: text(m.field, 300)! } : {}),
      ...(text(m.control, 300) ? { control: text(m.control, 300)! } : {}),
    })
  }
  return out
}

function findingList(value: unknown): Finding[] {
  if (!Array.isArray(value)) return []
  const out: Finding[] = []
  for (const item of value.slice(0, 200)) {
    const f = object(item)
    const kind = text(f.kind, 60)
    if (kind === null) continue
    out.push({
      kind,
      ...(text(f.url, 2048) ? { url: text(f.url, 2048)! } : {}),
      ...(text(f.control, 300) ? { control: text(f.control, 300)! } : {}),
    })
  }
  return out
}
