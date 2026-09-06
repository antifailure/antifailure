package mcp

import (
	"errors"
	"fmt"
	"strings"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/lock"
)

// refineLockFault turns a fault caused by a held branch lock into the
// BRANCH_LOCKED refusal, naming the holder.
//
// Every tool that reaches the orchestrator wraps an engine error in its own
// fault, with a detail that lists the usual causes of that tool failing. When
// the cause is the branch lock, none of the usual causes is the cause, and the
// command line for the identical situation prints AF-RUN-003 with the holder's
// process id, when it took the lock and what to do. This is applied at the
// two places a fault leaves this package, the run store and the tool response,
// so a lock refusal reaches a caller in the same words on both surfaces
// without every tool having to know about locks.
//
// Any other fault is returned as it is.
func refineLockFault(f *Fault) *Fault {
	if f == nil || !lock.IsHeld(f.wrapped) {
		return f
	}
	var coded *aferrors.Error
	if !errors.As(f.wrapped, &coded) {
		return f
	}
	return &Fault{
		Code:      FaultBranchLocked,
		Detail:    describeHolder(coded),
		Retryable: true,
		wrapped:   f.wrapped,
	}
}

// describeHolder renders the lock refusal in the command line's words, the
// code, the catalog sentence, the next step and the link, through the same
// explainCause every other catalogued cause goes through, with the holder's
// command added because a caller that cannot see a process table needs it.
//
// Written into the detail rather than left for document to add, because a
// submitted run's fault is stored, and the store keeps the code and the
// detail and not the error underneath. A caller polling a run refused by the
// lock reads this detail and nothing else.
func describeHolder(coded *aferrors.Error) string {
	var b strings.Builder
	b.WriteString("This call was refused rather than allowed to run against a branch " +
		"another process is using, so it says nothing about the change.")
	if command := strings.TrimSpace(coded.Fields["command"]); command != "" {
		fmt.Fprintf(&b, " That process is running %q.", safeText(command, 120))
	}
	b.WriteString(" ")
	b.WriteString(explainCause(coded))
	return b.String()
}
