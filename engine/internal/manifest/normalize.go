package manifest

import (
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
	DefaultTTL           = "168h"
	DefaultIdleSleep     = "30m"
	DefaultDomain        = "localhost"
	DefaultNamespacePfx  = "af"
	DefaultStartPath     = "/"
	DefaultWorkflowSteps = 60
	DefaultWorkflowUSD   = 0.5
	DefaultWorkflowTime  = "10m"
	DefaultLoadScale     = 0.05
	DefaultLoadDuration  = "2m"
	DefaultRegressionFac = 1.5
	DefaultRegressionMS  = 5
	DefaultLargeTable    = 100000
	// DefaultPersonaDomain is reserved by RFC 6761 and can never receive mail,
	// so a persona address cannot become a real one by accident.
	DefaultPersonaDomain = "example.test"
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
	normalizeWorkflows(m)
	normalizeInsights(m)
	normalizeLoad(m)
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
	for i := range m.Personas {
		p := &m.Personas[i]
		if p.Login == "" {
			p.Login = schema.LoginPassword
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
	if l.Thresholds.P95Increase == 0 {
		l.Thresholds.P95Increase = 0.25
	}
	if l.Thresholds.ErrorRate == 0 {
		l.Thresholds.ErrorRate = 0.01
	}
	if l.Thresholds.QueryCountIncrease == 0 {
		l.Thresholds.QueryCountIncrease = 0.2
	}
	for i, r := range l.SafeRoutes {
		l.SafeRoutes[i] = normalizeRoute(r)
	}
	for i, r := range l.UnsafeRoutes {
		l.UnsafeRoutes[i] = normalizeRoute(r)
	}
}

func normalizeRoute(r string) string {
	r = strings.TrimSpace(r)
	if !strings.HasPrefix(r, "/") {
		r = "/" + r
	}
	return path.Clean(r)
}

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
