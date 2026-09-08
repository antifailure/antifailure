package env

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"

	"github.com/antifailure/antifailure/engine/internal/db/pgcopy"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/golden"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/subset"
	"github.com/antifailure/antifailure/engine/internal/verify"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// A golden is a masked, verified copy of production that branches are made
// from. Refreshing one is the only place unmasked data is ever touched, and it
// happens on the machine that already has the credential for it.
//
// The order is the guarantee: copy, mask, verify, and only then publish. A
// golden that fails verification is not published, so it cannot be branched,
// so no environment can ever hold it. That is enforced by the provider
// contract rather than by remembering to check.

// MaskingKeyEnv is where a shared masking key is read from.
//
// Set it in CI so that every runner produces the same mapping and two goldens
// can be compared. Left unset, a key is generated once per machine and kept in
// the local state, which is right for a laptop and wrong for a fleet.
const MaskingKeyEnv = "AF_MASKING_KEY"

// maskingKeyMeta is where a generated key is kept.
const maskingKeyMeta = "masking.key"

// MaskingKey returns the key this project masks with.
func (o *Orchestrator) MaskingKey(ctx context.Context, s *session) (*masking.Key, error) {
	getenv := o.opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if raw := getenv(MaskingKeyEnv); raw != "" {
		key, err := masking.NewKey(secrets.New(raw))
		if err != nil {
			return nil, aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
		}
		o.opts.Redactor.Register(raw)
		return key, nil
	}

	stored, err := s.db.Meta(ctx, maskingKeyMeta)
	if err != nil {
		return nil, err
	}
	if stored == "" {
		// Generated once and kept, so that two runs on this machine produce
		// the same mapping. Regenerating per run would make every golden
		// incomparable with the last one, which is most of what a masked copy
		// is for.
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return nil, aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
		}
		stored = base64.StdEncoding.EncodeToString(buf)
		if err := s.db.SetMeta(ctx, maskingKeyMeta, stored); err != nil {
			return nil, err
		}
	}
	o.opts.Redactor.Register(stored)
	key, err := masking.NewKey(secrets.New(stored))
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
	}
	return key, nil
}

// rules returns the compiled masking rules for this project.
//
// The manifest is read defensively here and nowhere else in this file, because
// this is the one place reached without one. Every other caller of a manifest
// field in this file runs inside a command that loaded a project, so a nil
// there would be a bug worth crashing on. rules is now also called from
// verifyDatabase, by way of unruledColumns, and verification is the step that
// runs against a database somebody else masked: a branch, a published golden,
// a pull. A missing manifest means the same thing a missing rules file already
// meant, which is the built in rule set, so it is answered rather than
// refused. Before this guard existed, verifyDatabase on an orchestrator with
// no manifest was a nil dereference.
func (o *Orchestrator) rules() (*masking.RuleSet, string, error) {
	var declared []masking.Rule
	if o.opts.Manifest != nil && o.opts.Manifest.Database != nil &&
		o.opts.Manifest.Database.MaskingRules != "" {
		path := filepath.Join(o.opts.Root,
			filepath.FromSlash(strings.TrimPrefix(o.opts.Manifest.Database.MaskingRules, "./")))
		body, err := os.ReadFile(path)
		switch {
		case os.IsNotExist(err):
			// The manifest names a default path whether or not anybody wrote
			// the file. A missing one means the built in rules, which is the
			// common case and not an error; a file that exists and does not
			// parse is.
			body = nil
		case err != nil:
			return nil, "", aferrors.Wrap(err, aferrors.AFMSK010,
				"detail", "reading the masking rules at "+o.opts.Manifest.Database.MaskingRules)
		}
		var file struct {
			Rules []masking.Rule `yaml:"rules" json:"rules"`
		}
		if err := yaml.Unmarshal(body, &file); body != nil && err != nil {
			return nil, "", aferrors.Wrap(err, aferrors.AFMSK010,
				"detail", "the masking rules at "+o.opts.Manifest.Database.MaskingRules+" are not valid: "+err.Error())
		}
		declared = file.Rules
	}
	rs, err := masking.NewRuleSet(declared)
	if err != nil {
		return nil, "", aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
	}
	return rs, rulesHash(declared), nil
}

// rulesHash identifies a rule set, so a golden records what it was masked with
// and a changed rule set shows up as a different golden rather than as the
// same one behaving differently.
func rulesHash(rules []masking.Rule) string {
	sorted := append([]masking.Rule(nil), rules...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Table+sorted[i].Column+sorted[i].Transform <
			sorted[j].Table+sorted[j].Column+sorted[j].Transform
	})
	body, err := json.Marshal(sorted)
	if err != nil {
		return "unknown"
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:8])
}

// GoldenResult is what a refresh produced.
type GoldenResult struct {
	Version  string
	Verified bool
	Rows     int64
	Tables   int
	Duration time.Duration
	Report   verify.Report
	// Attestation is the signed statement, as JSON.
	Attestation string
	// Subset is what the subsetter did, when the manifest asked for one. Nil
	// when it did not, which is how a caller tells "took a slice of nothing"
	// from "took the whole thing".
	Subset *subset.Stats
	// SubsetPlan is the plan that was run, for the explanation.
	SubsetPlan *subset.Plan
	// Published names the store the golden was copied to, empty when this
	// project publishes nowhere.
	Published string
	// Datastores is what each declared datastore's refresh did, in manifest
	// order. Empty when the manifest declares none, which is every project
	// whose twin is one Postgres.
	Datastores []DatastoreGolden
	// PublishError is why the copy did not happen, when a store was
	// configured. It is reported rather than returned: the golden exists and
	// can be branched here, and failing the whole refresh would throw away the
	// part that read production.
	PublishError string
}

// subsetConfig turns the manifest's subset block into the subsetter's config,
// and reports whether one was asked for at all.
func (o *Orchestrator) subsetConfig() (subset.Config, bool) {
	db := o.opts.Manifest.Database
	if db == nil || db.Subset == nil || !db.Subset.Enabled {
		return subset.Config{}, false
	}
	cfg := subset.Config{
		SeedTable: db.Subset.SeedTable,
		SeedWhere: db.Subset.SeedWhere,
		MaxRows:   db.Subset.MaxRows,
	}
	if db.Subset.FollowDependents != nil {
		cfg.FollowDependents = *db.Subset.FollowDependents
	}
	for _, r := range db.Subset.VirtualRelationships {
		// The manifest writes a relationship as schema.table.column on each
		// side, which is the same shape the masking rules use and the only one
		// the validator accepts. Composite virtual relationships are not
		// expressible in that form; the schema's own composite keys are read
		// from the catalog and are unaffected.
		from, fromCol, okFrom := splitRef(r.From)
		to, toCol, okTo := splitRef(r.To)
		if !okFrom || !okTo {
			continue
		}
		cfg.Virtual = append(cfg.Virtual, subset.ForeignKey{
			Name:        "virtual",
			From:        from,
			FromColumns: []string{fromCol},
			To:          to,
			ToColumns:   []string{toCol},
		})
	}
	return cfg, true
}

// splitRef reads schema.table.column, and table.column as public.table.column,
// which is what somebody writes when every table they have is in public.
func splitRef(ref string) (table, column string, ok bool) {
	parts := strings.Split(ref, ".")
	switch len(parts) {
	case 3:
		return parts[0] + "." + parts[1], parts[2], true
	case 2:
		return "public." + parts[0], parts[1], true
	default:
		return "", "", false
	}
}

// loadSubset is what a provider calls instead of copying the whole source.
//
// It is a method on the orchestrator rather than a closure built inline
// because it is the one place the plan and the execution meet, and because a
// caller reading RefreshGolden should see that the manifest's subset block
// reaches a provider rather than having to work out whether it does.
func (o *Orchestrator) loadSubset(
	ctx context.Context, cfg subset.Config, sourceURL, candidateURL secrets.Value,
) (*subset.Plan, *subset.Stats, error) {
	// The structure first, and none of the rows. The rows are what a subset
	// exists not to copy, so copying them and then narrowing would be doing
	// the expensive part anyway.
	o.progress("copying the schema into the candidate")
	if err := pgcopy.CopySchema(ctx, sourceURL, candidateURL); err != nil {
		return nil, nil, aferrors.Wrap(err, aferrors.AFDB002, "host", "the source database")
	}

	src, err := pgx.Connect(ctx, sourceURL.Reveal())
	if err != nil {
		return nil, nil, aferrors.Wrap(err, aferrors.AFDB002, "host", "the source database")
	}
	cat, err := subset.ReadCatalog(ctx, src)
	closeErr := src.Close(context.WithoutCancel(ctx))
	if err != nil {
		return nil, nil, aferrors.Wrap(err, aferrors.AFDB011, "detail", err.Error())
	}
	_ = closeErr

	plan, err := subset.Build(cat, cfg)
	if err != nil {
		return nil, nil, aferrors.Coded(aferrors.AFDB011, "detail", err.Error())
	}
	for _, w := range plan.Warnings {
		o.progress("subset: " + w)
	}
	if len(plan.Unreachable) > 0 {
		// Reported, not refused. A table nothing reaches arrives empty, and a
		// table that arrives empty and should not have is a bug somebody finds
		// three days later in a test that returns nothing.
		o.progress(fmt.Sprintf(
			"subset: nothing connects %d tables to the seed, so they arrive empty: %s",
			len(plan.Unreachable), strings.Join(plan.Unreachable, ", ")))
	}

	stats, err := subset.Execute(ctx, subset.Options{
		SourceURL: sourceURL.Reveal(),
		TargetURL: candidateURL.Reveal(),
		Plan:      plan,
		Progress:  func(line string) { o.progress("subset: " + line) },
		Now:       o.opts.Clock.Now,
	})
	if err != nil {
		return &plan, stats, aferrors.Coded(aferrors.AFDB011, "detail", err.Error())
	}
	return &plan, stats, nil
}

// RefreshGolden copies, masks, verifies, and publishes a golden.
func (o *Orchestrator) RefreshGolden(ctx context.Context) (*GoldenResult, error) {
	s, err := o.open(ctx, "af golden refresh")
	if err != nil {
		return nil, err
	}
	defer s.close()
	return o.refreshWithin(ctx, s)
}

// refreshWithin does the refresh on a session that is already open.
//
// Split out because `af up` refreshes a stale golden before branching it, and
// it is already holding the session. Opening a second would take the same lock
// and wait on the one this call is holding, which is a deadlock that looks
// exactly like another process being in the way.
//
// That lock is also what makes two refreshes not overlap: a second process
// that tries while one is running does not start a competing copy of
// production, it waits or is refused.
func (o *Orchestrator) refreshWithin(ctx context.Context, s *session) (*GoldenResult, error) {
	key, err := o.MaskingKey(ctx, s)
	if err != nil {
		return nil, err
	}
	rules, hash, err := o.rules()
	if err != nil {
		return nil, err
	}
	prov, err := o.provenanceOf()
	if err != nil {
		return nil, err
	}

	result := &GoldenResult{}
	started := o.opts.Clock.Now()
	source, err := o.sourceURL(ctx)
	if err != nil {
		return result, err
	}

	// A manifest that names production and a shell that does not hold it is a
	// refusal rather than an empty golden.
	//
	// This is the same defect `af up` already refuses with AF-DB-012, arriving
	// through the other door. A refresh with the variable unset copied nothing,
	// masked nothing, verified nothing, published a golden whose provenance is
	// indistinguishable from a real one, exited 0 and printed "Bring an
	// environment up from it with: af up". The next `af up` then found a golden
	// for this project, branched an empty database, ran the migrations against
	// it, and produced an environment that looks correct and holds none of
	// production's shape or volume. It is the one failure a preview environment
	// exists to prevent, and the run that caused it reported success.
	//
	// It is not hypothetical and it is not a typo. A pull request from a fork
	// gets no secrets, so the variable is empty in exactly the runs nobody
	// watches.
	if o.opts.Manifest.Database != nil &&
		o.opts.Manifest.Database.SourceURLEnv != "" && source.IsZero() {
		return result, aferrors.Coded(aferrors.AFDB016,
			"variable", o.opts.Manifest.Database.SourceURLEnv)
	}

	spec := provider.GoldenSpec{
		Version:    databaseVersion(o.opts.Manifest),
		RulesHash:  hash,
		Provenance: prov.digest(),
		SourceURL:  source,
		Mask: func(ctx context.Context, url secrets.Value) error {
			rows, tables, maskErr := o.maskDatabase(ctx, s, url, key, rules, hash)
			result.Rows, result.Tables = rows, tables
			return maskErr
		},
		Verify: func(ctx context.Context, url secrets.Value) (string, error) {
			report, att, verifyErr := o.verifyDatabase(ctx, s, url, hash, prov.digest())
			result.Report = report
			result.Attestation = att
			if verifyErr != nil {
				return "", verifyErr
			}
			return att, nil
		},
	}

	if cfg, wanted := o.subsetConfig(); wanted {
		switch {
		case source.IsZero():
			// Nothing to take a slice of. Refusing would make `af up` on a
			// project that has not connected production yet impossible, and
			// the golden it gets is empty anyway, so the block is reported as
			// having no effect rather than silently having none.
			o.progress("the manifest asks for a subset and no source database is configured, " +
				"so there is nothing to take a slice of; set database.source_url_env")
		case !s.dbProv.Capabilities().Subsetting:
			// The manifest asked for something this provider cannot do.
			// Accepting it and copying everything is exactly the failure this
			// whole change exists to remove: a key that reads as
			// configuration and behaves as decoration.
			return result, aferrors.Coded(aferrors.AFDB011,
				"detail", fmt.Sprintf(
					"the provider %q builds a golden by branching the source rather than by "+
						"filling an empty database, so there is nowhere to load a slice into. "+
						"Remove database.subset, or use a provider that can: %s",
					s.dbProv.Name(), "docker"))
		default:
			spec.Load = func(ctx context.Context, src, candidate secrets.Value) error {
				plan, stats, loadErr := o.loadSubset(ctx, cfg, src, candidate)
				result.SubsetPlan, result.Subset = plan, stats
				return loadErr
			}
		}
	}

	store, err := o.goldenStore()
	if err != nil {
		return result, err
	}

	gv, err := s.dbProv.RefreshGolden(ctx, spec)
	result.Version, result.Verified = gv.ID, gv.Verified
	result.Duration = o.opts.Clock.Since(started)
	if err != nil {
		return result, err
	}
	if err := o.recordRefresh(ctx, s); err != nil {
		return result, err
	}
	// Said out loud, because a refresh is the only way a golden comes into
	// existence and this path emitted nothing. `af golden refresh` produced a
	// masked, verified, attested version and the control plane never heard of
	// it, so the row the compliance pack reads was written by nobody. The
	// attestation is taken from the result when the provider did not put it on
	// the version, because Verify's return value is where it is produced and
	// only some providers copy it back.
	if gv.Attestation == "" {
		gv.Attestation = result.Attestation
	}
	o.event(s, events.GoldenReady, "golden "+gv.ID+" is ready", o.goldenFields(gv)...)
	// Published after the version exists locally, never instead of it. A
	// publish that fails leaves a golden this machine can still branch, and
	// the failure is reported rather than turned into a failed refresh: the
	// expensive part, reading production, already succeeded.
	if store != nil {
		if pubErr := o.publishGolden(ctx, s, store, gv, result.Attestation); pubErr != nil {
			result.PublishError = pubErr.Error()
			o.progress("the golden was made and could not be published: " + pubErr.Error())
		} else {
			result.Published = store.Name()
		}
	}
	// The rest of the twin. A project whose events are the point would
	// otherwise have a command that refreshes its metadata and no command at
	// all for its events, and `af up` would refuse to branch a store nothing
	// could make a golden of.
	if err := o.refreshDatastores(ctx, s, result); err != nil {
		result.Duration = o.opts.Clock.Since(started)
		return result, err
	}
	result.Duration = o.opts.Clock.Since(started)
	return result, nil
}

// lastRefreshMeta is when a refresh last finished, so that a schedule can be
// honoured on a laptop with no daemon to run it: the next command that would
// use a golden asks whether one was due since then.
const lastRefreshMeta = "golden.last_refresh"

// recordRefresh notes that a refresh finished.
func (o *Orchestrator) recordRefresh(ctx context.Context, s *session) error {
	return s.db.SetMeta(ctx, lastRefreshMeta,
		o.opts.Clock.Now().UTC().Format(time.RFC3339))
}

// lastRefresh reads when one last did, and the zero time when none has.
func (o *Orchestrator) lastRefresh(ctx context.Context, s *session) time.Time {
	raw, err := s.db.Meta(ctx, lastRefreshMeta)
	if err != nil || raw == "" {
		return time.Time{}
	}
	when, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return when
}

// GoldenPolicy is the manifest's lifecycle settings, read once and validated.
//
// The manifest validator already refuses a schedule that is not a cron
// expression and a maximum age that is not a duration, so a failure to parse
// here would mean the two disagreed. It is still returned rather than ignored,
// because "the validator would have caught it" is how a second parser drifts
// from the first.
type GoldenPolicy struct {
	Schedule golden.Schedule
	MaxAge   time.Duration
	Retain   int
}

// GoldenPolicy reads the lifecycle settings out of the manifest.
func (o *Orchestrator) GoldenPolicy() (GoldenPolicy, error) {
	var p GoldenPolicy
	db := o.opts.Manifest.Database
	if db == nil || db.Golden == nil {
		return p, nil
	}
	if db.Golden.Schedule != "" {
		s, err := golden.ParseSchedule(db.Golden.Schedule)
		if err != nil {
			return p, aferrors.Coded(aferrors.AFDB011, "detail", err.Error())
		}
		p.Schedule = s
	}
	if db.Golden.MaxAge != "" {
		d, err := manifest.ParseDuration(db.Golden.MaxAge)
		if err != nil {
			return p, aferrors.Coded(aferrors.AFDB011, "detail",
				fmt.Sprintf("the golden's max_age %q is not a duration: %v", db.Golden.MaxAge, err))
		}
		p.MaxAge = d
	}
	p.Retain = db.Golden.Retain
	return p, nil
}

// RefreshDue reports whether the schedule or the maximum age says a refresh
// should happen before this environment comes up, and why.
//
// This is what a cron expression means on a laptop. There is no daemon to fire
// it, so the next command that would use a golden asks whether one was due
// since the last refresh, which gives a project the refresh it asked for
// without a background process nobody started.
func (o *Orchestrator) RefreshDue(
	ctx context.Context, s *session, versions []provider.GoldenVersion,
) (string, error) {
	policy, err := o.GoldenPolicy()
	if err != nil {
		return "", err
	}
	source, err := o.sourceURL(ctx)
	if err != nil {
		return "", err
	}
	if source.IsZero() {
		// No production to refresh from. The empty golden `af up` makes is not
		// stale in any meaningful sense, and refreshing it on a timer would be
		// a message about nothing.
		return "", nil
	}

	var known []golden.Version
	for _, v := range versions {
		known = append(known, golden.Version{ID: v.ID, CreatedAt: v.CreatedAt, Verified: v.Verified})
	}
	newest, haveOne := golden.Newest(known)

	if policy.MaxAge > 0 && haveOne &&
		golden.Stale(newest.CreatedAt, policy.MaxAge, o.opts.Clock.Now()) {
		return fmt.Sprintf("the newest golden is %s old and max_age is %s",
			o.opts.Clock.Now().Sub(newest.CreatedAt).Round(time.Minute), policy.MaxAge), nil
	}
	if !policy.Schedule.Zero() {
		last := o.lastRefresh(ctx, s)
		if last.IsZero() && haveOne {
			// A golden exists from before this was recorded. Its creation time
			// is the honest stand-in, and using the zero time instead would
			// refresh once, immediately, for every existing project.
			last = newest.CreatedAt
		}
		if policy.Schedule.Due(last, o.opts.Clock.Now()) {
			return "the schedule " + policy.Schedule.String() + " came due", nil
		}
	}
	return "", nil
}

// sourceURL is the production database to copy, when one is configured.
//
// Looked up through the same chain as every other variable the manifest names,
// which is what the secrets guide has always said happens. That page opens with
// source_url_env as its example and then lists four places a value is looked
// up: this shell, .env, the encrypted local store, and the system keyring. This
// function read the first of those and no others, so a project that put the
// production URL in .env, next to the STRIPE_SECRET_KEY that `af up` finds
// there, was told the variable held nothing.
//
// The chain also carries the two places a production credential actually
// belongs. `af secret set PRODUCTION_DATABASE_URL` writes to an encrypted store
// and the keyring holds it across repositories, and neither was reachable for
// this one value.
func (o *Orchestrator) sourceURL(ctx context.Context) (secrets.Value, error) {
	if o.opts.Manifest.Database == nil || o.opts.Manifest.Database.SourceURLEnv == "" {
		return secrets.Value{}, nil
	}
	value, _, found, err := o.secretChain().Lookup(ctx, o.opts.Manifest.Database.SourceURLEnv)
	if err != nil {
		// Reported rather than treated as absent. A keyring that will not open
		// is not a variable that is unset, and calling it one would send the
		// reader to export something they had already stored.
		return secrets.Value{}, aferrors.Wrap(err, aferrors.AFSEC005,
			"name", o.opts.Manifest.Database.SourceURLEnv, "detail", err.Error())
	}
	if !found || strings.TrimSpace(value.Reveal()) == "" {
		return secrets.Value{}, nil
	}
	// Registered before it is used, so that it is redacted everywhere rather
	// than everywhere somebody remembered.
	o.opts.Redactor.Register(value.Reveal())
	return value, nil
}

// checkMasking asks the registered hooks whether this plan may run.
//
// The community build registers nothing, so this returns nil after one pass
// over an empty slice, and the community suite runs byte for byte as it did
// before the call existed. The socket is here rather than in the enterprise
// edition for the same reason the policy socket is: a hook that only exists in
// a build nobody runs is a hook nobody has tested.
func (o *Orchestrator) checkMasking(
	ctx context.Context, tables []masking.Table, plan masking.Plan,
) error {
	registry := o.opts.Extensions
	if registry == nil {
		registry = extension.Default
	}
	if registry.Empty() {
		return nil
	}
	return registry.CheckMasking(ctx, o.maskingRequest(tables, plan))
}

// maskingRequest describes a plan in the two lists a hook needs.
//
// Split out and pure so that the mapping is testable without a database, and
// because getting it wrong is silent: a request whose MaskedColumns were built
// from the rules rather than from the plan would name columns no executor is
// going to touch, and a hook reading it would approve a database that still
// holds them.
//
// Every column carries its schema. The alternative, a bare table.column, is
// ambiguous the moment two schemas hold a table of the same name, and a policy
// that cannot tell public.users.email from staging.users.email is a policy
// about whichever one the map iterated first.
func (o *Orchestrator) maskingRequest(
	tables []masking.Table, plan masking.Plan,
) extension.MaskingRequest {
	req := extension.MaskingRequest{
		Branch:    o.opts.Branch,
		EnvID:     o.envID,
		RulesHash: plan.RulesHash,
	}
	if o.opts.Manifest != nil {
		req.Repository = o.opts.Manifest.Name
	}
	// The plan is what will run, so this is the list of columns that are about
	// to be rewritten and not a wider one. A table the plan skipped
	// contributes nothing here even though its columns were assigned, which is
	// correct: a skipped table is a table whose data survives.
	for _, tp := range plan.Tables {
		if tp.Skipped != "" {
			continue
		}
		for _, col := range tp.Columns {
			req.MaskedColumns = append(req.MaskedColumns,
				tp.Table.String()+"."+col.Column.Name)
		}
	}
	sort.Strings(req.MaskedColumns)

	// The whole catalogue, from the catalogue rather than from the plan. The
	// plan holds only the tables it will rewrite, so a database whose email
	// column matched no rule contributes that column to neither list if this
	// is read off the plan, and a hook cannot then tell an absent column from
	// an unmasked one.
	for _, t := range tables {
		for _, col := range t.Columns {
			req.CatalogColumns = append(req.CatalogColumns, t.String()+"."+col.Name)
		}
	}
	sort.Strings(req.CatalogColumns)
	return req
}

// maskDatabase applies the rules to a database.
//
// It takes the session because masking is the longest and least visible part of
// a refresh, and because the dashboard's database pane is drawn entirely from
// the mask events below. The pane existed before any of them were emitted: the
// catalog documented mask.planned, mask.progress, mask.applied, mask.verifying,
// mask.verified and mask.finding, the reference page generated from that
// catalog told a customer they could filter on all six, and this function
// reported its work with o.progress alone, which reaches a terminal and no sink.
func (o *Orchestrator) maskDatabase(
	ctx context.Context, s *session, url secrets.Value,
	key *masking.Key, rules *masking.RuleSet, hash string,
) (int64, int, error) {
	conn, err := pgx.Connect(ctx, url.Reveal())
	if err != nil {
		return 0, 0, aferrors.Wrap(err, aferrors.AFDB004, "env", "the golden candidate")
	}
	defer func() { _ = conn.Close(context.Background()) }()

	tables, err := masking.ReadCatalog(ctx, conn)
	if err != nil {
		return 0, 0, aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
	}
	plan := masking.BuildPlan(tables, rules.Assign(tables), hash)
	if !plan.Runnable() {
		return 0, 0, aferrors.Coded(aferrors.AFMSK010,
			"detail", masking.DescribeProblems(plan.Problems))
	}
	columns := 0
	for _, t := range plan.Tables {
		columns += len(t.Columns)
	}
	o.event(s, events.MaskPlanned,
		fmt.Sprintf("masking %d columns across %d tables", columns, len(plan.Tables)),
		events.F("tables", len(plan.Tables)), events.F("columns", columns),
		events.F("unclassified", len(plan.Unclassified)), events.F("percent", 0))

	if len(plan.Unclassified) > 0 {
		// Reported, not refused. Refusing would make the first run of a real
		// schema impossible; saying nothing would let the columns ship.
		o.progress(fmt.Sprintf(
			"%d columns matched no rule and may hold something: %s",
			len(plan.Unclassified), masking.DescribeColumns(plan.Unclassified, 6)))
	}

	// Asked here and nowhere else, because here is the only place the answer
	// is a fact. The policy hook runs before anything is created and is given
	// a manifest, which names no columns; this is given a catalogue that was
	// read from the database eleven lines up and a plan assigned against it.
	//
	// Before the executor rather than after it, so a refusal costs the rows
	// nothing: the candidate is not masked, not verified and never published,
	// and provider.Branch refuses a version that was not published. A hook
	// that answered after Apply would be reviewing a database it could no
	// longer stop anybody from branching.
	if err := o.checkMasking(ctx, tables, plan); err != nil {
		return 0, 0, err
	}
	if copied := plan.CopiedUnchanged(); len(copied) > 0 {
		// The half of that list the default could not empty. These hold
		// exactly what production holds, and the sentence above did not
		// distinguish them from the ones that were emptied.
		o.progress(fmt.Sprintf(
			"%d columns copied unchanged with no rule: %s",
			len(copied), masking.DescribeColumns(copied, 6)))
	}

	// Counted here rather than read off the executor because the callback is
	// what the percentage has to be derived from, and it reports one table at a
	// time. Apply runs the tables in order on one goroutine, so a plain counter
	// is enough and a mutex would only hide that if it ever stopped being true.
	finished := 0
	exec, err := masking.NewExecutor(masking.ExecutorOptions{
		Key: key, Clock: o.opts.Clock,
		Progress: func(p masking.Progress) {
			if !p.Finished {
				return
			}
			finished++
			percent := 100
			if len(plan.Tables) > 0 {
				percent = finished * 100 / len(plan.Tables)
			}
			o.event(s, events.MaskProgress,
				fmt.Sprintf("masked %s (%d rows)", p.Table, p.Rows),
				events.F("table", p.Table), events.F("rows", p.Rows),
				events.F("percent", percent))
			o.progress(fmt.Sprintf("masked %s (%d rows)", p.Table, p.Rows))
		},
	})
	if err != nil {
		return 0, 0, aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
	}
	res, err := exec.Apply(ctx, conn, plan)
	if err != nil {
		return res.Rows, res.Tables, aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
	}
	o.event(s, events.MaskApplied,
		fmt.Sprintf("masked %d rows across %d tables", res.Rows, res.Tables),
		events.F("rows", res.Rows), events.F("tables", res.Tables),
		events.F("percent", 100))

	// Analysed again, because masking just rewrote most of the columns the copy
	// had statistics for. A golden published without this is one every branch
	// inherits a blind planner from, and `af insights` then compares two
	// sequential scans and reports no regression however bad the change was.
	if err := pgcopy.Analyze(ctx, url); err != nil {
		// Not fatal. A golden with stale statistics is worse than one with
		// fresh ones and far better than no golden, and the failure is worth
		// saying out loud rather than stopping a refresh for.
		o.progress("could not refresh planner statistics: " + err.Error())
	}
	return res.Rows, res.Tables, nil
}

// verifyDatabase reads a database back and signs what it found.
//
// The provenance is signed alongside the report rather than only stamped on
// the provider's own copy, because a published golden is read by a machine
// that has only the store: an annotation on a Docker image never leaves the
// daemon that holds it, and a claim about whose data a dump is has to be one
// the puller can check.
func (o *Orchestrator) verifyDatabase(
	ctx context.Context, s *session, url secrets.Value, hash, prov string,
) (verify.Report, string, error) {
	conn, err := pgx.Connect(ctx, url.Reveal())
	if err != nil {
		return verify.Report{}, "", aferrors.Wrap(err, aferrors.AFDB004, "env", "the golden candidate")
	}
	defer func() { _ = conn.Close(context.Background()) }()

	o.event(s, events.MaskVerifying, "reading the masked copy back",
		events.F("phase", "verifying"))

	unruled, err := o.unruledColumns(ctx, conn)
	if err != nil {
		return verify.Report{}, "", err
	}
	report, err := verify.Scan(ctx, conn, verify.Options{
		Now:      func() time.Time { return o.opts.Clock.Now() },
		Progress: func(line string) { o.progress("verification: " + line) },
		Unruled:  unruled,
	})
	if err != nil {
		return report, "", aferrors.Wrap(err, aferrors.AFMSK002,
			"detector", "scan", "table", "any", "column", "any")
	}

	_, priv, err := verify.GenerateKey()
	if err != nil {
		return report, "", err
	}
	att, err := verify.Sign(report, "", hash, prov, priv)
	if err != nil {
		return report, "", err
	}
	body, err := json.Marshal(att)
	if err != nil {
		return report, "", err
	}

	if !report.Clean() {
		// The refusal that the whole product rests on. A golden that fails
		// here is never published, so it can never be branched, so no
		// environment can hold it.
		//
		// One event per finding, at error level, because the refusal below
		// names only the first and the pane counts them. Somebody looking at a
		// failed refresh wants to know whether one column leaked or forty.
		for _, f := range report.Findings {
			o.eventErr(s, events.MaskFinding,
				fmt.Sprintf("%s found %s in %s.%s.%s",
					f.Detector, f.Column, f.Schema, f.Table, f.Column),
				events.F("detector", f.Detector),
				events.F("table", f.Schema+"."+f.Table), events.F("column", f.Column))
		}
		// A skip with no findings reaches here now, and it did not before, so
		// this index was safe and is not. Clean() used to ignore Skipped, which
		// meant every path into this branch had at least one finding;
		// report.Findings[0] on a report whose only problem is an unreadable
		// column is an index out of range, and the fix for a silent pass would
		// have been a panic. Findings first, because a column the scan READ and
		// disliked is the more specific answer.
		if len(report.Findings) > 0 {
			first := report.Findings[0]
			return report, string(body), aferrors.Coded(aferrors.AFMSK002,
				"detector", first.Detector, "table", first.Schema+"."+first.Table,
				"column", first.Column)
		}
		for _, skipped := range report.Skipped {
			o.eventErr(s, events.MaskFinding,
				"verification could not read "+skipped,
				events.F("skipped", skipped))
		}
		// Safe because !Clean() with no findings means Skipped is non empty,
		// which is Clean()'s definition and not an assumption about the caller.
		table, column, detail := describeSkip(report.Skipped[0])
		return report, string(body), aferrors.Coded(aferrors.AFMSK011,
			"table", table, "column", column, "detail", detail)
	}
	o.event(s, events.MaskVerified,
		fmt.Sprintf("verified %d columns across %d tables", report.Columns, report.Tables),
		events.F("tables", report.Tables), events.F("columns", report.Columns),
		events.F("rows_sampled", report.RowsSampled), events.F("verified", true),
		events.F("columns_unread", len(report.Unread)),
		events.F("columns_unruled", len(report.Unruled)))
	if len(report.Unruled) > 0 {
		o.progress(fmt.Sprintf("%d columns copied unchanged with no rule", len(report.Unruled)))
	}
	return report, string(body), nil
}

// unruledColumns names the columns masking copied unchanged because no rule
// covered them, for the verification scan to carry.
//
// Computed here, beside the scan, rather than handed down from the masking
// step, because three of the four paths that verify a database never masked
// it: af mask verify reads a branch somebody already masked, af golden verify
// reads a published golden, and a pull reads what another machine published.
// The rules file on this machine is the one thing all four have, and a plan
// built from it against the database being scanned is exactly the list af
// mask plan would print at the bottom.
func (o *Orchestrator) unruledColumns(ctx context.Context, conn *pgx.Conn) ([]string, error) {
	rules, hash, err := o.rules()
	if err != nil {
		return nil, err
	}
	tables, err := masking.ReadCatalog(ctx, conn)
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
	}
	return masking.BuildPlan(tables, rules.Assign(tables), hash).CopiedUnchangedNames(), nil
}

// describeSkip splits one of verify.Scan's skipped lines into the fields the
// error catalogue names.
//
// The line is "schema.table.column: reason", so the column is what follows the
// LAST dot rather than the first: a schema qualified name has two, and cutting
// on the first turns public.customers.notes into table "public" and column
// "customers.notes". Written as its own function because that off by one is
// invisible in an error nobody has triggered yet.
func describeSkip(line string) (table, column, detail string) {
	where, detail, _ := strings.Cut(line, ": ")
	table, column = where, ""
	if i := strings.LastIndex(where, "."); i >= 0 {
		table, column = where[:i], where[i+1:]
	}
	return table, column, detail
}

// PlanResult is what af mask plan produced.
type PlanResult struct {
	Plan      masking.Plan
	RulesHash string
	// Source names the database the schema was read from, for the caller to
	// print. A plan is about a schema, and which schema is not obvious when
	// there are two it could have been.
	Source string
}

// MaskPlan reads a database and reports what masking would do to it.
//
// The environment's own branch when there is one, so somebody iterating on
// rules sees the effect on the schema they actually have rather than on a
// description of it.
//
// The configured source when there is not, and that fallback is the whole
// point of this comment. `af mask plan` in a fresh checkout is the first thing
// anybody does with masking, before `af up` has ever run, and it used to fail
// there: no branch exists, so the connection was refused, and what it said was
// that a golden with no name no longer existed. Three things wrong at once.
// The schema a plan is about is the source's schema, which the golden is a
// copy of, so reading it from the source is not a lesser answer. It is the
// same answer one step earlier, which is where somebody fixing a masking rule
// wants it.
//
// With neither, the refusal says so plainly rather than describing a database
// that was never configured.
func (o *Orchestrator) MaskPlan(ctx context.Context) (*PlanResult, error) {
	rules, hash, err := o.rules()
	if err != nil {
		return nil, err
	}

	conn, closeConn, source, err := o.connectForPlan(ctx)
	if err != nil {
		return nil, err
	}
	defer closeConn()

	tables, err := masking.ReadCatalog(ctx, conn)
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
	}
	return &PlanResult{
		Plan:      masking.BuildPlan(tables, rules.Assign(tables), hash),
		RulesHash: hash,
		Source:    source,
	}, nil
}

// DraftResult is what MaskDraft read, for the command that writes it down.
type DraftResult struct {
	Tables []masking.Table
	// Assignments are what the built in rules alone decided, column by
	// column, which is what a fresh masking.yaml is written from.
	Assignments []masking.Assignment
	// Source names the database the schema was read from.
	Source string
}

// MaskDraft reads a schema and classifies it with the built in rules alone.
//
// The built in rules alone, deliberately, and not the rule set the manifest
// names. This is what `af mask init` writes masking.yaml from, and a draft
// that read the file it is about to replace would be a draft of the wrong
// thing: a rule somebody wrote and then asked to have regenerated would come
// back restated as though the defaults had chosen it.
func (o *Orchestrator) MaskDraft(ctx context.Context) (*DraftResult, error) {
	rules, err := masking.NewRuleSet(nil)
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
	}
	conn, closeConn, source, err := o.connectForPlan(ctx)
	if err != nil {
		return nil, err
	}
	defer closeConn()

	tables, err := masking.ReadCatalog(ctx, conn)
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
	}
	return &DraftResult{Tables: tables, Assignments: rules.Assign(tables), Source: source}, nil
}

// MaskingRulesPath is where the manifest says the rules live, resolved
// against the repository root.
func (o *Orchestrator) MaskingRulesPath() string {
	rules := manifest.DefaultMaskingRules
	if o.opts.Manifest.Database != nil && o.opts.Manifest.Database.MaskingRules != "" {
		rules = o.opts.Manifest.Database.MaskingRules
	}
	return filepath.Join(o.opts.Root, filepath.FromSlash(strings.TrimPrefix(rules, "./")))
}

// connectForPlan opens the branch if there is one and the source if there is
// not, and says which it opened.
//
// The branch is tried first and its failure is not reported when a source is
// available, because "there is no environment yet" is the normal state for
// this command rather than an error worth interrupting somebody with.
func (o *Orchestrator) connectForPlan(
	ctx context.Context,
) (conn *pgx.Conn, done func(), source string, err error) {
	conn, done, branchErr := o.connectBranch(ctx)
	if branchErr == nil {
		return conn, done, "this environment's branch", nil
	}

	url, urlErr := o.sourceURL(ctx)
	if urlErr != nil {
		return nil, nil, "", urlErr
	}
	if url.IsZero() {
		// Nothing to fall back to, so the branch's own failure is the honest
		// answer and it is passed through rather than replaced.
		return nil, nil, "", branchErr
	}

	conn, err = pgx.Connect(ctx, url.Reveal())
	if err != nil {
		// The variable's name rather than the value. A connection string is a
		// credential, and the reader needs to know which setting to look at
		// rather than what is currently in it.
		return nil, nil, "", aferrors.Wrap(err, aferrors.AFDB002,
			"host", "the source named by "+o.opts.Manifest.Database.SourceURLEnv)
	}
	return conn, func() { _ = conn.Close(context.Background()) },
		"the source named by " + o.opts.Manifest.Database.SourceURLEnv, nil
}

// MaskApply applies the plan to the environment's branch.
func (o *Orchestrator) MaskApply(ctx context.Context) (masking.Result, error) {
	s, err := o.open(ctx, "af mask apply")
	if err != nil {
		return masking.Result{}, err
	}
	defer s.close()

	key, err := o.MaskingKey(ctx, s)
	if err != nil {
		return masking.Result{}, err
	}
	rules, hash, err := o.rules()
	if err != nil {
		return masking.Result{}, err
	}

	// Connected through the session already open, rather than opening another.
	// Opening a second would take the branch lock a second time and wait on
	// the one this call is already holding, which is a deadlock that looks
	// exactly like another process being in the way.
	conn, err := connectSession(ctx, o, s)
	if err != nil {
		return masking.Result{}, err
	}
	defer func() { _ = conn.Close(context.Background()) }()

	tables, err := masking.ReadCatalog(ctx, conn)
	if err != nil {
		return masking.Result{}, aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
	}
	plan := masking.BuildPlan(tables, rules.Assign(tables), hash)
	if !plan.Runnable() {
		return masking.Result{}, aferrors.Coded(aferrors.AFMSK010,
			"detail", masking.DescribeProblems(plan.Problems))
	}

	exec, err := masking.NewExecutor(masking.ExecutorOptions{
		Key: key, Clock: o.opts.Clock,
		Progress: func(p masking.Progress) {
			if p.Finished {
				o.progress(fmt.Sprintf("masked %s (%d rows)", p.Table, p.Rows))
			}
		},
	})
	if err != nil {
		return masking.Result{}, err
	}
	return exec.Apply(ctx, conn, plan)
}

// MaskVerify reads the environment's branch back and reports what still looks
// real.
func (o *Orchestrator) MaskVerify(ctx context.Context) (verify.Report, error) {
	conn, closeConn, err := o.connectBranch(ctx)
	if err != nil {
		return verify.Report{}, err
	}
	defer closeConn()

	unruled, err := o.unruledColumns(ctx, conn)
	if err != nil {
		return verify.Report{}, err
	}
	return verify.Scan(ctx, conn, verify.Options{
		Now:     func() time.Time { return o.opts.Clock.Now() },
		Unruled: unruled,
	})
}

// connectBranch opens a reading session and a connection to this
// environment's database, and returns a function that closes both.
//
// Its callers, the mask plan and the mask verification, read the branch and
// write nothing, so the session holds no lock. MaskApply, which writes, opens
// its own locked session and connects through that.
func (o *Orchestrator) connectBranch(ctx context.Context) (*pgx.Conn, func(), error) {
	s, err := o.openReading(ctx)
	if err != nil {
		return nil, nil, err
	}
	conn, err := connectSession(ctx, o, s)
	if err != nil {
		s.close()
		return nil, nil, err
	}
	return conn, func() {
		_ = conn.Close(context.Background())
		s.close()
	}, nil
}

// connectSession connects using a session that is already open.
func connectSession(ctx context.Context, o *Orchestrator, s *session) (*pgx.Conn, error) {
	url, err := s.dbProv.ConnString(ctx, provider.Branch{EnvID: o.envID}, provider.ConnDirect)
	if err != nil {
		return nil, err
	}
	o.opts.Redactor.Register(url.Reveal())

	conn, err := pgx.Connect(ctx, url.Reveal())
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFDB004, "env", o.envID)
	}
	return conn, nil
}

// Goldens lists what exists.
//
// A read, so it takes no lock: the list is most wanted while af up is
// branching one of them.
func (o *Orchestrator) Goldens(ctx context.Context) ([]provider.GoldenVersion, error) {
	s, err := o.openReading(ctx)
	if err != nil {
		return nil, err
	}
	defer s.close()
	return s.dbProv.ListGoldens(ctx)
}

// SelectGolden is the golden af up would branch, out of a listing, for the
// project whose identity is want. It returns nil when none is usable, and
// beside it the number of verified goldens that were refused because they were
// made for something else.
//
// Exported so that af start answers "is there a golden" with the SAME rule the
// run uses rather than a paraphrase of it. The doctor lists the Docker
// provider's images directly, because Goldens above goes through open and
// takes this branch's lock, and a status command that copied the selection
// rule into its own file would be one refactor away from reporting a golden
// the run then refuses: unverified, or another project's.
func SelectGolden(goldens []provider.GoldenVersion, want string) (*provider.GoldenVersion, int) {
	id, refused := pickGolden(goldens, want)
	if id == "" {
		return nil, refused
	}
	for i := range goldens {
		if goldens[i].ID == id {
			return &goldens[i], refused
		}
	}
	return nil, refused
}

// DestroyGolden removes one.
func (o *Orchestrator) DestroyGolden(ctx context.Context, version string) error {
	s, err := o.open(ctx, "af golden gc")
	if err != nil {
		return err
	}
	defer s.close()
	return s.dbProv.DestroyGolden(ctx, version)
}

// PreviewRow is one row before and after masking.
type PreviewRow struct {
	Column string
	Before string
	After  string
}

// MaskPreview shows what a few rows would look like after masking.
//
// It reads, transforms in memory, and writes nothing. Somebody iterating on
// rules needs to see the output before committing to it, and the alternative,
// applying and looking, is irreversible on a branch they may want to keep.
func (o *Orchestrator) MaskPreview(ctx context.Context, table string, rows int) ([][]PreviewRow, error) {
	if rows <= 0 {
		rows = 3
	}
	s, err := o.open(ctx, "af mask preview")
	if err != nil {
		return nil, err
	}
	defer s.close()

	key, err := o.MaskingKey(ctx, s)
	if err != nil {
		return nil, err
	}
	rules, hash, err := o.rules()
	if err != nil {
		return nil, err
	}
	conn, err := connectSession(ctx, o, s)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(context.Background()) }()

	tables, err := masking.ReadCatalog(ctx, conn)
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
	}
	plan := masking.BuildPlan(tables, rules.Assign(tables), hash)

	for _, tp := range plan.Tables {
		if table != "" && tp.Table.Name != table && tp.Table.String() != table {
			continue
		}
		previewed, previewErr := masking.Preview(ctx, conn, tp, key, rows)
		if previewErr != nil {
			return nil, previewErr
		}
		out := make([][]PreviewRow, 0, len(previewed))
		for _, row := range previewed {
			converted := make([]PreviewRow, 0, len(row))
			for _, cell := range row {
				converted = append(converted, PreviewRow(cell))
			}
			out = append(out, converted)
		}
		return out, nil
	}
	if table != "" {
		return nil, aferrors.Coded(aferrors.AFMSK010,
			"detail", "no table called "+table+" is being masked; 'af mask plan' lists the ones that are")
	}
	return nil, nil
}

// VerifyGolden re-checks a published golden.
//
// Worth doing because a golden published under one set of rules is not
// verified under another, and because an import path will eventually let a
// golden arrive without ever having been checked here.
func (o *Orchestrator) VerifyGolden(ctx context.Context, version string) (verify.Report, error) {
	s, err := o.open(ctx, "af golden verify")
	if err != nil {
		return verify.Report{}, err
	}
	defer s.close()

	branch, err := s.dbProv.Branch(ctx, version, o.envID+"-verify")
	if err != nil {
		return verify.Report{}, err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		// Removed whether or not the check passed. A verification branch that
		// outlives the check is a copy of the data nobody is watching.
		_ = s.dbProv.Destroy(c, branch)
	}()

	url, err := s.dbProv.ConnString(ctx, branch, provider.ConnDirect)
	if err != nil {
		return verify.Report{}, err
	}
	o.opts.Redactor.Register(url.Reveal())

	conn, err := pgx.Connect(ctx, url.Reveal())
	if err != nil {
		return verify.Report{}, aferrors.Wrap(err, aferrors.AFDB004, "env", version)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	unruled, err := o.unruledColumns(ctx, conn)
	if err != nil {
		return verify.Report{}, err
	}
	return verify.Scan(ctx, conn, verify.Options{
		Now:     func() time.Time { return o.opts.Clock.Now() },
		Unruled: unruled,
	})
}
