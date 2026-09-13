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

test('a page nobody could read is unverified, and it is not the synth case', () => {
  // These were one cause until now, and the conflation had a cost. The one
  // mapping that produces unverified was spoken for by "nothing on the page
  // confirmed or contradicted the expectation", which has nothing to do with a
  // synthesized response, so the promise that touching one comes back
  // unverified had a mapping, a test, and a producer firing on something else.
  // Different conditions, different remedies: a model key here, a sandbox
  // credential or a fixture there.
  const out = classify([attempt('page-unreadable', 'nothing confirms it either'),
                        attempt('succeeded')]);
  assert.equal(out.verdict, 'unverified');
  assert.equal(out.cause, 'page-unreadable');
  assert.notEqual(out.cause, 'synthesized-response');
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
    'page-unreadable', 'explored',
  ];
  for (const c of causes) {
    assert.ok(['pass', 'fail', 'flaky', 'blocked', 'unverified'].includes(verdictFor(c)), c);
  }
});

test('running out of the declared time is blocked, and it does not count against the application', () => {
  // An unfinished workflow is evidence about neither the change nor the
  // application, which is the schema's own promise: a workflow that exhausts
  // its budget ends as blocked with the reason, never as a partial pass.
  // The mapping on its own, because classify returns blocked for this cause
  // from its blocking set without consulting the map, and a map that said fail
  // would then mislead every other reader of verdictFor.
  assert.equal(verdictFor('budget-exhausted'), 'blocked');
  const outcome = classify([attempt('budget-exhausted', 'Stopped at its time budget of 2s')]);
  assert.equal(outcome.verdict, 'blocked');
  assert.equal(outcome.cause, 'budget-exhausted');
  assert.equal(countsAgainstTheApplication(outcome.verdict), false);
});

test('a retry stopped by its budget after an unreadable page is blocked with the budget, not unverified', () => {
  // The budget is the last attempt here, so the map decides. The orderings
  // below are the ones where the blocking set decides instead.
  const outcome = classify([
    attempt('page-unreadable', 'Nothing on the page confirms it'),
    attempt('budget-exhausted', 'Stopped at its time budget of 2s'),
  ]);
  assert.equal(outcome.verdict, 'blocked');
  assert.equal(outcome.cause, 'budget-exhausted');
});


test('a step budget that ran out before a retry keeps the result blocked, naming the budget', () => {
  // A step budget is per attempt, so running out of steps does not end the
  // retry loop, and budget-exhausted can come first. It belongs in the blocking
  // set like every other blocked cause. Without it, a retry that then proved
  // nothing turned the result unverified, and told the reader to set a model
  // key when the first attempt had simply run out of steps.
  const thenUnreadable = classify([
    attempt('budget-exhausted', 'Stopped at its budget of 5 steps'),
    attempt('page-unreadable', 'Nothing on the page confirms it'),
  ]);
  assert.equal(thenUnreadable.verdict, 'blocked');
  assert.equal(thenUnreadable.cause, 'budget-exhausted');

  // And a retry whose browser then failed must not replace the budget as the
  // reason: the first blocking attempt is the one reported.
  const thenCrashed = classify([
    attempt('budget-exhausted', 'Stopped at its budget of 5 steps'),
    attempt('runner-failure', 'The browser did not start'),
  ]);
  assert.equal(thenCrashed.cause, 'budget-exhausted');
});

// One workflow with retries, ordering by ordering. A time budget spans every
// attempt and ends the retry loop once spent. A step budget is per attempt, so
// running out of steps is followed by another attempt with a fresh budget.
// Each test below is one row of that table, and says why the verdict is what it
// is, because rows 3 and 4 are decisions and not consequences.

test('ordering 1: steps spent on attempt 1, then a pass, is a pass', () => {
  // A retry that reached what it was asked to reach did reach it. Running out
  // of steps is not a failure of the application, so it does not make the
  // result flaky, the same as a browser that crashed before a retry that passed.
  const outcome = classify([attempt('budget-exhausted', 'Stopped at its budget of 5 steps'), attempt('succeeded')]);
  assert.equal(outcome.verdict, 'pass');
  assert.equal(outcome.cause, 'succeeded');
});

test('ordering 2: steps spent on every attempt is blocked, naming the budget', () => {
  const outcome = classify([
    attempt('budget-exhausted', 'Stopped at its budget of 5 steps'),
    attempt('budget-exhausted', 'Stopped at its budget of 5 steps, again'),
  ]);
  assert.equal(outcome.verdict, 'blocked');
  assert.equal(outcome.cause, 'budget-exhausted');
  assert.match(outcome.detail, /budget of 5 steps/);
});

test('ordering 3: steps spent on attempt 1, then a real HTTP error page, is a failure', () => {
  // Decided deliberately: fail. The retry saw the application answer with an
  // error, and that is evidence about the application whatever the attempt
  // before it did. A budget running out is never allowed to outrank evidence.
  const outcome = classify([
    attempt('budget-exhausted', 'Stopped at its budget of 5 steps'),
    attempt('application-error', 'The page at /billing answered HTTP 500'),
  ]);
  assert.equal(outcome.verdict, 'fail');
  assert.equal(outcome.cause, 'application-error');
});

test('ordering 4: a real failure, then steps spent on the last attempt, is the failure', () => {
  // Decided deliberately: fail, reporting the first attempt's failure. The
  // last attempt running out of steps must not hide what the first one saw:
  // blocked would tell a pull request that nothing was learned when the
  // application had already been seen doing the wrong thing.
  const outcome = classify([
    attempt('expectation-not-met', 'The page shows an error rather than what was expected'),
    attempt('budget-exhausted', 'Stopped at its budget of 5 steps'),
  ]);
  assert.equal(outcome.verdict, 'fail');
  assert.equal(outcome.cause, 'expectation-not-met');
  assert.match(outcome.detail, /shows an error/);
});

test('ordering 5: a real failure, then the time budget running out during the retry, is the failure', () => {
  // The same decision as ordering 4, for the budget that ends the loop.
  const outcome = classify([
    attempt('application-error', 'The page at /billing answered HTTP 500'),
    attempt('budget-exhausted', 'Stopped at its time budget of 6s, 6s into the workflow on attempt 2'),
  ]);
  assert.equal(outcome.verdict, 'fail');
  assert.equal(outcome.cause, 'application-error');
});
