package mcp

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/oracle"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The four analysis tools answer the questions an agent asks around a change
// rather than about one artifact: what does this failure mean, what is this
// project actually configured to do, which checks does my diff need, and does
// the data still hold after the change ran.

// analyseChange classifies the diff between two refs.
type analyseChange func(ctx context.Context, base, head string) (*change.Profile, error)

// runInvariants asks the manifest's invariants of the environment's database.
//
// The second return says whether they could be asked at all, for the same
// reason the decision log has one: no violations found and nobody able to look
// are opposite facts that reduce to the same empty list.
type runInvariants func(ctx context.Context) (results []env.InvariantResult, available bool, err error)

// compareReleases runs the differential oracle against a baseline revision.
type compareReleases func(ctx context.Context, baseRef string) (*env.OracleResult, error)

// The output bounds for these tools. Each list grows with the repository
// rather than with the run, so each is cut at the point where a reader has
// stopped reading and the total is always stated.
const (
	maxCatalogMatches   = 12
	maxFactsReported    = 60
	maxPathsReported    = 40
	maxInvariantsShown  = 60
	maxProbesReported   = 40
	maxRulesInConfig    = 60
	maxServicesInConfig = 40
)

// gitRefPattern is what a caller may name as a ref.
//
// Anchored, and it cannot start with a dash. That is the whole point of it
// rather than a tidiness rule: the ref is joined into "base...head" and handed
// to git as one argv element, so a value beginning with a dash would be read by
// git as an option rather than as a revision. There is no shell anywhere on
// that path, so this is the only place that particular door is closed.
const gitRefPattern = `[A-Za-z0-9][A-Za-z0-9._/@^~-]{0,199}`

// repositoryPathPattern is what a path out of a diff has to look like to be
// repeated verbatim.
//
// The same argument as identifierPattern in untrusted.go, widened by the
// separator and nothing else. A path is chosen by whoever opened the pull
// request, and a "path" with a space in it is not a path this server will
// repeat into a document a model reads. The cost is a legitimate file name
// containing a space, which af change still shows.
var repositoryPathPattern = regexp.MustCompile(`^[A-Za-z0-9_./@+-]{1,256}$`)

// withheldPath replaces a path that is not one.
const withheldPath = "(withheld: not a plain repository path)"

// safePath repeats a path from the diff only if it really is one.
func safePath(s string) (string, bool) {
	cleaned := neutralize(s, 512)
	if repositoryPathPattern.MatchString(cleaned) {
		return cleaned, true
	}
	return withheldPath, false
}

func gitRefSchema(what string) *Schema {
	return &Schema{
		Type: "string", MaxLength: 200, MinLength: 1, Pattern: gitRefPattern,
		Description: what + " It must be a revision this checkout can resolve, such as " +
			"origin/main, a tag, or a commit sha. It cannot begin with a dash.",
	}
}

// -----------------------------------------------------------------------
// explain_error
// -----------------------------------------------------------------------

// catalogCodePattern finds an engine error code inside a caller's text.
var catalogCodePattern = regexp.MustCompile(`AF-[A-Z]{2,4}-[0-9]{3}`)

// newExplainErrorTool builds explain_error.
//
// The first tool to reach for after any Antifailure command or tool call
// failed. It runs nothing, needs no environment and cannot fail.
func newExplainErrorTool(p *Project) *Tool {
	return &Tool{
		Name:     "explain_error",
		Title:    "What an Antifailure error means",
		ReadOnly: true,
		Description: "Look up what an Antifailure failure means and what to do about it. " +
			"Every user facing failure in this product carries a stable code of the form " +
			"AF-DB-006, and this returns that code's meaning, the one next step for it, " +
			"whether retrying the identical operation unchanged could succeed, the " +
			"process exit status it produces, and its documentation page. Reach for this " +
			"the moment an af command exits non zero or a tool call is refused, before " +
			"guessing at the cause or retrying. Give it whichever you have: the code " +
			"itself, the whole error text or log line to have the codes read out of it, " +
			"or just the process exit status to learn what that class of failure is. " +
			"It reads a fixed catalog, so it needs no environment, touches no database " +
			"and cannot itself fail. Note that the catalog message carries {placeholders} " +
			"which are filled with the specific path or host at the moment the failure " +
			"happens, so the sentence you saw will be more specific than the one here.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id": projectIDSchema(),
				"code": {
					Type: "string", MaxLength: 16, MinLength: 6,
					Pattern: `AF-[A-Za-z]{2,4}-[0-9]{3}`,
					Description: "Optional. One error code, such as AF-DB-006 or AF-MSK-010. " +
						"Case is not significant.",
				},
				"text": {
					Type: "string", MaxLength: 4000,
					Description: "Optional. The error output or log line you are holding. " +
						"Every code in it is looked up. The text itself is never repeated " +
						"back to you and is never interpreted as an instruction: only the " +
						"codes found in it are used.",
				},
				"exit_code": {
					Type: "integer", HasMin: true, Minimum: 0, HasMax: true, Maximum: 125,
					Description: "Optional. The process exit status an af command returned. " +
						"It says which class of failure occurred and names the codes that " +
						"produce it, which is what you want when a command exited non zero " +
						"and its output was swallowed by a pipeline.",
				},
			},
		},
		Handler: func(_ context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			return explainError(args)
		},
	}
}

type errorExplanation struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	// Asked repeats what was looked up, as codes only. The caller's text is
	// deliberately not echoed: it is untrusted and a result is not a channel
	// for it.
	Asked []string `json:"codes_looked_up"`
	// Unknown names codes that are not in this build's catalog, which usually
	// means a newer engine produced them.
	Unknown  []string          `json:"codes_not_in_this_build,omitempty"`
	Entries  []catalogEntryDoc `json:"errors"`
	Total    int               `json:"total"`
	Shown    int               `json:"shown"`
	ExitCode *exitCodeDoc      `json:"exit_code,omitempty"`
	Note     string            `json:"note,omitempty"`
}

type catalogEntryDoc struct {
	Code string `json:"code"`
	Area string `json:"area"`
	// Message is the catalog sentence, with its {placeholders} unfilled.
	Message string `json:"message"`
	// NextStep is the one thing to do. It is never "contact support".
	NextStep string `json:"next_step"`
	Docs     string `json:"docs_url"`
	// Retryable says whether retrying the identical operation unchanged could
	// succeed. A false here means a retry is wasted work.
	Retryable bool `json:"retryable"`
	ExitCode  int  `json:"exit_code"`
	// ExitMeaning is what that status means as a class.
	ExitMeaning string `json:"exit_code_means"`
}

type exitCodeDoc struct {
	Code  int    `json:"code"`
	Means string `json:"means"`
	// Codes are the catalog entries that exit with this status, bounded.
	Codes     []string `json:"produced_by,omitempty"`
	Total     int      `json:"produced_by_total"`
	Truncated bool     `json:"produced_by_truncated"`
}

func explainError(args map[string]any) (any, *Fault) {
	out := errorExplanation{
		Kind: "error_explanation", Asked: []string{}, Entries: []catalogEntryDoc{},
	}

	// The catalog, keyed for a lookup that can say no. Lookup itself answers a
	// placeholder entry for an unknown code, which is right on an error path
	// and wrong here: a tool that invents an entry for a code nobody defined
	// would be telling a caller something untrue with total confidence.
	known := map[aferrors.Code]aferrors.Entry{}
	for _, e := range aferrors.All() {
		known[e.Code] = e
	}

	wanted := make([]aferrors.Code, 0, 4)
	seen := map[aferrors.Code]bool{}
	add := func(raw string) {
		c := aferrors.Code(strings.ToUpper(strings.TrimSpace(raw)))
		if c == "" || seen[c] {
			return
		}
		seen[c] = true
		wanted = append(wanted, c)
	}

	if code, ok := args["code"].(string); ok {
		add(code)
	}
	if text, ok := args["text"].(string); ok {
		for _, m := range catalogCodePattern.FindAllString(strings.ToUpper(text), maxCatalogMatches*4) {
			add(m)
		}
	}

	for _, c := range wanted {
		out.Asked = append(out.Asked, string(c))
		entry, found := known[c]
		if !found {
			out.Unknown = append(out.Unknown, string(c))
			continue
		}
		out.Entries = append(out.Entries, describeCatalogEntry(entry))
	}
	out.Total = len(out.Entries)
	if len(out.Entries) > maxCatalogMatches {
		out.Entries = out.Entries[:maxCatalogMatches]
		out.Note = fmt.Sprintf(
			"%d codes were found and the first %d are explained. Ask about the rest by "+
				"code.", out.Total, maxCatalogMatches)
	}
	out.Shown = len(out.Entries)

	if raw, present := args["exit_code"]; present {
		n, err := toInt(raw)
		if err == nil {
			out.ExitCode = describeExitCode(n)
		}
	}

	out.Summary = explainErrorSummary(out)
	return out, nil
}

func describeCatalogEntry(e aferrors.Entry) catalogEntryDoc {
	return catalogEntryDoc{
		Code: string(e.Code), Area: e.Area,
		// The catalog is written in this repository and shipped in the binary,
		// so it is engine prose rather than candidate text. It is bounded
		// anyway, because a result that trusts one field is a result with one
		// unbounded field in it.
		Message:     neutralize(e.Message, 500),
		NextStep:    neutralize(e.NextStep, 500),
		Docs:        "https://antifailure.dev/docs/" + neutralize(e.Docs, 200),
		Retryable:   e.Retryable,
		ExitCode:    int(e.ExitCode),
		ExitMeaning: exitCodeMeaning(int(e.ExitCode)),
	}
}

func describeExitCode(n int) *exitCodeDoc {
	doc := &exitCodeDoc{Code: n, Means: exitCodeMeaning(n)}
	for _, e := range aferrors.All() {
		if int(e.ExitCode) != n {
			continue
		}
		doc.Total++
		if len(doc.Codes) < maxCatalogMatches {
			doc.Codes = append(doc.Codes, string(e.Code))
			continue
		}
		doc.Truncated = true
	}
	return doc
}

// exitCodeMeaning names one process exit status.
//
// The registry in engine/internal/errors says these are part of the public
// interface because scripts branch on them, so the words here describe the
// same classes rather than inventing a second vocabulary for them.
func exitCodeMeaning(n int) string {
	switch aferrors.ExitCode(n) {
	case aferrors.ExitSuccess:
		return "success"
	case aferrors.ExitFailure:
		return "a generic failure with no more specific class"
	case aferrors.ExitUsage:
		return "the command was invoked wrongly"
	case aferrors.ExitConfiguration:
		return "the manifest or the configuration is wrong, so fix the file rather than retrying"
	case aferrors.ExitAuth:
		return "authentication or authorisation failed"
	case aferrors.ExitProvider:
		return "a provider failed, and this class is usually worth retrying"
	case aferrors.ExitPolicyDenied:
		return "the project's own policy refused it, which is the gate working rather than breaking"
	case aferrors.ExitVerification:
		return "a verification failed, such as masking that left something that still looks real"
	case aferrors.ExitTestFailure:
		return "the checks ran and something about the change failed them"
	case aferrors.ExitInterruptedClean:
		return "it was interrupted and everything it created was removed"
	case aferrors.ExitInterruptedDirty:
		return "it was interrupted with resources still pending cleanup, so run af down or af env reap"
	default:
		return "not a status this build's catalog assigns"
	}
}

func explainErrorSummary(out errorExplanation) string {
	var b strings.Builder
	switch {
	case len(out.Asked) == 0 && out.ExitCode == nil:
		return "Nothing was asked about. Pass a code such as AF-DB-006, the error text to " +
			"read codes out of, or the process exit status."
	case len(out.Asked) == 0:
		fmt.Fprintf(&b, "No Antifailure error code was given or found. ")
	case len(out.Entries) == 1:
		e := out.Entries[0]
		fmt.Fprintf(&b, "%s is a %s error. %s Retrying the identical operation %s. ",
			e.Code, e.Area, e.NextStep,
			retryWord(e.Retryable))
	default:
		fmt.Fprintf(&b, "%d %s explained below, worst read first in the order you gave them. ",
			out.Shown, plural(out.Shown, "code is", "codes are"))
	}
	if len(out.Unknown) > 0 {
		fmt.Fprintf(&b,
			"%s not in this build's catalog, which usually means a newer engine produced "+
				"it; check the version. ", strings.Join(out.Unknown, ", "))
	}
	if out.ExitCode != nil {
		fmt.Fprintf(&b, "Exit status %d means %s. ", out.ExitCode.Code, out.ExitCode.Means)
	}
	if len(out.Asked) > 0 && len(out.Entries) == 0 && len(out.Unknown) == 0 {
		b.WriteString("Not every failure carries a code: a crash, a runtime error inside " +
			"your own application and a network timeout all reach you without one. ")
	}
	return strings.TrimSpace(b.String())
}

func retryWord(retryable bool) string {
	if retryable {
		return "unchanged could succeed"
	}
	return "unchanged will fail the same way, so change something first"
}

// -----------------------------------------------------------------------
// explain_effective_configuration
// -----------------------------------------------------------------------

// newExplainConfigTool builds explain_effective_configuration.
func newExplainConfigTool(p *Project) *Tool {
	return &Tool{
		Name:     "explain_effective_configuration",
		Title:    "What this project is configured to do",
		ReadOnly: true,
		Description: "Report the settings this project actually runs under, with every " +
			"default filled in. The most common configuration bug is a default nobody " +
			"knew about, so this answers questions of the form why is it blocking that " +
			"host, why did that check not run, and what threshold decided that verdict, " +
			"without anybody reading antifailure.yaml and guessing at what it omits. " +
			"Use it before proposing a manifest change, and after a surprising verdict. " +
			"It reads the loaded manifest only: nothing is started, no database is " +
			"touched and it works with no environment running. " +
			"It never reports a secret. Variable names and where a value would come from " +
			"are configuration; the values are not, and none of them passes through here. " +
			"The text of a migrate or seed command, an invariant's SQL and an oracle " +
			"probe's body are also withheld, because they are free form text from the " +
			"repository and this result is read by a model.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id": projectIDSchema(),
				"section": {
					Type: "string", MaxLength: 20,
					Enum: []string{
						"all", "services", "database", "egress", "checks",
						"policy", "personas", "invariants", "workflows",
					},
					Description: "Optional, defaulting to all. Narrow the answer to one part: " +
						"services is the processes the environment runs; database is the " +
						"provider, the golden and the masking rules file; egress is the " +
						"network policy and the order its rules decide in; checks is which " +
						"of the migration rehearsal, the query comparison, the oracle and " +
						"the fidelity inventory are on and what they are tuned to; policy is " +
						"the thresholds and levels that decide a verdict; personas, " +
						"invariants and workflows are the declared accounts, data questions " +
						"and browser journeys by name.",
				},
			},
		},
		Handler: func(_ context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			section, _ := args["section"].(string)
			return describeConfiguration(p, section), nil
		},
	}
}

type configurationDoc struct {
	Kind        string `json:"kind"`
	Summary     string `json:"summary"`
	Application string `json:"application"`
	// ManifestVersion is the schema version of antifailure.yaml, not the
	// engine's version.
	ManifestVersion int    `json:"manifest_version"`
	Section         string `json:"section"`

	Services   []declaredServiceDoc   `json:"services,omitempty"`
	Database   *databaseDoc           `json:"database,omitempty"`
	Egress     *egressConfigDoc       `json:"egress,omitempty"`
	Checks     *checksDoc             `json:"checks,omitempty"`
	Policy     *policyDoc             `json:"policy,omitempty"`
	Personas   []personaDoc           `json:"personas,omitempty"`
	Invariants []declaredInvariantDoc `json:"invariants,omitempty"`
	Workflows  []string               `json:"workflows,omitempty"`

	// Withheld names what this result deliberately does not carry, so that an
	// absence is never read as "the manifest does not set it".
	Withheld []string `json:"withheld"`
}

type declaredServiceDoc struct {
	Name          string   `json:"name"`
	Kind          string   `json:"kind,omitempty"`
	Port          int      `json:"port,omitempty"`
	Path          string   `json:"path,omitempty"`
	Build         string   `json:"build_strategy,omitempty"`
	HealthPath    string   `json:"health_path,omitempty"`
	HealthTimeout string   `json:"health_timeout,omitempty"`
	Schedule      string   `json:"schedule,omitempty"`
	HasMigrate    bool     `json:"runs_migrations"`
	EnvVars       []string `json:"env_var_names,omitempty"`
	DependsOn     []string `json:"depends_on,omitempty"`
}

type databaseDoc struct {
	Provider string `json:"provider,omitempty"`
	Version  int    `json:"postgres_version,omitempty"`
	// URLEnv and SourceURLEnv are variable NAMES. The values are credentials
	// and never appear here.
	URLEnv       string `json:"url_injected_as,omitempty"`
	SourceURLEnv string `json:"source_url_from_variable,omitempty"`
	APIKeyEnv    string `json:"api_key_from_variable,omitempty"`
	MaskingRules string `json:"masking_rules_file,omitempty"`
	Project      string `json:"provider_project,omitempty"`
	MaxBranches  int    `json:"max_branches,omitempty"`
	HasGolden    bool   `json:"golden_configured"`
	HasSubset    bool   `json:"subset_configured"`
	HasSeed      bool   `json:"seed_configured"`
}

type egressConfigDoc struct {
	Default   string          `json:"default_mode"`
	AllowIPv6 bool            `json:"allow_ipv6"`
	Rules     []egressRuleDoc `json:"rules"`
	Total     int             `json:"total_rules"`
	Truncated bool            `json:"truncated"`
	Note      string          `json:"note"`
}

type checksDoc struct {
	Insights *insightsConfigDoc `json:"insights,omitempty"`
	Oracle   *oracleConfigDoc   `json:"oracle,omitempty"`
	Fidelity *fidelityConfigDoc `json:"fidelity,omitempty"`
	Off      []string           `json:"turned_off,omitempty"`
}

type insightsConfigDoc struct {
	Enabled            bool    `json:"enabled"`
	MigrationRehearsal bool    `json:"migration_rehearsal"`
	QueryRegression    bool    `json:"query_regression"`
	PlanDiff           bool    `json:"plan_diff"`
	RegressionFactor   float64 `json:"regression_factor,omitempty"`
	RegressionMinMS    float64 `json:"regression_min_ms,omitempty"`
	LargeTableRows     int     `json:"large_table_rows,omitempty"`
	RollingWhen        string  `json:"rolling_compatibility_when,omitempty"`
	RollingAgainst     string  `json:"rolling_compatibility_against,omitempty"`
}

type oracleConfigDoc struct {
	Enabled  bool   `json:"enabled"`
	Baseline string `json:"baseline,omitempty"`
	BaseRef  string `json:"base_ref,omitempty"`
	// FailOn is the lowest severity that fails the comparison. Empty means
	// none, which reports every difference and fails on none of them.
	FailOn     string   `json:"fail_on"`
	ProbeNames []string `json:"probe_names,omitempty"`
	ProbeCount int      `json:"probe_count"`
}

type fidelityConfigDoc struct {
	Enabled bool     `json:"enabled"`
	Require []string `json:"require,omitempty"`
}

type policyDoc struct {
	LockWarnMS float64 `json:"migration_lock_warn_ms"`
	LockFailMS float64 `json:"migration_lock_fail_ms"`
	Levels     []levelDoc
}

type levelDoc struct {
	Rule  string `json:"rule"`
	Level string `json:"level"`
}

type personaDoc struct {
	Name  string `json:"name"`
	Login string `json:"login,omitempty"`
}

type declaredInvariantDoc struct {
	Name string `json:"name"`
	// Description is the manifest's own sentence. The SQL is not reproduced.
	Description string `json:"description,omitempty"`
	SQLWithheld bool   `json:"sql_withheld"`
}

func describeConfiguration(p *Project, section string) *configurationDoc {
	if section == "" {
		section = "all"
	}
	m := p.Manifest
	out := &configurationDoc{
		Kind: "effective_configuration", Section: section,
		Application:     neutralize(m.Name, 200),
		ManifestVersion: m.Version,
		Withheld: []string{
			"every secret value, because a value is a credential and a variable name is not",
			"the text of migrate, seed and service commands, and of every invariant's SQL, " +
				"because they are free form text from the repository",
			"oracle probe headers and bodies, for the same reason",
		},
	}
	want := func(name string) bool { return section == "all" || section == name }

	if want("services") {
		for i, s := range m.Services {
			if i >= maxServicesInConfig {
				break
			}
			out.Services = append(out.Services, describeService(s))
		}
	}
	if want("database") && m.Database != nil {
		d := m.Database
		out.Database = &databaseDoc{
			Provider: string(d.Provider), Version: d.Version,
			URLEnv:       neutralize(d.URLEnv, 200),
			SourceURLEnv: neutralize(d.SourceURLEnv, 200),
			APIKeyEnv:    neutralize(d.APIKeyEnv, 200),
			Project:      neutralize(d.Project, 200),
			MaxBranches:  d.MaxBranches,
			HasGolden:    d.Golden != nil,
			HasSubset:    d.Subset != nil,
			HasSeed:      d.Seed != "",
		}
		if path, ok := safePath(d.MaskingRules); d.MaskingRules != "" && ok {
			out.Database.MaskingRules = path
		}
	}
	if want("egress") {
		out.Egress = describeEgressConfig(m.Egress)
	}
	if want("checks") {
		out.Checks = describeChecks(m)
	}
	if want("policy") {
		out.Policy = describePolicyConfig(p.Gate)
	}
	if want("personas") {
		for _, ps := range m.Personas {
			out.Personas = append(out.Personas, personaDoc{
				Name: neutralize(ps.Name, maxIdentifierBytes), Login: string(ps.Login),
			})
		}
	}
	if want("invariants") {
		for i, inv := range m.Invariants {
			if i >= maxInvariantsShown {
				break
			}
			out.Invariants = append(out.Invariants, declaredInvariantDoc{
				Name:        neutralize(inv.Name, maxIdentifierBytes),
				Description: neutralize(inv.Description, 300),
				SQLWithheld: true,
			})
		}
	}
	if want("workflows") {
		for _, w := range m.Workflows {
			out.Workflows = append(out.Workflows, neutralize(w.Name, maxIdentifierBytes))
		}
	}
	out.Summary = configurationSummary(p, out, section)
	return out
}

func describeService(s schema.Service) declaredServiceDoc {
	doc := declaredServiceDoc{
		Name: neutralize(s.Name, maxIdentifierBytes), Kind: string(s.Kind),
		Port:          s.Port,
		HealthPath:    neutralize(s.HealthPath, 200),
		HealthTimeout: neutralize(s.HealthTimeout, 40),
		Schedule:      neutralize(s.Schedule, 100),
		HasMigrate:    s.Migrate != "",
	}
	if path, ok := safePath(s.Path); s.Path != "" && ok {
		doc.Path = path
	}
	if s.Build != nil {
		doc.Build = string(s.Build.Strategy)
	}
	for _, e := range s.Env {
		// The NAME and whether the slot is a sandbox one. Never the value:
		// this is the whole reason a manifest names a variable rather than
		// carrying what is in it.
		name := neutralize(e.Name, 200)
		if e.Sandbox {
			name += " (sandbox)"
		}
		doc.EnvVars = append(doc.EnvVars, name)
	}
	for _, d := range s.DependsOn {
		doc.DependsOn = append(doc.DependsOn, neutralize(d, maxIdentifierBytes))
	}
	return doc
}

func describeEgressConfig(e *schema.Egress) *egressConfigDoc {
	doc := &egressConfigDoc{
		Rules: []egressRuleDoc{},
		Note: "The rules are listed as the manifest writes them, which is not the order " +
			"they decide in. Use inspect_egress_firewall for the resolved policy in " +
			"decision order and to ask what would happen to a request you name.",
	}
	if e == nil {
		doc.Default = "deny"
		doc.Note = "This manifest declares no egress block, so the environment reaches " +
			"nothing on the network. " + doc.Note
		return doc
	}
	doc.Default = string(e.Default)
	doc.AllowIPv6 = e.AllowIPv6
	doc.Total = len(e.Rules)
	rules := e.Rules
	if len(rules) > maxRulesInConfig {
		rules = rules[:maxRulesInConfig]
		doc.Truncated = true
	}
	for _, r := range rules {
		doc.Rules = append(doc.Rules, egressRuleDoc{
			Host: neutralize(r.Host, 253), Mode: string(r.Mode),
			Paths: r.Paths, Methods: r.Methods,
			// The credential NAME. The value never reaches this server's
			// output, and there is no argument anywhere that would ask for it.
			Credential: neutralize(r.Credential, 200),
			RateLimit:  neutralize(r.RateLimit, 40),
		})
	}
	return doc
}

func describeChecks(m *schema.Manifest) *checksDoc {
	doc := &checksDoc{}

	ins := &insightsConfigDoc{
		Enabled: true, MigrationRehearsal: true, QueryRegression: true, PlanDiff: true,
	}
	if i := m.Insights; i != nil {
		ins.Enabled = boolOr(i.Enabled, true)
		ins.MigrationRehearsal = boolOr(i.MigrationRehearsal, true)
		ins.QueryRegression = boolOr(i.QueryRegression, true)
		ins.PlanDiff = boolOr(i.PlanDiff, true)
		ins.RegressionFactor = i.RegressionFactor
		ins.RegressionMinMS = i.RegressionMinMS
		ins.LargeTableRows = i.LargeTableRows
		if rc := i.RollingCompatibility; rc != nil {
			ins.RollingWhen = neutralize(rc.When, 40)
			ins.RollingAgainst = neutralize(rc.Against, 200)
		}
	}
	doc.Insights = ins
	if !ins.Enabled {
		doc.Off = append(doc.Off, "insights, so no migration is rehearsed and no query is compared")
	}

	if o := m.Oracle; o != nil {
		oc := &oracleConfigDoc{
			Enabled: boolOr(o.Enabled, true), Baseline: string(o.Baseline),
			BaseRef: neutralize(o.BaseRef, 200), ProbeCount: len(o.Probes),
			FailOn: neutralize(o.FailOn, 20),
		}
		if oc.FailOn == "" {
			oc.FailOn = "none, so every difference is reported and none of them fails"
		}
		for i, pr := range o.Probes {
			if i >= maxProbesReported {
				break
			}
			oc.ProbeNames = append(oc.ProbeNames, neutralize(pr.Name, maxIdentifierBytes))
		}
		doc.Oracle = oc
		if !oc.Enabled {
			doc.Off = append(doc.Off, "the oracle, so this change is not compared against the release it replaces")
		}
	}

	if f := m.Fidelity; f != nil {
		fc := &fidelityConfigDoc{Enabled: boolOr(f.Enabled, true)}
		for _, d := range f.Require {
			fc.Require = append(fc.Require, string(d))
		}
		doc.Fidelity = fc
		if !fc.Enabled {
			doc.Off = append(doc.Off, "the fidelity inventory, so nothing measures how close the copy is")
		}
	}
	return doc
}

func boolOr(p *bool, fallback bool) bool {
	if p == nil {
		return fallback
	}
	return *p
}

func describePolicyConfig(p report.Policy) *policyDoc {
	return &policyDoc{
		LockWarnMS: p.LockWarnMS, LockFailMS: p.LockFailMS,
		Levels: []levelDoc{
			{Rule: "migration_failed", Level: string(p.MigrationFailed)},
			{Rule: "migration_rewrite", Level: string(p.MigrationRewrite)},
			{Rule: "migration_lint", Level: string(p.MigrationLint)},
			{Rule: "plan_regression", Level: string(p.PlanRegression)},
			{Rule: "query_regression", Level: string(p.QueryRegression)},
			{Rule: "load_regression", Level: string(p.LoadRegression)},
			{Rule: "egress_surprise", Level: string(p.EgressSurprise)},
			{Rule: "masking", Level: string(p.Masking)},
			{Rule: "cleanup", Level: string(p.Cleanup)},
			{Rule: "workflows_unverified", Level: string(p.WorkflowsUnverified)},
		},
	}
}

func configurationSummary(p *Project, doc *configurationDoc, section string) string {
	var b strings.Builder
	m := p.Manifest
	fmt.Fprintf(&b, "%s declares %d %s", doc.Application,
		len(m.Services), plural(len(m.Services), "service", "services"))
	if m.Database != nil {
		fmt.Fprintf(&b, " against %s Postgres %d", m.Database.Provider, m.Database.Version)
	}
	rules := 0
	def := "deny"
	if m.Egress != nil {
		rules, def = len(m.Egress.Rules), string(m.Egress.Default)
	}
	fmt.Fprintf(&b, ", with the network defaulting to %s and %d %s named. ",
		def, rules, plural(rules, "host rule", "host rules"))

	if doc.Checks != nil && len(doc.Checks.Off) > 0 {
		fmt.Fprintf(&b, "Turned off in this manifest: %s. ", strings.Join(doc.Checks.Off, "; "))
	}
	if section != "all" {
		fmt.Fprintf(&b, "Only the %s section was asked for. ", section)
	}
	b.WriteString("Every value here is the resolved one, defaults included, so a setting " +
		"absent from antifailure.yaml still appears with what it actually resolves to. " +
		"No secret value passes through this tool.")
	return b.String()
}

// -----------------------------------------------------------------------
// plan_checks_for_change
// -----------------------------------------------------------------------

// newPlanChecksTool builds plan_checks_for_change.
func newPlanChecksTool(p *Project, analyse analyseChange) *Tool {
	return &Tool{
		Name:     "plan_checks_for_change",
		Title:    "Which checks this diff needs",
		ReadOnly: true,
		Description: "Read the diff between two refs and say which checks exercise what it " +
			"touches, and what nothing is going to look at. This is the cheapest thing in " +
			"the product: it reads git, builds no image, starts no database and needs no " +
			"environment, so run it FIRST to find out whether the expensive rehearsals are " +
			"worth starting at all. The line worth reading is a check that is selected and " +
			"unavailable, which means the change touched something and nothing in this " +
			"project is configured to check it. " +
			"It reports no verdict and never says a change is safe or risky, deliberately: " +
			"it says which checks cover which files and what it cannot see. A path no rule " +
			"recognises selects EVERY check rather than none, because the cost of the two " +
			"mistakes is not the same, and that case is reported as everything_selected " +
			"rather than hidden. It is the one thing here that still answers without a " +
			"manifest, in which case every check is reported unavailable.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id": projectIDSchema(),
				"base": gitRefSchema("Optional. The ref the change is measured against, " +
					"defaulting to this job's base branch or the usual origin/main. " +
					"The comparison is against the merge base, which is the set of files " +
					"this change is responsible for rather than everything that landed on " +
					"the base branch since it forked."),
				"head": gitRefSchema("Optional. The ref being measured, defaulting to HEAD."),
			},
		},
		Handler: func(ctx context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			base, _ := args["base"].(string)
			head, _ := args["head"].(string)
			return planChecks(ctx, analyse, base, head)
		},
	}
}

type changePlanDoc struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	// There is deliberately no verdict here. af change never says a change is
	// safe or risky and neither does this: it says which checks cover which
	// files, and a verdict would be a judgement no policy in the manifest
	// authorises.
	Base  string `json:"base,omitempty"`
	Head  string `json:"head,omitempty"`
	Files int    `json:"files_changed"`
	// EverythingSelected reports that the plan holds every check because the
	// classification was incomplete, rather than because the diff selected
	// them one by one. It is the difference between a thorough answer and a
	// fallback, and a caller that cannot tell them apart is being misled.
	EverythingSelected bool `json:"everything_selected"`
	DiffTruncated      bool `json:"diff_truncated"`

	Plan         []selectionDoc `json:"plan"`
	Surfaces     []surfaceDoc   `json:"surfaces"`
	Facts        []factDoc      `json:"facts"`
	FactsTotal   int            `json:"facts_total"`
	FactsShown   int            `json:"facts_shown"`
	Unclassified []string       `json:"unclassified_paths,omitempty"`
	// Blind is what this analysis cannot see, stated rather than implied.
	Blind []string `json:"blind"`
	Note  string   `json:"note,omitempty"`
}

type selectionDoc struct {
	Check    string `json:"check"`
	Selected bool   `json:"selected"`
	// Available reports whether the manifest configures the check at all.
	Available bool `json:"available"`
	// WillRun is the conjunction, taken once here so that "selected" never
	// quietly comes to mean "runnable".
	WillRun     bool     `json:"will_run"`
	Unavailable string   `json:"unavailable,omitempty"`
	Because     []string `json:"because,omitempty"`
}

type surfaceDoc struct {
	Surface string `json:"surface"`
	Files   int    `json:"files"`
}

type factDoc struct {
	Path    string `json:"path"`
	Status  string `json:"status"`
	Surface string `json:"surface"`
	Subject string `json:"subject,omitempty"`
	Rule    string `json:"rule"`
	// Evidence is one sentence naming what matched, written by the engine.
	Evidence string `json:"evidence"`
	Line     int    `json:"line,omitempty"`
}

func planChecks(ctx context.Context, analyse analyseChange, base, head string) (any, *Fault) {
	profile, err := analyse(ctx, base, head)
	if err != nil {
		// A base ref that does not resolve is the common failure here, and it
		// is the caller's to fix rather than a defect, so it is reported as a
		// refusal with a next step rather than as an internal error.
		return nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The diff could not be read, so nothing here says what this change " +
				"touches. The usual cause is a base ref this checkout does not have, " +
				"which happens in a shallow clone; name one it does have as base.",
			Retryable: true,
			wrapped:   err,
		}
	}

	out := &changePlanDoc{
		Kind: "change_check_plan",
		Base: neutralize(profile.Base, 200), Head: neutralize(profile.Head, 200),
		Files: profile.Files, EverythingSelected: profile.Everything,
		DiffTruncated: profile.Truncated,
		Plan:          []selectionDoc{}, Facts: []factDoc{}, Surfaces: []surfaceDoc{},
		Blind: []string{},
	}

	for _, s := range profile.Plan {
		doc := selectionDoc{
			Check: string(s.Check), Selected: s.Selected, Available: s.Available,
			WillRun:     s.Run(),
			Unavailable: safeProse(s.Unavailable, 300),
		}
		for i, because := range s.Because {
			if i >= 10 {
				break
			}
			doc.Because = append(doc.Because, safeProse(because, 200))
		}
		out.Plan = append(out.Plan, doc)
	}

	counts := map[string]int{}
	for _, f := range profile.Facts {
		counts[string(f.Surface)]++
	}
	for surface, n := range counts {
		out.Surfaces = append(out.Surfaces, surfaceDoc{Surface: surface, Files: n})
	}
	sort.Slice(out.Surfaces, func(i, j int) bool {
		if out.Surfaces[i].Files != out.Surfaces[j].Files {
			return out.Surfaces[i].Files > out.Surfaces[j].Files
		}
		return out.Surfaces[i].Surface < out.Surfaces[j].Surface
	})

	out.FactsTotal = len(profile.Facts)
	for i, f := range profile.Facts {
		if i >= maxFactsReported {
			out.Note = fmt.Sprintf(
				"%d facts were produced and the first %d are shown. The plan above covers "+
					"all of them; run af change for the rest.", out.FactsTotal, maxFactsReported)
			break
		}
		path, _ := safePath(f.Path)
		// A subject is a service name from the manifest or a host found in an
		// added line, and a host in an added line is written by whoever opened
		// the pull request. It is checked against what a name can be for
		// exactly that reason.
		subject := ""
		if f.Subject != "" {
			subject, _ = safeIdentifier(f.Subject)
		}
		out.Facts = append(out.Facts, factDoc{
			Path: path, Status: string(f.Status), Surface: string(f.Surface),
			Subject: subject, Rule: neutralize(f.Rule, 64),
			Evidence: safeProse(f.Evidence, 300), Line: f.Line,
		})
	}
	out.FactsShown = len(out.Facts)

	for i, p := range profile.Unclassified {
		if i >= maxPathsReported {
			break
		}
		path, _ := safePath(p)
		out.Unclassified = append(out.Unclassified, path)
	}
	for _, b := range profile.Blind {
		out.Blind = append(out.Blind, safeProse(b, 300))
	}
	out.Summary = changeSummary(profile, out)
	return out, nil
}

func changeSummary(profile *change.Profile, doc *changePlanDoc) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d changed %s between %s and %s. ",
		profile.Files, plural(profile.Files, "path", "paths"),
		orUnnamed(doc.Base), orUnnamed(doc.Head))

	var willRun, gaps []string
	for _, s := range doc.Plan {
		if s.WillRun {
			willRun = append(willRun, s.Check)
		}
		if s.Selected && !s.Available {
			gaps = append(gaps, s.Check)
		}
	}
	if len(willRun) == 0 {
		b.WriteString("No check will run for this change. ")
	} else {
		fmt.Fprintf(&b, "These will run: %s. ", strings.Join(willRun, ", "))
	}
	if len(gaps) > 0 {
		fmt.Fprintf(&b,
			"This change touches something %s %s cover and this project configures "+
				"neither, so nothing is going to look at it. ",
			plural(len(gaps), "the check", "the checks"), strings.Join(gaps, " and "))
	}
	if doc.EverythingSelected {
		b.WriteString("Every check is held because the classification was incomplete " +
			"rather than because the diff selected each one: a path no rule recognises " +
			"selects everything, which is the fail safe direction. ")
	}
	if doc.DiffTruncated {
		b.WriteString("The diff was larger than this analysis reads, so the classification " +
			"is incomplete and every check is held for that reason. ")
	}
	if len(doc.Unclassified) > 0 {
		fmt.Fprintf(&b, "%d %s no rule claimed, listed under unclassified_paths. ",
			len(profile.Unclassified),
			plural(len(profile.Unclassified), "path is one", "paths are ones"))
	}
	b.WriteString("This says which checks cover which files. It does not say the change " +
		"is safe, and it reports no verdict.")
	return b.String()
}

func orUnnamed(ref string) string {
	if ref == "" {
		return "an unnamed ref"
	}
	return ref
}

// -----------------------------------------------------------------------
// check_data_invariants
// -----------------------------------------------------------------------

// newInvariantsTool builds check_data_invariants.
func newInvariantsTool(p *Project, run runInvariants) *Tool {
	return &Tool{
		Name:  "check_data_invariants",
		Title: "Ask the data whether it is still correct",
		// Read only, and enforced rather than promised: every statement runs
		// inside a transaction Postgres opened READ ONLY, so a write is
		// refused by the database rather than trusted not to happen.
		ReadOnly: true,
		Description: "Ask this project's declared invariants of the environment's database " +
			"and report which ones no longer hold. An invariant is a statement that must " +
			"return no rows, so rows coming back means the data is wrong: an order with " +
			"no customer, a balance that does not reconcile, a row a rolled back flow " +
			"left behind. This is the check for a flow that appeared to SUCCEED while " +
			"corrupting data, which no assertion about a screen or a status code can " +
			"catch. Run it after a migration, after a failing workflow run, or while " +
			"writing a new invariant. " +
			"Every statement runs inside a transaction Postgres opened READ ONLY, so it " +
			"cannot write whatever it says. " +
			"The rows themselves are NOT returned: this reports which invariant broke, " +
			"how many rows it returned and which columns they have, because the rows are " +
			"data out of a copy of production and this result is read by a model. Read " +
			"them with af invariants, which prints them. " +
			"A project that declares no invariants gets INCONCLUSIVE and not PASS, " +
			"because a check that examined nothing has not passed.",
		Input: &Schema{
			Type:       "object",
			Required:   []string{"project_id"},
			Properties: map[string]*Schema{"project_id": projectIDSchema()},
		},
		Handler: func(ctx context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			return checkInvariants(ctx, p, run)
		},
	}
}

type invariantsResult struct {
	Kind    string  `json:"kind"`
	Verdict Verdict `json:"verdict"`
	Summary string  `json:"summary"`
	// Asked says whether the invariants could be put to the database at all.
	Asked       bool   `json:"asked"`
	Unavailable string `json:"unavailable,omitempty"`

	Declared  int                  `json:"declared"`
	Held      int                  `json:"held"`
	Violated  int                  `json:"violated"`
	Errored   int                  `json:"errored"`
	Results   []invariantResultDoc `json:"results"`
	Shown     int                  `json:"shown"`
	Truncated bool                 `json:"truncated"`
	Metrics   []Metric             `json:"metrics,omitempty"`
	// RowsWithheld is always true and is stated rather than implied, so a
	// caller looking for the offending rows learns why they are not here
	// instead of concluding there were none.
	RowsWithheld bool   `json:"rows_withheld"`
	EvidenceNote string `json:"evidence_note,omitempty"`
}

type invariantResultDoc struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Held is meaningless when Error is set, which is why Violated is a field
	// of its own rather than the negation of Held.
	Held     bool `json:"held"`
	Violated bool `json:"violated"`
	// Rows is how many rows came back, which is the size of the violation.
	Rows int `json:"rows_returned"`
	// More reports that the statement returned more rows than were collected,
	// so Rows is a floor rather than the count.
	More       bool     `json:"more_rows_than_collected,omitempty"`
	Columns    []string `json:"columns,omitempty"`
	Error      string   `json:"error,omitempty"`
	DurationMS int64    `json:"duration_ms"`
}

func checkInvariants(ctx context.Context, p *Project, run runInvariants) (any, *Fault) {
	out := invariantsResult{
		Kind: "invariant_check", Results: []invariantResultDoc{},
		Declared: len(p.Manifest.Invariants), RowsWithheld: true,
	}

	if out.Declared == 0 {
		// Not a pass. A check with nothing to check has examined nothing, and
		// reporting that as PASS is the monitoring failure this whole product
		// exists to refuse.
		out.Verdict = VerdictInconclusive
		out.Summary = "This project declares no invariants, so nothing was asked of the " +
			"data and this says nothing about it. Add an invariants block to " +
			"antifailure.yaml: a read only statement that must return no rows."
		return out, nil
	}

	results, available, err := run(ctx)
	out.Asked = available
	if !available {
		out.Verdict = VerdictInconclusive
		out.Unavailable = invariantsUnavailable(err)
		out.Summary = "The invariants could not be put to the database, so this says " +
			"nothing about the data. " + out.Unavailable
		return out, nil
	}

	for _, r := range results {
		doc := invariantResultDoc{
			Name:        neutralize(r.Name, maxIdentifierBytes),
			Description: neutralize(r.Description, 300),
			Held:        r.Held, Violated: r.Violated(),
			Rows: len(r.Rows), More: r.More,
			// The error is Postgres's own message about a statement from the
			// manifest. Bounded and stripped of structure, never quoted whole.
			Error:      neutralize(r.Error, maxDetailBytes),
			DurationMS: r.DurationMs,
		}
		for i, c := range r.Columns {
			if i >= 32 {
				break
			}
			// A column name is a bounded identifier and is treated as one.
			// The VALUES underneath it are not reported at all.
			safe, _ := safeIdentifier(c)
			doc.Columns = append(doc.Columns, safe)
		}
		switch {
		case r.Error != "":
			out.Errored++
		case r.Violated():
			out.Violated++
		default:
			out.Held++
		}
		if len(out.Results) < maxInvariantsShown {
			out.Results = append(out.Results, doc)
			continue
		}
		out.Truncated = true
	}
	out.Shown = len(out.Results)

	// A proven violation outranks an error: one is a fact about the data and
	// the other is a gap in what could be seen, and a fact does not become
	// unknown because something beside it failed.
	switch {
	case out.Violated > 0:
		out.Verdict = VerdictFail
	case out.Errored > 0:
		out.Verdict = VerdictInconclusive
	default:
		out.Verdict = VerdictPass
	}

	zero := 0.0
	out.Metrics = []Metric{
		{
			Name: "invariants_violated", Value: float64(out.Violated), Unit: "invariants",
			Threshold: &zero, Breached: out.Violated > 0,
		},
		{Name: "invariants_held", Value: float64(out.Held), Unit: "invariants"},
		{Name: "invariants_errored", Value: float64(out.Errored), Unit: "invariants"},
	}
	out.Summary = invariantsSummary(out)
	out.EvidenceNote = "Run af invariants for the same questions with the offending rows " +
		"printed, which this result withholds."
	return out, nil
}

func invariantsUnavailable(err error) string {
	if err == nil {
		return "No environment is running for this branch, so there is no database to ask. " +
			"Bring one up with af up."
	}
	return withCause("The environment's database could not be reached.", err)
}

func invariantsSummary(out invariantsResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Asked %d %s of the environment's data: %d held, %d violated, %d could "+
		"not be evaluated. ",
		out.Declared, plural(out.Declared, "invariant", "invariants"),
		out.Held, out.Violated, out.Errored)

	switch {
	case out.Violated > 0:
		var broken []string
		for _, r := range out.Results {
			if r.Violated && len(broken) < 5 {
				broken = append(broken, r.Name)
			}
		}
		fmt.Fprintf(&b, "The data is wrong: %s returned rows and must not. ",
			strings.Join(broken, ", "))
	case out.Errored > 0:
		b.WriteString("Nothing was shown to be broken, and something could not be asked, " +
			"so this is INCONCLUSIVE rather than a pass. ")
	default:
		b.WriteString("Every declared invariant holds. ")
	}
	b.WriteString("The rows themselves are not reproduced here, because they come out of " +
		"a copy of production; the count and the column names are.")
	return b.String()
}

// -----------------------------------------------------------------------
// compare_with_previous_release
// -----------------------------------------------------------------------

// newCompareReleasesTool builds compare_with_previous_release.
func newCompareReleasesTool(p *Project, eng *Engine, compare compareReleases) *Tool {
	return &Tool{
		Name:  "compare_with_previous_release",
		Title: "Diff this change against the release it replaces",
		// Not read only: it brings a second environment up. It is not
		// destructive either, because that environment is created by this call
		// and removed again before it returns.
		ReadOnly: false,
		Description: "Run this change beside the version it is replacing and report every " +
			"difference in what came back and in what ended up in the database. It brings " +
			"a second environment up from the baseline revision, branches ONE golden for " +
			"both so they start from identical rows, sends both the same requests in the " +
			"same order, and compares the responses and the database contents. " +
			"This is the tool for the question no single sided test answers: did anything " +
			"change that I did not mean to change. It ranks directionally, which is the " +
			"point: a field or a row the candidate STOPPED returning is critical, because " +
			"losing something is almost never intended, while an extra field is minor " +
			"because that is what a feature branch does all day. " +
			"It takes many minutes and costs a second environment, so it returns a run_id " +
			"immediately: poll it with get_rehearsal_run, and stop it with " +
			"cancel_rehearsal_run. INCONCLUSIVE means the comparison did not finish and " +
			"says nothing about the change. " +
			"The two differing VALUES are not returned, because they are response bodies " +
			"and rows from a copy of production; what is returned is which probe, which " +
			"field, and what kind of difference. The baseline environment is always torn " +
			"down; there is no argument that leaves it running.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id":      projectIDSchema(),
				"idempotency_key": idempotencyKeySchema(),
				"baseline_ref": gitRefSchema("Optional. The revision to compare against, " +
					"overriding the manifest's oracle.base_ref. It is what the change is " +
					"measured against, so it should be the release currently deployed or " +
					"the branch this one forked from."),
				"hypothesis": {
					Type: "string", MaxLength: 2000,
					Description: "Optional. What you expect to differ, in your own words. " +
						"Recorded with the run so the result can be read against the " +
						"expectation. It is never executed and never changes what is " +
						"compared or how a difference is ranked.",
				},
			},
		},
		Handler: func(_ context.Context, call *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			if p.Manifest.Oracle == nil || len(p.Manifest.Oracle.Probes) == 0 {
				// Refused at submission rather than minutes later as a failed
				// run, because it is knowable now and the caller can act on it.
				return nil, &Fault{
					Code: FaultSafetyUnavailable,
					Detail: "This project declares no oracle probes, so there is nothing to " +
						"send to either side and no comparison to make. Add an oracle block " +
						"with probes to antifailure.yaml first.",
				}
			}
			baseRef, _ := args["baseline_ref"].(string)
			hypothesis, _ := args["hypothesis"].(string)

			return eng.Submit(call, "compare_with_previous_release", args,
				func(ctx context.Context, runID string) (string, *ResultBody, *Fault) {
					return runComparison(ctx, p, eng, compare, runID, baseRef, hypothesis)
				})
		},
	}
}

func runComparison(
	ctx context.Context, p *Project, eng *Engine, compare compareReleases,
	runID, baseRef, hypothesis string,
) (string, *ResultBody, *Fault) {
	if eng.Cancelled(ctx, runID) {
		return "", nil, faultf(FaultRunNotCancellable, "This run was cancelled before it started.")
	}
	eng.Phase(ctx, runID, "bringing the baseline up beside the candidate")

	res, err := compare(ctx, baseRef)
	if err != nil {
		return "", nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The comparison could not be completed, so it says nothing about the " +
				"change. It needs a baseline revision this checkout can resolve and enough " +
				"room for a second environment.",
			Retryable: true,
			wrapped:   err,
		}
	}
	if res == nil || res.Result == nil {
		return "", nil, &Fault{
			Code:      FaultSafetyUnavailable,
			Detail:    "The comparison produced no report, so it says nothing about the change.",
			Retryable: true,
		}
	}
	eng.Phase(ctx, runID, "ranking the differences")

	// The threshold is the manifest's own oracle.fail_on and there is no
	// argument that reaches it. An empty value parses as none, which reports
	// every difference and fails on none of them, and the summary says so
	// rather than letting a PASS stand unexplained.
	threshold, ok := oracle.ParseSeverity(p.Manifest.Oracle.FailOn)
	if !ok {
		return "", nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "This project's oracle.fail_on is not one of none, minor, major or " +
				"critical, so there is no threshold to judge a difference against.",
		}
	}

	findings := comparisonFindings(res.Findings, threshold)
	body := &ResultBody{
		Findings: boundFindings(findings),
		Metrics:  comparisonMetrics(res.Result, threshold),
		Evidence: comparisonEvidence(res),
		Detail:   describeComparison(res),
	}

	native := report.VerdictPass
	switch {
	case oracle.AtLeast(res.Findings, threshold):
		native = report.VerdictFail
	case len(res.Findings) > 0:
		native = report.VerdictWarn
	}
	body.Summary = comparisonSummary(res, threshold, native, hypothesis)
	return native, body, nil
}

// comparisonFindings maps oracle differences onto the shared finding shape.
//
// The level is decided by the manifest's threshold and nothing else, so a
// difference that fails here fails af oracle too. The two VALUES that differ
// are dropped rather than rendered: they are a response body and a database
// row out of a copy of production, and this document is read by a model.
func comparisonFindings(in []oracle.Finding, threshold oracle.Severity) []report.Finding {
	out := make([]report.Finding, 0, len(in))
	for _, f := range in {
		level := report.LevelWarn
		if threshold != 0 && f.Severity >= threshold {
			level = report.LevelFail
		}
		where, _ := safeIdentifier(f.Where)
		out = append(out, report.Finding{
			Rule: "oracle_" + string(f.Kind), Level: level,
			Title: fmt.Sprintf("%s: %s.", f.SeverityName, comparisonTitle(f.Kind)),
			Where: where,
			Detail: comparisonDetail(f) +
				" The two values are not reproduced here, because they are a response body " +
				"or a row from a copy of production. Read them with af oracle.",
			Fix: "If this difference is the change you meant to make, nothing needs doing. " +
				"If it is not, it is a regression the baseline did not have.",
		})
	}
	return out
}

// comparisonDetail locates the difference without carrying data.
//
// A body path is structure and is repeated; a row is named by its primary key,
// which is a VALUE out of the database, so it is replaced by the fact that the
// finding is about a row. That distinction is the whole of this function.
func comparisonDetail(f oracle.Finding) string {
	phase := ""
	switch f.Phase {
	case oracle.PhaseMigration:
		phase = " It was already there before any request was sent, so it is what the two " +
			"sets of migrations did rather than what the two applications did."
	case oracle.PhaseTraffic:
		phase = " It appeared while the requests were running, so it is what the two " +
			"applications did rather than what the migrations did."
	}
	switch f.Kind {
	case oracle.KindRowMissing, oracle.KindRowExtra, oracle.KindRowChanged:
		return "One row differs. It is identified by its primary key, which is a value out " +
			"of the database and is not repeated here." + phase
	}
	if f.Path == "" {
		return strings.TrimSpace(phase)
	}
	return "At " + neutralize(f.Path, 200) + " in the response." + phase
}

func comparisonTitle(k oracle.Kind) string {
	switch k {
	case oracle.KindTransport:
		return "one side answered and the other did not"
	case oracle.KindStatusClass:
		return "the status moved between the success, client error and server error classes"
	case oracle.KindStatus:
		return "the status changed inside its class"
	case oracle.KindContentType:
		return "the response changed media type"
	case oracle.KindHeader:
		return "a compared header changed"
	case oracle.KindBodyMissing:
		return "the candidate stopped returning something the baseline returned"
	case oracle.KindBodyExtra:
		return "the candidate returned something the baseline did not"
	case oracle.KindBodyType:
		return "a value changed type"
	case oracle.KindBodyValue:
		return "a value changed"
	case oracle.KindBodyLength:
		return "an array changed length"
	case oracle.KindBodyOrder:
		return "an array has the same members in a different order"
	case oracle.KindBodyBytes:
		return "a non JSON body did not match"
	case oracle.KindBodyParse:
		return "a body declared JSON does not parse on one side"
	case oracle.KindRowMissing:
		return "the candidate did not write a row the baseline wrote"
	case oracle.KindRowExtra:
		return "the candidate wrote a row the baseline did not"
	case oracle.KindRowChanged:
		return "a row present on both sides has columns that disagree"
	case oracle.KindTableMissing:
		return "the candidate does not have a table the baseline has"
	case oracle.KindTableExtra:
		return "the candidate has a table the baseline does not"
	case oracle.KindColumns:
		return "a table's columns differ between the two sides"
	default:
		return "the two sides differ"
	}
}

func comparisonMetrics(r *oracle.Result, threshold oracle.Severity) []Metric {
	counts := oracle.Count(r.Findings)
	zero := 0.0
	failing := 0.0
	for sev, n := range counts {
		if threshold != 0 && sev >= threshold {
			failing += float64(n)
		}
	}
	return []Metric{
		{
			Name: "differences_at_or_above_the_threshold", Value: failing, Unit: "differences",
			Threshold: &zero, Breached: failing > 0,
		},
		{Name: "differences_critical", Value: float64(counts[oracle.Critical]), Unit: "differences"},
		{Name: "differences_major", Value: float64(counts[oracle.Major]), Unit: "differences"},
		{Name: "differences_minor", Value: float64(counts[oracle.Minor]), Unit: "differences"},
		{Name: "probes_sent", Value: float64(len(r.Probes)), Unit: "requests"},
		{Name: "comparison_duration_ms", Value: float64(r.DurationMs), Unit: "ms"},
	}
}

func comparisonEvidence(res *env.OracleResult) []Evidence {
	out := []Evidence{{
		URI: "af://oracle", Kind: "command",
		Note: "Run af oracle for the full comparison, including the two values this " +
			"result withholds.",
	}}
	if !res.BaselineTornDown {
		// A leak somebody has to finish by hand. It is evidence rather than a
		// note, because it names a resource that still exists.
		branch, _ := safeIdentifier(res.BaselineBranch)
		out = append(out, Evidence{
			URI: "af://environment/" + branch, Kind: "leaked_environment",
			Note: "The baseline environment may still be up. Remove it with af down and " +
				"this branch name.",
		})
	}
	return out
}

type comparisonDoc struct {
	BaselineRef  string `json:"baseline_ref,omitempty"`
	CandidateRef string `json:"candidate_ref,omitempty"`
	// BaselineHow says in words how the baseline was chosen, because the merge
	// base and a named tag answer different questions.
	BaselineHow string `json:"baseline_how,omitempty"`
	// Golden names the one database version both sides branched, which is what
	// makes the comparison meaningful at all.
	Golden           string   `json:"golden,omitempty"`
	BaselineTornDown bool     `json:"baseline_environment_removed"`
	Probes           []string `json:"probes_compared,omitempty"`
	ProbesTotal      int      `json:"probes_total"`
	// Ignored is everything the comparison declined to look at, defaults
	// included, because an oracle that silently skips reads exactly like one
	// that found nothing.
	Ignored []string `json:"not_compared,omitempty"`
	Notes   []string `json:"notes,omitempty"`
	// ValuesWithheld is always true and is stated rather than implied.
	ValuesWithheld bool `json:"values_withheld"`
}

func describeComparison(res *env.OracleResult) *comparisonDoc {
	r := res.Result
	doc := &comparisonDoc{
		BaselineRef: neutralize(r.BaselineRef, 64), CandidateRef: neutralize(r.CandidateRef, 64),
		BaselineHow:      safeProse(r.BaselineHow, 200),
		BaselineTornDown: res.BaselineTornDown,
		ProbesTotal:      len(r.Probes),
		ValuesWithheld:   true,
	}
	if golden, ok := safeIdentifier(r.Golden); r.Golden != "" && ok {
		doc.Golden = golden
	}
	for i, pr := range r.Probes {
		if i >= maxProbesReported {
			break
		}
		name, _ := safeIdentifier(pr.Name)
		doc.Probes = append(doc.Probes, fmt.Sprintf("%s: baseline %d, candidate %d, %d %s",
			name, pr.Baseline.Status, pr.Candidate.Status,
			pr.Findings, plural(pr.Findings, "difference", "differences")))
	}
	for _, n := range r.Notes {
		doc.Notes = append(doc.Notes, safeProse(n, 300))
	}
	doc.Ignored = describeIgnored(r.Ignored)
	return doc
}

// describeIgnored lists what the comparison declined to look at.
//
// Always rendered, defaults included, because an oracle that silently skips
// reads exactly like one that found nothing. Assembled field by field rather
// than taken from Ignored.Describe, which composes several lines of terminal
// prose into one string; a list of short entries is what survives being read
// as a document.
func describeIgnored(ig oracle.Ignored) []string {
	out := make([]string, 0, 4)
	if len(ig.Headers) > 0 {
		out = append(out, fmt.Sprintf("%d response %s: %s",
			len(ig.Headers), plural(len(ig.Headers), "header", "headers"),
			neutralize(strings.Join(ig.Headers, ", "), 300)))
	}
	if len(ig.Fields) > 0 {
		out = append(out, fmt.Sprintf("%d field %s from the manifest: %s",
			len(ig.Fields), plural(len(ig.Fields), "pattern", "patterns"),
			neutralize(strings.Join(ig.Fields, ", "), 300)))
	}
	out = append(out, fmt.Sprintf(
		"numbers equal within %g relative tolerance", ig.FloatTolerance))
	for i, n := range ig.Normalisers {
		if i >= 8 {
			break
		}
		line := fmt.Sprintf("the %s normaliser made %d %s equal",
			neutralize(n.Name, 64), n.Count, plural(n.Count, "value", "values"))
		if n.Widest != "" {
			// The widest gap absorbed is what turns "timestamps are
			// normalised" from a claim into a number somebody can disagree
			// with, so it is carried rather than summarised away.
			line += ", the widest gap " + neutralize(n.Widest, 64)
		}
		out = append(out, line)
	}
	return out
}

func comparisonSummary(
	res *env.OracleResult, threshold oracle.Severity, native, hypothesis string,
) string {
	r := res.Result
	var b strings.Builder
	counts := oracle.Count(r.Findings)

	fmt.Fprintf(&b, "Sent %d %s to both sides from one golden. ",
		len(r.Probes), plural(len(r.Probes), "request", "requests"))
	if len(r.Findings) == 0 {
		b.WriteString("The two sides were identical on everything compared. ")
	} else {
		fmt.Fprintf(&b, "%d %s: %d critical, %d major, %d minor. ",
			len(r.Findings), plural(len(r.Findings), "difference", "differences"),
			counts[oracle.Critical], counts[oracle.Major], counts[oracle.Minor])
	}

	switch {
	case native == report.VerdictFail:
		fmt.Fprintf(&b, "At least one is %s or worse, which this project's oracle.fail_on "+
			"says stops a merge. ", threshold.String())
	case threshold == 0 && len(r.Findings) > 0:
		b.WriteString("This project's oracle.fail_on is none, so every difference is " +
			"reported and none of them fails. Nothing here was judged. ")
	case len(r.Findings) > 0:
		fmt.Fprintf(&b, "None reaches %s, which is where this project's oracle.fail_on "+
			"stops a merge. ", threshold.String())
	}

	if !res.BaselineTornDown {
		b.WriteString("The baseline environment may still be up and is named in the " +
			"evidence; remove it with af down. ")
	}
	if len(r.Notes) > 0 {
		fmt.Fprintf(&b, "%d %s could not be compared and are named in the detail. ",
			len(r.Notes), plural(len(r.Notes), "thing", "things"))
	}
	if hypothesis != "" {
		fmt.Fprintf(&b, "Your stated hypothesis, unevaluated: %q.",
			neutralize(hypothesis, 500))
	}
	return strings.TrimSpace(b.String())
}

// analyseChange reads the diff through an orchestrator built for this call.
//
// Getenv comes from the server's own configuration and not from the call, so
// the base ref a job's environment names is discovered the same way af change
// discovers it and no argument can point it elsewhere.
func (f *orchestratorFactory) analyseChange(
	ctx context.Context, base, head string,
) (*change.Profile, error) {
	o, err := f.build()
	if err != nil {
		return nil, err
	}
	// DiffPath is deliberately not set and no argument reaches it. Reading a
	// diff from a path a caller names would be a second file read with none of
	// the containment resolveInRoot gives the first one, for a capability that
	// answers the same question git already answers here.
	return o.Change(ctx, env.ChangeOptions{Base: base, Head: head, Getenv: f.cfg.Getenv})
}

// invariants asks the manifest's invariants, saying whether they could be
// asked at all.
func (f *orchestratorFactory) invariants(
	ctx context.Context,
) ([]env.InvariantResult, bool, error) {
	o, err := f.build()
	if err != nil {
		return nil, false, err
	}
	results, err := o.RunInvariants(ctx)
	if err != nil {
		return nil, false, err
	}
	return results, true, nil
}

// compareReleases runs the differential oracle.
//
// Keep is deliberately not set and there is no argument that could set it.
// Leaving the baseline environment running is a leak somebody has to finish by
// hand, and an agent is the caller least able to notice it happened.
func (f *orchestratorFactory) compareReleases(
	ctx context.Context, baseRef string,
) (*env.OracleResult, error) {
	o, err := f.build()
	if err != nil {
		return nil, err
	}
	return o.Oracle(ctx, env.OracleOptions{BaseRef: baseRef})
}
