import { test } from 'node:test';
import assert from 'node:assert/strict';
import { totpCode, decodeSecret, secondsIntoWindow, DIGITS, PERIOD } from '../src/totp.ts';

/** The RFC 6238 Appendix B seed, "12345678901234567890", base32 encoded. */
const SECRET = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ';

// Checked against the RFC rather than against this implementation's output.
// A test that asserts what the code already does proves the code has not
// changed, not that it is right. The engine's Go implementation is checked
// against these same rows, which is what makes the two agree.
test('the codes match the RFC 6238 vectors', () => {
  const cases: ReadonlyArray<readonly [number, string]> = [
    [59, '94287082'],
    [1111111109, '07081804'],
    [1111111111, '14050471'],
    [1234567890, '89005924'],
    [2000000000, '69279037'],
    [20000000000, '65353130'],
  ];
  for (const [unix, eight] of cases) {
    assert.equal(totpCode(SECRET, unix * 1000), eight.slice(-DIGITS), `T=${unix}`);
  }
});

test('a secret is accepted in the forms it is written in', () => {
  const at = 1234567890 * 1000;
  const want = totpCode(SECRET, at);
  for (const form of [
    'GEZD GNBV GY3T QOJQ GEZD GNBV GY3T QOJQ',
    `${SECRET}======`,
    SECRET.toLowerCase(),
  ]) {
    assert.equal(totpCode(form, at), want, form);
  }
});

test('a secret that is not base32 throws rather than producing a wrong code', () => {
  // A silently wrong key produces codes that are wrong forever and look
  // exactly like an application refusing a correct one.
  assert.throws(() => totpCode('not base32 at all!', 0));
  assert.throws(() => totpCode('', 0));
});

test('decodeSecret produces the RFC seed', () => {
  assert.equal(decodeSecret(SECRET).toString('utf8'), '12345678901234567890');
});

test('secondsIntoWindow reports the position in the window', () => {
  assert.equal(secondsIntoWindow(0), 0);
  assert.equal(secondsIntoWindow(29_000), 29);
  assert.equal(secondsIntoWindow(30_000), 0);
  assert.equal(secondsIntoWindow(31_000), 1);
  assert.ok(secondsIntoWindow() < PERIOD);
});
