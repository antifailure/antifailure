package detect

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Merge turns findings into a manifest draft and a list of questions.
//
// The rule that shapes it: a finding at high confidence goes into the draft
// silently, and anything below that becomes a question. The alternative,
// guessing quietly, produces a manifest the user has to audit rather than
// read, and the whole point of af init is that its output is trustworthy
// enough to commit.
//
// When two analyzers disagree, the stronger evidence wins and the
// disagreement becomes a question, because a conflict is exactly the case
// where a silent choice is most likely to be wrong.
func Merge(findings []Finding, root string) (*schema.Manifest, []Question, Proposals) {
	m := &schema.Manifest{
		Version: schema.ManifestVersion,
		Name:    sanitizeServiceName(path.Base(root)),
	}
	var questions []Question

	services := mergeServices(findings, &questions)
	m.Services = services

	// The checkout directory is whatever the developer happened to clone into,
	// and it is often not the application's name at all. A framework backed
	// service name comes from the package itself, so it is what a reader will
	// recognise.
	if name := primaryServiceName(findings, services); name != "" {
		m.Name = name
	}

	m.Database = mergeDatabase(findings, services, &questions)
	// A store beside the primary, with the stance detection proposes for it.
	// Only the ones this build can bring up reach the draft; the rest are
	// returned so af init can name them, because a manifest declaring an
	// engine no provider serves is a manifest af up refuses, and af init
	// promises the opposite.
	var proposals Proposals
	proposals.Datastores, m.Datastores = mergeDatastores(findings, hasPostgresFinding(findings))
	m.Egress, proposals.Emulators = mergeEgress(findings)
	m.Personas = defaultPersonas()
	m.Auth = mergeAuth(findings)
	m.Workflows = suggestedWorkflows(findings)

	// Sorted so that two runs over the same tree produce byte identical YAML.
	sort.SliceStable(m.Services, func(i, j int) bool {
		// Web services first, since they are what a reader looks for.
		if (m.Services[i].Kind == schema.ServiceWeb) != (m.Services[j].Kind == schema.ServiceWeb) {
			return m.Services[i].Kind == schema.ServiceWeb
		}
		return m.Services[i].Name < m.Services[j].Name
	})
	sort.SliceStable(questions, func(i, j int) bool { return questions[i].ID < questions[j].ID })
	return m, questions, proposals
}

// Proposals are the things detection found and could not simply write into the
// manifest.
//
// They are separate from the draft because each one needs a sentence rather
// than a line: a datastore this build has no provider for would make a
// manifest af up refuses, and an emulator is evidence about the manifest
// rather than a part of it. Before this existed both were dropped on the
// floor by Merge, which is why a compose file running ClickHouse and
// LocalStack produced a manifest that mentioned neither.
type Proposals struct {
	// Datastores are the stores found beside the primary database, both the
	// ones written into the draft and the ones this build cannot bring up.
	Datastores []ProposedDatastore
	// Emulators are the cloud emulators the repository runs in its own
	// compose file, and the rules each one produced.
	Emulators []DetectedEmulator
}

// hasPostgresFinding reports whether anything in the repository said Postgres.
//
// It is what tells a Mongo that IS the application's database apart from a
// Mongo sitting beside a Postgres as a second store. mergeDatabase already
// asks about the first case by name, and proposing a stance for it as well
// would be one fact stated twice in two vocabularies.
func hasPostgresFinding(findings []Finding) bool {
	for _, f := range OfKind(findings, KindDatabase) {
		if f.Subject == "postgres" {
			return true
		}
	}
	return false
}

// primaryServiceName picks the name the application should carry: the first
// web service that a framework analyzer recognised, since that is the one
// whose package name a person would use.
func primaryServiceName(findings []Finding, services []schema.Service) string {
	framework := map[string]bool{}
	for _, f := range OfKind(findings, KindFramework) {
		framework[f.Subject] = true
	}
	for _, s := range services {
		if s.Kind == schema.ServiceWeb && framework[s.Name] {
			return s.Name
		}
	}
	for _, s := range services {
		if s.Kind == schema.ServiceWeb {
			return s.Name
		}
	}
	return ""
}

// candidate accumulates every finding about one service before it becomes one.
type candidate struct {
	name      string
	kind      schema.ServiceKind
	dir       string
	framework string
	port      int
	portConf  Confidence
	portWhy   string
	command   string
	cmdConf   Confidence
	// cmdEvidence is the file the command came from. It breaks a tie between
	// two commands of equal confidence in favour of the one the image runs.
	cmdEvidence string
	migrate     string
	build       *schema.Build
	schedule    string
	cronPath    string
	dependsOn   []string
	evidence    []string
	// portConflicts records ports other analyzers proposed, which is what
	// turns a disagreement into a question rather than a silent choice.
	portConflicts map[int]string
	// declaredBy is the set of analyzers that declared this candidate a
	// service. It is what tells one source describing two services apart from
	// two sources describing one, which coalesce turns on.
	declaredBy map[string]bool
	// nameRank is how much the name is worth as an identity, from the source
	// that produced it. See nameRankOf.
	nameRank int
	// contextWhy explains which directory the Dockerfile is built from, and
	// contextAmbiguous holds the directory when the evidence does not settle
	// it, so the caller asks instead of choosing.
	contextWhy       string
	contextAmbiguous string
	// contextDefault is the answer an unattended run takes, which is not
	// always the directory. See detect.contextFor.
	contextDefault string
}

func mergeServices(findings []Finding, questions *[]Question) []schema.Service {
	byName := map[string]*candidate{}
	order := []string{}

	get := func(name string) *candidate {
		if c, ok := byName[name]; ok {
			return c
		}
		c := &candidate{
			name:          name,
			kind:          schema.ServiceWeb,
			portConflicts: map[int]string{},
			declaredBy:    map[string]bool{},
		}
		byName[name] = c
		order = append(order, name)
		return c
	}

	// declare records that an analyzer says this candidate is a service, and
	// where the name it used came from.
	declare := func(c *candidate, f Finding) {
		c.declaredBy[f.Analyzer] = true
		if r := nameRankOf(f.Extra["name_from"]); r > c.nameRank {
			c.nameRank = r
		}
	}

	for _, f := range findings {
		switch f.Kind {
		case KindService:
			c := get(f.Subject)
			declare(c, f)
			switch f.Value {
			case "worker":
				c.kind = schema.ServiceWorker
			case "cron":
				c.kind = schema.ServiceCron
			}
			if d := f.Extra["dir"]; d != "" && c.dir == "" {
				c.dir = d
			}
			if fw := f.Extra["framework"]; fw != "" && c.framework == "" {
				c.framework = fw
			}
			c.evidence = appendUnique(c.evidence, f.Evidence)

		case KindWorker:
			c := get(f.Subject)
			declare(c, f)
			c.kind = schema.ServiceWorker
			if f.Value != "" && c.command == "" {
				c.command, c.cmdConf, c.cmdEvidence = f.Value, f.Confidence, f.Evidence
			}
			if d := f.Extra["dir"]; d != "" && c.dir == "" {
				c.dir = d
			}
			c.evidence = appendUnique(c.evidence, f.Evidence)

		case KindCron:
			c := get(f.Subject)
			declare(c, f)
			c.kind = schema.ServiceCron
			c.schedule = f.Value
			c.cronPath = f.Extra["path"]
			c.evidence = appendUnique(c.evidence, f.Evidence)

		case KindFramework:
			c := get(f.Subject)
			if c.framework == "" {
				c.framework = f.Value
			}
			if d := f.Extra["dir"]; d != "" && c.dir == "" {
				c.dir = d
			}

		case KindPort:
			c := get(f.Subject)
			n, err := strconv.Atoi(f.Value)
			if err != nil || n <= 0 || n >= 65536 {
				continue
			}
			c.absorbPort(n, f.Confidence, f.Detail)

		case KindCommand:
			c := get(f.Subject)
			if c.command == "" || f.Confidence > c.cmdConf {
				c.command, c.cmdConf, c.cmdEvidence = f.Value, f.Confidence, f.Evidence
			}

		case KindMigration:
			c := get(f.Subject)
			if c.dir == "" {
				c.dir = f.Extra["dir"]
			}
			if c.migrate == "" {
				c.migrate = f.Value
			}

		case KindBuild:
			c := get(f.Subject)
			if f.Value == "dockerfile" {
				b := &schema.Build{Strategy: schema.BuildDockerfile}
				if df := f.Extra["dockerfile"]; df != "" {
					b.Dockerfile = df
				}
				if t := f.Extra["target"]; t != "" {
					b.Target = t
				}
				if ctx := f.Extra["context"]; ctx != "" {
					b.Context = ctx
				}
				c.build = b
				c.contextWhy = f.Extra["context_why"]
				c.contextAmbiguous = f.Extra["context_ambiguous"]
				c.contextDefault = f.Extra["context_default"]
			}
			if d := f.Extra["dir"]; d != "" && c.dir == "" {
				c.dir = d
			}

		case KindNote:
			if strings.HasSuffix(f.Subject, ".depends_on") && f.Value != "" {
				name := strings.TrimSuffix(f.Subject, ".depends_on")
				c := get(name)
				c.dependsOn = appendUnique(c.dependsOn, f.Value)
			}
		}
	}

	// Two sources describing one service are still one service.
	order = coalesceServices(byName, order)

	// A migration needs an owner with evidence, not the first service in a
	// sorted list. An orphan with no unique local owner becomes a question.
	reassignRepoWideMigration(byName, order, questions)

	out := make([]schema.Service, 0, len(order))
	for _, name := range order {
		c := byName[name]
		if len(c.declaredBy) == 0 {
			continue
		}
		// A candidate with nothing but a migration command is not a service.
		if c.kind == schema.ServiceWeb && c.port == 0 && c.command == "" && c.build == nil && c.framework == "" {
			continue
		}
		s := schema.Service{
			Name:      c.name,
			Path:      c.dir,
			Kind:      c.kind,
			Command:   c.command,
			Migrate:   c.migrate,
			DependsOn: c.dependsOn,
		}
		if c.build != nil {
			s.Build = c.build
		}
		if c.kind == schema.ServiceWeb {
			s.Port = c.port
		}
		if c.kind == schema.ServiceCron {
			s.Schedule = c.schedule
			if c.command == "" && c.cronPath != "" {
				s.HealthPath = c.cronPath
			}
		}
		out = append(out, s)

		// Questions. A port below high confidence, or a conflict, is asked.
		if c.kind == schema.ServiceWeb {
			switch {
			case c.port == 0:
				// A default even here, and it is the language's own. The
				// question used to carry none, so a run with no terminal had
				// nothing to take and refused with AF-DET-004, which for a
				// pull request check meant no check at all. A port that is
				// wrong is found in seconds by a readiness probe that never
				// answers; a check that never ran is found by nobody.
				port, why := defaultPort(c)
				*questions = append(*questions, Question{
					ID:      "service." + c.name + ".port",
					Prompt:  fmt.Sprintf("Which port does %s listen on?", c.name),
					Options: []string{strconv.Itoa(port)},
					Default: strconv.Itoa(port),
					Why:     "No port was found in the code, a Dockerfile, or a compose file. " + why,
				})
			case len(c.portConflicts) > 0:
				options := []string{strconv.Itoa(c.port)}
				for p := range c.portConflicts {
					options = append(options, strconv.Itoa(p))
				}
				sort.Strings(options[1:])
				*questions = append(*questions, Question{
					ID:      "service." + c.name + ".port",
					Prompt:  fmt.Sprintf("Which port does %s listen on?", c.name),
					Options: options,
					Default: strconv.Itoa(c.port),
					Why: fmt.Sprintf("Sources disagree. %s %s",
						c.portWhy, strings.Join(sortedValues(c.portConflicts), " ")),
				})
			case c.portConf < High:
				*questions = append(*questions, Question{
					ID:      "service." + c.name + ".port",
					Prompt:  fmt.Sprintf("Does %s listen on port %d?", c.name, c.port),
					Options: []string{strconv.Itoa(c.port)},
					Default: strconv.Itoa(c.port),
					Why:     c.portWhy,
				})
			}
		}
		// A Dockerfile in a subdirectory is built either from that directory,
		// which is what 'docker build <dir>' does, or from the repository
		// root, which is what a monorepo image needs to reach a lockfile at
		// the top of the tree. Both are common, so where the COPY lines do not
		// settle it this is a question rather than a default.
		if c.contextAmbiguous != "" {
			*questions = append(*questions, Question{
				ID:      "service." + c.name + ".context",
				Prompt:  fmt.Sprintf("Which directory is %s built from?", c.name),
				Options: []string{c.contextAmbiguous, "."},
				Default: c.contextDefault,
				Why: c.contextWhy + " " + fmt.Sprintf(
					"'docker build %s' would use %s; a monorepo image that needs a file from the top of the tree wants '.'.",
					c.contextAmbiguous, c.contextAmbiguous),
			})
		}
		if c.command == "" && c.build == nil && c.kind != schema.ServiceCron {
			q := Question{
				ID:     "service." + c.name + ".command",
				Prompt: fmt.Sprintf("What command starts %s?", c.name),
				Why:    "No start script, Dockerfile command, or Procfile entry was found.",
			}
			// The conventional start where the language has one. Where it
			// does not, the question keeps no default and an unattended run
			// still refuses with AF-DET-004, because a made up command is
			// worse than a question: it fails inside a container, in a log,
			// ten seconds later.
			if cmd, why := defaultCommand(c); cmd != "" {
				q.Options, q.Default = []string{cmd}, cmd
				q.Why += " " + why
			}
			*questions = append(*questions, q)
		}
	}
	return out
}

// languageOf names the toolchain a candidate is built with, from the analyzer
// that declared it or the framework it was recognised by. Empty when neither
// says, which is what a bare Dockerfile with no EXPOSE looks like.
func languageOf(c *candidate) string {
	switch c.framework {
	case "django", "fastapi", "flask":
		return "python"
	case "go":
		return "go"
	case "rails":
		return "ruby"
	}
	for _, lang := range []string{"node", "python", "go", "ruby"} {
		if c.declaredBy[lang] {
			return lang
		}
	}
	if c.framework != "" {
		// Every framework the node analyzer recognises reaches here, since
		// the three other languages name theirs above.
		return "node"
	}
	return ""
}

// defaultPort is the port a service is assumed to listen on when nothing in
// the repository says, with the sentence that explains the assumption.
func defaultPort(c *candidate) (int, string) {
	switch languageOf(c) {
	case "node":
		return 3000, "3000 is what a node server listens on unless told otherwise."
	case "python":
		return 8000, "8000 is what a python server listens on unless told otherwise."
	case "go":
		return 8080, "8080 is what a go server listens on unless told otherwise."
	case "ruby":
		return 3000, "3000 is what a ruby server listens on unless told otherwise."
	}
	return 3000, "3000 is the most common port for a web service, and the language was not recognised."
}

// defaultCommand is the conventional start command for the language, or
// empty when the language has none worth guessing.
func defaultCommand(c *candidate) (string, string) {
	switch c.framework {
	case "django":
		return "python manage.py runserver 0.0.0.0:$PORT",
			"Django applications start with manage.py unless a server such as gunicorn is configured."
	case "rails":
		return "bundle exec rails server -p $PORT",
			"Rails applications start with the rails server command."
	}
	switch languageOf(c) {
	case "node":
		return "npm start", "npm start is what a node application runs when package.json declares no start script of its own."
	case "go":
		return "go run .", "go run . builds and starts the package in the service directory."
	}
	return "", ""
}

// absorbPort records a port proposal, keeping the best evidence and turning a
// disagreement into a conflict the caller can ask about.
func (c *candidate) absorbPort(n int, conf Confidence, why string) {
	if n <= 0 || n >= 65536 {
		return
	}
	if c.port == 0 || conf > c.portConf {
		if c.port != 0 && c.port != n {
			c.portConflicts[c.port] = c.portWhy
		}
		c.port, c.portConf, c.portWhy = n, conf, why
		delete(c.portConflicts, n)
		return
	}
	if n != c.port {
		c.portConflicts[n] = why
	}
}

// Where a service name came from, ranked by how much it identifies the
// application rather than the place it happens to sit. A package manifest
// carries the name the authors chose. A compose key or a Procfile process name
// is usually a role word such as "web". A directory name is the checkout path,
// which is whatever the developer cloned into.
const (
	nameFromDir      = 1
	nameFromProcfile = 2
	nameFromCompose  = 3
	nameFromPackage  = 4
)

func nameRankOf(source string) int {
	switch source {
	case "package":
		return nameFromPackage
	case "compose":
		return nameFromCompose
	case "procfile":
		return nameFromProcfile
	default:
		return nameFromDir
	}
}

// coalesceServices folds candidates that are one service described by several
// sources into one, and returns the surviving order.
//
// The failure it exists to stop: a repository with a Dockerfile and a
// package.json whose name is not the directory name produced two services,
// because the merge key was the service name and every source spells the name
// differently. Docker and the language analyzers name a service after its
// directory, compose after the key in the file, Procfile after the process,
// and Node after the package. A name is a label. The identity of a service in
// a repository is where it is built and run from, plus its role, so that is
// what candidates are grouped on here.
//
// The guard that keeps this from eating real services: one source declaring
// two services in a directory means two services, and a compose file with a
// web and an admin container on the same build context is exactly that. So a
// group is only folded when every source in it contributed exactly one
// candidate, which is the shape of one thing seen several times.
//
// The alternative that lost was renumbering the duplicate's port. That would
// have written a second service that does not exist into a file people commit,
// and it would have passed validation, which is worse than the refusal.
func coalesceServices(byName map[string]*candidate, order []string) []string {
	groups := map[string][]string{}
	var groupOrder []string
	for _, name := range order {
		c := byName[name]
		// An image may borrow runtime evidence from another source in its
		// directory. Orphan migrations are resolved separately below.
		if len(c.declaredBy) == 0 && c.build == nil {
			continue
		}
		key := normalizeDir(c.dir) + "\x00" + string(c.kind)
		if _, seen := groups[key]; !seen {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], name)
	}

	renamed := map[string]string{}
	for _, key := range groupOrder {
		members := groups[key]
		if len(members) < 2 || !oneCandidatePerSource(byName, members) {
			continue
		}
		target := byName[pickName(byName, members)]
		target.dir = normalizeDir(target.dir)
		for _, name := range members {
			if name == target.name {
				continue
			}
			target.absorb(byName[name])
			renamed[name] = target.name
			delete(byName, name)
		}
	}
	if len(renamed) == 0 {
		return order
	}

	// A compose service that depended on a name this pass folded away has to
	// follow it, or the manifest names a service that is not declared and the
	// validator rejects the file af init just wrote.
	for _, c := range byName {
		var deps []string
		for _, d := range c.dependsOn {
			if to, ok := renamed[d]; ok {
				d = to
			}
			if d != c.name {
				deps = appendUnique(deps, d)
			}
		}
		c.dependsOn = deps
	}

	out := make([]string, 0, len(order))
	for _, name := range order {
		if _, gone := renamed[name]; !gone {
			out = append(out, name)
		}
	}
	return out
}

// oneCandidatePerSource reports whether every analyzer in the group declared
// exactly one of its members.
func oneCandidatePerSource(byName map[string]*candidate, members []string) bool {
	count := map[string]int{}
	for _, name := range members {
		for analyzer := range byName[name].declaredBy {
			count[analyzer]++
			if count[analyzer] > 1 {
				return false
			}
		}
	}
	return true
}

// pickName chooses which member's name the folded service keeps: the one from
// the source that identifies the application best, and on a tie the one that
// was found first, so that two runs over the same tree agree.
func pickName(byName map[string]*candidate, members []string) string {
	best := members[0]
	for _, name := range members[1:] {
		if byName[name].nameRank > byName[best].nameRank {
			best = name
		}
	}
	return best
}

// absorb folds another candidate's evidence into this one. Stronger evidence
// wins field by field, which is the same rule the finding loop applies, so a
// Dockerfile's EXPOSE still outranks a framework's default port after folding.
func (c *candidate) absorb(o *candidate) {
	if o.port != 0 {
		c.absorbPort(o.port, o.portConf, o.portWhy)
	}
	for p, why := range o.portConflicts {
		if p != c.port {
			c.portConflicts[p] = why
		}
	}
	if c.build == nil {
		c.build = o.build
	}
	// These describe the Dockerfile, and only one candidate in a group has
	// one, so they follow it. Leaving them behind was silent rather than
	// loud: an inferred context rides inside build and survived the fold on
	// its own, while an AMBIGUOUS context lived only here, so the question
	// that should have been asked disappeared exactly when the Dockerfile was
	// folded into the package that named it, which is the common case.
	if c.contextWhy == "" {
		c.contextWhy = o.contextWhy
	}
	if c.contextAmbiguous == "" {
		c.contextAmbiguous = o.contextAmbiguous
		c.contextDefault = o.contextDefault
	}
	// The command the image runs beats a package script of equal confidence.
	// A Dockerfile that ships a standalone server declares CMD ["node",
	// "server.js"], and "npm run start" would run the framework's dev server
	// against a toolchain the final stage does not contain.
	fromImage := c.build != nil && c.build.Dockerfile != "" && o.cmdEvidence == c.build.Dockerfile
	if o.command != "" && (c.command == "" || o.cmdConf > c.cmdConf || (o.cmdConf == c.cmdConf && fromImage)) {
		c.command, c.cmdConf, c.cmdEvidence = o.command, o.cmdConf, o.cmdEvidence
	}
	if c.framework == "" {
		c.framework = o.framework
	}
	if c.migrate == "" {
		c.migrate = o.migrate
	}
	if c.schedule == "" {
		c.schedule = o.schedule
	}
	if c.cronPath == "" {
		c.cronPath = o.cronPath
	}
	for _, d := range o.dependsOn {
		c.dependsOn = appendUnique(c.dependsOn, d)
	}
	for _, e := range o.evidence {
		c.evidence = appendUnique(c.evidence, e)
	}
	for a := range o.declaredBy {
		c.declaredBy[a] = true
	}
}

// normalizeDir puts every spelling of "the repository root" into one. Compose
// writes a build context of ".", dirOf writes "", and grouping has to see
// those as the same place.
func normalizeDir(dir string) string {
	if dir == "" {
		return ""
	}
	cleaned := path.Clean(dir)
	if cleaned == "." || cleaned == "/" {
		return ""
	}
	return strings.TrimPrefix(cleaned, "./")
}

// reassignRepoWideMigration transfers a migration only to a unique local
// service. A command found elsewhere needs the user's explicit image choice.
func reassignRepoWideMigration(byName map[string]*candidate, order []string, questions *[]Question) {
	for _, name := range order {
		orphan := byName[name]
		if orphan.migrate == "" || len(orphan.declaredBy) != 0 {
			continue
		}
		var owners []*candidate
		var options []string
		for _, other := range order {
			c := byName[other]
			if len(c.declaredBy) == 0 || c.migrate != "" {
				continue
			}
			options = append(options, c.name)
			if normalizeDir(c.dir) == normalizeDir(orphan.dir) {
				owners = append(owners, c)
			}
		}
		if len(owners) == 1 {
			owners[0].migrate = orphan.migrate
		} else {
			*questions = append(*questions, Question{
				ID: "migration." + name + ".service", Migration: orphan.migrate,
				Prompt:  fmt.Sprintf("Which service image can run %s?", orphan.migrate),
				Options: append(options, "manual:configure"),
				Why:     "The migration was found outside a unique service directory. Its image must contain the migration tool and files. Choose manual only if you will configure migrations separately before af up.",
			})
		}
		orphan.migrate = ""
	}
}

func sortedValues(m map[int]string) []string {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if m[k] != "" {
			out = append(out, m[k])
		}
	}
	return out
}

func mergeDatabase(findings []Finding, services []schema.Service, questions *[]Question) *schema.Database {
	db := &schema.Database{Provider: schema.DBDocker, Version: 17}

	foundPostgres := false
	supabase := false
	for _, f := range OfKind(findings, KindDatabase) {
		switch f.Subject {
		case "postgres":
			foundPostgres = true
			if f.Value == "supabase" {
				supabase = true
			}
		case "mysql", "mongodb":
			*questions = append(*questions, Question{
				ID:      "database.unsupported",
				Prompt:  fmt.Sprintf("This project appears to use %s, which Antifailure does not manage yet. Continue with Postgres only?", f.Subject),
				Options: []string{"yes", "no"},
				Default: "yes",
				Why:     f.Detail,
			})
		}
	}
	if supabase {
		db.Provider = schema.DBSupabase
	}
	if !foundPostgres {
		*questions = append(*questions, Question{
			ID:      "database.present",
			Prompt:  "Does this application use Postgres?",
			Options: []string{"yes", "no"},
			Default: "yes",
			Why:     "No Postgres connection string, Prisma datasource, or compose service was found.",
		})
	}

	// The variable holding the production connection string. Naming it is what
	// lets a golden refresh find the source without ever storing it.
	for _, f := range OfKind(findings, KindEnvVar) {
		if isDatabaseURLName(f.Subject) {
			db.URLEnv = f.Subject
			break
		}
	}
	if db.URLEnv == "" {
		db.URLEnv = "DATABASE_URL"
	}
	// The variable naming PRODUCTION, when the repository already has one.
	// A .env.example listing PRODUCTION_DATABASE_URL is a repository that has
	// already decided what the source is called, and writing that name into
	// database.source_url_env is what lets af init draft the masking rules
	// from the real schema in the same run.
	for _, f := range OfKind(findings, KindEnvVar) {
		if isProductionURLName(f.Subject) {
			db.SourceURLEnv = f.Subject
			break
		}
	}

	// Migration commands live on services, so the database section only needs
	// to know a golden refresh is possible.
	for _, s := range services {
		if s.Migrate != "" {
			break
		}
	}
	return db
}

// isProductionURLName recognises the names a repository gives the read only
// connection string of production, as distinct from the one the application
// itself reads.
func isProductionURLName(name string) bool {
	switch name {
	case "PRODUCTION_DATABASE_URL", "PROD_DATABASE_URL", "PRODUCTION_DB_URL",
		"PROD_DB_URL", "SOURCE_DATABASE_URL", "DATABASE_URL_PRODUCTION",
		"DATABASE_URL_PROD", "PRODUCTION_POSTGRES_URL", "PROD_POSTGRES_URL":
		return true
	}
	return false
}

func isDatabaseURLName(name string) bool {
	switch name {
	case "DATABASE_URL", "POSTGRES_URL", "POSTGRESQL_URL", "PG_URL",
		"DATABASE_URI", "POSTGRES_PRISMA_URL", "DB_URL":
		return true
	}
	return false
}

func mergeEgress(findings []Finding) (*schema.Egress, []DetectedEmulator) {
	e := &schema.Egress{Default: schema.ModeBlock}
	seen := map[string]bool{}
	// A provider's webhook path belongs on the host that serves its API, not
	// on every host it happens to use. Repeating it on a CDN host would
	// register three forwarders for one provider and deliver every event
	// three times.
	webhookClaimed := map[string]bool{}

	for _, f := range OfKind(findings, KindThirdParty) {
		if seen[f.Subject] {
			continue
		}
		seen[f.Subject] = true
		rule := schema.EgressRule{
			Host: f.Subject,
			Mode: schema.Mode(f.Value),
			Note: f.Extra["why"],
		}
		if rule.Mode == schema.ModeSandbox {
			rule.Credential = f.Extra["credential"]
			// A sandbox rule with no credential to check against the live key
			// formats is refused by the manifest validator, so a provider with
			// no conventional variable name falls back to mock, which is safe
			// and needs nothing.
			if rule.Credential == "" {
				rule.Mode = schema.ModeMock
				rule.Note = f.Extra["why"] + " Sandbox needs a credential variable, so this starts as mock."
			}
		}
		if wp := f.Extra["webhook_path"]; wp != "" {
			provider := f.Extra["provider"]
			if !webhookClaimed[provider] {
				webhookClaimed[provider] = true
				rule.WebhookPath = wp
			}
		}
		e.Rules = append(e.Rules, rule)
	}
	emulators := mergeEmulators(findings, e, seen)
	sort.SliceStable(e.Rules, func(i, j int) bool { return e.Rules[i].Host < e.Rules[j].Host })
	return e, emulators
}

// mergeEmulators turns a cloud emulator running in the repository's own
// compose file into the egress rules for the cloud it answers for.
//
// An emulator's own configuration is better evidence about which services an
// application uses than its dependency list is, because somebody configured it
// rather than installed it: LocalStack with SERVICES=s3,sqs,sns is a developer
// stating that this application calls exactly those three. It is also the
// thing this product replaces, since reaching an emulator costs an endpoint
// override and the override means the code under test is not the code that
// ships. So the emulator is read as the roster, and every service on it gets
// the catalog's rule for the real host, with the catalog's own reason.
//
// A host the dependency scan already claimed is left alone rather than added
// twice. Two rules for one host is a manifest the validator refuses, and the
// first one carries the same reason as the second would.
//
// The mode is the catalog's, which today is block for every cloud service
// this can reach. That is the honest answer while nothing answers for the
// service in the environment: a rule promising something else would be a
// promise the engine cannot keep.
func mergeEmulators(findings []Finding, e *schema.Egress, seen map[string]bool) []DetectedEmulator {
	found := OfKind(findings, KindEmulator)
	if len(found) == 0 {
		return nil
	}
	out := make([]DetectedEmulator, 0, len(found))
	for _, f := range found {
		em := DetectedEmulator{
			Product:  f.Extra["product"],
			Cloud:    f.Subject,
			Service:  f.Extra["service"],
			Image:    f.Value,
			Evidence: f.Evidence,
			Services: emulatorTokens(f.Extra["services"]),
		}
		for _, tp := range thirdPartiesForCloud(em.Cloud, em.Services) {
			for _, host := range tp.Hosts {
				if seen[host] {
					continue
				}
				seen[host] = true
				em.Hosts = append(em.Hosts, host)
				e.Rules = append(e.Rules, schema.EgressRule{
					Host: host,
					Mode: schema.Mode(tp.Mode),
					Note: tp.Why,
				})
			}
		}
		em.Unnamed = unclaimedTokens(em.Cloud, em.Services)
		out = append(out, em)
	}
	// Findings arrive sorted, but two emulators in one compose file must not
	// swap places between runs because a map iteration inside the analyzer
	// changed, and the note is read by a person who runs af init twice.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Evidence != out[j].Evidence {
			return out[i].Evidence < out[j].Evidence
		}
		return out[i].Service < out[j].Service
	})
	return out
}

// mergeAuth turns the authentication finding into the manifest's auth block.
//
// Absent when nothing was recognised, which is the right answer rather than a
// gap: with no block the engine picks the adapter from the live schema at run
// time, and that is better evidence than a dependency list. Writing
// `adapter: auto` into every manifest would be noise that says nothing.
func mergeAuth(findings []Finding) *schema.Auth {
	found := OfKind(findings, KindAuth)
	if len(found) == 0 {
		return nil
	}
	f := found[0]

	auth := &schema.Auth{Adapter: schema.AuthAdapter(f.Value)}
	if f.Extra["hosted"] == "true" {
		auth.TokenEnv = f.Extra["token_env"]
		// Deliberately left false, and this is the important line in the
		// function. A hosted adapter refuses to create anybody until somebody
		// says the tenant is not production, and writing sandbox: true here
		// would make that decision on their behalf, from a dependency list,
		// for the one setting whose whole purpose is that a person confirmed
		// it. AF-DB-020 asks for it by name when the run reaches that point.
		auth.Sandbox = false
	}
	return auth
}

// defaultPersonas returns the two accounts nearly every application needs, so
// that a first af test has someone to log in as.
func defaultPersonas() []schema.Persona {
	return []schema.Persona{
		{Name: "owner", Email: "owner@example.test", Role: "admin", Login: schema.LoginPassword},
		{Name: "member", Email: "member@example.test", Role: "member", Login: schema.LoginPassword},
	}
}

// suggestedWorkflows proposes workflows based on what the repository suggests
// the application does. They are starting points a user edits, and each one is
// written the way a person would describe the task.
func suggestedWorkflows(findings []Finding) []schema.Workflow {
	hasBilling, hasAuth, hasMail := false, false, false
	for _, f := range OfKind(findings, KindThirdParty) {
		switch f.Extra["provider"] {
		case "Stripe":
			hasBilling = true
		case "Clerk", "Auth0", "Supabase":
			hasAuth = true
		case "SendGrid", "Resend", "Postmark", "Mailgun", "Amazon SES":
			hasMail = true
		}
	}

	out := []schema.Workflow{{
		Name:    "sign-up",
		Persona: "owner",
		Description: "Sign up for a new account with a fresh email address. " +
			"Complete every required field, submit the form, and confirm that you land on a signed in page " +
			"rather than back on the form with an error.",
		Expect: []string{"The account is created and the session is signed in."},
	}}
	if hasMail {
		out[0].Description += " Then confirm that a welcome email arrives."
		out[0].Expect = append(out[0].Expect, "A welcome message arrives in the inbox.")
	}
	if hasAuth {
		out = append(out, schema.Workflow{
			Name:    "sign-in",
			Persona: "member",
			Description: "Sign in with the existing member account, then sign out again. " +
				"Confirm that signing out returns you to a page that no longer shows account details.",
			Expect: []string{"Signing in reaches the application. Signing out ends the session."},
		})
	}
	if hasBilling {
		out = append(out, schema.Workflow{
			Name:    "subscribe",
			Persona: "owner",
			Description: "Open the pricing or billing page, choose a paid plan, and complete checkout with the standard test card. " +
				"Confirm that the account shows the paid plan afterwards, not a pending or failed state.",
			Expect: []string{"The account shows the paid plan after checkout completes."},
		})
	}
	return out
}

func appendUnique(s []string, v string) []string {
	if v == "" {
		return s
	}
	for _, existing := range s {
		if existing == v {
			return s
		}
	}
	return append(s, v)
}
