package manifest

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Defaults, in one place so that the schema, the documentation, and the code
// cannot disagree about what happens when a key is omitted.
const (
	DefaultHealthPath    = "/"
	DefaultHealthTimeout = "180s"
	DefaultReplicas      = 1
	DefaultCPU           = "1"
	DefaultMemory        = "1Gi"
	DefaultPostgres      = 17
	DefaultURLEnv        = "DATABASE_URL"
	DefaultMaskingRules  = "masking.yaml"
	DefaultGoldenMaxAge  = "168h"
	DefaultGoldenRetain  = 5
	DefaultSubsetMaxRows = 1000000
	DefaultTTL           = "24h"
	DefaultMaxTTL        = "168h"
	DefaultIdleSleep     = "30m"
	DefaultDomain        = "localhost"
	DefaultNamespacePfx  = "af"
	DefaultStartPath     = "/"
	DefaultWorkflowSteps = 60
	DefaultWorkflowUSD   = 0.5
	DefaultWorkflowTime  = "10m"
	DefaultExploreSteps  = 40
	DefaultExploreSlowMs = 3000
	DefaultLoadScale     = 0.05
	DefaultLoadDuration  = "2m"
	DefaultRegressionFac = 1.5
	DefaultRegressionMS  = 5
	DefaultLargeTable    = 100000
	// DefaultOracleFailOn is the lowest severity that fails af oracle.
	//
	// critical rather than any difference. A pull request exists to change
	// behaviour, so failing on any difference at all would fail every branch
	// and teach everybody to pass the flag that turns it off. critical is the
	// short list of differences that are a regression unless somebody can say
	// why they are not: a request the baseline served and the candidate did
	// not, a status that fell into an error class, and a row the baseline
	// wrote and the candidate did not.
	DefaultOracleFailOn = "critical"
	// DefaultOracleMaxRows is how many rows a table may hold and still have
	// its contents compared. It matches oracle.DefaultMaxRowsPerTable, and the
	// two are checked against each other by a test rather than by a comment.
	DefaultOracleMaxRows = 10000
	// DefaultLockWarnMS and DefaultLockFailMS bound how long a migration may
	// hold a lock on a table. Two seconds is the figure the product has
	// advertised since before it could enforce it: past that, a lock on a busy
	// table is an outage rather than a pause. Half a second is the point at
	// which it is worth a line in the comment.
	DefaultLockWarnMS  = 500
	DefaultLockFailMS  = 2000
	DefaultRollingWhen = "risky"
	DefaultRollingRef  = "merge-base"
	// DefaultPersonaDomain is reserved by RFC 6761 and can never receive mail,
	// so a persona address cannot become a real one by accident.
	DefaultPersonaDomain = "example.test"
	// DefaultPersonaPhonePrefix is the North American block reserved for
	// fictional use, 555-0100 through 555-0199. It is the phone number's
	// version of example.test: a persona number cannot become a real handset
	// by accident, which matters more here than for mail, because a text
	// message reaches somebody immediately and at their expense.
	//
	// The block holds a hundred numbers and a manifest holds at most fifty
	// personas, so numbering never wraps and two personas never collide.
	DefaultPersonaPhonePrefix = "+155501"
)

// normalize fills in every default and cleans every path, exactly once.
//
// Every later package reads a normalized manifest, so no downstream code has
// to remember what the default health path was or whether a path had a leading
// slash. Normalization is idempotent: running it twice changes nothing, which
// a property test holds.
func normalize(m *schema.Manifest, root string) {
	if m.Version == 0 {
		m.Version = schema.ManifestVersion
	}
	if m.Name == "" && root != "" {
		m.Name = sanitizeName(filepath.Base(root))
	}

	for i := range m.Services {
		normalizeService(&m.Services[i])
	}
	normalizeDatabase(m)
	normalizeEgress(m)
	normalizePersonas(m)
	normalizeAuth(m)
	normalizeWorkflows(m)
	normalizeInsights(m)
	normalizeOracle(m)
	normalizeExplore(m)
	normalizeFidelity(m)
	normalizeLoad(m)
	normalizePolicy(m)
	normalizeRuntime(m)
	normalizeGitHub(m)
}

func normalizeService(s *schema.Service) {
	if s.Kind == "" {
		s.Kind = schema.ServiceWeb
	}
	if c, ok := confine(s.Path); ok {
		s.Path = c
	}
	if s.Kind == schema.ServiceWeb {
		if s.HealthPath == "" {
			s.HealthPath = DefaultHealthPath
		}
		if !strings.HasPrefix(s.HealthPath, "/") {
			s.HealthPath = "/" + s.HealthPath
		}
	}
	if s.HealthTimeout == "" {
		s.HealthTimeout = DefaultHealthTimeout
	}
	if s.Replicas == 0 {
		s.Replicas = DefaultReplicas
	}
	if s.Resources == nil {
		s.Resources = &schema.Resources{}
	}
	if s.Resources.CPU == "" {
		s.Resources.CPU = DefaultCPU
	}
	if s.Resources.Memory == "" {
		s.Resources.Memory = DefaultMemory
	}
	if s.Build == nil {
		s.Build = &schema.Build{}
	}
	if s.Build.Strategy == "" {
		s.Build.Strategy = schema.BuildAuto
	}
	if c, ok := confine(s.Build.Dockerfile); ok {
		s.Build.Dockerfile = c
	}
	if c, ok := confine(s.Build.Context); ok {
		s.Build.Context = c
	}
	// Hosts are lowercased and punycode is left as it arrives, so that rule
	// matching later never has to case fold.
	for i, h := range s.Build.AllowHosts {
		s.Build.AllowHosts[i] = strings.ToLower(strings.TrimSpace(h))
	}
	// Sorting makes the normalized manifest a stable input to the image cache
	// key, so reordering a list does not force a rebuild.
	sortStrings(s.Build.AllowHosts)
	sortStrings(s.DependsOn)
}

func normalizeDatabase(m *schema.Manifest) {
	if m.Database == nil {
		m.Database = &schema.Database{}
	}
	d := m.Database
	if d.Provider == "" {
		d.Provider = schema.DBDocker
	}
	if d.Version == 0 {
		d.Version = DefaultPostgres
	}
	if d.URLEnv == "" {
		d.URLEnv = DefaultURLEnv
	}
	if d.MaskingRules == "" {
		d.MaskingRules = DefaultMaskingRules
	}
	if c, ok := confine(d.MaskingRules); ok {
		d.MaskingRules = c
	}
	if d.Golden == nil {
		d.Golden = &schema.Golden{}
	}
	if d.Golden.MaxAge == "" {
		d.Golden.MaxAge = DefaultGoldenMaxAge
	}
	if d.Golden.Retain == 0 {
		d.Golden.Retain = DefaultGoldenRetain
	}
	if d.Golden.Storage == "" {
		d.Golden.Storage = schema.StorageLocal
	}
	if d.Subset == nil {
		d.Subset = &schema.Subset{}
	}
	if d.Subset.MaxRows == 0 {
		d.Subset.MaxRows = DefaultSubsetMaxRows
	}
	if d.Subset.FollowDependents == nil {
		one := 1
		d.Subset.FollowDependents = &one
	}
}

func normalizeEgress(m *schema.Manifest) {
	if m.Egress == nil {
		m.Egress = &schema.Egress{}
	}
	if m.Egress.Default == "" {
		// Block by default. Every other default in this file is a convenience;
		// this one is the product's central promise, and it is deliberately the
		// strictest possible value.
		m.Egress.Default = schema.ModeBlock
	}
	for i := range m.Egress.Rules {
		r := &m.Egress.Rules[i]
		r.Host = strings.ToLower(strings.TrimSpace(r.Host))
		for j, p := range r.Paths {
			if !strings.HasPrefix(p, "/") {
				r.Paths[j] = "/" + p
			}
		}
		for j, meth := range r.Methods {
			r.Methods[j] = strings.ToUpper(strings.TrimSpace(meth))
		}
		if c, ok := confine(r.Fixtures); ok {
			r.Fixtures = c
		}
		if r.WebhookPath != "" && !strings.HasPrefix(r.WebhookPath, "/") {
			r.WebhookPath = "/" + r.WebhookPath
		}
	}
}

func normalizePersonas(m *schema.Manifest) {
	texted := 0
	for i := range m.Personas {
		p := &m.Personas[i]
		if p.Login == "" {
			p.Login = schema.LoginPassword
		}
		if p.Login == schema.LoginSMSCode && p.Phone == "" {
			// Only for the strategy that uses it. Giving every persona a
			// number would put one in the manifest for accounts that will
			// never receive a text, which reads as configuration and is not.
			p.Phone = fmt.Sprintf("%s%02d", DefaultPersonaPhonePrefix, texted)
			texted++
		}
		if p.Email == "" {
			p.Email = p.Name + "@" + DefaultPersonaDomain
		}
		p.Email = strings.ToLower(strings.TrimSpace(p.Email))
	}
}

func normalizeWorkflows(m *schema.Manifest) {
	firstPersona := ""
	if len(m.Personas) > 0 {
		firstPersona = m.Personas[0].Name
	}
	for i := range m.Workflows {
		w := &m.Workflows[i]
		if w.Persona == "" {
			w.Persona = firstPersona
		}
		if w.StartPath == "" {
			w.StartPath = DefaultStartPath
		}
		if !strings.HasPrefix(w.StartPath, "/") {
			w.StartPath = "/" + w.StartPath
		}
		if w.Budget == nil {
			w.Budget = &schema.Budget{}
		}
		if w.Budget.Steps == 0 {
			w.Budget.Steps = DefaultWorkflowSteps
		}
		if w.Budget.USD == 0 {
			w.Budget.USD = DefaultWorkflowUSD
		}
		if w.Budget.Duration == "" {
			w.Budget.Duration = DefaultWorkflowTime
		}
	}
}

func normalizeInsights(m *schema.Manifest) {
	if m.Insights == nil {
		m.Insights = &schema.Insights{}
	}
	i := m.Insights
	setTrue(&i.Enabled)
	setTrue(&i.MigrationRehearsal)
	setTrue(&i.QueryRegression)
	setTrue(&i.PlanDiff)
	if i.RegressionFactor == 0 {
		i.RegressionFactor = DefaultRegressionFac
	}
	if i.RegressionMinMS == 0 {
		i.RegressionMinMS = DefaultRegressionMS
	}
	if i.LargeTableRows == 0 {
		i.LargeTableRows = DefaultLargeTable
	}
	// Filled in rather than left nil, so that `af explain` can print which
	// commit this repository's rolling check would compare against. A block
	// nobody can see the effective value of is a block people set twice.
	if i.RollingCompatibility == nil {
		i.RollingCompatibility = &schema.RollingCompatibility{}
	}
	if i.RollingCompatibility.When == "" {
		i.RollingCompatibility.When = DefaultRollingWhen
	}
	if i.RollingCompatibility.Against == "" {
		i.RollingCompatibility.Against = DefaultRollingRef
	}
}

// normalizeOracle fills the defaults, and only when the block is present.
//
// Every other normaliser in this file creates its block when it is missing,
// because every other subsystem runs whether or not the manifest mentions it.
// The oracle does not: it doubles the environments a run costs and it needs a
// probe plan somebody wrote. So an absent block stays absent, which is what
// lets `af oracle` tell "not configured" from "configured and turned off" and
// say something different about each.
func normalizeOracle(m *schema.Manifest) {
	o := m.Oracle
	if o == nil {
		return
	}
	setTrue(&o.Enabled)
	if o.Baseline == "" {
		o.Baseline = schema.BaselineMergeBase
	}
	if o.FailOn == "" {
		o.FailOn = DefaultOracleFailOn
	}
	o.FailOn = strings.ToLower(strings.TrimSpace(o.FailOn))

	for i := range o.Probes {
		p := &o.Probes[i]
		p.Name = strings.TrimSpace(p.Name)
		p.Method = strings.ToUpper(strings.TrimSpace(p.Method))
		if p.Method == "" {
			p.Method = "GET"
		}
		p.Path = strings.TrimSpace(p.Path)
		if p.Path != "" && !strings.HasPrefix(p.Path, "/") {
			p.Path = "/" + p.Path
		}
	}

	if o.Ignore == nil {
		o.Ignore = &schema.OracleIgnore{}
	}
	for i, h := range o.Ignore.Headers {
		// Lowercased here so the comparison never has to case fold, which is
		// the same reason egress hosts are lowercased above.
		o.Ignore.Headers[i] = strings.ToLower(strings.TrimSpace(h))
	}
	for i, f := range o.Ignore.Fields {
		o.Ignore.Fields[i] = strings.TrimSpace(f)
	}
	if o.Database == nil {
		o.Database = &schema.OracleDatabase{}
	}
	setTrue(&o.Database.Enabled)
	if o.Database.MaxRows == 0 {
		o.Database.MaxRows = DefaultOracleMaxRows
	}
	for i, t := range o.Database.Tables {
		o.Database.Tables[i] = strings.TrimSpace(t)
	}
	for i, t := range o.Database.Exclude {
		o.Database.Exclude[i] = strings.TrimSpace(t)
	}
}

func normalizeExplore(m *schema.Manifest) {
	if m.Explore == nil {
		m.Explore = &schema.Explore{}
	}
	firstPersona := ""
	if len(m.Personas) > 0 {
		firstPersona = m.Personas[0].Name
	}
	for i := range m.Explore.Goals {
		g := &m.Explore.Goals[i]
		if g.Persona == "" {
			g.Persona = firstPersona
		}
		// The name, so that a manifest which sets no seed still replays. A
		// seed derived from the wall clock or from the process would make the
		// default case the one that cannot be reproduced, which is exactly
		// backwards for the feature this key exists to make possible.
		if g.Seed == "" {
			g.Seed = g.Name
		}
		if g.StartPath == "" {
			g.StartPath = DefaultStartPath
		}
		if !strings.HasPrefix(g.StartPath, "/") {
			g.StartPath = "/" + g.StartPath
		}
		if g.SlowMs == 0 {
			g.SlowMs = DefaultExploreSlowMs
		}
		if g.Budget == nil {
			g.Budget = &schema.Budget{}
		}
		if g.Budget.Steps == 0 {
			g.Budget.Steps = DefaultExploreSteps
		}
		if g.Budget.USD == 0 {
			g.Budget.USD = DefaultWorkflowUSD
		}
		if g.Budget.Duration == "" {
			g.Budget.Duration = DefaultWorkflowTime
		}
	}
}

func normalizeFidelity(m *schema.Manifest) {
	if m.Fidelity == nil {
		m.Fidelity = &schema.Fidelity{}
	}
	setTrue(&m.Fidelity.Enabled)
}

func normalizeLoad(m *schema.Manifest) {
	if m.Load == nil {
		m.Load = &schema.Load{}
	}
	l := m.Load
	if l.Source == "" {
		l.Source = schema.LoadNone
	}
	if l.Scale == 0 {
		l.Scale = DefaultLoadScale
	}
	if l.Duration == "" {
		l.Duration = DefaultLoadDuration
	}
	if l.Thresholds == nil {
		l.Thresholds = &schema.LoadThresholds{}
	}
	// Only under a source that carries a baseline. p95_increase divides a
	// measured p95 by production's own p95 for that route, and a combined
	// format log line has no duration in it, so under access_log or none the
	// default was a threshold the report listed and no route could ever be
	// measured against. Filling it in there produced the exact shape this
	// repository keeps finding: a configured check, evaluated zero times,
	// reported green. A threshold somebody writes under those sources is
	// refused by the validator; this is the half the validator cannot see,
	// because the engine set it rather than the author.
	if l.Thresholds.P95Increase == 0 && l.Source == schema.LoadOTel {
		l.Thresholds.P95Increase = 0.25
	}
	if l.Thresholds.ErrorRate == 0 {
		l.Thresholds.ErrorRate = 0.01
	}
	// No default for query_count_increase. Nothing reads it, so filling it in
	// put a threshold on every manifest that could not affect any verdict.
	// Writing one is refused by the validator, which names the check that does
	// compare statement counts.
	for i, r := range l.SafeRoutes {
		l.SafeRoutes[i] = normalizeRoute(r)
	}
	for i, r := range l.UnsafeRoutes {
		l.UnsafeRoutes[i] = normalizeRoute(r)
	}
}

// normalizePolicy fills in the release gate.
//
// The two defaults that are not "warn" are the two the product has always
// claimed: an unknown destination and a failed teardown cannot ship. A
// migration that does not apply is the third, and it is not really a policy
// choice: a migration that fails against production's shape fails the deploy.
// Everything else defaults to warn, because a gate that blocks on its first
// day is a gate people turn off on their second.
func normalizePolicy(m *schema.Manifest) {
	if m.Policy == nil {
		m.Policy = &schema.Policy{}
	}
	p := m.Policy
	if p.MigrationLock == nil {
		p.MigrationLock = &schema.LockPolicy{}
	}
	if p.MigrationLock.WarnMS == 0 {
		p.MigrationLock.WarnMS = DefaultLockWarnMS
	}
	if p.MigrationLock.FailMS == 0 {
		p.MigrationLock.FailMS = DefaultLockFailMS
	}
	level := func(v *schema.PolicyLevel, def schema.PolicyLevel) {
		if *v == "" {
			*v = def
		}
	}
	level(&p.MigrationFailed, schema.PolicyFail)
	level(&p.MigrationRewrite, schema.PolicyWarn)
	level(&p.MigrationLint, schema.PolicyWarn)
	level(&p.PlanRegression, schema.PolicyWarn)
	level(&p.QueryRegression, schema.PolicyWarn)
	level(&p.LoadRegression, schema.PolicyWarn)
	level(&p.EgressSurprise, schema.PolicyFail)
	level(&p.WorkflowsUnverified, schema.PolicyFail)
	level(&p.Masking, schema.PolicyFail)
	level(&p.Cleanup, schema.PolicyFail)
}

// normalizeRoute cleans a safe or unsafe route pattern without destroying it.
//
// A pattern is a method and a path glob, "GET /api/*", or a bare glob that
// matches any method. That is what load.matchesAny reads and what every
// example in the documentation is written as.
//
// This function used to prefix anything not starting with a slash with one, so
// "DELETE /*" became "/DELETE /*". The matcher splits on the first space and
// compares the method exactly, and "/DELETE" is not "DELETE", so every
// documented pattern matched nothing. The safe list failing that way is loud,
// because a run that may send nothing refuses everything and says so. The
// unsafe list failing that way is silent: a list that matches nothing refuses
// nothing, and under a permissive safe list the deletes somebody wrote the
// entry to prevent were sent at production's rate.
//
// Only a method the matcher would actually compare is split off. A path
// carrying a space is not a method and stays whole, which is what it did
// before and what a strange entry should keep doing rather than becoming a
// second guess.
func normalizeRoute(r string) string {
	r = strings.TrimSpace(r)
	if method, rest, ok := strings.Cut(r, " "); ok && isHTTPMethod(method) {
		return strings.ToUpper(method) + " " + normalizePath(rest)
	}
	return normalizePath(r)
}

func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

// httpMethods is the set a route pattern may name.
//
// A closed list rather than "anything before a space", because a path is
// allowed to contain a space and reading one as a method would silently change
// what the pattern means. Upper cased on the way in, since the matcher
// compares without regard to case and two spellings of one method in a stored
// list is a difference a reader has to decide is meaningless.
var httpMethods = map[string]bool{
	"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "OPTIONS": true, "TRACE": true, "CONNECT": true,
}

func isHTTPMethod(s string) bool { return httpMethods[strings.ToUpper(strings.TrimSpace(s))] }

func normalizeRuntime(m *schema.Manifest) {
	if m.Runtime == nil {
		m.Runtime = &schema.Runtime{}
	}
	r := m.Runtime
	if r.Provider == "" {
		r.Provider = schema.RuntimeLocal
	}
	if r.TTL == "" {
		r.TTL = DefaultTTL
	}
	if r.MaxTTL == "" {
		r.MaxTTL = DefaultMaxTTL
	}
	if r.IdleSleep == "" {
		r.IdleSleep = DefaultIdleSleep
	}
	if r.Domain == "" {
		r.Domain = DefaultDomain
	}
	r.Domain = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(r.Domain), "*."))
	if r.NamespacePrefix == "" {
		r.NamespacePrefix = DefaultNamespacePfx
	}
}

func normalizeGitHub(m *schema.Manifest) {
	if m.GitHub == nil {
		m.GitHub = &schema.GitHub{}
	}
	g := m.GitHub
	if g.Mode == "" {
		g.Mode = schema.GitHubActions
	}
	setTrue(&g.Comment)
	if g.ForkPolicy == "" {
		// label, not always. A fork's code would otherwise run with the
		// environment's credentials on the strength of a stranger opening a
		// pull request.
		g.ForkPolicy = schema.ForkLabel
	}
	if len(g.TeardownOn) == 0 {
		g.TeardownOn = []string{"close", "merge", "ttl"}
	}
	sortStrings(g.TeardownOn)
}

func setTrue(p **bool) {
	if *p == nil {
		t := true
		*p = &t
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// sanitizeName turns a directory name into a valid manifest name.
func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ' || r == '.':
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

// normalizeAuth fills in the defaults an adapter can assume.
//
// Only where there is one right answer. auth.adapter defaults to auto because
// detection is better than a guess written into every manifest, and the Auth0
// connection defaults to the name Auth0 itself creates.
func normalizeAuth(m *schema.Manifest) {
	if m.Auth == nil {
		return
	}
	a := m.Auth
	if a.Adapter == "" {
		a.Adapter = schema.AuthAuto
	}
	if a.Adapter == schema.AuthAuth0 && a.Connection == "" {
		a.Connection = "Username-Password-Authentication"
	}
	if a.Table != nil {
		if a.Table.Schema == "" {
			a.Table.Schema = "public"
		}
		if a.Table.ID == "" {
			a.Table.ID = "id"
		}
		if a.Table.Email == "" {
			a.Table.Email = "email"
		}
	}
}
