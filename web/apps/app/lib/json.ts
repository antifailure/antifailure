// Reading a jsonb value.
//
// Its own file, with no framework import, so it can be tested by running it
// rather than by rendering a page. lib/api.ts imports next/headers, which only
// resolves inside a request, and a pure function that cannot be called from a
// test is a pure function nobody tests.

/**
 * A jsonb value, whatever encoding it arrives in.
 *
 * A jsonb column holds structure and there is more than one way to end up with
 * a JSON *string* in one instead. The one that happened here: postgres.js
 * applies its own JSON serializer when it sees a `::jsonb` cast in the query,
 * so a caller that had already stringified its value stored `["a"]` as the
 * jsonb string `"[\"a\"]"`. `jsonb_typeof` said `string`, every read came back
 * as text, and this page rendered a paragraph of escaped quotes where it should
 * have rendered five numbered steps.
 *
 * That writer is fixed. This stays, because the boundary is the place to be
 * tolerant: the next writer is an engine on somebody else's machine sending
 * events over HTTP, and one badly encoded field must degrade one field rather
 * than a page. Strict on the way in, tolerant on the way out.
 *
 * One level of unwrapping and no more. A string that parses to another string
 * is left alone: at that point the value genuinely is text, and unwrapping
 * forever would turn the literal `"hello"` into something else.
 */
export function asJson(value: unknown): unknown {
  if (typeof value !== "string") return value;
  const trimmed = value.trim();
  // Only what could be structure. A bare word is a string and stays one.
  if (!trimmed.startsWith("[") && !trimmed.startsWith("{")) return value;
  try {
    const parsed: unknown = JSON.parse(trimmed);
    return typeof parsed === "object" && parsed !== null ? parsed : value;
  } catch {
    // Text that happens to begin with a bracket. Returned as it came, because
    // showing it is more useful than hiding it.
    return value;
  }
}
