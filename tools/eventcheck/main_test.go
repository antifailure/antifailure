package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each direction this is supposed to refuse gets a tree that breaks exactly
// that one thing, because a check nobody has watched say no is a check nobody
// knows the shape of.

const sourceGo = `package events

type Type string

const (
	EnvReady Type = "env.ready"
	DBReset  Type = "db.reset"
)

var typeDocs = map[Type]string{
	EnvReady: "An environment is ready.",
	DBReset:  "A branch was reset.",
}
`

const schemaJSON = `{
  "required": ["id", "seq", "type", "level"],
  "properties": {
    "id": {"type": "string"},
    "seq": {"type": "integer"},
    "msg": {"type": "string"},
    "level": {"enum": ["debug", "info", "warn", "error"]},
    "type": {"oneOf": [{"const": "env.ready"}, {"const": "db.reset"}]}
  }
}`

const registerJSON = `{
  "envelope": [
    {"field": "id", "kind": "string", "required": true},
    {"field": "seq", "kind": "integer", "required": true},
    {"field": "msg", "kind": "string", "required": false},
    {"field": "level", "kind": "enum", "required": true,
      "values": ["debug", "error", "info", "warn"]},
    {"field": "type", "kind": "enum", "required": true}
  ],
  "types": ["db.reset", "env.ready"]
}`

func tree(t *testing.T, source, schema, reg string) string {
	t.Helper()
	root := t.TempDir()
	for path, body := range map[string]string{
		filepath.Join("engine", "internal", "events", "event.go"):             source,
		filepath.Join("engine", "internal", "events", "stream.register.json"): reg,
		filepath.Join("schemas", "events.v1.json"):                            schema,
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func problems(t *testing.T, root string) string {
	t.Helper()
	got, _, err := run(root, false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.Join(got, "\n")
}

func TestAStreamThatKeptItsPromiseIsQuiet(t *testing.T) {
	if got := problems(t, tree(t, sourceGo, schemaJSON, registerJSON)); got != "" {
		t.Fatalf("an unchanged stream reported: %s", got)
	}
}

// The promise itself. Everything else about a deletion regenerates cleanly,
// which is why nothing was catching this.
func TestARemovedTypeIsRefused(t *testing.T) {
	source := strings.NewReplacer(
		"\tDBReset  Type = \"db.reset\"\n", "",
		"\tDBReset:  \"A branch was reset.\",\n", "").Replace(sourceGo)
	schema := strings.Replace(schemaJSON, `, {"const": "db.reset"}`, "", 1)
	got := problems(t, tree(t, source, schema, registerJSON))
	if !strings.Contains(got, "db.reset was on the stream in version 1 and is gone") {
		t.Fatalf("a removed type was not reported: %s", got)
	}
}

func TestAFieldThatLeavesTheEnvelopeIsRefused(t *testing.T) {
	schema := strings.Replace(schemaJSON, `    "msg": {"type": "string"},`+"\n", "", 1)
	got := problems(t, tree(t, sourceGo, schema, registerJSON))
	if !strings.Contains(got, `the envelope no longer carries "msg"`) {
		t.Fatalf("a removed field was not reported: %s", got)
	}
}

func TestAFieldThatChangesTypeIsRefused(t *testing.T) {
	schema := strings.Replace(schemaJSON, `"seq": {"type": "integer"}`, `"seq": {"type": "string"}`, 1)
	got := problems(t, tree(t, sourceGo, schema, registerJSON))
	if !strings.Contains(got, `"seq" was integer in version 1 and is string now`) {
		t.Fatalf("a changed type was not reported: %s", got)
	}
}

func TestAFieldThatStopsBeingRequiredIsRefused(t *testing.T) {
	schema := strings.Replace(schemaJSON, `"required": ["id", "seq"`, `"required": ["seq"`, 1)
	got := problems(t, tree(t, sourceGo, schema, registerJSON))
	if !strings.Contains(got, `"id" was on every event in version 1 and is optional now`) {
		t.Fatalf("a field that stopped being required was not reported: %s", got)
	}
}

// A closed set that loses a member breaks whoever was matching on the member
// that went, which is not obvious from a diff that only adds a line.
func TestAClosedSetThatNarrowsIsRefused(t *testing.T) {
	schema := strings.Replace(schemaJSON, `"debug", "info", "warn", "error"`, `"debug", "info", "warn"`, 1)
	got := problems(t, tree(t, sourceGo, schema, registerJSON))
	if !strings.Contains(got, `"level" could be "error" in version 1 and cannot now`) {
		t.Fatalf("a narrowed enum was not reported: %s", got)
	}
}

// The direction every other check in the repository is blind to: a type with no
// typeDocs entry is not in AllTypes, so nothing that walks AllTypes can see it.
func TestAnUndocumentedTypeIsRefused(t *testing.T) {
	source := strings.Replace(sourceGo, "\tDBReset:  \"A branch was reset.\",\n", "", 1)
	got := problems(t, tree(t, source, schemaJSON, registerJSON))
	if !strings.Contains(got, "db.reset (DBReset) is a type the engine can emit and typeDocs does not describe it") {
		t.Fatalf("an undocumented type was not reported: %s", got)
	}
}

func TestADocumentedTypeThatNoConstantDeclaresIsRefused(t *testing.T) {
	source := strings.Replace(sourceGo, "\tDBReset  Type = \"db.reset\"\n", "", 1)
	got := problems(t, tree(t, source, schemaJSON, registerJSON))
	if !strings.Contains(got, "typeDocs describes DBReset and no constant declares it") {
		t.Fatalf("an orphaned description was not reported: %s", got)
	}
}

// A new type has to be registered before it is trusted, and freezing is the
// only thing that writes the register.
func TestANewTypeMustBeRegistered(t *testing.T) {
	source := strings.Replace(sourceGo, "\tDBReset  Type", "\tDBBranched Type = \"db.branched\"\n\tDBReset  Type", 1)
	source = strings.Replace(source, "\tDBReset:  ", "\tDBBranched: \"A branch is ready.\",\n\tDBReset:  ", 1)
	schema := strings.Replace(schemaJSON, `{"const": "env.ready"}`, `{"const": "env.ready"}, {"const": "db.branched"}`, 1)

	root := tree(t, source, schema, registerJSON)
	if got := problems(t, root); !strings.Contains(got, "db.branched is on the stream and not in") {
		t.Fatalf("an unregistered type was not reported: %s", got)
	}
	if _, _, err := run(root, true); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if got := problems(t, root); got != "" {
		t.Fatalf("freezing did not settle it: %s", got)
	}
	body, err := os.ReadFile(filepath.Join(root, "engine", "internal", "events", "stream.register.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "db.reset") {
		t.Fatal("freezing dropped an already registered type, so the register is not append only")
	}
}

// A tree this cannot read must fail loudly rather than reporting agreement.
func TestAnUnreadableTreeIsAnErrorRatherThanAPass(t *testing.T) {
	if _, _, err := run(tree(t, "package events\n", schemaJSON, registerJSON), false); err == nil {
		t.Fatal("a source file with no types passed")
	}
	if _, _, err := run(tree(t, sourceGo, `{"properties":{}}`, registerJSON), false); err == nil {
		t.Fatal("a schema with no properties passed")
	}
	if _, _, err := run(tree(t, sourceGo, schemaJSON, `{"types":[],"envelope":[]}`), false); err == nil {
		t.Fatal("an empty register passed")
	}
}
