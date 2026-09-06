import { test } from 'node:test';
import assert from 'node:assert/strict';
import { sessionsFor } from '../src/execute.ts';
import type { Persona } from '../src/login.ts';

// Which sessions a workflow holds, and in what order.
//
// One browser, two cookies, is the state the control plane's own launch night
// bug needed and the state no workflow could reach: a workflow signed in as
// one persona, so the operator transport check that fired on customer
// mutations whenever the operator cookie was PRESENT was never exercised with
// both cookies present. `personas` names every session; these say what the
// runner makes of the list.

const owner: Persona = { name: 'owner', email: 'owner@example.test', login: 'magic_link' };
const operator: Persona = {
  name: 'operator', email: 'operator@example.test', login: 'password', password: 'x',
};
const declared = [owner, operator];

test('the list form signs in every persona named, in the order named', () => {
  const got = sessionsFor({ personas: ['operator', 'owner'] }, declared);
  assert.equal(got.missing, undefined);
  assert.deepEqual(got.personas.map((p) => p.name), ['operator', 'owner']);
});

test('the list form wins over a single persona when both are present', () => {
  const got = sessionsFor({ persona: 'owner', personas: ['operator', 'owner'] }, declared);
  assert.deepEqual(got.personas.map((p) => p.name), ['operator', 'owner']);
});

test('a name in the list that matches no persona is reported, not skipped', () => {
  // Skipping it would run the workflow with one session and report against
  // the application whatever that state produced.
  const got = sessionsFor({ personas: ['operator', 'onwer'] }, declared);
  assert.equal(got.missing, 'onwer');
  assert.deepEqual(got.personas, []);
});

test('the single form keeps its fallback to the first declared persona', () => {
  assert.deepEqual(sessionsFor({}, declared).personas.map((p) => p.name), ['owner']);
  assert.deepEqual(sessionsFor({ persona: 'operator' }, declared).personas.map((p) => p.name), ['operator']);
  // An unknown single name falls back to the first persona, as it always has;
  // the manifest validator is what refuses the misspelling. Only a manifest
  // with no personas at all leaves the name unresolved.
  assert.deepEqual(sessionsFor({ persona: 'nobody' }, declared).personas.map((p) => p.name), ['owner']);
  assert.equal(sessionsFor({ persona: 'nobody' }, []).missing, 'nobody');
  assert.deepEqual(sessionsFor({}, []).personas, []);
});

test('an empty list means the single form, not no sign in at all', () => {
  assert.deepEqual(sessionsFor({ persona: 'operator', personas: [] }, declared).personas.map((p) => p.name), ['operator']);
});
