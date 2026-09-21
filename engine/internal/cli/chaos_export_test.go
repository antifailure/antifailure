package cli

import "github.com/antifailure/antifailure/engine/internal/report"

// The one door this package opens for its own tests, and no wider.
//
// gateError is unexported because nothing outside the command should be
// choosing the exit code a finding carries. The classification that decides
// what a chaos finding MEANS used to be behind two more doors here and now
// lives in engine/internal/gate, where the MCP server can read the same answer:
// a copy of it inside that package would have been a second implementation of
// the split this whole feature rests on.

func GateErrorForTest(f report.Finding) error { return gateError(f) }
