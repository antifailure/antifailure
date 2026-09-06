package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/golden"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/verify"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// A golden is the masked, verified copy of production every environment
// branches from, and it is the one thing in this product whose absence stops
// everything else.
//
// So the two questions worth exposing are "is there one this project can
// branch, and is it stale" and "make me one". They are separate tools because
// the first is free and the second takes minutes and, for a refresh, is the
// only place in the whole product where unmasked data is read.

// readGoldens returns the versions the provider is holding and the provenance
// string that identifies this project's own.
//
// Both, because a golden pool is shared: on a local daemon it holds every
// project on the machine, and a listing that does not say whose is what made
// an unbranchable golden look like a usable one.
type readGoldens func(ctx context.Context) (versions []provider.GoldenVersion, mine string, err error)

// readPublishedGoldens lists what this project publishes to its store, with
// the store's name. An empty name means this project publishes nowhere, which
// is not a failure.
type readPublishedGoldens func(ctx context.Context) (objects []golden.Object, store string, err error)

// readGoldenPolicy reads the lifecycle settings out of the manifest.
type readGoldenPolicy func() (env.GoldenPolicy, error)

// destroyGolden removes one version. The provider refuses a version something
// is still branched from, which is the refusal that matters and the only place
// that knows.
type destroyGolden func(ctx context.Context, version string) error

// goldenOutcome is the result of a refresh, a pull or a verify, in one shape.
//
// One shape because the three answer the same question, which is whether this
// project now has a golden it may branch, and a caller polling one run should
// not have to know which of the three it submitted to read the answer.
type goldenOutcome struct {
	// Action is refresh, pull or verify.
	Action string
	// Version is the version this project may now branch, empty when the
	// operation produced none.
	Version string
	// From is the version identifier in the store, for a pull. A pulled
	// golden gets a NEW identifier on this machine, because an identifier
	// carries the time it was made and the copy here was made now.
	From string
	// Verified reports whether the verification scan passed. A golden that
	// fails it is never published and can never be branched.
	Verified bool
	Report   verify.Report
	// Rows and Tables are what a refresh masked.
	Rows   int64
	Tables int
	// Bytes is what a pull transferred.
	Bytes int64
	// Published names the store a refresh copied to, empty when this project
	// publishes nowhere.
	Published string
	Duration  time.Duration
}

// prepareGolden runs one of the three long operations.
type prepareGolden func(ctx context.Context, action, version string) (goldenOutcome, error)

// goldenVersionSchema is the shared declaration of a version identifier.
//
// The pattern is the shape the engine mints, gv_ followed by a timestamp and a
// hash, so a path, a branch name or a wildcard is refused before it reaches a
// provider.
func goldenVersionSchema(purpose string) *Schema {
	return &Schema{
		Type: "string", MaxLength: 64, MinLength: 4, Pattern: `gv_[0-9]{8,20}_[0-9a-f]{4,32}`,
		Description: purpose + " It is a version identifier inspect_goldens reports, such " +
			"as gv_20260830044013_74234e98. It is never a pattern: this server has no " +
			"wildcard and every identifier names one version.",
	}
}

// ---------------------------------------------------------------------------
// inspect_goldens
// ---------------------------------------------------------------------------

type goldensResult struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	// Branchable is the newest verified version this project may branch, empty
	// when there is none. It is the field a caller reads to answer "can af up
	// work at all", so it is stated rather than left to be derived.
	Branchable string             `json:"branchable_version,omitempty"`
	Versions   []goldenVersionDoc `json:"versions"`
	Total      int                `json:"total"`
	Shown      int                `json:"shown"`
	Truncated  bool               `json:"truncated"`
	// Published is what this project's store holds, which is what another
	// machine would pull rather than refresh.
	Published      []publishedGoldenDoc `json:"published,omitempty"`
	PublishedStore string               `json:"published_store,omitempty"`
	// PublishedUnavailable says the store could not be listed, which is
	// different from a store with nothing in it and is never reported as one.
	PublishedUnavailable string           `json:"published_unavailable,omitempty"`
	Policy               *goldenPolicyDoc `json:"policy,omitempty"`
	Note                 string           `json:"note,omitempty"`
}

type goldenVersionDoc struct {
	Version   string  `json:"version"`
	Verified  bool    `json:"verified"`
	CreatedAt string  `json:"created_at,omitempty"`
	AgeHours  float64 `json:"age_hours,omitempty"`
	SizeBytes int64   `json:"size_bytes,omitempty"`
	// RulesRecorded says whether the masking rules that produced this version
	// were recorded. False means what is unknown is whether the rules have
	// changed since, which is the one thing somebody reads this column for.
	RulesRecorded bool `json:"masking_rules_recorded"`
	// Mine says whether this project may branch it. A version made for
	// another project is refused by the engine rather than branched.
	Mine bool `json:"branchable_by_this_project"`
}

type publishedGoldenDoc struct {
	Name     string `json:"name"`
	SizeByte int64  `json:"size_bytes"`
	Modified string `json:"modified,omitempty"`
}

type goldenPolicyDoc struct {
	// Retain is how many of the newest to keep, from database.golden.retain.
	Retain int `json:"retain"`
	// MaxAgeHours is how old a golden may be before it is considered stale.
	MaxAgeHours float64 `json:"max_age_hours,omitempty"`
	// Schedule is when a refresh is due, in the manifest's own words.
	Schedule string `json:"refresh_schedule,omitempty"`
	// NextRefresh is when the schedule next fires after the newest version.
	NextRefresh string `json:"next_scheduled_refresh,omitempty"`
	// Stale reports that the newest version this project can branch is older
	// than max_age.
	Stale bool `json:"newest_is_stale"`
}

// maxGoldensReported bounds the listing, which grows with the machine.
const maxGoldensReported = 40

func newInspectGoldensTool(
	p *Project, read readGoldens, published readPublishedGoldens, policy readGoldenPolicy,
) *Tool {
	return &Tool{
		Name:     "inspect_goldens",
		Title:    "See the masked copies",
		ReadOnly: true,
		Description: "Report the masked copies of production this project can branch an " +
			"environment from: which exist, which passed verification, which belong to this " +
			"project rather than another one on this machine, how old the newest is, and " +
			"what has been published to this project's store for other machines to pull. " +
			"Call this when an environment will not come up, when a rehearsal is refused " +
			"for want of a golden, or before deciding whether to refresh. A version that " +
			"failed verification is never published and can never be branched, so it is " +
			"reported and is not an option. This reads and changes nothing.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id": projectIDSchema(),
				"include_published": {
					Type: "boolean",
					Description: "Optional, true by default. Also list what this project's " +
						"store holds, which needs a round trip to that store. Set it false " +
						"when only the local versions matter.",
				},
			},
		},
		Handler: func(ctx context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			return inspectGoldens(ctx, read, published, policy, args)
		},
	}
}

func inspectGoldens(
	ctx context.Context, read readGoldens, published readPublishedGoldens,
	policy readGoldenPolicy, args map[string]any,
) (any, *Fault) {
	versions, mine, err := read(ctx)
	if err != nil {
		return nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The golden versions could not be listed, so this says nothing about " +
				"what this project can branch. The database provider has to be reachable " +
				"for that.",
			Retryable: true, wrapped: err,
		}
	}

	out := goldensResult{Kind: "golden_inventory", Versions: []goldenVersionDoc{}}
	docs, branchable, newest := describeGoldens(versions, mine)
	out.Total = len(docs)
	if len(docs) > maxGoldensReported {
		out.Truncated = true
		out.Note = fmt.Sprintf(
			"%d versions exist and the %d newest are shown, newest first. Everything "+
				"withheld is older than the last one here.", len(docs), maxGoldensReported)
		docs = docs[:maxGoldensReported]
	}
	out.Versions, out.Shown, out.Branchable = docs, len(docs), branchable

	if p, err := policy(); err == nil {
		doc := describeGoldenPolicy(p, newest)
		out.Policy = &doc
	}

	includePublished := true
	if raw, ok := args["include_published"].(bool); ok {
		includePublished = raw
	}
	if includePublished {
		objects, store, err := published(ctx)
		switch {
		case err != nil:
			// Named as unavailable rather than left empty. An empty list and
			// an unreachable store look identical and mean opposite things,
			// and reporting the second as the first is how "nothing has been
			// published" gets said about a store nobody could read.
			out.PublishedUnavailable = withCause("This project's golden store could not be "+
				"listed, so nothing here says whether anything is published.", err)
		case store != "":
			out.PublishedStore = store
			out.Published = describePublished(objects)
		}
	}
	out.Summary = goldensSummary(out, len(versions))
	return out, nil
}

func describeGoldens(
	versions []provider.GoldenVersion, mine string,
) (docs []goldenVersionDoc, branchable string, newest time.Time) {
	sorted := append([]provider.GoldenVersion(nil), versions...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
		}
		return sorted[i].ID > sorted[j].ID
	})

	docs = make([]goldenVersionDoc, 0, len(sorted))
	for _, v := range sorted {
		id, _ := safeIdentifier(v.ID)
		// Mine is an exact comparison against the provenance this project
		// computes, not a prefix. Two unrelated projects that declare no
		// masking rules hash their rules to the same value, so provenance is
		// the only field that can answer this, and an empty one means the
		// version predates the field and the engine refuses to branch it
		// rather than guessing.
		isMine := v.Provenance != "" && v.Provenance == mine
		doc := goldenVersionDoc{
			Version: id, Verified: v.Verified, SizeBytes: v.SizeBytes,
			RulesRecorded: v.RulesHash != "", Mine: isMine,
		}
		if !v.CreatedAt.IsZero() {
			doc.CreatedAt = v.CreatedAt.UTC().Format(time.RFC3339)
			doc.AgeHours = time.Since(v.CreatedAt).Hours()
		}
		if branchable == "" && isMine && v.Verified {
			branchable = id
			newest = v.CreatedAt
		}
		docs = append(docs, doc)
	}
	return docs, branchable, newest
}

func describePublished(objects []golden.Object) []publishedGoldenDoc {
	sort.SliceStable(objects, func(i, j int) bool {
		return objects[i].Modified.After(objects[j].Modified)
	})
	out := make([]publishedGoldenDoc, 0, len(objects))
	for i, o := range objects {
		if i >= maxGoldensReported {
			break
		}
		doc := publishedGoldenDoc{SizeByte: o.Size}
		doc.Name, _ = safeIdentifier(o.Name)
		if !o.Modified.IsZero() {
			doc.Modified = o.Modified.UTC().Format(time.RFC3339)
		}
		out = append(out, doc)
	}
	return out
}

func describeGoldenPolicy(p env.GoldenPolicy, newest time.Time) goldenPolicyDoc {
	doc := goldenPolicyDoc{Retain: p.Retain}
	if p.MaxAge > 0 {
		doc.MaxAgeHours = p.MaxAge.Hours()
		if !newest.IsZero() && time.Since(newest) > p.MaxAge {
			doc.Stale = true
		}
	}
	if !p.Schedule.Zero() {
		doc.Schedule = safeText(p.Schedule.String(), 100)
		if !newest.IsZero() {
			if next := p.Schedule.Next(newest); !next.IsZero() {
				doc.NextRefresh = next.UTC().Format(time.RFC3339)
			}
		}
	}
	return doc
}

func goldensSummary(out goldensResult, total int) string {
	if total == 0 {
		return "There are no goldens at all, so no environment can be brought up and no " +
			"migration can be rehearsed. Make one with prepare_golden, or pull one this " +
			"project has already published."
	}
	mine, verified := 0, 0
	for _, v := range out.Versions {
		if v.Mine {
			mine++
			if v.Verified {
				verified++
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s here, %d made for this project and %d of those verified. ",
		total, plural(total, "golden version is", "golden versions are"), mine, verified)
	switch out.Branchable {
	case "":
		b.WriteString("NONE of them can be branched by this project, so nothing can be " +
			"brought up: a version another project made, or one that failed verification, " +
			"is refused by the engine rather than used. Make one with prepare_golden. ")
	default:
		fmt.Fprintf(&b, "The newest this project can branch is %s. ", out.Branchable)
	}
	if out.Policy != nil && out.Policy.Stale {
		fmt.Fprintf(&b, "It is older than the %.0f hours this project treats as fresh, so "+
			"an environment made from it is that far behind production's shape. ",
			out.Policy.MaxAgeHours)
	}
	if out.PublishedUnavailable != "" {
		b.WriteString("The published store could not be read, so this says nothing about " +
			"what other machines could pull.")
	}
	return strings.TrimSpace(b.String())
}

// ---------------------------------------------------------------------------
// prepare_golden
// ---------------------------------------------------------------------------

func newPrepareGoldenTool(p *Project, eng *Engine, run prepareGolden) *Tool {
	return &Tool{
		Name:  "prepare_golden",
		Title: "Make a golden this project can branch",
		// Not read only: it creates a version, and a refresh reads production.
		// Not destructive: nothing existing is removed by any of the three.
		ReadOnly: false,
		Description: "Produce a masked copy of production this project can branch, in one " +
			"of three ways. pull brings a copy this project already published onto this " +
			"machine and is what most machines should do. refresh READS PRODUCTION, masks " +
			"it, reads it back to check the masking, and publishes it only if that check " +
			"passes; it is the only operation in this product that touches unmasked data, " +
			"it needs the production credential, and it belongs on the one machine that " +
			"holds it. verify re-checks a version that already exists, which is worth doing " +
			"because a golden published under one set of masking rules is not verified " +
			"under another. None of them can skip verification and none of them can publish " +
			"a version that failed it. This takes minutes, so it returns a run_id " +
			"immediately: poll it with get_rehearsal_run. A verdict of INCONCLUSIVE means " +
			"the operation did not finish and says nothing about the golden.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id", "action"},
			Properties: map[string]*Schema{
				"project_id":      projectIDSchema(),
				"idempotency_key": idempotencyKeySchema(),
				"action": {
					Type: "string",
					Enum: []string{"pull", "refresh", "verify"},
					Description: "What to do. pull brings a published copy onto this machine " +
						"and verifies it here; prefer it unless this is the machine that " +
						"refreshes. refresh reads production through the masking pipeline, " +
						"which is expensive and privileged. verify re-checks an existing " +
						"version and needs the version field.",
				},
				"version": goldenVersionSchema(
					"Required for verify and optional for pull, where omitting it takes the " +
						"newest complete published version. Ignored by refresh, which makes " +
						"a new one."),
			},
		},
		Handler: func(_ context.Context, call *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			action, _ := args["action"].(string)
			version, _ := args["version"].(string)
			if action == "verify" && version == "" {
				// Refused at submission rather than minutes later inside the
				// run, and refused here rather than in the schema because a
				// field required by only one value of another cannot be
				// expressed in the published document.
				return nil, fieldFault(FaultInvalidArgument, "version",
					"This field is required when action is verify: verify re-checks one "+
						"existing version. inspect_goldens lists the versions that exist.")
			}
			if action == "refresh" && version != "" {
				return nil, fieldFault(FaultInvalidArgument, "version",
					"A refresh makes a new version and cannot be pointed at an existing "+
						"one. Use verify to re-check a version that already exists.")
			}

			return eng.Submit(call, "prepare_golden", args,
				func(ctx context.Context, runID string) (string, *ResultBody, *Fault) {
					return runPrepareGolden(ctx, eng, run, runID, action, version)
				})
		},
	}
}

func runPrepareGolden(
	ctx context.Context, eng *Engine, run prepareGolden, runID, action, version string,
) (string, *ResultBody, *Fault) {
	if eng.Cancelled(ctx, runID) {
		return "", nil, faultf(FaultRunNotCancellable, "This run was cancelled before it started.")
	}
	eng.Phase(ctx, runID, goldenPhase(action))

	outcome, err := run(ctx, action, version)
	if err != nil {
		// A refresh that produced an unverified copy is a REFUSAL and not a
		// crash, and it is the outcome this whole subsystem exists to
		// produce: the copy was not published, so nothing can branch it. It
		// is reported as a finished run with a FAIL verdict rather than as a
		// failed run, because it is evidence about the masking rules.
		if outcome.Version != "" || len(outcome.Report.Findings) > 0 {
			body := goldenBody(outcome)
			return report.VerdictFail, body, nil
		}
		return "", nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The operation could not be carried out, so this says nothing about " +
				"whether a golden is available. A refresh needs the production credential " +
				"and a database provider, a pull needs a configured store, and a verify " +
				"needs the version to exist.",
			Retryable: true, wrapped: err,
		}
	}

	eng.Phase(ctx, runID, "reading the verification scan")
	body := goldenBody(outcome)
	if outcome.Verified {
		return report.VerdictPass, body, nil
	}
	return report.VerdictFail, body, nil
}

func goldenPhase(action string) string {
	switch action {
	case "refresh":
		return "copying production, masking it, and reading it back to check the masking"
	case "pull":
		return "bringing the published golden onto this machine and verifying what arrived"
	default:
		return "branching the golden and reading it back with the detectors"
	}
}

// goldenDoc is the golden specific evidence in a result.
type goldenDoc struct {
	Action   string `json:"action"`
	Version  string `json:"version,omitempty"`
	From     string `json:"pulled_from_version,omitempty"`
	Verified bool   `json:"verified"`
	// Branchable restates Verified in the words that matter: an unverified
	// golden is not a golden with a warning on it, it is one nothing can use.
	Branchable   bool    `json:"branchable"`
	RowsMasked   int64   `json:"rows_masked,omitempty"`
	TablesMasked int     `json:"tables_masked,omitempty"`
	BytesPulled  int64   `json:"bytes_pulled,omitempty"`
	PublishedTo  string  `json:"published_to,omitempty"`
	DurationSec  float64 `json:"duration_seconds,omitempty"`
	ScanTables   int     `json:"verification_tables"`
	ScanColumns  int     `json:"verification_columns"`
	ScanRows     int64   `json:"verification_rows_sampled"`
	ScanSample   int     `json:"verification_sample_size"`
	// SkippedColumns is what the scan could not read. A column nobody could
	// read is not a column that passed, and the engine's own Clean() counts
	// it, so it is reported rather than folded into the finding count.
	SkippedColumns int `json:"verification_columns_skipped"`
	// Findings are columns that still hold something that looks like real
	// data. The value found is NEVER reproduced: it is by definition the
	// production data this whole subsystem exists to keep out of a copy.
	Findings   []goldenFindingDoc `json:"findings"`
	ValuesNote string             `json:"values_note"`
}

type goldenFindingDoc struct {
	Table    string `json:"table"`
	Column   string `json:"column"`
	Detector string `json:"detector"`
	Rows     int64  `json:"rows"`
}

func goldenBody(outcome goldenOutcome) *ResultBody {
	doc := goldenDoc{
		Action: outcome.Action, Verified: outcome.Verified, Branchable: outcome.Verified,
		RowsMasked: outcome.Rows, TablesMasked: outcome.Tables, BytesPulled: outcome.Bytes,
		ScanTables: outcome.Report.Tables, ScanColumns: outcome.Report.Columns,
		ScanRows: outcome.Report.RowsSampled, ScanSample: outcome.Report.SampleSize,
		SkippedColumns: len(outcome.Report.Skipped),
		Findings:       []goldenFindingDoc{},
		ValuesNote: "The values the detectors matched are not reproduced anywhere in this " +
			"result. They are unmasked production data, which is the exact thing this " +
			"check exists to keep out of a copy, and a report is not a way around that. " +
			"Read them with af golden verify on the machine itself.",
	}
	doc.Version, _ = safeIdentifier(outcome.Version)
	if outcome.Version == "" {
		doc.Version = ""
	}
	doc.From, _ = safeIdentifier(outcome.From)
	if outcome.From == "" {
		doc.From = ""
	}
	if outcome.Published != "" {
		doc.PublishedTo = safeText(outcome.Published, 200)
	}
	if outcome.Duration > 0 {
		doc.DurationSec = outcome.Duration.Seconds()
	}
	for i, f := range outcome.Report.Findings {
		if i >= maxFindings {
			break
		}
		table, _ := safeIdentifier(f.Schema + "." + f.Table)
		column, _ := safeIdentifier(f.Column)
		detector, _ := safeIdentifier(f.Detector)
		doc.Findings = append(doc.Findings, goldenFindingDoc{
			Table: table, Column: column, Detector: detector, Rows: int64(f.Rows),
		})
	}

	zero := 0.0
	body := &ResultBody{
		Detail: doc,
		Metrics: []Metric{
			{
				Name: "unmasked_columns_found", Value: float64(len(outcome.Report.Findings)),
				Unit: "columns", Threshold: &zero, Breached: len(outcome.Report.Findings) > 0,
			},
			{
				Name: "columns_not_readable", Value: float64(len(outcome.Report.Skipped)),
				Unit: "columns", Threshold: &zero, Breached: len(outcome.Report.Skipped) > 0,
			},
			{Name: "columns_scanned", Value: float64(outcome.Report.Columns), Unit: "columns"},
			{Name: "rows_sampled", Value: float64(outcome.Report.RowsSampled), Unit: "rows"},
		},
		Evidence: []Evidence{{
			URI: "af://golden", Kind: "command",
			Note: "Run af golden list on the machine for the full inventory, and " +
				"af golden verify for the detector matches this result withholds.",
		}},
	}
	body.Metrics = boundMetrics(body.Metrics)
	body.Summary = goldenSummary(outcome, doc)
	return body
}

func goldenSummary(outcome goldenOutcome, doc goldenDoc) string {
	var b strings.Builder
	switch outcome.Action {
	case "refresh":
		fmt.Fprintf(&b, "Copied production and masked %d rows across %d tables. ",
			outcome.Rows, outcome.Tables)
	case "pull":
		fmt.Fprintf(&b, "Brought %s onto this machine as %s. ",
			orNotRecorded(doc.From), orNotRecorded(doc.Version))
	default:
		fmt.Fprintf(&b, "Re-checked %s by branching it and reading it back. ",
			orNotRecorded(doc.Version))
	}
	fmt.Fprintf(&b, "The verification scan read %d columns across %d tables, sampling %d rows. ",
		outcome.Report.Columns, outcome.Report.Tables, outcome.Report.RowsSampled)

	switch {
	case len(outcome.Report.Findings) > 0:
		fmt.Fprintf(&b, "It found %d %s still holding something that looks like real data, "+
			"so this version was NOT published and nothing can branch it. Add a masking "+
			"rule for each column named below and run this again. ",
			len(outcome.Report.Findings),
			plural(len(outcome.Report.Findings), "column", "columns"))
	case len(outcome.Report.Skipped) > 0:
		fmt.Fprintf(&b, "It could not read %d %s, and a column nobody could read is not a "+
			"column that passed, so this version is not branchable. ",
			len(outcome.Report.Skipped),
			plural(len(outcome.Report.Skipped), "column", "columns"))
	case outcome.Verified:
		fmt.Fprintf(&b, "Nothing that looks like real data survived, so %s is verified and "+
			"this project can branch environments from it. ", orNotRecorded(doc.Version))
	default:
		b.WriteString("It did not pass, so this version is not published and cannot be " +
			"branched. ")
	}
	if outcome.Published != "" {
		fmt.Fprintf(&b, "It was published to %s, so other machines can pull it rather than "+
			"reading production themselves.", doc.PublishedTo)
	}
	return strings.TrimSpace(b.String())
}

// ---------------------------------------------------------------------------
// remove_old_goldens
// ---------------------------------------------------------------------------

type goldenSweepResult struct {
	Kind     string `json:"kind"`
	Planned  bool   `json:"planned_only"`
	Summary  string `json:"summary"`
	Keep     int    `json:"keep"`
	KeepFrom string `json:"keep_from"`
	// OtherProjects is how many versions on this machine belong to another
	// project and were not considered at all.
	OtherProjects int                 `json:"other_projects_left_alone"`
	Decisions     []goldenDecisionDoc `json:"versions"`
	Removed       int                 `json:"removed"`
	Refused       int                 `json:"refused"`
	ConfirmWith   []string            `json:"confirm_with,omitempty"`
	Note          string              `json:"note,omitempty"`
}

type goldenDecisionDoc struct {
	Version string `json:"version"`
	// Outcome is would-remove, keep, removed or refused.
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

func newRemoveOldGoldensTool(
	p *Project, read readGoldens, policy readGoldenPolicy, destroy destroyGolden,
) *Tool {
	return &Tool{
		Name:  "remove_old_goldens",
		Title: "Remove old masked copies",
		// Not read only and genuinely destructive: a removed golden is gone,
		// and a refresh to make another one reads production.
		ReadOnly:    false,
		Destructive: true,
		Description: "PERMANENTLY DELETES old masked copies of production, keeping the " +
			"newest. Each one is expensive to replace: making another means reading " +
			"production again. By default it only PLANS, listing exactly which versions it " +
			"would remove and why it would keep the rest, and changing nothing; that is the " +
			"call to make first. To carry the plan out, call again passing confirm_versions " +
			"naming every version the plan marked would-remove. If the set has changed in " +
			"between, the call is refused rather than deleting something the plan did not " +
			"show you. Two things can never be removed however this is called: a version an " +
			"environment is still branched from, which the provider refuses, and the newest " +
			"verified version, because a project with nothing left to branch cannot bring " +
			"an environment up at all. It only ever considers versions made for THIS " +
			"project, never another project's on the same machine, and it accepts no " +
			"wildcard.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id": projectIDSchema(),
				"keep": {
					Type: "integer", HasMin: true, Minimum: 1, HasMax: true, Maximum: 100,
					Description: "Optional. How many of the newest to keep, overriding the " +
						"project's own database.golden.retain for this call. Leave it out to " +
						"use the project's setting, which is what every machine and every " +
						"runner uses and is the answer that keeps them collecting alike.",
				},
				"confirm_versions": {
					Type: "array", MaxItems: 100,
					Description: "The versions to delete, named one by one, exactly as a " +
						"previous planning call listed them in confirm_with. Omit this and " +
						"nothing is deleted. Passing it is the whole of the confirmation: " +
						"there is no force argument and no pattern that stands for many.",
					Items: goldenVersionSchema("One version to delete."),
				},
			},
		},
		Handler: func(ctx context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			return removeOldGoldens(ctx, read, policy, destroy, args)
		},
	}
}

func removeOldGoldens(
	ctx context.Context, read readGoldens, policy readGoldenPolicy,
	destroy destroyGolden, args map[string]any,
) (any, *Fault) {
	confirm, fault := stringList(args, "confirm_versions")
	if fault != nil {
		return nil, fault
	}

	settings, err := policy()
	if err != nil {
		return nil, &Fault{
			Code:    FaultSafetyUnavailable,
			Detail:  "The project's golden retention setting could not be read, so nothing was planned.",
			wrapped: err,
		}
	}
	keep, keepFrom := settings.Retain, "database.golden.retain"
	if raw, ok := args["keep"]; ok {
		n, err := toInt(raw)
		if err != nil {
			return nil, fieldFault(FaultInvalidArgument, "keep",
				"This field must be a whole number.")
		}
		keep, keepFrom = n, "the keep argument on this call"
	}

	versions, mine, err := read(ctx)
	if err != nil {
		return nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The golden versions could not be listed, so nothing was planned and " +
				"nothing was deleted.",
			Retryable: true, wrapped: err,
		}
	}

	// Only this project's, which is a correctness rule rather than a
	// courtesy. A golden pool is shared, so sweeping every version on the
	// machine would destroy another repository's copies and enforce a
	// retention count across all of them.
	skipped := 0
	own := make([]golden.Version, 0, len(versions))
	for _, v := range versions {
		if v.Provenance == "" || v.Provenance != mine {
			skipped++
			continue
		}
		own = append(own, golden.Version{ID: v.ID, CreatedAt: v.CreatedAt, Verified: v.Verified})
	}
	decisions := golden.Sweep(own, keep)

	out := goldenSweepResult{
		Kind: "golden_sweep", Keep: keep, KeepFrom: keepFrom,
		OtherProjects: skipped, Decisions: []goldenDecisionDoc{},
	}
	var planned []string
	for _, d := range decisions {
		id, ok := safeIdentifier(d.Version.ID)
		doc := goldenDecisionDoc{Version: id, Outcome: "keep", Reason: safeProse(d.Reason, 200)}
		if d.Remove {
			doc.Outcome = "would-remove"
			if ok {
				planned = append(planned, id)
			}
		}
		out.Decisions = append(out.Decisions, doc)
	}
	sort.Strings(planned)

	if len(confirm) == 0 {
		out.Planned = true
		out.ConfirmWith = planned
		switch len(planned) {
		case 0:
			out.Summary = fmt.Sprintf(
				"Nothing would be removed. %d %s made for this project, keeping %d from %s, "+
					"and the newest verified one is never removed whatever the count says.",
				len(own), plural(len(own), "version is", "versions are"), keep, keepFrom)
		default:
			out.Summary = fmt.Sprintf(
				"%d of this project's %d versions would be PERMANENTLY DELETED, keeping %d "+
					"from %s. Nothing has been deleted. Each one costs a read of production "+
					"to replace. Call again with confirm_versions set to exactly the list in "+
					"confirm_with to carry this out.",
				len(planned), len(own), keep, keepFrom)
		}
		if skipped > 0 {
			out.Note = fmt.Sprintf(
				"%d versions on this machine were made for another project and were not "+
					"considered. They are not this project's to remove.", skipped)
		}
		return out, nil
	}

	if diff := describeSetDifference(planned, confirm, "version"); diff != "" {
		return nil, fieldFault(FaultInvalidArgument, "confirm_versions",
			"This does not match what would be deleted right now, so nothing was deleted. "+
				"%s Call again with no confirm_versions to see the current plan.", diff)
	}

	byID := make(map[string]int, len(out.Decisions))
	for i, d := range out.Decisions {
		byID[d.Version] = i
	}
	for _, id := range planned {
		i := byID[id]
		if err := destroy(ctx, id); err != nil {
			// Almost always something still branched from it, which is the
			// provider's refusal and the only place that knows. Reported
			// against the version so somebody can tear that environment down.
			out.Decisions[i].Outcome = "refused"
			out.Decisions[i].Detail = safeText(err.Error(), 300)
			out.Refused++
			continue
		}
		out.Decisions[i].Outcome = "removed"
		out.Removed++
	}
	out.Summary = fmt.Sprintf(
		"%d versions deleted for good and %d refused. A refusal is almost always an "+
			"environment still branched from that version; tear it down and call again.",
		out.Removed, out.Refused)
	return out, nil
}

// ---------------------------------------------------------------------------
// the adapters
// ---------------------------------------------------------------------------

// goldens lists the versions the provider holds, with this project's identity.
func (f *orchestratorFactory) goldens(
	ctx context.Context,
) ([]provider.GoldenVersion, string, error) {
	o, err := f.build()
	if err != nil {
		return nil, "", err
	}
	versions, err := o.Goldens(ctx)
	if err != nil {
		return nil, "", err
	}
	mine, err := o.GoldenIdentity()
	if err != nil {
		return nil, "", err
	}
	return versions, mine, nil
}

// publishedGoldens lists what this project's store holds.
func (f *orchestratorFactory) publishedGoldens(
	ctx context.Context,
) ([]golden.Object, string, error) {
	o, err := f.build()
	if err != nil {
		return nil, "", err
	}
	return o.PublishedGoldens(ctx)
}

// goldenPolicy reads the lifecycle settings out of the manifest.
func (f *orchestratorFactory) goldenPolicy() (env.GoldenPolicy, error) {
	o, err := f.build()
	if err != nil {
		return env.GoldenPolicy{}, err
	}
	return o.GoldenPolicy()
}

// destroyGolden removes one version, or reports the provider's refusal.
func (f *orchestratorFactory) destroyGolden(ctx context.Context, version string) error {
	o, err := f.build()
	if err != nil {
		return err
	}
	return o.DestroyGolden(ctx, version)
}

// prepare runs a refresh, a pull or a verify and reports them in one shape.
func (f *orchestratorFactory) prepare(
	ctx context.Context, action, version string,
) (goldenOutcome, error) {
	o, err := f.build()
	if err != nil {
		return goldenOutcome{}, err
	}
	out := goldenOutcome{Action: action}

	switch action {
	case "refresh":
		res, err := o.RefreshGolden(ctx)
		if res != nil {
			out.Version, out.Verified = res.Version, res.Verified
			out.Report, out.Rows, out.Tables = res.Report, res.Rows, res.Tables
			out.Published, out.Duration = res.Published, res.Duration
		}
		return out, err

	case "pull":
		res, err := o.PullGolden(ctx, version)
		if res != nil {
			out.Version, out.From = res.Version, res.From
			out.Verified, out.Report, out.Bytes = res.Verified, res.Report, res.Bytes
		}
		return out, err

	default:
		report, err := o.VerifyGolden(ctx, version)
		out.Version, out.Report = version, report
		// Clean() is the engine's own rule and it counts a column nobody could
		// read as a failure, which is why the verdict is taken from it rather
		// than from the finding count.
		out.Verified = err == nil && report.Clean()
		return out, err
	}
}
