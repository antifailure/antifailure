package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A built console, laid out by hand, so what exported reads can be chosen: the
// shape of a real Next export with source maps on, measured from a build of the
// console before this was written, and no network or build in the test.

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// builtConsole returns a build directory and the console's own tracked files.
func builtConsole(t *testing.T) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	nm := filepath.Join(dir, "node_modules")
	writeFile(t, filepath.Join(nm, "next", "package.json"), `{"name":"next","version":"16.3.4","license":"MIT"}`)
	writeFile(t, filepath.Join(nm, "next", "license.md"), fixture(t, "MIT.txt"))
	// Next's vendored React: a manifest with no version and no licence field.
	writeFile(t, filepath.Join(nm, "next", "dist", "compiled", "react", "package.json"), `{"name":"react-builtin"}`)
	writeFile(t, filepath.Join(nm, "next", "dist", "compiled", "react", "LICENSE"), fixture(t, "MIT.txt"))
	writeFile(t, filepath.Join(nm, "geist", "package.json"), `{"name":"geist","version":"1.7.2","license":"SIL OPEN FONT LICENSE"}`)
	writeFile(t, filepath.Join(nm, "geist", "LICENSE.txt"), fixture(t, "OFL-1.1.txt"))
	writeFile(t, filepath.Join(nm, "geist", "dist", "fonts", "Geist-Variable.woff2"), "font bytes")

	writeFile(t, filepath.Join(dir, "out", "_next", "static", "chunks", "a.js"), "js")
	writeFile(t, filepath.Join(dir, "out", "_next", "static", "chunks", "a.js.map"),
		`{"version":3,"sources":["turbopack:///[project]/app/page.tsx",`+
			`"turbopack:///[project]/node_modules/next/dist/client/index.js",`+
			`"turbopack:///[project]/node_modules/next/dist/compiled/react/cjs/react.production.js"]}`)
	writeFile(t, filepath.Join(dir, "out", "_next", "static", "media", "Geist_Variable.p.woff2"), "font bytes")
	writeFile(t, filepath.Join(dir, "out", "_next", "static", "media", "icon.svg"), "<svg/>")

	ownIcon := filepath.Join(t.TempDir(), "app", "icon.svg")
	writeFile(t, ownIcon, "<svg/>")
	return dir, []string{ownIcon}
}

func TestTheExportIsAttributedFromItsSourceMapsAndItsAssetsBytes(t *testing.T) {
	dir, own := builtConsole(t)
	pkgs, err := exported(dir, own)
	if err != nil {
		t.Fatalf("exported: %v", err)
	}
	var got []string
	for _, p := range pkgs {
		got = append(got, p.Name+" | "+p.Version+" | "+p.Licence)
	}
	want := []string{
		"geist | 1.7.2 | OFL-1.1",
		"next | 16.3.4 | MIT",
		"next/dist/compiled/react | vendored in next 16.3.4 | MIT",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("attributed:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestAnAssetIdenticalToNothingKnownFails(t *testing.T) {
	dir, own := builtConsole(t)
	writeFile(t, filepath.Join(dir, "out", "_next", "static", "media", "stranger.png"), "bytes from nowhere")
	_, err := exported(dir, own)
	if err == nil || !strings.Contains(err.Error(), "stranger.png") {
		t.Fatalf("an asset traced to nothing was attributed or ignored: %v", err)
	}
}

func TestTheConsolesOwnAssetIsNotAThirdPartyAndItsAbsenceFromOwnFailsIt(t *testing.T) {
	dir, _ := builtConsole(t)
	// The same build with the console's own icon not among its tracked files:
	// the icon is then identical to nothing known, and must fail.
	if _, err := exported(dir, nil); err == nil || !strings.Contains(err.Error(), "icon.svg") {
		t.Fatalf("an asset matching only the console's own files passed when they were not supplied: %v", err)
	}
}

func TestAVendoredCopyWithNoRecognisableLicenceFails(t *testing.T) {
	dir, own := builtConsole(t)
	writeFile(t, filepath.Join(dir, "node_modules", "next", "dist", "compiled", "react", "LICENSE"), "All rights reserved.")
	_, err := exported(dir, own)
	if err == nil || !strings.Contains(err.Error(), "next/dist/compiled/react") {
		t.Fatalf("a vendored copy with an unrecognised licence was attributed: %v", err)
	}
}

func TestABuildThatWroteNoSourceMapsFails(t *testing.T) {
	dir, own := builtConsole(t)
	if err := os.Remove(filepath.Join(dir, "out", "_next", "static", "chunks", "a.js.map")); err != nil {
		t.Fatal(err)
	}
	if _, err := exported(dir, own); err == nil || !strings.Contains(err.Error(), "no source maps") {
		t.Fatalf("an export with no maps was read as one that bundles nothing: %v", err)
	}
}

func TestMapsThatNameNoPackageFail(t *testing.T) {
	dir, own := builtConsole(t)
	writeFile(t, filepath.Join(dir, "out", "_next", "static", "chunks", "a.js.map"),
		`{"version":3,"sources":["turbopack:///[project]/app/page.tsx"]}`)
	if _, err := exported(dir, own); err == nil || !strings.Contains(err.Error(), "name no package") {
		t.Fatalf("maps naming no package were accepted: %v", err)
	}
}

func TestSourceMapsAreSwitchedOnBesideTheExportLineOrRefused(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "next.config.ts")
	writeFile(t, cfg, "const c = {\n  output: \"export\",\n};\n")
	if err := withSourceMaps(dir); err != nil {
		t.Fatalf("a config with the export line was refused: %v", err)
	}
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `output: "export", productionBrowserSourceMaps: true`) {
		t.Errorf("source maps were not switched on: %s", body)
	}

	bare := t.TempDir()
	writeFile(t, filepath.Join(bare, "next.config.ts"), "const c = {};\n")
	if err := withSourceMaps(bare); err == nil {
		t.Fatal("a config with no export line was patched as if it had one")
	}
}

func TestAnAssetBelongsToTheInnermostPackageHoldingIt(t *testing.T) {
	modules := filepath.Join("x", "node_modules")
	for path, want := range map[string]string{
		filepath.Join(modules, "geist", "dist", "fonts", "a.woff2"):                       filepath.Join(modules, "geist"),
		filepath.Join(modules, "@scope", "pkg", "a.woff2"):                                filepath.Join(modules, "@scope", "pkg"),
		filepath.Join(modules, "outer", "node_modules", "inner", "fonts", "a.woff2"):      filepath.Join(modules, "outer", "node_modules", "inner"),
		filepath.Join(modules, "outer", "node_modules", "@s", "inner", "fonts", "a.woff"): filepath.Join(modules, "outer", "node_modules", "@s", "inner"),
	} {
		if got := packageRoot(modules, path); got != want {
			t.Errorf("packageRoot(%s) = %s, want %s", path, got, want)
		}
	}
}
