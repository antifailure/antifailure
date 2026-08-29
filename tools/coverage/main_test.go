package main

import (
	"os"
	"path/filepath"
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
