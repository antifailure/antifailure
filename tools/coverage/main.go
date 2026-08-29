// Command coverage enforces the per-package coverage thresholds in the build
// plan's C.5, which is gate G4.
//
// G4 was the one mandatory gate with nothing behind it. docs/plan/STATUS.md
// carried a coverage column quoting a percentage per package, and no command
// computed those numbers and no gate compared them to a threshold, so they were
// a claim rather than a measurement. Several were already below the floor the
// plan sets, which is what an unenforced number does over time.
//
// It reads a coverage profile rather than running the tests itself. Producing
// the profile needs the whole suite, a Docker daemon and a Postgres, and takes
// long enough that a tool which insisted on running it would be a tool nobody
// ran. The justfile recipe produces the profile and then calls this.
//
// Statement coverage, not branch coverage. See thresholds.yaml for why, and for
// what that costs.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type tier struct {
	Mode     string   `yaml:"mode"`
	Min      float64  `yaml:"min"`
	Packages []string `yaml:"packages"`
}

type config struct {
	Strict  tier `yaml:"strict"`
	High    tier `yaml:"high"`
	Default tier `yaml:"default"`
}

// counts is the covered and total statement count for one package.
type counts struct {
	covered, total int
}

func (c counts) percent() float64 {
	if c.total == 0 {
		return 100
	}
	return float64(c.covered) / float64(c.total) * 100
}

func main() {
	profile := flag.String("profile", "coverage.out", "coverage profile to read")
	rules := flag.String("thresholds", "", "thresholds file (default beside this tool)")
	module := flag.String("module", "github.com/antifailure/antifailure/engine", "module path to strip")
	flag.Parse()

	if *rules == "" {
		*rules = filepath.Join("tools", "coverage", "thresholds.yaml")
	}

	cfg, err := readConfig(*rules)
	if err != nil {
		fail("the thresholds could not be read: %v", err)
	}
	byPkg, err := readProfile(*profile, *module)
	if err != nil {
		fail("the coverage profile could not be read: %v", err)
	}
	if len(byPkg) == 0 {
		fail("%s covers no packages, so there is nothing to check. "+
			"Produce it with `just coverage-profile` rather than by hand.", *profile)
	}

	report(cfg, byPkg)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "coverage: "+format+"\n", args...)
	os.Exit(1)
}

func readConfig(path string) (config, error) {
	body, err := os.ReadFile(path) //nolint:gosec // a path this tool is given
	if err != nil {
		return config{}, err
	}
	var c config
	if err := yaml.Unmarshal(body, &c); err != nil {
		return config{}, err
	}
	if c.Default.Min <= 0 {
		return config{}, fmt.Errorf("the default tier has no minimum, so every package would pass")
	}
	return c, nil
}

// readProfile sums a Go coverage profile into per-package statement counts.
//
// The format is one line per block: `path/to/file.go:12.34,56.78 3 1`, where
// the middle number is how many statements the block holds and the last is how
// many times it ran. Summing statements rather than counting lines is what
// makes the number comparable to `go tool cover -func`.
//
// A block appearing more than once, which happens with -coverpkg when several
// test binaries cover the same file, is counted once and treated as covered if
// any binary reached it. Adding the repeats would inflate the total and hide a
// gap behind a bigger denominator.
func readProfile(path, module string) (map[string]counts, error) {
	f, err := os.Open(path) //nolint:gosec // a path this tool is given
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	type block struct{ pkg, id string }
	seen := map[block]bool{}
	stmts := map[block]int{}
	hit := map[block]bool{}

	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		colon := strings.LastIndex(line, ":")
		if colon < 0 {
			continue
		}
		file, rest := line[:colon], line[colon+1:]
		fields := strings.Fields(rest)
		if len(fields) != 3 {
			continue
		}
		n, err1 := strconv.Atoi(fields[1])
		count, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil {
			continue
		}
		pkg := packageOf(file, module)
		if pkg == "" {
			continue
		}
		b := block{pkg: pkg, id: file + ":" + fields[0]}
		if !seen[b] {
			seen[b] = true
			stmts[b] = n
		}
		if count > 0 {
			hit[b] = true
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}

	out := map[string]counts{}
	for b, n := range stmts {
		c := out[b.pkg]
		c.total += n
		if hit[b] {
			c.covered += n
		}
		out[b.pkg] = c
	}
	return out, nil
}

// packageOf turns a profile's file path into a package path relative to the
// module, and returns "" for anything outside it.
func packageOf(file, module string) string {
	if !strings.HasPrefix(file, module+"/") {
		return ""
	}
	rel := strings.TrimPrefix(file, module+"/")
	dir := filepath.Dir(rel)
	if dir == "." {
		return ""
	}
	return dir
}

// thresholdFor returns the minimum for a package and the tier that set it.
//
// Longest prefix wins, so a rule naming internal/db also governs
// internal/db/docker unless something names that directly. Without it every
// nested package would silently fall to the default tier, which is how a
// package that is supposed to be held at 100 quietly ends up held at 85.
func thresholdFor(cfg config, pkg string) (float64, string) {
	best, name, bestLen := cfg.Default.Min, "default", -1
	for _, candidate := range []struct {
		name string
		rule tier
	}{{"strict", cfg.Strict}, {"high", cfg.High}} {
		for _, p := range candidate.rule.Packages {
			if pkg != p && !strings.HasPrefix(pkg, p+"/") {
				continue
			}
			if len(p) > bestLen {
				best, name, bestLen = candidate.rule.Min, candidate.name, len(p)
			}
		}
	}
	return best, name
}

func report(cfg config, byPkg map[string]counts) {
	pkgs := make([]string, 0, len(byPkg))
	for p := range byPkg {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)

	type shortfall struct {
		pkg   string
		got   float64
		want  float64
		tier  string
		lines int
	}
	var below []shortfall
	checked := 0

	for _, p := range pkgs {
		c := byPkg[p]
		// A package with no statements at all is a directory of declarations.
		// There is nothing to cover and nothing to claim.
		if c.total == 0 {
			continue
		}
		checked++
		want, tier := thresholdFor(cfg, p)
		got := c.percent()
		// Rounded before comparing, so a package the report prints as 85.0
		// does not fail a check for 85.
		if round1(got) < want {
			below = append(below, shortfall{p, got, want, tier, c.total - c.covered})
		}
	}

	fmt.Printf("coverage: %d packages measured against C.5\n", checked)
	if len(below) == 0 {
		fmt.Println("coverage: every package meets its threshold")
		return
	}

	sort.Slice(below, func(i, j int) bool {
		di := below[i].want - below[i].got
		dj := below[j].want - below[j].got
		return di > dj
	})

	fmt.Printf("coverage: %d below the threshold the plan sets\n\n", len(below))
	fmt.Printf("  %-42s %8s %8s  %-8s %s\n", "package", "have", "want", "tier", "uncovered")
	for _, b := range below {
		fmt.Printf("  %-42s %7.1f%% %7.0f%%  %-8s %d statements\n",
			b.pkg, b.got, b.want, b.tier, b.lines)
	}
	fmt.Println()
	fmt.Println("  Raising a threshold to make this pass is the one repair that is not allowed.")
	fmt.Println("  `go tool cover -html=coverage.out` shows which statements never ran.")
	os.Exit(1)
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}
