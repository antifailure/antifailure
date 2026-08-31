package change

import (
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// classify produces every fact about one file.
//
// The order matters and is the reason this is a list rather than a map. A
// path can look like three things at once: db/migrate/schema.rb is a
// migration and a Ruby file, .github/workflows/ci.yml is a pipeline and a
// YAML config, docs/api/openapi.yaml is prose and a specification. The first
// rule that claims a path wins, and the list is written most specific first,
// so adding a general rule cannot silently take a path away from a specific
// one.
func classify(f File, m *schema.Manifest, engine *policy.Engine) []Fact {
	var out []Fact

	base, rule, evidence := baseSurface(f.Path, m)
	if base != SurfaceUnknown {
		out = append(out, Fact{
			Path: f.Path, Status: f.Status, Surface: base,
			Rule: rule, Evidence: evidence,
		})
	}

	// Service attribution is an addition, not a replacement. It is applied
	// only to the kinds of file a service is actually made of: attributing
	// README.md to the api service because the manifest says the service
	// lives at "." would be true and useless, and it would make a docs only
	// pull request select the workflows.
	if attributable(base) {
		out = append(out, serviceFacts(f, m)...)
	}

	if readable(base) {
		out = append(out, hostFacts(f, engine)...)
	}
	return out
}

// attributable reports whether a surface is the kind of file a service is
// built from.
func attributable(s Surface) bool {
	switch s {
	case SurfaceCode, SurfaceAsset, SurfaceBuild, SurfaceDependency,
		SurfaceConfig, SurfaceSchema:
		return true
	}
	return false
}

// readable reports whether the content rules should read a file's added
// lines. A URL in a README is a link and not a call, and a URL in a lockfile
// is a registry the build reaches rather than something the application does
// at runtime.
func readable(s Surface) bool {
	switch s {
	case SurfaceCode, SurfaceConfig, SurfaceManifest:
		return true
	}
	return false
}

// pathRule is one entry in the classification table.
type pathRule struct {
	// name is stable and appears in every fact the rule produces. Renaming one
	// changes a report somebody may be diffing against, so do not.
	name string
	// why is the sentence the fact carries, in the present tense.
	why string
	// surface is what the rule assigns.
	surface Surface
	// match reports whether the rule claims a path. It is given the path with
	// forward slashes, the lowercase base name, and the lowercase extension.
	match func(p, base, ext string) bool
}

// segment reports whether any directory component of p equals one of names.
func segment(p string, names ...string) bool {
	parts := strings.Split(path.Dir(p), "/")
	for _, part := range parts {
		for _, n := range names {
			if strings.EqualFold(part, n) {
				return true
			}
		}
	}
	return false
}

func hasExt(ext string, list ...string) bool {
	for _, e := range list {
		if ext == e {
			return true
		}
	}
	return false
}

func isBase(base string, list ...string) bool {
	for _, b := range list {
		if base == b {
			return true
		}
	}
	return false
}

// pathRules is the classification table, most specific first.
//
// Every rule here is a claim about a convention, and a convention is a thing
// projects break. That is what the manifest's change.rules exist for, and it
// is why an unmatched path selects everything rather than being dropped.
var pathRules = []pathRule{
	{
		name: "path.migration", surface: SurfaceSchema,
		why: "the path is inside a migrations directory",
		match: func(p, base, ext string) bool {
			// migrations/, db/migrate/, prisma/migrations/, and alembic's
			// versions/, which is the one that needs its parent named because
			// "versions" on its own is a directory anything could have.
			return segment(p, "migrations", "migrate") ||
				(segment(p, "versions") && segment(p, "alembic"))
		},
	},
	{
		name: "path.sql", surface: SurfaceSchema,
		why:   "it is a .sql file",
		match: func(p, base, ext string) bool { return ext == ".sql" },
	},
	{
		name: "path.schema_definition", surface: SurfaceSchema,
		why: "it is a schema definition a migration tool reads",
		match: func(p, base, ext string) bool {
			return ext == ".prisma" || ext == ".dbml" ||
				isBase(base, "schema.rb", "structure.sql", "atlas.hcl") ||
				(segment(p, "db") && isBase(base, "schema.rb"))
		},
	},
	{
		name: "path.docs", surface: SurfaceDocs,
		why: "it is prose",
		match: func(p, base, ext string) bool {
			return hasExt(ext, ".md", ".mdx", ".rst", ".txt", ".adoc") ||
				isBase(base, "license", "notice", "codeowners") ||
				segment(p, "docs", "doc", ".changes")
		},
	},
	{
		name: "path.test", surface: SurfaceTest,
		why: "it is part of your own test suite",
		match: func(p, base, ext string) bool {
			if segment(p, "test", "tests", "spec", "specs", "__tests__", "e2e",
				"testdata", "fixtures", "cypress", "playwright") {
				return true
			}
			return strings.HasSuffix(base, "_test.go") ||
				strings.HasSuffix(base, "_test.py") ||
				strings.HasPrefix(base, "test_") ||
				strings.HasSuffix(base, "_spec.rb") ||
				strings.Contains(base, ".test.") ||
				strings.Contains(base, ".spec.") ||
				isBase(base, "conftest.py")
		},
	},
	{
		name: "path.pipeline", surface: SurfacePipeline,
		why: "it configures continuous integration",
		match: func(p, base, ext string) bool {
			return strings.HasPrefix(p, ".github/") ||
				strings.HasPrefix(p, ".gitlab/") ||
				strings.HasPrefix(p, ".circleci/") ||
				strings.HasPrefix(p, ".buildkite/") ||
				isBase(base, ".gitlab-ci.yml", "jenkinsfile", "azure-pipelines.yml", ".travis.yml")
		},
	},
	{
		name: "path.infrastructure", surface: SurfaceInfrastructure,
		why: "it is infrastructure as code",
		match: func(p, base, ext string) bool {
			if hasExt(ext, ".tf", ".tfvars", ".bicep", ".hcl") {
				return true
			}
			if segment(p, "terraform", "infra", "infrastructure", "helm", "charts",
				"k8s", "kubernetes", "ansible", "deploy", "manifests") {
				return true
			}
			return isBase(base, "serverless.yml", "serverless.yaml", "pulumi.yaml",
				"cloudformation.yaml", "cloudformation.yml", "chart.yaml", "kustomization.yaml")
		},
	},
	{
		name: "path.build", surface: SurfaceBuild,
		why: "it decides how the application is built",
		match: func(p, base, ext string) bool {
			if strings.HasPrefix(base, "dockerfile") || strings.HasSuffix(base, ".dockerfile") {
				return true
			}
			if strings.HasPrefix(base, "docker-compose") || strings.HasPrefix(base, "compose.") {
				return true
			}
			return isBase(base, "makefile", "justfile", "procfile", ".dockerignore",
				"nixpacks.toml", "earthfile", "build.gradle", "build.gradle.kts",
				"pom.xml", "go.work", "rakefile", "webpack.config.js", "vite.config.ts",
				"vite.config.js", "tsconfig.json", "next.config.js", "next.config.mjs",
				"turbo.json", "nx.json") ||
				ext == ".csproj" || ext == ".mk"
		},
	},
	{
		name: "path.dependency", surface: SurfaceDependency,
		why: "it is a package manifest or a lockfile",
		match: func(p, base, ext string) bool {
			return isBase(base, "package.json", "package-lock.json", "yarn.lock",
				"pnpm-lock.yaml", "npm-shrinkwrap.json", "go.mod", "go.sum",
				"pipfile", "pipfile.lock", "poetry.lock", "pyproject.toml",
				"gemfile", "gemfile.lock", "cargo.toml", "cargo.lock",
				"composer.json", "composer.lock", "mix.exs", "mix.lock",
				"pubspec.yaml", "pubspec.lock", "gradle.lockfile") ||
				(strings.HasPrefix(base, "requirements") && ext == ".txt")
		},
	},
	{
		name: "path.asset", surface: SurfaceAsset,
		why: "it is something the application serves",
		match: func(p, base, ext string) bool {
			return hasExt(ext, ".css", ".scss", ".sass", ".less", ".html", ".htm",
				".hbs", ".ejs", ".erb", ".haml", ".pug", ".liquid", ".twig",
				".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico", ".avif",
				".woff", ".woff2", ".ttf", ".otf", ".eot",
				".mp3", ".mp4", ".webm", ".wav", ".pdf")
		},
	},
	{
		name: "path.code", surface: SurfaceCode,
		why: "it is application source",
		match: func(p, base, ext string) bool {
			return hasExt(ext, ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
				".py", ".rb", ".java", ".kt", ".kts", ".php", ".rs", ".cs", ".fs",
				".ex", ".exs", ".erl", ".scala", ".swift", ".m", ".mm",
				".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".clj", ".cljs",
				".vue", ".svelte", ".astro", ".dart", ".lua", ".pl", ".sh", ".bash",
				".ps1", ".proto", ".graphql", ".gql", ".r", ".jl", ".zig", ".nim")
		},
	},
	{
		name: "path.config", surface: SurfaceConfig,
		why: "it is configuration the application reads",
		match: func(p, base, ext string) bool {
			if strings.HasPrefix(base, ".env") || strings.HasSuffix(base, ".env") {
				return true
			}
			if hasExt(ext, ".yaml", ".yml", ".json", ".toml", ".ini", ".conf", ".cfg",
				".properties", ".xml", ".plist") {
				return true
			}
			return segment(p, "config", "configs", "settings") ||
				strings.HasPrefix(base, ".") // a dotfile with no extension is tooling configuration
		},
	},
}

// baseSurface returns the one surface a path falls into, the rule that decided
// it, and the sentence the fact carries.
//
// The manifest is consulted first, for two paths it names exactly and for the
// rules a project adds. A project's own rule beats the built in table, because
// the project knows its layout and this file is guessing at conventions.
func baseSurface(p string, m *schema.Manifest) (Surface, string, string) {
	p = path.Clean(strings.ReplaceAll(p, "\\", "/"))
	base := strings.ToLower(path.Base(p))
	ext := strings.ToLower(path.Ext(p))

	if isBase(base, "antifailure.yaml", "antifailure.yml") {
		return SurfaceManifest, "path.manifest", "it is the antifailure manifest"
	}
	if m != nil && m.Database != nil && m.Database.MaskingRules != "" {
		if path.Clean(m.Database.MaskingRules) == p {
			return SurfaceMasking, "manifest.masking_rules",
				"the manifest names it as the masking rules file"
		}
	}
	if s, rule, why, ok := manifestRule(p, m); ok {
		return s, rule, why
	}
	for _, r := range pathRules {
		if r.match(p, base, ext) {
			return r.surface, r.name, r.why
		}
	}
	return SurfaceUnknown, "", ""
}

// manifestRule applies the rules a project declared under change.rules.
//
// Longest matching pattern wins, so a project can say "everything under
// packages/ is code" and then say "packages/ops is infrastructure" without
// the order of the two mattering. Order deciding would mean appending a rule
// could silently change what an existing one does, which is the same reason
// the egress policy is decided by specificity.
func manifestRule(p string, m *schema.Manifest) (Surface, string, string, bool) {
	if m == nil || m.Change == nil {
		return "", "", "", false
	}
	best := -1
	var win schema.ChangeRule
	for _, r := range m.Change.Rules {
		if !globMatch(r.Path, p) {
			continue
		}
		if len(r.Path) > best {
			best, win = len(r.Path), r
		}
	}
	if best < 0 {
		return "", "", "", false
	}
	why := "the manifest's change rule " + win.Path + " claims it"
	if win.Note != "" {
		why = win.Note
	}
	return Surface(win.Surface), "manifest.change_rule", why, true
}

// globMatch matches a path against a pattern with * and ** .
//
// * does not cross a slash and ** does. Written here rather than taken from
// path.Match because path.Match has no **, and a monorepo rule that cannot say
// "anything under this directory" is a rule nobody can write.
func globMatch(pattern, p string) bool {
	return globParts(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

func globParts(pat, seg []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(seg); i++ {
				if globParts(pat[1:], seg[i:]) {
					return true
				}
			}
			return false
		}
		if len(seg) == 0 {
			return false
		}
		if !segMatch(pat[0], seg[0]) {
			return false
		}
		pat, seg = pat[1:], seg[1:]
	}
	return len(seg) == 0
}

// segMatch matches one path component against a pattern that may hold *.
func segMatch(pattern, s string) bool {
	ok, err := path.Match(pattern, s)
	return err == nil && ok
}

// serviceFacts attributes a file to the services whose declared path contains
// it.
//
// Longest path wins, so in a monorepo where one service is declared at "." and
// another at "services/billing", a file under services/billing belongs to
// billing alone. A tie attributes to all of them, sorted, because two services
// declaring the same directory is the project saying both are built from it.
func serviceFacts(f File, m *schema.Manifest) []Fact {
	if m == nil {
		return nil
	}
	p := path.Clean(f.Path)
	best := -1
	var names []string
	for _, s := range m.Services {
		dir := path.Clean(s.Path)
		if s.Path == "" {
			dir = "."
		}
		if !underDir(p, dir) {
			continue
		}
		n := len(dir)
		if dir == "." {
			n = 0
		}
		switch {
		case n > best:
			best, names = n, []string{s.Name}
		case n == best:
			names = append(names, s.Name)
		}
	}
	if best < 0 {
		return nil
	}
	sort.Strings(names)
	out := make([]Fact, 0, len(names))
	for _, n := range names {
		why := "the manifest declares the service " + n + " at this path"
		if best == 0 {
			// A service declared at the repository root claims every file,
			// which is true and says nothing about this one. Saying which is
			// the difference between a reader trusting the attribution and
			// wondering why a migration belongs to the web service.
			why = "the manifest declares the service " + n +
				" at the repository root, so every file in the repository is part of it"
		}
		out = append(out, Fact{
			Path: f.Path, Status: f.Status, Surface: SurfaceService, Subject: n,
			Rule: "manifest.service", Evidence: why,
		})
	}
	return out
}

func underDir(p, dir string) bool {
	if dir == "." || dir == "" {
		return true
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// urlPattern finds absolute http and https URLs in an added line.
var urlPattern = regexp.MustCompile(`https?://[A-Za-z0-9._~%\-]+(?::\d+)?[^\s"'` + "`" + `)\]}>,;]*`)

// notEndpoints are hosts that appear in source constantly and are never a call
// the application makes. Skipping them is the difference between a report with
// three hosts in it and a report with forty.
var notEndpoints = map[string]bool{
	"localhost": true, "127.0.0.1": true, "0.0.0.0": true, "::1": true,
	"example.com": true, "example.org": true, "example.net": true,
	"www.example.com": true, "www.w3.org": true, "w3.org": true,
	"schema.org": true, "json-schema.org": true, "spdx.org": true,
	"creativecommons.org": true, "opensource.org": true,
	"go.dev": true, "golang.org": true, "pkg.go.dev": true,
}

// hostFacts reports the outbound hosts an added line names, and what the
// firewall would do about each.
//
// The decision comes from internal/policy, the same pure function the sidecar
// asks per request and af net explain asks about a hypothetical. Asking the
// real engine rather than reimplementing the match is the whole reason this is
// worth saying: a second implementation would eventually disagree with the one
// that decides real traffic, and the one that disagreed would be this one.
func hostFacts(f File, engine *policy.Engine) []Fact {
	if f.Binary || engine == nil {
		return nil
	}
	seen := map[string]int{}
	var order []string
	for _, line := range f.AddedLines {
		if len(order) >= MaxHostsPerFile {
			break
		}
		for _, raw := range urlPattern.FindAllString(line.Text, -1) {
			u, err := url.Parse(raw)
			if err != nil || u.Hostname() == "" {
				continue
			}
			host := strings.ToLower(u.Hostname())
			if notEndpoints[host] || strings.HasSuffix(host, ".local") ||
				strings.HasSuffix(host, ".example.com") || !strings.Contains(host, ".") {
				continue
			}
			if _, ok := seen[host]; ok {
				continue
			}
			seen[host] = line.N
			order = append(order, host)
			if len(order) >= MaxHostsPerFile {
				break
			}
		}
	}
	sort.Strings(order)

	out := make([]Fact, 0, len(order))
	for _, host := range order {
		d := engine.Evaluate(policy.Request{Host: host, Method: "GET", Path: "/", TLS: true})
		var why string
		if d.Matched() {
			why = "an added line names " + host + ", which the manifest routes to mode " + string(d.Mode)
		} else {
			why = "an added line names " + host + ", which no egress rule matches, so the default of " +
				string(d.Mode) + " applies"
		}
		out = append(out, Fact{
			Path: f.Path, Status: f.Status, Surface: SurfaceEgress, Subject: host,
			Rule: "content.outbound_host", Evidence: why, Line: seen[host],
		})
	}
	return out
}
