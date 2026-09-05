package runnerpath

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A runner directory that cannot resolve its own declared dependencies is not
// a runner, and this is the one place that says so.
//
// The question was answered in two places that never compared answers.
// `af runner check` read ~/.antifailure/runner and reported every dependency
// against node_modules, which is thorough and was pointed at the wrong tree: a
// run resolves its runner with ToRun, and ToRun offers the checkout's own
// runner/ BEFORE Home. On a fresh checkout that directory is source with no
// node_modules, so on 2026-09-05 the walkthrough ran `af runner install`, ran
// `af runner check`, was told the runner was ready, and then died inside the
// next command with
//
//	Cannot find package 'playwright' imported from
//	  /home/runner/work/antifailure/antifailure/runner/src/browser.ts
//
// Both commands were honest about the directory they looked at. They looked at
// different directories, and neither said which. install.sh already carries
// half of this rule, in shell, for exactly one path: it deletes $PREFIX/runner
// when it has no node_modules, because "af test finds it before it finds
// anything else". That rule belongs to the engine and to every path the search
// offers, not to one path a shell script happens to know about.

// State is what could be learned about one runner directory without running it.
//
// Proven unrunnable and unknown are kept apart on purpose. A manifest that
// cannot be parsed is not a broken runner, it is a runner nothing could be
// said about, and reporting the two the same way is how a check starts
// answering ok about things it never examined.
type State struct {
	// Dir is the directory that was inspected, whether or not it holds a runner.
	Dir string
	// Entry is Dir/src/main.ts when a runner is here, and empty when none is.
	Entry string
	// Node is the version range the runner's manifest requires, empty when the
	// manifest declares none or could not be read.
	Node string
	// Declared is how many dependencies the manifest names.
	Declared int
	// Missing names the declared dependencies with nothing under node_modules,
	// sorted, so a report of them reads the same twice.
	Missing []string
	// NoModules reports that node_modules itself is absent.
	NoModules bool
	// Pinned reports that package-lock.json sits beside the manifest, which is
	// what makes two people installing one release get one tree.
	Pinned bool
	// Undetermined says why the dependencies could not be read, and is empty
	// when they could.
	Undetermined string
	// InCheckout says this directory was found at or above the manifest,
	// inside the checkout, rather than beside the af binary.
	//
	// It decides whether being passed over is worth telling anybody about. A
	// release installs its runner SOURCE at $PREFIX/share/antifailure/runner
	// and expects af runner install to resolve it elsewhere, so that copy has
	// no node_modules on every machine, for ever, by design. Reporting it as
	// passed over would put a warning on every af runner check anybody ever
	// runs. A runner inside the checkout is different: it is the copy somebody
	// may be editing, and going past it silently is how their edits do nothing.
	InCheckout bool
}

// Exists reports whether this directory holds a runner at all.
func (s State) Exists() bool { return s.Entry != "" }

// Blocked reports whether this runner was PROVEN unable to run.
//
// Only a proof counts. A runner whose manifest could not be read is not
// blocked here, because a search that skipped every directory it could not
// understand would silently pick a further one for a reason nobody could see.
// A runner declaring no dependencies at all is not blocked by an absent
// node_modules either, because it needs none.
func (s State) Blocked() bool {
	if !s.Exists() || s.Undetermined != "" || s.Declared == 0 {
		return false
	}
	return s.NoModules || len(s.Missing) > 0
}

// Why names what Blocked found, in a sentence that follows the directory.
// Empty when nothing was proven, so a caller cannot print a reason that does
// not exist.
func (s State) Why() string {
	if !s.Blocked() {
		return ""
	}
	if s.NoModules {
		return "has no node_modules"
	}
	return "is missing " + strings.Join(s.Missing, ", ")
}

// Inspect reads one runner directory. It runs nothing.
func Inspect(dir string) State {
	s := State{Dir: dir}
	entry := filepath.Join(dir, "src", "main.ts")
	if _, err := os.Stat(entry); err != nil {
		return s
	}
	s.Entry = entry

	if _, err := os.Stat(filepath.Join(dir, "package-lock.json")); err == nil {
		s.Pinned = true
	}

	blob, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		s.Undetermined = "package.json could not be read"
		return s
	}
	var m struct {
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(blob, &m); err != nil {
		s.Undetermined = "package.json could not be parsed"
		return s
	}
	s.Node = m.Engines.Node
	s.Declared = len(m.Dependencies)

	modules := filepath.Join(dir, "node_modules")
	if _, err := os.Stat(modules); err != nil {
		s.NoModules = true
		return s
	}
	for name := range m.Dependencies {
		// filepath.Join handles the scoped @scope/name form, which is two
		// directory levels on disk rather than one.
		if _, err := os.Stat(filepath.Join(modules, filepath.FromSlash(name))); err != nil {
			s.Missing = append(s.Missing, name)
		}
	}
	sort.Strings(s.Missing)
	return s
}

// Choice is which runner a run would use and what it went past to get there.
type Choice struct {
	// Runner is the one that would run. Runner.Exists() is false when none of
	// the candidates held a runner that could.
	Runner State
	// PassedOver holds the runners nearer than Runner that this could PROVE
	// would not run, nearest first.
	//
	// It is returned rather than discarded because a run that quietly used a
	// different runner from the nearest one has to be able to say so. A
	// contributor editing runner/src whose node_modules is not installed would
	// otherwise watch their edits do nothing.
	//
	// It holds every one of them, including the release's own source beside
	// the binary, so a refusal can name a concrete tree. Only the InCheckout
	// ones are worth REPORTING when a runner was found; see State.InCheckout.
	PassedOver []State
	// Looked is every directory considered, in order, so a refusal can name
	// them all.
	Looked []string
}

// InCheckout returns the passed over runners that are worth reporting: the
// ones inside the checkout, which somebody may be editing. The release's own
// source beside the binary is expected to have no node_modules and reporting
// it would be a warning nobody can act on and everybody sees.
func (c Choice) InCheckout() []State {
	var out []State
	for _, s := range c.PassedOver {
		if s.InCheckout {
			out = append(out, s)
		}
	}
	return out
}

// Choose is the runner a run rooted at dir would use: the nearest candidate
// that was not proven unable to run.
//
// Nearest-that-works rather than nearest. The nearest rule sent every run in
// this checkout at runner/, which on a fresh clone is source with no
// dependencies, and made `af runner install` pointless: it populates Home, and
// Home was never reached.
func Choose(dir string) Choice {
	inside := map[string]bool{}
	for _, d := range inCheckout(dir) {
		inside[d] = true
	}
	c := Choice{Looked: ToRun(dir)}
	for _, d := range c.Looked {
		s := Inspect(d)
		s.InCheckout = inside[d]
		if !s.Exists() {
			continue
		}
		if s.Blocked() {
			c.PassedOver = append(c.PassedOver, s)
			continue
		}
		c.Runner = s
		return c
	}
	return c
}
