package manifest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The guarantee two doc comments already make, finally enforced.
//
// engine/pkg/schema/manifest.go opens with "a test validates real manifests
// against both so the two cannot drift: a field added to the schema and not
// here, or here and not there, fails the build", and reference/manifest.md
// repeats it for users. Before this file, nothing in the repository read
// schemas/manifest.v1.json at all: not a test, not a tool, not the engine.
// The two could drift freely and the only symptom would be a manifest that
// the published schema accepts and the engine rejects, or the reverse, which
// is a bad afternoon for somebody whose editor validates against the schema.
//
// What this compares is the field NAMES on each side, walked structurally. It
// deliberately does not compare types: the JSON Schema says "integer" where Go
// says int64 and enumerates values Go models as a string constant, and a test
// that tried to unify those would be a test people delete. Names are where
// drift actually happens, because drift happens when somebody adds a field to
// one file and forgets the other.

// schemaDoc is the part of a JSON Schema this walks.
type schemaDoc struct {
	Properties           map[string]*schemaDoc `json:"properties"`
	Items                *schemaDoc            `json:"items"`
	Ref                  string                `json:"$ref"`
	Defs                 map[string]*schemaDoc `json:"$defs"`
	AdditionalProperties json.RawMessage       `json:"additionalProperties"`
}

func loadSchema(t *testing.T) *schemaDoc {
	t.Helper()
	// Four levels up from engine/internal/manifest to the repository root.
	path := filepath.Join("..", "..", "..", "schemas", "manifest.v1.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "the schema this repository calls its source of truth is missing")

	var doc schemaDoc
	require.NoError(t, json.Unmarshal(raw, &doc))
	return &doc
}

// resolve follows a $ref to the definition it names.
func (d *schemaDoc) resolve(root *schemaDoc) *schemaDoc {
	if d == nil || d.Ref == "" {
		return d
	}
	name := strings.TrimPrefix(d.Ref, "#/$defs/")
	return root.Defs[name]
}

// goFields returns the JSON field names a Go struct declares, and the nested
// struct each one leads to.
func goFields(t reflect.Type) map[string]reflect.Type {
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	out := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			continue
		}
		out[name] = f.Type
	}
	return out
}

// reporter is what compare reports drift to.
//
// An interface rather than *testing.T so the negative control below can run
// the same code and collect the failures instead of aborting the process.
// assert rather than require throughout, because require's FailNow calls
// Goexit, which is fatal outside a real test goroutine.
type reporter interface {
	Errorf(format string, args ...any)
}

// recorder collects failures instead of failing.
type recorder struct{ failures []string }

func (r *recorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, format)
}

// compare walks one object in both documents.
func compare(t reporter, where string, goType reflect.Type, node, root *schemaDoc) {
	node = node.resolve(root)
	if node == nil {
		return
	}
	// A map-valued object (attributes, source_config) has no named properties
	// on either side, so there is nothing to compare.
	if len(node.Properties) == 0 {
		return
	}

	fields := goFields(goType)
	if fields == nil {
		return
	}

	var inSchemaOnly, inGoOnly []string
	for name := range node.Properties {
		if _, ok := fields[name]; !ok {
			inSchemaOnly = append(inSchemaOnly, name)
		}
	}
	for name := range fields {
		if _, ok := node.Properties[name]; !ok {
			inGoOnly = append(inGoOnly, name)
		}
	}
	sort.Strings(inSchemaOnly)
	sort.Strings(inGoOnly)

	assert.Emptyf(t, inSchemaOnly,
		"%s: the JSON schema declares %v and the Go type does not. "+
			"Add the field to engine/pkg/schema/manifest.go.", where, inSchemaOnly)
	assert.Emptyf(t, inGoOnly,
		"%s: the Go type declares %v and the JSON schema does not. "+
			"Add the property to schemas/manifest.v1.json.", where, inGoOnly)

	for name, child := range node.Properties {
		goChild := fields[name]
		child = child.resolve(root)
		if child == nil {
			continue
		}
		// An array of objects: descend through items on the schema side and
		// through the element type on the Go side.
		if child.Items != nil {
			compare(t, where+"."+name+"[]", goChild, child.Items, root)
			continue
		}
		compare(t, where+"."+name, goChild, child, root)
	}
}

func TestSchemaAndGoTypesDoNotDrift(t *testing.T) {
	root := loadSchema(t)
	compare(t, "manifest", reflect.TypeOf(schema.Manifest{}), root, root)
}

// TestSchemaDriftTestCanActuallyFail is the negative control.
//
// A comparison that silently walks nothing passes for every input, which is
// exactly how the createdSet.matches bug in the conformance suite turned a
// filter into a switch that disabled the check entirely. So: give it a Go
// type missing a field the schema declares, and require that it notices.
func TestSchemaDriftTestCanActuallyFail(t *testing.T) {
	root := loadSchema(t)

	// Persona without Login, which schemas/manifest.v1.json declares.
	type persona struct {
		Name  string `json:"name"`
		Email string `json:"email,omitempty"`
		Role  string `json:"role,omitempty"`
		MFA   bool   `json:"mfa,omitempty"`
	}

	var got recorder
	compare(&got, "persona", reflect.TypeOf(persona{}), root.Defs["persona"], root)
	require.NotEmpty(t, got.failures,
		"the drift check passed a type that is missing a field the schema declares, "+
			"so it would pass anything")

	// And it passes the real type, so the control is measuring drift rather
	// than simply always failing.
	var clean recorder
	compare(&clean, "persona", reflect.TypeOf(schema.Persona{}), root.Defs["persona"], root)
	require.Empty(t, clean.failures)
}

func TestEveryDeclaredKeyIsSuggestible(t *testing.T) {
	// knownKeys drives the "did you mean" on a misspelled key. A key in the
	// schema and not in that list gives somebody a typo with no suggestion,
	// which is a small thing that happens at the worst moment.
	root := loadSchema(t)

	declared := map[string]bool{}
	var walk func(d *schemaDoc)
	walk = func(d *schemaDoc) {
		d = d.resolve(root)
		if d == nil {
			return
		}
		for name, child := range d.Properties {
			declared[name] = true
			if child = child.resolve(root); child == nil {
				continue
			}
			if child.Items != nil {
				walk(child.Items)
			}
			walk(child)
		}
	}
	walk(root)

	raw, err := os.ReadFile(filepath.Join("manifest.go"))
	require.NoError(t, err)
	source := string(raw)
	start := strings.Index(source, "var knownKeys = []string{")
	require.Positive(t, start, "knownKeys is not where this test expects it")
	end := strings.Index(source[start:], "\n}")
	require.Positive(t, end)
	list := source[start : start+end]

	var missing []string
	for name := range declared {
		if !strings.Contains(list, `"`+name+`"`) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	require.Emptyf(t, missing,
		"these keys are in the schema and not in knownKeys, so a typo of them "+
			"gets no suggestion: %v", missing)
}
