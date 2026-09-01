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

/**
 * How long ago, or how long until. It goes beside an absolute time, never
 * instead of one, because a relative time cannot be compared with a log line.
 *
 * The forward direction is not decoration. Every screen in this console used
 * to show only times that had already happened, so a future instant came out
 * of the abs() below as "-1h ago", which is not a phrase. The Load area shows
 * a run's deadline, which is the first time in this product a person is asked
 * to read a moment that has not arrived, and "Gives up at -1h ago" is worse
 * than no answer: it reads as a rendering fault rather than as a time.
 */
export function ago(value: string | Date | null | undefined): string {
  if (!value) return "";
  const d = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  const signed = Math.round((Date.now() - d.getTime()) / 1000);
  const ahead = signed < 0;
  const say = (n: number, unit: string) => (ahead ? `in ${n}${unit}` : `${n}${unit} ago`);
  const s = Math.abs(signed);
  if (s < 60) return ahead ? "in under a minute" : "just now";
  const m = Math.round(s / 60);
  if (m < 60) return say(m, "m");
  const h = Math.round(m / 60);
  if (h < 48) return say(h, "h");
  return say(Math.round(h / 24), "d");
}

export function usd(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return "--";
  return value.toLocaleString(undefined, { style: "currency", currency: "USD" });
}
