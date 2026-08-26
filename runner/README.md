# Runner

Drives an application the way a person does, and returns a verdict with
evidence.

It reads a job document on standard input and writes results on standard
output, as JSON. A subprocess with a document boundary rather than a library,
because the engine is Go and the browser automation that actually works is
TypeScript; the alternatives are a worse browser driver or a foreign function
interface, and this is neither.

```
echo '{"base_url":"http://127.0.0.1:46000","artifacts":"/tmp/af","workflows":[...],"personas":[...]}' \
  | node --experimental-strip-types src/main.ts
```

## No build step

Node runs the TypeScript directly by stripping the types. That rules out
parameter properties, enums, and namespaces, which cost a few written out
constructors, and it removes an entire toolchain between the source somebody
reads and the code that runs.

## What it decides, and what it refuses to decide

Five verdicts, and the distinction that matters is between `fail` and
`blocked`. A browser that crashed, a page that never loaded, a persona with no
password: none of those is evidence about the application, and charging them to
it is how people learn to ignore the results.

Without a model key the expectations are checked by matching the words that
carry meaning. That is honest about its limits: a page saying the opposite is
`fail`, a page the checker cannot read is `unverified`, and it never guesses a
pass. A result that rests on a guess is not a result.
