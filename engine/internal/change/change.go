// Package change reads the diff of a pull request and says which of this
// product's checks will exercise what it touched.
//
// It is a sibling of internal/detect, and the difference between them is the
// whole point. detect reads a REPOSITORY to work out what the project is, so
// af init can write a manifest. This package reads a CHANGE to work out what
// the run will and will not cover. One answers "what is this", the other
// answers "what does this touch".
//
// Three rules govern everything here, and they are rules rather than
// preferences because each one has an obvious wrong version that would be
// easier to write and worse to trust.
//
// It never claims to understand intent. Every sentence this package produces
// has the form "this file is X, and X is exercised by check Y". It does not
// say a change is risky, and it never says a change is safe. A tool that calls
// a change safe is making a promise the terms of this product deliberately do
// not make, and it would be making it from a file listing.
//
// An unrecognised path selects every check. The incentive over the life of a
// classifier like this one is to prune: somebody notices that .parquet files
// always trigger a full run, adds a rule mapping them to "data", and quietly
// removes coverage from a category nobody examined. So the fail safe is
// written down as one function, runEverything, and there is a test that feeds
// it a path no rule can match and asserts the plan holds every check. When
// classification is incomplete the answer is more work, never less.
//
// Every conclusion names its file and its rule. A profile whose reasoning
// cannot be audited is astrology, and a reviewer who cannot see why the
// analyser thinks api/billing.ts is the billing service cannot correct it.
package change

import (
	"sort"
	"strconv"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Limits on what a diff may be before classification gives up and falls back
// to running everything. A pull request above these is not a pull request
// anybody reviews either.
const (
	// MaxFiles is how many changed files are classified. Above it the profile
	// is truncated, which selects every check.
	MaxFiles = 2000
	// MaxAddedLines is how many added lines are read per file for the content
	// rules. A generated lockfile is fifty thousand lines and none of them
	// need reading twice.
	MaxAddedLines = 4000
	// MaxHostsPerFile bounds how many distinct outbound hosts one file
	// contributes, so a checked in list of domains cannot produce a profile
	// longer than the diff.
	MaxHostsPerFile = 20
)

// Status is what the diff did to a path.
type Status string

const (
	StatusAdded    Status = "added"
	StatusModified Status = "modified"
	StatusDeleted  Status = "deleted"
	StatusRenamed  Status = "renamed"
)

// File is one changed path, as read from a diff.
type File struct {
	// Path is the new path, slash separated, relative to the repository root.
	Path string
	// OldPath is where a renamed file came from, and is otherwise empty.
	OldPath string
	Status  Status
	Added   int
	Removed int
	// Binary reports that the diff carried no text for this file, so the
	// content rules could not read it.
	Binary bool
	// AddedLines are the lines this diff adds, without the leading plus, each
	// with the line number it will have in the new file. It is empty when the
	// diff was produced without hunks, which the content rules treat as "not
	// read" rather than as "nothing found".
	AddedLines []AddedLine
	// LinesTruncated reports that AddedLines was cut at MaxAddedLines.
	LinesTruncated bool
}

// Surface is the category a path falls into. It is a fact about the file, not
// a judgement about the change.
type Surface string

const (
	// SurfaceSchema is anything that changes the database's shape: a migration
	// directory, a .sql file, a schema definition a migration tool reads.
	SurfaceSchema Surface = "schema"
	// SurfaceService marks a file attributed to a service the manifest
	// declares, by the path that manifest gave. It coexists with the file's
	// other surface rather than replacing it.
	SurfaceService Surface = "service"
	// SurfaceCode is application source.
	SurfaceCode Surface = "code"
	// SurfaceAsset is something the application serves: a stylesheet, an
	// image, a template.
	SurfaceAsset Surface = "asset"
	// SurfaceBuild is how the application becomes an image.
	SurfaceBuild Surface = "build"
	// SurfaceDependency is a lockfile or a package manifest.
	SurfaceDependency Surface = "dependency"
	// SurfaceConfig is configuration the application reads at runtime.
	SurfaceConfig Surface = "config"
	// SurfaceInfrastructure is infrastructure as code.
	SurfaceInfrastructure Surface = "infrastructure"
	// SurfacePipeline is continuous integration configuration.
	SurfacePipeline Surface = "pipeline"
	// SurfaceManifest is antifailure.yaml itself.
	SurfaceManifest Surface = "manifest"
	// SurfaceMasking is the masking rules file the manifest names.
	SurfaceMasking Surface = "masking"
	// SurfaceTest is the project's own test suite, which this product does not
	// run.
	SurfaceTest Surface = "test"
	// SurfaceDocs is prose.
	SurfaceDocs Surface = "docs"
	// SurfaceEgress marks an outbound host found in an added line.
	SurfaceEgress Surface = "egress"
	// SurfaceUnknown is a path no rule claimed. It is the fail safe: one of
	// these selects every check.
	SurfaceUnknown Surface = "unknown"
)

// Fact is one observation about one file. Every field that carries a
// conclusion sits next to the file and the rule that produced it, because a
// conclusion without those two is not auditable.
type Fact struct {
	Path string `json:"path"`
	// Status is what the diff did to the path.
	Status Status `json:"status"`
	// Surface is the category this fact assigns.
	Surface Surface `json:"surface"`
	// Subject names what the fact is about when the surface has one: a service
	// name, an outbound host.
	Subject string `json:"subject,omitempty"`
	// Rule is the stable identifier of the rule that fired, for example
	// "path.migration" or "manifest.service". It is what a reader greps for
	// when the classification is wrong.
	Rule string `json:"rule"`
	// Evidence is one sentence naming what matched, in the present tense.
	Evidence string `json:"evidence"`
	// Line is the added line number a content rule matched on, and is zero for
	// a rule that only read the path.
	Line int `json:"line,omitempty"`
}

// Check is one of the things a run can do. The vocabulary is deliberately the
// set of checks the engine actually has, not a set of aspirations: a plan that
// names a check nobody implemented is worse than no plan.
type Check string

const (
	// CheckEnvironment is bringing the environment up: build, start, migrate.
	CheckEnvironment Check = "environment"
	// CheckMigration is the migration rehearsal and plan diff in af insights.
	CheckMigration Check = "migration"
	// CheckInvariants is the read only statements asked of the database after
	// the workflows.
	CheckInvariants Check = "invariants"
	// CheckWorkflows is the agents driving a browser.
	CheckWorkflows Check = "workflows"
	// CheckLoad is production shaped traffic.
	CheckLoad Check = "load"
	// CheckEgress is the firewall's decisions about outbound requests.
	CheckEgress Check = "egress"
	// CheckMasking is the verification scan over the golden.
	CheckMasking Check = "masking"
)

// Checks returns every check, in the order a report renders them.
func Checks() []Check {
	return []Check{
		CheckEnvironment, CheckMigration, CheckInvariants,
		CheckWorkflows, CheckLoad, CheckEgress, CheckMasking,
	}
}

// Selection is what the plan says about one check.
type Selection struct {
	Check Check `json:"check"`
	// Selected reports whether the diff selects this check.
	Selected bool `json:"selected"`
	// Available reports whether the manifest configures it at all. A check
	// that is selected and unavailable is the most useful line in the report:
	// the change touched something and nothing will look at it.
	Available bool `json:"available"`
	// Unavailable says why, in one sentence, when Available is false.
	Unavailable string `json:"unavailable,omitempty"`
	// Because names the facts that selected it, each naming a path.
	Because []string `json:"because,omitempty"`
}

// Run reports whether this check should actually be run.
//
// Selected and Available are kept apart everywhere else because they answer
// different questions and a reader needs both: selected says the change
// touched something the check covers, available says the manifest configures
// it. A workflow step deciding whether to do work needs the conjunction, and
// this is the one place it is taken, so that "selected" never quietly comes to
// mean "runnable" in the reporting.
func (s Selection) Run() bool { return s.Selected && s.Available }

// Profile is the whole answer for one diff.
type Profile struct {
	Base string `json:"base,omitempty"`
	Head string `json:"head,omitempty"`
	// Files is how many changed paths were classified.
	Files int `json:"files"`
	// Facts are sorted, so two runs over the same diff produce identical
	// output and a report can be diffed against an earlier one.
	Facts []Fact `json:"facts"`
	// Unclassified are paths no rule claimed. Any entry here makes the plan
	// hold every check.
	Unclassified []string `json:"unclassified,omitempty"`
	// Plan is one entry per check, in Checks order.
	Plan []Selection `json:"plan"`
	// Blind is what this analysis cannot see, stated rather than implied.
	Blind []string `json:"blind"`
	// Truncated reports that the diff was larger than MaxFiles.
	Truncated bool `json:"truncated"`
	// Everything reports that the plan holds every check because
	// classification was incomplete, rather than because the diff selected
	// them one by one.
	Everything bool `json:"everything"`
}

// Options are the inputs to one analysis.
type Options struct {
	// Truncated says the diff handed in is already incomplete, because the
	// reader hit a byte limit. It selects every check, for the same reason
	// exceeding MaxFiles does.
	Truncated bool
	// Manifest is what the checks are read from. It may be nil, in which case
	// every check that needs configuration is reported unavailable.
	Manifest *schema.Manifest
	// Base and Head are the refs the diff came from, carried through to the
	// report so a reader can reproduce it.
	Base, Head string
	// Files is the diff.
	Files []File
}

// Analyze classifies a diff and produces the plan.
//
// It does no I/O and takes no clock: the same diff and the same manifest give
// the same profile forever, which is what makes the output reviewable in a
// pull request comment.
func Analyze(opts Options) *Profile {
	p := &Profile{Base: opts.Base, Head: opts.Head}

	files := opts.Files
	p.Truncated = opts.Truncated
	if len(files) > MaxFiles {
		files = files[:MaxFiles]
		p.Truncated = true
	}
	p.Files = len(files)

	// Sorted first so that the walk below, and therefore the facts and the
	// reasons attached to each check, do not depend on the order git happened
	// to print the diff in.
	sorted := make([]File, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	// A manifest that reaches here has already been validated, so a policy
	// that will not compile should be impossible. It is carried rather than
	// dropped anyway, because the alternative is an analysis that silently
	// reports no outbound hosts, which reads exactly like a change that calls
	// nothing.
	engine, policyErr := policy.New(manifestEgress(opts.Manifest))

	for _, f := range sorted {
		facts := classify(f, opts.Manifest, engine)
		p.Facts = append(p.Facts, facts...)
		if !anyBase(facts) {
			p.Unclassified = append(p.Unclassified, f.Path)
		}
	}
	sortFacts(p.Facts)
	sort.Strings(p.Unclassified)

	p.Everything = p.runEverything()
	p.Plan = plan(p, opts.Manifest)
	p.Blind = blindSpots(p, sorted, opts.Manifest)
	if policyErr != nil {
		p.Blind = append(p.Blind, "The egress policy in this manifest would not compile ("+
			policyErr.Error()+"), so no outbound host in this diff was checked against it.")
	}
	return p
}

// runEverything is the fail safe, and it is one function on purpose.
//
// The pressure on a classifier like this one is always toward pruning: a rule
// gets added so that a noisy category stops selecting a full run, and coverage
// leaves with it. Keeping the rule in one named place means the next person to
// weaken it has to weaken this, in a diff, next to the comment saying why not.
//
// Three conditions, and all of them mean the same thing: classification is
// incomplete, so the plan cannot be narrowed by it.
//
//	a path matched no rule
//	the diff was larger than the limit and part of it was never read
//	the diff was empty, which is either a change with no files or a base ref
//	that is not this change's base, and those are indistinguishable here
func (p *Profile) runEverything() bool {
	return len(p.Unclassified) > 0 || p.Truncated || p.Files == 0
}

// anyBase reports whether any fact assigned the file a base surface. Service
// attribution and the outbound host rule are additions to a classification
// rather than classifications on their own: a file known only as "part of the
// billing service" is still a file whose kind nothing recognised.
func anyBase(facts []Fact) bool {
	for _, f := range facts {
		if f.Surface != SurfaceService && f.Surface != SurfaceEgress {
			return true
		}
	}
	return false
}

func manifestEgress(m *schema.Manifest) *schema.Egress {
	if m == nil {
		return nil
	}
	return m.Egress
}

// sortFacts orders facts so that output is identical across runs.
func sortFacts(f []Fact) {
	sort.SliceStable(f, func(i, j int) bool {
		if f[i].Path != f[j].Path {
			return f[i].Path < f[j].Path
		}
		if f[i].Rule != f[j].Rule {
			return f[i].Rule < f[j].Rule
		}
		if f[i].Line != f[j].Line {
			return f[i].Line < f[j].Line
		}
		return f[i].Subject < f[j].Subject
	})
}

// Selected returns the checks the diff selects, in Checks order, WHETHER OR
// NOT the manifest can run them.
//
// This is the analyser's own answer and not an instruction to anybody. Telling
// a runner to run these would tell it to run checks the manifest has turned
// off, which is the one mistake the Selected and Available split exists to
// prevent. Anything deciding whether to do work wants Runnable.
func (p *Profile) Selected() []Check {
	var out []Check
	for _, s := range p.Plan {
		if s.Selected {
			out = append(out, s.Check)
		}
	}
	return out
}

// Selects reports whether the diff selects one check, ignoring availability
// for the same reason Selected does. See Runnable before reaching for it.
func (p *Profile) Selects(c Check) bool {
	for _, s := range p.Plan {
		if s.Check == c {
			return s.Selected
		}
	}
	return false
}

// Runnable returns the checks that will actually run: selected by the diff and
// configured in the manifest.
//
// This is the one a caller deciding whether to do work should use, and it
// exists as a named thing so that the safe answer is the one with the obvious
// name. The distinction is invisible in a green test and expensive in a
// workflow: a check that is selected and unavailable is a sentence for the
// report, never a step for a runner.
func (p *Profile) Runnable() []Check {
	var out []Check
	for _, s := range p.Plan {
		if s.Run() {
			out = append(out, s.Check)
		}
	}
	return out
}

// Gaps returns the checks the diff selects that the manifest cannot run. These
// are the sentences worth reading: something changed and nothing will look at
// it.
func (p *Profile) Gaps() []Selection {
	var out []Selection
	for _, s := range p.Plan {
		if s.Selected && !s.Available {
			out = append(out, s)
		}
	}
	return out
}

// coverage maps a surface to the checks that exercise it.
//
// This table is the claim the whole package makes, so it is written once,
// here, rather than spread through the renderers. Read it as: a file of this
// kind changed, and these are the checks that will actually touch it. A
// surface with no checks is not an oversight; it is this product saying it
// does not exercise that, which the blind spots then say out loud.
var coverage = map[Surface][]Check{
	SurfaceSchema:     {CheckEnvironment, CheckMigration, CheckInvariants, CheckLoad},
	SurfaceCode:       {CheckEnvironment, CheckWorkflows, CheckLoad},
	SurfaceAsset:      {CheckEnvironment, CheckWorkflows},
	SurfaceBuild:      {CheckEnvironment},
	SurfaceDependency: {CheckEnvironment, CheckEgress},
	SurfaceConfig:     {CheckEnvironment, CheckWorkflows},
	SurfaceManifest:   {CheckEnvironment, CheckEgress},
	SurfaceMasking:    {CheckMasking},
	SurfaceEgress:     {CheckEgress},

	// A service attribution adds the workflows, because a workflow drives the
	// application through its interface and a web service is what that
	// interface is. Whether the workflows reach THIS service is not knowable
	// from a diff, which the blind spots say.
	SurfaceService: {CheckEnvironment, CheckWorkflows},

	// Deliberately empty. The environment is built from the manifest and not
	// from your Terraform, this product does not run your test suite, and
	// nothing here reads your pull request template.
	SurfaceInfrastructure: nil,
	SurfacePipeline:       nil,
	SurfaceTest:           nil,
	SurfaceDocs:           nil,
}

// plan turns the facts into one entry per check.
func plan(p *Profile, m *schema.Manifest) []Selection {
	because := map[Check][]string{}
	selected := map[Check]bool{}

	add := func(c Check, reason string) {
		if !selected[c] {
			selected[c] = true
		}
		for _, existing := range because[c] {
			if existing == reason {
				return
			}
		}
		because[c] = append(because[c], reason)
	}

	if p.Everything {
		for _, c := range Checks() {
			add(c, everythingReason(p))
		}
	}

	for _, f := range p.Facts {
		for _, c := range coverage[f.Surface] {
			add(c, f.Path+": "+f.Evidence)
		}
	}

	out := make([]Selection, 0, len(Checks()))
	for _, c := range Checks() {
		s := Selection{Check: c, Selected: selected[c], Because: because[c]}
		s.Available, s.Unavailable = available(c, m)
		out = append(out, s)
	}
	return out
}

func everythingReason(p *Profile) string {
	switch {
	case p.Files == 0:
		return "the diff is empty, which is either a change with no files or the wrong base ref, and this cannot tell them apart"
	case p.Truncated:
		return "the diff is larger than the " + strconv.Itoa(MaxFiles) + " file limit, so part of it was never classified"
	default:
		return "at least one path matched no rule, so classification is incomplete"
	}
}

// available reports whether the manifest configures a check, and says why not
// when it does not.
//
// A check that cannot run is reported as unavailable rather than as
// unselected, because those two mean opposite things to a reader. Unselected
// says the change did not touch anything it covers. Unavailable says the
// change did touch it and nothing is going to look.
func available(c Check, m *schema.Manifest) (bool, string) {
	if m == nil {
		return false, "no manifest was loaded, so nothing about this check is configured"
	}
	switch c {
	case CheckEnvironment:
		if len(m.Services) == 0 {
			return false, "the manifest declares no services"
		}
		return true, ""
	case CheckMigration:
		cfg := insights.Configure(m.Insights)
		if !cfg.Enabled {
			return false, "insights are turned off in the manifest"
		}
		if !cfg.MigrationRehearsal && !cfg.PlanDiff {
			return false, "both the migration rehearsal and the plan diff are turned off in the manifest"
		}
		return true, ""
	case CheckInvariants:
		if len(m.Invariants) == 0 {
			return false, "the manifest declares no invariants, so nothing is asked of the data after the workflows"
		}
		return true, ""
	case CheckWorkflows:
		if len(m.Workflows) == 0 {
			return false, "the manifest declares no workflows, so nothing drives the application"
		}
		return true, ""
	case CheckLoad:
		if m.Load == nil || !m.Load.Enabled {
			return false, "load is off in the manifest, and af ci generates it only with --load"
		}
		return true, ""
	case CheckEgress:
		// Always available. With no egress block the default is block, which
		// is a policy, and every outbound request still produces a decision.
		return true, ""
	case CheckMasking:
		if m.Database == nil || m.Database.MaskingRules == "" {
			return false, "the manifest names no masking rules file"
		}
		return true, ""
	}
	return false, "this check is not known"
}

// blindSpots says what the analysis cannot see.
//
// Two of these are unconditional and they are the two that matter. Everything
// else here is a specific consequence of what this particular diff contained.
func blindSpots(p *Profile, files []File, m *schema.Manifest) []string {
	out := []string{
		"This reads paths and added lines. It does not run the program, so a one line change to a configuration default can change behaviour that nothing here can see, and a thousand line refactor that changes nothing will still select every check its files touch.",
		"Nothing here says this change is safe. It says which checks will exercise the files it touched, and which will not.",
	}

	counts := map[Surface]int{}
	services := map[string]bool{}
	for _, f := range p.Facts {
		counts[f.Surface]++
		if f.Surface == SurfaceService {
			services[f.Subject] = true
		}
	}

	var deleted, binary, cut int
	var renames []string
	for _, f := range files {
		switch f.Status {
		case StatusDeleted:
			deleted++
		case StatusRenamed:
			if f.OldPath != "" && len(renames) < 5 {
				renames = append(renames, f.OldPath+" became "+f.Path)
			}
		}
		if f.Binary {
			binary++
		}
		if f.LinesTruncated {
			cut++
		}
	}

	if deleted > 0 {
		out = append(out, plural(deleted, "file was", "files were")+
			" deleted. A caller left behind in a file this diff does not touch is not visible here; the build is what finds that.")
	}
	if len(renames) > 0 {
		out = append(out, plural(len(renames), "file was renamed", "files were renamed")+
			", "+englishList(renames)+
			". A path rule reads the new path, so a rename into or out of a category changes the classification without changing a line of code.")
	}
	switch {
	case binary == 1:
		out = append(out, "One file in this diff is binary, so the content rules did not read it and no outbound host in it is reported.")
	case binary > 1:
		out = append(out, strconv.Itoa(binary)+" files in this diff are binary, so the content rules did not read them and no outbound host in them is reported.")
	}

	switch {
	case cut == 1:
		out = append(out, "One file adds more than "+strconv.Itoa(MaxAddedLines)+
			" lines and only the first "+strconv.Itoa(MaxAddedLines)+
			" were read, so an outbound host named below that is not reported.")
	case cut > 1:
		out = append(out, strconv.Itoa(cut)+" files each add more than "+strconv.Itoa(MaxAddedLines)+
			" lines and only the first "+strconv.Itoa(MaxAddedLines)+
			" of each were read, so an outbound host named below that is not reported.")
	}

	if counts[SurfaceSchema] > 0 {
		out = append(out, "Columns this migration adds do not exist in the golden yet, so nothing has checked whether they will need a masking rule once they carry production data. The masking check reads the golden, not the diff.")
	}
	if n := counts[SurfaceInfrastructure]; n > 0 {
		out = append(out, plural(n, "infrastructure file", "infrastructure files")+
			" changed. The environment is built from antifailure.yaml rather than from your infrastructure as code, so no run applies or checks what changed there.")
	}
	if n := counts[SurfacePipeline]; n > 0 {
		out = append(out, plural(n, "continuous integration file", "continuous integration files")+
			" changed. Nothing in a run reads continuous integration configuration.")
	}
	if n := counts[SurfaceTest]; n > 0 {
		out = append(out, plural(n, "file in your own test suite", "files in your own test suite")+
			" changed. This product does not run your test suite; it runs the workflows the manifest declares.")
	}

	// Two services claiming the same file is the manifest saying both are
	// built from it, and the report has to say that rather than letting a
	// reader assume the first name is the answer.
	perFile := map[string]int{}
	for _, f := range p.Facts {
		if f.Surface == SurfaceService {
			perFile[f.Path]++
		}
	}
	shared := 0
	for _, n := range perFile {
		if n > 1 {
			shared++
		}
	}
	if shared > 0 {
		out = append(out, plural(shared, "changed file is", "changed files are")+
			" attributed to more than one service, because the manifest declares those services at the same path. Which one a line belongs to is not visible from a diff.")
	}

	// A worker is the sharpest of these. The site's own example diff touches a
	// billing worker, and a browser agent cannot reach one.
	if m != nil {
		var offline []string
		for _, s := range m.Services {
			if !services[s.Name] {
				continue
			}
			if s.Kind == schema.ServiceWorker || s.Kind == schema.ServiceCron {
				offline = append(offline, s.Name+" is a "+string(s.Kind))
			}
		}
		sort.Strings(offline)
		if len(offline) > 0 {
			out = append(out, "The workflow agents drive a browser, and "+strings.Join(offline, ", ")+
				". A change to one is exercised only where the application's own interface reaches it, and this cannot tell whether it does.")
		}
	}

	if len(p.Facts) > 0 && len(p.Unclassified) == 0 && !p.Truncated && p.Files > 0 {
		// Only worth saying when the classification was in fact complete,
		// because otherwise the plan is already everything.
		out = append(out, "A check that is not selected was not run against this change. That is a statement about what was exercised, not a finding that the untouched parts are correct.")
	}
	return out
}

// plural picks the singular or the plural form and prefixes the count.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
