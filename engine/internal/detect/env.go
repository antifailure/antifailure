package detect

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// jsonUnmarshal exists so that the third party analyzer does not import
// encoding/json for one call.
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// EnvAnalyzer finds the environment variables the application reads.
//
// Two sources, and the difference between them matters. Example files say what
// the author intended; source references say what the code actually reads. A
// variable in the code and not in any example file is the interesting case: it
// is the one that will be missing at run time, and it is reported as required
// with an unknown purpose rather than quietly dropped.
type EnvAnalyzer struct{}

// Name identifies the analyzer.
func (*EnvAnalyzer) Name() string { return "env" }

var envExampleNames = []string{
	".env.example", ".env.sample", ".env.template", ".env.defaults",
	".env.local.example", "env.example", ".env.test.example",
}

// envRefPatterns match a variable read from code, per language. Only static
// references count: a name assembled at run time cannot be found by reading,
// and guessing at one would produce noise.
var envRefPatterns = []*regexp.Regexp{
	// JavaScript and TypeScript
	regexp.MustCompile(`process\.env\.([A-Z][A-Z0-9_]{2,})`),
	regexp.MustCompile(`process\.env\[["']([A-Z][A-Z0-9_]{2,})["']\]`),
	regexp.MustCompile(`import\.meta\.env\.([A-Z][A-Z0-9_]{2,})`),
	regexp.MustCompile(`Deno\.env\.get\(["']([A-Z][A-Z0-9_]{2,})["']\)`),
	regexp.MustCompile(`Bun\.env\.([A-Z][A-Z0-9_]{2,})`),
	// Python
	regexp.MustCompile(`os\.environ\[["']([A-Z][A-Z0-9_]{2,})["']\]`),
	regexp.MustCompile(`os\.environ\.get\(\s*["']([A-Z][A-Z0-9_]{2,})["']`),
	regexp.MustCompile(`os\.getenv\(\s*["']([A-Z][A-Z0-9_]{2,})["']`),
	// Go
	regexp.MustCompile(`os\.Getenv\(\s*["'` + "`" + `]([A-Z][A-Z0-9_]{2,})["'` + "`" + `]\)`),
	regexp.MustCompile(`os\.LookupEnv\(\s*["'` + "`" + `]([A-Z][A-Z0-9_]{2,})["'` + "`" + `]\)`),
	// Ruby
	regexp.MustCompile(`ENV\[["']([A-Z][A-Z0-9_]{2,})["']\]`),
	regexp.MustCompile(`ENV\.fetch\(\s*["']([A-Z][A-Z0-9_]{2,})["']`),
	// PHP and shell, which appear in Dockerfiles and entrypoints
	regexp.MustCompile(`getenv\(\s*["']([A-Z][A-Z0-9_]{2,})["']\s*\)`),
	regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]{2,})(?::-[^}]*)?\}`),
}

// sourceExtensions are the files worth scanning for variable references.
var sourceExtensions = []string{
	".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".py", ".go", ".rb",
	".php", ".java", ".kt", ".rs", ".sh", ".yml", ".yaml",
}

// maxSourceFiles bounds the reference scan. A repository with a hundred
// thousand source files does not need all of them read to find its
// configuration, and reading them all is how detection blows its time budget.
const maxSourceFiles = 4000

// Analyze reports every variable the application appears to need.
func (a *EnvAnalyzer) Analyze(_ context.Context, r *Repo) ([]Finding, error) {
	var out []Finding

	declared := map[string]string{}
	for _, name := range envExampleNames {
		for _, p := range r.Glob(name) {
			body, ok := r.ReadString(p)
			if !ok {
				continue
			}
			for _, v := range parseDotenvNames(body) {
				if _, exists := declared[v]; !exists {
					declared[v] = p
				}
			}
		}
	}
	for v, file := range declared {
		out = append(out, Finding{
			Kind: KindEnvVar, Subject: v, Value: "declared",
			Confidence: High, Evidence: file,
			Detail: fmt.Sprintf("%s lists %s.", file, v),
		})
	}

	// turbo.json declares which variables affect a build, which is an
	// authoritative list for a monorepo.
	if b, ok := r.Read("turbo.json"); ok {
		for _, v := range turboEnvNames(b) {
			if _, exists := declared[v]; exists {
				continue
			}
			declared[v] = "turbo.json"
			out = append(out, Finding{
				Kind: KindEnvVar, Subject: v, Value: "declared",
				Confidence: High, Evidence: "turbo.json",
				Detail: "turbo.json lists it as a build input.",
			})
		}
	}

	referenced := map[string]string{}
	scanned := 0
	for _, p := range r.Files() {
		if scanned >= maxSourceFiles {
			break
		}
		if !hasAnySuffix(p, sourceExtensions) || isExamplePath(p) {
			continue
		}
		body, ok := r.ReadString(p)
		if !ok {
			continue
		}
		scanned++
		for _, re := range envRefPatterns {
			for _, m := range re.FindAllStringSubmatch(body, -1) {
				name := m[1]
				if isNoiseEnvName(name) {
					continue
				}
				if _, exists := referenced[name]; !exists {
					referenced[name] = p
				}
			}
		}
	}

	// The interesting set: read by the code, declared nowhere. These are the
	// variables that will be missing at run time.
	var undeclared []string
	for name := range referenced {
		if _, ok := declared[name]; !ok {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(undeclared)
	for _, name := range undeclared {
		out = append(out, Finding{
			Kind: KindEnvVar, Subject: name, Value: "referenced",
			Confidence: Medium, Evidence: referenced[name],
			Detail: fmt.Sprintf(
				"%s reads %s, and no example file declares it, so its purpose is unknown.",
				referenced[name], name),
		})
	}
	return out, nil
}

// parseDotenvNames reads names from a dotenv file, ignoring the values.
//
// Values are ignored deliberately. An example file sometimes contains a real
// credential by accident, and reading the value would put it into a finding,
// then into an event, then into a log.
func parseDotenvNames(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(body, "\n") {
		lineText := strings.TrimSpace(raw)
		if lineText == "" || strings.HasPrefix(lineText, "#") {
			continue
		}
		lineText = strings.TrimPrefix(lineText, "export ")
		i := strings.IndexByte(lineText, '=')
		if i <= 0 {
			continue
		}
		name := strings.TrimSpace(lineText[:i])
		if !validEnvName(name) || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func validEnvName(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i, c := range s {
		ok := c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(i > 0 && c >= '0' && c <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// turboEnvNames reads the env arrays from a turbo pipeline.
func turboEnvNames(b []byte) []string {
	var doc struct {
		GlobalEnv []string `json:"globalEnv"`
		Tasks     map[string]struct {
			Env []string `json:"env"`
		} `json:"tasks"`
		Pipeline map[string]struct {
			Env []string `json:"env"`
		} `json:"pipeline"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(names []string) {
		for _, n := range names {
			n = strings.TrimPrefix(n, "$")
			if validEnvName(n) && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	add(doc.GlobalEnv)
	for _, t := range doc.Tasks {
		add(t.Env)
	}
	for _, t := range doc.Pipeline {
		add(t.Env)
	}
	sort.Strings(out)
	return out
}

// isNoiseEnvName filters names that are set by the platform rather than by the
// operator, which would otherwise fill a manifest with variables nobody has to
// supply.
func isNoiseEnvName(name string) bool {
	switch name {
	case "NODE_ENV", "PATH", "HOME", "PWD", "USER", "SHELL", "TERM", "LANG",
		"TZ", "CI", "PORT", "HOSTNAME", "TMPDIR", "GOPATH", "GOROOT",
		"PYTHONPATH", "RAILS_ENV", "RACK_ENV", "DEBUG", "LOG_LEVEL",
		"NODE_OPTIONS", "npm_config_registry", "VERCEL", "VERCEL_ENV",
		"NEXT_RUNTIME", "AWS_REGION", "AWS_DEFAULT_REGION":
		return true
	}
	return strings.HasPrefix(name, "GITHUB_") || strings.HasPrefix(name, "RUNNER_") ||
		strings.HasPrefix(name, "CI_")
}

func hasAnySuffix(s string, suffixes []string) bool {
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}

// collectEnvNames gathers declared variable names for the third party
// analyzer's corroboration step.
func collectEnvNames(r *Repo) map[string]string {
	out := map[string]string{}
	for _, name := range envExampleNames {
		for _, p := range r.Glob(name) {
			body, ok := r.ReadString(p)
			if !ok {
				continue
			}
			for _, v := range parseDotenvNames(body) {
				if _, exists := out[v]; !exists {
					out[v] = p
				}
			}
		}
	}
	return out
}

// MigrationAnalyzer finds the database migration tool and its command.
//
// Getting this right is what makes migration rehearsal possible: without the
// command, there is nothing to rehearse, and the most valuable database check
// in the product does not run.
type MigrationAnalyzer struct{}

// Name identifies the analyzer.
func (*MigrationAnalyzer) Name() string { return "migrations" }

// Analyze reports the migration tool and command.
func (a *MigrationAnalyzer) Analyze(_ context.Context, r *Repo) ([]Finding, error) {
	var out []Finding

	for _, p := range r.Glob("schema.prisma") {
		dir := dirOf(path.Dir(p)) // prisma/schema.prisma sits one level down
		body, _ := r.ReadString(p)
		provider := prismaProvider(body)
		out = append(out,
			Finding{Kind: KindMigration, Subject: sanitizeServiceName(baseNameFor(dir, r)),
				Value: "npx prisma migrate deploy", Confidence: High, Evidence: p,
				Detail: fmt.Sprintf("%s defines a Prisma schema.", p),
				Extra:  map[string]string{"tool": "prisma", "dir": dir}},
		)
		if provider != "" {
			out = append(out, Finding{
				Kind: KindDatabase, Subject: provider, Value: "prisma",
				Confidence: High, Evidence: p,
				Detail: fmt.Sprintf("The Prisma datasource provider is %s.", provider),
			})
		}
	}

	for _, p := range r.Glob("drizzle.config.ts", "drizzle.config.js", "drizzle.config.json") {
		dir := dirOf(p)
		out = append(out, Finding{
			Kind: KindMigration, Subject: sanitizeServiceName(baseNameFor(dir, r)),
			Value: "npx drizzle-kit migrate", Confidence: High, Evidence: p,
			Detail: fmt.Sprintf("%s configures Drizzle.", p),
			Extra:  map[string]string{"tool": "drizzle", "dir": dir},
		})
		if body, ok := r.ReadString(p); ok && strings.Contains(body, "postgres") {
			out = append(out, Finding{
				Kind: KindDatabase, Subject: "postgres", Value: "drizzle",
				Confidence: High, Evidence: p,
			})
		}
	}

	// Supabase migrations are plain SQL under a known directory.
	if r.Exists("supabase/config.toml") {
		out = append(out,
			Finding{Kind: KindMigration, Subject: "supabase",
				Value: "supabase db push", Confidence: High, Evidence: "supabase/config.toml",
				Extra: map[string]string{"tool": "supabase", "dir": ""}},
			Finding{Kind: KindDatabase, Subject: "postgres", Value: "supabase",
				Confidence: High, Evidence: "supabase/config.toml",
				Detail: "The project is a Supabase project, so its database is Postgres."},
		)
	}

	for _, p := range r.Glob("alembic.ini") {
		dir := dirOf(p)
		out = append(out, Finding{
			Kind: KindMigration, Subject: sanitizeServiceName(baseNameFor(dir, r)),
			Value: "alembic upgrade head", Confidence: High, Evidence: p,
			Extra: map[string]string{"tool": "alembic", "dir": dir},
		})
	}
	for _, p := range r.Glob("knexfile.js", "knexfile.ts") {
		dir := dirOf(p)
		out = append(out, Finding{
			Kind: KindMigration, Subject: sanitizeServiceName(baseNameFor(dir, r)),
			Value: "npx knex migrate:latest", Confidence: High, Evidence: p,
			Extra: map[string]string{"tool": "knex", "dir": dir},
		})
	}

	// A plain SQL migration directory, which many projects have with no tool
	// at all. Reporting it at low confidence surfaces the question rather than
	// silently skipping rehearsal.
	sqlDirs := map[string]int{}
	for _, p := range r.WithExtension(".sql") {
		d := path.Dir(p)
		if strings.Contains(d, "migration") || strings.Contains(d, "migrate") {
			sqlDirs[d]++
		}
	}
	dirs := make([]string, 0, len(sqlDirs))
	for d := range sqlDirs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		out = append(out, Finding{
			Kind: KindNote, Subject: "migrations.sql", Value: d,
			Confidence: Low, Evidence: d,
			Detail: fmt.Sprintf(
				"%s holds %d SQL files and looks like a migration directory, but no migration tool was recognised.",
				d, sqlDirs[d]),
		})
	}

	// Any Postgres connection string in an example file confirms the database.
	for _, name := range envExampleNames {
		for _, p := range r.Glob(name) {
			body, ok := r.ReadString(p)
			if !ok {
				continue
			}
			if regexp.MustCompile(`(?i)postgres(ql)?://`).MatchString(body) {
				out = append(out, Finding{
					Kind: KindDatabase, Subject: "postgres", Value: "connection-string",
					Confidence: High, Evidence: p,
					Detail: fmt.Sprintf("%s contains a Postgres connection string.", p),
				})
				break
			}
		}
	}
	return out, nil
}

var prismaProviderRe = regexp.MustCompile(`(?s)datasource\s+\w+\s*\{[^}]*provider\s*=\s*["'](\w+)["']`)

func prismaProvider(body string) string {
	if m := prismaProviderRe.FindStringSubmatch(body); m != nil {
		switch m[1] {
		case "postgresql", "postgres":
			return "postgres"
		default:
			return m[1]
		}
	}
	return ""
}

// ScheduleAnalyzer finds scheduled jobs.
type ScheduleAnalyzer struct{}

// Name identifies the analyzer.
func (*ScheduleAnalyzer) Name() string { return "schedules" }

// Analyze reports cron definitions from the places they are declared.
func (a *ScheduleAnalyzer) Analyze(_ context.Context, r *Repo) ([]Finding, error) {
	var out []Finding

	// Vercel declares crons as route plus schedule, which maps directly onto a
	// cron service that calls an endpoint.
	if b, ok := r.Read("vercel.json"); ok {
		var doc struct {
			Crons []struct {
				Path     string `json:"path"`
				Schedule string `json:"schedule"`
			} `json:"crons"`
		}
		if json.Unmarshal(b, &doc) == nil {
			for _, c := range doc.Crons {
				if c.Path == "" || c.Schedule == "" {
					continue
				}
				out = append(out, Finding{
					Kind: KindCron, Subject: sanitizeServiceName("cron" + strings.ReplaceAll(c.Path, "/", "-")),
					Value: c.Schedule, Confidence: High, Evidence: "vercel.json",
					Detail: fmt.Sprintf("vercel.json runs %s on the schedule %s.", c.Path, c.Schedule),
					Extra:  map[string]string{"path": c.Path},
				})
			}
		}
	}

	// A GitHub Actions schedule is usually CI rather than the application, so
	// it is reported as a note rather than as a service.
	for _, p := range r.Files() {
		if !strings.HasPrefix(p, ".github/workflows/") {
			continue
		}
		body, ok := r.ReadString(p)
		if !ok {
			continue
		}
		for _, m := range regexp.MustCompile(`cron:\s*["']([^"']+)["']`).FindAllStringSubmatch(body, -1) {
			out = append(out, Finding{
				Kind: KindNote, Subject: "ci-schedule", Value: m[1],
				Confidence: High, Evidence: p,
				Detail: fmt.Sprintf("%s runs on the schedule %s. This is continuous integration rather than an application job.", p, m[1]),
			})
		}
	}
	return out, nil
}
