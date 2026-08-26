package build

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A buildpack writes a Dockerfile for a service that does not have one.
//
// Most repositories do not. Requiring one before af up works would mean the
// first thing Antifailure asks of somebody is to write the file they came here
// to avoid writing, so the generated Dockerfile has to be good enough to run
// the real application, not a demonstration.
//
// Three properties matter more than cleverness. The dependency install is its
// own layer keyed on the lockfile, so editing a source file does not reinstall
// the world. The lockfile is honoured exactly, because a preview environment
// that resolves different versions than production is testing a different
// application. And the runtime stage carries no build toolchain, because the
// image is what the agents drive and it should be the thing that ships.

// Buildpack is a generated build for one service.
type Buildpack struct {
	// Name identifies the buildpack, for output and for tests.
	Name string
	// Dockerfile is the generated content.
	Dockerfile string
	// Why explains, in one or two sentences, what was detected and what was
	// decided from it. It is printed by af build explain and written into the
	// manifest comment by af init.
	Why string
	// Port is the port the generated image listens on, when the buildpack can
	// tell. Zero means it could not.
	Port int
}

// fileSet is what a buildpack reads: a lookup for whether a path exists in the
// build context and a reader for its content.
//
// It is an interface rather than a directory so that detection runs against
// the context that will actually be sent to the daemon. Reading the disk
// separately would let a file excluded by .dockerignore decide the build, and
// then be missing when the build runs.
type fileSet interface {
	Has(path string) bool
	Read(path string) ([]byte, bool)
}

// DetectBuildpack picks a buildpack for a service directory.
//
// dir is relative to the context root and slash separated, or empty for the
// root. A service in a monorepo package is built with the whole repository as
// context, because its dependencies live in the root lockfile.
func DetectBuildpack(fs fileSet, dir, command string, port int) (*Buildpack, bool) {
	for _, d := range buildpacks {
		if bp, ok := d(fs, dir, command, port); ok {
			return bp, true
		}
	}
	return nil, false
}

type detector func(fs fileSet, dir, command string, port int) (*Buildpack, bool)

// Ordered by how specific the evidence is. Go before Node, because a Go
// service with a small frontend has both a go.mod and a package.json, and the
// go.mod is the one that says what the service is.
var buildpacks = []detector{goBuildpack, nodeBuildpack, pythonBuildpack, rubyBuildpack}

func at(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return strings.TrimSuffix(dir, "/") + "/" + name
}

// ---------------------------------------------------------------- Node

type nodeManager struct {
	name        string
	lockfile    string
	installArgs string
	corepack    bool
}

// Ordered so that a repository with several lockfiles picks the one its
// developers actually use. pnpm and yarn write their own lockfile and also
// leave a package-lock.json behind often enough that checking npm first would
// pick the wrong one and install different versions than production.
var nodeManagers = []nodeManager{
	{name: "pnpm", lockfile: "pnpm-lock.yaml", installArgs: "pnpm install --frozen-lockfile", corepack: true},
	{name: "yarn", lockfile: "yarn.lock", installArgs: "yarn install --frozen-lockfile", corepack: true},
	{name: "bun", lockfile: "bun.lockb", installArgs: "bun install --frozen-lockfile"},
	{name: "npm", lockfile: "package-lock.json", installArgs: "npm ci"},
}

var nvmrcVersion = regexp.MustCompile(`^v?(\d+)`)

func nodeBuildpack(fs fileSet, dir, command string, port int) (*Buildpack, bool) {
	pkgPath := at(dir, "package.json")
	raw, ok := fs.Read(pkgPath)
	if !ok {
		return nil, false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
		PackageManager string `json:"packageManager"`
	}
	// A package.json that does not parse is still evidence this is a Node
	// service. Refusing here would send the user to the buildpack that fits
	// worst rather than to the error that says what is wrong with their file.
	_ = json.Unmarshal(raw, &pkg)

	// The fallback when nothing is found. npm install rather than npm ci: ci
	// refuses to run at all without a lockfile, so a Dockerfile that reaches
	// for it here cannot succeed, and every repository without a lockfile
	// would get a build that fails on a message about npm ci's usage rather
	// than about anything the developer did.
	mgr := nodeManager{name: "npm", installArgs: "npm install --no-audit --no-fund"}
	found := false
	// The lockfile at the repository root governs a monorepo package, so both
	// places are checked and the nearer one wins.
	for _, m := range nodeManagers {
		if fs.Has(at(dir, m.lockfile)) || fs.Has(m.lockfile) {
			mgr, found = m, true
			break
		}
	}
	if name, _, cut := strings.Cut(pkg.PackageManager, "@"); cut {
		for _, m := range nodeManagers {
			if m.name == name {
				mgr, found = m, true
				break
			}
		}
	}

	major := nodeMajor(fs, dir, pkg.Engines.Node)
	hasBuild := pkg.Scripts["build"] != ""
	start := command
	if start == "" {
		switch {
		case pkg.Scripts["start"] != "":
			start = mgr.name + " run start"
		default:
			start = "node index.js"
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Generated by Antifailure. https://antifailure.dev/docs/guides/builds\n")
	fmt.Fprintf(&b, "FROM node:%s-bookworm-slim AS deps\n", major)
	b.WriteString("WORKDIR /app\n")
	if mgr.corepack {
		// Pinned by package.json's packageManager field when there is one, so
		// the version that installs is the version the repository declares.
		b.WriteString("RUN corepack enable\n")
	}
	if mgr.name == "bun" {
		b.WriteString("RUN npm install -g bun\n")
	}
	// Only the files the install reads, so a source edit does not invalidate
	// the dependency layer. This is the difference between a five second
	// rebuild and a three minute one.
	b.WriteString("COPY " + strings.Join(nodeInstallInputs(fs, dir, mgr), " ") + " ./\n")
	// The directory has to exist even when the install created nothing,
	// because the runtime stage copies it unconditionally and COPY --from
	// fails on a path that is not there. An application with no dependencies
	// yet is a real thing, and it is usually the first one somebody tries.
	fmt.Fprintf(&b, "RUN mkdir -p /app/node_modules && %s\n", mgr.installArgs)
	b.WriteString("\n")

	fmt.Fprintf(&b, "FROM node:%s-bookworm-slim AS runtime\n", major)
	b.WriteString("WORKDIR /app\n")
	b.WriteString("ENV NODE_ENV=production\n")
	if mgr.corepack {
		b.WriteString("RUN corepack enable\n")
	}
	b.WriteString("COPY --from=deps /app/node_modules ./node_modules\n")
	b.WriteString("COPY . .\n")
	if hasBuild {
		fmt.Fprintf(&b, "RUN %s run build\n", mgr.name)
	}
	if port > 0 {
		fmt.Fprintf(&b, "ENV PORT=%d\nEXPOSE %d\n", port, port)
	}
	// A container that runs as root is a container whose escape is a root
	// escape, and node's image already ships a non-root user.
	b.WriteString("USER node\n")
	fmt.Fprintf(&b, "CMD %s\n", shellForm(start))

	why := fmt.Sprintf(
		"package.json and %s put this on Node %s with %s.", mgr.lockfile, major, mgr.name)
	if !found {
		why = fmt.Sprintf(
			"package.json puts this on Node %s. No lockfile was found, so npm install is used "+
				"and it may resolve versions production does not have. Commit a lockfile and "+
				"the environment gets the versions production has.", major)
	}
	if hasBuild {
		why += " The build script runs before the image is finished."
	}
	return &Buildpack{Name: "node", Dockerfile: b.String(), Why: why, Port: port}, true
}

// nodeInstallInputs lists the files the dependency install reads.
func nodeInstallInputs(fs fileSet, dir string, mgr nodeManager) []string {
	inputs := []string{at(dir, "package.json")}
	for _, candidate := range []string{mgr.lockfile, ".npmrc", ".yarnrc.yml", "pnpm-workspace.yaml"} {
		if p := at(dir, candidate); fs.Has(p) {
			inputs = append(inputs, p)
		} else if dir != "" && fs.Has(candidate) {
			inputs = append(inputs, candidate)
		}
	}
	return inputs
}

func nodeMajor(fs fileSet, dir, engines string) string {
	if v := majorFromRange(engines); v != "" {
		return v
	}
	for _, p := range []string{at(dir, ".nvmrc"), ".nvmrc"} {
		if raw, ok := fs.Read(p); ok {
			if m := nvmrcVersion.FindSubmatch([]byte(strings.TrimSpace(string(raw)))); m != nil {
				return string(m[1])
			}
		}
	}
	// The newest release that has been an LTS long enough that a repository
	// with no declared version is more likely to work on it than to break.
	return "22"
}

// majorFromRange pulls a major version out of a semver range like ">=20.9.0"
// or "^22". It takes the first number it finds, which is the floor of every
// range anyone writes in an engines field.
func majorFromRange(s string) string {
	m := regexp.MustCompile(`(\d+)`).FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	if n, err := strconv.Atoi(m[1]); err == nil && n >= 8 && n < 100 {
		return m[1]
	}
	return ""
}

// ---------------------------------------------------------------- Python

func pythonBuildpack(fs fileSet, dir, command string, port int) (*Buildpack, bool) {
	type pyManager struct {
		file, install, why string
	}
	managers := []pyManager{
		{"uv.lock", "pip install uv && uv sync --frozen --no-dev", "uv.lock"},
		{"poetry.lock", "pip install poetry && poetry install --no-root --only main", "poetry.lock"},
		{"Pipfile.lock", "pip install pipenv && pipenv install --deploy --system", "Pipfile.lock"},
		{"requirements.txt", "pip install --no-cache-dir -r requirements.txt", "requirements.txt"},
	}
	var chosen *pyManager
	var chosenPath string
	for i := range managers {
		for _, p := range []string{at(dir, managers[i].file), managers[i].file} {
			if fs.Has(p) {
				chosen, chosenPath = &managers[i], p
				break
			}
		}
		if chosen != nil {
			break
		}
	}
	if chosen == nil && !fs.Has(at(dir, "pyproject.toml")) {
		return nil, false
	}
	if chosen == nil {
		chosen = &pyManager{"pyproject.toml", "pip install --no-cache-dir .", "pyproject.toml"}
		chosenPath = at(dir, "pyproject.toml")
	}

	version := pythonVersion(fs, dir)
	start := command
	if start == "" {
		start = "python main.py"
	}

	var b strings.Builder
	b.WriteString("# Generated by Antifailure. https://antifailure.dev/docs/guides/builds\n")
	fmt.Fprintf(&b, "FROM python:%s-slim AS runtime\n", version)
	b.WriteString("WORKDIR /app\n")
	// Unbuffered output, or a crash loses the traceback that explains it, and
	// no .pyc files, which only grow the image.
	b.WriteString("ENV PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1 PIP_DISABLE_PIP_VERSION_CHECK=1\n")
	b.WriteString("COPY " + chosenPath + " ./\n")
	if chosen.file != "pyproject.toml" && fs.Has(at(dir, "pyproject.toml")) {
		b.WriteString("COPY " + at(dir, "pyproject.toml") + " ./\n")
	}
	fmt.Fprintf(&b, "RUN %s\n", chosen.install)
	b.WriteString("COPY . .\n")
	if port > 0 {
		fmt.Fprintf(&b, "ENV PORT=%d\nEXPOSE %d\n", port, port)
	}
	b.WriteString("RUN useradd --create-home --uid 10001 app && chown -R app /app\nUSER app\n")
	fmt.Fprintf(&b, "CMD %s\n", shellForm(start))

	return &Buildpack{
		Name:       "python",
		Dockerfile: b.String(),
		Why: fmt.Sprintf("%s puts this on Python %s, installed with %s.",
			chosen.why, version, strings.Fields(chosen.install)[0]),
		Port: port,
	}, true
}

var pyVersionRe = regexp.MustCompile(`(\d+)\.(\d+)`)

func pythonVersion(fs fileSet, dir string) string {
	for _, p := range []string{at(dir, ".python-version"), ".python-version"} {
		if raw, ok := fs.Read(p); ok {
			if m := pyVersionRe.FindStringSubmatch(string(raw)); m != nil {
				return m[1] + "." + m[2]
			}
		}
	}
	if raw, ok := fs.Read(at(dir, "pyproject.toml")); ok {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, "requires-python") || strings.Contains(line, "python =") {
				if m := pyVersionRe.FindStringSubmatch(line); m != nil {
					return m[1] + "." + m[2]
				}
			}
		}
	}
	return "3.12"
}

// ---------------------------------------------------------------- Go

var goDirectiveRe = regexp.MustCompile(`(?m)^go\s+(\d+\.\d+)`)

func goBuildpack(fs fileSet, dir, command string, port int) (*Buildpack, bool) {
	modPath := at(dir, "go.mod")
	raw, ok := fs.Read(modPath)
	if !ok {
		if raw, ok = fs.Read("go.mod"); !ok {
			return nil, false
		}
		modPath = "go.mod"
	}
	version := "1.23"
	if m := goDirectiveRe.FindSubmatch(raw); m != nil {
		version = string(m[1])
	}
	pkg := "."
	if dir != "" && dir != "." {
		pkg = "./" + strings.TrimSuffix(dir, "/")
	}

	var b strings.Builder
	b.WriteString("# Generated by Antifailure. https://antifailure.dev/docs/guides/builds\n")
	fmt.Fprintf(&b, "FROM golang:%s AS build\n", version)
	b.WriteString("WORKDIR /src\n")
	// The module cache is its own layer, so editing a source file does not
	// re-download the dependency graph.
	b.WriteString("COPY go.mod go.su[m] ./\n")
	b.WriteString("RUN go mod download\n")
	b.WriteString("COPY . .\n")
	// Static, so the runtime stage can be a distroless base with no libc.
	fmt.Fprintf(&b, "RUN CGO_ENABLED=0 go build -trimpath -o /out/app %s\n\n", pkg)
	b.WriteString("FROM gcr.io/distroless/static-debian12:nonroot AS runtime\n")
	b.WriteString("COPY --from=build /out/app /app\n")
	if port > 0 {
		fmt.Fprintf(&b, "ENV PORT=%d\nEXPOSE %d\n", port, port)
	}
	b.WriteString("USER nonroot\n")
	b.WriteString(`ENTRYPOINT ["/app"]` + "\n")

	return &Buildpack{
		Name:       "go",
		Dockerfile: b.String(),
		Why: fmt.Sprintf(
			"%s puts this on Go %s. The binary is built static so the image carries no toolchain and no libc.",
			modPath, version),
		Port: port,
	}, true
}

// ---------------------------------------------------------------- Ruby

var rubyVersionRe = regexp.MustCompile(`(\d+\.\d+\.\d+)`)

func rubyBuildpack(fs fileSet, dir, command string, port int) (*Buildpack, bool) {
	gemfile := at(dir, "Gemfile")
	if !fs.Has(gemfile) {
		if !fs.Has("Gemfile") {
			return nil, false
		}
		gemfile = "Gemfile"
	}
	version := "3.3"
	for _, p := range []string{at(dir, ".ruby-version"), ".ruby-version"} {
		if raw, ok := fs.Read(p); ok {
			if m := rubyVersionRe.FindStringSubmatch(string(raw)); m != nil {
				version = m[1]
				break
			}
		}
	}
	start := command
	if start == "" {
		start = "bundle exec rackup --host 0.0.0.0"
	}

	var b strings.Builder
	b.WriteString("# Generated by Antifailure. https://antifailure.dev/docs/guides/builds\n")
	fmt.Fprintf(&b, "FROM ruby:%s-slim AS runtime\n", version)
	b.WriteString("WORKDIR /app\n")
	b.WriteString("RUN apt-get update && apt-get install -y --no-install-recommends " +
		"build-essential libpq-dev git && rm -rf /var/lib/apt/lists/*\n")
	b.WriteString("ENV BUNDLE_DEPLOYMENT=1 BUNDLE_WITHOUT=development:test\n")
	b.WriteString("COPY " + gemfile + " Gemfile.loc[k] ./\n")
	b.WriteString("RUN bundle install\n")
	b.WriteString("COPY . .\n")
	if port > 0 {
		fmt.Fprintf(&b, "ENV PORT=%d\nEXPOSE %d\n", port, port)
	}
	b.WriteString("RUN useradd --create-home --uid 10001 app && chown -R app /app\nUSER app\n")
	fmt.Fprintf(&b, "CMD %s\n", shellForm(start))

	return &Buildpack{
		Name:       "ruby",
		Dockerfile: b.String(),
		Why:        fmt.Sprintf("%s puts this on Ruby %s, installed with bundler in deployment mode.", gemfile, version),
		Port:       port,
	}, true
}

// ---------------------------------------------------------------- shared

// shellForm renders a command for CMD.
//
// The exec form is used when the command has no shell syntax in it, because a
// shell in front of the process means the process is not PID 1 and does not
// receive the signal that stops the container. A command that genuinely needs
// a shell gets one, and the tradeoff is noted rather than hidden.
func shellForm(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if strings.ContainsAny(cmd, "&|;<>$`(){}*?~") {
		return fmt.Sprintf(`["/bin/sh", "-c", %s]`, quote(cmd))
	}
	parts := strings.Fields(cmd)
	quoted := make([]string, 0, len(parts))
	for _, p := range parts {
		quoted = append(quoted, quote(p))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// quote renders a JSON string without HTML escaping.
//
// json.Marshal turns & < and > into \u0026 and friends, which is correct JSON
// and terrible Dockerfile: "npm run migrate \u0026\u0026 npm start" is what a
// human reads when they open the generated file to find out what it does.
func quote(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		// Unreachable for a string, which always encodes. Kept so that a
		// change to the argument type cannot silently produce a Dockerfile
		// with an unquoted command in it.
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return strings.TrimRight(b.String(), "\n")
}

// BuildpackNames lists every buildpack, for documentation and for af doctor.
func BuildpackNames() []string {
	names := []string{"go", "node", "python", "ruby"}
	sort.Strings(names)
	return names
}
