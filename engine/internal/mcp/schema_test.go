package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func sampleSchema() *Schema {
	return &Schema{
		Type:     "object",
		Required: []string{"name"},
		Properties: map[string]*Schema{
			"name":    {Type: "string", MaxLength: 32, MinLength: 1},
			"mode":    {Type: "string", Enum: []string{"fast", "thorough"}},
			"count":   {Type: "integer", HasMin: true, Minimum: 1, HasMax: true, Maximum: 10},
			"ratio":   {Type: "number", HasMin: true, Minimum: 0, HasMax: true, Maximum: 1},
			"enabled": {Type: "boolean"},
			"tags": {
				Type: "array", MaxItems: 3,
				Items: &Schema{Type: "string", MaxLength: 8},
			},
			"ref": {Type: "string", MaxLength: 16, Pattern: `[a-z]+`},
		},
	}
}

func validateJSON(t *testing.T, body string) (map[string]any, *Fault) {
	t.Helper()
	return validateArguments(sampleSchema(), json.RawMessage(body))
}

func TestValidate_RejectsUnknownFields(t *testing.T) {
	t.Parallel()
	// The published schema says additionalProperties is false, so the
	// validator has to agree with it. A schema that promises a closed object
	// and a validator that accepts anything is worse than no schema, because
	// the caller has been told a promise the server does not keep.
	_, fault := validateJSON(t, `{"name":"x","is_admin":true}`)

	require.NotNil(t, fault)
	require.Equal(t, FaultUnknownField, fault.Code)
	require.Contains(t, fault.Detail, "is_admin")
}

func TestValidate_UnknownFieldIsReportedBeforeOtherProblems(t *testing.T) {
	t.Parallel()
	// Both wrong: an unknown field and a missing required one. The unknown
	// field wins, because a caller carrying a field this server never heard
	// of is usually built against a different contract, and pointing it at
	// the required field would send it to fix the wrong thing.
	_, fault := validateJSON(t, `{"skip_sanitization":true}`)

	require.NotNil(t, fault)
	require.Equal(t, FaultUnknownField, fault.Code)
}

func TestValidate_RejectsTheSafetyWeakeningFieldsThatDoNotExist(t *testing.T) {
	t.Parallel()
	// The schemas make these inexpressible rather than refusing them by name.
	// There is no allow list of forbidden words anywhere in this package: a
	// field is refused because it was never declared, which is why a new way
	// to spell "turn the firewall off" cannot be smuggled past a filter.
	for _, field := range []string{
		"disable_firewall", "skip_sanitization", "allow_production_network",
		"ignore_lock_threshold", "database_url", "is_admin", "project_id_override",
	} {
		_, fault := validateJSON(t, `{"name":"x","`+field+`":"anything"}`)
		require.NotNil(t, fault, "the field %q must not be accepted", field)
		require.Equal(t, FaultUnknownField, fault.Code, "field %q", field)
	}
}

func TestValidate_RejectsOversizedArguments(t *testing.T) {
	t.Parallel()
	body := `{"name":"` + strings.Repeat("A", maxArgumentBytes) + `"}`
	_, fault := validateArguments(sampleSchema(), json.RawMessage(body))

	require.NotNil(t, fault)
	require.Equal(t, FaultArgumentTooLarge, fault.Code)
}

func TestValidate_RejectsAnOversizedStringWithinTheByteBudget(t *testing.T) {
	t.Parallel()
	// Under the whole argument cap but over the field's own. Two bounds
	// rather than one, so the refusal names the thing actually exceeded.
	_, fault := validateJSON(t, `{"name":"`+strings.Repeat("A", 33)+`"}`)

	require.NotNil(t, fault)
	require.Equal(t, FaultArgumentTooLarge, fault.Code)
	require.Equal(t, "name", fault.Field)
}

func TestValidate_RejectsTooManyArrayElements(t *testing.T) {
	t.Parallel()
	_, fault := validateJSON(t, `{"name":"x","tags":["a","b","c","d"]}`)

	require.NotNil(t, fault)
	require.Equal(t, FaultArgumentTooLarge, fault.Code)
	require.Equal(t, "tags", fault.Field)
}

func TestValidate_ChecksInsideArrayElements(t *testing.T) {
	t.Parallel()
	_, fault := validateJSON(t, `{"name":"x","tags":["ok","waytoolongforthis"]}`)

	require.NotNil(t, fault)
	require.Equal(t, "tags[1]", fault.Field, "the refusal names the offending element")
}

func TestValidate_RequiredFieldsAndTypes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		code       FaultCode
		field      string
	}{
		{"missing required", `{}`, FaultInvalidArgument, "name"},
		{"wrong type", `{"name":42}`, FaultInvalidArgument, "name"},
		{"not an object", `["name"]`, FaultInvalidArgument, "arguments"},
		{"bad enum", `{"name":"x","mode":"reckless"}`, FaultInvalidArgument, "mode"},
		{"below minimum", `{"name":"x","count":0}`, FaultInvalidArgument, "count"},
		{"above maximum", `{"name":"x","count":11}`, FaultArgumentTooLarge, "count"},
		{"fractional integer", `{"name":"x","count":2.5}`, FaultInvalidArgument, "count"},
		{"bad boolean", `{"name":"x","enabled":"yes"}`, FaultInvalidArgument, "enabled"},
		{"pattern", `{"name":"x","ref":"NOPE"}`, FaultInvalidArgument, "ref"},
		{"empty string", `{"name":""}`, FaultInvalidArgument, "name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, fault := validateJSON(t, tc.body)
			require.NotNil(t, fault)
			require.Equal(t, tc.code, fault.Code)
			require.Equal(t, tc.field, fault.Field)
		})
	}
}

func TestValidate_PatternsAreAnchored(t *testing.T) {
	t.Parallel()
	// An unanchored pattern that a caller can satisfy with a prefix is a
	// validation that does not validate.
	_, fault := validateJSON(t, `{"name":"x","ref":"abc123"}`)
	require.NotNil(t, fault, "a partial match must not satisfy the pattern")

	_, ok := validateJSON(t, `{"name":"x","ref":"abc"}`)
	require.Nil(t, ok)
}

func TestValidate_RejectsMalformedAndDoubledJSON(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`{"name":`, `{"name":"x"} {"name":"y"}`, `not json`} {
		_, fault := validateArguments(sampleSchema(), json.RawMessage(body))
		require.NotNil(t, fault, "body %q", body)
		require.Equal(t, FaultInvalidArgument, fault.Code)
	}
}

func TestValidate_AcceptsAWellFormedCall(t *testing.T) {
	t.Parallel()
	args, fault := validateJSON(t,
		`{"name":"x","mode":"fast","count":3,"ratio":0.5,"enabled":true,"tags":["a"],"ref":"abc"}`)

	require.Nil(t, fault)
	require.Equal(t, "x", args["name"])
	require.Equal(t, json.Number("3"), args["count"], "integers stay exact rather than becoming float64")
}

func TestValidate_AbsentArgumentsAreAnEmptyObject(t *testing.T) {
	t.Parallel()
	// A tool that takes no required arguments must be callable with no
	// arguments member at all, which is what a client that has nothing to
	// send actually writes.
	empty := &Schema{Type: "object", Properties: map[string]*Schema{}}
	args, fault := validateArguments(empty, nil)

	require.Nil(t, fault)
	require.Empty(t, args)
}

func TestSchemaDocument_PublishesAClosedObject(t *testing.T) {
	t.Parallel()
	doc := sampleSchema().document()

	require.Equal(t, false, doc["additionalProperties"],
		"the published contract must say the object is closed")
	require.Equal(t, []string{"name"}, doc["required"])

	props := doc["properties"].(map[string]any)
	require.Equal(t, 32, props["name"].(map[string]any)["maxLength"],
		"a bound the validator enforces must appear in the document")
	require.Equal(t, 3, props["tags"].(map[string]any)["maxItems"])
	require.Equal(t, []string{"fast", "thorough"}, props["mode"].(map[string]any)["enum"])
}

func TestSchemaDocument_IsValidJSON(t *testing.T) {
	t.Parallel()
	// The document goes onto the wire, so it has to encode. A schema holding
	// something unencodable would fail at the worst moment, during a
	// tools/list a client makes once at startup.
	body, err := json.Marshal(sampleSchema().document())
	require.NoError(t, err)
	require.Contains(t, string(body), `"additionalProperties":false`)
}
