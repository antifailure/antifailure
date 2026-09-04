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
 * How far away in time, in the direction it actually lies. It goes beside an
 * absolute time, never instead of one.
 *
 * It says "in 30d" as well as "30d ago", and that is a fix rather than a
 * flourish. This was written when every caller passed something that had
 * already happened: a creation, an occurrence, a rotation. The Math.abs guards
 * chose the right unit for a negative interval and then printed the number
 * still signed, with "ago" after it, so the first caller to pass a future date
 * rendered "-30d ago". That caller is the renewal date on the Plan page, which
 * is the line telling somebody paying for this when they will next be charged.
 */
export function ago(value: string | Date | null | undefined): string {
  if (!value) return "";
  const d = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  const s = Math.round((Date.now() - d.getTime()) / 1000);
  if (Math.abs(s) < 60) return "just now";
  // Decided once, from the sign, before any rounding can lose it.
  const past = s > 0;
  const say = (n: number, unit: string) => (past ? `${n}${unit} ago` : `in ${n}${unit}`);
  const m = Math.abs(Math.round(s / 60));
  if (m < 60) return say(m, "m");
  const h = Math.round(m / 60);
  if (h < 48) return say(h, "h");
  return say(Math.round(h / 24), "d");
}

export function usd(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return "--";
  return value.toLocaleString(undefined, { style: "currency", currency: "USD" });
}

/**
 * How long until an instant, in words.
 *
 * Beside `ago` rather than in the operator lane that needed it, because it is
 * the same job pointing forwards and a second module would be a second answer
 * to "under a minute". It takes the clock as an argument, which `ago` does not:
 * this one is tested, and a formatter that reads the wall clock is a formatter
 * whose test passes at whatever time it ran.
 *
 * The one case worth naming is zero. Flooring to minutes renders forty seconds
 * as "0 minutes", which reads as expired and is not, so the answer closest to
 * zero is the one that has to be words.
 */
export function until(instant: string | Date | null | undefined, now: Date): string {
  if (instant === null || instant === undefined) return "unknown";
  const ms = (instant instanceof Date ? instant : new Date(instant)).getTime() - now.getTime();
  if (!Number.isFinite(ms)) return "unknown";
  if (ms <= 0) return "expired";
  const minutes = Math.floor(ms / 60_000);
  if (minutes < 1) return "under a minute";
  if (minutes === 1) return "1 minute";
  if (minutes < 60) return `${minutes} minutes`;
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  if (rest === 0) return hours === 1 ? "1 hour" : `${hours} hours`;
  return `${hours}h ${rest}m`;
}
