package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Schema is the declaration of one tool argument, or of a whole argument
// object.
//
// It is both the validator and the JSON Schema published to the client. One
// declaration produces both, which is the point: a published schema that says
// a field is bounded, and a validator that does not enforce the bound, is
// worse than no schema at all, because a caller has been told a promise the
// server does not keep. Deriving the document from the thing that enforces it
// makes the two incapable of disagreeing.
//
// The vocabulary is small on purpose. It covers what these tools accept and
// nothing else, so there is no partially implemented keyword whose published
// meaning outruns its enforcement.
type Schema struct {
	// Type is the JSON type: object, array, string, integer, number or
	// boolean.
	Type string
	// Description is what the model reads. It says what the field means and
	// what it is for, never how to bypass anything.
	Description string
	// Properties are an object's members. Only meaningful for type object.
	Properties map[string]*Schema
	// Required names the members that must be present.
	Required []string
	// Enum, when set, is the closed set of permitted string values.
	Enum []string
	// MinLength and MaxLength bound a string. MaxLength is required on every
	// string field; a string with no upper bound is an unbounded allocation
	// driven by the caller.
	MinLength, MaxLength int
	// Minimum and Maximum bound a number, inclusive. Both are honoured only
	// when HasMin or HasMax is set, so that a legitimate bound of zero is not
	// mistaken for an absent one.
	Minimum, Maximum float64
	HasMin, HasMax   bool
	// Items is the element schema of an array.
	Items *Schema
	// MaxItems bounds an array. Required on every array field, for the same
	// reason MaxLength is required on every string.
	MaxItems int
	// Pattern is a regular expression a string must match entirely.
	Pattern string

	// compiled caches the anchored pattern. Built on first use by validate
	// and never mutated afterwards, because schemas are built at process
	// start and are read only from then on.
	compiled *regexp.Regexp
}

// maxArgumentBytes caps the encoded arguments object of one tool call.
//
// Well under the transport's frame cap, because the transport has to carry
// whole protocol messages while a tool's arguments are a handful of short
// strings. Two bounds rather than one, so that the refusal a caller gets names
// the thing it actually exceeded.
const maxArgumentBytes = 64 << 10

// document renders the schema as JSON Schema for tools/list.
//
// additionalProperties is emitted as false on every object, which is what
// makes the published contract match the validator: unknown members are
// refused here and the document says so, rather than the client discovering it
// by being rejected.
func (s *Schema) document() map[string]any {
	if s == nil {
		return map[string]any{}
	}
	d := map[string]any{"type": s.Type}
	if s.Description != "" {
		d["description"] = s.Description
	}
	switch s.Type {
	case "object":
		props := map[string]any{}
		for name, p := range s.Properties {
			props[name] = p.document()
		}
		d["properties"] = props
		// Always present, even when empty, so a caller reading the document
		// sees an object that takes no members rather than one whose members
		// were omitted.
		d["additionalProperties"] = false
		if len(s.Required) > 0 {
			req := append([]string(nil), s.Required...)
			sort.Strings(req)
			d["required"] = req
		}
	case "array":
		if s.Items != nil {
			d["items"] = s.Items.document()
		}
		if s.MaxItems > 0 {
			d["maxItems"] = s.MaxItems
		}
	case "string":
		if len(s.Enum) > 0 {
			d["enum"] = append([]string(nil), s.Enum...)
		}
		if s.MinLength > 0 {
			d["minLength"] = s.MinLength
		}
		if s.MaxLength > 0 {
			d["maxLength"] = s.MaxLength
		}
		if s.Pattern != "" {
			d["pattern"] = s.Pattern
		}
	case "integer", "number":
		if s.HasMin {
			d["minimum"] = s.Minimum
		}
		if s.HasMax {
			d["maximum"] = s.Maximum
		}
	}
	return d
}

// validateArguments decodes and checks a tool call's arguments object.
//
// The raw bytes are checked against the byte cap before anything is decoded,
// so an oversized argument object is refused without being parsed into memory
// first. Numbers are decoded as json.Number rather than float64 so that an
// integer field can tell 3 from 3.5, which float64 cannot once it has rounded.
func validateArguments(s *Schema, raw json.RawMessage) (map[string]any, *Fault) {
	if len(raw) > maxArgumentBytes {
		return nil, faultf(FaultArgumentTooLarge,
			"The arguments are %d bytes, and this server accepts at most %d.",
			len(raw), maxArgumentBytes)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, faultf(FaultInvalidArgument, "The arguments are not valid JSON.")
	}
	// A second token means the caller sent two documents where one was
	// expected, which json.Decoder would otherwise ignore entirely.
	if dec.More() {
		return nil, faultf(FaultInvalidArgument,
			"The arguments carry more than one JSON value.")
	}

	if f := validate(s, value, ""); f != nil {
		return nil, f
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, faultf(FaultInvalidArgument, "The arguments must be a JSON object.")
	}
	return obj, nil
}

// validate checks one value against one schema.
//
// path is the dotted location of the value for the error message, empty at the
// root. Every refusal names the field, because a model given "invalid
// argument" with no location retries the same call.
func validate(s *Schema, v any, path string) *Fault {
	if s == nil {
		return nil
	}
	switch s.Type {
	case "object":
		return validateObject(s, v, path)
	case "array":
		return validateArray(s, v, path)
	case "string":
		return validateString(s, v, path)
	case "integer", "number":
		return validateNumber(s, v, path)
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fieldFault(FaultInvalidArgument, at(path), "This field must be true or false.")
		}
		return nil
	default:
		return internalFault(fmt.Errorf("schema at %q declares unknown type %q", at(path), s.Type))
	}
}

func validateObject(s *Schema, v any, path string) *Fault {
	obj, ok := v.(map[string]any)
	if !ok {
		return fieldFault(FaultInvalidArgument, at(path), "This field must be an object.")
	}
	// Unknown members first. A call carrying a field this server does not
	// know is refused before anything else is judged, because the most likely
	// cause is a caller built against a different contract, and reporting a
	// type error inside a call that was going to be refused anyway sends it
	// to fix the wrong thing.
	unknown := make([]string, 0)
	for name := range obj {
		if _, declared := s.Properties[name]; !declared {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fieldFault(FaultUnknownField, at(join(path, unknown[0])),
			"This server does not accept %s. It accepts only: %s.",
			quoteList(unknown), strings.Join(sortedKeys(s.Properties), ", "))
	}
	for _, name := range s.Required {
		if _, present := obj[name]; !present {
			return fieldFault(FaultInvalidArgument, at(join(path, name)),
				"This field is required.")
		}
	}
	// Deterministic order, so that a call with two bad fields is refused with
	// the same message every time rather than whichever the map yielded first.
	for _, name := range sortedKeys(s.Properties) {
		value, present := obj[name]
		if !present {
			continue
		}
		if f := validate(s.Properties[name], value, join(path, name)); f != nil {
			return f
		}
	}
	return nil
}

func validateArray(s *Schema, v any, path string) *Fault {
	arr, ok := v.([]any)
	if !ok {
		return fieldFault(FaultInvalidArgument, at(path), "This field must be an array.")
	}
	if s.MaxItems > 0 && len(arr) > s.MaxItems {
		return fieldFault(FaultArgumentTooLarge, at(path),
			"This field carries %d elements, and this server accepts at most %d.",
			len(arr), s.MaxItems)
	}
	for i, item := range arr {
		if f := validate(s.Items, item, fmt.Sprintf("%s[%d]", path, i)); f != nil {
			return f
		}
	}
	return nil
}

func validateString(s *Schema, v any, path string) *Fault {
	str, ok := v.(string)
	if !ok {
		return fieldFault(FaultInvalidArgument, at(path), "This field must be a string.")
	}
	if s.MaxLength > 0 && len(str) > s.MaxLength {
		return fieldFault(FaultArgumentTooLarge, at(path),
			"This field is %d bytes, and this server accepts at most %d.",
			len(str), s.MaxLength)
	}
	if s.MinLength > 0 && len(str) < s.MinLength {
		return fieldFault(FaultInvalidArgument, at(path),
			"This field must be at least %d bytes.", s.MinLength)
	}
	if len(s.Enum) > 0 {
		for _, allowed := range s.Enum {
			if str == allowed {
				return nil
			}
		}
		return fieldFault(FaultInvalidArgument, at(path),
			"This field must be one of: %s.", strings.Join(s.Enum, ", "))
	}
	if s.Pattern != "" {
		re, err := s.regexp()
		if err != nil {
			return internalFault(err)
		}
		if !re.MatchString(str) {
			// The offending value is not echoed. It is caller supplied text
			// and a report is not a channel for it.
			return fieldFault(FaultInvalidArgument, at(path),
				"This field does not have the required form.")
		}
	}
	return nil
}

func validateNumber(s *Schema, v any, path string) *Fault {
	num, ok := v.(json.Number)
	if !ok {
		return fieldFault(FaultInvalidArgument, at(path), "This field must be a number.")
	}
	f, err := num.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return fieldFault(FaultInvalidArgument, at(path), "This field must be a finite number.")
	}
	if s.Type == "integer" {
		if _, err := num.Int64(); err != nil {
			return fieldFault(FaultInvalidArgument, at(path),
				"This field must be a whole number.")
		}
	}
	if s.HasMin && f < s.Minimum {
		return fieldFault(FaultInvalidArgument, at(path),
			"This field must be at least %s.", trimFloat(s.Minimum))
	}
	if s.HasMax && f > s.Maximum {
		return fieldFault(FaultArgumentTooLarge, at(path),
			"This field must be at most %s.", trimFloat(s.Maximum))
	}
	return nil
}

// regexp compiles and caches the anchored pattern.
//
// Anchored on both ends, because an unanchored pattern that a caller can
// satisfy with a prefix is a validation that does not validate. A schema
// author writing "[a-z]+" means the whole value.
func (s *Schema) regexp() (*regexp.Regexp, error) {
	if s.compiled != nil {
		return s.compiled, nil
	}
	re, err := regexp.Compile("^(?:" + s.Pattern + ")$")
	if err != nil {
		return nil, fmt.Errorf("schema pattern %q does not compile: %w", s.Pattern, err)
	}
	s.compiled = re
	return re, nil
}

// at names the field for an error, calling the whole object "the arguments"
// rather than an empty string.
func at(path string) string {
	if path == "" {
		return "arguments"
	}
	return path
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func sortedKeys(m map[string]*Schema) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func quoteList(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, quoteName(n))
	}
	if len(quoted) == 1 {
		return "the field " + quoted[0]
	}
	return "the fields " + strings.Join(quoted, ", ")
}

// quoteName quotes a caller supplied field name for a message.
//
// The name is quoted with %q rather than interpolated raw, because it is
// caller supplied and a field name containing a newline would otherwise
// rearrange the message around it.
func quoteName(s string) string {
	if len(s) > 64 {
		s = s[:64]
	}
	return fmt.Sprintf("%q", s)
}

// trimFloat renders a bound without a trailing ".0" on whole numbers.
func trimFloat(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%g", f)
}
