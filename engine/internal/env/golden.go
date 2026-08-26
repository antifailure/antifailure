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

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/verify"
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
func (o *Orchestrator) rules() (*masking.RuleSet, string, error) {
	var declared []masking.Rule
	if o.opts.Manifest.Database != nil && o.opts.Manifest.Database.MaskingRules != "" {
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
}

// RefreshGolden copies, masks, verifies, and publishes a golden.
func (o *Orchestrator) RefreshGolden(ctx context.Context) (*GoldenResult, error) {
	s, err := o.open(ctx, "af golden refresh")
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

	result := &GoldenResult{}
	started := o.opts.Clock.Now()

	spec := provider.GoldenSpec{
		Version:   databaseVersion(o.opts.Manifest),
		RulesHash: hash,
		SourceURL: o.sourceURL(),
		Mask: func(ctx context.Context, url secrets.Value) error {
			rows, tables, maskErr := o.maskDatabase(ctx, url, key, rules, hash)
			result.Rows, result.Tables = rows, tables
			return maskErr
		},
		Verify: func(ctx context.Context, url secrets.Value) (string, error) {
			report, att, verifyErr := o.verifyDatabase(ctx, url, hash)
			result.Report = report
			result.Attestation = att
			if verifyErr != nil {
				return "", verifyErr
			}
			return att, nil
		},
	}

	gv, err := s.dbProv.RefreshGolden(ctx, spec)
	result.Version, result.Verified = gv.ID, gv.Verified
	result.Duration = o.opts.Clock.Since(started)
	if err != nil {
		return result, err
	}
	return result, nil
}

// sourceURL is the production database to copy, when one is configured.
func (o *Orchestrator) sourceURL() secrets.Value {
	if o.opts.Manifest.Database == nil || o.opts.Manifest.Database.SourceURLEnv == "" {
		return secrets.Value{}
	}
	getenv := o.opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	raw := getenv(o.opts.Manifest.Database.SourceURLEnv)
	if raw == "" {
		return secrets.Value{}
	}
	// Registered before it is used, so that it is redacted everywhere rather
	// than everywhere somebody remembered.
	o.opts.Redactor.Register(raw)
	return secrets.New(raw)
}

// maskDatabase applies the rules to a database.
func (o *Orchestrator) maskDatabase(
	ctx context.Context, url secrets.Value, key *masking.Key, rules *masking.RuleSet, hash string,
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
	if len(plan.Unclassified) > 0 {
		// Reported, not refused. Refusing would make the first run of a real
		// schema impossible; saying nothing would let the columns ship.
		o.progress(fmt.Sprintf(
			"%d columns matched no rule and may hold something: %s",
			len(plan.Unclassified), masking.DescribeColumns(plan.Unclassified, 6)))
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
		return 0, 0, aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
	}
	res, err := exec.Apply(ctx, conn, plan)
	if err != nil {
		return res.Rows, res.Tables, aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
	}
	return res.Rows, res.Tables, nil
}

// verifyDatabase reads a database back and signs what it found.
func (o *Orchestrator) verifyDatabase(
	ctx context.Context, url secrets.Value, hash string,
) (verify.Report, string, error) {
	conn, err := pgx.Connect(ctx, url.Reveal())
	if err != nil {
		return verify.Report{}, "", aferrors.Wrap(err, aferrors.AFDB004, "env", "the golden candidate")
	}
	defer func() { _ = conn.Close(context.Background()) }()

	report, err := verify.Scan(ctx, conn, verify.Options{
		Now:      func() time.Time { return o.opts.Clock.Now() },
		Progress: func(line string) { o.progress("verification: " + line) },
	})
	if err != nil {
		return report, "", aferrors.Wrap(err, aferrors.AFMSK002,
			"detector", "scan", "table", "any", "column", "any")
	}

	_, priv, err := verify.GenerateKey()
	if err != nil {
		return report, "", err
	}
	att, err := verify.Sign(report, "", hash, priv)
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
		first := report.Findings[0]
		return report, string(body), aferrors.Coded(aferrors.AFMSK002,
			"detector", first.Detector, "table", first.Schema+"."+first.Table,
			"column", first.Column)
	}
	return report, string(body), nil
}

// PlanResult is what af mask plan produced.
type PlanResult struct {
	Plan      masking.Plan
	RulesHash string
}

// MaskPlan reads a database and reports what masking would do to it.
//
// It runs against the environment's own branch, so somebody iterating on rules
// sees the effect on the schema they actually have rather than on a
// description of it.
func (o *Orchestrator) MaskPlan(ctx context.Context) (*PlanResult, error) {
	conn, closeConn, err := o.connectBranch(ctx)
	if err != nil {
		return nil, err
	}
	defer closeConn()

	rules, hash, err := o.rules()
	if err != nil {
		return nil, err
	}
	tables, err := masking.ReadCatalog(ctx, conn)
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
	}
	return &PlanResult{
		Plan:      masking.BuildPlan(tables, rules.Assign(tables), hash),
		RulesHash: hash,
	}, nil
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

	return verify.Scan(ctx, conn, verify.Options{
		Now: func() time.Time { return o.opts.Clock.Now() },
	})
}

// connectBranch opens a session and a connection to this environment's
// database, and returns a function that closes both.
func (o *Orchestrator) connectBranch(ctx context.Context) (*pgx.Conn, func(), error) {
	s, err := o.open(ctx, "af mask")
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
func (o *Orchestrator) Goldens(ctx context.Context) ([]provider.GoldenVersion, error) {
	s, err := o.open(ctx, "af golden list")
	if err != nil {
		return nil, err
	}
	defer s.close()
	return s.dbProv.ListGoldens(ctx)
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
