/**
 * The demo-request form's logic, kept out of the component so it can be tested
 * without a browser.
 *
 * The form on /request-demo posts to the SAME endpoint the enterprise form
 * does, POST /v1/leads, which writes a row into enterprise_leads and notifies
 * over the existing Resend path. That table has no place for a job title, an
 * organization type or a country of its own, so those ride in the `message`
 * column as a labelled block, and `source` is set to "demo" so a demo request
 * is told apart from a contact-form lead in the queue and the notification.
 * Reusing the endpoint is what keeps this off a new table and its migration
 * number, which parallel branches always collide on.
 *
 * validateDemoRequest is the client half of the guard the server's validateLead
 * is the other half of. It refuses one thing at a time, in the order the fields
 * appear, so the message always points at the first thing to fix, and it uses
 * the same permissive email judgement leads.ts uses on the server, so the two
 * halves agree about what an address is.
 */

/** The longest each composed field may be, matching leads.ts LIMITS so the
 *  server's CHECK constraints are never the thing that refuses a submission. */
export const DEMO_LIMITS = {
  name: 200,
  email: 320,
  company: 200,
  jobTitle: 200,
  phone: 60,
  message: 4000,
} as const;

/** What the organization type select offers. Real categories, not filler, and
 *  a plain "Other" so nobody is forced into a box that does not fit. */
export const ORG_TYPES = [
  "Startup",
  "Scale-up",
  "Enterprise",
  "Agency or consultancy",
  "Public sector",
  "Other",
] as const;

/** The country select. The full ISO 3166 short-name list, so nobody scrolls
 *  looking for a country that was left out. Reference data, not placeholder. */
export const COUNTRIES = [
  "Afghanistan", "Albania", "Algeria", "Andorra", "Angola", "Antigua and Barbuda", "Argentina",
  "Armenia", "Australia", "Austria", "Azerbaijan", "Bahamas", "Bahrain", "Bangladesh", "Barbados",
  "Belarus", "Belgium", "Belize", "Benin", "Bhutan", "Bolivia", "Bosnia and Herzegovina",
  "Botswana", "Brazil", "Brunei", "Bulgaria", "Burkina Faso", "Burundi", "Cabo Verde", "Cambodia",
  "Cameroon", "Canada", "Central African Republic", "Chad", "Chile", "China", "Colombia",
  "Comoros", "Congo (Brazzaville)", "Congo (Kinshasa)", "Costa Rica", "Croatia", "Cuba", "Cyprus",
  "Czechia", "Denmark", "Djibouti", "Dominica", "Dominican Republic", "Ecuador", "Egypt",
  "El Salvador", "Equatorial Guinea", "Eritrea", "Estonia", "Eswatini", "Ethiopia", "Fiji",
  "Finland", "France", "Gabon", "Gambia", "Georgia", "Germany", "Ghana", "Greece", "Grenada",
  "Guatemala", "Guinea", "Guinea-Bissau", "Guyana", "Haiti", "Honduras", "Hungary", "Iceland",
  "India", "Indonesia", "Iran", "Iraq", "Ireland", "Israel", "Italy", "Ivory Coast", "Jamaica",
  "Japan", "Jordan", "Kazakhstan", "Kenya", "Kiribati", "Kuwait", "Kyrgyzstan", "Laos", "Latvia",
  "Lebanon", "Lesotho", "Liberia", "Libya", "Liechtenstein", "Lithuania", "Luxembourg",
  "Madagascar", "Malawi", "Malaysia", "Maldives", "Mali", "Malta", "Marshall Islands",
  "Mauritania", "Mauritius", "Mexico", "Micronesia", "Moldova", "Monaco", "Mongolia", "Montenegro",
  "Morocco", "Mozambique", "Myanmar", "Namibia", "Nauru", "Nepal", "Netherlands", "New Zealand",
  "Nicaragua", "Niger", "Nigeria", "North Korea", "North Macedonia", "Norway", "Oman", "Pakistan",
  "Palau", "Palestine", "Panama", "Papua New Guinea", "Paraguay", "Peru", "Philippines", "Poland",
  "Portugal", "Qatar", "Romania", "Russia", "Rwanda", "Saint Kitts and Nevis", "Saint Lucia",
  "Saint Vincent and the Grenadines", "Samoa", "San Marino", "Sao Tome and Principe",
  "Saudi Arabia", "Senegal", "Serbia", "Seychelles", "Sierra Leone", "Singapore", "Slovakia",
  "Slovenia", "Solomon Islands", "Somalia", "South Africa", "South Korea", "South Sudan", "Spain",
  "Sri Lanka", "Sudan", "Suriname", "Sweden", "Switzerland", "Syria", "Taiwan", "Tajikistan",
  "Tanzania", "Thailand", "Timor-Leste", "Togo", "Tonga", "Trinidad and Tobago", "Tunisia",
  "Turkey", "Turkmenistan", "Tuvalu", "Uganda", "Ukraine", "United Arab Emirates",
  "United Kingdom", "United States", "Uruguay", "Uzbekistan", "Vanuatu", "Vatican City",
  "Venezuela", "Vietnam", "Yemen", "Zambia", "Zimbabwe",
] as const;

/** What the person typed, before it is validated or composed. */
export interface DemoRequestFields {
  firstName: string;
  lastName: string;
  email: string;
  company: string;
  jobTitle: string;
  phone: string;
  orgType: string;
  country: string;
  marketingOptIn: boolean;
}

/** The body POST /v1/leads takes. The same shape the enterprise form sends. */
export interface DemoLeadPayload {
  name: string;
  email: string;
  company: string;
  message: string;
  source: "demo";
}

/**
 * The same permissive address test leads.ts makes on the server. A regular
 * expression strict about RFC 5322 turns away real addresses, and turning away
 * somebody who wants a demo costs more than one row that bounces.
 */
export function looksLikeEmail(value: string): boolean {
  const email = value.trim().toLowerCase();
  const at = email.indexOf("@");
  const domain = email.slice(at + 1);
  return (
    at > 0 &&
    at === email.lastIndexOf("@") &&
    !/[\s,;<>"]/.test(email) &&
    domain.includes(".") &&
    !domain.startsWith(".") &&
    !domain.endsWith(".")
  );
}

/**
 * What is wrong with what somebody typed, or null when nothing is.
 *
 * One message at a time, in field order, naming the field, so the person is
 * pointed at the first thing to fix rather than at a wall of complaints. The
 * required fields are the ones marked required on the form; phone and the
 * marketing opt-in are the two that are not.
 */
export function validateDemoRequest(fields: DemoRequestFields): string | null {
  const firstName = fields.firstName.trim();
  if (!firstName) return "Tell us your first name so a reply is addressed to somebody.";
  const lastName = fields.lastName.trim();
  if (!lastName) return "Tell us your last name too.";
  if ((firstName + " " + lastName).length > DEMO_LIMITS.name) {
    return `A name has to be under ${DEMO_LIMITS.name} characters.`;
  }

  const email = fields.email.trim();
  if (!email) return "We need a work email to reply to.";
  if (!looksLikeEmail(email)) return "That does not look like an email address.";
  if (email.length > DEMO_LIMITS.email) {
    return `An email address has to be under ${DEMO_LIMITS.email} characters.`;
  }

  const company = fields.company.trim();
  if (!company) return "Tell us who you work for.";
  if (company.length > DEMO_LIMITS.company) {
    return `A company name has to be under ${DEMO_LIMITS.company} characters.`;
  }

  const jobTitle = fields.jobTitle.trim();
  if (!jobTitle) return "Tell us your job title.";
  if (jobTitle.length > DEMO_LIMITS.jobTitle) {
    return `A job title has to be under ${DEMO_LIMITS.jobTitle} characters.`;
  }

  if (fields.phone.trim().length > DEMO_LIMITS.phone) {
    return `A phone number has to be under ${DEMO_LIMITS.phone} characters.`;
  }

  if (!ORG_TYPES.includes(fields.orgType as (typeof ORG_TYPES)[number])) {
    return "Choose the option that best describes your organization.";
  }
  if (!COUNTRIES.includes(fields.country as (typeof COUNTRIES)[number])) {
    return "Choose your country.";
  }

  return null;
}

/**
 * Turns the validated Harvey fields into the lead body the endpoint takes.
 *
 * The three fields enterprise_leads has no column for, plus the phone and the
 * marketing opt-in, are written into the message as a labelled block a person
 * reading the queue can act on. `source` is "demo" so the row and the
 * notification say where it came from. Call only on fields that already passed
 * validateDemoRequest.
 */
export function composeDemoLead(fields: DemoRequestFields): DemoLeadPayload {
  const name = `${fields.firstName.trim()} ${fields.lastName.trim()}`;
  const phone = fields.phone.trim();
  const message = [
    "Demo request from the /request-demo form.",
    "",
    `Job title: ${fields.jobTitle.trim()}`,
    `Organization type: ${fields.orgType}`,
    `Country: ${fields.country}`,
    `Phone: ${phone || "not given"}`,
    `Marketing opt-in: ${fields.marketingOptIn ? "yes" : "no"}`,
  ].join("\n");
  return { name, email: fields.email.trim(), company: fields.company.trim(), message, source: "demo" };
}
