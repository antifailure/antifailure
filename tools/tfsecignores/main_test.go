package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// A well formed directive: a rule, an expiry, and a paragraph above it.
const good = `
# The vault is reachable and not readable, and the RBAC assignments in this
# file are the whole list of principals that can read a secret.
#tfsec:ignore:azure-keyvault-specify-network-acl:exp:2027-03-03
default_action = "Allow"
`

func TestParseFileReadsRuleExpiryAndLine(t *testing.T) {
	got, err := parseFile("a.tf", good)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 directive, got %d", len(got))
	}
	d := got[0]
	if d.Rule != "azure-keyvault-specify-network-acl" {
		t.Errorf("rule = %q", d.Rule)
	}
	if d.Expires != "2027-03-03" {
		t.Errorf("expires = %q", d.Expires)
	}
	if d.Line != 4 {
		t.Errorf("line = %d, want 4", d.Line)
	}
}

// Rule 1, first half. tfsec accepts a bare directive; this does not, because a
// suppression with no shelf life is one nobody rereads.
func TestParseFileRefusesADirectiveWithNoExpiry(t *testing.T) {
	_, err := parseFile("a.tf", "# a reason long enough to clear the floor comfortably\n#tfsec:ignore:azure-keyvault-specify-network-acl\n")
	if err == nil {
		t.Fatal("want an error for a directive with no expiry")
	}
	if !strings.Contains(err.Error(), "no expiry") {
		t.Errorf("error should say what is missing, got %v", err)
	}
}

func TestParseFileRefusesAMalformedExpiry(t *testing.T) {
	_, err := parseFile("a.tf", "# a reason long enough to clear the floor comfortably\n#tfsec:ignore:some-rule:exp:next-march\n")
	if err == nil || !strings.Contains(err.Error(), "not a 2006-01-02 date") {
		t.Fatalf("want a date format error, got %v", err)
	}
}

// Rule 1, second half. tfsec has no field for a reason, so the prose beside the
// directive is the only place one can live.
func TestParseFileRefusesADirectiveWithNoReason(t *testing.T) {
	_, err := parseFile("a.tf", "resource \"x\" \"y\" {\n#tfsec:ignore:some-rule:exp:2099-01-01\n}\n")
	if err == nil || !strings.Contains(err.Error(), "no reason") {
		t.Fatalf("want a missing reason error, got %v", err)
	}
}

// A one word comment is not a reason. The floor is low on purpose, and it is
// still a floor.
func TestParseFileRefusesAReasonTooShortToBeOne(t *testing.T) {
	_, err := parseFile("a.tf", "# later\n#tfsec:ignore:some-rule:exp:2099-01-01\n")
	if err == nil || !strings.Contains(err.Error(), "no reason") {
		t.Fatalf("want a missing reason error, got %v", err)
	}
}

// Another directive sitting above this one is not prose about this one, so a
// stack of bare directives under a single paragraph does not launder them all.
func TestParseFileDoesNotCountAnotherDirectiveAsAReason(t *testing.T) {
	src := "# a reason long enough to clear the floor comfortably\n" +
		"#tfsec:ignore:rule-one:exp:2099-01-01\n" +
		"#tfsec:ignore:rule-two:exp:2099-01-01\n"
	_, err := parseFile("a.tf", src)
	if err == nil || !strings.Contains(err.Error(), "rule-two") {
		t.Fatalf("want the second directive refused, got %v", err)
	}
}

// A workspace qualifier is tfsec's, not ours, and an unknown key added upstream
// must not break the parse.
func TestParseFileToleratesOtherDirectiveKeys(t *testing.T) {
	src := "# a reason long enough to clear the floor comfortably\n" +
		"#tfsec:ignore:some-rule:ws:staging:exp:2099-01-01\n"
	got, err := parseFile("a.tf", src)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if len(got) != 1 || got[0].Expires != "2099-01-01" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseDirectivesReadsOnlyTerraformAndSkipsDownloadedModules(t *testing.T) {
	fsys := fstest.MapFS{
		"modules/kv.tf":          {Data: []byte(good)},
		"modules/notes.md":       {Data: []byte("#tfsec:ignore:not-a-rule\n")},
		".terraform/vendor/z.tf": {Data: []byte("#tfsec:ignore:someone-elses-rule\n")},
	}
	got, err := parseDirectives(fsys, "infra/terraform")
	if err != nil {
		t.Fatalf("parseDirectives: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 directive, got %d: %+v", len(got), got)
	}
	if got[0].File != "infra/terraform/modules/kv.tf" {
		t.Errorf("file = %q, want it reported under the scanned prefix", got[0].File)
	}
}

func decideFor(t *testing.T, ds []directive, failing map[string]map[string]bool, now time.Time) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := decideAt(ds, failing, &buf, now)
	return buf.String(), err
}

func live(rule, expires string) directive {
	t, _ := time.Parse(dateLayout, expires)
	return directive{File: "infra/terraform/kv.tf", Line: 4, Rule: rule, Expires: expires, expires: t}
}

var march2026 = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

// THE POSITIVE CONTROL. A checker that refuses everything would pass every
// negative test above and be worthless, so one case has to come back clean.
func TestDecideAcceptsALiveIgnoreThatSuppressedSomething(t *testing.T) {
	ds := []directive{live("azure-keyvault-specify-network-acl", "2027-03-03")}
	failing := map[string]map[string]bool{
		"infra/terraform/kv.tf": {"azure-keyvault-specify-network-acl": true},
	}
	out, err := decideFor(t, ds, failing, march2026)
	if err != nil {
		t.Fatalf("a live, used ignore must pass: %v", err)
	}
	if !strings.Contains(out, "1 ignores, 1 live, 0 expired or stale") {
		t.Errorf("summary = %q", out)
	}
}

func TestDecideFailsAnExpiredIgnore(t *testing.T) {
	ds := []directive{live("azure-keyvault-specify-network-acl", "2025-01-01")}
	failing := map[string]map[string]bool{
		"infra/terraform/kv.tf": {"azure-keyvault-specify-network-acl": true},
	}
	out, err := decideFor(t, ds, failing, march2026)
	if err == nil {
		t.Fatal("want an error for an expired ignore")
	}
	if !strings.Contains(out, "EXPIRED") {
		t.Errorf("report should say EXPIRED, got %q", out)
	}
	// The message has to explain why the finding reappeared, because tfsec's
	// own output will not.
	if !strings.Contains(err.Error(), "expired on 2025-01-01") {
		t.Errorf("error should name the date, got %v", err)
	}
}

// RULE 3, THE ONE TFSEC HAS NO VERSION OF.
func TestDecideFailsAnIgnoreThatSuppressedNothing(t *testing.T) {
	ds := []directive{live("azure-keyvault-specify-network-acl", "2027-03-03")}
	out, err := decideFor(t, ds, map[string]map[string]bool{}, march2026)
	if err == nil {
		t.Fatal("want an error for an ignore that matched nothing")
	}
	if !strings.Contains(out, "STALE") {
		t.Errorf("report should say STALE, got %q", out)
	}
	if !strings.Contains(err.Error(), "protection that is not there") {
		t.Errorf("error should say what a stale suppression costs, got %v", err)
	}
}

// Matching is per file, not per rule. Without this, an ignore left behind in
// one file would be kept alive by a live ignore for the same rule in another,
// which is the quietest possible way for rule 3 to stop working.
func TestDecideDoesNotLetOneFileCoverAnotherFilesIgnore(t *testing.T) {
	ds := []directive{live("azure-keyvault-specify-network-acl", "2027-03-03")}
	failing := map[string]map[string]bool{
		"infra/terraform/other.tf": {"azure-keyvault-specify-network-acl": true},
	}
	if _, err := decideFor(t, ds, failing, march2026); err == nil {
		t.Fatal("a suppression in another file must not cover this one")
	}
}

// tfsec reports both ids and a directive may be written with either.
func TestDecideAcceptsTheShortRuleId(t *testing.T) {
	ds := []directive{live("AVD-AZU-0013", "2027-03-03")}
	failing := map[string]map[string]bool{
		"infra/terraform/kv.tf": {"AVD-AZU-0013": true, "azure-keyvault-specify-network-acl": true},
	}
	if _, err := decideFor(t, ds, failing, march2026); err != nil {
		t.Fatalf("an ignore written with the short id must be recognised: %v", err)
	}
}

// fakeTfsec writes a script that prints what it is told and exits how it is
// told, so the two ways of not knowing can be tested without a real scanner.
func fakeTfsec(t *testing.T, stdout string, code int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake scanner is a shell script")
	}
	path := filepath.Join(t.TempDir(), "tfsec")
	body := "#!/bin/sh\nprintf '%s' " + shellQuote(stdout) + "\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
func itoa(i int) string {
	return strings.TrimSpace(strings.Fields(strings.Repeat(" ", 0) + string(rune('0'+i)))[0])
}

// THE FAILURE THAT STARTED ALL OF THIS. A scanner that did not run must not
// read as a scanner that found nothing.
func TestIgnoredResultsRefusesASilentScanner(t *testing.T) {
	bin := fakeTfsec(t, "", 1)
	_, err := unsuppressedFindings(bin, t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("a scanner that printed nothing must be an error, not an empty result")
	}
	if !strings.Contains(err.Error(), "nothing was scanned") {
		t.Errorf("error should say nothing was scanned, got %v", err)
	}
}

func TestIgnoredResultsRefusesOutputItCannotParse(t *testing.T) {
	bin := fakeTfsec(t, "Error: 403 rate limited\n", 1)
	_, err := unsuppressedFindings(bin, t.TempDir(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "as JSON") {
		t.Fatalf("want a parse error naming the output, got %v", err)
	}
}

// ONLY A FAILING RESULT COUNTS, AND THIS IS THE TEST THAT KEEPS THE TOOL ABLE
// TO SAY NO.
//
// The first version of this asked tfsec --include-ignored which rules it had
// suppressed, which reads like the right question and is not: tfsec marks a
// check ignored whenever a directive NAMES it, even when the check was passing
// and there was nothing to suppress. Pointed at a real tree, that version
// called a made up directive for azure-keyvault-no-purge "live". Status 1 is a
// pass and status 2 is tfsec repeating the directive back; neither is evidence.
func TestUnsuppressedFindingsCountsOnlyFailures(t *testing.T) {
	root := t.TempDir()
	body := `{"results":[
	  {"rule_id":"AVD-AZU-0013","long_id":"acl","status":0,"location":{"filename":"` + root + `/kv.tf"}},
	  {"rule_id":"AVD-AZU-0031","long_id":"purge","status":1,"location":{"filename":"` + root + `/kv.tf"}},
	  {"rule_id":"AVD-AZU-0017","long_id":"exp","status":2,"location":{"filename":"` + root + `/kv.tf"}}
	]}`
	got, err := unsuppressedFindings(fakeTfsec(t, body, 1), root, root)
	if err != nil {
		t.Fatalf("unsuppressedFindings: %v", err)
	}
	if !got["kv.tf"]["acl"] {
		t.Error("a failing rule should be recorded")
	}
	if got["kv.tf"]["purge"] {
		t.Error("a rule that PASSED is not one a directive is suppressing")
	}
	if got["kv.tf"]["exp"] {
		t.Error("a rule tfsec calls ignored is not evidence that anything was suppressed")
	}
}

// The directives this repository actually ships have to satisfy rule 1. This
// is the check that keeps the tool honest about its own tree, and it needs no
// scanner: an expiry and a reason are properties of the text.
func TestThisRepositorysDirectivesCarryAnExpiryAndAReason(t *testing.T) {
	dir := filepath.Join("..", "..", "infra", "terraform")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no %s to read: %v", dir, err)
	}
	got, err := parseDirectives(os.DirFS(dir), "infra/terraform")
	if err != nil {
		t.Fatalf("the tfsec ignores in this repository do not satisfy the policy: %v", err)
	}
	for _, d := range got {
		if d.expires.Before(time.Now()) {
			t.Errorf("%s: the ignore for %s expired on %s", d.where(), d.Rule, d.Expires)
		}
	}
}
