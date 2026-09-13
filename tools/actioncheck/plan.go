package main

// THE STEP THAT DECIDES WHICH COMMAND A CUSTOMER'S RUN IS, RUN AGAINST EVERY
// DISPATCH A CALLER CAN SEND.
//
// From #276 on 2026-09-05 action.yml's plan step began
//
//	dispatch="${AF_DISPATCH:-{}}"
//
// and a shell reads that as the default `{` followed by a literal `}`, so any
// dispatch that was not empty came out with a brace appended. The example
// passes `toJSON(inputs)`, which renders `{}` on a pull request, so jq was
// handed `{}}` and every customer's check failed with "Unmatched '}'" before af
// ran. The shape checks in main.go read the file and cannot see a script's
// behaviour, the script tests beside them ran the claim, publish and last steps
// and never this one, and no workflow in this repository calls the action. The
// only runs that line ever had were customers'.
//
// So this executes the step exactly as action.yml holds it, under bash with jq,
// for each dispatch below, and for the one the environment carries when there
// is one. CI sets AF_DISPATCH to `${{ toJSON(inputs) }}`, so that one is
// GitHub's own rendering rather than a value typed into this file.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// planCase is one dispatch and what the plan step must make of it.
type planCase struct {
	name     string
	dispatch string
	// unset leaves AF_DISPATCH out of the environment entirely.
	unset bool
	// command is the action's own command input. Empty means its default.
	command string
	exit    int
	// want is every output that must be written, with its exact value.
	want map[string]string
}

// asRunnerWrites renders an object of string members the way the runner's
// toJSON does: a newline and two spaces before every member, a comma after
// every member but the last, and a newline before the closing brace. That is
// src/Sdk/DTExpressions2/Expressions2/Sdk/Functions/ToJson.cs in actions/runner,
// and GitHub's documentation calls the result pretty printed. An object with no
// members is `{}`.
func asRunnerWrites(pairs ...string) string {
	if len(pairs) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i+1 < len(pairs); i += 2 {
		if i > 0 {
			b.WriteString(",")
		}
		k, _ := json.Marshal(pairs[i])
		v, _ := json.Marshal(pairs[i+1])
		fmt.Fprintf(&b, "\n  %s: %s", k, v)
	}
	b.WriteString("\n}")
	return b.String()
}

// consoleDispatch is what `toJSON(inputs)` renders when the hosted control
// plane dispatches the example: every input the example declares, in the order
// it declares them, because the inputs context carries each one, and the empty
// string for each one the dispatch did not set, because none has a default.
func consoleDispatch(set map[string]string) string {
	var pairs []string
	for _, k := range dispatchInputs {
		pairs = append(pairs, k, set[k])
	}
	return asRunnerWrites(pairs...)
}

// noPick is every output the step writes out of the dispatch, empty.
func noPick(extra map[string]string) map[string]string {
	out := map[string]string{
		"workflows": "", "duration": "", "scale": "", "seed": "", "concurrency": "", "run_id": "",
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func planCases() []planCase {
	return []planCase{
		{
			// Run 34741145486 on antifailure-demo-orders, a pull request through
			// the example and check.yml, printed `AF_DISPATCH: {}` in this step
			// and failed on the next line.
			name:     "a pull request, where toJSON(inputs) renders an object with no members",
			dispatch: asRunnerWrites(),
			want:     noPick(map[string]string{"command": "ci", "runner": "yes"}),
		},
		{
			name: "a caller that passes an empty dispatch",
			want: noPick(map[string]string{"command": "ci", "runner": "yes"}),
		},
		{
			name:  "a caller whose dispatch input reaches the step unset",
			unset: true,
			want:  noPick(map[string]string{"command": "ci", "runner": "yes"}),
		},
		{
			name:     "a dispatch of null, which is toJSON of a context that does not exist",
			dispatch: "null",
			want:     noPick(map[string]string{"command": "ci", "runner": "yes"}),
		},
		{
			name:     "a compact object from a caller that writes its own JSON",
			dispatch: `{"command":"up","duration":"30s"}`,
			want:     noPick(map[string]string{"command": "up", "duration": "30s", "runner": "no"}),
		},
		{
			// web/apps/api/src/routers/dispatch.ts sends command, workflows,
			// duration and scale for a load run.
			name: "the console's load dispatch, as the runner renders it",
			dispatch: consoleDispatch(map[string]string{
				"command": "load", "duration": "60s", "scale": "2",
			}),
			want: noPick(map[string]string{"command": "load", "duration": "60s", "scale": "2", "runner": "no"}),
		},
		{
			name: "the console's agents dispatch, as the runner renders it",
			dispatch: consoleDispatch(map[string]string{
				"command": "agents", "workflows": "checkout,search",
			}),
			want: noPick(map[string]string{"command": "agents", "workflows": "checkout,search", "runner": "yes"}),
		},
		{
			name:     "a dispatch naming no command, from a job that set the command input",
			dispatch: asRunnerWrites(),
			command:  "down",
			want:     noPick(map[string]string{"command": "down", "runner": "no"}),
		},
		{
			name:     "a command the action does not know",
			dispatch: `{"command":"deploy"}`,
			exit:     2,
		},
	}
}

// planScript is the run block of the step that reads AF_DISPATCH. Found by what
// it reads rather than by its id, so renaming the step does not turn this off.
func planScript(root string) (string, error) {
	body, err := os.ReadFile(filepath.Join(root, actionFile))
	if err != nil {
		return "", err
	}
	var act action
	if err := yaml.Unmarshal(body, &act); err != nil {
		return "", fmt.Errorf("%s: %w", actionFile, err)
	}
	for _, s := range act.Runs.Steps {
		if _, ok := s.Env["AF_DISPATCH"]; ok && s.Run != "" {
			return s.Run, nil
		}
	}
	return "", fmt.Errorf("%s: no step reads AF_DISPATCH, so nothing decides which command a "+
		"dispatched run is and there is no plan step to run", actionFile)
}

// runPlan runs the step's script under bash with the case's dispatch, and reads
// back what it wrote to GITHUB_OUTPUT. The three inputs the step reads are
// always set from the case and never inherited, so a value this process was
// started with cannot stand in for the one under test.
func runPlan(script string, c planCase) (int, map[string]string, string, error) {
	dir, err := os.MkdirTemp("", "actioncheck-plan-")
	if err != nil {
		return 0, nil, "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	outFile := filepath.Join(dir, "github-output")
	if err := os.WriteFile(outFile, nil, 0o600); err != nil {
		return 0, nil, "", err
	}
	command := c.command
	if command == "" {
		command = "ci"
	}
	var env []string
	for _, kv := range os.Environ() {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "AF_DISPATCH", "AF_COMMAND_INPUT", "AF_RUNNER_INPUT", "GITHUB_OUTPUT":
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GITHUB_OUTPUT="+outFile, "AF_COMMAND_INPUT="+command, "AF_RUNNER_INPUT=auto")
	if !c.unset {
		env = append(env, "AF_DISPATCH="+c.dispatch)
	}
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	cmd.Env = env
	log, runErr := cmd.CombinedOutput()
	exit := 0
	if runErr != nil {
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) {
			return 0, nil, string(log), runErr
		}
		exit = ee.ExitCode()
	}
	written, err := os.ReadFile(outFile)
	if err != nil {
		return exit, nil, string(log), err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(written), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			out[k] = v
		}
	}
	return exit, out, string(log), nil
}

// planProblems runs every case, plus the carried dispatch when there is one,
// and says what it ran.
func planProblems(root, carried string, carriedSet bool) ([]string, string) {
	script, err := planScript(root)
	if err != nil {
		return []string{err.Error()}, ""
	}
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			return []string{fmt.Sprintf("%s: the plan step was not run, because %s is not on PATH. "+
				"That is not a pass: the step decides which command a customer's run is, and "+
				"nothing here looked at it", actionFile, tool)}, ""
		}
	}
	cases := planCases()
	// The carried dispatch has no expected command, because this file did not
	// choose it. It has to exit zero: the step exits 2 on a command it does not
	// know and 5 on JSON jq cannot read, so zero means it read a command it runs.
	if carriedSet {
		cases = append(cases, planCase{
			name:     "the dispatch this job carries, GitHub's own rendering of toJSON(inputs)",
			dispatch: carried,
		})
	}
	var problems []string
	for _, c := range cases {
		exit, out, log, err := runPlan(script, c)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: could not run the plan step for %s: %v", actionFile, c.name, err))
			continue
		}
		if exit != c.exit {
			problems = append(problems, fmt.Sprintf("%s: the plan step exited %d, not %d, for %s "+
				"(AF_DISPATCH=%q). It printed:\n%s", actionFile, exit, c.exit, c.name, c.dispatch,
				strings.TrimSpace(log)))
			continue
		}
		keys := make([]string, 0, len(c.want))
		for k := range c.want {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if got, ok := out[k]; !ok || got != c.want[k] {
				problems = append(problems, fmt.Sprintf("%s: for %s the plan step wrote %s=%q, want %q",
					actionFile, c.name, k, got, c.want[k]))
			}
		}
	}
	note := "none of them GitHub's own rendering, because AF_DISPATCH is not set here and CI is what sets it"
	if carriedSet {
		note = fmt.Sprintf("including the one this job carries, %q", carried)
	}
	return problems, fmt.Sprintf("the plan step read %d dispatches as intended, %s", len(cases), note)
}
