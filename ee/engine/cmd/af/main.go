// Command af is the Antifailure engine, enterprise edition.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The same engine, the same commands, the same behaviour, with the enterprise
// features plugged into the sockets the community edition already exposes. It
// is a separate binary rather than a build tag because the code is a separate
// module: there is no import path by which the community binary could pull this
// in even by accident, and CI proves it by deleting this directory and building
// the community engine.
//
// This file is deliberately the only thing in the enterprise edition that knows
// how the pieces fit together, and it is short enough to read in one go. That
// matters: it is the answer to "what does the licence actually turn on", and
// the answer should be readable rather than distributed across a dozen init
// functions.
//
// Before this existed, everything under ee/engine compiled, was tested, and
// could not be run by anything. A policy hook nothing consults and a secret
// source nothing registers are the same shippable gap as a block button that
// does not hide anything: the pieces are all there and the behaviour is absent.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/antifailure/antifailure/ee/engine/compliance"
	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
	"github.com/antifailure/antifailure/ee/engine/policyenforce"
	"github.com/antifailure/antifailure/ee/engine/secrets"
	"github.com/antifailure/antifailure/engine/pkg/afcli"
	"github.com/antifailure/antifailure/engine/pkg/edition"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/jackc/pgx/v5"
)

func main() {
	// The same signal handling the community binary has, from the same
	// function, so that control C means the same thing in both. The first
	// interrupt cancels so in flight work rolls back and teardown runs; the
	// second exits with the journal intact.
	ctx, _, stop := afcli.WithSignals(context.Background())
	defer stop()

	// The licence is attached to the context rather than passed as an argument,
	// which is what lets a check happen deep inside a command without a licence
	// parameter on every function between here and there. An enterprise entry
	// point that forgot this would degrade to the community behaviour, which is
	// the direction the mistake has to fail in and is why it is the default.
	status, notes := loadLicence(os.Getenv)
	ctx = feature.With(ctx, status)
	// And again in the form the community command tree can read, so that
	// af license status reports this installation rather than reporting the
	// community edition from inside the enterprise binary. Two attachments
	// because they serve two consumers: feature.Enabled needs the evaluated
	// licence, and the command needs rendered text it can print without
	// importing any enterprise code.
	ctx = edition.With(ctx, describe(status))

	registered, err := secrets.RegisterFromEnvironment(extension.Default, os.Getenv)
	if err != nil {
		// Refused at startup rather than at the first lookup. A source somebody
		// named in AF_SECRET_SOURCES and this binary could not build would
		// otherwise be silently absent, their variables would resolve out of
		// .env instead, and the environment would come up with the wrong
		// values, which is worse than not coming up.
		fmt.Fprintf(os.Stderr, "af: %v\n", err)
		os.Exit(3)
	}

	// Printed to standard error, never to standard output, because every
	// command in this CLI has a --output json form and a startup banner on
	// standard output would break every one of them.
	for _, note := range notes {
		fmt.Fprintf(os.Stderr, "af: %s\n", note)
	}

	// Each configured store, with whether it can actually be used, at startup
	// rather than on the first environment somebody tries to create. This makes
	// whatever reachability check each store has, which is the point: an
	// operator whose Vault token expired should find that out when they start
	// the binary, not twenty minutes later inside a failed run.
	//
	// Only when a store is configured, so the ordinary case of no enterprise
	// secret sources at all prints nothing. A banner on every invocation of
	// every command is a banner people stop reading.
	if len(registered) > 0 {
		for _, line := range secrets.Describe(ctx, registered) {
			fmt.Fprintf(os.Stderr, "af: secret source: %s\n", line)
		}
	}

	// The organization policy, plugged into the same registry and refused at
	// startup for the same reason. This registration is the whole of what the
	// policy_enforcement feature does, and it was missing: the hook was written
	// and tested and no binary constructed one, so a licensed customer got a
	// feature that refused nothing and a compliance control that reported no
	// policy was configured. The comment at the top of this file already said
	// that a policy hook nothing consults is a shippable gap, and this file was
	// the place it was.
	//
	// Registered unconditionally rather than only under a licence. Hook.Check
	// asks the licence per call, so a licence that lapses mid-process stops
	// enforcement without a restart, and gating here instead would mean an
	// installation that starts before its licence renews never enforces again.
	policy, err := policyenforce.RegisterFromEnvironment(extension.Default, os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "af: %v\n", err)
		os.Exit(3)
	}
	for _, rule := range policyenforce.Rules(policy) {
		fmt.Fprintf(os.Stderr, "af: organization policy: %s\n", rule)
	}
	if warning := status.Warning; warning != "" {
		fmt.Fprintf(os.Stderr, "af: %s\n", warning)
	}
	os.Exit(afcli.Run(ctx, os.Args[1:], afcli.Options{
		Extra: []afcli.Command{compliance.Contributed(gatherEvidence)},
	}))
}

// loadLicence reads and evaluates the licence, and never fails.
//
// Never, and that is the whole design of the licensing in this product. A
// missing licence, a malformed one, a build with no signing keys, and an
// expired one all produce a status that permits nothing and a sentence saying
// which of those happened. None of them stops the engine: an organization whose
// purchase order is slow does not lose its preview environments, and neither
// does one running a binary they built themselves.
func loadLicence(getenv func(string) string) (license.Status, []string) {
	key := getenv(licenceEnv)
	if key == "" {
		// No licence at all is the ordinary case for somebody evaluating this,
		// and it is not worth a line of output. Everything enterprise is off
		// and af license status says so when asked.
		return license.None(), nil
	}

	verifier, keys, err := license.LoadVerifier(getenv)
	if err != nil {
		return license.None(), []string{err.Error()}
	}
	if keys == 0 {
		return license.None(), []string{license.NoKeysMessage}
	}

	claims, err := verifier.Parse(key)
	if err != nil {
		return license.None(), []string{"the licence could not be read: " + err.Error()}
	}
	org := getenv(orgEnv)
	if org == "" {
		// The licence names the organization it was issued to, and without one
		// to compare against there is nothing to check. Using the licence's own
		// value would make the check a tautology, so it is refused with the
		// variable that fixes it named.
		return license.None(), []string{
			"the licence is for " + claims.Org + " and " + orgEnv +
				" is not set, so it cannot be checked against this installation"}
	}

	// LastSeen is zero here, which turns off the clock rollback check. That is
	// honest rather than convenient: the check needs somewhere durable to
	// record when the licence was last evaluated, the control plane's database
	// is where a hosted installation keeps it, and a self-hosted engine has no
	// such place yet. A check that pretended to run against a value that is
	// always zero would be a check that never fires.
	status := verifier.Evaluate(claims, license.Evaluation{Org: org, Now: time.Now()})
	return status, nil
}

// gatherEvidence opens a read only connection to the control plane and reads.
//
// The connection is opened per invocation rather than held, because this is a
// command that runs and exits, and a pool would be a pool of one connection
// used once. The URL is the control plane's, and the role it names should be
// able to SELECT and nothing else: a tool that produces evidence about a
// database it can also write to is a tool whose evidence is worth less.
func gatherEvidence(ctx context.Context, org string, from, to time.Time) (compliance.Evidence, error) {
	url := os.Getenv(databaseEnv)
	if url == "" {
		return compliance.Evidence{}, fmt.Errorf(
			"%s is not set, and the evidence lives in the control plane's database", databaseEnv)
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return compliance.Evidence{}, fmt.Errorf("the control plane database: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	reader := compliance.NewReader(conn)
	if role := os.Getenv(appRoleEnv); role != "" {
		reader.AppRole = role
	}
	if days := os.Getenv(retentionEnv); days != "" {
		parsed, err := strconv.Atoi(days)
		if err != nil {
			return compliance.Evidence{}, fmt.Errorf("%s is not a number of days", retentionEnv)
		}
		reader.RetentionDays = parsed
	}
	return reader.Gather(ctx, org, from, to)
}

// describe renders a licence for the command that prints it.
//
// Rendering here rather than in the CLI is what keeps the community build free
// of enterprise code: what crosses the boundary is strings, not a licence.
func describe(status license.Status) edition.Status {
	out := edition.Status{
		Name:    "enterprise",
		State:   string(status.State),
		Org:     status.Claims.Org,
		Plan:    status.Claims.Plan,
		Warning: status.Warning,
	}
	if !status.Claims.ExpiresAt.IsZero() {
		out.ExpiresAt = status.Claims.ExpiresAt.UTC().Format("2 January 2006")
	}

	// Asked rather than copied out of the claims. A licence lists what was
	// bought and Enabled reports what is permitted right now, and those differ
	// for an expired licence, a revoked one, and a rolled back clock. Printing
	// the claims would tell an administrator whose licence lapsed that
	// everything is on.
	for _, f := range license.AllFeatures() {
		if status.Enabled(f) {
			out.Features = append(out.Features, string(f))
		}
	}

	switch status.State {
	case license.StateNone:
		out.Message = "This is the enterprise edition and no license is installed, " +
			"so it behaves exactly as the community edition does."
	case license.StateActive:
		out.Message = "This is the enterprise edition, licensed to " + status.Claims.Org + "."
	case license.StateGrace:
		out.Message = "This is the enterprise edition. The license has expired and is " +
			"still being honoured."
	default:
		out.Message = "This is the enterprise edition. The license is not being honoured, " +
			"so enterprise features are off and every enterprise setting is preserved."
	}
	return out
}

const (
	licenceEnv = "AF_LICENSE_KEY"
	orgEnv     = "AF_ORG"
	// databaseEnv is the control plane's database, which is where the audit
	// log, the attestations and the environments are.
	databaseEnv = "AF_CONTROL_PLANE_DATABASE_URL"
	// appRoleEnv names the role the APPLICATION connects as, whose privileges
	// on the audit log are one of the things reported on. It is not the role
	// this command connects as, and conflating the two would report on the
	// wrong one.
	appRoleEnv = "AF_APP_ROLE"
	// retentionEnv is how long audit entries are kept, in days. Read from
	// configuration rather than from the database, because a retention policy
	// that has not yet deleted anything leaves no trace in the data.
	retentionEnv = "AF_AUDIT_RETENTION_DAYS"
)
