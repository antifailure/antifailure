export function cn(...classes: Array<unknown>) {
  return classes.filter((c): c is string => typeof c === "string" && c.length > 0).join(" ");
}
