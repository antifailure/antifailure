package oracle

import (
	"sort"
	"strings"
)

// pairPersonaRows gives the candidate's persona rows the baseline's keys.
//
// THE FAILURE. Both sides of a comparison provision the same personas, each
// into its own database, and every row that provisioning writes carries a
// primary key the database generated for it. Rows are matched by primary key,
// so the owner's account on one side and the owner's account on the other
// paired with nothing: the baseline's read as a row the candidate stopped
// writing and the candidate's as a row nobody asked for. This repository's own
// manifest, on an identical build, reported eleven differences, six of them
// major, and every one was a persona. A comparison that raises six majors on a
// change that did nothing is one a reader stops opening.
//
// So a row the two sides do not share by key, and which names a persona, is
// matched by that persona instead. A row names a persona when a column holds
// one of the persona identities the manifest declares, compared without case,
// or when a column holds an identifier already matched this way, which is how
// the owner's membership follows the owner's account. The matched candidate row
// is re-keyed to the baseline's key and then compared like any other row, so a
// column that really differs, a name the candidate provisioned differently or a
// role the candidate's migration rewrote, is still reported, and reported as a
// changed row with the column named.
//
// Only a match that is unique on both sides is made. Two sessions for the
// owner on each side have the same persona behind them and nothing to say which
// is which, and inventing that correspondence would print a confident and wrong
// "this column changed". They stay reported as they were.
//
// Identifiers are followed only when they are UUIDs. A generated integer key
// equal to 5 is also every other 5 in the database, a count or a price, and
// following it would match rows to a persona by coincidence.
func pairPersonaRows(personas []string, base, cand *Snapshot) *Snapshot {
	names := map[string]bool{}
	for _, p := range personas {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			names[p] = true
		}
	}
	if len(names) == 0 || base == nil || cand == nil {
		return cand
	}

	baseTables := map[string]*Table{}
	for i := range base.Tables {
		baseTables[base.Tables[i].Qualified()] = &base.Tables[i]
	}

	// learned is a candidate identifier and the baseline identifier the same
	// persona's row carries; targets is the baseline half, for looking a value
	// up from the baseline side.
	learned := map[string]string{}
	targets := map[string]bool{}
	// paired is, per table, a candidate row key and the baseline row key it is
	// matched to.
	paired := map[string]map[string]string{}

	for progress := true; progress; {
		progress = false
		for i := range cand.Tables {
			c := &cand.Tables[i]
			b := baseTables[c.Qualified()]
			if b == nil || len(c.Key) == 0 || !sameKey(b.Key, c.Key) || b.Truncated || c.Truncated {
				continue
			}
			name := c.Qualified()
			if paired[name] == nil {
				paired[name] = map[string]string{}
			}
			taken := map[string]bool{}
			for _, bk := range paired[name] {
				taken[bk] = true
			}

			fromBase := func(s string) (string, bool) { return s, targets[s] }
			fromCand := func(s string) (string, bool) {
				t, ok := learned[s]
				return t, ok
			}
			baseBy := map[string][]string{}
			for k, row := range b.Rows {
				if _, shared := c.Rows[k]; shared || taken[k] {
					continue
				}
				if sig := personaSignature(row, b.Key, names, fromBase); sig != "" {
					baseBy[sig] = append(baseBy[sig], k)
				}
			}
			candBy := map[string][]string{}
			for k, row := range c.Rows {
				if _, shared := b.Rows[k]; shared {
					continue
				}
				if _, done := paired[name][k]; done {
					continue
				}
				if sig := personaSignature(row, c.Key, names, fromCand); sig != "" {
					candBy[sig] = append(candBy[sig], k)
				}
			}

			for sig, bks := range baseBy {
				cks := candBy[sig]
				if len(bks) != 1 || len(cks) != 1 {
					continue
				}
				bk, ck := bks[0], cks[0]
				paired[name][ck] = bk
				progress = true
				for _, col := range c.Key {
					cv, cok := c.Rows[ck][col].(string)
					bv, bok := b.Rows[bk][col].(string)
					if cok && bok && uuidPattern.MatchString(cv) && uuidPattern.MatchString(bv) {
						learned[cv] = bv
						targets[bv] = true
					}
				}
			}
		}
	}

	matched := false
	for _, m := range paired {
		if len(m) > 0 {
			matched = true
			break
		}
	}
	if !matched {
		return cand
	}

	out := *cand
	out.Tables = make([]Table, len(cand.Tables))
	for i := range cand.Tables {
		t := cand.Tables[i]
		m := paired[t.Qualified()]
		if len(m) > 0 {
			b := baseTables[t.Qualified()]
			rows := make(map[string]map[string]any, len(t.Rows))
			for k, row := range t.Rows {
				bk, ok := m[k]
				if !ok {
					rows[k] = row
					continue
				}
				rekeyed := make(map[string]any, len(row))
				for col, v := range row {
					rekeyed[col] = v
				}
				for _, col := range t.Key {
					rekeyed[col] = b.Rows[bk][col]
				}
				rows[bk] = rekeyed
			}
			t.Rows = rows
		}
		out.Tables[i] = t
	}
	return &out
}

// personaSignature is what a row says about which persona it belongs to, or
// empty when it names none. The key columns are left out, because they are the
// generated values the match exists to see past.
func personaSignature(
	row map[string]any, key []string, names map[string]bool, known func(string) (string, bool),
) string {
	var parts []string
	for col, v := range row {
		if isKeyColumn(col, key) {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		if lower := strings.ToLower(s); names[lower] {
			parts = append(parts, col+"\x00persona\x00"+lower)
			continue
		}
		if uuidPattern.MatchString(s) {
			if id, ok := known(s); ok {
				parts = append(parts, col+"\x00id\x00"+id)
			}
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x01")
}

func isKeyColumn(col string, key []string) bool {
	for _, k := range key {
		if k == col {
			return true
		}
	}
	return false
}

func sameKey(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
