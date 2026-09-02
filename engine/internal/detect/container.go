package detect

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// DockerAnalyzer reads Dockerfiles.
//
// A Dockerfile is the most reliable source in a repository, because it is what
// actually builds the image. Its EXPOSE, CMD, and ENTRYPOINT are statements
// about the real runtime rather than inferences from a dependency list.
type DockerAnalyzer struct{}

// Name identifies the analyzer.
func (*DockerAnalyzer) Name() string { return "docker" }

var (
	dockerFromRe    = regexp.MustCompile(`(?i)^\s*FROM\s+(\S+)(?:\s+AS\s+(\S+))?`)
	dockerExposeRe  = regexp.MustCompile(`(?i)^\s*EXPOSE\s+(.+)$`)
	dockerCmdRe     = regexp.MustCompile(`(?i)^\s*(CMD|ENTRYPOINT)\s+(.+)$`)
	dockerWorkdirRe = regexp.MustCompile(`(?i)^\s*WORKDIR\s+(\S+)`)
	dockerCopyRe    = regexp.MustCompile(`(?i)^\s*(COPY|ADD)\s+(.+)$`)
)

// dockerfileNames are the spellings that appear in real repositories.
var dockerfileNames = []string{"Dockerfile", "dockerfile", "Dockerfile.prod", "Dockerfile.production"}

// Analyze reports what each Dockerfile says about its service.
func (a *DockerAnalyzer) Analyze(_ context.Context, r *Repo) ([]Finding, error) {
	var out []Finding
	for _, p := range r.Glob(dockerfileNames...) {
		// A Dockerfile inside a test or example directory describes something
		// other than the application.
		if isExamplePath(p) {
			continue
		}
		body, ok := r.ReadString(p)
		if !ok {
			continue
		}
		dir := dirOf(p)
		name := sanitizeServiceName(baseNameFor(dir, r))

		df := parseDockerfile(body)
		inDir, ambiguous, why := contextFor(dir, df.copySources, r)
		extra := map[string]string{
			"dir":        dir,
			"dockerfile": p,
			"target":     df.finalStage,
			"base":       df.finalBase,
		}
		switch {
		case ambiguous:
			// inDir is the default here rather than the answer, and which one
			// it is depends on which ambiguity this is. See contextFor.
			extra["context_ambiguous"] = dir
			extra["context_default"] = "."
			if inDir {
				extra["context_default"] = dir
			}
			extra["context_why"] = why
		case inDir:
			extra["context"] = dir
			extra["context_why"] = why
		}
		out = append(out, Finding{
			Kind: KindBuild, Subject: name, Value: "dockerfile",
			Confidence: High, Evidence: p,
			Detail: fmt.Sprintf("%s builds this service.", p),
			Extra:  extra,
		})
		out = append(out, Finding{
			Kind: KindService, Subject: name, Value: "web",
			Confidence: Medium, Evidence: p,
			// A Dockerfile carries no name of its own, so this one is the
			// directory, which the merger treats as the weakest identity.
			Extra: map[string]string{"dir": dir, "name_from": "dir"},
		})
		if len(df.stages) > 1 {
			out = append(out, Finding{
				Kind: KindNote, Subject: name + ".stages", Value: strings.Join(df.stages, ","),
				Confidence: High, Evidence: p,
				Detail: fmt.Sprintf("%s is a multi stage build with stages %s.",
					p, strings.Join(df.stages, ", ")),
			})
		}
		for _, port := range df.ports {
			out = append(out, Finding{
				Kind: KindPort, Subject: name, Value: strconv.Itoa(port),
				Confidence: High, Evidence: p,
				Detail: fmt.Sprintf("%s exposes port %d.", p, port),
			})
		}
		if df.command != "" {
			out = append(out, Finding{
				Kind: KindCommand, Subject: name, Value: df.command,
				Confidence: High, Evidence: p,
			})
		}
	}
	return out, nil
}

type dockerfileInfo struct {
	stages     []string
	finalStage string
	finalBase  string
	ports      []int
	command    string
	workdir    string
	// copySources are the paths COPY and ADD read out of the build context.
	// They are the only statement a Dockerfile makes about which directory it
	// expects to be built from. A copy from an earlier stage is excluded,
	// because it reads from the image rather than from the context.
	copySources []string
}

// parseDockerfile reads the instructions detection cares about. It is not a
// full Dockerfile parser and does not need to be: it reads four instructions,
// tolerates everything else, and never evaluates anything.
func parseDockerfile(body string) dockerfileInfo {
	var info dockerfileInfo
	seenPorts := map[int]bool{}
	var entrypoint, cmd string

	for _, raw := range logicalLines(body) {
		if m := dockerFromRe.FindStringSubmatch(raw); m != nil {
			info.finalBase = m[1]
			// The canonical multi-stage build names its builder and leaves the
			// runtime stage unnamed. Only assigning on a named stage therefore
			// left finalStage pointing at the builder, and af init wrote
			// 'target: build' into the manifest, so af up would have built the
			// stage that compiles the application instead of the one that runs
			// it. An unnamed FROM has to clear the name, not keep the last one.
			info.finalStage = m[2]
			if m[2] != "" {
				info.stages = append(info.stages, m[2])
			}
			continue
		}
		if m := dockerExposeRe.FindStringSubmatch(raw); m != nil {
			for _, f := range strings.Fields(m[1]) {
				// EXPOSE accepts 8080 and 8080/tcp alike.
				numText := strings.SplitN(f, "/", 2)[0]
				if n, err := strconv.Atoi(numText); err == nil && n > 0 && n < 65536 && !seenPorts[n] {
					seenPorts[n] = true
					info.ports = append(info.ports, n)
				}
			}
			continue
		}
		if m := dockerWorkdirRe.FindStringSubmatch(raw); m != nil {
			info.workdir = m[1]
			continue
		}
		if m := dockerCopyRe.FindStringSubmatch(raw); m != nil {
			info.copySources = append(info.copySources, copySourcesOf(m[2])...)
			continue
		}
		if m := dockerCmdRe.FindStringSubmatch(raw); m != nil {
			text := normalizeDockerArgs(m[2])
			if strings.EqualFold(m[1], "ENTRYPOINT") {
				entrypoint = text
			} else {
				cmd = text
			}
		}
	}
	// An image with both runs the entrypoint with the command as arguments,
	// which is the semantics the runtime has to reproduce.
	switch {
	case entrypoint != "" && cmd != "":
		info.command = entrypoint + " " + cmd
	case entrypoint != "":
		info.command = entrypoint
	default:
		info.command = cmd
	}
	return info
}

// logicalLines joins continuations so that an instruction split across lines
// with a trailing backslash is read as one.
func logicalLines(body string) []string {
	var out []string
	var cur strings.Builder
	for _, l := range strings.Split(body, "\n") {
		trimmed := strings.TrimRight(l, "\r")
		if strings.HasPrefix(strings.TrimSpace(trimmed), "#") && cur.Len() == 0 {
			continue
		}
		if strings.HasSuffix(trimmed, "\\") {
			cur.WriteString(strings.TrimSuffix(trimmed, "\\"))
			cur.WriteString(" ")
			continue
		}
		cur.WriteString(trimmed)
		out = append(out, cur.String())
		cur.Reset()
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// normalizeDockerArgs turns the JSON array form into a plain command line, and
// leaves the shell form as it is.
func normalizeDockerArgs(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") {
		return s
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	var parts []string
	for _, f := range strings.Split(inner, ",") {
		f = strings.TrimSpace(f)
		f = strings.Trim(f, `"'`)
		if f != "" {
			parts = append(parts, f)
		}
	}
	return strings.Join(parts, " ")
}

// copySourcesOf returns the context paths one COPY or ADD instruction reads.
//
// A line carrying --from reads from an earlier stage or a named image rather
// than from the build context, so it says nothing about which directory the
// context is and is dropped whole. Every other flag is dropped individually,
// and the last remaining field is the destination inside the image.
func copySourcesOf(args string) []string {
	fields := strings.Fields(normalizeDockerArgs(args))
	var kept []string
	for _, f := range fields {
		if strings.HasPrefix(f, "--") {
			if strings.HasPrefix(strings.ToLower(f), "--from=") {
				return nil
			}
			continue
		}
		kept = append(kept, f)
	}
	if len(kept) < 2 {
		// One field is a destination with no source, which Docker refuses.
		// Reading it as a source would invent evidence out of a broken line.
		return nil
	}
	return kept[:len(kept)-1]
}

// contextFor works out which directory a Dockerfile expects to be built from.
//
// The failure it exists to stop: af init never set build.context, so the
// context was always the repository root, while a Dockerfile at
// dashboard/Dockerfile is conventionally built with dashboard/ as its context,
// which is what 'docker build dashboard' does. With an explicit COPY the build
// fails outright; with COPY . . it SUCCEEDS and produces an image built from
// the wrong directory, so the failure moves to startup and nothing says why.
//
// Rather than choose a default and be wrong for one of the two conventions,
// this reads the evidence the Dockerfile already carries. A COPY source that
// exists beside the Dockerfile and not at the root means the context is the
// Dockerfile's directory. One that exists at the root and not beside it means
// the root, which is the monorepo shape where a service image needs the
// lockfile at the top of the tree. Anything else is a real ambiguity and is
// returned as one, so it can be asked rather than guessed.
//
// It never reports the root, because the root is what an unset context already
// means. Saying so in the manifest would be noise, and more importantly it
// would be a behaviour change for every repository that builds correctly
// today.
//
// The two ambiguous shapes deliberately get different defaults, because they
// are different questions. When every source resolves in BOTH places a build
// from either works, so a repository doing that today is building from the
// root and succeeding, and the root stays the default. When nothing resolves
// anywhere the only instruction reading the context is COPY . ., there is no
// evidence for the root at all, and the root is the case that succeeds while
// assembling the image from the wrong directory. Treating those two as one
// ambiguity would either break the first or leave the second exactly as it
// was. Both are still asked.
func contextFor(dir string, sources []string, r *Repo) (dirContext bool, ambiguous bool, why string) {
	if dir == "" || len(sources) == 0 {
		// A Dockerfile at the root is already built from the root. One that
		// copies nothing does not read the context at all, so which directory
		// it is cannot change what the image contains, and asking would be
		// noise about a decision that has no consequence.
		return false, false, ""
	}
	var beside, atRoot string
	for _, src := range sources {
		clean := strings.TrimPrefix(path.Clean(src), "./")
		if clean == "." || clean == "/" || clean == "*" || strings.HasPrefix(clean, "..") {
			// COPY . . works from either directory and copies whatever it is
			// given, which is exactly why it is the shape that fails quietly.
			continue
		}
		if beside == "" && repoHas(r, path.Join(dir, clean)) {
			beside = clean
		}
		if atRoot == "" && repoHas(r, clean) {
			atRoot = clean
		}
	}
	switch {
	case beside != "" && atRoot == "":
		return true, false, fmt.Sprintf("%s/Dockerfile copies %s, which exists in %s and not at the repository root.",
			dir, beside, dir)
	case atRoot != "" && beside == "":
		return false, false, ""
	case beside != "" && atRoot != "":
		// Both roots satisfy every source, so a build from either produces an
		// image. A repository doing this today is building from the root and
		// working, so the root stays the answer a run with nobody watching
		// takes, and the question is only put to a person.
		return false, true, fmt.Sprintf(
			"%s/Dockerfile copies %s, which exists in %s and at the repository root, so both would build.",
			dir, beside, dir)
	}
	// Nothing resolves anywhere, which in practice means the only instruction
	// reading the context is COPY . .. That is a different ambiguity from the
	// one above and it does not get the same answer: there is no evidence for
	// the root, and building from the root is the case that SUCCEEDS while
	// producing an image assembled from the wrong directory. So the default
	// here is what 'docker build <dir>' does, and it is still asked.
	return true, true, fmt.Sprintf(
		"%s/Dockerfile copies its whole context and names no path that exists in only one of them.", dir)
}

// repoHas reports whether the index holds this path, a file under it, or
// anything matching it as a pattern. COPY takes globs, and a directory source
// never appears in an index of files.
func repoHas(r *Repo, p string) bool {
	p = path.Clean(p)
	if r.Exists(p) {
		return true
	}
	prefix := p + "/"
	for _, f := range r.Files() {
		if strings.HasPrefix(f, prefix) {
			return true
		}
		if ok, err := path.Match(p, f); err == nil && ok {
			return true
		}
	}
	return false
}

func isExamplePath(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "test", "tests", "testdata", "example", "examples", "fixtures",
			"docs", "doc", "sample", "samples", "e2e", "__tests__":
			return true
		}
	}
	return false
}

// ComposeAnalyzer reads Docker Compose files.
//
// Compose is the closest thing most repositories have to a declaration of
// their whole stack: it names the services, their ports, their dependencies,
// and, importantly, the databases and queues they need.
type ComposeAnalyzer struct{}

// Name identifies the analyzer.
func (*ComposeAnalyzer) Name() string { return "compose" }

var composeNames = []string{
	"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml",
}

// Analyze reports the services a compose file declares.
func (a *ComposeAnalyzer) Analyze(_ context.Context, r *Repo) ([]Finding, error) {
	var out []Finding
	for _, p := range r.Glob(composeNames...) {
		if isExamplePath(p) {
			continue
		}
		body, ok := r.ReadString(p)
		if !ok {
			continue
		}
		for _, svc := range parseCompose(body) {
			name := sanitizeServiceName(svc.name)

			// A database or queue in compose is infrastructure the environment
			// provides, not a service to build. Recognising it is how the
			// database provider gets chosen without asking.
			if kind := infraKind(svc.image); kind != "" {
				out = append(out, Finding{
					Kind: KindDatabase, Subject: kind, Value: svc.image,
					Confidence: High, Evidence: p,
					Detail: fmt.Sprintf("%s runs %s as the %s.", p, svc.image, kind),
				})
				continue
			}

			conf := Medium
			detail := fmt.Sprintf("%s declares the service %s.", p, svc.name)
			out = append(out, Finding{
				Kind: KindService, Subject: name, Value: "web",
				Confidence: conf, Evidence: p, Detail: detail,
				Extra: map[string]string{"dir": svc.buildContext, "name_from": "compose"},
			})
			if svc.port != 0 {
				out = append(out, Finding{
					Kind: KindPort, Subject: name, Value: strconv.Itoa(svc.port),
					Confidence: High, Evidence: p,
					Detail: fmt.Sprintf("%s publishes container port %d.", p, svc.port),
				})
			}
			if svc.command != "" {
				out = append(out, Finding{
					Kind: KindCommand, Subject: name, Value: svc.command,
					Confidence: High, Evidence: p,
				})
			}
			for _, dep := range svc.dependsOn {
				if infraKind(dep) != "" || isInfraName(dep) {
					continue
				}
				out = append(out, Finding{
					Kind: KindNote, Subject: name + ".depends_on", Value: sanitizeServiceName(dep),
					Confidence: High, Evidence: p,
				})
			}
			for _, e := range svc.envNames {
				out = append(out, Finding{
					Kind: KindEnvVar, Subject: e, Value: name,
					Confidence: High, Evidence: p,
					Detail: fmt.Sprintf("%s passes %s to %s.", p, e, svc.name),
				})
			}
		}
	}
	return out, nil
}

type composeService struct {
	name         string
	image        string
	buildContext string
	port         int
	command      string
	dependsOn    []string
	envNames     []string
}

// parseCompose reads the fields detection needs with an indentation aware
// scanner rather than a YAML library.
//
// The reason is not to avoid a dependency: it is that compose files in the
// wild use anchors, extends, profiles, and merge keys that a strict decoder
// rejects outright. A tolerant reader that extracts four fields and ignores
// everything it does not understand gets useful results from files a strict
// one refuses entirely, and detection is allowed to be approximate in a way
// that manifest parsing is not.
func parseCompose(body string) []composeService {
	var out []composeService
	var cur *composeService
	inServices := false
	serviceIndent := -1
	section := ""

	for _, raw := range strings.Split(body, "\n") {
		lineText := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(lineText)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(lineText) - len(strings.TrimLeft(lineText, " "))

		if indent == 0 {
			inServices = strings.HasPrefix(trimmed, "services:")
			if cur != nil {
				out = append(out, *cur)
				cur = nil
			}
			continue
		}
		if !inServices {
			continue
		}
		if serviceIndent < 0 || indent == serviceIndent {
			if strings.HasSuffix(trimmed, ":") && !strings.Contains(trimmed, " ") {
				if cur != nil {
					out = append(out, *cur)
				}
				serviceIndent = indent
				cur = &composeService{name: strings.TrimSuffix(trimmed, ":")}
				section = ""
				continue
			}
		}
		if cur == nil {
			continue
		}

		key, value := splitYAMLPair(trimmed)
		switch {
		case key == "image":
			cur.image = strings.Trim(value, `"'`)
			section = ""
		case key == "command":
			cur.command = normalizeDockerArgs(strings.Trim(value, `"'`))
			section = ""
		case key == "build":
			if value != "" && value != "." {
				cur.buildContext = strings.Trim(value, `"'`)
			}
			section = "build"
		case key == "context" && section == "build":
			cur.buildContext = strings.Trim(value, `"'`)
		case key == "ports":
			section = "ports"
		case key == "depends_on":
			section = "depends_on"
		case key == "environment":
			section = "environment"
		case strings.HasPrefix(trimmed, "-"):
			item := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "-")), `"'`)
			switch section {
			case "ports":
				if p := containerPort(item); p != 0 && cur.port == 0 {
					cur.port = p
				}
			case "depends_on":
				cur.dependsOn = append(cur.dependsOn, strings.TrimSuffix(item, ":"))
			case "environment":
				if n := envNameOf(item); n != "" {
					cur.envNames = append(cur.envNames, n)
				}
			}
		case section == "depends_on" && strings.HasSuffix(trimmed, ":"):
			cur.dependsOn = append(cur.dependsOn, strings.TrimSuffix(trimmed, ":"))
		case section == "environment" && key != "":
			cur.envNames = append(cur.envNames, key)
		default:
			if key != "" {
				section = ""
			}
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out
}

func splitYAMLPair(s string) (key, value string) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return "", ""
	}
	return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])
}

// containerPort extracts the container side of a compose port mapping, which
// is the number the application actually binds. "8080:3000" means the
// container listens on 3000.
func containerPort(spec string) int {
	spec = strings.SplitN(spec, "/", 2)[0] // drop /tcp
	parts := strings.Split(spec, ":")
	last := parts[len(parts)-1]
	if strings.Contains(last, "-") {
		last = strings.SplitN(last, "-", 2)[0]
	}
	n, err := strconv.Atoi(last)
	if err != nil || n <= 0 || n >= 65536 {
		return 0
	}
	return n
}

func envNameOf(item string) string {
	if i := strings.IndexByte(item, '='); i > 0 {
		return item[:i]
	}
	if item != "" && !strings.ContainsAny(item, " \t:") {
		return item
	}
	return ""
}

// infraKind recognises an image as infrastructure rather than application code.
func infraKind(image string) string {
	l := strings.ToLower(image)
	if l == "" {
		return ""
	}
	// Strip a registry prefix and a tag so that ghcr.io/x/postgres:16 matches.
	base := l
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	base = strings.SplitN(base, ":", 2)[0]
	base = strings.SplitN(base, "@", 2)[0]

	switch {
	case strings.Contains(base, "postgres"), strings.Contains(base, "pgvector"),
		strings.Contains(base, "timescale"), strings.Contains(base, "supabase"):
		return "postgres"
	case base == "redis", strings.Contains(base, "redis"), strings.Contains(base, "valkey"):
		return "redis"
	case strings.Contains(base, "mysql"), strings.Contains(base, "mariadb"):
		return "mysql"
	case strings.Contains(base, "mongo"):
		return "mongodb"
	case strings.Contains(base, "rabbitmq"):
		return "rabbitmq"
	case strings.Contains(base, "elasticsearch"), strings.Contains(base, "opensearch"):
		return "search"
	case strings.Contains(base, "minio"):
		return "objectstore"
	case strings.Contains(base, "clickhouse"):
		return "clickhouse"
	}
	return ""
}

func isInfraName(name string) bool {
	switch strings.ToLower(name) {
	case "db", "database", "postgres", "postgresql", "pg", "redis", "cache",
		"mysql", "mongo", "mongodb", "rabbitmq", "queue", "elasticsearch", "minio":
		return true
	}
	return false
}

// ProcfileAnalyzer reads a Procfile, which names processes directly.
type ProcfileAnalyzer struct{}

// Name identifies the analyzer.
func (*ProcfileAnalyzer) Name() string { return "procfile" }

// Analyze reports one service per Procfile entry.
func (a *ProcfileAnalyzer) Analyze(_ context.Context, r *Repo) ([]Finding, error) {
	var out []Finding
	for _, p := range r.Glob("Procfile", "Procfile.dev") {
		body, ok := r.ReadString(p)
		if !ok {
			continue
		}
		dir := dirOf(p)
		for _, lineText := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(lineText)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			i := strings.IndexByte(trimmed, ':')
			if i <= 0 {
				continue
			}
			procName := strings.TrimSpace(trimmed[:i])
			command := strings.TrimSpace(trimmed[i+1:])
			if command == "" {
				continue
			}
			name := sanitizeServiceName(procName)
			// Heroku's convention: the process named web receives traffic and
			// everything else does not.
			kind := "worker"
			if procName == "web" {
				kind = "web"
			}
			if procName == "release" {
				out = append(out, Finding{
					Kind: KindMigration, Subject: sanitizeServiceName(baseNameFor(dir, r)),
					Value: command, Confidence: High, Evidence: p,
					Detail: "The Procfile release phase runs before a deploy, which is where migrations go.",
				})
				continue
			}
			out = append(out,
				Finding{Kind: KindService, Subject: name, Value: kind,
					Confidence: High, Evidence: p,
					Detail: fmt.Sprintf("%s declares the process %s.", p, procName),
					Extra:  map[string]string{"dir": dir, "name_from": "procfile"}},
				Finding{Kind: KindCommand, Subject: name, Value: command,
					Confidence: High, Evidence: p},
			)
			if port := portFromCommand(command); port != 0 {
				out = append(out, Finding{
					Kind: KindPort, Subject: name, Value: strconv.Itoa(port),
					Confidence: High, Evidence: p,
				})
			}
		}
	}
	return out, nil
}

func portFromCommand(cmd string) int {
	if m := portFlagRe.FindStringSubmatch(cmd); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n < 65536 {
			return n
		}
	}
	return 0
}

// unusedPathHelper keeps the path import honest if the analyzer set changes.
var _ = path.Base
