package events_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/events"
)

var updateSchema = flag.Bool("update-schema", false, "rewrite schemas/events.v1.json")

// schemaPath is the committed artifact. It lives outside the engine module on
// purpose: the runner and the control plane speak the same envelope and cannot
// import an internal package to find out what it is.
const schemaPath = "../../../schemas/events.v1.json"

// buildSchema renders the envelope from the Go type and the catalog.
//
// Generated rather than written, because the alternative is a schema that
// agrees with the code on the day it is committed. The catalog gains a type
// most weeks; a hand written enum would be wrong by the second one, and wrong
// quietly, since nothing validates against a schema nobody regenerates.
func buildSchema() map[string]any {
	oneOf := make([]any, 0, len(events.AllTypes()))
	for _, t := range events.AllTypes() {
		oneOf = append(oneOf, map[string]any{
			"const":       string(t),
			"description": events.Describe(t),
		})
	}

	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     "https://antifailure.dev/schemas/events.v1.json",
		"title":   "Antifailure event",
		// The sentence here used to say the envelope was identical across the
		// engine, the runner and the control plane. It is not, and the comment
		// on the Go type has said so since somebody checked: four of the eight
		// names differ on the control plane's side and two have no counterpart
		// at all. This is the published artifact, so it was the copy of that
		// claim a consumer would have built against.
		"description": "One thing that happened, as it appears on the engine's event stream " +
			"and in its NDJSON log. This is the engine's envelope: the control plane receives " +
			"a translated form, with different names for four of these fields and no " +
			"counterpart for two of them. Within version 1 a type listed here is never removed " +
			"and never changes meaning, and a field here is never removed, never changes type " +
			"and never becomes optional. Both may gain new members, so ignore a type or a " +
			"field you were not built to understand rather than refusing the event. " +
			"Generated from the Go type and the event catalog by " +
			"go test ./internal/events -update-schema.",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"id", "ts", "seq", "type", "level"},
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"minLength":   1,
				"description": "Unique for this event.",
			},
			"ts": map[string]any{
				"type":        "string",
				"format":      "date-time",
				"description": "When it happened, from the engine's injected clock.",
			},
			"env": map[string]any{
				"type": "string",
				"description": "The environment identifier. Absent on engine wide events, " +
					"which share the empty environment's sequence.",
			},
			"seq": map[string]any{
				"type":    "integer",
				"minimum": 0,
				"description": "A monotonic counter per environment, so a consumer can order " +
					"events and notice a gap.",
			},
			"type": map[string]any{
				"description": "What happened. Every value in the engine's catalog is listed here, " +
					"so a consumer can reject an event it was not built to understand rather " +
					"than guessing from the prefix.",
				"oneOf": oneOf,
			},
			"level": map[string]any{
				"description": "Classifies the event for display and filtering.",
				"enum":        []any{"debug", "info", "warn", "error"},
			},
			"msg": map[string]any{
				"type": "string",
				"description": "A short human readable summary, already redacted, like " +
					"everything else that reaches a log or an artifact.",
			},
			"data": map[string]any{
				"type":        "object",
				"description": "The type specific payload. Always an object, never a scalar or a list.",
			},
		},
	}
}

func renderSchema(t *testing.T) []byte {
	t.Helper()
	out, err := json.MarshalIndent(buildSchema(), "", "  ")
	require.NoError(t, err)
	return append(out, '\n')
}

// The committed schema is what other services read, so it has to be in the
// tree and it has to match what the code produces. Regenerate with:
//
//	go test ./internal/events -update-schema
func TestEventSchemaIsCommittedAndCurrent(t *testing.T) {
	want := renderSchema(t)
	if *updateSchema {
		require.NoError(t, os.MkdirAll(filepath.Dir(schemaPath), 0o755))
		require.NoError(t, os.WriteFile(schemaPath, want, 0o644))
		return
	}

	got, err := os.ReadFile(schemaPath)
	require.NoError(t, err,
		"schemas/events.v1.json is missing. Regenerate with: go test ./internal/events -update-schema")
	require.Equal(t, string(want), string(got),
		"schemas/events.v1.json is out of date. Regenerate with: go test ./internal/events -update-schema")
}

// The schema describes the Go type or it describes nothing. This walks the
// struct rather than trusting the generator, so a field added to Event with no
// entry in the schema fails here instead of shipping as an event nobody
// downstream can validate.
func TestSchemaPropertiesMatchTheGoType(t *testing.T) {
	schema := buildSchema()
	props := schema["properties"].(map[string]any)

	required := map[string]bool{}
	for _, r := range schema["required"].([]any) {
		required[r.(string)] = true
	}

	seen := map[string]bool{}
	rt := reflect.TypeOf(events.Event{})
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		tag := field.Tag.Get("json")
		require.NotEmpty(t, tag, "%s has no json tag, so it has no wire name", field.Name)

		name := strings.Split(tag, ",")[0]
		seen[name] = true

		require.Contains(t, props, name,
			"Event.%s serialises as %q and the schema does not describe it", field.Name, name)

		// A field with omitempty can be absent, so it cannot be required, and
		// a field without it is always written, so it must be. Getting this
		// backwards produces a schema that rejects valid events, or one that
		// accepts an event with no identifier.
		optional := strings.Contains(tag, ",omitempty")
		require.Equal(t, !optional, required[name],
			"Event.%s is optional=%v in Go and required=%v in the schema",
			field.Name, optional, required[name])
	}

	for name := range props {
		require.True(t, seen[name],
			"the schema describes %q and no field of Event serialises to it", name)
	}
}

// Every type in the catalog reaches the schema with its description. A type
// with no description already fails the catalog's own completeness test; this
// checks the other direction, that nothing is dropped on the way out.
func TestSchemaListsEveryEventTypeWithItsDescription(t *testing.T) {
	props := buildSchema()["properties"].(map[string]any)
	listed := map[string]string{}
	for _, entry := range props["type"].(map[string]any)["oneOf"].([]any) {
		e := entry.(map[string]any)
		listed[e["const"].(string)] = e["description"].(string)
	}

	require.Len(t, listed, len(events.AllTypes()))
	for _, tp := range events.AllTypes() {
		require.NotEmpty(t, listed[string(tp)], "%s reached the schema with no description", tp)
	}
}

// A real event matches the shape the schema declares. Not a full JSON Schema
// implementation, which would be a dependency for one test, but the two
// properties that actually break a consumer: nothing is written that the
// schema forbids, and every required field is present.
func TestAMarshalledEventMatchesTheDeclaredShape(t *testing.T) {
	bus := events.NewBus(clock.NewFake(epoch))
	e := bus.Info("pr-482", events.EnvReady, "ready", events.F("url", "http://127.0.0.1:8080"))
	require.NoError(t, bus.Close())

	raw, err := json.Marshal(e)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	schema := buildSchema()
	props := schema["properties"].(map[string]any)
	for name := range got {
		require.Contains(t, props, name,
			"a real event carries %q and the schema forbids unknown properties", name)
	}
	for _, r := range schema["required"].([]any) {
		require.Contains(t, got, r.(string), "a real event is missing a required property")
	}
}
