package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The shape below is copied out of a real `npm audit --package-lock-only
// --json` run, against a lockfile pinning request 2.88.2, rather than written
// from the documentation. It is here because the union in `via` is the thing a
// hand-written fixture gets wrong: the entry for `request` mixes one advisory
// object with four plain strings naming the packages it pulls in, and a decoder
// that assumed an array of objects would turn each of those strings into a
// finding with an empty identifier.
const realReport = `{
  "auditReportVersion": 2,
  "vulnerabilities": {
    "form-data": {
      "name": "form-data",
      "severity": "critical",
      "via": [
        {"source": 1106834, "name": "form-data", "dependency": "form-data",
         "title": "form-data uses unsafe random function", "severity": "critical",
         "url": "https://github.com/advisories/GHSA-fjxv-7rqg-78g4", "range": "<2.5.4"}
      ],
      "range": "<2.5.4",
      "fixAvailable": false
    },
    "request": {
      "name": "request",
      "severity": "critical",
      "via": [
        {"source": 1096727, "name": "request", "dependency": "request",
         "title": "Server-Side Request Forgery in Request", "severity": "moderate",
         "url": "https://github.com/advisories/GHSA-p8p7-x288-28g6", "range": "<=2.88.2"},
        "form-data",
        "qs",
        "tough-cookie",
        "uuid"
      ],
      "range": "*",
      "fixAvailable": false
    }
  },
  "metadata": {"vulnerabilities": {"info": 0, "low": 0, "moderate": 0, "high": 0, "critical": 2, "total": 2}}
}`

func TestAStringInViaIsNotAFinding(t *testing.T) {
	found, err := parse([]byte(realReport), "probe", nil, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("want 2 findings, got %d: %+v", len(found), found)
	}
	for _, f := range found {
		if f.ID == "" || f.Package == "" {
			t.Fatalf("a via element produced an empty finding, which is the union being decoded as one type: %+v", f)
		}
		if !strings.HasPrefix(f.ID, "GHSA-") {
			t.Fatalf("want a GHSA identifier, got %q", f.ID)
		}
	}
}

// A clean tree is the state this repository is in today, and a gate that cannot
// report it is a gate that has to fail to say anything.
func TestACleanReportIsNoFindings(t *testing.T) {
	found, err := parse([]byte(`{"auditReportVersion":2,"vulnerabilities":{},"metadata":{}}`), "web", nil, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("want no findings, got %+v", found)
	}
}

// npm exits non-zero both when it finds advisories and when it cannot run, so
// the two have to be told apart by whether a report came back. ENOLOCK is the
// one this repository will actually meet: runner/ has dependencies and no
// lockfile.
func TestNpmRefusingIsAnErrorAndNotAnEmptyReport(t *testing.T) {
	const enolock = `{"error":{"code":"ENOLOCK","summary":"This command requires an existing lockfile.","detail":"Try creating one first"}}`
	_, err := parse([]byte(enolock), "runner", nil, "")
	if err == nil {
		t.Fatal("npm refusing to run was read as a clean tree")
	}
	if !strings.Contains(err.Error(), "ENOLOCK") || !strings.Contains(err.Error(), "runner") {
		t.Fatalf("the error should name the project and npm's code: %v", err)
	}
}

func TestOutputThatIsNotAReportFails(t *testing.T) {
	if _, err := parse([]byte("npm ERR! network timeout\n"), "web", os.ErrDeadlineExceeded, "network timeout"); err == nil {
		t.Fatal("a non-report was accepted")
	}
}

// The three rules, each proved able to fail. A gate nobody has watched go red
// is a gate nobody knows the shape of.
func TestAnAdvisoryWithNoEntryFails(t *testing.T) {
	out, err := decideNow(t, findingsFrom(t, realReport), &policy{})
	if err == nil {
		t.Fatal("two unaccepted advisories passed")
	}
	if !strings.Contains(out, "ADVISORY") || !strings.Contains(out, "GHSA-fjxv-7rqg-78g4") {
		t.Fatalf("the report should name the advisory:\n%s", out)
	}
	if !strings.Contains(err.Error(), ".npmaudit.yaml") {
		t.Fatalf("the error should say where a decision goes: %v", err)
	}
}

func TestAnAcceptedAdvisoryPasses(t *testing.T) {
	pol := &policy{Allow: []allowEntry{
		{ID: "GHSA-fjxv-7rqg-78g4", Package: "form-data", Reason: "test", expires: farFuture()},
		{ID: "GHSA-p8p7-x288-28g6", Package: "request", Reason: "test", expires: farFuture()},
	}}
	if _, err := decideNow(t, findingsFrom(t, realReport), pol); err != nil {
		t.Fatalf("accepted advisories still failed: %v", err)
	}
}

func TestAnEntryPastItsExpiryFails(t *testing.T) {
	pol := &policy{Allow: []allowEntry{
		{ID: "GHSA-fjxv-7rqg-78g4", Package: "form-data", Reason: "test", Expires: "2020-01-01", expires: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "GHSA-p8p7-x288-28g6", Package: "request", Reason: "test", expires: farFuture()},
	}}
	var out bytes.Buffer
	err := decideAt(findingsFrom(t, realReport), []string{"probe"}, nil, pol, &out, time.Now())
	if err == nil {
		t.Fatal("an expired acceptance passed")
	}
	if !strings.Contains(out.String(), "EXPIRED") || !strings.Contains(err.Error(), "expired on 2020-01-01") {
		t.Fatalf("want the expiry named:\n%s\n%v", out.String(), err)
	}
}

func TestAnEntryThatMatchesNothingFails(t *testing.T) {
	pol := &policy{Allow: []allowEntry{
		{ID: "GHSA-0000-0000-0000", Package: "nothing", Reason: "test", expires: farFuture()},
	}}
	var out bytes.Buffer
	err := decideAt(nil, []string{"web"}, nil, pol, &out, time.Now())
	if err == nil {
		t.Fatal("a suppression that suppresses nothing passed")
	}
	if !strings.Contains(out.String(), "STALE") {
		t.Fatalf("want STALE in the report:\n%s", out.String())
	}
}

// A project with no lockfile is not covered, and the summary must say so rather
// than counting a smaller repository than the one being checked.
func TestAProjectWithNoLockfileIsNamedRatherThanIgnored(t *testing.T) {
	var out bytes.Buffer
	if err := decideAt(nil, []string{"web"}, []string{"runner"}, &policy{}, &out, time.Now()); err != nil {
		t.Fatalf("an uncovered project should not fail the gate: %v", err)
	}
	if !strings.Contains(out.String(), "UNCHECKED  runner") {
		t.Fatalf("runner was skipped silently:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "1 not covered") {
		t.Fatalf("the summary hid the uncovered project:\n%s", out.String())
	}
}

func TestDiscoveryFindsLockedAndUnlockedProjects(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "web", "package.json"), `{"dependencies":{"a":"1"}}`)
	write(t, filepath.Join(root, "web", "package-lock.json"), `{}`)
	write(t, filepath.Join(root, "runner", "package.json"), `{"dependencies":{"playwright":"^1"}}`)
	write(t, filepath.Join(root, "empty", "package.json"), `{"name":"empty"}`)
	// Somebody else's tree, and a directory this repository never audits.
	write(t, filepath.Join(root, "web", "node_modules", "dep", "package.json"), `{"dependencies":{"b":"1"}}`)

	locked, unlocked, err := discoverProjects(root)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(locked) != 1 || locked[0] != "web" {
		t.Fatalf("locked: %v", locked)
	}
	if len(unlocked) != 1 || unlocked[0] != "runner" {
		t.Fatalf("unlocked: %v, and a package.json with no dependencies is neither covered nor uncovered", unlocked)
	}
}

// The false alarm that the first version of discovery produced, pinned so it
// cannot come back. Seven of this repository's lockfile-less projects are npm
// workspaces resolved by a lockfile one or two directories up.
func TestAWorkspaceMemberIsCoveredByTheRootLockfile(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "web", "package.json"), `{"workspaces":["packages/*"],"dependencies":{"a":"1"}}`)
	write(t, filepath.Join(root, "web", "package-lock.json"), `{}`)
	write(t, filepath.Join(root, "web", "packages", "db", "package.json"), `{"dependencies":{"postgres":"^3"}}`)
	write(t, filepath.Join(root, "web", "packages", "policy", "package.json"), `{"dependencies":{"zod":"^3"}}`)

	locked, unlocked, err := discoverProjects(root)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(locked) != 1 || locked[0] != "web" {
		t.Fatalf("locked: %v", locked)
	}
	if len(unlocked) != 0 {
		t.Fatalf("a workspace member was reported as uncovered, which is how a real one stops being read: %v", unlocked)
	}
}

// A policy file is a security decision, so the fields that make it one are
// required rather than optional.
func TestAPolicyEntryMustCarryAReasonAndAnExpiry(t *testing.T) {
	for name, body := range map[string]string{
		"no reason":  "allow:\n  - id: GHSA-a\n    package: p\n    expires: 2030-01-01\n",
		"no expiry":  "allow:\n  - id: GHSA-a\n    package: p\n    reason: because\n",
		"no package": "allow:\n  - id: GHSA-a\n    reason: because\n    expires: 2030-01-01\n",
		"bad date":   "allow:\n  - id: GHSA-a\n    package: p\n    reason: because\n    expires: soon\n",
		"unknown key": "allow:\n  - id: GHSA-a\n    package: p\n    reason: because\n" +
			"    expires: 2030-01-01\n    severity: high\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), policyFile)
			write(t, path, body)
			if _, err := loadPolicy(path); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

func TestNoPolicyFileMeansNoAcceptedRisks(t *testing.T) {
	pol, err := loadPolicy(filepath.Join(t.TempDir(), policyFile))
	if err != nil {
		t.Fatalf("an absent policy file is the correct starting state: %v", err)
	}
	if len(pol.Allow) != 0 {
		t.Fatalf("want no entries, got %d", len(pol.Allow))
	}
}

// The file this repository actually ships has to parse, or the gate fails for a
// reason that has nothing to do with a dependency.
func TestTheCommittedPolicyFileParses(t *testing.T) {
	path := filepath.Join("..", "..", policyFile)
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no %s at the repository root", policyFile)
	}
	if _, err := loadPolicy(path); err != nil {
		t.Fatalf("%s does not parse: %v", policyFile, err)
	}
}

func findingsFrom(t *testing.T, body string) []*finding {
	t.Helper()
	found, err := parse([]byte(body), "probe", nil, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return found
}

func decideNow(t *testing.T, found []*finding, pol *policy) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := decideAt(found, []string{"probe"}, nil, pol, &out, time.Now())
	return out.String(), err
}

func farFuture() time.Time { return time.Now().AddDate(10, 0, 0) }

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The fixture above must stay a real npm report rather than drifting into
// something only this test can read.
func TestTheFixtureIsShapedLikeNpmsOutput(t *testing.T) {
	var rep report
	if err := json.Unmarshal([]byte(realReport), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.AuditReportVersion == nil || *rep.AuditReportVersion != 2 {
		t.Fatal("npm audit --json carries auditReportVersion 2")
	}
	if len(rep.Vulnerabilities["request"].Via) != 5 {
		t.Fatal("the request entry is the union case and needs all five elements")
	}
}

// The exact shape that took main red: npm returns an error object and fills in
// none of it. The old format string printed three empty strings, so the whole
// output was "npmaudit: api: npm audit refused:" with nothing after the colon.
func TestAnEmptyRefusalSaysThatItIsEmpty(t *testing.T) {
	const blank = `{"error":{"code":"","summary":"","detail":""}}`
	_, err := parse([]byte(blank), "api", nil, "")
	if err == nil {
		t.Fatal("an empty refusal was read as a clean tree")
	}
	msg := err.Error()
	if !strings.Contains(msg, "gave no code, summary or detail") {
		t.Errorf("an empty refusal must say it is empty: %q", msg)
	}
	if strings.HasSuffix(strings.SplitN(msg, "\n", 2)[0], ":") {
		t.Errorf("the first line trails off after a colon: %q", msg)
	}
}

// The one path that fires when the registry is unreachable is the one that used
// to discard the diagnostics. Both the process's exit error and its stderr have
// to reach the reader.
func TestARefusalCarriesStderrAndTheExitError(t *testing.T) {
	const blank = `{"error":{"code":"","summary":"","detail":""}}`
	_, err := parse([]byte(blank), "api", os.ErrDeadlineExceeded, "npm ERR! network request to https://registry.npmjs.org failed")
	if err == nil {
		t.Fatal("an empty refusal was read as a clean tree")
	}
	msg := err.Error()
	if !strings.Contains(msg, "registry.npmjs.org") {
		t.Errorf("stderr did not reach the reader: %q", msg)
	}
	if !strings.Contains(msg, os.ErrDeadlineExceeded.Error()) {
		t.Errorf("the exit error did not reach the reader: %q", msg)
	}
}

// Silence from npm and silence from this tool look identical in a log, and the
// second one is the defect being fixed, so an empty stderr is stated rather
// than left out.
func TestAnEmptyStderrIsStatedRatherThanOmitted(t *testing.T) {
	const blank = `{"error":{"code":"","summary":"","detail":""}}`
	_, err := parse([]byte(blank), "api", nil, "   ")
	if err == nil {
		t.Fatal("an empty refusal was read as a clean tree")
	}
	if !strings.Contains(err.Error(), "npm wrote nothing to stderr") {
		t.Errorf("an empty stderr must be stated: %q", err.Error())
	}
}

// A refusal that DOES say why keeps saying it. Fixing the empty case must not
// bury the populated one.
func TestAPopulatedRefusalStillNamesItsCause(t *testing.T) {
	const enolock = `{"error":{"code":"ENOLOCK","summary":"This command requires an existing lockfile.","detail":"Try creating one first"}}`
	_, err := parse([]byte(enolock), "runner", nil, "")
	if err == nil {
		t.Fatal("npm refusing to run was read as a clean tree")
	}
	msg := err.Error()
	for _, want := range []string{"ENOLOCK", "existing lockfile", "Try creating one first", "runner"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal dropped %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "gave no code") {
		t.Errorf("a populated refusal was reported as empty: %s", msg)
	}
}
