/** The access-probe pass: the runner half of the authenticated authorization
 *  differential.
 *
 *  The engine declares, in the manifest, the ownership-scoped objects a persona
 *  can reach: a concrete object at a route, who owns it, and the canary the
 *  application's own seed planted into it. This pass reaches each object as
 *  every persona and once as nobody, decides inside the run whether the object's
 *  canary came back, and emits one structured observation per reach. The engine
 *  reads the observations and a persona that reached another owner's object and
 *  got its canary back becomes a proven authorization break.
 *
 *  The value boundary is the whole point. The canary lives here only long enough
 *  to be matched against a body; what leaves is a flag, never the token and
 *  never the body. An observation carries a route TEMPLATE, an identity
 *  comparison and the flag, so the engine decides access control by content
 *  presence rather than by status code, and no captured byte crosses the
 *  boundary. */

import { DEFAULT_VIEWPORT, Session } from './browser.ts';
import type { Exploration, Observation } from './explore.ts';
import type { InboxSource } from './inbox.ts';
import { type Persona, signIn } from './login.ts';
import { classify } from './verdict.ts';

/** AccessOwnerDoc is the resolved owning identity of an access object. The
 *  engine resolves a persona owner to its identity before sending, so the runner
 *  never has to know a persona's fields to attribute an object. */
export interface AccessOwnerDoc {
  readonly tenant?: string;
  readonly user?: string;
  readonly role?: string;
}

/** AccessObjectDoc is one declared object the pass reaches as each persona. */
export interface AccessObjectDoc {
  readonly route: string;
  readonly id: string;
  readonly objectClass: string;
  readonly canary: string;
  readonly owner: AccessOwnerDoc;
}

/** AccessProbeJob is everything the pass needs. */
export interface AccessProbeJob {
  readonly baseURL: string;
  readonly artifacts: string;
  readonly objects: readonly AccessObjectDoc[];
  readonly personas: readonly Persona[];
  readonly inbox?: InboxSource;
  readonly headless?: boolean;
}

/** ProbeReach is one reach's answer: the status and the body, still inside the
 *  runner. */
export interface ProbeReach {
  readonly status: number;
  readonly body: string;
}

/** ProbeSession is one actor's session: reach reads an object as this actor,
 *  carrying whatever session the actor holds, and close disposes it. */
export interface ProbeSession {
  reach(url: string): Promise<ProbeReach>;
  close(): Promise<void>;
}

/** Prober opens a session as an actor. undefined is the anonymous actor, which
 *  signs in as nobody. It is an interface so the pass is testable without a
 *  browser: a test hands a fake prober whose reaches return a body that does or
 *  does not carry the canary. */
export interface Prober {
  open(persona: Persona | undefined): Promise<ProbeSession>;
}

/** browserProber is the real prober: a fresh browser context per actor, so an
 *  actor's cookies never leak into another's, signed in through the same flow a
 *  workflow uses. The anonymous actor opens a context and never signs in, so it
 *  carries no session, which is exactly the reach the engine's unauthenticated
 *  class reasons about. */
export function browserProber(job: AccessProbeJob): Prober {
  return {
    async open(persona: Persona | undefined): Promise<ProbeSession> {
      const session = await Session.open({
        artifacts: job.artifacts,
        ...(job.headless === undefined ? {} : { headless: job.headless }),
        viewport: { width: DEFAULT_VIEWPORT.width, height: DEFAULT_VIEWPORT.height },
      });
      if (persona) {
        const login = await signIn(session.page(), persona, {
          baseURL: job.baseURL,
          ...(persona.signInPath ? { signInPath: persona.signInPath } : {}),
          ...(job.inbox ? { inbox: job.inbox } : {}),
        });
        if (!login.ok) {
          await session.close(`access-${persona.name}`).catch(() => undefined);
          // The environment's problem, not the change's, and named. A persona
          // whose sign in fails cannot make an authorization reading, so the
          // pass throws rather than record a reach it never authenticated.
          throw new Error(`Sign in for ${persona.name} did not complete: ${login.detail}`);
        }
      }
      return {
        reach: (url: string) => session.reach(url),
        close: () => session.close(`access-${persona?.name ?? 'anon'}`).then(() => undefined).catch(() => undefined),
      };
    },
  };
}

/** actor is one identity the pass reaches objects as: a declared persona, or the
 *  anonymous actor. */
interface Actor {
  readonly persona?: Persona;
}

/** accessProbe reaches every declared object as every persona and once as
 *  nobody, and returns one exploration carrying the observations.
 *
 *  A reach that could not sign an actor in is not a reason to fail the change:
 *  that actor's reaches are skipped and noted in the steps, and the other actors
 *  still run, exactly as an exploration blocked at the sign in is the
 *  environment's problem and not the application's. The pass itself only reports
 *  blocked when it could reach nothing at all, so the engine tells "the runner
 *  emitted nothing" from "the runner reached and found no boundary crossed". */
export async function accessProbe(
  job: AccessProbeJob,
  prober: Prober = browserProber(job),
): Promise<Exploration> {
  const started = Date.now();
  const observations: Observation[] = [];
  const steps: string[] = [];
  let reached = 0;

  const actors: Actor[] = [
    {},
    ...job.personas.map((p) => ({ persona: p })),
  ];

  for (const actor of actors) {
    const who = actor.persona ? actor.persona.name : 'nobody';
    let session: ProbeSession | undefined;
    try {
      session = await prober.open(actor.persona);
    } catch (err) {
      steps.push(`Skipped ${who}: ${err instanceof Error ? err.message : String(err)}`);
      continue;
    }
    try {
      for (const obj of job.objects) {
        const url = objectUrl(job.baseURL, obj.route, obj.id);
        const answer = await session.reach(url);
        reached++;
        observations.push(observe(actor, obj, answer));
      }
    } finally {
      await session.close();
    }
    steps.push(`Reached ${job.objects.length} object(s) as ${who}.`);
  }

  // Reached nothing at all is the one blocked case: every actor failed to open
  // or sign in, so there is no reading to hand the engine. A pass that made even
  // one reach is a real reading, whatever it found.
  const outcome = reached === 0
    ? classify([{
      cause: 'environment-incomplete',
      detail: 'No actor could reach any declared object, so no authorization reading was made.',
      durationMs: Date.now() - started,
    }])
    : classify([{
      cause: 'explored',
      detail: `Reached ${job.objects.length} object(s) as ${actors.length} actor(s).`,
      durationMs: Date.now() - started,
    }]);

  return {
    name: 'access-probe',
    goal: 'Reach each declared object as every persona and as nobody.',
    seed: '',
    persona: '',
    startPath: '/',
    viewport: { name: '', ...DEFAULT_VIEWPORT, mobile: false },
    outcome: { ...outcome, reproduction: [] },
    reached: reached > 0,
    steps,
    journey: [],
    findings: [],
    visited: [],
    missing: [],
    evidence: { console: [], failed: [], dom: [], responses: [] },
    observations,
    requests: [],
    durationMs: Date.now() - started,
  };
}

/** observe turns one reach into one observation. The canary is matched against
 *  the body HERE and only the flag is emitted: the token and the body never
 *  leave this function. The route TEMPLATE, not the concrete url, is what the
 *  observation carries, so no id ever rides in a location. setupConfirmed is
 *  true because a declared fixture is a seeded object, which is what makes a
 *  refusal prove a boundary held rather than that the id was invented. */
function observe(actor: Actor, obj: AccessObjectDoc, answer: ProbeReach): Observation {
  const present = obj.canary !== '' && answer.body.includes(obj.canary);
  const o: Observation = {
    route: obj.route,
    method: 'GET',
    objectClass: obj.objectClass,
    status: answer.status,
    setupConfirmed: true,
    ...(actor.persona ? {} : { anonymous: true }),
    ...(actor.persona?.tenant ? { actorTenant: actor.persona.tenant } : {}),
    ...(actor.persona?.name ? { actorUser: actor.persona.name } : {}),
    ...(actor.persona?.role ? { actorRole: actor.persona.role } : {}),
    ...(obj.owner.tenant ? { ownerTenant: obj.owner.tenant } : {}),
    ...(obj.owner.user ? { ownerUser: obj.owner.user } : {}),
    ...(obj.owner.role ? { ownerRole: obj.owner.role } : {}),
    ...(present ? { victimContentPresent: true } : {}),
  };
  return o;
}

/** objectUrl substitutes the concrete id into the route template's dynamic
 *  segment and joins it to the base. A route with a {id}, :id or [id] segment
 *  has that segment replaced; a route with none has the id appended as a path
 *  segment, so a collection route still reaches one object. */
export function objectUrl(baseURL: string, route: string, id: string): string {
  const encoded = encodeURIComponent(id);
  const segments = route.split('/');
  let replaced = false;
  const out = segments.map((s) => {
    if (replaced) return s;
    if (isDynamicSegment(s)) {
      replaced = true;
      return encoded;
    }
    return s;
  });
  let path = out.join('/');
  if (!replaced) {
    path = route.replace(/\/+$/, '') + '/' + encoded;
  }
  return join(baseURL, path);
}

/** isDynamicSegment reports whether a path segment is a route parameter in any
 *  of the common spellings: {id}, :id, [id]. */
function isDynamicSegment(s: string): boolean {
  return (
    (s.startsWith('{') && s.endsWith('}')) ||
    s.startsWith(':') ||
    (s.startsWith('[') && s.endsWith(']'))
  );
}

/** join is url composition that leaves an absolute path alone. The same shape
 *  the other passes use, kept local so the pass does not reach into one. */
function join(base: string, path: string): string {
  if (/^https?:\/\//.test(path)) return path;
  return base.replace(/\/+$/, '') + '/' + path.replace(/^\/+/, '');
}
