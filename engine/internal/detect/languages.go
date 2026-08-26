package detect

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// PythonAnalyzer recognises Django, FastAPI, Flask, and their workers.
type PythonAnalyzer struct{}

// Name identifies the analyzer.
func (*PythonAnalyzer) Name() string { return "python" }

// pythonDepFiles are every place a Python project declares dependencies. All
// of them are read, because a project routinely has more than one and the
// interesting dependency can be in any of them.
var pythonDepFiles = []string{
	"requirements.txt", "requirements-prod.txt", "requirements/base.txt",
	"pyproject.toml", "Pipfile", "setup.py", "setup.cfg", "uv.lock", "poetry.lock",
}

// Analyze reports Python services.
func (a *PythonAnalyzer) Analyze(_ context.Context, r *Repo) ([]Finding, error) {
	var out []Finding

	// Django announces itself with manage.py, which is both the strongest
	// signal and the thing that runs migrations.
	for _, p := range r.Glob("manage.py") {
		dir := dirOf(p)
		body, _ := r.ReadString(p)
		if !strings.Contains(body, "django") && !strings.Contains(body, "DJANGO_SETTINGS_MODULE") {
			continue
		}
		name := sanitizeServiceName(baseNameFor(dir, r))
		out = append(out,
			Finding{Kind: KindFramework, Subject: name, Value: "django",
				Confidence: High, Evidence: p,
				Detail: fmt.Sprintf("%s configures DJANGO_SETTINGS_MODULE.", p),
				Extra:  map[string]string{"dir": dir}},
			Finding{Kind: KindService, Subject: name, Value: "web",
				Confidence: High, Evidence: p,
				Extra: map[string]string{"dir": dir, "framework": "django"}},
			Finding{Kind: KindPort, Subject: name, Value: "8000",
				Confidence: Medium, Evidence: p,
				Detail: "Django's development server listens on 8000; a production server usually keeps it."},
			Finding{Kind: KindMigration, Subject: name, Value: "python manage.py migrate",
				Confidence: High, Evidence: p,
				Detail: "Django migrations are applied with manage.py."},
		)
		if hasPythonDep(r, dir, "gunicorn") {
			out = append(out, Finding{
				Kind: KindCommand, Subject: name,
				Value:      "gunicorn --bind 0.0.0.0:8000 " + djangoWSGIModule(r, dir) + ".wsgi:application",
				Confidence: Medium, Evidence: p,
				Detail: "The project depends on gunicorn.",
			})
		}
	}

	// FastAPI and Flask are recognised from their dependency plus the module
	// that constructs the application, which is what the server command needs.
	for _, dir := range pythonProjectDirs(r) {
		if hasPythonDep(r, dir, "fastapi") {
			name := sanitizeServiceName(baseNameFor(dir, r))
			mod := findPythonAppModule(r, dir, `FastAPI\s*\(`)
			cmd := "uvicorn " + orDefault(mod, "main:app") + " --host 0.0.0.0 --port 8000"
			out = append(out,
				Finding{Kind: KindFramework, Subject: name, Value: "fastapi",
					Confidence: High, Evidence: pythonEvidence(r, dir),
					Extra: map[string]string{"dir": dir}},
				Finding{Kind: KindService, Subject: name, Value: "web",
					Confidence: High, Evidence: pythonEvidence(r, dir),
					Extra: map[string]string{"dir": dir, "framework": "fastapi"}},
				Finding{Kind: KindPort, Subject: name, Value: "8000",
					Confidence: Medium, Evidence: pythonEvidence(r, dir),
					Detail: "uvicorn listens on 8000 by default."},
				Finding{Kind: KindCommand, Subject: name, Value: cmd,
					Confidence: confidenceIf(mod != "", Medium, Low),
					Evidence:   pythonEvidence(r, dir)},
			)
			continue
		}
		if hasPythonDep(r, dir, "flask") {
			name := sanitizeServiceName(baseNameFor(dir, r))
			mod := findPythonAppModule(r, dir, `Flask\s*\(`)
			out = append(out,
				Finding{Kind: KindFramework, Subject: name, Value: "flask",
					Confidence: High, Evidence: pythonEvidence(r, dir),
					Extra: map[string]string{"dir": dir}},
				Finding{Kind: KindService, Subject: name, Value: "web",
					Confidence: High, Evidence: pythonEvidence(r, dir),
					Extra: map[string]string{"dir": dir, "framework": "flask"}},
				Finding{Kind: KindPort, Subject: name, Value: "5000",
					Confidence: Medium, Evidence: pythonEvidence(r, dir),
					Detail: "Flask listens on 5000 by default."},
				Finding{Kind: KindCommand, Subject: name,
					Value:      "gunicorn --bind 0.0.0.0:5000 " + orDefault(mod, "app:app"),
					Confidence: Low, Evidence: pythonEvidence(r, dir)},
			)
		}
	}

	// Celery workers, which are a service in their own right and are the
	// reason a Django application's background jobs run at all.
	for _, dir := range pythonProjectDirs(r) {
		if !hasPythonDep(r, dir, "celery") {
			continue
		}
		app := findPythonAppModule(r, dir, `Celery\s*\(`)
		name := sanitizeServiceName(baseNameFor(dir, r)) + "-worker"
		out = append(out, Finding{
			Kind: KindWorker, Subject: name,
			Value:      "celery -A " + orDefault(strings.SplitN(app, ":", 2)[0], "app") + " worker --loglevel=info",
			Confidence: confidenceIf(app != "", Medium, Low),
			Evidence:   pythonEvidence(r, dir),
			Detail:     "The project depends on celery.",
			Extra:      map[string]string{"dir": dir, "queue": "Celery"},
		})
	}
	return out, nil
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// pythonProjectDirs returns directories holding a Python dependency file.
func pythonProjectDirs(r *Repo) []string {
	seen := map[string]bool{}
	var out []string
	for _, name := range pythonDepFiles {
		for _, p := range r.Glob(path.Base(name)) {
			d := dirOf(p)
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out
}

func pythonEvidence(r *Repo, dir string) string {
	for _, name := range []string{"pyproject.toml", "requirements.txt", "Pipfile", "setup.py"} {
		p := joinDir(dir, name)
		if r.Exists(p) {
			return p
		}
	}
	return joinDir(dir, "requirements.txt")
}

// hasPythonDep reports whether a dependency appears in any declaration file in
// the directory. The check is on a word boundary so that "fastapi-users" does
// not count as "fastapi" and, more importantly, so that "flask" matches
// "Flask==3.0.0" and "flask = "^3.0"" alike.
func hasPythonDep(r *Repo, dir, dep string) bool {
	re := regexp.MustCompile(`(?im)^\s*["']?` + regexp.QuoteMeta(dep) + `["']?\s*(==|>=|<=|~=|=|\[|$|,|")`)
	for _, name := range pythonDepFiles {
		body, ok := r.ReadString(joinDir(dir, name))
		if !ok {
			continue
		}
		if re.MatchString(body) {
			return true
		}
		// pyproject dependency arrays put the name inside a quoted string on a
		// line with other punctuation.
		if regexp.MustCompile(`["']` + regexp.QuoteMeta(dep) + `["'\[><=~,\s]`).MatchString(body) {
			return true
		}
	}
	return false
}

// findPythonAppModule looks for the module and attribute that construct the
// application, so that the server command points at something real.
func findPythonAppModule(r *Repo, dir, constructor string) string {
	re := regexp.MustCompile(`(?m)^\s*(\w+)\s*=\s*` + constructor)
	candidates := []string{"main.py", "app.py", "asgi.py", "wsgi.py", "server.py",
		"src/main.py", "app/main.py", "api/main.py"}
	for _, c := range candidates {
		p := joinDir(dir, c)
		body, ok := r.ReadString(p)
		if !ok {
			continue
		}
		if m := re.FindStringSubmatch(body); m != nil {
			module := strings.TrimSuffix(strings.TrimPrefix(c, dir+"/"), ".py")
			module = strings.ReplaceAll(module, "/", ".")
			return module + ":" + m[1]
		}
	}
	return ""
}

// djangoWSGIModule finds the package holding settings.py, which is what the
// wsgi entry point is named after.
func djangoWSGIModule(r *Repo, dir string) string {
	for _, p := range r.Glob("wsgi.py") {
		if dir == "" || strings.HasPrefix(p, dir+"/") {
			return path.Base(path.Dir(p))
		}
	}
	for _, p := range r.Glob("settings.py") {
		if dir == "" || strings.HasPrefix(p, dir+"/") {
			return path.Base(path.Dir(p))
		}
	}
	return "config"
}

// GoAnalyzer recognises Go services.
type GoAnalyzer struct{}

// Name identifies the analyzer.
func (*GoAnalyzer) Name() string { return "go" }

var goPortRe = regexp.MustCompile(`(?:ListenAndServe|Listen)\s*\(\s*["'` + "`" + `]:(\d{2,5})`)

// Analyze reports Go services, one per main package under cmd or at the root.
func (a *GoAnalyzer) Analyze(_ context.Context, r *Repo) ([]Finding, error) {
	var out []Finding
	for _, mod := range r.Glob("go.mod") {
		dir := dirOf(mod)
		body, _ := r.ReadString(mod)
		modulePath := goModulePath(body)

		mains := goMainPackages(r, dir)
		if len(mains) == 0 {
			continue
		}
		for _, mainDir := range mains {
			name := sanitizeServiceName(baseNameFor(mainDir, r))
			if name == "" || name == path.Base(dir) {
				name = sanitizeServiceName(path.Base(modulePath))
			}
			out = append(out,
				Finding{Kind: KindFramework, Subject: name, Value: "go",
					Confidence: High, Evidence: mod,
					Detail: fmt.Sprintf("%s declares module %s.", mod, modulePath),
					Extra:  map[string]string{"dir": mainDir}},
				Finding{Kind: KindService, Subject: name, Value: "web",
					Confidence: Medium, Evidence: mod,
					Extra: map[string]string{"dir": mainDir, "framework": "go"}},
				Finding{Kind: KindCommand, Subject: name,
					Value:      "./" + path.Base(mainDir),
					Confidence: Medium, Evidence: mod},
			)
			if port := goListenPort(r, mainDir); port != 0 {
				out = append(out, Finding{
					Kind: KindPort, Subject: name, Value: strconv.Itoa(port),
					Confidence: High, Evidence: mainDir,
					Detail: fmt.Sprintf("The listener binds port %d.", port),
				})
			}
		}
	}
	return out, nil
}

func goModulePath(body string) string {
	for _, l := range strings.Split(body, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(l), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// goMainPackages finds directories declaring package main, preferring cmd
// subdirectories, which is where a Go project puts its binaries.
func goMainPackages(r *Repo, modDir string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range r.WithExtension(".go") {
		if modDir != "" && !strings.HasPrefix(p, modDir+"/") && dirOf(p) != modDir {
			continue
		}
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		body, ok := r.ReadString(p)
		if !ok || !regexp.MustCompile(`(?m)^package\s+main\s*$`).MatchString(body) {
			continue
		}
		d := dirOf(p)
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

func goListenPort(r *Repo, dir string) int {
	for _, p := range r.WithExtension(".go") {
		if dirOf(p) != dir && !strings.HasPrefix(p, dir+"/") {
			continue
		}
		body, ok := r.ReadString(p)
		if !ok {
			continue
		}
		if m := goPortRe.FindStringSubmatch(body); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n < 65536 {
				return n
			}
		}
	}
	return 0
}

// RubyAnalyzer recognises Rails applications and Sidekiq workers.
type RubyAnalyzer struct{}

// Name identifies the analyzer.
func (*RubyAnalyzer) Name() string { return "ruby" }

// Analyze reports Ruby services.
func (a *RubyAnalyzer) Analyze(_ context.Context, r *Repo) ([]Finding, error) {
	var out []Finding
	for _, gemfile := range r.Glob("Gemfile") {
		dir := dirOf(gemfile)
		body, ok := r.ReadString(gemfile)
		if !ok {
			continue
		}
		name := sanitizeServiceName(baseNameFor(dir, r))

		if hasGem(body, "rails") {
			out = append(out,
				Finding{Kind: KindFramework, Subject: name, Value: "rails",
					Confidence: High, Evidence: gemfile,
					Detail: fmt.Sprintf("%s requires the rails gem.", gemfile),
					Extra:  map[string]string{"dir": dir}},
				Finding{Kind: KindService, Subject: name, Value: "web",
					Confidence: High, Evidence: gemfile,
					Extra: map[string]string{"dir": dir, "framework": "rails"}},
				Finding{Kind: KindPort, Subject: name, Value: "3000",
					Confidence: Medium, Evidence: gemfile,
					Detail: "Rails listens on 3000 by default."},
				Finding{Kind: KindMigration, Subject: name, Value: "bin/rails db:migrate",
					Confidence: High, Evidence: gemfile},
				Finding{Kind: KindCommand, Subject: name,
					Value:      "bin/rails server -b 0.0.0.0 -p 3000",
					Confidence: Medium, Evidence: gemfile},
			)
		}
		if hasGem(body, "sidekiq") {
			out = append(out, Finding{
				Kind: KindWorker, Subject: name + "-worker", Value: "bundle exec sidekiq",
				Confidence: High, Evidence: gemfile,
				Detail: fmt.Sprintf("%s requires the sidekiq gem.", gemfile),
				Extra:  map[string]string{"dir": dir, "queue": "Sidekiq"},
			})
		}
	}
	return out, nil
}

func hasGem(gemfile, gem string) bool {
	return regexp.MustCompile(`(?m)^\s*gem\s+["']` + regexp.QuoteMeta(gem) + `["']`).MatchString(gemfile)
}

// dirOf returns a path's directory, with the repository root as the empty
// string rather than a dot, which is what the manifest expects.
func dirOf(p string) string {
	d := path.Dir(p)
	if d == "." {
		return ""
	}
	return d
}

// joinDir joins a directory and a name, treating the empty directory as root.
func joinDir(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// baseNameFor names a service after its directory, or after the repository
// when it is at the root.
func baseNameFor(dir string, r *Repo) string {
	if dir == "" {
		return path.Base(r.Root())
	}
	return path.Base(dir)
}
