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
		out = append(out, Finding{
			Kind: KindBuild, Subject: name, Value: "dockerfile",
			Confidence: High, Evidence: p,
			Detail: fmt.Sprintf("%s builds this service.", p),
			Extra: map[string]string{
				"dir":        dir,
				"dockerfile": p,
				"target":     df.finalStage,
				"base":       df.finalBase,
			},
		})
		out = append(out, Finding{
			Kind: KindService, Subject: name, Value: "web",
			Confidence: Medium, Evidence: p,
			Extra: map[string]string{"dir": dir},
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
			if m[2] != "" {
				info.stages = append(info.stages, m[2])
				info.finalStage = m[2]
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
				Extra: map[string]string{"dir": svc.buildContext},
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
					Extra:  map[string]string{"dir": dir}},
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
