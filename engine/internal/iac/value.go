package iac

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// THE PROPERTY THIS FILE EXISTS FOR, and it is the one the rest of the package
// is arranged around: "I could not look" is a DISTINCT ANSWER from "it is not
// there".
//
// A reader of infrastructure as code meets expressions it cannot resolve
// without running Terraform: a variable with no default, a value that only
// exists after an apply, a module fetched from a registry this reader will not
// fetch. Every one of those is a hole in the description of production. A hole
// reported as a zero is worse than no reader at all, because a caller comparing
// production against a twin would read "production declares 0 replicas" and
// conclude the twin matches when nothing was ever measured.
//
// So a field is one of exactly three things, and the caller is made to say
// which one it is handling:
//
//	Absent      the configuration does not declare it
//	Known       the configuration declares it and this reader resolved it
//	Unreadable  the configuration declares it and this reader could not resolve it
//
// TWO DECISIONS MAKE THAT STRUCTURAL RATHER THAN DOCUMENTED.
//
// The zero value is Absent. A Component built by a reader that sets only the
// three fields it understood has every other field reading "not declared",
// which is true, rather than "declared as the empty string", which is a lie
// that compiles.
//
// The value is UNEXPORTED and Get is the only way to it. There is no field a
// caller can read that skips the state check, so putting an unresolved field's
// zero into a report is not a mistake that can be made quietly: it requires
// ignoring a second return value, which vet and every reviewer already read as
// a question.

// State says whether a field was read, could not be read, or is genuinely
// absent from the configuration.
type State uint8

const (
	// Absent is the zero value on purpose: a field nobody set is a field the
	// configuration does not declare.
	Absent State = iota
	// Known is a value this reader resolved from the configuration.
	Known
	// Unreadable is a value the configuration declares and this reader could
	// not resolve, carrying the reason and the line it is on.
	Unreadable
	// Withheld is a value this reader RESOLVED and will not carry, because it
	// is a credential.
	//
	// It is a fourth state rather than a flavour of Unreadable, and the
	// distinction was forced by a test rather than invented. "I could not work
	// this out" is a hole in the description of production and somebody should
	// go and look at it. "I know exactly what this is and I am not putting a
	// password in your report" is the system working. Collapsing them makes a
	// reading of a plan, where nothing is unresolvable by construction, report
	// holes it does not have, and hides the real ones among them.
	//
	// Get refuses a Withheld value exactly as it refuses the other two, so no
	// consumer can read a credential by failing to notice a new state.
	Withheld
)

// String names the state in the words the report uses.
func (s State) String() string {
	switch s {
	case Known:
		return "known"
	case Unreadable:
		return "unreadable"
	case Withheld:
		return "withheld"
	case Absent:
		return "absent"
	default:
		return "state(" + strconv.Itoa(int(s)) + ")"
	}
}

// Position is where in the tree something was read, relative to the root that
// was read rather than absolute, so a Reading can be compared between two
// checkouts of the same repository.
type Position struct {
	File string `json:"file"`
	Line int    `json:"line,omitempty"`
	Col  int    `json:"col,omitempty"`
}

// String renders the position the way an editor and a grep both accept.
func (p Position) String() string {
	switch {
	case p.File == "":
		return ""
	case p.Line == 0:
		return p.File
	case p.Col == 0:
		return fmt.Sprintf("%s:%d", p.File, p.Line)
	default:
		return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
	}
}

// Value is one field that was read, could not be read, or is not declared.
//
// Its fields are unexported so that Get is the only route to the value. See
// the header of this file for why that is the design and not an inconvenience.
type Value[T any] struct {
	state State
	value T
	why   string
	at    Position
}

// Resolved is a value this reader resolved, and where it resolved it from.
func Resolved[T any](v T, at Position) Value[T] {
	return Value[T]{state: Known, value: v, at: at}
}

// Refused is a value this reader resolved and will not carry, because its name,
// its resource type, or the plan's own mask says it is a credential.
func Refused[T any](why string, at Position) Value[T] {
	return Value[T]{state: Withheld, why: why, at: at}
}

// Unresolved is a value the configuration declares and this reader could not
// resolve.
//
// The reason is a sentence a report puts in front of a person, so it names the
// thing that could not be resolved rather than the parser that failed. It must
// never quote the expression: an expression can hold a credential written in
// line, and a reason is written into logs and reports that a credential must
// not reach.
func Unresolved[T any](why string, at Position) Value[T] {
	return Value[T]{state: Unreadable, why: why, at: at}
}

// Get returns the value, and true only when it is Known.
//
// Absent and Unreadable both return false, because a caller that wants to tell
// them apart has State and a caller that does not must treat both as "no value
// here" rather than as a zero.
func (v Value[T]) Get() (T, bool) {
	if v.state != Known {
		var zero T
		return zero, false
	}
	return v.value, true
}

// State says which of the three answers this is.
func (v Value[T]) State() State { return v.state }

// Why is the sentence explaining an Unreadable value, and empty otherwise.
func (v Value[T]) Why() string { return v.why }

// At is where the value, or the expression that could not be resolved, was
// written.
func (v Value[T]) At() Position { return v.at }

// Or returns the value when it is Known and the fallback otherwise.
//
// It exists so that a caller with a genuine default does not have to write the
// two line form, and it is deliberately the ONLY convenience of its kind: a
// caller reaching for it has said out loud that a missing value and a
// substitute are interchangeable here, which is a claim about that call site.
func (v Value[T]) Or(fallback T) T {
	if v.state != Known {
		return fallback
	}
	return v.value
}

// MarshalJSON writes the state alongside the value, so that a Reading that
// crosses a process boundary cannot arrive with its holes closed up.
//
// An Absent value writes null rather than an object, because a field that is
// not declared should not add noise to every serialised component, and null
// decodes back to the zero Value, which is Absent.
func (v Value[T]) MarshalJSON() ([]byte, error) {
	switch v.state {
	case Absent:
		return []byte("null"), nil
	case Known:
		return json.Marshal(struct {
			State string    `json:"state"`
			Value T         `json:"value"`
			At    *Position `json:"at,omitempty"`
		}{State: "known", Value: v.value, At: v.at.orNil()})
	default:
		return json.Marshal(struct {
			State string    `json:"state"`
			Why   string    `json:"why"`
			At    *Position `json:"at,omitempty"`
		}{State: v.state.String(), Why: v.why, At: v.at.orNil()})
	}
}

// UnmarshalJSON reads back what MarshalJSON wrote, preserving the state.
func (v *Value[T]) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*v = Value[T]{}
		return nil
	}
	var wire struct {
		State string    `json:"state"`
		Value T         `json:"value"`
		Why   string    `json:"why"`
		At    *Position `json:"at"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return err
	}
	out := Value[T]{why: wire.Why}
	if wire.At != nil {
		out.at = *wire.At
	}
	switch wire.State {
	case "known":
		out.state = Known
		out.value = wire.Value
	case "unreadable":
		out.state = Unreadable
	case "withheld":
		out.state = Withheld
	case "absent", "":
		out.state = Absent
	default:
		return fmt.Errorf("iac: %q is not a value state", wire.State)
	}
	*v = out
	return nil
}

func (p Position) orNil() *Position {
	if p.File == "" {
		return nil
	}
	return &p
}
