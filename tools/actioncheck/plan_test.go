package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The step as customers run it, one dispatch per case, against the real
// action.yml. Each case is its own subtest so a break names the dispatch it
// broke rather than the first one to fail.
func TestTheActionsPlanStepReadsEveryDispatchACallerSends(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("jq is not on PATH and the plan step needs it. This is not a skip: nothing ran")
	}
	script, err := planScript(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range planCases() {
		t.Run(c.name, func(t *testing.T) {
			exit, out, log, err := runPlan(script, c)
			if err != nil {
				t.Fatalf("could not run the plan step: %v", err)
			}
			if exit != c.exit {
				t.Fatalf("the plan step exited %d, want %d, for AF_DISPATCH=%q, so a customer's "+
					"run ends here before af starts:\n%s", exit, c.exit, c.dispatch, log)
			}
			keys := make([]string, 0, len(c.want))
			for k := range c.want {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if got, ok := out[k]; !ok || got != c.want[k] {
					t.Errorf("the plan step wrote %s=%q, want %q, for AF_DISPATCH=%q:\n%s",
						k, got, c.want[k], c.dispatch, log)
				}
			}
		})
	}
}

// The rendering the cases rely on, pinned to the runner's source rather than
// assumed: the pull request value is exactly two characters, and a dispatch
// carries newlines and indentation, which the step must not care about.
func TestTheRenderingMatchesTheRunnersToJSON(t *testing.T) {
	if got := asRunnerWrites(); got != "{}" {
		t.Errorf("an object with no members renders %q, want {}", got)
	}
	want := "{\n  \"command\": \"load\",\n  \"duration\": \"60s\"\n}"
	if got := asRunnerWrites("command", "load", "duration", "60s"); got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}
	if got := consoleDispatch(map[string]string{"command": "up"}); strings.Count(got, "\n  \"") != len(dispatchInputs) {
		t.Errorf("the console's dispatch does not carry every input the example declares:\n%s", got)
	}
}

func TestThePlanGateAcceptsTheRealAction(t *testing.T) {
	problems, said := planProblems(filepath.Join("..", ".."), "", false)
	if len(problems) != 0 {
		t.Fatalf("the plan step misread a dispatch:\n%s", strings.Join(problems, "\n"))
	}
	if !strings.Contains(said, "AF_DISPATCH is not set here") {
		t.Errorf("a run with no carried dispatch did not say GitHub's rendering was not among them: %q", said)
	}
}

// The line that broke every customer's pull request, put back into a copy of
// the real action. The gate has to refuse it on the value a pull request sends.
func TestThePlanGateRefusesTheDefaultThatAppendedABrace(t *testing.T) {
	root := tree(t)
	edit(t, root, actionFile, `dispatch="${AF_DISPATCH:-}"`, `dispatch="${AF_DISPATCH:-{}}"`)
	problems, _ := planProblems(root, "", false)
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "for a pull request, where toJSON(inputs) renders an object with no members") ||
		!strings.Contains(joined, "Unmatched") {
		t.Fatalf("the gate did not refuse the default that turns {} into {}}:\n%s", joined)
	}
}

func TestThePlanGateRunsTheDispatchThisJobCarries(t *testing.T) {
	problems, said := planProblems(filepath.Join("..", ".."), asRunnerWrites(), true)
	if len(problems) != 0 {
		t.Fatalf("a carried {} was refused:\n%s", strings.Join(problems, "\n"))
	}
	if !strings.Contains(said, `including the one this job carries, "{}"`) {
		t.Errorf("the gate did not say which carried dispatch it ran: %q", said)
	}
	problems, _ = planProblems(filepath.Join("..", ".."), `{"command":"deploy"}`, true)
	if !strings.Contains(strings.Join(problems, "\n"), "for the dispatch this job carries") {
		t.Fatalf("a carried dispatch the step refuses did not fail the gate: %q", problems)
	}
}

// CI sets AF_DISPATCH on the very step that runs this gate. If the typed cases
// inherited it, the unset case would quietly test GitHub's value instead, and a
// carried dispatch the step refuses would fail cases that are not about it.
func TestThePlanGateNeverLetsTheCarriedDispatchStandInForACase(t *testing.T) {
	t.Setenv("AF_DISPATCH", `{"command":"deploy"}`)
	problems, _ := planProblems(filepath.Join("..", ".."), "", false)
	if len(problems) != 0 {
		t.Fatalf("a dispatch in this process's environment changed what the typed cases ran:\n%s",
			strings.Join(problems, "\n"))
	}
}

// Without jq the step cannot run, and a gate that cannot run must say so and
// fail rather than report the step as fine.
func TestThePlanGateFailsWhenItCannotRunTheStep(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(bash, filepath.Join(bin, "bash")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	problems, _ := planProblems(filepath.Join("..", ".."), "", false)
	if !strings.Contains(strings.Join(problems, "\n"), "because jq is not on PATH") {
		t.Fatalf("a gate that could not run the step did not fail: %q", problems)
	}
}

// The carried dispatch is only GitHub's rendering if CI hands it over. The step
// that runs this command has to set AF_DISPATCH from the same expression the
// example passes, or the gate quietly runs only the values typed above.
func TestCIHandsTheGateGitHubsOwnRendering(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var ci struct {
		Jobs map[string]struct {
			Steps []struct {
				Run string         `yaml:"run"`
				Env map[string]any `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(body, &ci); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, j := range ci.Jobs {
		for _, s := range j.Steps {
			if strings.TrimSpace(s.Run) != "go run ./tools/actioncheck ." {
				continue
			}
			found++
			if got, _ := s.Env["AF_DISPATCH"].(string); got != "${{ toJSON(inputs) }}" {
				t.Errorf("the CI step running actioncheck sets AF_DISPATCH to %q, want "+
					"${{ toJSON(inputs) }}, the expression examples/github-workflow.yml passes", got)
			}
		}
	}
	if found != 1 {
		t.Fatalf("found %d CI steps running actioncheck, want exactly one", found)
	}
}
