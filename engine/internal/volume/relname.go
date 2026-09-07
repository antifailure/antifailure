package volume

import "strings"

// FindRelname resolves an unqualified relation name against the profile.
//
// The lock sampler reports pg_class.relname, which carries no schema, and two
// schemas may hold a table of the same name. One match is the answer, none is
// no answer, and more than one is REFUSED by name rather than resolved by
// taking the first: the entire value of a number attached to a table is that
// it belongs to the table somebody is looking at, and a silently chosen
// namesake is the worst possible way to be wrong about that.
//
// The second result is the reason it could not be resolved, empty on success,
// which is the only way a caller can tell "production holds nothing in it"
// from "nothing here knows what production holds in it".
func (p Profile) FindRelname(rel string) (Table, string) {
	if strings.Contains(rel, ".") {
		if t, ok := p.Find(rel); ok {
			return t, ""
		}
		return Table{}, "the volume profile does not name " + rel
	}
	var found []Table
	for _, t := range p.Tables {
		if t.Name == rel || strings.HasSuffix(t.Name, "."+rel) {
			found = append(found, t)
		}
	}
	switch len(found) {
	case 0:
		return Table{}, "the volume profile does not name a table called " + rel
	case 1:
		if !found[0].Analyzed {
			return Table{}, found[0].Name +
				" has never been analyzed on production, so the profile carries no row count for it"
		}
		return found[0], ""
	}
	names := make([]string, 0, len(found))
	for _, t := range found {
		names = append(names, t.Name)
	}
	return Table{}, "more than one table is called " + rel + " on production, " + list(names) +
		", and nothing here says which of them this is"
}
