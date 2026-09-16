package insights

import "strings"

// The database-security migration checks.
//
// These are lint rules in the same machine as the DDL-safety rules in lint.go:
// they carry the same LintFinding shape, they are stamped with an identifier
// from the same catalogue, and they run on the same CheckMigration path. What
// separates them is what they are about and where their findings go. The
// DDL-safety rules are about a migration taking production down by locking or
// rewriting; these are about a migration weakening who can read what. They
// route to the security.db_security policy key rather than to migration_lint,
// so a project can gate a broadened grant apart from a non concurrent index,
// and their finding rule carries the security namespace so the release gate
// gives it a security exit code.
//
// They live in their own file, not because the compiler needs it, but because
// they are a separate closed set from the DDL-safety rules that the docs and
// tools/constcheck count and describe. Folding them into that set would make
// every sentence about "the lint rules" wrong by five and would put a security
// rule in a table headed by table rewrites.
//
// Every rule here reasons about the migration DIFF, meaning the statements this
// change adds, and never about the absolute grant or role catalogue of the
// branch. That distinction is load bearing: a snapshot restored twin inherits
// the source database's whole role catalogue, which the provider then
// neutralises, so a rule that read the absolute catalogue would fire on roles
// the change never touched. Reading only the migration's own statements is
// both the correct question ("what does this change loosen") and the one with
// no false positives from inherited state.
const (
	// RuleRLSDisabled is ALTER TABLE ... DISABLE ROW LEVEL SECURITY, which
	// turns off every policy on the table at once.
	RuleRLSDisabled Rule = "rls_disabled"
	// RuleRLSPolicyPermissive is a policy whose USING or WITH CHECK clause is a
	// tautology, so it admits every row rather than the tenant's own.
	RuleRLSPolicyPermissive Rule = "rls_policy_permissive"
	// RuleBroadGrant is a table privilege granted to PUBLIC, anon or
	// authenticated: a grant broader than a change ordinarily needs, and the
	// one database-security rule the gate refuses the change over rather than
	// reporting as a proven hole.
	RuleBroadGrant Rule = "broad_grant"
	// RuleTenantColumnRemoved is a DROP COLUMN of the column a table is scoped
	// by, org_id or tenant_id or the like, which is what a row level policy
	// compares against to keep one tenant out of another's rows.
	RuleTenantColumnRemoved Rule = "tenant_column_removed"
	// RuleDBRolePrivBroadened is a role handed SUPERUSER, BYPASSRLS or
	// CREATEROLE. BYPASSRLS is not a grant and is not row level security: it is
	// an attribute on the role that makes every policy stop applying to that
	// role's sessions, which is why it is caught apart from RuleBroadGrant.
	RuleDBRolePrivBroadened Rule = "db_role_privilege_broadened"
)

// RuleClass separates a rule that a migration weakening security trips from one
// a migration risking availability trips. The release gate reads it to send a
// finding to security.db_security or to migration_lint, so the two are gated by
// their own manifest keys rather than sharing one.
type RuleClass string

const (
	// ClassAvailability is the DDL-safety rules: locks, rewrites, non
	// concurrent index builds, destructive statements.
	ClassAvailability RuleClass = "availability"
	// ClassSecurity is the database-security rules in this file.
	ClassSecurity RuleClass = "security"
)

// securityRules is the membership set behind Class. It is the one place a rule
// is named as security class, so Class and SecurityRules cannot disagree.
var securityRules = map[Rule]bool{
	RuleRLSDisabled:         true,
	RuleRLSPolicyPermissive: true,
	RuleBroadGrant:          true,
	RuleTenantColumnRemoved: true,
	RuleDBRolePrivBroadened: true,
}

// Class reports whether a rule is about security or about availability. A rule
// not named in securityRules is availability, which is the safe default: a new
// DDL-safety rule added to lint.go and forgotten here still gates under
// migration_lint rather than vanishing from the gate.
func (r Rule) Class() RuleClass {
	if securityRules[r] {
		return ClassSecurity
	}
	return ClassAvailability
}

// SecurityRules is every database-security rule, for a caller that needs the
// set rather than a single rule's class: the family that declares a policy key
// per rule walks this so a rule and its key cannot drift.
func SecurityRules() []Rule {
	return []Rule{
		RuleRLSDisabled,
		RuleRLSPolicyPermissive,
		RuleBroadGrant,
		RuleTenantColumnRemoved,
		RuleDBRolePrivBroadened,
	}
}

// tenantColumns is the set of column names a multi tenant schema scopes rows
// by. A DROP of one of these is what removes the thing a row level policy
// compares against, so it is treated as a security regression rather than an
// ordinary column drop. The list is deliberately tight: a name outside it is
// left to the DDL-safety DROP COLUMN rule, because a rule that called every
// dropped id column a tenant boundary would be the false positive that gets
// the check switched off.
var tenantColumns = map[string]bool{
	"org_id":          true,
	"organization_id": true,
	"tenant_id":       true,
	"account_id":      true,
	"company_id":      true,
	"workspace_id":    true,
	"team_id":         true,
}

// lintSecurity runs the database-security rules over one statement. It is given
// the already folded upper form so it does not fold twice, and it reads no
// captured schema: every rule here is about the statement the migration adds.
func lintSecurity(st Statement, upper string) []LintFinding {
	var out []LintFinding
	add := func(rule Rule, table, detail, fix string) {
		out = append(out, LintFinding{
			Rule: rule, Migration: st.Migration, Statement: st.SQL,
			Table: table, Detail: detail, Fix: fix,
		})
	}

	switch {
	case strings.Contains(upper, "DISABLE ROW LEVEL SECURITY"):
		add(RuleRLSDisabled, tableAfter(st.SQL, "ALTER TABLE"),
			"DISABLE ROW LEVEL SECURITY turns off every policy on the table at once, so "+
				"every session reads every row regardless of the policies that are still "+
				"defined on it. The policies remain in the catalogue and stop being enforced, "+
				"which is why this is easy to do by accident and hard to see in a review.",
			"If the table is meant to be readable across tenants, say so in a policy rather "+
				"than by disabling the mechanism. If it is not, leave row level security on "+
				"and change the policy that is in the way.")

	case strings.HasPrefix(upper, "CREATE POLICY"), strings.HasPrefix(upper, "ALTER POLICY"):
		if permissiveClause(upper) {
			add(RuleRLSPolicyPermissive, tableAfter(st.SQL, " ON "),
				"The policy's USING or WITH CHECK clause is a tautology, so it admits every "+
					"row rather than the tenant's own. A policy that always evaluates true is "+
					"row level security that is on and enforcing nothing, which reads in a schema "+
					"dump as protected.",
				"Scope the clause to the tenant, comparing the row's tenant column against the "+
					"session's identity, for example org_id = current_setting('request.jwt.claims') "+
					"or a call to the project's own auth function.")
		}

	case strings.HasPrefix(upper, "GRANT "):
		// A GRANT with no ON clause is a role membership grant, whose danger
		// depends on what the granted role already carries, which is the
		// absolute catalogue this rule refuses to read. Only an object
		// privilege granted to a broad grantee is judged here.
		if strings.Contains(upper, " ON ") {
			if grantee := broadGrantee(upper); grantee != "" {
				add(RuleBroadGrant, tableAfter(st.SQL, " ON "),
					"The privilege is granted to "+grantee+", which is every session rather "+
						"than a named role. A grant to "+grantee+" is broader than a change "+
						"ordinarily needs and is the kind of loosening that outlives the reason "+
						"it was added.",
					"Grant the privilege to the specific role that needs it. If the intent is "+
						"genuinely that anyone may read the table, record that intent in a policy "+
						"and a review rather than in a grant to "+grantee+".")
			}
		}

	case strings.HasPrefix(upper, "ALTER ROLE"), strings.HasPrefix(upper, "CREATE ROLE"):
		if attr := escalatingRoleAttr(upper); attr != "" {
			add(RuleDBRolePrivBroadened, roleName(st.SQL, upper),
				"The role is given "+attr+". "+attrMeaning(attr)+" It is an attribute on the "+
					"role, not a grant on a table, so it is not undone by revoking a privilege "+
					"and it is easy to miss in a review that looks only at grants.",
				"Remove the "+attr+" attribute unless the role genuinely requires it. A role "+
					"that only needs to read a few tables needs a grant on those tables, never "+
					attr+".")
		}
	}

	// DROP COLUMN of a tenant scoping column is an ALTER TABLE, so it can share
	// the statement with nothing above; it is checked on its own rather than in
	// the switch, and it fires alongside the DDL-safety DROP COLUMN rule when a
	// view also reads the column, because the two say different things.
	if strings.HasPrefix(upper, "ALTER TABLE") && strings.Contains(upper, "DROP COLUMN") {
		if col := droppedColumn(st.SQL); tenantColumns[col] {
			add(RuleTenantColumnRemoved, tableAfter(st.SQL, "ALTER TABLE"),
				"The column "+col+" is what a multi tenant table is scoped by, and a row level "+
					"policy compares against it to keep one tenant out of another's rows. "+
					"Dropping it removes the thing those policies read, so a policy that still "+
					"names it fails and a policy rewritten around its absence stops isolating "+
					"tenants.",
				"If the column is genuinely unused, drop every policy that references it first, "+
					"in its own migration, so the isolation it provided is removed deliberately "+
					"rather than as a side effect of a column drop.")
		}
	}

	return out
}

// permissiveClause reports whether a CREATE or ALTER POLICY statement carries a
// tautological USING or WITH CHECK clause. It compares against a space stripped
// form so that the many spellings of the same clause, USING (true) and
// USING(TRUE) and USING ( 1 = 1 ), all read as one. Only a tautology is
// treated as permissive: a clause that omits a tenant predicate but is not a
// tautology, such as an admin only policy, is a legitimate shape and firing on
// it is the false positive that gets a security rule switched off.
func permissiveClause(upper string) bool {
	compact := strings.ReplaceAll(upper, " ", "")
	for _, clause := range []string{"USING", "WITHCHECK"} {
		for _, taut := range []string{"(TRUE)", "(1=1)"} {
			if strings.Contains(compact, clause+taut) {
				return true
			}
		}
	}
	return false
}

// broadGrantee returns PUBLIC, anon or authenticated when a GRANT names one of
// them as its grantee, and the empty string otherwise. It reads the grantee
// list after the last TO, dropping a trailing WITH GRANT OPTION, so a grant to
// a named role does not trip it.
func broadGrantee(upper string) string {
	i := strings.LastIndex(upper, " TO ")
	if i < 0 {
		return ""
	}
	tail := upper[i+len(" TO "):]
	if j := strings.Index(tail, " WITH "); j >= 0 {
		tail = tail[:j]
	}
	// Grantees are comma separated and GROUP is a noise word before a role.
	for _, raw := range strings.FieldsFunc(tail, func(r rune) bool {
		return r == ',' || r == ' '
	}) {
		switch raw {
		case "PUBLIC":
			return "PUBLIC"
		case "ANON":
			return "anon"
		case "AUTHENTICATED":
			return "authenticated"
		}
	}
	return ""
}

// escalatingRoleAttr returns the first privilege escalating attribute an ALTER
// or CREATE ROLE statement carries, or the empty string. It matches whole words
// from the folded statement, so NOSUPERUSER and NOBYPASSRLS, which REMOVE the
// attribute, do not match the attribute they contain as a substring.
func escalatingRoleAttr(upper string) string {
	for _, w := range strings.Fields(upper) {
		switch w {
		case "SUPERUSER":
			return "SUPERUSER"
		case "BYPASSRLS":
			return "BYPASSRLS"
		case "CREATEROLE":
			return "CREATEROLE"
		}
	}
	return ""
}

// attrMeaning is the one sentence a reader needs about why the attribute is a
// security regression, kept beside the rule rather than in the detail so each
// attribute reads correctly.
func attrMeaning(attr string) string {
	switch attr {
	case "SUPERUSER":
		return "A superuser bypasses every permission check and every row level policy in the database."
	case "BYPASSRLS":
		return "A role with BYPASSRLS has every row level policy stop applying to its sessions, so it reads every tenant's rows."
	case "CREATEROLE":
		return "A role with CREATEROLE can create and grant other roles, so it can hand itself any privilege indirectly."
	default:
		return ""
	}
}

// roleName pulls the role an ALTER or CREATE ROLE statement names, for the
// finding's location. It is the word after ROLE, past the noise words a role
// statement can carry.
func roleName(sql, upper string) string {
	keyword := "ALTER ROLE"
	if strings.HasPrefix(upper, "CREATE ROLE") {
		keyword = "CREATE ROLE"
	}
	for _, w := range strings.Fields(fold(sql[strings.Index(upper, keyword)+len(keyword):])) {
		switch w {
		case "IF", "NOT", "EXISTS", "ONLY":
			continue
		}
		return unquote(w)
	}
	return ""
}
