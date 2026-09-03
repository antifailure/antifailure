/**
 * The brand mark, byte for byte the one the marketing site and the favicon
 * use. Copied rather than imported because the two applications are separate
 * builds with separate lockfiles, and a shared package for one SVG would be a
 * workspace to maintain forever. If it changes, it changes in both.
 */
export function LogoMark({ className = "h-5 w-5" }: { className?: string }) {
  return (
    <svg viewBox="0 0 18 18" className={className} fill="none" aria-hidden>
      <path
        d="M1.8 6.4V1.8H6.4M11.6 1.8H16.2V6.4M16.2 11.6V16.2H11.6M6.4 16.2H1.8V11.6"
        stroke="#33bf00"
        strokeWidth="2.1"
        strokeLinecap="square"
      />
    </svg>
  );
}

export function GitHubMark({ className = "h-[18px] w-[18px]" }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 16 16" fill="currentColor" aria-hidden>
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z" />
    </svg>
  );
}

/** One stroke width, one grid, one size, for every icon in the navigation. */
function stroke(d: string) {
  return function Icon({ className = "h-4 w-4" }: { className?: string }) {
    return (
      <svg viewBox="0 0 16 16" className={className} fill="none" aria-hidden>
        <path d={d} stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    );
  };
}

export const IconEnvironments = stroke("M3.5 5.2 8 3l4.5 2.2v5.6L8 13l-4.5-2.2zM3.5 5.2 8 7.4l4.5-2.2M8 7.4V13");
export const IconRuns = stroke("M2.5 8h3l1.6-3.4 2.4 6.8L11.2 8h2.3");
export const IconLoad = stroke("M2.6 4.6h4.4M2.6 11.4h3.4M2.6 8h6.8M9.4 6.2 11.2 8 9.4 9.8M13.6 4.4v7.2");
export const IconMasking = stroke("M8 2.6 13 4.5v4c0 2.6-2.1 4.2-5 4.9-2.9-.7-5-2.3-5-4.9v-4z");
export const IconNetwork = stroke("M8 2.4v11.2M2.4 8h11.2M8 2.4a7.4 7.4 0 0 1 0 11.2M8 2.4a7.4 7.4 0 0 0 0 11.2");
export const IconAudit = stroke("M4 2.6h8v10.8H4zM6.2 5.6h3.6M6.2 8h3.6M6.2 10.4h2.2");
export const IconMembers = stroke("M6 7.6a2 2 0 1 0 0-4 2 2 0 0 0 0 4ZM2.6 13c0-2 1.5-3.2 3.4-3.2S9.4 11 9.4 13M10.6 4a2 2 0 0 1 0 3.6M11.4 9.9c1.3.3 2 1.4 2 3.1");
export const IconKeys = stroke("M10.4 2.6a3.4 3.4 0 1 1-3.2 4.5L2.6 11.7v1.7h1.7v-1.3h1.3v-1.3h1.3l1.3-1.3");
export const IconPlan = stroke("M2.6 12.6V7.4M6.2 12.6V3.4M9.8 12.6V9M13.4 12.6V5.6");
// A shell prompt, unframed. The obvious terminal glyph is a rounded rectangle
// with a chevron inside it, and at 16 pixels in this rail that rectangle is the
// same shape as IconAudit's document: two entries a reader tells apart by
// squinting at what is inside a box. The chevron and the caret carry the whole
// meaning, so the box is what goes.
export const IconTerminal = stroke("M3.2 4.6 6.6 8l-3.4 3.4M8.4 11.4h4.4");
// A rising trend inside an axis. Distinct from IconPlan, which is a bar chart:
// two icons that are both bars are two rail entries a reader cannot tell apart
// at 16 pixels, and the rail is scanned rather than read.
export const IconAnalytics = stroke(
  "M2.6 2.8v10.4h10.8M5 10.6l2.6-3.2 2.2 2 3-4",
);

// Two sliders rather than a cog. A cog is the most reused glyph in every icon
// set there is, and at 16 pixels its teeth turn into noise; two tracks with a
// handle each stay legible and are not the same shape as everything else in
// this rail.
export const IconSettings = stroke("M2.8 5.4h10.4M2.8 10.6h10.4M6.2 3.9v3M10 9.1v3");
export const IconSignOut = stroke("M6.4 3.2H3.2v9.6h3.2M9.6 10.4 12.8 8 9.6 5.6M12.8 8H6.4");

/* -------------------------------------------------------------------------
 * The operator portal
 *
 * Its own icons rather than borrowed ones. Reusing IconMembers for tenants and
 * IconSettings for operators would have shipped faster and read wrong: the
 * navigation is the one place where an icon is the only thing distinguishing
 * two entries at a glance, and "a person" meaning both a customer's member and
 * a member of staff is exactly the confusion this portal cannot afford.
 *
 * Same stroke() helper, so they inherit the one grid, one width and one size
 * the rest of the navigation uses.
 * ---------------------------------------------------------------------- */

/** Organizations: two buildings, because a tenant is a company and not a person. */
export const IconTenants = stroke(
  "M2.6 13.4V5.2l4-1.8v10M6.6 13.4V7l6.8-2.2v8.6M2 13.4h12M8.6 8.6v1M8.6 10.8v1M11 8v1M11 10.2v1",
);

/** Operators: a person with a key, which is what an operator account is. */
export const IconOperators = stroke(
  "M6 7.4a2 2 0 1 0 0-4 2 2 0 0 0 0 4ZM2.4 13c0-2 1.5-3.3 3.6-3.3M12.2 6.4a1.5 1.5 0 1 1-1.4 2l-2.2 2.2v1.2h1.2v-1h1v-1h1l.9-.9",
);

/* -------------------------------------------------------------------------
 * The operator portal's navigation
 *
 * Twenty three entries in six groups, and in a rail that long the icon is not
 * decoration: it is the thing that lets somebody find "Logs" again without
 * reading twenty two labels. So every glyph here is drawn for the ONE section
 * it names, on the same 16 grid and the same 1.3 stroke as the rest, and none
 * of them is a sparkle, a rocket or a lightning bolt.
 *
 * Seven entries reuse an icon that already exists rather than getting a near
 * duplicate: tenants, runs, keys, audit, the bar chart, the operator and the
 * two sliders all mean here exactly what they mean where they were drawn. A
 * second glyph for the same idea is how two lists in one product stop
 * matching.
 * ---------------------------------------------------------------------- */

/** Overview: four panels, which is what the page is. */
export const IconOverview = stroke(
  "M2.6 2.6h4.4v4.4H2.6zM9 2.6h4.4v4.4H9zM2.6 9h4.4v4.4H2.6zM9 9h4.4v4.4H9z",
);

/** Support: a conversation. The portal's support section starts from somebody
 *  having asked something. */
export const IconSupport = stroke(
  "M13.4 9.4a1.4 1.4 0 0 1-1.4 1.4H6.4L3.4 13.2v-2.4H3a1.4 1.4 0 0 1-1.4-1.4V4.2A1.4 1.4 0 0 1 3 2.8h9a1.4 1.4 0 0 1 1.4 1.4zM5 5.6h6M5 7.8h3.6",
);

/** Billing: a card with a stripe across it. */
export const IconBilling = stroke("M2 4.6h12v6.8H2zM2 7.2h12M4.4 9.6h2.6");

/** Production twins: one surface copied onto another, offset. */
export const IconTwins = stroke("M2.6 2.8h7v7h-7zM6.4 6.4h7v7h-7");

/** Safe state and databases: the cylinder, which is the only glyph everybody
 *  reads as a database without a label. */
export const IconDatabase = stroke(
  "M8 2.4c2.8 0 4.8.8 4.8 1.7S10.8 5.8 8 5.8 3.2 5 3.2 4.1 5.2 2.4 8 2.4ZM3.2 4.1v3.8c0 .9 2 1.7 4.8 1.7s4.8-.8 4.8-1.7V4.1M3.2 7.9v3.8c0 .9 2 1.7 4.8 1.7s4.8-.8 4.8-1.7V7.9",
);

/** Branches: the git glyph, a trunk and one branch merging off it. */
export const IconBranches = stroke(
  "M4.8 5.4v5.2M4.8 5.4a1.4 1.4 0 1 0 0-2.8 1.4 1.4 0 0 0 0 2.8ZM4.8 13.4a1.4 1.4 0 1 0 0-2.8 1.4 1.4 0 0 0 0 2.8ZM11.2 5.4a1.4 1.4 0 1 0 0-2.8 1.4 1.4 0 0 0 0 2.8ZM11.2 5.4v1.2c0 1.7-1.4 2.6-3.2 2.8",
);

/** Experiments: a flask. An experiment is a thing being measured, not a lab
 *  coat and not a beaker of bubbles. */
export const IconExperiments = stroke(
  "M6.4 2.4v3.7L3.2 12a1 1 0 0 0 .9 1.5h7.8a1 1 0 0 0 .9-1.5L9.6 6.1V2.4M5.4 2.4h5.2M4.6 9.4h6.8",
);

/** Repositories: a book that opens, which is the shape every forge uses. */
export const IconRepositories = stroke(
  "M3.4 2.8h8.2a1 1 0 0 1 1 1v9.4H4.6a1.2 1.2 0 0 1-1.2-1.2zM3.4 11h9.2M5.8 5.4h4",
);

/** MCP: a connector with two pins, because what this section manages is what
 *  is plugged into the product. */
export const IconMcp = stroke(
  "M6 2.6v3M10 2.6v3M4.4 5.6h7.2v2.2A3.6 3.6 0 0 1 8 11.4a3.6 3.6 0 0 1-3.6-3.6zM8 11.4v2",
);

/** Integrations: something leaving the box, which is what a webhook is. */
export const IconIntegrations = stroke(
  "M8.4 3.2H3.2v9.6h9.6V7.6M9.6 6.4l3.6-3.6M10.2 2.8h3v3",
);

/** Infrastructure: two racked machines, each with its own indicator. The dot
 *  is a zero length segment under a round cap, so it inherits the one stroke
 *  width rather than being a second shape to keep in step. */
export const IconInfrastructure = stroke(
  "M2.6 3.2h10.8v3.4H2.6zM2.6 9.4h10.8v3.4H2.6zM4.8 4.9h.01M4.8 11.1h.01",
);

/** Logs: a terminal, which is where the reader has been looking already. */
export const IconLogs = stroke("M2.6 3.2h10.8v9.6H2.6zM5 6.4 7 8.4l-2 2M8.8 10.4h3");

/** Email: an envelope. */
export const IconEmail = stroke("M2.4 4.4h11.2v7.2H2.4zM2.4 4.8 8 8.9l5.6-4.1");

/** Incidents and kill switches: the power symbol. The one glyph whose meaning
 *  nobody has to be taught, on the section where being taught is too slow. */
export const IconIncidents = stroke("M8 2.6v4.8M4.6 5a4.6 4.6 0 1 0 6.8 0");

/** The security centre: a shield with a check, so it is not confused with the
 *  plain shield the customer console uses for masking. */
export const IconSecurity = stroke(
  "M8 2.2 12.8 4v4c0 2.6-2 4.3-4.8 5-2.8-.7-4.8-2.4-4.8-5V4zM6 7.9 7.4 9.3 10 6.5",
);

/** Data governance: a records box with a label. Governance is custody of what
 *  is kept, and this is the shape of custody. */
export const IconGovernance = stroke("M2.6 3h10.8v2.6H2.6zM3.6 5.6h8.8v7.4H3.6zM6.4 8.6h3.2");
