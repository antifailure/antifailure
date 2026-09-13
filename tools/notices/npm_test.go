package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pkgIn writes one installed package under root/node_modules.
func pkgIn(t *testing.T, root, name, version, declared string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, "node_modules", filepath.FromSlash(name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"name": name, "version": version}
	if declared != "" {
		manifest["license"] = declared
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	for f, body := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestALicenceTextBeatsTheLicenceAPackageDeclares(t *testing.T) {
	root := t.TempDir()
	// The posthog-node shape: declared MIT, and the licence file carries two.
	pkgIn(t, root, "posthog-shaped", "1.0.0", "MIT", map[string]string{
		"LICENSE": fixture(t, "Apache-2.0.txt") + "\n\n" + fixture(t, "MIT.txt"),
	})
	got, err := installed(root)
	if err != nil {
		t.Fatalf("installed: %v", err)
	}
	if len(got) != 1 || got[0].Licence != "Apache-2.0 AND MIT" || got[0].Declared {
		t.Fatalf("got %+v; the text carries two licences and the declaration names one", got)
	}
}

func TestAPackageWithNoLicenceFileIsAttributedFromAKnownDeclarationAndSaysSo(t *testing.T) {
	root := t.TempDir()
	pkgIn(t, root, "drizzle-shaped", "0.1.0", "Apache-2.0", nil)
	pkgIn(t, root, "@scope/either", "2.0.0", "(MIT OR Apache-2.0)", nil)
	got, err := installed(root)
	if err != nil {
		t.Fatalf("installed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v, want both packages", got)
	}
	for _, p := range got {
		if !p.Declared {
			t.Errorf("%s was attributed from a declaration and does not say so", p.Name)
		}
	}
}

func TestADeclarationThatIsNotALicenceIdentifierFails(t *testing.T) {
	root := t.TempDir()
	pkgIn(t, root, "see-elsewhere", "1.0.0", "SEE LICENSE IN LICENSE.md", nil)
	_, err := installed(root)
	if err == nil || !strings.Contains(err.Error(), "see-elsewhere@1.0.0") {
		t.Fatalf("a package with no licence file and no usable declaration was attributed: %v", err)
	}
}

func TestALicenceFileNothingRecognisesFailsEvenWhenTheDeclarationLooksFine(t *testing.T) {
	root := t.TempDir()
	pkgIn(t, root, "closed-text", "1.0.0", "MIT", map[string]string{"LICENSE": "All rights reserved."})
	_, err := installed(root)
	if err == nil || !strings.Contains(err.Error(), "closed-text@1.0.0") || !strings.Contains(err.Error(), "LICENSE") {
		t.Fatalf("a licence text the matcher does not know was waved through on its declaration: %v", err)
	}
}

func TestEveryPackageThatFailsIsNamedAndNotOnlyTheFirst(t *testing.T) {
	root := t.TempDir()
	pkgIn(t, root, "first-bad", "1.0.0", "Proprietary", nil)
	pkgIn(t, root, "second-bad", "1.0.0", "Proprietary", nil)
	_, err := installed(root)
	for _, want := range []string{"first-bad", "second-bad"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not name %s: %v", want, err)
		}
	}
}

func TestAWorkspaceLinkAndOurOwnPackagesAreNotAttributed(t *testing.T) {
	root := t.TempDir()
	pkgIn(t, root, "third-party", "1.0.0", "MIT", map[string]string{"LICENSE": fixture(t, "MIT.txt")})
	own := filepath.Join(t.TempDir(), "apps", "api")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(own, "package.json"), []byte(`{"name":"@antifailure/api","license":"SEE LICENSE IN ../../LICENSE.md"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "@antifailure"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(own, filepath.Join(root, "node_modules", "@antifailure", "api")); err != nil {
		t.Fatal(err)
	}
	pkgIn(t, root, "@antifailure/copied", "1.0.0", "SEE LICENSE IN ../../LICENSE.md", nil)
	got, err := installed(root)
	if err != nil {
		t.Fatalf("our own packages made the install fail: %v", err)
	}
	if len(got) != 1 || got[0].Name != "third-party" {
		t.Errorf("got %+v; only the third party package belongs in a third party notice", got)
	}
}

func TestANestedCopyIsAttributedOnceAndADifferentVersionIsNot(t *testing.T) {
	root := t.TempDir()
	mit := map[string]string{"LICENSE": fixture(t, "MIT.txt")}
	pkgIn(t, root, "outer", "1.0.0", "MIT", mit)
	pkgIn(t, root, "shared", "1.0.0", "MIT", mit)
	pkgIn(t, filepath.Join(root, "node_modules", "outer"), "shared", "1.0.0", "MIT", mit)
	pkgIn(t, filepath.Join(root, "node_modules", "outer"), "@scope/pinned", "2.0.0", "MIT", mit)
	pkgIn(t, root, "@scope/pinned", "3.0.0", "MIT", mit)
	got, err := installed(root)
	if err != nil {
		t.Fatalf("installed: %v", err)
	}
	var names []string
	for _, p := range got {
		names = append(names, p.Name+"@"+p.Version)
	}
	want := "@scope/pinned@2.0.0 @scope/pinned@3.0.0 outer@1.0.0 shared@1.0.0"
	if strings.Join(names, " ") != want {
		t.Errorf("attributed %v, want %s", names, want)
	}
}

func TestANoticeAPackageShipsIsCarried(t *testing.T) {
	root := t.TempDir()
	pkgIn(t, root, "noted", "1.0.0", "Apache-2.0", map[string]string{
		"LICENSE": fixture(t, "Apache-2.0.txt"),
		"NOTICE":  "noted\nCopyright 2024 Somebody\n",
	})
	got, err := installed(root)
	if err != nil {
		t.Fatalf("installed: %v", err)
	}
	if len(got) != 1 || len(got[0].Notices) != 1 || !strings.Contains(got[0].Notices[0].text, "Copyright 2024 Somebody") {
		t.Errorf("the NOTICE the package ships was not carried: %+v", got)
	}
}

func TestAnInstallWithNoNodeModulesIsAFailure(t *testing.T) {
	if _, err := installed(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no node_modules") {
		t.Fatalf("an install that produced nothing was read as one with nothing to attribute: %v", err)
	}
}
