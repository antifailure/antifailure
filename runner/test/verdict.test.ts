import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  classify, verdictFor, countsAgainstTheApplication, exitCodeFor,
  type Attempt, type Cause,
} from '../src/verdict.ts';

function attempt(cause: Cause, detail = 'x'): Attempt {
  return { cause, detail, durationMs: 10 };
}

test('a runner failure never counts against the application', () => {
  // The distinction the whole model exists for. The moment a browser crash
  // reads as a failing test, people stop believing the failures that are real.
  assert.equal(verdictFor('runner-failure'), 'blocked');
  assert.equal(verdictFor('environment-incomplete'), 'blocked');
  assert.equal(countsAgainstTheApplication('blocked'), false);
  assert.equal(countsAgainstTheApplication('fail'), true);
  assert.equal(countsAgainstTheApplication('flaky'), false);
  assert.equal(countsAgainstTheApplication('unverified'), false);
  assert.equal(countsAgainstTheApplication('pass'), false);
});

test('an application error and an unmet expectation both fail', () => {
  assert.equal(verdictFor('expectation-not-met'), 'fail');
  assert.equal(verdictFor('application-error'), 'fail');
});

test('a workflow that never ran is blocked, not failed', () => {
  const out = classify([]);
  assert.equal(out.verdict, 'blocked');
  assert.equal(out.cause, 'runner-failure');
});

test('one success is a pass', () => {
  const out = classify([attempt('succeeded', 'signed in')]);
  assert.equal(out.verdict, 'pass');
  assert.equal(out.detail, 'signed in');
});

test('failing then passing is flaky, not passing', () => {
  // Reporting a pass hides it until it happens in production; reporting a
  // fail wastes somebody's afternoon on a pull request that is fine.
  const out = classify([attempt('expectation-not-met'), attempt('succeeded')]);
  assert.equal(out.verdict, 'flaky');
  assert.match(out.detail, /1 of 2 attempts/);
});

test('passing then failing is also flaky', () => {
  const out = classify([attempt('succeeded'), attempt('application-error')]);
  assert.equal(out.verdict, 'flaky');
});

test('failing every time is a failure', () => {
  const out = classify([
    attempt('expectation-not-met', 'no paid plan'),
    attempt('expectation-not-met', 'no paid plan'),
  ]);
  assert.equal(out.verdict, 'fail');
  assert.equal(out.detail, 'no paid plan');
});

test('a blocked attempt with no failure stays blocked', () => {
  const out = classify([
    attempt('environment-incomplete', 'no fixture for POST /v1/x'),
    attempt('runner-failure', 'the browser closed'),
  ]);
  assert.equal(out.verdict, 'blocked');
  assert.match(out.detail, /no fixture/, 'the first blocking reason is the useful one');
});

test('a real failure outweighs a later runner problem', () => {
  // Otherwise a browser crash on the retry would hide a genuine failure.
  const out = classify([
    attempt('expectation-not-met', 'the account still shows the free plan'),
    attempt('runner-failure', 'the browser closed'),
  ]);
  assert.equal(out.verdict, 'fail');
});

test('touching a synthesized response is unverified even when it passed', () => {
  // A model invented the answer, so the workflow proved nothing either way.
  const out = classify([attempt('synthesized-response', 'api.example.com was synthesized'),
                        attempt('succeeded')]);
  assert.equal(out.verdict, 'unverified');
  assert.match(out.detail, /synthesized/);
});

test('only a real failure fails the build', () => {
  const pass = classify([attempt('succeeded')]);
  const blocked = classify([attempt('runner-failure')]);
  const flaky = classify([attempt('expectation-not-met'), attempt('succeeded')]);
  const failed = classify([attempt('expectation-not-met')]);

  assert.equal(exitCodeFor([pass, blocked, flaky]), 0,
    'an incomplete environment must not be indistinguishable from a broken application');
  assert.equal(exitCodeFor([pass, failed]), 8);
});

test('every cause maps to a verdict', () => {
  // A cause with no mapping would be a runtime undefined that reads as a pass.
  const causes: Cause[] = [
    'succeeded', 'expectation-not-met', 'application-error',
    'runner-failure', 'environment-incomplete', 'synthesized-response',
  ];
  for (const c of causes) {
    assert.ok(['pass', 'fail', 'flaky', 'blocked', 'unverified'].includes(verdictFor(c)), c);
  }
});
