package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stream builds a govulncheck-shaped JSON stream. The real output is a sequence
// of concatenated objects rather than an array, which is the detail worth
// pinning down: a decoder written against an array silently reads nothing.
func stream(parts ...string) []byte {
	return []byte(strings.Join(parts, "\n"))
}

func osvMsg(id, summary string) string {
	return `{"osv":{"id":"` + id + `","summary":"` + summary + `"}}`
}

// calledMsg is a finding govulncheck considers reachable: the innermost frame
// names a function.
func calledMsg(id, module, fn string) string {
	return `{"finding":{"osv":"` + id + `","trace":[{"module":"` + module + `","package":"p","function":"` + fn + `"},{"module":"ours","package":"q","function":"Caller"}]}}`
}

// requiredMsg is a finding with no function on the innermost frame, which is how
// govulncheck reports "you require a module that has a vulnerability somewhere
// in it" as opposed to "your code calls it".
func requiredMsg(id, module string) string {
	return `{"finding":{"osv":"` + id + `","trace":[{"module":"` + module + `"}]}}`
}

func TestOnlyCalledFindingsCount(t *testing.T) {
	findings, summaries, err := parse(stream(
		osvMsg("GO-1", "reachable one"),
		calledMsg("GO-1", "example.com/a", "Boom"),
		osvMsg("GO-2", "merely required"),
		requiredMsg("GO-2", "example.com/b"),
	))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want only the called one", len(findings))
	}
	if findings[0].OSV != "GO-1" {
		t.Errorf("kept %s, want GO-1", findings[0].OSV)
	}
	if summaries["GO-2"] != "merely required" {
		t.Errorf("summaries should still carry every advisory, got %q", summaries["GO-2"])
	}
}

// The module we key on must be where the vulnerability lives, not where the
// call starts. Keying on the outermost frame would make every entry in the
// policy file name our own module, which distinguishes nothing.
func TestTheModuleIsWhereTheVulnerabilityLivesNotWhereTheCallStarts(t *testing.T) {
	findings, _, err := parse(stream(calledMsg("GO-1", "example.com/vulnerable", "Boom")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := findings[0].vulnerableModule(); got != "example.com/vulnerable" {
		t.Errorf("vulnerableModule = %q, want the innermost frame", got)
	}
}

func policyWith(entries ...allowEntry) *policy {
	p := &policy{Allow: entries}
	for i := range p.Allow {
		t, err := time.Parse(dateLayout, p.Allow[i].Expires)
		if err != nil {
			panic(err)
		}
		p.Allow[i].expires = t
	}
	return p
}

func decideErr(t *testing.T, findings []*finding, pol *policy, now string) (string, error) {
	t.Helper()
	at, err := time.Parse(dateLayout, now)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	e := decideAt(findings, map[string]string{}, pol, &out, at)
	return out.String(), e
}

// Rule 1. The default is to fail.
func TestAReachableVulnerabilityWithNoEntryFails(t *testing.T) {
	findings, _, _ := parse(stream(calledMsg("GO-1", "example.com/a", "Boom")))

	out, err := decideErr(t, findings, policyWith(), "2026-01-01")
	if err == nil {
		t.Fatal("a reachable vulnerability with no entry must fail the gate")
	}
	if !strings.Contains(err.Error(), "GO-1") {
		t.Errorf("the failure must name the advisory, got %q", err)
	}
	if !strings.Contains(out, "REACHABLE") {
		t.Errorf("report should mark it REACHABLE, got %q", out)
	}
}

func TestAnAcceptedVulnerabilityPasses(t *testing.T) {
	findings, _, _ := parse(stream(calledMsg("GO-1", "example.com/a", "Boom")))

	_, err := decideErr(t, findings, policyWith(allowEntry{
		ID: "GO-1", Module: "example.com/a", Reason: "daemon side", Expires: "2026-12-31",
	}), "2026-01-01")
	if err != nil {
		t.Fatalf("an accepted, unexpired, matching entry should pass: %v", err)
	}
}

// Accepting an advisory for one module path is not accepting it for another.
// The Moby advisories cover three module paths at once and only one of them has
// a fix, so this distinction is the difference between a considered exception
// and a blanket one.
func TestAcceptingOneModulePathDoesNotAcceptAnother(t *testing.T) {
	findings, _, _ := parse(stream(calledMsg("GO-1", "example.com/other", "Boom")))

	_, err := decideErr(t, findings, policyWith(allowEntry{
		ID: "GO-1", Module: "example.com/a", Reason: "r", Expires: "2026-12-31",
	}), "2026-01-01")
	if err == nil {
		t.Fatal("an entry for a different module path must not cover this finding")
	}
}

// Rule 2. An accepted risk has a shelf life.
func TestAnExpiredEntryFailsEvenThoughItMatches(t *testing.T) {
	findings, _, _ := parse(stream(calledMsg("GO-1", "example.com/a", "Boom")))

	out, err := decideErr(t, findings, policyWith(allowEntry{
		ID: "GO-1", Module: "example.com/a", Reason: "r", Expires: "2026-01-01",
	}), "2026-06-01")
	if err == nil {
		t.Fatal("an expired entry must fail so the decision gets reread")
	}
	if !strings.Contains(out, "EXPIRED") {
		t.Errorf("report should mark it EXPIRED, got %q", out)
	}
}

func TestAnEntryIsGoodThroughItsExpiryDay(t *testing.T) {
	findings, _, _ := parse(stream(calledMsg("GO-1", "example.com/a", "Boom")))

	if _, err := decideErr(t, findings, policyWith(allowEntry{
		ID: "GO-1", Module: "example.com/a", Reason: "r", Expires: "2026-06-01",
	}), "2026-06-01"); err != nil {
		t.Fatalf("the expiry date itself should still be covered: %v", err)
	}
}

// Rule 3. The one that matters most: a suppression that suppresses nothing is
// dead code, and dead code in a security policy reads as protection that is not
// there.
func TestAnEntryThatMatchesNothingFails(t *testing.T) {
	out, err := decideErr(t, nil, policyWith(allowEntry{
		ID: "GO-1", Module: "example.com/a", Reason: "r", Expires: "2026-12-31",
	}), "2026-01-01")
	if err == nil {
		t.Fatal("an entry matching no finding must fail rather than linger")
	}
	if !strings.Contains(out, "STALE") {
		t.Errorf("report should mark it STALE, got %q", out)
	}
}

func TestNoFindingsAndNoEntriesPasses(t *testing.T) {
	if _, err := decideErr(t, nil, policyWith(), "2026-01-01"); err != nil {
		t.Fatalf("a clean scan with an empty policy should pass: %v", err)
	}
}

func writePolicy(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, policyFile)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// An entry with no stated reason is not a decision, it is a mute button.
func TestAnEntryWithoutAReasonIsRejected(t *testing.T) {
	path := writePolicy(t, "allow:\n  - id: GO-1\n    module: m\n    expires: 2026-12-31\n")
	_, err := loadPolicy(path)
	if err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("want a complaint about the missing reason, got %v", err)
	}
}

func TestAnEntryWithoutAnExpiryIsRejected(t *testing.T) {
	path := writePolicy(t, "allow:\n  - id: GO-1\n    module: m\n    reason: because\n")
	_, err := loadPolicy(path)
	if err == nil || !strings.Contains(err.Error(), "expires") {
		t.Fatalf("want a complaint about the missing expiry, got %v", err)
	}
}

func TestAMisspelledExpiryIsRejectedRatherThanTreatedAsForever(t *testing.T) {
	path := writePolicy(t, "allow:\n  - id: GO-1\n    module: m\n    reason: because\n    expires: 31-12-2026\n")
	_, err := loadPolicy(path)
	if err == nil || !strings.Contains(err.Error(), "2006-01-02") {
		t.Fatalf("want a complaint naming the accepted date layout, got %v", err)
	}
}

// A typo in a field name must not silently become an entry with no expiry.
func TestAnUnknownFieldIsRejected(t *testing.T) {
	path := writePolicy(t, "allow:\n  - id: GO-1\n    module: m\n    reason: because\n    expires: 2026-12-31\n    untilthedate: 2027-01-01\n")
	if _, err := loadPolicy(path); err == nil {
		t.Fatal("an unknown field must be rejected, not ignored")
	}
}

// No file means no accepted risks, which is the right starting state for a
// repository that has none.
func TestAMissingPolicyFileIsNotAnError(t *testing.T) {
	pol, err := loadPolicy(filepath.Join(t.TempDir(), policyFile))
	if err != nil {
		t.Fatalf("a missing policy file should mean an empty policy: %v", err)
	}
	if len(pol.Allow) != 0 {
		t.Errorf("want no entries, got %d", len(pol.Allow))
	}
}

// The policy actually committed to this repository must itself be valid. This
// catches a hand edit that a unit test over temp files never would.
func TestTheCommittedPolicyIsValid(t *testing.T) {
	pol, err := loadPolicy(filepath.Join("..", "..", policyFile))
	if err != nil {
		t.Fatalf("the committed %s does not load: %v", policyFile, err)
	}
	for _, e := range pol.Allow {
		if len(strings.Fields(e.Reason)) < 20 {
			t.Errorf("%s in %s: the reason is %d words, which is too short to be a real justification",
				e.ID, e.Module, len(strings.Fields(e.Reason)))
		}
	}
}

// The module list is discovered by walking, so adding a module cannot silently
// leave it unscanned.

func TestFindsEveryModuleIncludingOnesOutsideTheWorkspace(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"engine", "tools", "ee/engine", "web/node_modules/pkg", "engine/testdata/broken"} {
		full := filepath.Join(root, filepath.FromSlash(dir))
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, "go.mod"), []byte("module x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	mods, err := discoverModules(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ee/engine", "engine", "tools"}
	if len(mods) != len(want) {
		t.Fatalf("got %v, want %v", mods, want)
	}
	for i := range want {
		if mods[i] != want[i] {
			t.Errorf("module %d is %q, want %q", i, mods[i], want[i])
		}
	}
}

// Finding nothing must never read as a clean scan.
func TestARepositoryWithNoModulesIsAnError(t *testing.T) {
	if _, err := discoverModules(t.TempDir()); err == nil {
		t.Fatal("finding no module must be an error, not an empty scan")
	}
}

// The real repository must yield the modules it actually has, ee/engine
// included: it holds shipping enterprise code and sits outside go.work, so it
// is the one module a workspace-based list would quietly miss.
func TestTheRealRepositoryYieldsItsModules(t *testing.T) {
	mods, err := discoverModules(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("discovering modules: %v", err)
	}
	found := map[string]bool{}
	for _, m := range mods {
		found[m] = true
	}
	for _, want := range []string{"engine", "tools", "ee/engine"} {
		if !found[want] {
			t.Errorf("discovered %v, which is missing %q", mods, want)
		}
	}
}
