// Refuses a test run that would examine nothing.
//
// `node --test` given a pattern that matches no file runs zero tests and exits
// zero, so a renamed directory would turn this package's tests into a green
// check that looked at nothing. This used to be `ls test/*.test.ts > /dev/null`
// in front of the run, which is a Unix shell's answer: under cmd.exe, which is
// what npm runs scripts with on Windows, `ls` does not exist and `/dev/null` is
// a path, so the guard failed every run there and the tests behind it never
// started. Node expands the pattern itself here, on every platform, the same
// way `node --test` does.
import { globSync } from 'node:fs';

const patterns = process.argv.slice(2);
if (patterns.length === 0) {
  console.error('requiretests: no pattern given, so there is nothing to require');
  process.exit(2);
}
for (const pattern of patterns) {
  // A shell that expands the pattern hands over files, which match themselves.
  if (globSync(pattern).length === 0) {
    console.error(`requiretests: ${pattern} matches no test file, so the run would examine nothing`);
    process.exit(1);
  }
}
