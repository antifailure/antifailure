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
	res, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	found := res.Findings
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
	res, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	found := res.Findings
	if len(found) != 3 {
		t.Fatalf("found %d, want 3: %+v", len(found), found)
	}
}

func TestNeverCarriesTheValue(t *testing.T) {
	// The report goes into the log of the job that found it. Quoting the
	// credential there would publish it a second time.
	secret := fakeKey("sk_live_", 24)
	root := tree(t, map[string]string{"a.env": "K=" + secret})
	res, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	found := res.Findings
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
	res, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("a clean tree reported %d credentials: %+v", len(res.Findings), res.Findings)
	}
	// The other half of a clean answer, and the half that used to be missing.
	// "Nothing found" only means "nothing is there" when the scan says how
	// much it read and that it could read all of it.
	if res.Files != 3 {
		t.Fatalf("read %d files, want 3", res.Files)
	}
	if len(res.Unreadable) != 0 {
		t.Fatalf("a readable tree reported unreadable paths: %v", res.Unreadable)
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
	res, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("a skipped directory was scanned: %+v", res.Findings)
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
	res, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("a file past the size bound was read: %+v", res.Findings)
	}
	// Counted as skipped by policy rather than as a failure to look, because
	// the bound is a decision. main prints it rather than refusing on it.
	if res.TooLarge != 1 {
		t.Fatalf("TooLarge is %d, want 1", res.TooLarge)
	}
	if len(res.Unreadable) != 0 {
		t.Fatalf("the size bound was reported as a path it could not read: %v", res.Unreadable)
	}
}

func TestAMissingRootIsNotASilentPass(t *testing.T) {
	// THIS TEST USED TO ASSERT THE OPPOSITE OF ITS NAME. It required err to be
	// nil and the findings to be empty for a root that is not there, and its
	// closing comment said "scanrepo trusts its argument". So the suite carried
	// a green tick under a name promising the hole was closed, and the
	// assertion held it open.
	//
	// WalkDir on a path that is not there calls the callback once with the
	// error and nothing else, so the honest answer is neither a finding nor a
	// clean tree: it is that nothing was read.
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	res, err := scan(missing)
	if err != nil {
		t.Fatalf("the walk should record the error rather than return it, got %v", err)
	}
	if res.Files != 0 {
		t.Fatalf("read %d files under a root that does not exist", res.Files)
	}
	if len(res.Unreadable) != 1 {
		t.Fatalf("a root that does not exist produced %d unreadable paths, want 1: %v",
			len(res.Unreadable), res.Unreadable)
	}
	if !strings.Contains(res.Unreadable[0], "does-not-exist") {
		t.Errorf("the unreadable path does not name the root: %q", res.Unreadable[0])
	}
}

func TestReadingNothingIsRefusedRatherThanReportedClean(t *testing.T) {
	// The verdict main reaches, rather than the shape scan returns. A tree
	// with no files in it is not a tree with no credentials in it, and the
	// difference is the whole reason this file was rewritten.
	res, err := scan(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 || res.Files != 0 {
		t.Fatalf("an empty directory: %d findings, %d files", len(res.Findings), res.Files)
	}
	if verdict(res) != refused {
		t.Fatalf("an empty tree reached verdict %v, want refused", verdict(res))
	}
}

func TestADirectoryItCannotEnterIsNotACleanTree(t *testing.T) {
	// The case that matters in a real repository: the walk reaches a directory
	// it cannot open, and every file under it goes unscanned. Reporting that
	// as clean is the failure this whole file is about.
	if os.Geteuid() == 0 {
		// Root reads a 0000 directory, so the case cannot be built here.
		// Loudly, rather than as a quiet skip: this is the assertion that
		// matters most and a run without it has not made it.
		if os.Getenv("CI") != "" {
			t.Fatal("running as root, so the unreadable directory case cannot be built and was NOT checked")
		}
		t.Skip("running as root: chmod 000 does not stop a read")
	}
	root := t.TempDir()
	closed := filepath.Join(root, "closed")
	if err := os.MkdirAll(closed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(closed, "creds.env"),
		[]byte("K="+fakeKey("sk_live_", 24)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "readme.md"), []byte("nothing here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(closed, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(closed, 0o755) })

	res, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	// The credential inside is genuinely not found, which is exactly why the
	// verdict must not be clean.
	if len(res.Findings) != 0 {
		t.Fatalf("the unreadable directory was read after all: %+v", res.Findings)
	}
	if res.Files != 1 {
		t.Fatalf("read %d files, want the 1 readable one", res.Files)
	}
	if len(res.Unreadable) == 0 {
		t.Fatal("a directory the scan could not enter was not recorded, so its contents " +
			"went unscanned and the tree would be reported clean")
	}
	if verdict(res) != refused {
		t.Fatalf("a tree with an unreadable directory reached verdict %v, want refused", verdict(res))
	}
}

func TestAReadableTreeStillPasses(t *testing.T) {
	// The other direction. A gate loosened until it stops complaining is the
	// same defect wearing the opposite sign, so the clean case is pinned here
	// against the same verdict function.
	root := tree(t, map[string]string{
		"main.go":  "package main\n\nfunc main() {}\n",
		"doc/a.md": "The key looks like sk_live_ followed by the rest.\n",
	})
	res, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if verdict(res) != allowed {
		t.Fatalf("a clean readable tree reached verdict %v, want allowed: %+v", verdict(res), res)
	}
}

func TestAFileItCannotOpenIsNotACleanTree(t *testing.T) {
	// The directory case above is stopped by WalkDir, which reports the
	// unreadable directory through the callback's error. A file that lists
	// fine and will not open fails later, in the read, and that was a
	// separate `return nil` with a separate silence.
	if os.Geteuid() == 0 {
		if os.Getenv("CI") != "" {
			t.Fatal("running as root, so the unreadable file case cannot be built and was NOT checked")
		}
		t.Skip("running as root: chmod 000 does not stop a read")
	}
	root := tree(t, map[string]string{
		"readme.md":  "nothing here\n",
		"secret.env": "K=" + fakeKey("sk_live_", 24),
	})
	if err := os.Chmod(filepath.Join(root, "secret.env"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "secret.env"), 0o600) })

	res, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("the unreadable file was read after all: %+v", res.Findings)
	}
	if len(res.Unreadable) != 1 {
		t.Fatalf("a file that would not open produced %d unreadable paths, want 1: %v",
			len(res.Unreadable), res.Unreadable)
	}
	if verdict(res) != refused {
		t.Fatalf("a tree with a file that would not open reached verdict %v, want refused", verdict(res))
	}
}
