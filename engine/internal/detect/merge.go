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
func Merge(findings []Finding, root string) (*schema.Manifest, []Question) {
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
	m.Egress = mergeEgress(findings)
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
	return m, questions
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
	migrate   string
	build     *schema.Build
	schedule  string
	cronPath  string
	dependsOn []string
	evidence  []string
	// portConflicts records ports other analyzers proposed, which is what
	// turns a disagreement into a question rather than a silent choice.
	portConflicts map[int]string
}

func mergeServices(findings []Finding, questions *[]Question) []schema.Service {
	byName := map[string]*candidate{}
	order := []string{}

	get := func(name string) *candidate {
		if c, ok := byName[name]; ok {
			return c
		}
		c := &candidate{name: name, kind: schema.ServiceWeb, portConflicts: map[int]string{}}
		byName[name] = c
		order = append(order, name)
		return c
	}

	for _, f := range findings {
		switch f.Kind {
		case KindService:
			c := get(f.Subject)
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
			c.kind = schema.ServiceWorker
			if f.Value != "" && c.command == "" {
				c.command, c.cmdConf = f.Value, f.Confidence
			}
			if d := f.Extra["dir"]; d != "" && c.dir == "" {
				c.dir = d
			}
			c.evidence = appendUnique(c.evidence, f.Evidence)

		case KindCron:
			c := get(f.Subject)
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
			if c.port == 0 || f.Confidence > c.portConf {
				if c.port != 0 && c.port != n {
					c.portConflicts[c.port] = c.portWhy
				}
				c.port, c.portConf, c.portWhy = n, f.Confidence, f.Detail
			} else if n != c.port {
				c.portConflicts[n] = f.Detail
			}

		case KindCommand:
			c := get(f.Subject)
			if c.command == "" || f.Confidence > c.cmdConf {
				c.command, c.cmdConf = f.Value, f.Confidence
			}

		case KindMigration:
			c := get(f.Subject)
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
				c.build = b
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

	// A migration command found for the repository as a whole belongs to the
	// web service that will run it, not to a phantom service of its own.
	reassignRepoWideMigration(byName, order)

	out := make([]schema.Service, 0, len(order))
	for _, name := range order {
		c := byName[name]
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
				*questions = append(*questions, Question{
					ID:     "service." + c.name + ".port",
					Prompt: fmt.Sprintf("Which port does %s listen on?", c.name),
					Why:    "No port was found in the code, a Dockerfile, or a compose file.",
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
		if c.command == "" && c.build == nil && c.kind != schema.ServiceCron {
			*questions = append(*questions, Question{
				ID:     "service." + c.name + ".command",
				Prompt: fmt.Sprintf("What command starts %s?", c.name),
				Why:    "No start script, Dockerfile command, or Procfile entry was found.",
			})
		}
	}
	return out
}

// reassignRepoWideMigration moves a migration command that was attributed to
// the repository onto the service most likely to own it.
func reassignRepoWideMigration(byName map[string]*candidate, order []string) {
	var orphan *candidate
	for _, name := range order {
		c := byName[name]
		if c.migrate != "" && c.port == 0 && c.command == "" && c.framework == "" && c.build == nil {
			orphan = c
			break
		}
	}
	if orphan == nil {
		return
	}
	for _, name := range order {
		c := byName[name]
		if c == orphan || c.kind != schema.ServiceWeb || c.migrate != "" {
			continue
		}
		c.migrate = orphan.migrate
		orphan.migrate = ""
		return
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

	// Migration commands live on services, so the database section only needs
	// to know a golden refresh is possible.
	for _, s := range services {
		if s.Migrate != "" {
			break
		}
	}
	return db
}

func isDatabaseURLName(name string) bool {
	switch name {
	case "DATABASE_URL", "POSTGRES_URL", "POSTGRESQL_URL", "PG_URL",
		"DATABASE_URI", "POSTGRES_PRISMA_URL", "DB_URL":
		return true
	}
	return false
}

func mergeEgress(findings []Finding) *schema.Egress {
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
	sort.SliceStable(e.Rules, func(i, j int) bool { return e.Rules[i].Host < e.Rules[j].Host })
	return e
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
