package cli

import "github.com/antifailure/antifailure/engine/internal/report"

// The one door this package opens for its own tests, and no wider.
//
// gateError is unexported because nothing outside the command should be
// turning a finding into an exit code. A test still has to be able to hold
// that routing against the set of rules pgcrash declares, because that is the
// pairing that rots: a rule added on one side and routed on neither takes the
// default, and the default is the wrong answer for a rule that means "I could
// not look".
//
// The classification itself moved to env.UnverifiedRule and env.ChaosRun.Holds
// when the MCP server grew a chaos tool, because two surfaces reading two
// copies of that loop is how a run comes to fail at a terminal and pass
// through an agent.

func GateErrorForTest(f report.Finding) error { return gateError(f) }
