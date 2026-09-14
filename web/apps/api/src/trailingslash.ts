// A configured address with its trailing slashes removed.
//
// ONE FUNCTION FOR EVERY PLACE THE API TRIMS AN ADDRESS, and the reason is how
// the obvious way to write it behaves rather than taste. The obvious way is a
// regular expression anchored at the end of the string, and on a value that
// does NOT end in a slash the engine retries that match from every slash in the
// value, so its cost grows with the square of the length: measured in node
// 24.2.0 at 41 ms for ten thousand slashes and 4.3 s for a hundred thousand,
// against well under a millisecond for this loop on the same input. Every value
// trimmed here is an operator's configuration rather than a request, so nothing
// outside the operator could reach that cost, and CodeQL still reported four
// of the eight copies in the api as high severity findings. One loop that says
// what it does is cheaper than explaining those, and it is what the next copy
// will be taken from.
//
// It lived as three lines inside hostedMcpEndpoint first, which had already
// been rewritten this way for the same reason; this is those lines with a name.

/** `value` without any slashes at its end. Slashes anywhere else are kept. */
export function trimTrailingSlashes(value: string): string {
  let end = value.length
  while (end > 0 && value.charCodeAt(end - 1) === 47) end -= 1
  return value.slice(0, end)
}
