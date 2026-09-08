package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mod = "github.com/antifailure/antifailure/engine"

func writeProfile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cover.out")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAProfileSumsStatementsRatherThanCountingLines(t *testing.T) {
	// Three statements covered, one not: 75 percent, not the 50 percent that
	// counting blocks would give.
	p := writeProfile(t, `mode: set
`+mod+`/internal/policy/a.go:1.1,3.2 3 1
`+mod+`/internal/policy/a.go:5.1,6.2 1 0
`)
	got, err := readProfile(p, mod)
	if err != nil {
		t.Fatal(err)
	}
	c := got["internal/policy"]
	if c.covered != 3 || c.total != 4 {
		t.Fatalf("covered/total = %d/%d, want 3/4", c.covered, c.total)
	}
	if c.percent() != 75 {
		t.Fatalf("percent = %v, want 75", c.percent())
	}
}

// -coverpkg makes every test binary report every package, so the same block
// arrives many times. Adding the repeats would inflate the denominator and let
// a package pass on arithmetic rather than on tests.
func TestARepeatedBlockIsCountedOnceAndCoveredIfAnyBinaryReachedIt(t *testing.T) {
	p := writeProfile(t, `mode: set
`+mod+`/internal/policy/a.go:1.1,3.2 3 0
`+mod+`/internal/policy/a.go:1.1,3.2 3 1
`+mod+`/internal/policy/a.go:5.1,6.2 2 0
`+mod+`/internal/policy/a.go:5.1,6.2 2 0
`)
	got, err := readProfile(p, mod)
	if err != nil {
		t.Fatal(err)
	}
	c := got["internal/policy"]
	if c.total != 5 {
		t.Fatalf("total = %d, want 5; the repeat was counted twice", c.total)
	}
	if c.covered != 3 {
		t.Fatalf("covered = %d, want 3; a block one binary reached is covered", c.covered)
	}
}

func TestAnythingOutsideTheModuleIsIgnored(t *testing.T) {
	p := writeProfile(t, `mode: set
`+mod+`/internal/policy/a.go:1.1,2.2 1 1
github.com/somebody/else/x.go:1.1,2.2 9 0
`)
	got, err := readProfile(p, mod)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("packages = %v, want only the module's own", got)
	}
}

// The rule that stops a nested package quietly falling to the default tier.
func TestTheLongestMatchingPrefixSetsTheThreshold(t *testing.T) {
	cfg := config{
		Strict:  tier{Min: 100, Packages: []string{"internal/masking"}},
		High:    tier{Min: 90, Packages: []string{"internal"}},
		Default: tier{Min: 85},
	}
	for _, c := range []struct {
		pkg  string
		want float64
		tier string
	}{
		{"internal/masking", 100, "strict"},
		{"internal/masking/rules", 100, "strict"},
		{"internal/state", 90, "high"},
		{"pkg/provider", 85, "default"},
	} {
		got, name := thresholdFor(cfg, c.pkg)
		if got != c.want || name != c.tier {
			t.Errorf("%s = %v/%s, want %v/%s", c.pkg, got, name, c.want, c.tier)
		}
	}
}

func TestAPackageWithNoStatementsIsFullyCoveredRatherThanZero(t *testing.T) {
	var c counts
	if c.percent() != 100 {
		t.Fatalf("percent = %v, want 100; a package of declarations has nothing to cover",
			c.percent())
	}
}

// The thresholds file that ships has to parse, and has to actually enforce
// something. A default of zero would let every package pass while the gate
// reported success, which is the exact failure this gate exists to end.
func TestTheShippedThresholdsParseAndEnforceSomething(t *testing.T) {
	cfg, err := readConfig(filepath.Join("thresholds.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default.Min != 85 {
		t.Errorf("default = %v, want the plan's 85", cfg.Default.Min)
	}
	if cfg.High.Min != 90 {
		t.Errorf("high = %v, want the plan's 90", cfg.High.Min)
	}
	if cfg.Strict.Min != 100 {
		t.Errorf("strict = %v, want the plan's 100", cfg.Strict.Min)
	}
	// Every package C.5 names at 100 percent.
	for _, p := range []string{
		"internal/masking", "internal/subset", "internal/verify", "internal/policy",
		"internal/journal", "internal/redact", "internal/secrets", "internal/webhook",
	} {
		if got, tier := thresholdFor(cfg, p); got != 100 || tier != "strict" {
			t.Errorf("%s = %v/%s, want 100/strict", p, got, tier)
		}
	}
}

func TestAThresholdsFileWithNoDefaultIsRefused(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.yaml")
	if err := os.WriteFile(p, []byte("strict:\n  min: 100\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfig(p); err == nil {
		t.Fatal("a file with no default was accepted, so every package would pass")
	}
}

// The case the gate could not see. report() builds its package list from the
// PROFILE, so a package the thresholds name and the profile does not carry was
// never iterated, never compared to its floor, and never printed even under
// -all. internal/masking is held at 100 percent because a missed line there is
// a masking failure; if its tests stopped building, it left the profile and
// this gate went on printing a count and exiting 0.
func TestAStrictPackageMissingFromTheProfileIsNamed(t *testing.T) {
	cfg, err := readConfig("thresholds.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// Everything the thresholds name, except the one that fell out.
	byPkg := map[string]counts{}
	for _, tierPkgs := range [][]string{cfg.Strict.Packages, cfg.High.Packages} {
		for _, p := range tierPkgs {
			if p == "internal/masking" {
				continue
			}
			byPkg[p] = counts{covered: 10, total: 10}
		}
	}
	missing := unmeasured(cfg, byPkg)
	if len(missing) != 1 {
		t.Fatalf("unmeasured = %v, want exactly the one package that is not in the profile", missing)
	}
	if !strings.Contains(missing[0], "internal/masking") {
		t.Errorf("the report does not name the missing package: %q", missing[0])
	}
	if !strings.Contains(missing[0], "strict") {
		t.Errorf("the report does not say which tier was left unmeasured: %q", missing[0])
	}
}

// The other direction, so the refusal cannot be satisfied by refusing
// everything. A profile that carries every named package must be accepted.
func TestAProfileCarryingEveryNamedPackageIsAccepted(t *testing.T) {
	cfg, err := readConfig("thresholds.yaml")
	if err != nil {
		t.Fatal(err)
	}
	byPkg := map[string]counts{}
	for _, tierPkgs := range [][]string{cfg.Strict.Packages, cfg.High.Packages} {
		for _, p := range tierPkgs {
			byPkg[p] = counts{covered: 10, total: 10}
		}
	}
	if missing := unmeasured(cfg, byPkg); len(missing) != 0 {
		t.Fatalf("a complete profile was reported as unmeasured: %v", missing)
	}
}

// A tier entry may name a tree rather than a package, which is thresholdFor's
// rule and has to be this one too. Naming only a subpackage must satisfy the
// entry, or the refusal would fire on a correct profile.
func TestASubpackageSatisfiesATreeEntry(t *testing.T) {
	cfg := config{
		Strict:  tier{Min: 100, Packages: []string{"internal/masking"}},
		Default: tier{Min: 85},
	}
	byPkg := map[string]counts{"internal/masking/dialect": {covered: 1, total: 1}}
	if missing := unmeasured(cfg, byPkg); len(missing) != 0 {
		t.Fatalf("a subpackage did not satisfy the tree it sits under: %v", missing)
	}
	if missing := unmeasured(cfg, map[string]counts{"internal/maskingother": {covered: 1, total: 1}}); len(missing) != 1 {
		t.Fatalf("a package that merely shares a prefix satisfied the entry: %v", missing)
	}
}
