package main

import (
	"os"
	"path/filepath"
	"testing"
)

// This command had no test, and both scans it grew here were written because it
// had been passing while two shipped codes sat marked as not existing yet.
//
// AF-EE-004 is thrown by the control plane, which is TypeScript, and the walk
// returned early on anything that was not a .go file. AF-EE-010 is built with
// Sprintf in ee/engine/policyenforce and never names its constant, and the walk
// counted identifiers. Both were on the enterprise pages, both had passing
// tests, and neither was on the reference page a user searches after seeing one.

func TestAStringLiteralCountsOnlyWhenItIsTheError(t *testing.T) {
	for _, c := range []struct {
		name string
		lit  string
		want string
	}{
		{
			// ee/engine/policyenforce/policyenforce.go, the defect that
			// motivated reading literals at all.
			name: "an assembled error message",
			lit:  `"AF-EE-010: organization policy %s refuses this environment: %s"`,
			want: "AF-EE-010",
		},
		{
			name: "the bare code, which a test asserts on",
			lit:  `"AF-EE-010"`,
			want: "AF-EE-010",
		},
		{
			// engine/chaos/controlplane_test.go. Somebody writing about an
			// error is not somebody returning one, and treating this as a use
			// would have cleared the marker on a code nothing can raise.
			name: "a sentence that mentions a code",
			lit:  `"the events were lost at exit rather than kept, so AF-CPL-003's promise is not kept"`,
			want: "",
		},
		{
			name: "a sentence that ends on a code",
			lit:  `"the caller needs the backup path for AF-RUN-011"`,
			want: "",
		},
		{
			name: "a format string that mentions a code",
			lit:  `"the sentence under AF-MSK-007 names %s and it does not"`,
			want: "",
		},
		{
			name: "a raw string holding an error",
			lit:  "`AF-DB-011: the branch is gone`",
			want: "AF-DB-011",
		},
		{
			name: "no code at all",
			lit:  `"nothing to see"`,
			want: "",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := stringLiteralCode(c.lit); got != c.want {
				t.Errorf("stringLiteralCode(%s) = %q, want %q", c.lit, got, c.want)
			}
		})
	}
}

func TestConstantForMatchesTheGeneratedName(t *testing.T) {
	if got := constantFor("AF-EE-004"); got != "AFEE004" {
		t.Errorf("constantFor(AF-EE-004) = %q, want AFEE004; the catalog map is keyed by the "+
			"constant and a wrong key silently finds nothing", got)
	}
}

func TestATypeScriptSourceCodeCountsAndATestOrACommentDoesNot(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "ee", "web", "sso", "src", "provision.ts"),
		"// The seat limit is the first. AF-EE-999 says the license covers N seats.\n"+
			"throw new ProvisioningRefused('AF-EE-004', 'all seats are in use')\n")
	write(t, filepath.Join(root, "web", "apps", "api", "test", "metrics.test.ts"),
		"payload: { code: 'AF-DB-001', detail: 'a sentence written for a terminal' }\n")
	write(t, filepath.Join(root, "web", "apps", "api", "src", "ingest.test.ts"),
		"assert.equal(body.code, 'AF-CP-002')\n")
	write(t, filepath.Join(root, "web", "node_modules", "junk", "index.ts"),
		"const c = 'AF-RUN-001'\n")

	used, err := usedInTypeScript(root)
	if err != nil {
		t.Fatal(err)
	}

	if !used["AFEE004"] {
		t.Error("a code thrown from a source file was not counted, which is how AF-EE-004 " +
			"shipped marked as a feature this version does not have")
	}
	if used["AFEE999"] {
		t.Error("a code named in a comment was counted as returned; the pattern is supposed " +
			"to require the quotes")
	}
	if used["AFDB001"] {
		t.Error("a code invented by a test fixture was counted. metrics-endpoint.test.ts posts " +
			"AF-DB-001 to prove the control plane groups by whatever code arrives, and the " +
			"control plane cannot raise it")
	}
	if used["AFCP002"] {
		t.Error("a .test.ts file beside the source was counted")
	}
	if used["AFRUN001"] {
		t.Error("node_modules was walked")
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
