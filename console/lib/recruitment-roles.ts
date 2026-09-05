/**
 * The two roles an application can be for, and what each is called on screen.
 *
 * ITS OWN MODULE, AND IT IMPORTS NOTHING. admin-recruitment.ts next door
 * imports React and the `@/` path alias, so the console's test runner cannot
 * execute it: `npm test` in console/ is `node --test lib/*.test.ts` with no
 * loader and no alias resolution. lib/admin-csrf.ts was carved out of that same
 * file for the same reason, and the header there says what it cost: the one
 * piece of the operator client with a rule in it sat in the one file nothing
 * could run, and every operator mutation was refused for as long as that was
 * true. A closed vocabulary is a rule. It lives where it can be tested.
 *
 * THE LIST IS THE DATABASE'S LIST. Migration 0038 constrains
 * `recruitment_applications.role` with a CHECK naming exactly these two values,
 * and admin-recruitment.test.ts reads that migration and asserts the two agree
 * in both directions. So a third role added to the schema fails this console's
 * suite rather than rendering as nothing on the operator's screen, which is the
 * defect loadshapes.test.ts was written after: the console's copy of
 * `verdict_value` was missing `flaky`, and a run that had found something drew
 * as "No verdict".
 */

export const APPLICATION_ROLES = {
  founding_engineer: "Founding engineer",
  founding_growth: "Founding growth",
} as const;

export type ApplicationRole = keyof typeof APPLICATION_ROLES;

/**
 * A role as a person reads it.
 *
 * An unrecognized value renders the raw string rather than a blank or a bare
 * "Unknown". An operator looking at an application needs to see WHICH
 * unexpected value arrived, because the answer decides whether the fix is in
 * this console or in the database, and a page that swallows it turns a one line
 * diagnosis into an investigation. An absence shown where there is a value is
 * the worst direction for this to be wrong in: nobody investigates a blank.
 */
export function roleLabel(role: string): string {
  return APPLICATION_ROLES[role as ApplicationRole] ?? `Unrecognized role: ${role}`;
}
