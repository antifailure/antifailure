// Command eventcheck proves the event stream keeps the shape it promised.
//
// The stability page used to say only that types are added as features land.
// That is true and it is not a promise: it says what happens when the catalog
// grows and nothing at all about what happens when it shrinks. Nothing stopped
// a type being deleted, and nothing stopped a field leaving the envelope. Both
// regenerate cleanly, because the schema is generated from the Go type and the
// catalog, so removing either produces a green diff and a consumer that stops
// working on the next upgrade.
//
// So the set of types and the envelope are registered, and this refuses to let
// a registered one go.
//
// It also closes the direction nothing was watching. A type with no entry in
// typeDocs is not in AllTypes, so it is not in the generated schema and not on
// the reference page, and every existing check walks AllTypes and therefore
// cannot see it. An event nobody can look up is one a consumer has to guess at
// from its prefix, which is exactly what the catalog exists to prevent.
//
// The other direction, a documented type nothing emits, is checked by
// engine/internal/events/emitters_test.go, which parses the engine for emit
// call sites and carries a written reason for each type that has none.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

const (
	sourcePath   = "engine/internal/events/event.go"
	schemaPath   = "schemas/events.v1.json"
	registerPath = "engine/internal/events/stream.register.json"
)

const registerNote = "The event stream as version 1 promised it. Every type listed here still " +
	"exists and every envelope field still has the shape recorded, or tools/eventcheck fails. " +
	"Appended to by 'go run ./tools/eventcheck -freeze .' and never rewritten by it: a type is " +
	"added as a feature lands, and nothing takes one away inside a major version."

type register struct {
	Note     string   `json:"note"`
	Envelope []field  `json:"envelope"`
	Types    []string `json:"types"`
}

// field is one envelope property, recorded in the terms a consumer would break
// on: the wire name, what kind of value arrives, whether it is always there,
// and for a closed set, the values it can hold.
type field struct {
	Name     string   `json:"field"`
	Kind     string   `json:"kind"`
	Required bool     `json:"required"`
	Values   []string `json:"values,omitempty"`
}

func main() {
	freeze := flag.Bool("freeze", false, "record types and fields that are not registered yet")
	flag.Parse()
	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	problems, types, err := run(root, *freeze)
	if err != nil {
		fmt.Fprintln(os.Stderr, "eventcheck:", err)
		os.Exit(2)
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "eventcheck:", p)
		}
		os.Exit(1)
	}
	fmt.Printf("eventcheck: %d event types, every one documented and every one still on the stream\n",
		types)
}

func run(root string, freeze bool) ([]string, int, error) {
	declared, documented, err := catalogSource(filepath.Join(root, sourcePath))
	if err != nil {
		return nil, 0, err
	}
	if len(declared) == 0 {
		return nil, 0, fmt.Errorf("no Type constants in %s, so this check is measuring nothing",
			sourcePath)
	}

	published, envelope, err := readSchema(filepath.Join(root, schemaPath))
	if err != nil {
		return nil, 0, err
	}

	var problems []string

	// A type the engine can emit that nothing describes. It is absent from
	// AllTypes, from the generated schema and from the reference page, and
	// every other check in this repository walks AllTypes, so this is the one
	// place it can be seen.
	var undocumented, orphaned []string
	for name, value := range declared {
		if !documented[name] {
			undocumented = append(undocumented, fmt.Sprintf("%s (%s)", value, name))
		}
	}
	for name := range documented {
		if _, ok := declared[name]; !ok {
			orphaned = append(orphaned, name)
		}
	}
	sort.Strings(undocumented)
	sort.Strings(orphaned)
	for _, item := range undocumented {
		problems = append(problems, fmt.Sprintf(
			"%s is a type the engine can emit and typeDocs does not describe it. Add a "+
				"sentence there. Without one it is missing from AllTypes, from "+
				"schemas/events.v1.json and from the reference page, so a consumer receiving "+
				"it has nothing to look it up in and has to guess from the prefix.", item))
	}
	for _, name := range orphaned {
		problems = append(problems, fmt.Sprintf(
			"typeDocs describes %s and no constant declares it, so the reference page "+
				"documents an event that cannot happen.", name))
	}

	// The committed schema is what the runner and the control plane read. A
	// stale one describes a stream nobody is sending.
	byValue := map[string]bool{}
	for _, value := range declared {
		byValue[value] = true
	}
	var missingFromSchema, extraInSchema []string
	for value := range byValue {
		if !published[value] {
			missingFromSchema = append(missingFromSchema, value)
		}
	}
	for value := range published {
		if !byValue[value] {
			extraInSchema = append(extraInSchema, value)
		}
	}
	sort.Strings(missingFromSchema)
	sort.Strings(extraInSchema)
	for _, value := range append(missingFromSchema, extraInSchema...) {
		problems = append(problems, fmt.Sprintf(
			"%s is in one of %s and %s and not the other. Regenerate with "+
				"'go test ./internal/events -update-schema'.", value, sourcePath, schemaPath))
	}

	reg, err := readRegister(filepath.Join(root, registerPath))
	if err != nil {
		return nil, 0, err
	}

	registered := map[string]bool{}
	for _, t := range reg.Types {
		registered[t] = true
	}
	var removed, unregistered []string
	for _, t := range reg.Types {
		if !byValue[t] {
			removed = append(removed, t)
		}
	}
	for value := range byValue {
		if !registered[value] {
			unregistered = append(unregistered, value)
		}
	}
	sort.Strings(removed)
	sort.Strings(unregistered)

	for _, t := range removed {
		problems = append(problems, fmt.Sprintf(
			"%s was on the stream in version 1 and is gone. A consumer filtering on it now "+
				"receives nothing and cannot tell that from a quiet system. Removing a type "+
				"costs a major version; deprecate it and stop emitting it instead.", t))
	}

	problems = append(problems, envelopeProblems(reg.Envelope, envelope)...)

	if freeze {
		next, ferr := frozen(reg, byValue, envelope)
		if ferr != nil {
			return nil, 0, ferr
		}
		if werr := writeRegister(filepath.Join(root, registerPath), next); werr != nil {
			return nil, 0, werr
		}
	} else {
		for _, t := range unregistered {
			problems = append(problems, fmt.Sprintf(
				"%s is on the stream and not in %s. Run 'just generate'. The register is what "+
					"proves the type was never taken away later.", t, registerPath))
		}
		for _, f := range envelope {
			if findField(reg.Envelope, f.Name) == nil {
				problems = append(problems, fmt.Sprintf(
					"the envelope carries %q and %s does not record it. Run 'just generate'.",
					f.Name, registerPath))
			}
		}
	}

	return problems, len(byValue), nil
}

// envelopeProblems reports a field that has gone or changed under a consumer.
//
// Three ways it breaks one, and they are worth naming separately because the
// fix differs. A field that is gone breaks every reader of it. A field whose
// kind changed breaks a typed decoder while a loose one keeps working, which is
// the worse failure: it ships. A field that was always present and is now
// optional breaks a reader that never had a reason to check.
func envelopeProblems(recorded, current []field) []string {
	var out []string
	for _, want := range recorded {
		got := findField(current, want.Name)
		if got == nil {
			out = append(out, fmt.Sprintf(
				"the envelope no longer carries %q. Every event in version 1 carries it, so "+
					"removing it costs a major version.", want.Name))
			continue
		}
		if got.Kind != want.Kind {
			out = append(out, fmt.Sprintf(
				"%q was %s in version 1 and is %s now. A consumer decoding it into a typed "+
					"field stops parsing the whole event.", want.Name, want.Kind, got.Kind))
		}
		if want.Required && !got.Required {
			out = append(out, fmt.Sprintf(
				"%q was on every event in version 1 and is optional now. A reader that never "+
					"had a reason to check for it breaks on the first event without it.",
				want.Name))
		}
		have := map[string]bool{}
		for _, v := range got.Values {
			have[v] = true
		}
		for _, v := range want.Values {
			if !have[v] {
				out = append(out, fmt.Sprintf(
					"%q could be %q in version 1 and cannot now. Narrowing a closed set is a "+
						"break for whoever was matching on the value that went.", want.Name, v))
			}
		}
	}
	sort.Strings(out)
	return out
}

func findField(in []field, name string) *field {
	for i := range in {
		if in[i].Name == name {
			return &in[i]
		}
	}
	return nil
}

// frozen returns the register with anything new appended and nothing changed.
//
// Append only on purpose. A freeze that rewrote an existing entry would erase
// the record of what version 1 promised, which is the only thing that makes
// this a check rather than a description of today.
func frozen(reg register, types map[string]bool, envelope []field) (register, error) {
	out := register{Note: registerNote, Envelope: reg.Envelope, Types: reg.Types}
	known := map[string]bool{}
	for _, t := range out.Types {
		known[t] = true
	}
	for t := range types {
		if !known[t] {
			out.Types = append(out.Types, t)
		}
	}
	sort.Strings(out.Types)

	for _, f := range envelope {
		if findField(out.Envelope, f.Name) == nil {
			out.Envelope = append(out.Envelope, f)
		}
	}
	sort.Slice(out.Envelope, func(i, j int) bool { return out.Envelope[i].Name < out.Envelope[j].Name })
	return out, nil
}

func writeRegister(path string, reg register) error {
	body, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o644)
}

func readRegister(path string) (register, error) {
	var reg register
	body, err := os.ReadFile(path)
	if err != nil {
		return reg, err
	}
	if err := json.Unmarshal(body, &reg); err != nil {
		return reg, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(reg.Types) == 0 || len(reg.Envelope) == 0 {
		return reg, fmt.Errorf("%s records no types or no envelope, so this check is measuring "+
			"nothing", path)
	}
	return reg, nil
}

// catalogSource reads the Type constants and the typeDocs keys.
//
// Parsed rather than grepped, for the reason errcheck gives: a grep for a type
// name matches the constant, its documentation, every display that switches on
// it and every sink that translates it, so it would report everything as
// present and prove nothing.
func catalogSource(path string) (map[string]string, map[string]bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, nil, err
	}

	declared := map[string]string{}
	documented := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		if ident, isType := spec.Type.(*ast.Ident); isType && ident.Name == "Type" {
			for i, name := range spec.Names {
				if i >= len(spec.Values) {
					continue
				}
				if value, verr := stringLiteral(spec.Values[i]); verr == nil {
					declared[name.Name] = value
				}
			}
			return true
		}
		if len(spec.Names) == 0 || spec.Names[0].Name != "typeDocs" {
			return true
		}
		for _, value := range spec.Values {
			lit, isLit := value.(*ast.CompositeLit)
			if !isLit {
				continue
			}
			for _, elt := range lit.Elts {
				kv, isKV := elt.(*ast.KeyValueExpr)
				if !isKV {
					continue
				}
				if key, isIdent := kv.Key.(*ast.Ident); isIdent {
					documented[key.Name] = true
				}
			}
		}
		return true
	})
	return declared, documented, nil
}

func stringLiteral(e ast.Expr) (string, error) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", fmt.Errorf("not a string literal")
	}
	return strconv.Unquote(lit.Value)
}

// readSchema returns the type values the committed schema publishes and the
// envelope it declares.
//
// The schema rather than the Go struct, because the schema is what a consumer
// outside this module reads. Reading the struct would check the code against
// itself and miss a committed artifact that has drifted from it, which is the
// only way this can be wrong in a way anybody notices.
func readSchema(path string) (map[string]bool, []field, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var doc struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(doc.Properties) == 0 {
		return nil, nil, fmt.Errorf("%s declares no properties; has its shape changed?", path)
	}

	required := map[string]bool{}
	for _, r := range doc.Required {
		required[r] = true
	}

	types := map[string]bool{}
	var envelope []field
	for name, raw := range doc.Properties {
		var prop struct {
			Type  string   `json:"type"`
			Enum  []string `json:"enum"`
			OneOf []struct {
				Const string `json:"const"`
			} `json:"oneOf"`
		}
		if err := json.Unmarshal(raw, &prop); err != nil {
			return nil, nil, fmt.Errorf("parse %s in %s: %w", name, path, err)
		}
		f := field{Name: name, Kind: prop.Type, Required: required[name]}
		switch {
		case prop.Type != "":
		case len(prop.Enum) > 0:
			f.Kind = "enum"
			f.Values = append(f.Values, prop.Enum...)
		case len(prop.OneOf) > 0:
			f.Kind = "enum"
			for _, one := range prop.OneOf {
				if name == "type" {
					types[one.Const] = true
					continue
				}
				f.Values = append(f.Values, one.Const)
			}
		default:
			return nil, nil, fmt.Errorf("%s in %s declares neither a type nor a closed set, so "+
				"this cannot say what shape it is", name, path)
		}
		sort.Strings(f.Values)
		envelope = append(envelope, f)
	}
	sort.Slice(envelope, func(i, j int) bool { return envelope[i].Name < envelope[j].Name })

	if len(types) == 0 {
		return nil, nil, fmt.Errorf("%s lists no event types under the type property", path)
	}
	return types, envelope, nil
}
