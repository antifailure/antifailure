package detect

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/personas"
)

// Whether the application has anywhere to sign in, answered from the
// repository.
//
// This exists for one decision: the login strategy the drafted personas get.
// A persona that signs in with a password needs two things the repository can
// be asked about before there is a database. Somewhere to create the account,
// which is a users table or an authentication provider that owns one, and a
// form to type the password into, which means the application renders HTML.
// A JSON API has neither, and a password persona there fails twice over: af up
// refuses with AF-DB-022 because there is no table to write the account into,
// and had it got past that, the runner waits for a login form on a service
// that serves JSON until the workflow's budget is gone. Blocked is not a pass.
//
// The signals are deliberately about capability rather than certainty. A
// repository that renders any HTML could have a sign in page, and one that
// declares a users table could hold an account, so either keeps the password
// personas that nearly every web application wants. Only a repository with
// neither gets personas that never sign in, because there a password persona
// is not a guess that might be right: it cannot work.
//
// engine/internal/personas answers the same question at run time from the live
// schema, and prefers what it finds there. This is the earlier, weaker answer,
// and it reuses that package's table names so the two cannot drift.

// KindSignInSurface is a finding that the repository could have somewhere to
// sign in: a users table it declares, or HTML it renders.
const KindSignInSurface Kind = "signin_surface"

// SignInAnalyzer reports whether the repository could have a sign in.
type SignInAnalyzer struct{}

// Name identifies the analyzer.
func (*SignInAnalyzer) Name() string { return "signin" }

// createTable matches a CREATE TABLE and captures the name, which may be
// quoted and may carry a schema.
var createTable = regexp.MustCompile(`(?is)create\s+table\s+(?:if\s+not\s+exists\s+)?([a-zA-Z0-9_."` + "`" + `]+)\s*\(`)

// prismaModel matches a Prisma model declaration.
var prismaModel = regexp.MustCompile(`(?m)^\s*model\s+([A-Za-z0-9_]+)\s*\{`)

// uiExtensions are files that mean the application renders something a person
// can type into. Templates, server rendered views and component files.
var uiExtensions = []string{
	".html", ".htm", ".xhtml",
	".tmpl", ".gohtml", ".tpl", ".twig", ".liquid", ".mustache",
	".ejs", ".hbs", ".handlebars", ".pug", ".jade",
	".erb", ".haml", ".slim",
	".jsx", ".tsx", ".vue", ".svelte", ".astro",
	".blade.php", ".razor", ".cshtml",
}

// Analyze reports each sign in surface the repository shows.
func (a *SignInAnalyzer) Analyze(_ context.Context, r *Repo) ([]Finding, error) {
	var out []Finding
	if file, table, ok := usersTableInSQL(r); ok {
		out = append(out, Finding{
			Kind: KindSignInSurface, Subject: "users-table", Value: table,
			Confidence: High, Evidence: file,
			Detail:   fmt.Sprintf("%s declares a %s table with an email column.", file, table),
			Analyzer: a.Name(),
		})
	} else if file, model, ok := usersModelInPrisma(r); ok {
		out = append(out, Finding{
			Kind: KindSignInSurface, Subject: "users-table", Value: model,
			Confidence: High, Evidence: file,
			Detail:   fmt.Sprintf("%s declares a %s model with an email field.", file, model),
			Analyzer: a.Name(),
		})
	}
	if file, ok := uiSurface(r); ok {
		out = append(out, Finding{
			Kind: KindSignInSurface, Subject: "ui", Value: path.Ext(file),
			Confidence: High, Evidence: file,
			Detail:   fmt.Sprintf("%s is rendered markup, so the application has pages a person can use.", file),
			Analyzer: a.Name(),
		})
	}
	return out, nil
}

// usersTableInSQL looks for a table a persona could be created in, declared in
// the repository's own SQL.
//
// The names are the ones the run time inference accepts, and the email column
// is required for the same reason it requires one: a manifest identifies a
// persona by address, so a users table without an address is not one this can
// provision into.
func usersTableInSQL(r *Repo) (file, table string, ok bool) {
	for _, p := range r.WithExtension(".sql") {
		body, found := r.ReadString(p)
		if !found {
			continue
		}
		for _, m := range createTable.FindAllStringSubmatchIndex(body, -1) {
			name := bareTableName(body[m[2]:m[3]])
			if !isCandidateUsersTable(name) {
				continue
			}
			if !strings.Contains(strings.ToLower(columnsOf(body[m[1]:])), "email") {
				continue
			}
			return p, name, true
		}
	}
	return "", "", false
}

// columnsOf returns the text of a CREATE TABLE's column list, from just after
// its opening parenthesis. Read to the end of the statement rather than
// matched with a balanced parser, because a type like numeric(10,2) and a
// constraint both nest, and the only thing asked of this text is whether the
// word email is in it.
func columnsOf(after string) string {
	if end := strings.Index(after, ";"); end >= 0 {
		return after[:end]
	}
	return after
}

// bareTableName strips quoting and any schema from a table name.
func bareTableName(raw string) string {
	name := strings.NewReplacer(`"`, "", "`", "").Replace(strings.TrimSpace(raw))
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return strings.ToLower(name)
}

// isCandidateUsersTable reports whether a declared name is one the run time
// inference would recognise, in either number. Prisma writes a model User for
// a table of users, and a hand written schema may do either.
func isCandidateUsersTable(name string) bool {
	for _, candidate := range personas.CandidateUserTables {
		if name == candidate || name == strings.TrimSuffix(candidate, "s") || name+"s" == candidate {
			return true
		}
	}
	return false
}

// usersModelInPrisma looks for the same table declared in a Prisma schema,
// which is how a repository with no SQL of its own declares one.
func usersModelInPrisma(r *Repo) (file, model string, ok bool) {
	for _, p := range r.WithExtension(".prisma") {
		body, found := r.ReadString(p)
		if !found {
			continue
		}
		for _, m := range prismaModel.FindAllStringSubmatchIndex(body, -1) {
			name := strings.ToLower(body[m[2]:m[3]])
			if !isCandidateUsersTable(name) {
				continue
			}
			block := body[m[1]:]
			if end := strings.Index(block, "\n}"); end >= 0 {
				block = block[:end]
			}
			if !strings.Contains(strings.ToLower(block), "email") {
				continue
			}
			return p, body[m[2]:m[3]], true
		}
	}
	return "", "", false
}

// uiSurface reports the first rendered markup file in the repository.
//
// The file only has to exist. Whether one of them is a login form is not
// decidable here and does not need to be: an application that renders pages is
// one a password persona might sign in to, and this decides only whether to
// keep that default.
func uiSurface(r *Repo) (string, bool) {
	for _, p := range r.Files() {
		lower := strings.ToLower(p)
		for _, ext := range uiExtensions {
			if strings.HasSuffix(lower, ext) {
				return p, true
			}
		}
	}
	return "", false
}
