// A personality is HOW an agent behaves, not WHO it signs in as.
//
// The engine resolves the manifest's diversity block into a fixed per-agent
// plan and sends it in the job document; this file is the runner's view of
// that plan plus the one mechanical thing it does with it. A personality's
// whole effect is a preamble prepended to the model prompt: the action grammar,
// the exact-name refusal, and the test-data rule below it are untouched, so a
// personality can only reorder the agent's preference among the controls
// already on the page and change how it explains its choice. It can never name
// a control that is not there.
//
// Nothing here draws or randomises. The engine already did that from the seed,
// which is what keeps a personality driven run replayable: the same seed
// assigns the same personalities, and because the preamble is part of the
// prompt and the cassette keys on the whole prompt, each personality records
// and replays its own answers.

/** The second, computed layer of behavioral variance, from the engine. */
export interface DiversityProfile {
  readonly personaId: string;
  readonly strategyArchetype: string;
  readonly riskStyle: string;
  readonly pacingStyle: string;
  readonly attentionBias: string;
  readonly errorResponseStyle: string;
  readonly cognitiveStyle: string;
  readonly noveltyBias: number;
  readonly seed: number;
  readonly profileKey: string;
}

/** One built in personality, resolved by the engine. */
export interface Personality {
  readonly id: string;
  readonly name: string;
  readonly reasoningPrompt: string;
}

/** One personality varied agent driving one workflow. */
export interface Assignment {
  readonly workflow: string;
  readonly agentIndex: number;
  readonly personality: Personality;
  readonly profile: DiversityProfile;
}

/** The whole resolved plan, keyed by workflow name. */
export interface ResolvedDiversity {
  readonly seed: string;
  readonly assignments: Readonly<Record<string, readonly Assignment[]>>;
  readonly diagnostics: {
    readonly uniquenessScore: number;
    readonly strategyCount: number;
    readonly personaCount: number;
  };
}

/** agentsFor returns the assignments for a workflow, or a single neutral run.
 *
 * A single `undefined` rather than an empty list, because a workflow with no
 * personality plan is one ordinary run, which is exactly today's behavior. The
 * caller loops over what this returns and a neutral run is the `undefined`.
 */
export function agentsFor(
  diversity: ResolvedDiversity | undefined, workflow: string,
): readonly (Assignment | undefined)[] {
  const list = diversity?.assignments[workflow];
  if (!list || list.length === 0) return [undefined];
  return list;
}

/** preamble compiles a personality into the text prepended to the prompt.
 *
 * Two parts: the personality's reasoning instruction, then one line per axis of
 * the computed profile phrased as a decision lens. Both are only preference: a
 * closing sentence restates that the actions and the exact names below are the
 * whole surface, so nothing here can be read as permission to invent a control.
 */
export function preamble(a: Assignment): string {
  const lines = [
    a.personality.reasoningPrompt,
    ``,
    `Your behavioral profile for this run is a lens on the same task, not a new set of actions:`,
    `- Pace: ${pace(a.profile.pacingStyle)}`,
    `- Risk: ${risk(a.profile.riskStyle)}`,
    `- Attention: ${attention(a.profile.attentionBias)}`,
    `- On a dead end or an error: ${errorResponse(a.profile.errorResponseStyle)}`,
    ``,
    `This changes which of the controls already listed below you prefer and how you explain your choice. It never adds a control that is not listed, and the rules below still hold exactly.`,
  ];
  return lines.join('\n');
}

/** traits is the computed profile in a few words, for a watcher's pane.
 *
 * Three axes rather than all six, and short words rather than the preamble's
 * sentences, because this has to be readable at a glance in a pane thirty
 * characters wide beside five others. The three chosen are the ones that show
 * up in what an agent visibly DOES: how fast it moves, how far off the obvious
 * path it will go, and what it looks at. The rest of the profile is still in
 * the preamble and still in the report.
 */
export function traits(profile: DiversityProfile): string {
  return [
    shortPace(profile.pacingStyle),
    shortRisk(profile.riskStyle),
    shortAttention(profile.attentionBias),
  ].join(' \u00b7 ');
}

function shortPace(s: string): string {
  switch (s) {
    case 'slow': return 'unhurried';
    case 'fast': return 'impatient';
    default: return 'steady';
  }
}

function shortRisk(s: string): string {
  switch (s) {
    case 'conservative': return 'cautious';
    case 'risky': return 'bold';
    default: return 'balanced';
  }
}

function shortAttention(s: string): string {
  switch (s) {
    case 'visual_heavy': return 'follows prominence';
    case 'text_heavy': return 'reads labels';
    case 'cta_focused': return 'button first';
    case 'navigation_focused': return 'nav first';
    default: return 'reads evenly';
  }
}

function pace(s: string): string {
  switch (s) {
    case 'slow': return 'read labels and any help or trust text before acting';
    case 'fast': return 'prefer the first control that plainly advances the task and do not deliberate';
    default: return 'move at a steady pace';
  }
}

function risk(s: string): string {
  switch (s) {
    case 'conservative': return 'prefer the safe, clearly signposted option';
    case 'risky': return 'be willing to take a less obvious path';
    default: return 'weigh the obvious and the less obvious options evenly';
  }
}

function attention(s: string): string {
  switch (s) {
    case 'visual_heavy': return 'weight the most prominent, primary controls';
    case 'text_heavy': return 'weight the most clearly labelled, descriptive controls';
    case 'cta_focused': return 'weight the main call to action';
    case 'navigation_focused': return 'weight navigation and page structure';
    default: return 'weight controls evenly';
  }
}

function errorResponse(s: string): string {
  switch (s) {
    case 'retry_heavy': return 'retry the same path once before changing approach';
    case 'reroute_fast': return 'change approach quickly rather than retrying';
    case 'diagnostic': return 'read the page to understand why before retrying';
    default: return 'reassess and choose the next best listed control';
  }
}
