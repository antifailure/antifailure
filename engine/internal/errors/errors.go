// Package errors defines the engine's error type and the codes it carries.
//
// Every error a user can see has a code from catalog.yaml, a one sentence
// cause, a one sentence next step, a documentation slug, a retryable flag, and
// the process exit code it maps to. The catalog is the source of truth: Go
// constants and documentation pages are generated from it, a code with no
// entry fails the build, and an entry nothing returns fails it too. That is
// what keeps the error surface from drifting away from the documentation.
//
// Errors are wrapped with the operation that produced them, so a failure deep
// in a provider arrives at the command boundary reading as a path rather than
// a mystery. Classification is by errors.Is and errors.As, never by string
// comparison.
package errors

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Re-exported so that callers need only this package.
var (
	// Is reports whether any error in err's chain matches target.
	Is = errors.Is
	// As finds the first error in err's chain that matches target.
	As = errors.As
	// Unwrap returns the result of calling Unwrap on err.
	Unwrap = errors.Unwrap
	// Join returns an error wrapping the given errors.
	Join = errors.Join
	// New returns an error with the given text. Prefer Coded for anything a
	// user can see; New is for internal invariants that indicate a bug.
	New = errors.New
)

// ExitCode is the process exit status an error maps to.
type ExitCode int

// The exit code registry. These are part of the public interface: scripts
// branch on them, so they are stable.
const (
	ExitSuccess          ExitCode = 0
	ExitFailure          ExitCode = 1
	ExitUsage            ExitCode = 2
	ExitConfiguration    ExitCode = 3
	ExitAuth             ExitCode = 4
	ExitProvider         ExitCode = 5
	ExitPolicyDenied     ExitCode = 6
	ExitVerification     ExitCode = 7
	ExitTestFailure      ExitCode = 8
	ExitInterruptedClean ExitCode = 9
	ExitInterruptedDirty ExitCode = 10
)

// Code identifies an entry in the catalog, for example AF-DB-006.
type Code string

// Entry is one catalog record.
type Entry struct {
	Code      Code
	Area      string
	Message   string
	NextStep  string
	Docs      string
	Retryable bool
	ExitCode  ExitCode
}

// Error is the engine's user facing error.
//
// It renders as the filled message followed by the next step, which is the
// shape every command's error output takes. The code, the documentation link,
// and the retryable flag are available for machine readable output and for the
// retry logic in provider calls.
type Error struct {
	// Entry is the catalog record this error instantiates.
	Entry Entry
	// Fields fill the {placeholders} in the catalog message and next step.
	Fields map[string]string
	// Op names the operation, for example "db.docker: branch". It builds the
	// path a reader follows back to the cause.
	Op string
	// Err is the wrapped cause, if any.
	Err error
}

// Coded returns an error for the given code with the given fields.
//
// Fields are supplied as alternating key and value strings, which reads
// naturally at a call site and needs no map literal:
//
//	errors.Coded(errors.AFDB006, "limit", strconv.Itoa(max))
//
// An odd number of arguments is a programming error and panics in tests
// through the catalog test; at run time the trailing key is given an empty
// value rather than losing the error entirely.
func Coded(code Code, fields ...string) *Error {
	return &Error{Entry: Lookup(code), Fields: pairs(fields)}
}

// Wrap returns an error for the given code that wraps cause.
func Wrap(cause error, code Code, fields ...string) *Error {
	return &Error{Entry: Lookup(code), Fields: pairs(fields), Err: cause}
}

// WithOp returns a copy of err with the operation set. Callers use it to
// record where an error passed through:
//
//	return nil, errors.WithOp(err, "db.docker: branch")
func WithOp(err error, op string) error {
	if err == nil {
		return nil
	}
	var e *Error
	if As(err, &e) {
		cp := *e
		if cp.Op == "" {
			cp.Op = op
		} else {
			cp.Op = op + ": " + cp.Op
		}
		return &cp
	}
	return fmt.Errorf("%s: %w", op, err)
}

func pairs(kv []string) map[string]string {
	if len(kv) == 0 {
		return nil
	}
	m := make(map[string]string, (len(kv)+1)/2)
	for i := 0; i < len(kv); i += 2 {
		k := kv[i]
		v := ""
		if i+1 < len(kv) {
			v = kv[i+1]
		}
		m[k] = v
	}
	return m
}

// Error renders the message with its fields filled in.
func (e *Error) Error() string {
	var b strings.Builder
	if e.Op != "" {
		b.WriteString(e.Op)
		b.WriteString(": ")
	}
	b.WriteString(string(e.Entry.Code))
	b.WriteString(": ")
	b.WriteString(fill(e.Entry.Message, e.Fields))
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

// Unwrap returns the wrapped cause.
func (e *Error) Unwrap() error { return e.Err }

// Is reports whether target is the same catalog code. It lets callers write
// errors.Is(err, errors.Coded(errors.AFDB006)) without constructing fields.
func (e *Error) Is(target error) bool {
	var t *Error
	if !errors.As(target, &t) {
		return false
	}
	return t.Entry.Code == e.Entry.Code
}

// Message returns the filled cause sentence, with no code and no operation.
func (e *Error) Message() string { return fill(e.Entry.Message, e.Fields) }

// NextStep returns the filled next step sentence.
func (e *Error) NextStep() string { return fill(e.Entry.NextStep, e.Fields) }

// Code returns the catalog code.
func (e *Error) Code() Code { return e.Entry.Code }

// Retryable reports whether retrying the same operation unchanged could
// succeed. Provider call sites use it to decide whether to back off or fail.
func (e *Error) Retryable() bool { return e.Entry.Retryable }

// ExitCode returns the process exit status this error maps to.
func (e *Error) ExitCode() ExitCode { return e.Entry.ExitCode }

// DocsURL returns the documentation page for this code.
func (e *Error) DocsURL() string {
	return "https://antifailure.dev/docs/" + e.Entry.Docs
}

// fill substitutes {key} placeholders. An unknown placeholder is left as it is
// rather than rendered as an empty string, so that a missing field shows up as
// an obvious bug in output instead of a sentence with a hole in it.
func fill(s string, fields map[string]string) string {
	if len(fields) == 0 || !strings.ContainsRune(s, '{') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for {
		i := strings.IndexByte(s, '{')
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := strings.IndexByte(s[i:], '}')
		if j < 0 {
			b.WriteString(s)
			return b.String()
		}
		j += i
		key := s[i+1 : j]
		b.WriteString(s[:i])
		if v, ok := fields[key]; ok {
			b.WriteString(v)
		} else {
			b.WriteString(s[i : j+1])
		}
		s = s[j+1:]
	}
}

// Lookup returns the catalog entry for a code.
//
// An unknown code returns a placeholder entry rather than panicking, because
// an error path is the worst possible place to introduce a new crash. The
// catalog test asserts that no unknown code is ever constructed, so this
// branch is unreachable in a build that passes its gates.
func Lookup(code Code) Entry {
	if e, ok := catalog[code]; ok {
		return e
	}
	return Entry{
		Code:     code,
		Area:     "UNK",
		Message:  "An unrecognised error code was raised: " + string(code),
		NextStep: "Report this at https://github.com/antifailure/antifailure/issues; it is a bug in Antifailure, not in your project.",
		Docs:     "reference/errors",
		ExitCode: ExitFailure,
	}
}

// All returns every catalog entry, sorted by code. The documentation
// generator and the completeness gate use it.
func All() []Entry {
	out := make([]Entry, 0, len(catalog))
	for _, e := range catalog {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// ExitCodeOf returns the exit status for any error: the catalog's code for an
// engine error, ExitSuccess for nil, and ExitFailure for anything else.
func ExitCodeOf(err error) ExitCode {
	if err == nil {
		return ExitSuccess
	}
	var e *Error
	if As(err, &e) {
		return e.Entry.ExitCode
	}
	return ExitFailure
}

// IsRetryable reports whether err is an engine error marked retryable.
func IsRetryable(err error) bool {
	var e *Error
	return As(err, &e) && e.Entry.Retryable
}
