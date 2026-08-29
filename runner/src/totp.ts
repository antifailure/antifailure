// Time based one time passwords, RFC 6238 over RFC 4226.
//
// The other half of engine/internal/personas/totp.go. The engine enrols the
// secret and this produces the code from it, so the two have to agree about
// digits, period and algorithm. They are written from the same RFC and both
// are checked against the same Appendix B vectors, which is what makes
// "they agree" a fact rather than a hope: a TOTP integration usually fails at
// exactly this seam while every unit test on either side passes.
//
// SHA-1 is correct here rather than an oversight. RFC 6238 specifies
// HMAC-SHA1 as the default and every authenticator app implements it. The
// collision attacks that make SHA-1 unsuitable for signatures do not apply to
// HMAC-SHA1.

import { createHmac } from 'node:crypto';

/** The window length every common implementation uses, in seconds. */
export const PERIOD = 30;

/** The code length every common implementation uses. */
export const DIGITS = 6;

/** decodeSecret accepts the secret in the forms it is written in.
 *
 * Authenticator apps show it in spaced groups and some systems store it
 * padded, so both are accepted. Anything that is not base32 throws, because a
 * silently wrong key produces codes that are wrong forever and look exactly
 * like an application refusing a correct one.
 */
export function decodeSecret(secret: string): Buffer {
  const cleaned = secret.replace(/[\s-]/g, '').toUpperCase().replace(/=+$/, '');
  if (cleaned === '') throw new Error('The TOTP secret is empty.');

  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = 0;
  let value = 0;
  const out: number[] = [];
  for (const char of cleaned) {
    const index = alphabet.indexOf(char);
    if (index === -1) throw new Error(`The TOTP secret is not base32: ${char} is not a base32 character.`);
    value = (value << 5) | index;
    bits += 5;
    if (bits >= 8) {
      bits -= 8;
      out.push((value >>> bits) & 0xff);
    }
  }
  return Buffer.from(out);
}

/** hotp is RFC 4226: HMAC the counter, take the dynamic truncation, reduce. */
function hotp(key: Buffer, counter: number): string {
  const buf = Buffer.alloc(8);
  // Written as two 32 bit halves because a JavaScript number cannot hold a
  // 64 bit integer exactly, and writeBigUInt64BE would need a BigInt for a
  // value that is always small.
  buf.writeUInt32BE(Math.floor(counter / 2 ** 32), 0);
  buf.writeUInt32BE(counter >>> 0, 4);

  const sum = createHmac('sha1', key).update(buf).digest();

  // The low four bits of the last byte choose where to read from, which is
  // what makes the truncation dynamic rather than a fixed prefix.
  const offset = sum[sum.length - 1]! & 0x0f;
  const value =
    ((sum[offset]! & 0x7f) << 24) |
    (sum[offset + 1]! << 16) |
    (sum[offset + 2]! << 8) |
    sum[offset + 3]!;

  return String(value % 10 ** DIGITS).padStart(DIGITS, '0');
}

/** totpCode returns the code for a secret at a moment, in milliseconds. */
export function totpCode(secret: string, atMs: number = Date.now()): string {
  const counter = Math.floor(atMs / 1000 / PERIOD);
  return hotp(decodeSecret(secret), counter);
}

/** secondsIntoWindow says how far through the current window a moment is.
 *
 * Used to decide whether to wait. A code produced with two seconds left is one
 * the application may well reject by the time it is typed, and waiting three
 * seconds is much cheaper than a failed sign in that reads as a real bug.
 */
export function secondsIntoWindow(atMs: number = Date.now()): number {
  return Math.floor(atMs / 1000) % PERIOD;
}
