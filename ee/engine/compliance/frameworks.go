package compliance

// The two packs, as tables.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Data rather than code, deliberately. The audience for the part of this that
// matters is a security engineer or an auditor, and they should be able to read
// what is claimed, and what is explicitly not claimed, without reading Go.
//
// Every control carries a Scope, and the Scope is the honest half. This product
// is a preview environment engine: it holds a masked copy of production, decides
// what an environment may reach, and records what happened. That touches maybe a
// third of either framework and touches none of it completely. A control whose
// Scope said "covered" would be the lie; a control omitted because it is not
// covered would be the quieter lie, because the document would then read as a
// complete answer.
//
// So both packs list the whole relevant surface, including the controls this
// product can say nothing about, marked as such and with no evidence attached.

// SOC2 is the Trust Services Criteria this product can speak to, plus the ones
// it deliberately cannot.
func SOC2() Pack {
	return Pack{
		Name:     "SOC 2",
		Revision: "2017 Trust Services Criteria, with the 2022 points of focus",
		Note: "This covers the criteria a preview environment engine touches. It is not a " +
			"SOC 2 report and it is not an opinion: it shows what this system recorded and " +
			"names the artifact, and every conclusion is the auditor's. Criteria this product " +
			"has nothing to say about are listed as such rather than omitted, so that the " +
			"gaps are visible rather than implied.",
		Controls: []Control{
			{
				ID:    "CC6.1",
				Title: "Logical access security",
				Requirement: "The entity implements logical access security software, " +
					"infrastructure, and architectures over protected information assets.",
				Scope: "This product enforces tenant isolation in the database with row level " +
					"security rather than in application queries, so a query that omits its own " +
					"filter returns nothing rather than another organization's rows. It says " +
					"nothing about the entity's own workstations, network or identity provider.",
				Check: CheckTenantIsolation,
			},
			{
				ID:    "CC6.2",
				Title: "Registration and authorization of users",
				Requirement: "Prior to issuing credentials, the entity registers and authorizes " +
					"new internal and external users.",
				Scope: "Every membership change is recorded in the audit log with who made it " +
					"and from where. Whether the authorization behind it was appropriate is " +
					"outside anything this product can see.",
				Check: CheckAuditCoverage,
			},
			{
				ID:    "CC6.3",
				Title: "Access removal",
				Requirement: "The entity authorizes, modifies, or removes access to data and " +
					"software based on roles, responsibilities, and the system design, and " +
					"removes access when no longer required.",
				Scope: "A membership removal revokes that member's sessions in the same " +
					"transaction, so access ends when the membership does rather than when the " +
					"session expires. This check reports any removal that did not.",
				Check: CheckAccessRemoval,
			},
			{
				ID:    "CC6.6",
				Title: "Boundary protection",
				Requirement: "The entity implements logical access security measures to protect " +
					"against threats from sources outside its system boundaries.",
				Scope: "An environment created by this product has no route off its boundary " +
					"except a proxy that decides every request against the repository's own " +
					"manifest, and every decision is recorded with the rule that made it. This " +
					"is about environments this product created and nothing else the entity runs.",
				Check: CheckEgress,
			},
			{
				ID:    "CC6.7",
				Title: "Restricting the movement of information",
				Requirement: "The entity restricts the transmission, movement, and removal of " +
					"information to authorized internal and external users and processes.",
				Scope: "Production data reaches an environment only through a golden that was " +
					"scanned and found free of real data, and the scan is signed. This check " +
					"reports any environment created from a golden that was not verified first.",
				Check: CheckMaskingBeforeUse,
			},
			{
				ID:    "CC7.2",
				Title: "Monitoring for anomalies",
				Requirement: "The entity monitors system components for anomalies indicative of " +
					"malicious acts, natural disasters, and errors.",
				Scope: "Every action taken through this product is recorded with its actor, " +
					"target, origin and time. Whether anybody reviews those records is a process " +
					"question this cannot answer.",
				Check: CheckAuditCoverage,
			},
			{
				ID:    "CC7.3",
				Title: "Evaluation of security events",
				Requirement: "The entity evaluates security events to determine whether they " +
					"could or have resulted in a failure to meet its objectives.",
				Scope: "The audit log is append only at the database level and hash chained, so " +
					"the record an evaluation rests on can be shown not to have been rewritten.",
				Check: CheckAuditChain,
			},
			{
				ID:    "CC8.1",
				Title: "Change management",
				Requirement: "The entity authorizes, designs, develops, configures, documents, " +
					"tests, approves, and implements changes to infrastructure, data, software " +
					"and procedures.",
				Scope: "Organization policy is evaluated before any environment is created and " +
					"can only refuse, never permit, so a repository cannot opt out of a control " +
					"by changing a file it owns. This says nothing about the entity's own code " +
					"review or release process.",
				Check: CheckPolicy,
			},
			{
				ID:    "CC7.1",
				Title: "Detection of configuration changes",
				Requirement: "The entity uses detection and monitoring procedures to identify " +
					"changes to configurations that introduce new vulnerabilities.",
				Scope: "The application role's privileges on the audit log are checked directly, " +
					"so a grant that would allow history to be rewritten is visible here rather " +
					"than only in a migration nobody re-read.",
				Check: CheckAuditAppendOnly,
			},
			{
				ID:    "A1.2",
				Title: "Recovery and backup",
				Requirement: "The entity authorizes, designs, develops, implements, operates, " +
					"approves, maintains, and monitors environmental protections, software, data " +
					"backup processes, and recovery infrastructure.",
				Scope: "Nothing here. Backup and restore of the control plane is an operator " +
					"responsibility and its evidence comes from wherever that runs.",
				Check: CheckNone,
			},
			{
				ID:    "CC1.4",
				Title: "Competence of personnel",
				Requirement: "The entity demonstrates a commitment to attract, develop, and " +
					"retain competent individuals.",
				Scope: "Nothing here. This is a people process and its evidence is elsewhere.",
				Check: CheckNone,
			},
			{
				ID:    "CC6.4",
				Title: "Physical access",
				Requirement: "The entity restricts physical access to facilities and protected " +
					"information assets.",
				Scope: "Nothing here. This is a facilities control.",
				Check: CheckNone,
			},
		},
	}
}

// HIPAA is the Security Rule safeguards this product can speak to, plus the
// Privacy Rule's de-identification standard, which is the one it speaks to
// most directly.
func HIPAA() Pack {
	// Six years, from 45 CFR 164.316(b)(2)(i). The number belongs to the
	// framework rather than to the check, which is why it is here: SOC 2 asks
	// for the period under review and HIPAA asks for six years, and a check
	// with a number compiled into it would be right for one of them.
	const sixYears = 6 * 365

	return Pack{
		Name:     "HIPAA Security Rule",
		Revision: "45 CFR Part 164, Subparts C and E",
		Note: "This covers the safeguards a preview environment engine touches. It is not a " +
			"risk analysis and it is not a determination of compliance. The most useful part " +
			"is likely to be 164.514(b): a preview environment built by this product holds a " +
			"copy of production that was scanned for real data and signed, and that scan is " +
			"the artifact somebody asks for. Safeguards this product has nothing to say about " +
			"are listed as such rather than omitted.",
		Controls: []Control{
			{
				ID:    "164.308(a)(1)(ii)(D)",
				Title: "Information system activity review",
				Requirement: "Implement procedures to regularly review records of information " +
					"system activity, such as audit logs, access reports, and security incident " +
					"tracking reports.",
				Scope: "The records exist, are append only at the database level, and are hash " +
					"chained. Whether they are regularly reviewed is a procedure this cannot see.",
				Check: CheckAuditCoverage,
			},
			{
				ID:    "164.308(a)(3)(ii)(C)",
				Title: "Termination procedures",
				Requirement: "Implement procedures for terminating access to electronic " +
					"protected health information when the employment of, or other arrangement " +
					"with, a workforce member ends.",
				Scope: "A membership removal revokes that member's sessions in the same " +
					"transaction. This check reports any removal that did not.",
				Check: CheckAccessRemoval,
			},
			{
				ID:    "164.308(a)(4)(ii)(B)",
				Title: "Access authorization",
				Requirement: "Implement policies and procedures for granting access to " +
					"electronic protected health information.",
				Scope: "Access to an organization's data is enforced by row level security in " +
					"the database, not by application filters, and the application role cannot " +
					"bypass it.",
				Check: CheckTenantIsolation,
			},
			{
				ID:    "164.312(b)",
				Title: "Audit controls",
				Requirement: "Implement hardware, software, and procedural mechanisms that " +
					"record and examine activity in information systems that contain or use " +
					"electronic protected health information.",
				Scope: "The audit log records every action with its actor, target, origin and " +
					"time, and the application role holds only INSERT and SELECT on it, so a " +
					"rewrite is refused by the database rather than by a code path.",
				Check: CheckAuditAppendOnly,
			},
			{
				ID:    "164.312(c)(1)",
				Title: "Integrity",
				Requirement: "Implement policies and procedures to protect electronic protected " +
					"health information from improper alteration or destruction.",
				Scope: "Each audit entry carries the hash of the one before it, so altering or " +
					"removing an entry leaves a break that this check finds. That covers the " +
					"record of activity; it does not cover the health information itself, which " +
					"lives in the entity's own systems.",
				Check: CheckAuditChain,
			},
			{
				ID:    "164.312(e)(1)",
				Title: "Transmission security",
				Requirement: "Implement technical security measures to guard against " +
					"unauthorized access to electronic protected health information that is " +
					"being transmitted over an electronic communications network.",
				Scope: "An environment has no route off its boundary except a proxy that decides " +
					"every request against the repository's manifest, and every decision is " +
					"recorded. This is about what environments this product created may reach.",
				Check: CheckEgress,
			},
			{
				ID:    "164.514(b)",
				Title: "De-identification of protected health information",
				Requirement: "Apply generally accepted statistical or expert methods, or remove " +
					"the specified identifiers, such that the information is not individually " +
					"identifiable.",
				Scope: "This is where this product does the most work and where its limits " +
					"matter most. Every golden is scanned for real data before it can be " +
					"branched, and the scan is signed with what was looked at, how many rows " +
					"were sampled, and the hash of the rules used. A scan is a SAMPLE and not a " +
					"proof: it is evidence that a masking rule was applied and worked on what " +
					"was read, and it is not an expert determination under 164.514(b)(1). If " +
					"you need that determination, this is an input to it and not a substitute.",
				Check: CheckMasking,
			},
			{
				ID:    "164.502(b)",
				Title: "Minimum necessary",
				Requirement: "Make reasonable efforts to limit protected health information to " +
					"the minimum necessary to accomplish the intended purpose.",
				Scope: "An environment is created from a masked golden and is destroyed when the " +
					"branch closes, so a copy does not persist beyond its purpose. This check " +
					"reports environments whose resources were not destroyed.",
				Check: CheckTeardown,
			},
			{
				ID:    "164.316(b)(2)(i)",
				Title: "Retention",
				Requirement: "Retain the documentation required by this subpart for six years " +
					"from the date of its creation or the date when it last was in effect, " +
					"whichever is later.",
				Scope: "The audit log's retention setting is read and compared against six " +
					"years. Retention of the entity's own documentation is elsewhere.",
				Check:         CheckRetention,
				retentionDays: sixYears,
			},
			{
				ID:    "164.308(a)(7)(ii)(A)",
				Title: "Data backup plan",
				Requirement: "Establish and implement procedures to create and maintain " +
					"retrievable exact copies of electronic protected health information.",
				Scope: "Nothing here. Backup of the control plane is an operator responsibility " +
					"and its evidence comes from wherever that runs.",
				Check: CheckNone,
			},
			{
				ID:    "164.310(a)(1)",
				Title: "Facility access controls",
				Requirement: "Implement policies and procedures to limit physical access to " +
					"electronic information systems and the facilities in which they are housed.",
				Scope: "Nothing here. This is a facilities control.",
				Check: CheckNone,
			},
			{
				ID:          "164.308(a)(5)(ii)(A)",
				Title:       "Security reminders",
				Requirement: "Periodic security updates for the workforce.",
				Scope:       "Nothing here. This is a training process.",
				Check:       CheckNone,
			},
		},
	}
}

// Packs returns every pack this build carries, by name.
func Packs() map[string]Pack {
	return map[string]Pack{
		"soc2":  SOC2(),
		"hipaa": HIPAA(),
	}
}
