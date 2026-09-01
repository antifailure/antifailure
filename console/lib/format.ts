/** Bytes as an operator reads them. The driver hands back bigints as strings,
 *  so this takes either rather than assuming one. */
export function bytes(value: string | number | null | undefined): string {
  if (value === null || value === undefined) return "--";
  const n = typeof value === "string" ? Number(value) : value;
  if (!Number.isFinite(n)) return "--";
  if (n < 1024) return `${n} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

/** An absolute timestamp, in the reader's own zone. Never "3 days ago" on its
 *  own: a relative time cannot be compared with a log line or a git commit. */
export function when(value: string | Date | null | undefined): string {
  if (!value) return "--";
  const d = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(d.getTime())) return "--";
  return d.toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** How long ago, for the cases where recency is the question being asked. It
 *  goes beside an absolute time, never instead of one. */
export function ago(value: string | Date | null | undefined): string {
  if (!value) return "";
  const d = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  const s = Math.round((Date.now() - d.getTime()) / 1000);
  if (Math.abs(s) < 60) return "just now";
  // Future instants read forwards, and this is not a nicety. Every caller until
  // now passed a timestamp from the past, so the absolute values below were
  // never negative and nobody saw what they produce: a session that expires in
  // a month rendered as "-30d ago", which is a phrase that means nothing and
  // looks like a bug in the clock rather than in the wording. The sessions table
  // is the first screen to show a time that has not happened yet.
  const ahead = s < 0;
  const say = (value: number, unit: string): string =>
    ahead ? `in ${Math.abs(value)}${unit}` : `${value}${unit} ago`;
  const m = Math.round(s / 60);
  if (Math.abs(m) < 60) return say(m, "m");
  const h = Math.round(m / 60);
  if (Math.abs(h) < 48) return say(h, "h");
  return say(Math.round(h / 24), "d");
}

export function usd(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return "--";
  return value.toLocaleString(undefined, { style: "currency", currency: "USD" });
}
