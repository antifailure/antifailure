package detect

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// packageJSON is the subset of package.json detection reads. Decoding a subset
// rather than a map keeps the parse cheap and makes the fields that matter
// explicit.
type packageJSON struct {
	Name            string            `json:"name"`
	Private         bool              `json:"private"`
	Workspaces      json.RawMessage   `json:"workspaces"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Engines         map[string]string `json:"engines"`
	PackageManager  string            `json:"packageManager"`
	Main            string            `json:"main"`
	Type            string            `json:"type"`
}

func (p packageJSON) allDeps() map[string]string {
	out := make(map[string]string, len(p.Dependencies)+len(p.DevDependencies))
	for k, v := range p.Dependencies {
		out[k] = v
	}
	for k, v := range p.DevDependencies {
		out[k] = v
	}
	return out
}

// WorkspaceAnalyzer finds the package manager and the workspace layout.
type WorkspaceAnalyzer struct{}

// Name identifies the analyzer.
func (*WorkspaceAnalyzer) Name() string { return "workspace" }

// Analyze reports the package manager and any workspace roots.
func (a *WorkspaceAnalyzer) Analyze(_ context.Context, r *Repo) ([]Finding, error) {
	var out []Finding

	// The lockfile is the strongest signal, because it is the one file that
	// only the real package manager writes.
	lockfiles := []struct{ file, mgr string }{
		{"pnpm-lock.yaml", "pnpm"},
		{"package-lock.json", "npm"},
		{"yarn.lock", "yarn"},
		{"bun.lockb", "bun"},
		{"bun.lock", "bun"},
	}
	for _, lf := range lockfiles {
		if r.Exists(lf.file) {
			out = append(out, Finding{
				Kind: KindPackageMgr, Subject: "node", Value: lf.mgr,
				Confidence: High, Evidence: lf.file,
				Detail: fmt.Sprintf("The lockfile %s is present.", lf.file),
			})
		}
	}
	if len(out) == 0 && r.Exists("package.json") {
		// No lockfile. The packageManager field is next best, then npm, which
		// is what the ecosystem assumes.
		var pkg packageJSON
		if b, ok := r.Read("package.json"); ok && json.Unmarshal(b, &pkg) == nil {
			mgr := "npm"
			conf := Low
			if pkg.PackageManager != "" {
				mgr = strings.SplitN(pkg.PackageManager, "@", 2)[0]
				conf = Medium
			}
			out = append(out, Finding{
				Kind: KindPackageMgr, Subject: "node", Value: mgr,
				Confidence: conf, Evidence: "package.json",
				Detail: "No lockfile was found, so the package manager is inferred.",
			})
		}
	}

	// Workspace roots. Each member is a candidate service, which is what makes
	// a monorepo produce several services rather than one.
	if b, ok := r.Read("pnpm-workspace.yaml"); ok {
		for _, pattern := range parsePnpmWorkspace(string(b)) {
			out = append(out, Finding{
				Kind: KindWorkspace, Subject: pattern, Value: "pnpm",
				Confidence: High, Evidence: "pnpm-workspace.yaml",
			})
		}
	}
	if b, ok := r.Read("package.json"); ok {
		var pkg packageJSON
		if json.Unmarshal(b, &pkg) == nil && len(pkg.Workspaces) > 0 {
			for _, pattern := range parseWorkspacesField(pkg.Workspaces) {
				out = append(out, Finding{
					Kind: KindWorkspace, Subject: pattern, Value: "npm",
					Confidence: High, Evidence: "package.json",
				})
			}
		}
	}
	for _, f := range []string{"turbo.json", "nx.json", "lerna.json", "rush.json"} {
		if r.Exists(f) {
			tool := strings.TrimSuffix(f, ".json")
			out = append(out, Finding{
				Kind: KindNote, Subject: "monorepo-tool", Value: tool,
				Confidence: High, Evidence: f,
				Detail: fmt.Sprintf("This is a %s monorepo.", tool),
			})
		}
	}
	return out, nil
}

// parsePnpmWorkspace reads the packages list without a YAML dependency, which
// keeps this analyzer to one small parser rather than a general one.
func parsePnpmWorkspace(s string) []string {
	var out []string
	inPackages := false
	for _, lineText := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(lineText)
		if strings.HasPrefix(trimmed, "packages:") {
			inPackages = true
			continue
		}
		if !inPackages {
			continue
		}
		if !strings.HasPrefix(trimmed, "-") {
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				inPackages = false
			}
			continue
		}
		v := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "-")), `"'`)
		if v != "" {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// parseWorkspacesField handles both spellings: an array, and an object with a
// packages key, which yarn uses.
func parseWorkspacesField(raw json.RawMessage) []string {
	var asArray []string
	if json.Unmarshal(raw, &asArray) == nil {
		sort.Strings(asArray)
		return asArray
	}
	var asObject struct {
		Packages []string `json:"packages"`
	}
	if json.Unmarshal(raw, &asObject) == nil {
		sort.Strings(asObject.Packages)
		return asObject.Packages
	}
	return nil
}

// NodeAnalyzer recognises Node applications and their frameworks.
type NodeAnalyzer struct{}

// Name identifies the analyzer.
func (*NodeAnalyzer) Name() string { return "node" }

// nodeFramework describes how to recognise a framework and what it implies.
type nodeFramework struct {
	dep         string // the package that identifies it
	name        string // what it is called in findings
	port        int    // the port it listens on by default
	startScript string // the script that runs it in production
	kind        string // what the service is
}

// nodeFrameworks is ordered from most specific to least, because a Next.js
// application also depends on react and a NestJS one also depends on express.
// Taking the first match is what keeps a Next.js app from being reported as a
// bare React build.
var nodeFrameworks = []nodeFramework{
	{dep: "next", name: "nextjs", port: 3000, startScript: "start", kind: "web"},
	{dep: "@remix-run/serve", name: "remix", port: 3000, startScript: "start", kind: "web"},
	{dep: "@remix-run/node", name: "remix", port: 3000, startScript: "start", kind: "web"},
	{dep: "@nestjs/core", name: "nestjs", port: 3000, startScript: "start:prod", kind: "web"},
	{dep: "nuxt", name: "nuxt", port: 3000, startScript: "start", kind: "web"},
	{dep: "@sveltejs/kit", name: "sveltekit", port: 3000, startScript: "start", kind: "web"},
	{dep: "astro", name: "astro", port: 4321, startScript: "start", kind: "web"},
	{dep: "@angular/core", name: "angular", port: 4200, startScript: "start", kind: "web"},
	{dep: "fastify", name: "fastify", port: 3000, startScript: "start", kind: "web"},
	{dep: "koa", name: "koa", port: 3000, startScript: "start", kind: "web"},
	{dep: "hono", name: "hono", port: 3000, startScript: "start", kind: "web"},
	{dep: "express", name: "express", port: 3000, startScript: "start", kind: "web"},
	{dep: "vite", name: "vite", port: 5173, startScript: "preview", kind: "web"},
}

// nodeWorkerDeps identify a background worker rather than a web service.
var nodeWorkerDeps = []struct{ dep, label string }{
	{"bullmq", "BullMQ"},
	{"bull", "Bull"},
	{"bee-queue", "Bee-Queue"},
	{"agenda", "Agenda"},
	{"graphile-worker", "Graphile Worker"},
	{"inngest", "Inngest"},
	{"@trigger.dev/sdk", "Trigger.dev"},
	{"pg-boss", "pg-boss"},
}

// Analyze reports one candidate service per package.json that looks runnable.
func (a *NodeAnalyzer) Analyze(_ context.Context, r *Repo) ([]Finding, error) {
	var out []Finding
	for _, p := range r.Glob("package.json") {
		dir := path.Dir(p)
		if dir == "." {
			dir = ""
		}
		b, ok := r.Read(p)
		if !ok {
			continue
		}
		var pkg packageJSON
		if err := json.Unmarshal(b, &pkg); err != nil {
			out = append(out, Finding{
				Kind: KindNote, Subject: p, Confidence: High, Evidence: p,
				Detail: fmt.Sprintf("%s is not valid JSON, so it was skipped.", p),
			})
			continue
		}
		deps := pkg.allDeps()

		// A package with no scripts and no dependencies is a stub or a types
		// only package, not something to run.
		if len(pkg.Scripts) == 0 && len(deps) == 0 {
			continue
		}

		name := serviceNameFor(pkg.Name, dir, r)
		matched := false
		for _, fw := range nodeFrameworks {
			if _, has := deps[fw.dep]; !has {
				continue
			}
			matched = true
			out = append(out, Finding{
				Kind: KindFramework, Subject: name, Value: fw.name,
				Confidence: High, Evidence: p,
				Detail: fmt.Sprintf("%s depends on %s.", p, fw.dep),
				Extra:  map[string]string{"dir": dir},
			})
			out = append(out, Finding{
				Kind: KindService, Subject: name, Value: fw.kind,
				Confidence: High, Evidence: p,
				Extra: map[string]string{"dir": dir, "framework": fw.name},
			})
			port, portConf, portWhy := nodePort(pkg, fw)
			out = append(out, Finding{
				Kind: KindPort, Subject: name, Value: strconv.Itoa(port),
				Confidence: portConf, Evidence: p, Detail: portWhy,
			})
			if cmd, conf := nodeStartCommand(pkg, fw, r); cmd != "" {
				out = append(out, Finding{
					Kind: KindCommand, Subject: name, Value: cmd,
					Confidence: conf, Evidence: p,
				})
			}
			break
		}

		// Workers are additive: an application often has a web service and a
		// worker in the same package.
		for _, w := range nodeWorkerDeps {
			if _, has := deps[w.dep]; !has {
				continue
			}
			workerName := name + "-worker"
			if !matched {
				workerName = name
			}
			cmd := findWorkerScript(pkg)
			out = append(out, Finding{
				Kind: KindWorker, Subject: workerName, Value: cmd,
				Confidence: confidenceIf(cmd != "", High, Low), Evidence: p,
				Detail: fmt.Sprintf("%s depends on %s.", p, w.label),
				Extra:  map[string]string{"dir": dir, "queue": w.label},
			})
			matched = true
			break
		}

		if !matched && dir == "" && hasStartScript(pkg) {
			// A root package with a start script and no recognised framework
			// is still runnable. Reporting it at low confidence lets af init
			// ask, rather than silently producing a manifest with no services.
			//
			// The command has to come with it. Without one the merger sees a
			// candidate with nothing but a kind and discards it, which turns
			// "we are not sure about the port" into "there is no application
			// here", and those are very different answers.
			out = append(out,
				Finding{Kind: KindService, Subject: name, Value: "web",
					Confidence: Low, Evidence: p,
					Detail: "The package has a start script but no framework was recognised.",
					Extra:  map[string]string{"dir": dir}},
				Finding{Kind: KindCommand, Subject: name, Value: startCommandPrefix(r) + " start",
					Confidence: Medium, Evidence: p},
			)
		}

		if v, ok := pkg.Engines["node"]; ok {
			out = append(out, Finding{
				Kind: KindNote, Subject: name + ".node", Value: v,
				Confidence: High, Evidence: p,
				Detail: fmt.Sprintf("%s requires Node %s.", p, v),
			})
		}
	}
	return out, nil
}

func confidenceIf(cond bool, yes, no Confidence) Confidence {
	if cond {
		return yes
	}
	return no
}

func hasStartScript(pkg packageJSON) bool {
	_, ok := pkg.Scripts["start"]
	return ok
}

// portFlagRe finds a port in a script command, which is the only place an
// application states the port it will actually bind.
var portFlagRe = regexp.MustCompile(`(?:--port[= ]|-p |PORT=)(\d{2,5})\b`)

func nodePort(pkg packageJSON, fw nodeFramework) (int, Confidence, string) {
	for _, key := range []string{fw.startScript, "start", "dev", "serve"} {
		script, ok := pkg.Scripts[key]
		if !ok {
			continue
		}
		if m := portFlagRe.FindStringSubmatch(script); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n < 65536 {
				return n, High, fmt.Sprintf("The %q script binds port %d.", key, n)
			}
		}
	}
	return fw.port, Medium, fmt.Sprintf("%s listens on %d by default.", fw.name, fw.port)
}

// nodeStartCommand picks the command that runs the service in production.
func nodeStartCommand(pkg packageJSON, fw nodeFramework, r *Repo) (string, Confidence) {
	mgr := startCommandPrefix(r)
	switch {
	case r.Exists("pnpm-lock.yaml"):
		mgr = "pnpm"
	case r.Exists("yarn.lock"):
		mgr = "yarn"
	case r.Exists("bun.lockb"), r.Exists("bun.lock"):
		mgr = "bun run"
	}
	for _, key := range []string{fw.startScript, "start"} {
		if _, ok := pkg.Scripts[key]; ok {
			return mgr + " " + key, High
		}
	}
	// Next.js in standalone output runs its own server file, which is what a
	// container image ships rather than the whole toolchain.
	if fw.name == "nextjs" {
		return "node server.js", Low
	}
	if pkg.Main != "" {
		return "node " + pkg.Main, Low
	}
	return "", Low
}

// startCommandPrefix picks the invocation the repository's package manager
// uses, from the lockfile that only that manager writes.
func startCommandPrefix(r *Repo) string {
	switch {
	case r.Exists("pnpm-lock.yaml"):
		return "pnpm"
	case r.Exists("yarn.lock"):
		return "yarn"
	case r.Exists("bun.lockb"), r.Exists("bun.lock"):
		return "bun run"
	default:
		return "npm run"
	}
}

// findWorkerScript looks for the script that starts a queue consumer.
func findWorkerScript(pkg packageJSON) string {
	for _, key := range []string{"worker", "start:worker", "worker:start", "consumer", "jobs"} {
		if _, ok := pkg.Scripts[key]; ok {
			return "npm run " + key
		}
	}
	return ""
}

// serviceNameFor derives a service name from the package name or its directory.
//
// The package name wins when it is meaningful. A scoped name such as @acme/web
// becomes web, because the scope is the same for every package in the monorepo
// and adds nothing to a hostname.
func serviceNameFor(pkgName, dir string, r *Repo) string {
	candidate := pkgName
	if i := strings.LastIndexByte(candidate, '/'); i >= 0 {
		candidate = candidate[i+1:]
	}
	if candidate == "" || candidate == "root" || candidate == "monorepo" {
		if dir != "" {
			candidate = path.Base(dir)
		} else {
			candidate = path.Base(r.Root())
		}
	}
	if dir != "" && (candidate == "src" || candidate == "app" || candidate == "server") {
		// A directory named src tells the reader nothing. Its parent usually
		// does.
		if parent := path.Base(path.Dir(dir)); parent != "." && parent != "" {
			candidate = parent
		}
	}
	return sanitizeServiceName(candidate)
}

func sanitizeServiceName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.' || r == ' ' || r == '@' || r == '/':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	if out == "" {
		return "app"
	}
	return out
}
