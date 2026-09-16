import { test } from 'node:test';
import assert from 'node:assert/strict';
import { accessProbe, objectUrl, type Prober } from '../src/access.ts';
import type { Persona } from '../src/login.ts';

const CANARY = 'ALICE-ORDER-CANARY-4f2a';

/** A prober with no browser: it answers each reach from a table keyed on who is
 *  asking, so a test can plant a body that does or does not carry the canary and
 *  assert what the pass emits. It records the actors it was opened as, so a test
 *  can prove the anonymous arm and every persona were reached. */
function fakeProber(
  answer: (actor: string, url: string) => { status: number; body: string },
  opened: string[],
  failFor: Set<string> = new Set(),
): Prober {
  return {
    async open(persona: Persona | undefined) {
      const who = persona?.name ?? 'anon';
      opened.push(who);
      if (failFor.has(who)) {
        throw new Error(`sign in for ${who} refused`);
      }
      return {
        reach: async (url: string) => answer(who, url),
        close: async () => {},
      };
    },
  };
}

const alice: Persona = { name: 'alice', email: 'alice@example.test', role: 'member', tenant: 'org_a', login: 'password' };
const bob: Persona = { name: 'bob', email: 'bob@example.test', role: 'member', tenant: 'org_a', login: 'password' };

function ordersJob(personas: readonly Persona[]) {
  return {
    baseURL: 'http://twin.local',
    artifacts: '/tmp/none',
    personas,
    objects: [{
      route: '/api/orders/{id}',
      id: '1001',
      objectClass: "another customer's order",
      canary: CANARY,
      owner: { tenant: 'org_a', user: 'alice', role: 'member' },
    }],
  };
}

// The headline: bob, in alice's tenant, reads alice's order and the order's
// canary comes back. The pass must emit an idor-shaped observation, and it must
// emit the anonymous arm and alice's own self-access arm too, so the engine can
// tell a dead control from a held boundary.
test('access probe emits one observation per actor with the right shape', async () => {
  const opened: string[] = [];
  const prober = fakeProber((who) => {
    if (who === 'anon') return { status: 403, body: 'forbidden' };
    // alice (owner) and bob (attacker) both get the order body with the canary.
    return { status: 200, body: `{"order":"1001","secret":"${CANARY}"}` };
  }, opened);

  const result = await accessProbe(ordersJob([alice, bob]), prober);

  assert.deepEqual(opened, ['anon', 'alice', 'bob'], 'every persona and the anonymous actor are reached');
  const obs = result.observations ?? [];
  assert.equal(obs.length, 3, 'one observation per actor');

  const anon = obs.find((o) => o.anonymous);
  assert.ok(anon, 'the anonymous arm is emitted');
  assert.equal(anon!.status, 403);
  assert.ok(!anon!.victimContentPresent, 'a denied anonymous reach saw no victim content');
  assert.ok(!anon!.actorUser, 'the anonymous actor carries no identity');

  const self = obs.find((o) => o.actorUser === 'alice');
  assert.ok(self, "the owner's self-access arm is emitted");
  assert.equal(self!.ownerUser, 'alice', 'actor and owner match on the self-access arm');
  assert.equal(self!.victimContentPresent, true, 'the owner can read its own object, proving setup');
  assert.equal(self!.setupConfirmed, true);

  const idor = obs.find((o) => o.actorUser === 'bob');
  assert.ok(idor, "bob's cross-owner reach is emitted");
  assert.equal(idor!.ownerUser, 'alice', 'the object is owned by alice');
  assert.equal(idor!.actorTenant, 'org_a');
  assert.equal(idor!.ownerTenant, 'org_a');
  assert.equal(idor!.victimContentPresent, true, 'bob got the victim canary back: an idor');
  assert.equal(idor!.route, '/api/orders/{id}', 'the route is the template, never the concrete url');
  assert.equal(idor!.method, 'GET');
});

// The value boundary: the canary is matched inside the run and only a flag is
// emitted. No captured byte and no canary value may cross into an observation.
test('access probe never lets the canary value escape into an observation', async () => {
  const prober = fakeProber(() => ({ status: 200, body: `leaked ${CANARY} here` }), []);
  const result = await accessProbe(ordersJob([alice]), prober);
  const serialized = JSON.stringify(result.observations);
  assert.ok(!serialized.includes(CANARY), 'the canary value must never appear in an observation');
  assert.ok(serialized.includes('victimContentPresent'), 'only the presence flag leaves the run');
});

// A reach that returned no canary is not a violation: victimContentPresent is
// absent, so the engine reads a boundary that held rather than a leak.
test('access probe marks a reach that saw no canary as not present', async () => {
  const prober = fakeProber((who) => (
    who === 'bob' ? { status: 404, body: 'not found' } : { status: 200, body: CANARY }
  ), []);
  const result = await accessProbe(ordersJob([alice, bob]), prober);
  const bobObs = (result.observations ?? []).find((o) => o.actorUser === 'bob');
  assert.ok(bobObs, "bob's reach is recorded");
  assert.ok(!bobObs!.victimContentPresent, 'a denied reach with no canary is not a leak');
  assert.equal(bobObs!.status, 404);
});

// An actor that could not sign in is skipped, not fatal: the others still make
// their readings, exactly as an exploration blocked at the sign in is the
// environment's problem and not the application's.
test('access probe skips an actor whose sign in fails and keeps the rest', async () => {
  const prober = fakeProber(() => ({ status: 200, body: CANARY }), [], new Set(['bob']));
  const result = await accessProbe(ordersJob([alice, bob]), prober);
  const actors = (result.observations ?? []).map((o) => o.actorUser ?? 'anon');
  assert.ok(actors.includes('alice'), 'alice still reached her object');
  assert.ok(!actors.includes('bob'), 'bob was skipped after a failed sign in');
  assert.equal(result.outcome.verdict, 'pass', 'reaching something at all is a real reading, not a block');
});

// objectUrl substitutes the concrete id into the route's dynamic segment, in
// each of the spellings, and appends it when the route has none.
test('objectUrl substitutes the id into the route template', () => {
  assert.equal(objectUrl('http://x', '/api/orders/{id}', '1001'), 'http://x/api/orders/1001');
  assert.equal(objectUrl('http://x', '/api/orders/:id', '1001'), 'http://x/api/orders/1001');
  assert.equal(objectUrl('http://x', '/api/orders/[id]', '1001'), 'http://x/api/orders/1001');
  assert.equal(objectUrl('http://x', '/api/orders', '1001'), 'http://x/api/orders/1001');
  assert.equal(objectUrl('http://x/', '/api/orders/{id}/items', '7'), 'http://x/api/orders/7/items');
});
