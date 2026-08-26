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

## Bring your own key

With `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` set, a model reads the page and
decides what a person would do next. Without one, the deterministic planner
runs and the workflows that depend on reading a page come back `unverified`
rather than guessed at.

The key is yours. Nothing here ships one, the engine never stores one, and the
key is read from the runner's own environment rather than sent in the job
document, so it never passes through a file the engine wrote or a document
anybody logged. `AF_MODEL` pins a model; `ANTHROPIC_BASE_URL` and
`OPENAI_BASE_URL` point at a gateway.

Two properties matter more than the prompt. The model chooses from a fixed set
of actions against names that are actually on the page, so it cannot invent a
button: anything it names that is not there is refused rather than
approximated, because a click on the wrong control produces a result that looks
like an application failure and is not one. And it never sees the page's HTML,
only the accessibility snapshot, which is what a person navigating with a
screen reader gets and keeps whatever is in the DOM out of somebody else's
logs.

A model that cannot be reached mid run falls back to the deterministic planner
rather than ending the workflow, because a model being down is not evidence
about the application.
