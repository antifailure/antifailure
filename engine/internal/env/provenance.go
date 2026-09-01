package env

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Which project a golden was made for, and out of what.
//
// A golden pool is shared. The Docker provider keeps goldens as images on the
// daemon, and a daemon is machine wide, so every project on a laptop draws
// from one pool; a configured golden store is shared on purpose, by a fleet.
// Selection therefore has to answer "may this environment branch that golden",
// and until this file existed it answered a different question.
//
// What it answered was "were the two masked under the same rules". That is a
// real question and the answer is still needed, but it is not the same
// question, and the gap between them is the whole defect: a project with no
// masking.yaml declares no rules, json.Marshal of a nil slice is the four
// bytes `null`, and every project on the machine without a masking.yaml
// therefore hashed to 74234e98afe7498f and shared one pool. The discriminator
// separated masking configurations. It did not separate projects, and the
// common configuration is no configuration.
//
// Reproduced before it was fixed, with the released binary and two ordinary
// Express repositories: acme-billing declared a production database and ran
// `af golden refresh`; nova-shop declared none, said in its own manifest that
// "branches will start empty", and came up holding acme-billing's customers
// and regions tables with acme-billing's rows in them. Copying one project's
// data into another project's preview is the failure this product is sold to
// prevent.
//
// So a golden now records the identity of the work that produced it, and
// selection is equality on that identity. The parts, and why each one is
// stable across the reuse that has to keep working:
//
//   - project, the manifest name. It travels with the repository, so it is the
//     same on a laptop, on every branch, on a CI runner with a fresh checkout,
//     and on a second machine that pulls from the store. The repository root
//     path has none of those properties: CI checks out somewhere new every
//     run, a git worktree per branch is a different root, and the machine that
//     pulls a published golden has never seen the publisher's disk at all.
//     Keying on the path would cost a full refresh on every one of those,
//     which is a pool that never matches, which people route around.
//   - source, the NAME of the variable holding production's connection string,
//     never the string. The resolved host and database would discriminate
//     harder, and it is exactly the wrong choice: the runner that pulls a
//     published golden has no production credential, which is the entire point
//     of publishing, so it cannot resolve the variable and would compute a
//     different identity from the machine that made the golden. The declared
//     name is in the manifest, so both sides read the same thing. It also
//     keeps a production hostname out of a label and out of a shared bucket.
//   - seed, the command that fills a golden when there is no production to
//     copy. Change the command and the next run refuses the golden the old one
//     made rather than branching stale data.
//   - rules, the masking digest that used to be the whole of this. Still here,
//     still doing its own job: a golden of the right database masked under
//     rules somebody has since changed is a golden whose attestation answers a
//     different question from the one being asked.
//   - subset, because a slice of a database and the whole of it are different
//     content, and a manifest that turns subsetting on has changed what a
//     golden IS rather than how it was made.
//   - postgres, the major version. Branching a 16 golden into a project that
//     asks for 17 is a preview of a server the project does not run.
//
// The schema of the golden itself is deliberately not part of this. It is
// tempting, and it cannot work as an acceptance test: the golden holds
// production's schema, the project's own migrations have not run yet, and
// there is nothing on this side to compare it against without running them
// first. A fingerprint is good provenance to display and a bad key to match
// on, and matching on it would make every pool empty for one migration.
//
// What this deliberately does NOT separate: two projects that declare the same
// name, the same source variable, the same seed, the same rules, the same
// subset and the same major version. Those two goldens were made from the same
// inputs, and there is nothing observable that tells them apart. Separating
// them needs an identifier a person assigns, and the manifest has no field for
// one today. It is recorded here rather than left to be rediscovered.
type provenance struct {
	// Project is the manifest name.
	Project string
	// Source is the variable naming production, or the empty string.
	Source string
	// Seed is the manifest's seed command, or the empty string.
	Seed string
	// Rules is the masking rules digest, the same value GoldenVersion.RulesHash
	// carries.
	Rules string
	// Subset describes the slice the manifest asks for, or the empty string.
	Subset string
	// Postgres is the major version the golden holds.
	Postgres int
}

// provenanceScheme prefixes every digest.
//
// A version rather than a bare hash, so that changing what goes into the
// recipe is a visible refusal of every older golden rather than a silent one.
// Anybody who bumps it should expect one refresh per project and should say so
// in the changelog.
const provenanceScheme = "gp1"

// digest is the value a golden records and selection compares.
//
// The canonical form names every field rather than relying on the order of a
// struct or of a JSON encoder. Reordering the fields of a Go struct is a
// refactor, and a refactor that silently invalidates every golden on every
// machine is the kind of change nobody connects to the refresh it causes.
func (p provenance) digest() string {
	var b strings.Builder
	write := func(k, v string) {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(v)
		b.WriteByte('\n')
	}
	write("project", p.Project)
	write("source", orNone(p.Source))
	write("seed", orNone(shortHash("seed", p.Seed)))
	write("rules", orNone(p.Rules))
	write("subset", orNone(p.Subset))
	write("postgres", strconv.Itoa(p.Postgres))
	sum := sha256.Sum256([]byte(b.String()))
	return provenanceScheme + "-" + hex.EncodeToString(sum[:10])
}

// describe says in one clause where a golden came from.
//
// It exists because the run used to print only "branching the database from
// gv_20260901033741_74234e98", an opaque identifier and nothing else, which is
// exactly as convincing when the choice is right as when it is wrong. A
// correct decision nobody can check is one refactor away from an incorrect one
// nobody notices.
//
// It describes THIS project's inputs, and it is only ever printed about a
// golden whose recorded identity is equal to this project's, so the two
// descriptions are the same description. The pinned path prints it too. It
// may, because a pin now requires that same equality: naming a version says
// which of this project's goldens to use, not that the check is waived.
func (p provenance) describe() string {
	parts := make([]string, 0, 3)
	parts = append(parts, "made for "+p.Project)
	switch {
	case p.Source != "":
		parts = append(parts, "from the database named by "+p.Source)
	case p.Seed != "":
		parts = append(parts, "from this project's seed command")
	default:
		parts = append(parts, "from no production database, so it holds no rows")
	}
	if p.Rules != "" {
		parts = append(parts, "under masking rules "+p.Rules)
	}
	return strings.Join(parts, ", ")
}

// provenanceOf is the identity of the golden this project may branch.
func (o *Orchestrator) provenanceOf() (provenance, error) {
	_, rules, err := o.rules()
	if err != nil {
		return provenance{}, err
	}
	p := provenance{
		Project:  o.opts.Manifest.Name,
		Rules:    rules,
		Postgres: databaseVersion(o.opts.Manifest),
	}
	if db := o.opts.Manifest.Database; db != nil {
		p.Source = db.SourceURLEnv
		p.Seed = db.Seed
		p.Subset = subsetIdentity(db.Subset)
	}
	return p, nil
}

// subsetIdentity describes the slice a manifest asks for, and is empty when it
// asks for none.
//
// A disabled subset block and an absent one produce the same golden, so they
// produce the same identity. The relationships are sorted because a manifest
// listing the same two in the other order asks for the same slice, and a
// refresh forced by moving a line is a refresh nobody can explain.
func subsetIdentity(s *schema.Subset) string {
	if s == nil || !s.Enabled {
		return ""
	}
	follow := "default"
	if s.FollowDependents != nil {
		follow = strconv.Itoa(*s.FollowDependents)
	}
	virtual := make([]string, 0, len(s.VirtualRelationships))
	for _, r := range s.VirtualRelationships {
		virtual = append(virtual, r.From+">"+r.To)
	}
	sort.Strings(virtual)
	return shortHash("subset", fmt.Sprintf("%s|%s|%d|%s|%s",
		s.SeedTable, s.SeedWhere, s.MaxRows, follow, strings.Join(virtual, ",")))
}

// shortHash digests one component under its own domain separator, so that a
// seed command and a subset description can never collide with each other.
func shortHash(domain, body string) string {
	if body == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(domain + "\x00" + body))
	return hex.EncodeToString(sum[:8])
}

// orNone spells an absent component rather than leaving it blank, so that
// "declares no source" and "declares a source named the empty string" cannot
// hash to the same thing.
func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// GoldenIdentity is the value a golden has to record for this project to be
// allowed to branch it.
//
// Exported for the commands that have to reason about the same pool the
// selector does. `af golden gc` is the sharp one: it used to sweep every
// golden the daemon held, so running it in one project deleted another
// project's goldens, which is the same shared pool defect pointing the other
// way. `af golden list` uses it to say which rows are this project's rather
// than presenting a machine's worth of goldens as though they were all
// available here.
func (o *Orchestrator) GoldenIdentity() (string, error) {
	p, err := o.provenanceOf()
	if err != nil {
		return "", err
	}
	return p.digest(), nil
}
