package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A gate nobody has proved can fail is a gate that passes everything the day it
// breaks. This one decides whether a credential ships, and it had no tests.
//
// Every fake key here is assembled at run time rather than written out, for the
// reason in the package comment: a fixture that looks like a key is a
// repository that fails this check.

func fakeKey(prefix string, tailLen int) string {
	return prefix + strings.Repeat("A1b2C3d4", (tailLen/8)+1)[:tailLen]
}

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFindsACredentialInTheTree(t *testing.T) {
	root := tree(t, map[string]string{
		"config/settings.ts": "export const key = '" + fakeKey("sk_live_", 24) + "'\n",
	})
	found, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("found %d credentials, want 1: %+v", len(found), found)
	}
	if found[0].Provider != "Stripe secret key" {
		t.Errorf("provider is %q", found[0].Provider)
	}
	if !strings.HasSuffix(found[0].Path, "settings.ts") {
		t.Errorf("path is %q", found[0].Path)
	}
}

func TestFindsSeveralKindsAtOnce(t *testing.T) {
	// One finding would be enough to fail the build, but a scan that stopped
	// at the first would leave the rest for somebody to discover one CI run at
	// a time.
	root := tree(t, map[string]string{
		"a.env":     "STRIPE=" + fakeKey("sk_live_", 24),
		"b/c.json":  `{"gh": "` + fakeKey("ghp_", 36) + `"}`,
		"d/e/f.yml": "neon: " + fakeKey("napi_", 30),
	})
	found, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 3 {
		t.Fatalf("found %d, want 3: %+v", len(found), found)
	}
}

func TestNeverCarriesTheValue(t *testing.T) {
	// The report goes into the log of the job that found it. Quoting the
	// credential there would publish it a second time.
	secret := fakeKey("sk_live_", 24)
	root := tree(t, map[string]string{"a.env": "K=" + secret})
	found, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("found %d, want 1", len(found))
	}
	for _, field := range []string{found[0].Path, found[0].Provider, found[0].Prefix} {
		if strings.Contains(field, secret[len("sk_live_"):]) {
			t.Fatalf("the finding carries the credential: %q", field)
		}
	}
}

func TestIsQuietOnACleanTree(t *testing.T) {
	root := tree(t, map[string]string{
		"main.go":  "package main\n\nfunc main() {}\n",
		"note.md":  "The key looks like sk_live_ followed by the rest.\n",
		"urls.txt": "https://example.com/ac/path\n",
	})
	found, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("a clean tree reported %d credentials: %+v", len(found), found)
	}
}

func TestSkipsTheDirectoriesItSaysItSkips(t *testing.T) {
	// Documented behaviour, pinned. A dependency's own test fixtures are not
	// this repository's credentials, and scanning them makes the check noisy
	// enough that somebody removes it.
	root := tree(t, map[string]string{
		"node_modules/pkg/fixture.js": "const k = '" + fakeKey("sk_live_", 24) + "'",
		"dist/bundle.js":              "var k='" + fakeKey("ghp_", 36) + "'",
	})
	found, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("a skipped directory was scanned: %+v", found)
	}
}

func TestIgnoresAFileTooLargeToBeSource(t *testing.T) {
	root := t.TempDir()
	big := filepath.Join(root, "huge.bin")
	body := make([]byte, maxFile+1)
	for i := range body {
		body[i] = 'x'
	}
	copy(body, []byte(fakeKey("sk_live_", 24)))
	if err := os.WriteFile(big, body, 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("a file past the size bound was read: %+v", found)
	}
}

func TestAMissingRootIsNotASilentPass(t *testing.T) {
	// WalkDir on a path that is not there reports nothing to walk. A check
	// pointed at the wrong tree must not be indistinguishable from a clean
	// one, so this pins which of the two happens today.
	found, err := scan(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("expected the walk to swallow the error, got %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("unexpected findings: %+v", found)
	}
	// Documented consequence: scanrepo trusts its argument. CI passes the
	// repository root explicitly, and this test exists so that the day
	// somebody changes that, they read this.
}
