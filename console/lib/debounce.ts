/**
 * Call the function once the caller has stopped calling it for `ms`.
 *
 * The audit filter fired one request per keystroke, so typing "billing" sent
 * seven requests and the answer to the first six was thrown away. A filter that
 * reaches the server on every keystroke is also a filter that flickers through
 * "no entries" states for prefixes nobody asked about.
 *
 * The last call's arguments win, because a filter is a value and not a queue:
 * what the reader typed last is what they want to see.
 */
export function debounce<A extends unknown[]>(
  fn: (...args: A) => void,
  ms: number,
): { (...args: A): void; cancel: () => void } {
  let timer: ReturnType<typeof setTimeout> | null = null;
  const run = (...args: A) => {
    if (timer !== null) clearTimeout(timer);
    timer = setTimeout(() => {
      timer = null;
      fn(...args);
    }, ms);
  };
  run.cancel = () => {
    if (timer !== null) clearTimeout(timer);
    timer = null;
  };
  return run;
}
