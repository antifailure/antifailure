package masking

import (
	"fmt"
	"sort"
	"strings"
)

// The cross store check answers one question: does a value that identifies one
// person in two stores mask to the same thing in both?
//
// It is here because the answer being NO is the worst failure this package can
// have, and it is silent. An empty ClickHouse beside a masked Postgres is a
// twin that is visibly incomplete, and somebody notices within a minute of
// opening a chart. One identity masked into two different fake people is a twin
// that is confidently wrong: every join across the two stores returns nothing
// or returns the wrong person, every report built on it is plausible, and
// nothing anywhere says so.
//
// Determinism inside one store has been enforced since the beginning, by the
// key derivation: a transform is a pure function of a subkey derived from the
// column identity, and two columns that must agree are given the same identity
// through their link. Nothing extended that across stores, because there was
// only ever one. The identity deliberately does NOT include the store, which is
// what makes the guarantee possible at all, and this file is what proves the
// guarantee holds rather than assuming it from the construction.
//
// It is deliberately strict. A pair it cannot verify counts as a pair it did
// not verify, never as one that passed, and OK is false for a report that
// found no join keys at all. A check that cannot come back unflattering is not
// a check.

// JoinColumn is one side of a candidate join key.
type JoinColumn struct {
	// Store is the datastore's name in the manifest.
	Store string
	// Table and Column are where it lives.
	Table  string
	Column string
	// Transform is what masks it, empty when nothing does.
	Transform string
	// Link is the identity its subkey is derived from.
	Link string
	// Identity is the column identity the executor would use, which is what
	// actually decides the output.
	Identity Column
}

// String names the column the way a person would.
func (j JoinColumn) String() string { return j.Store + "." + j.Table + "." + j.Column }

// Masked reports whether anything rewrites this column.
func (j JoinColumn) Masked() bool { return j.Transform != "" }

// JoinPair is one candidate join key: the same identifier, in two stores.
type JoinPair struct {
	// Key is what paired them, either a shared column name or a shared link.
	Key string
	A   JoinColumn
	B   JoinColumn
	// Identical reports whether every applicable probe masked to the same
	// value on both sides.
	Identical bool
	// Probes is how many probe values were applicable to both sides. A pair
	// where nothing was applicable is not a pair that passed.
	Probes int
	// Reason says why the pair is not identical, and is empty when it is.
	Reason string
}

// CrossStoreReport is what the check found.
type CrossStoreReport struct {
	// Stores are the datastores compared, in the order they were given.
	Stores []string
	// Pairs are the candidate join keys, sorted.
	Pairs []JoinPair
	// Checked and Identical are the denominator and the numerator of the
	// number this check exists to produce.
	Checked   int
	Identical int
}

// Percent is the share of candidate join keys that mask identically in every
// store they appear in.
//
// Zero for a report with nothing in it, which is not a pass and is not meant to
// read as one. Use OK.
func (r CrossStoreReport) Percent() float64 {
	if r.Checked == 0 {
		return 0
	}
	return float64(r.Identical) * 100 / float64(r.Checked)
}

// OK reports whether every candidate join key was verified identical.
//
// False when a pair disagreed AND false when there was nothing to compare. A
// report over two stores that share no identifier has not proved anything, and
// returning true for it would be a check that cannot say no.
func (r CrossStoreReport) OK() bool { return r.Checked > 0 && r.Identical == r.Checked }

// Mismatches returns the pairs that are not identical.
func (r CrossStoreReport) Mismatches() []JoinPair {
	var out []JoinPair
	for _, p := range r.Pairs {
		if !p.Identical {
			out = append(out, p)
		}
	}
	return out
}

// Summary is the one line a person quotes, and the lines under it that say why
// when the number is not 100.
func (r CrossStoreReport) Summary() string {
	var b strings.Builder
	if r.Checked == 0 {
		fmt.Fprintf(&b, "no column identifies the same thing in more than one of %s, "+
			"so nothing was compared and nothing is proved\n", strings.Join(r.Stores, " and "))
		return b.String()
	}
	fmt.Fprintf(&b, "join keys verified identical across %s: %d of %d (%.1f%%)\n",
		strings.Join(r.Stores, " and "), r.Identical, r.Checked, r.Percent())
	for _, p := range r.Mismatches() {
		fmt.Fprintf(&b, "  %s and %s: %s\n", p.A, p.B, p.Reason)
	}
	return b.String()
}

// StoreAssignments is one store's masking decisions, as its plan made them.
type StoreAssignments struct {
	// Store is the datastore's name in the manifest.
	Store string
	// Assignments is what Assign decided for that store's catalog.
	Assignments []Assignment
}

// DefaultProbes are the values the check masks through both sides.
//
// Spread across the shapes the transforms accept, because several of them
// refuse a value that is not what they are for: uuid_remap refuses anything
// that is not a UUID, credit_card refuses anything that is not a card, and
// date_shift refuses anything that is not a date. A probe set of email
// addresses alone would leave a pair of UUID columns with nothing applicable to
// compare, and a pair with nothing applicable is reported as unverified rather
// than counted as identical.
//
// Every one of them is a reserved name or a documentation range, which is not
// only tidiness. This list ships in the product and its values appear in the
// report it prints, so an address here that could belong to somebody is an
// address this repository publishes. contactcheck said so, by name, on the
// first version of this file.
func DefaultProbes() []string {
	return []string{
		"ada@example.com",
		"01890fa1-9e40-7d3c-8b9a-2f5c6d7e8a90",
		"+14155550123",
		"4111111111111111",
		"2026-09-07",
		"1234567",
		"Ada Lovelace",
		"https://example.com/report?tab=usage",
		"198.51.100.24",
		"A sentence somebody typed into a notes field.",
	}
}

// CrossStoreCheck compares what two or more stores do to the same identifier.
//
// A candidate join key is a column name that appears in more than one store, or
// two columns in different stores that a rule gave the same EXPLICIT link.
// Either is a join somebody could write, and either breaks if the two sides do
// not mask identically. The default link, which Assign fills in as the
// transform's own name, does not count: it would pair every nullified column
// with every other one and pass every time.
//
// There is deliberately no way to declare a candidate exempt. Two columns with
// one name in two stores that mean different things is a real thing to look at,
// and the cost of looking at it is a rule; the cost of silencing it is a twin
// that is wrong in a way nobody can see. A false positive here costs somebody a
// minute, and a false negative costs somebody their data.
func CrossStoreCheck(key *Key, stores []StoreAssignments, probes []string) (CrossStoreReport, error) {
	if key == nil {
		return CrossStoreReport{}, fmt.Errorf("masking: the cross store check needs a key")
	}
	if len(stores) < 2 {
		return CrossStoreReport{}, fmt.Errorf(
			"masking: the cross store check compares stores and it was given %d", len(stores))
	}
	if len(probes) == 0 {
		probes = DefaultProbes()
	}

	report := CrossStoreReport{}
	columns := map[string][]JoinColumn{}
	links := map[string][]JoinColumn{}
	for _, s := range stores {
		report.Stores = append(report.Stores, s.Store)
		for _, a := range s.Assignments {
			j := JoinColumn{
				Store: s.Store, Table: a.Table.String(), Column: a.Column.Name,
				Transform: a.Transform, Link: a.Link,
				Identity: Column{
					Schema: a.Table.Schema, Table: a.Table.Name,
					Name: a.Column.Name, Link: a.Link,
				},
			}
			if a.Problem != "" {
				// An assignment that cannot be carried out is not a decision
				// about what the column holds, and a plan carrying one is
				// refused before anything runs. Comparing it would report a
				// difference that no environment can reach.
				continue
			}
			columns[a.Column.Name] = append(columns[a.Column.Name], j)
			if a.Link != "" && a.Link != a.Transform {
				// Only a link somebody WROTE. Assign fills in the transform's
				// own name as the default link, so every nullified column in
				// one store would otherwise pair with every nullified column
				// in the other: pairs that share an identity by construction,
				// that pass by construction, and that would pad the
				// denominator of this check with agreement it did not have to
				// find. A check whose number is mostly free passes is a check
				// nobody should quote. An explicit link is the opposite: it is
				// somebody saying these two are the same thing.
				links[a.Link] = append(links[a.Link], j)
			}
		}
	}

	seen := map[string]bool{}
	add := func(keyName string, a, b JoinColumn) {
		if a.Store == b.Store {
			return
		}
		if !a.Masked() && !b.Masked() {
			// Neither side is rewritten, so there is nothing here that could
			// diverge. Both hold what production held, which is a different
			// problem and one the plan already reports as copied unchanged.
			return
		}
		id := a.String() + "|" + b.String()
		if b.String() < a.String() {
			id = b.String() + "|" + a.String()
			a, b = b, a
		}
		if seen[id] {
			return
		}
		seen[id] = true
		report.Pairs = append(report.Pairs, comparePair(key, keyName, a, b, probes))
	}

	for name, cols := range columns {
		for i := range cols {
			for j := i + 1; j < len(cols); j++ {
				add(name, cols[i], cols[j])
			}
		}
	}
	for link, cols := range links {
		for i := range cols {
			for j := i + 1; j < len(cols); j++ {
				add("link "+link, cols[i], cols[j])
			}
		}
	}

	sort.Slice(report.Pairs, func(i, j int) bool {
		if report.Pairs[i].A.String() != report.Pairs[j].A.String() {
			return report.Pairs[i].A.String() < report.Pairs[j].A.String()
		}
		return report.Pairs[i].B.String() < report.Pairs[j].B.String()
	})
	report.Checked = len(report.Pairs)
	for _, p := range report.Pairs {
		if p.Identical {
			report.Identical++
		}
	}
	return report, nil
}

// comparePair masks the probes through both sides and reports what happened.
func comparePair(key *Key, keyName string, a, b JoinColumn, probes []string) JoinPair {
	pair := JoinPair{Key: keyName, A: a, B: b}

	switch {
	case a.Masked() != b.Masked():
		unmasked, masked := a, b
		if a.Masked() {
			unmasked, masked = b, a
		}
		pair.Reason = fmt.Sprintf(
			"%s is masked with %s and %s is copied unchanged, so one store holds a fake "+
				"value for this identifier and the other holds the real one",
			masked, masked.Transform, unmasked)
		return pair
	case a.Transform != b.Transform:
		pair.Reason = fmt.Sprintf(
			"the two are masked with different transforms, %s and %s, so one identity "+
				"becomes two people",
			a.Transform, b.Transform)
		return pair
	case a.Identity.String() != b.Identity.String():
		pair.Reason = fmt.Sprintf(
			"the two derive their subkey from different identities, %s and %s, so the same "+
				"input masks to two different outputs; give them one link",
			a.Identity, b.Identity)
		return pair
	}

	transform, ok := Lookup(a.Transform)
	if !ok {
		pair.Reason = "there is no transform called " + a.Transform
		return pair
	}

	for _, probe := range probes {
		value := probe
		left, leftErr := transform.Apply(key, a.Identity, &value)
		right, rightErr := transform.Apply(key, b.Identity, &value)
		if leftErr != nil || rightErr != nil {
			// A transform refuses a value that is not what it is for, and
			// refusing the same value on both sides is agreement rather than
			// disagreement: this probe simply does not apply to this pair.
			// One side refusing and the other not would be a real difference,
			// and is why this compares the errors rather than skipping on the
			// first one.
			if (leftErr == nil) != (rightErr == nil) {
				pair.Reason = fmt.Sprintf(
					"%s accepted a value that %s refused, which cannot happen for two "+
						"columns masked the same way", a, b)
				return pair
			}
			continue
		}
		pair.Probes++
		if orNull(left) != orNull(right) {
			pair.Reason = fmt.Sprintf(
				"the same input masks to different values in the two stores, which is the " +
					"failure this check exists for")
			return pair
		}
	}

	if pair.Probes == 0 {
		pair.Reason = fmt.Sprintf(
			"no probe value was applicable to %s, so nothing was compared and this pair "+
				"is not verified", a.Transform)
		return pair
	}
	pair.Identical = true
	return pair
}
