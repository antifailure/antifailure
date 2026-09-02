package workload

import (
	"strconv"
	"strings"
)

// The reproducible command is the whole trust anchor of this package.
//
// A hosted result that cannot be reproduced on a laptop is a number somebody
// has to believe. So every result carries the argv of the PLAIN command that
// produced the same run: `af load run ...`, not `af workload run ...`. A
// command only the hosted caller can run would prove nothing, and would let
// this package drift from the commands it claims to be adapting without
// anything noticing.
//
// Two rules follow from that and they are the reason this file is short.
//
// Every knob is stated explicitly, even when the request left it empty and the
// plan resolved it to the flag's own default. An argv that omits a flag
// reproduces whatever that flag defaults to on the day somebody pastes it,
// which is a weaker promise than reproducing this run, and the defaults have
// moved in this repository before.
//
// No knob may exist in a request that has no flag here. That is enforced in
// Parse rather than here, and it is why refusal is loud: a knob with nowhere
// to go in this function is a knob the pasted command would silently drop.

// Argv is the plain af command that reproduces this plan.
//
// The leading element is the program name rather than a path, because the
// installer puts af on PATH and a hosted result quoting /home/runner/... is
// the runner-local path defect this product has shipped in reports before.
func (p *Plan) Argv() []string {
	switch p.Kind {
	case ObservedLoad:
		return []string{"af", "load", "run",
			"--duration", p.Duration.String(),
			"--scale", strconv.FormatFloat(p.Scale, 'g', -1, 64),
			"--seed", strconv.FormatInt(p.SeedNumber, 10),
		}
	case HTTPScenario:
		argv := []string{"af", "load", "scenario",
			"--seed", strconv.FormatInt(p.SeedNumber, 10),
			"--concurrency", strconv.Itoa(p.Concurrency),
		}
		return appendOnly(argv, p.Select)
	case BrowserWorkflow:
		argv := []string{"af", "test", "--attempts", strconv.Itoa(p.Attempts)}
		return appendOnly(argv, p.Select)
	case Exploration:
		argv := []string{"af", "explore"}
		if p.SeedText != "" {
			argv = append(argv, "--seed", p.SeedText)
		}
		return appendOnly(argv, p.Select)
	}
	return nil
}

// appendOnly repeats --only once per name.
//
// Repeated rather than comma joined, and the difference is not cosmetic. The
// scenario command declares --only as a StringSlice, which splits on commas
// and reads its values as CSV, while test and explore declare it as a
// StringArray, which does neither. One spelling that means the same thing to
// all three is the repeated flag, and Parse refuses a name carrying a comma or
// a quote so that the two readings cannot diverge.
func appendOnly(argv []string, names []string) []string {
	for _, n := range names {
		argv = append(argv, "--only", n)
	}
	return argv
}

// Command is the argv as a single line somebody can paste.
//
// Quoted only where a value needs it, because a line where every argument is
// quoted is a line people stop reading, and this one exists to be read.
func (p *Plan) Command() string {
	argv := p.Argv()
	parts := make([]string, 0, len(argv))
	for _, a := range argv {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// shellQuote wraps a value in single quotes when a shell would otherwise read
// it as more than one word or as syntax.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`&|;<>()*?[]#~=") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
