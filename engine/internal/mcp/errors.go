package mcp

import (
	"errors"
	"fmt"
	"strings"
)

// Fault is a tool level failure, addressed to a model rather than a person.
//
// It is deliberately not an engine/internal/errors code. That catalog exists
// for errors that reach a command boundary and it carries a human next step, a
// documentation slug and a process exit code, none of which mean anything to a
// caller reading a JSON result. Rather than fill a catalog entry with three
// fields that would be ignored and one exit code that can never be reached,
// this package keeps its own small closed vocabulary and states the mapping
// here. Engine errors that arise underneath a tool keep their AF code and are
// reported inside the Detail of the fault that wraps them.
//
// The vocabulary is closed on purpose. A caller can branch on Code, and a code
// that is invented at a call site is a code no caller can branch on.
type Fault struct {
	// Code is the stable machine readable reason.
	Code FaultCode
	// Detail is one sentence a model can act on. It never contains bytes
	// copied out of the candidate repository, because the candidate is
	// untrusted input and a report is not a channel for it.
	Detail string
	// Field names the offending argument, when one argument is at fault.
	Field string
	// Retryable says whether the identical call could later succeed.
	Retryable bool
	// wrapped is the underlying engine error, kept for the server log on
	// standard error. It is never rendered into the result, because an
	// internal error string is a way for details of the host to reach a
	// caller that has no business seeing them.
	wrapped error
}

// FaultCode is the closed set of tool level failure reasons.
type FaultCode string

const (
	// FaultInvalidArgument is a request that failed schema validation:
	// a missing required field, a wrong type, a value out of range, or a
	// string that does not match its pattern.
	FaultInvalidArgument FaultCode = "INVALID_ARGUMENT"
	// FaultUnknownField is a request carrying a member no schema declares.
	//
	// Separate from INVALID_ARGUMENT rather than folded into it, because the
	// two mean different things to a caller. A wrong value is a call to fix;
	// an unknown field is usually a caller built against a different version,
	// and telling it apart from a typo is worth one code.
	FaultUnknownField FaultCode = "UNKNOWN_FIELD"
	// FaultArgumentTooLarge is a request whose arguments exceed a documented
	// bound: too many bytes, too many array elements, or a string too long.
	FaultArgumentTooLarge FaultCode = "ARGUMENT_TOO_LARGE"
	// FaultProjectMismatch is a project_id that does not name the repository
	// this server was started against.
	//
	// This is never an authorisation decision. The server serves exactly one
	// checkout, chosen by whoever started it, and this code means the caller
	// believes it is talking to a different one. Answering the call anyway
	// would run an experiment against a repository the caller did not mean.
	FaultProjectMismatch FaultCode = "PROJECT_MISMATCH"
	// FaultRunNotFound is a run_id that names no run this server started.
	//
	// It is also what a caller gets for a run belonging to a different
	// project, which is why the message never distinguishes the two: telling
	// a caller that a run exists but is not theirs is itself a disclosure.
	FaultRunNotFound FaultCode = "RUN_NOT_FOUND"
	// FaultIdempotencyConflict is an idempotency key reused with different
	// canonical inputs.
	FaultIdempotencyConflict FaultCode = "IDEMPOTENCY_CONFLICT"
	// FaultPathRejected is a repository_file that does not resolve to a
	// regular file inside the checkout: traversal, a symlink leaving the
	// tree, a device or a pipe, or a path that is simply absent.
	FaultPathRejected FaultCode = "PATH_REJECTED"
	// FaultSafetyUnavailable is a safety subsystem that could not be
	// established, so the experiment did not start.
	//
	// This is the fail closed code. It is returned when the thing that would
	// have contained the experiment is missing, never when the experiment
	// itself found a problem. A monitoring failure must never widen access,
	// so a subsystem that cannot be brought up stops the run instead.
	FaultSafetyUnavailable FaultCode = "SAFETY_UNAVAILABLE"
	// FaultRunNotCancellable is a cancel of a run that already finished.
	FaultRunNotCancellable FaultCode = "RUN_NOT_CANCELLABLE"
	// FaultUnsupported is a documented capability this build does not serve.
	FaultUnsupported FaultCode = "UNSUPPORTED"
	// FaultInternal is a defect in this server.
	//
	// The detail is a fixed sentence. Whatever went wrong is written to
	// standard error for whoever runs the server, and is not handed to the
	// caller, because an internal failure string is a description of the host.
	FaultInternal FaultCode = "INTERNAL"
)

// Error renders the fault for a Go caller and for the server log.
func (f *Fault) Error() string {
	var b strings.Builder
	b.WriteString(string(f.Code))
	if f.Field != "" {
		b.WriteString(" at ")
		b.WriteString(f.Field)
	}
	if f.Detail != "" {
		b.WriteString(": ")
		b.WriteString(f.Detail)
	}
	return b.String()
}

// Unwrap exposes the underlying engine error to errors.Is and errors.As.
func (f *Fault) Unwrap() error { return f.wrapped }

// faultf builds a fault with a formatted detail.
func faultf(code FaultCode, format string, args ...any) *Fault {
	return &Fault{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// fieldFault builds a fault that blames one argument.
func fieldFault(code FaultCode, field, format string, args ...any) *Fault {
	return &Fault{Code: code, Field: field, Detail: fmt.Sprintf(format, args...)}
}

// internalFault wraps a defect without disclosing it.
//
// The caller sees a fixed sentence. The cause travels in the wrapped error, so
// the server can log it to standard error where the operator will see it.
func internalFault(err error) *Fault {
	return &Fault{
		Code:      FaultInternal,
		Detail:    "This server failed to complete the call. The cause was written to the server log.",
		Retryable: true,
		wrapped:   err,
	}
}

// faultDocument is how a fault appears inside a tool result.
//
// Faults are reported in a successful JSON-RPC response rather than as a
// protocol error, because a tool that ran and refused is telling the model
// something it is meant to read and act on. Reserving the protocol error
// channel for genuine protocol faults keeps the two legible.
type faultDocument struct {
	Kind      string `json:"kind"`
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	Field     string `json:"field,omitempty"`
	Retryable bool   `json:"retryable"`
}

// document renders the caller facing form.
func (f *Fault) document() faultDocument {
	return faultDocument{
		Kind: "error", Code: string(f.Code), Detail: f.Detail,
		Field: f.Field, Retryable: f.Retryable,
	}
}

// asFault converts any error into a fault, so that no path can return an
// unclassified error to a caller.
func asFault(err error) *Fault {
	if err == nil {
		return nil
	}
	var f *Fault
	if errors.As(err, &f) {
		return f
	}
	return internalFault(err)
}
