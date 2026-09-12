package schema

import "strings"

// ScopedName is where a service's own value is stored: the service's name in
// capitals with hyphens as underscores, two underscores, then the name. The
// storage service's DATABASE_URL is STORAGE__DATABASE_URL.
//
// Spelled so that every source can hold it. A shell cannot export a name with a
// slash or a dot in it and the .env parser refuses one, so the separator is
// made of the characters a variable name already uses, and every registered
// secret store is asked for it through the Lookup it already implements.
//
// The price is that two different pairs can spell one name: the URL of a
// service called storage-db, and the DB__URL of one called storage. Validation
// refuses exactly that rather than letting one service read the other's value.
//
// One function for the resolver and the validator both, because a spelling
// written twice is two spellings, and the day they differ the validator
// approves a name the resolver never asks for.
func ScopedName(service, name string) string {
	return strings.ToUpper(strings.ReplaceAll(service, "-", "_")) + "__" + name
}

// StoredName is the name a declaration's value is looked up under: from when it
// is set and the variable's own name otherwise, spelled as the service's own
// when the variable is scoped to that service. Empty for a literal, which is
// not looked up at all.
func (e EnvVar) StoredName(service string) string {
	if e.Value != "" {
		return ""
	}
	name := e.Name
	if e.From != "" {
		name = e.From
	}
	if e.Scope == ScopeService {
		return ScopedName(service, name)
	}
	return name
}
